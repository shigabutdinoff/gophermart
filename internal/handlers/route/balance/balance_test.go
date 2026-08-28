package balance

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	domain "github.com/shigabutdinoff/gophermart/internal/balance"
	"github.com/shigabutdinoff/gophermart/internal/handlers/route/message"
	"github.com/shigabutdinoff/gophermart/internal/money"
)

func serveBalance(handler http.Handler) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, balancePath, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	return response
}

func readReturning(result domain.Balance, err error, userID *int64) Deps {
	return Deps{Read: func(_ context.Context, id int64) (domain.Balance, error) {
		if userID != nil {
			*userID = id
		}

		return result, err
	}}
}

func TestBalanceReturnsUserTotalsAsJSONNumbers(t *testing.T) {
	var userID int64
	deps := readReturning(domain.Balance{
		Current:   money.Points(50050),
		Withdrawn: money.Points(4200),
	}, nil, &userID)

	response := serveBalance(newRouter(zap.NewNop(), deps, testUserID))

	require.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, "application/json", response.Header().Get("Content-Type"))
	assert.JSONEq(t, `{"current":500.5,"withdrawn":42}`, response.Body.String())
	assert.Equal(t, int64(testUserID), userID)
}

func TestBalanceReturnsZerosForEmptyAccount(t *testing.T) {
	response := serveBalance(newRouter(
		zap.NewNop(),
		readReturning(domain.Balance{}, nil, nil),
		testUserID,
	))

	require.Equal(t, http.StatusOK, response.Code)
	assert.JSONEq(t, `{"current":0,"withdrawn":0}`, response.Body.String())
}

func TestBalanceRejectsRequestWithoutUser(t *testing.T) {
	calls := 0
	deps := Deps{Read: func(context.Context, int64) (domain.Balance, error) {
		calls++

		return domain.Balance{}, nil
	}}

	response := serveBalance(newUnprotectedRouter(zap.NewNop(), deps))

	assert.Equal(t, http.StatusUnauthorized, response.Code)
	assert.Equal(t, "application/problem+json", response.Header().Get("Content-Type"))
	assert.Contains(t, response.Body.String(), message.Unauthorized)
	assert.Zero(t, calls)
}

func TestBalanceLogsInternalError(t *testing.T) {
	core, logs := observer.New(zapcore.ErrorLevel)
	storageErr := errors.New("storage is down")
	deps := readReturning(domain.Balance{}, storageErr, nil)

	response := serveBalance(newRouter(zap.New(core), deps, testUserID))

	require.Equal(t, http.StatusInternalServerError, response.Code)
	assert.Contains(t, response.Body.String(), message.Internal)
	require.Equal(t, 1, logs.Len())
	assert.Contains(t, logs.All()[0].ContextMap()["error"], storageErr.Error())
}
