package accrual

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shigabutdinoff/gophermart/internal/money"
	"github.com/shigabutdinoff/gophermart/internal/order"
)

const testNumber = "9278923470"

// newTestServer поднимает подменную систему расчёта на время теста.
func newTestServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	return server
}

// newTestClient направляет клиент на подменную систему расчёта.
func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()

	client, err := New(newTestServer(t, handler).URL)
	require.NoError(t, err)

	return client
}

// recordPath запоминает путь запроса и отвечает готовым телом расчёта.
func recordPath(path *string, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		*path = r.URL.EscapedPath()
		respondJSON(body)(w, r)
	}
}

// respondJSON отвечает готовым телом расчёта.
func respondJSON(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}
}

// orderBody собирает ответ расчёта по номеру теста.
// Пустая сумма означает тело без поля начисления.
func orderBody(status, accrual string) string {
	body := `{"order":"` + testNumber + `","status":"` + status + `"`
	if accrual != "" {
		body += `,"accrual":` + accrual
	}

	return body + "}"
}

func TestClientOrderInfoReturnsResult(t *testing.T) {
	integer := money.Points(50000)
	fractional := money.Points(72998)
	zero := money.Points(0)
	tests := []struct {
		name string
		body string
		want OrderInfo
	}{
		{
			name: "целое начисление",
			body: orderBody("PROCESSED", "500"),
			want: OrderInfo{Number: testNumber, Status: order.StatusProcessed, Accrual: &integer},
		},
		{
			name: "дробное начисление",
			body: orderBody("PROCESSED", "729.98"),
			want: OrderInfo{Number: testNumber, Status: order.StatusProcessed, Accrual: &fractional},
		},
		{
			name: "нулевое начисление",
			body: orderBody("PROCESSED", "0"),
			want: OrderInfo{Number: testNumber, Status: order.StatusProcessed, Accrual: &zero},
		},
		{
			name: "поле начисления отсутствует",
			body: orderBody("PROCESSED", ""),
			want: OrderInfo{Number: testNumber, Status: order.StatusProcessed},
		},
		{
			name: "начисление равно null",
			body: orderBody("PROCESSED", "null"),
			want: OrderInfo{Number: testNumber, Status: order.StatusProcessed},
		},
		{
			name: "расчёт идёт",
			body: orderBody("PROCESSING", "500"),
			want: OrderInfo{Number: testNumber, Status: order.StatusProcessing},
		},
		{
			name: "заказ зарегистрирован",
			body: orderBody("REGISTERED", "500"),
			want: OrderInfo{Number: testNumber, Status: order.StatusProcessing},
		},
		{
			name: "расчёт отклонён",
			body: orderBody("INVALID", "500"),
			want: OrderInfo{Number: testNumber, Status: order.StatusInvalid},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var path string
			client := newTestClient(t, recordPath(&path, test.body))

			info, err := client.OrderInfo(context.Background(), testNumber)

			require.NoError(t, err)
			assert.Equal(t, test.want, info)
			assert.Equal(t, "/api/orders/"+testNumber, path)
		})
	}
}

func TestClientOrderInfoNotRegistered(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	_, err := client.OrderInfo(context.Background(), testNumber)

	require.ErrorIs(t, err, ErrNotRegistered)
}

func TestClientOrderInfoTransformsTooManyRequests(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "42")
		w.WriteHeader(http.StatusTooManyRequests)
	})

	_, err := client.OrderInfo(context.Background(), testNumber)

	var throttled *TooManyRequestsError
	require.ErrorAs(t, err, &throttled)
	assert.Equal(t, 42*time.Second, throttled.RetryAfter)
}

func TestClientOrderInfoLeavesUnnamedRetryAfterToCaller(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	})

	_, err := client.OrderInfo(context.Background(), testNumber)

	var throttled *TooManyRequestsError
	require.ErrorAs(t, err, &throttled)
	assert.Zero(t, throttled.RetryAfter)
	assert.Contains(t, err.Error(), "too many requests")
}

func TestRetryAfter(t *testing.T) {
	// заголовок ставится по указателю, иначе пустое значение
	// неотличимо от его отсутствия
	value := func(header string) *string { return &header }

	tests := []struct {
		name   string
		header *string
		want   time.Duration
	}{
		{name: "секунды", header: value("60"), want: time.Minute},
		{name: "часы", header: value("7200"), want: maxRetryAfter},
		{name: "переполняющее число секунд", header: value("10000000000000"), want: maxRetryAfter},
		{
			// ParseInt на переполнении отдаёт край int64 вместе с ErrRange
			name:   "секунды за пределом int64",
			header: value("99999999999999999999"),
			want:   maxRetryAfter,
		},
		{name: "отрицательные секунды за пределом int64", header: value("-99999999999999999999")},
		{name: "ноль секунд", header: value("0")},
		{name: "отрицательные секунды", header: value("-5")},
		{
			// без среза до умножения секунды переполнились бы ровно в минуту
			name:   "переполняющее отрицательное число",
			header: value("-36028797018963908"),
		},
		{name: "без заголовка"},
		{name: "пустое значение заголовка", header: value("")},
		{name: "заголовок из одних пробелов", header: value("   ")},
		{name: "непонятный заголовок", header: value("позже")},
		{name: "прошедшая HTTP-дата", header: value("Sun, 06 Nov 1994 08:49:37 GMT")},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			header := make(http.Header)
			if test.header != nil {
				header.Set("Retry-After", *test.header)
			}

			assert.Equal(t, test.want, retryAfter(header))
		})
	}
}

// Внутри пузыря часы стоят, поэтому остаток до даты называется точно, а не
// зажимается измерениями до и после вызова.
func TestRetryAfterUsesRemainingTimeUntilHTTPDate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		date := time.Now().Add(2 * time.Minute).UTC()
		header := http.Header{"Retry-After": {date.Format(http.TimeFormat)}}

		assert.Equal(t, 2*time.Minute, retryAfter(header))
	})
}

func TestRetryAfterClampsFutureHTTPDate(t *testing.T) {
	date := time.Now().Add(2 * time.Hour).UTC().Truncate(time.Second)
	header := http.Header{"Retry-After": {date.Format(http.TimeFormat)}}

	assert.Equal(t, maxRetryAfter, retryAfter(header))
}

func TestClientOrderInfoUnexpectedStatusCode(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	_, err := client.OrderInfo(context.Background(), testNumber)

	var unexpected *UnexpectedStatusError
	require.ErrorAs(t, err, &unexpected)
	assert.Equal(t, http.StatusInternalServerError, unexpected.StatusCode)
}

func TestClientOrderInfoErrorsCarryOrderNumber(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{
			name: "заказ не зарегистрирован",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			},
		},
		{
			name: "слишком много запросов",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusTooManyRequests)
			},
		},
		{
			name: "неожиданный код",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			},
		},
		{name: "битое тело", handler: respondJSON(`{"order":`)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := newTestClient(t, test.handler)

			_, err := client.OrderInfo(context.Background(), testNumber)

			require.Error(t, err)
			assert.Contains(t, err.Error(), testNumber)
		})
	}
}

func TestClientOrderInfoRejectsMalformedResponse(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		messagePart string
	}{
		{
			name: "тысячные доли начисления",
			body: orderBody("PROCESSED", "500.555"),
		},
		{
			name: "двоичный хвост начисления",
			body: orderBody("PROCESSED", "729.98000000000002"),
		},
		{
			name: "начисление строкой",
			body: orderBody("PROCESSED", `"500"`),
		},
		{name: "битое тело", body: `{"order":`},
		{
			name:        "чужой номер заказа",
			body:        `{"order":"9278923471","status":"PROCESSED","accrual":500}`,
			messagePart: "9278923471",
		},
		{name: "без номера заказа", body: `{"status":"PROCESSED","accrual":500}`},
		{
			name: "отрицательное начисление",
			body: orderBody("PROCESSED", "-5"),
		},
		{
			name: "непредставимое начисление",
			body: orderBody("PROCESSED", "1e18"),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := newTestClient(t, respondJSON(test.body))

			_, err := client.OrderInfo(context.Background(), testNumber)

			require.ErrorIs(t, err, ErrMalformedResponse)
			if test.messagePart != "" {
				assert.Contains(t, err.Error(), test.messagePart)
			}
		})
	}
}

func TestClientOrderInfoRejectsUnknownStatus(t *testing.T) {
	client := newTestClient(t, respondJSON(orderBody("FINISHED", "")))

	_, err := client.OrderInfo(context.Background(), testNumber)

	require.ErrorIs(t, err, ErrUnknownStatus)
	assert.NotErrorIs(t, err, ErrMalformedResponse)
}

func TestClientOrderInfoRespectsCanceledContext(t *testing.T) {
	client := newTestClient(t, respondJSON(orderBody("PROCESSED", "")))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := client.OrderInfo(ctx, testNumber)

	require.ErrorIs(t, err, context.Canceled)
}

func TestClientOrderInfoDoesNotFollowRedirect(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/api/orders/other", http.StatusFound)
	})

	_, err := client.OrderInfo(context.Background(), testNumber)

	require.Error(t, err)
}

func TestClientOrderInfoEscapesNumber(t *testing.T) {
	var path string
	client := newTestClient(t, recordPath(&path, `{"order":"1/2","status":"PROCESSED"}`))

	_, err := client.OrderInfo(context.Background(), "1/2")

	require.NoError(t, err)
	assert.Equal(t, "/api/orders/1%2F2", path)
}

func TestNewRejectsUnusableAddress(t *testing.T) {
	tests := []struct {
		name    string
		address string
	}{
		{name: "пустой адрес", address: ""},
		{name: "пробелы", address: "   "},
		{name: "только порт", address: ":8080"},
		{name: "опечатка в схеме", address: "htp://localhost:8080"},
		{name: "адрес без хоста", address: "http://"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client, err := New(test.address)

			require.Error(t, err)
			assert.Nil(t, client)
		})
	}
}

func TestNewAddsSchemeToBareAddress(t *testing.T) {
	server := newTestServer(t, respondJSON(orderBody("PROCESSED", "")))

	client, err := New(strings.TrimPrefix(server.URL, "http://"))
	require.NoError(t, err)

	info, err := client.OrderInfo(context.Background(), testNumber)

	require.NoError(t, err)
	assert.Equal(t, order.StatusProcessed, info.Status)
}

func TestNewDropsQueryAndFragmentFromAddress(t *testing.T) {
	tests := []struct {
		name   string
		suffix string
	}{
		{name: "запрос", suffix: "?debug=1"},
		{name: "якорь", suffix: "#accrual"},
		{name: "хвостовой слеш", suffix: "/"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var path string
			server := newTestServer(t, recordPath(&path, orderBody("PROCESSED", "")))

			client, err := New(server.URL + test.suffix)
			require.NoError(t, err)

			_, err = client.OrderInfo(context.Background(), testNumber)

			require.NoError(t, err)
			assert.Equal(t, "/api/orders/"+testNumber, path)
		})
	}
}
