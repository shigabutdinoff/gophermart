package orders

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/shigabutdinoff/gophermart/internal/handlers/route/message"
	"github.com/shigabutdinoff/gophermart/internal/order"
)

func serveList(handler http.Handler) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/user/orders", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)

	return response
}

// listReturning собирает зависимости выдачи с заданным исходом.
func listReturning(list []order.Order, err error, userID *int64) Deps {
	return Deps{
		List: func(_ context.Context, id int64) ([]order.Order, error) {
			if userID != nil {
				*userID = id
			}
			return list, err
		},
	}
}

func TestListReturnsOrdersOfUser(t *testing.T) {
	newer := time.Date(2026, 8, 19, 12, 0, 0, 0, time.FixedZone("MSK", 3*60*60))
	var userID int64
	deps := listReturning([]order.Order{
		{Number: "9278923470", Status: order.StatusProcessed, UploadedAt: newer},
		{Number: "9278923471", Status: order.StatusInvalid, UploadedAt: newer.Add(-time.Hour)},
		{Number: "9278923472", Status: order.StatusProcessing, UploadedAt: newer.Add(-2 * time.Hour)},
		{Number: testNumber, Status: order.StatusNew, UploadedAt: newer.Add(-3 * time.Hour)},
	}, nil, &userID)

	response := serveList(newRouter(zap.NewNop(), deps, testUserID))

	require.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, "application/json", response.Header().Get("Content-Type"))
	assert.Equal(t, int64(testUserID), userID)

	var body []map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	expected := []struct {
		number     string
		status     string
		uploadedAt string
	}{
		{number: "9278923470", status: "PROCESSED", uploadedAt: "2026-08-19T12:00:00+03:00"},
		{number: "9278923471", status: "INVALID", uploadedAt: "2026-08-19T11:00:00+03:00"},
		{number: "9278923472", status: "PROCESSING", uploadedAt: "2026-08-19T10:00:00+03:00"},
		{number: testNumber, status: "NEW", uploadedAt: "2026-08-19T09:00:00+03:00"},
	}
	require.Len(t, body, len(expected))
	for i, want := range expected {
		assert.Equal(t, want.number, body[i]["number"])
		assert.Equal(t, want.status, body[i]["status"])
		uploadedAt, ok := body[i]["uploaded_at"].(string)
		require.Truef(t, ok, "order at index %d has non-string uploaded_at: %#v", i, body[i]["uploaded_at"])
		assert.Equal(t, want.uploadedAt, uploadedAt)
		_, err := time.Parse(time.RFC3339, uploadedAt)
		assert.NoError(t, err)
		assert.NotContains(t, body[i], "accrual")
	}
}

func TestListAnswersWithoutData(t *testing.T) {
	deps := listReturning(nil, nil, nil)

	response := serveList(newRouter(zap.NewNop(), deps, testUserID))

	assert.Equal(t, http.StatusNoContent, response.Code)
	assert.Empty(t, response.Body.String())
	assert.Empty(t, response.Header().Get("Content-Type"))
}

func TestListPassesFilterWithUnknownBodyLength(t *testing.T) {
	deps := listReturning(nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/user/orders", http.NoBody)
	// клиент с chunked-кодировкой не сообщает длину тела
	req.ContentLength = -1
	req.TransferEncoding = []string{"chunked"}
	response := httptest.NewRecorder()

	newRouter(zap.NewNop(), deps, testUserID).ServeHTTP(response, req)

	assert.Equal(t, http.StatusNoContent, response.Code)
}

// Маршрут не полагается на посредника, пользователь берётся из контекста.
func TestListRejectsRequestWithoutUser(t *testing.T) {
	calls := 0
	deps := Deps{List: func(context.Context, int64) ([]order.Order, error) {
		calls++
		return nil, nil
	}}

	response := serveList(newUnprotectedRouter(deps))

	assert.Equal(t, http.StatusUnauthorized, response.Code)
	assert.Equal(t, "application/problem+json", response.Header().Get("Content-Type"))
	assert.Contains(t, response.Body.String(), message.Unauthorized)
	assert.Zero(t, calls)
}

func TestListRequiresAuthenticatedUser(t *testing.T) {
	calls := 0
	deps := Deps{List: func(context.Context, int64) ([]order.Order, error) {
		calls++
		return nil, nil
	}}

	response := serveList(newRouter(zap.NewNop(), deps, 0))

	assert.Equal(t, http.StatusUnauthorized, response.Code)
	assert.Zero(t, calls)
}

func TestListLogsInternalError(t *testing.T) {
	core, logs := observer.New(zapcore.ErrorLevel)
	deps := listReturning(nil, errors.New("storage is down"), nil)

	response := serveList(newRouter(zap.New(core), deps, testUserID))

	require.Equal(t, http.StatusInternalServerError, response.Code)
	assert.Contains(t, response.Body.String(), message.Internal)
	require.Equal(t, 1, logs.Len())
	assert.Contains(t, logs.All()[0].ContextMap()["error"], "storage is down")
}
