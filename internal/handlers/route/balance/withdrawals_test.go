package balance

import (
	"context"
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

	domain "github.com/shigabutdinoff/gophermart/internal/balance"
	"github.com/shigabutdinoff/gophermart/internal/handlers/route/message"
	"github.com/shigabutdinoff/gophermart/internal/money"
)

func serveWithdrawals(handler http.Handler) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, withdrawalsPath, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	return response
}

func listReturning(result []domain.Withdrawal, err error, userID *int64) Deps {
	return Deps{List: func(_ context.Context, id int64) ([]domain.Withdrawal, error) {
		if userID != nil {
			*userID = id
		}

		return result, err
	}}
}

func TestWithdrawalsReturnsUserHistoryAsJSON(t *testing.T) {
	var userID int64
	deps := listReturning([]domain.Withdrawal{{
		Order:       "2377225624",
		Sum:         money.Points(75150),
		ProcessedAt: time.Date(2026, time.August, 28, 12, 34, 56, 0, time.UTC),
	}}, nil, &userID)

	response := serveWithdrawals(newRouter(zap.NewNop(), deps))

	require.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, "application/json", response.Header().Get("Content-Type"))
	assert.JSONEq(t, `[{"order":"2377225624","sum":751.5,"processed_at":"2026-08-28T12:34:56Z"}]`, response.Body.String())
	assert.Equal(t, int64(testUserID), userID)
}

func TestWithdrawalsReturnsNoContentForEmptyHistory(t *testing.T) {
	for _, withdrawals := range [][]domain.Withdrawal{nil, {}} {
		response := serveWithdrawals(newRouter(
			zap.NewNop(),
			listReturning(withdrawals, nil, nil)))

		assert.Equal(t, http.StatusNoContent, response.Code)
		assert.Empty(t, response.Body.Bytes())
		assert.Empty(t, response.Header().Get("Content-Type"))
	}
}

func TestWithdrawalsRejectsRequestWithoutUser(t *testing.T) {
	calls := 0
	deps := Deps{List: func(context.Context, int64) ([]domain.Withdrawal, error) {
		calls++

		return nil, nil
	}}

	response := serveWithdrawals(newUnprotectedRouter(zap.NewNop(), deps))

	assert.Equal(t, http.StatusUnauthorized, response.Code)
	assert.Contains(t, response.Body.String(), message.Unauthorized)
	assert.Zero(t, calls)
}

func TestWithdrawalsLogsInternalError(t *testing.T) {
	core, logs := observer.New(zapcore.ErrorLevel)
	storageErr := errors.New("storage is down")

	response := serveWithdrawals(newRouter(
		zap.New(core),
		listReturning(nil, storageErr, nil)))

	require.Equal(t, http.StatusInternalServerError, response.Code)
	assert.Contains(t, response.Body.String(), message.Internal)
	require.Equal(t, 1, logs.Len())
	assert.Contains(t, logs.All()[0].ContextMap()["error"], storageErr.Error())
}
