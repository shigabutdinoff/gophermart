package balance

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	domain "github.com/shigabutdinoff/gophermart/internal/balance"
	balancemocks "github.com/shigabutdinoff/gophermart/internal/balance/mocks"
	"github.com/shigabutdinoff/gophermart/internal/handlers/route/message"
	"github.com/shigabutdinoff/gophermart/internal/money"
	"github.com/shigabutdinoff/gophermart/internal/ordernumber"
)

func serveWithdraw(handler http.Handler, body string) *httptest.ResponseRecorder {
	return serveWithdrawAs(handler, "application/json", body)
}

func serveWithdrawAs(handler http.Handler, contentType, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, withdrawPath, strings.NewReader(body))
	request.Header.Set("Content-Type", contentType)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	return response
}

func withdrawReturning(err error, calls *int) Deps {
	return Deps{Withdraw: func(context.Context, string, money.Points, int64) error {
		if calls != nil {
			*calls++
		}

		return err
	}}
}

func TestWithdrawReturnsEmptyOKAndPassesBodyWithUser(t *testing.T) {
	var number string
	var sum money.Points
	var userID int64
	deps := Deps{Withdraw: func(_ context.Context, gotNumber string, gotSum money.Points, gotUserID int64) error {
		number = gotNumber
		sum = gotSum
		userID = gotUserID

		return nil
	}}

	response := serveWithdraw(
		newRouter(zap.NewNop(), deps),
		`{"order":"2377225624","sum":751}`,
	)

	require.Equal(t, http.StatusOK, response.Code)
	assert.Empty(t, response.Body.String())
	assert.Empty(t, response.Header().Get("Content-Type"))
	assert.Equal(t, "2377225624", number)
	assert.Equal(t, money.Points(75100), sum)
	assert.Equal(t, int64(testUserID), userID)
}

func TestWithdrawPassesFractionalSumAsKopecks(t *testing.T) {
	var sum money.Points
	calls := 0
	deps := Deps{Withdraw: func(_ context.Context, _ string, gotSum money.Points, _ int64) error {
		calls++
		sum = gotSum

		return nil
	}}

	response := serveWithdraw(
		newRouter(zap.NewNop(), deps),
		`{"order":"2377225624","sum":751.5}`,
	)

	require.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, 1, calls)
	assert.Equal(t, money.Points(75150), sum)
}

func TestWithdrawAnswersExpectedDomainFailuresWithoutLogging(t *testing.T) {
	tests := []struct {
		name        string
		withdrawErr error
		wantStatus  int
	}{
		{name: "not enough funds", withdrawErr: domain.ErrInsufficientFunds, wantStatus: http.StatusPaymentRequired},
		{name: "order taken", withdrawErr: domain.ErrOrderTaken, wantStatus: http.StatusConflict},
		{name: "empty order", withdrawErr: ordernumber.ErrEmpty, wantStatus: http.StatusUnprocessableEntity},
		{name: "invalid order", withdrawErr: ordernumber.ErrInvalid, wantStatus: http.StatusUnprocessableEntity},
		{name: "nonpositive sum", withdrawErr: domain.ErrNonPositiveSum, wantStatus: http.StatusBadRequest},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			core, logs := observer.New(zapcore.ErrorLevel)
			response := serveWithdraw(
				newRouter(zap.New(core), withdrawReturning(test.withdrawErr, nil)),
				`{"order":"2377225624","sum":751}`,
			)

			assert.Equal(t, test.wantStatus, response.Code)
			assert.Equal(t, "application/problem+json", response.Header().Get("Content-Type"))
			assert.Zero(t, logs.Len())
		})
	}
}

func TestWithdrawValidatesJSONAndValuesBeforeRepository(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantStatus int
	}{
		{name: "malformed JSON", body: `{`, wantStatus: http.StatusBadRequest},
		{name: "sum is a string", body: `{"order":"2377225624","sum":"751"}`, wantStatus: http.StatusBadRequest},
		{name: "sum is more precise than a kopeck", body: `{"order":"2377225624","sum":1.001}`, wantStatus: http.StatusBadRequest},
		{name: "sum is null", body: `{"order":"2377225624","sum":null}`, wantStatus: http.StatusBadRequest},
		{name: "sum is zero", body: `{"order":"2377225624","sum":0}`, wantStatus: http.StatusBadRequest},
		{name: "sum is negative", body: `{"order":"2377225624","sum":-1}`, wantStatus: http.StatusBadRequest},
		{name: "order is missing", body: `{"sum":1}`, wantStatus: http.StatusUnprocessableEntity},
		{name: "order is empty", body: `{"order":"","sum":1}`, wantStatus: http.StatusUnprocessableEntity},
		{name: "order fails Luhn", body: `{"order":"2377225625","sum":1}`, wantStatus: http.StatusUnprocessableEntity},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			storage := balancemocks.NewMockWithdrawer(t)
			deps := Deps{Withdraw: domain.NewWithdrawService(storage).Withdraw}

			response := serveWithdraw(
				newRouter(zap.NewNop(), deps),
				test.body,
			)

			assert.Equal(t, test.wantStatus, response.Code)
		})
	}
}

func TestWithdrawRejectsRequestBeforeService(t *testing.T) {
	tests := []struct {
		name        string
		userID      int64
		contentType string
		body        string
		bodyLimit   int64
		wantStatus  int
	}{
		{
			name:        "without user",
			contentType: "application/json",
			body:        `{"order":"2377225624","sum":1}`,
			bodyLimit:   testBodyLimit,
			wantStatus:  http.StatusUnauthorized,
		},
		{
			name:        "unsupported content type",
			userID:      testUserID,
			contentType: "text/plain",
			body:        `{"order":"2377225624","sum":1}`,
			bodyLimit:   testBodyLimit,
			wantStatus:  http.StatusUnsupportedMediaType,
		},
		{
			name:        "body over limit",
			userID:      testUserID,
			contentType: "application/json",
			body:        `{"order":"2377225624","sum":1}`,
			bodyLimit:   8,
			wantStatus:  http.StatusRequestEntityTooLarge,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			response := serveWithdrawAs(
				newRouterWithBodyLimit(zap.NewNop(), withdrawReturning(nil, &calls), test.userID, test.bodyLimit),
				test.contentType,
				test.body,
			)

			assert.Equal(t, test.wantStatus, response.Code)
			assert.Zero(t, calls)
		})
	}
}

func TestWithdrawWithoutRouteAuthorizationRejectsMissingUser(t *testing.T) {
	calls := 0
	response := serveWithdraw(
		newUnprotectedRouter(zap.NewNop(), withdrawReturning(nil, &calls)),
		`{"order":"2377225624","sum":1}`,
	)

	assert.Equal(t, http.StatusUnauthorized, response.Code)
	assert.Contains(t, response.Body.String(), message.Unauthorized)
	assert.Zero(t, calls)
}

func TestWithdrawLogsInternalError(t *testing.T) {
	core, logs := observer.New(zapcore.ErrorLevel)
	storageErr := errors.New("storage is down")

	response := serveWithdraw(
		newRouter(zap.New(core), withdrawReturning(storageErr, nil)),
		`{"order":"2377225624","sum":751}`,
	)

	require.Equal(t, http.StatusInternalServerError, response.Code)
	assert.Contains(t, response.Body.String(), message.Internal)
	require.Equal(t, 1, logs.Len())
	assert.Contains(t, logs.All()[0].ContextMap()["error"], storageErr.Error())
}
