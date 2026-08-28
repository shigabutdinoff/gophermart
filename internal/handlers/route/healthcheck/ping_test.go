package healthcheck_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/shigabutdinoff/gophermart/internal/handlers/route/healthcheck"
	healthcheckmocks "github.com/shigabutdinoff/gophermart/internal/handlers/route/healthcheck/mocks"
)

type pingerState struct {
	err         error
	sawDeadline bool
	deadline    time.Time
	checkedAt   time.Time
	contextErr  error
}

func (s *pingerState) mock(t *testing.T, expectedCalls int) *healthcheckmocks.MockPinger {
	t.Helper()

	pinger := healthcheckmocks.NewMockPinger(t)
	if expectedCalls == 0 {
		pinger.EXPECT().PingContext(mock.Anything).
			RunAndReturn(func(ctx context.Context) error { return ctx.Err() }).
			Maybe()

		return pinger
	}
	pinger.EXPECT().PingContext(mock.Anything).RunAndReturn(func(ctx context.Context) error {
		s.checkedAt = time.Now()
		s.deadline, s.sawDeadline = ctx.Deadline()
		s.contextErr = ctx.Err()
		if s.err != nil {
			return s.err
		}

		return s.contextErr
	}).Times(expectedCalls)

	return pinger
}

func servePing(pinger healthcheck.Pinger) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, healthcheck.PingPath, nil)
	healthcheck.Ping(func() healthcheck.Pinger { return pinger })(rec, req)
	return rec
}

// reportStatus достаёт из отчёта состояние сервиса и проверки хранилища.
func reportStatus(t *testing.T, rec *httptest.ResponseRecorder) (string, map[string]any) {
	t.Helper()

	var report struct {
		Status  string                    `json:"status"`
		Details map[string]map[string]any `json:"details"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &report))

	return report.Status, report.Details[healthcheck.DatabaseCheck]
}

func TestPing_DatabaseReachable(t *testing.T) {
	rec := servePing((&pingerState{}).mock(t, 1))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "application/json")
	status, database := reportStatus(t, rec)
	assert.Equal(t, "up", status)
	assert.Equal(t, "up", database["status"])
}

func TestPing_DatabaseUnreachable(t *testing.T) {
	rec := servePing((&pingerState{err: context.DeadlineExceeded}).mock(t, 1))

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "application/json")
	status, database := reportStatus(t, rec)
	assert.Equal(t, "down", status)
	assert.Equal(t, "down", database["status"])
}

func TestPing_NoDatabase(t *testing.T) {
	rec := servePing((&pingerState{err: errors.New("database is unavailable")}).mock(t, 1))

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	status, database := reportStatus(t, rec)
	assert.Equal(t, "down", status)
	assert.Contains(t, database["error"], "database is unavailable")
}

func TestPing_UsesCurrentDatabaseOnEveryRequest(t *testing.T) {
	var current healthcheck.Pinger = (&pingerState{err: errors.New("database is unavailable")}).mock(t, 1)
	getterCalls := 0
	handler := healthcheck.Ping(func() healthcheck.Pinger {
		getterCalls++
		return current
	})

	first := httptest.NewRecorder()
	handler(first, httptest.NewRequest(http.MethodGet, healthcheck.PingPath, nil))
	assert.Equal(t, http.StatusServiceUnavailable, first.Code)

	current = (&pingerState{}).mock(t, 1)
	second := httptest.NewRecorder()
	handler(second, httptest.NewRequest(http.MethodGet, healthcheck.PingPath, nil))
	assert.Equal(t, http.StatusOK, second.Code)
	assert.Equal(t, 2, getterCalls)
}

func TestPing_AppliesDeadline(t *testing.T) {
	pinger := &pingerState{}

	servePing(pinger.mock(t, 1))

	require.True(t, pinger.sawDeadline, "проверка обязана идти с таймаутом")
	assert.LessOrEqual(t, pinger.deadline.Sub(pinger.checkedAt), healthcheck.PingTimeout)
}

// На отменённый контекст запроса приходит отказ, а не успешный отчёт.
func TestPing_FailsOnCanceledRequestContext(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, healthcheck.PingPath, nil)
	ctx, cancel := context.WithCancel(req.Context())
	cancel()

	pinger := (&pingerState{}).mock(t, 0)
	healthcheck.Ping(func() healthcheck.Pinger { return pinger })(rec, req.WithContext(ctx))

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	status, _ := reportStatus(t, rec)
	assert.Equal(t, "down", status)
}

// Один обработчик на оба пути даёт побайтово одинаковые ответы.
func TestPing_SameHandlerAnswersBothRoutes(t *testing.T) {
	pinger := (&pingerState{}).mock(t, 2)
	handler := healthcheck.Ping(func() healthcheck.Pinger { return pinger })

	ready := httptest.NewRecorder()
	handler(ready, httptest.NewRequest(http.MethodGet, healthcheck.ReadyPath, nil))
	ping := httptest.NewRecorder()
	handler(ping, httptest.NewRequest(http.MethodGet, healthcheck.PingPath, nil))

	require.Equal(t, ready.Code, ping.Code)
	assert.Equal(t, ready.Header().Get("Content-Type"), ping.Header().Get("Content-Type"))

	readyStatus, _ := reportStatus(t, ready)
	pingStatus, _ := reportStatus(t, ping)
	assert.Equal(t, readyStatus, pingStatus)
}
