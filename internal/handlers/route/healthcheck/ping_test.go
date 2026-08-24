package healthcheck

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
)

type fakePinger struct {
	err         error
	sawDeadline bool
	deadline    time.Time
	checkedAt   time.Time
	contextErr  error
}

func (f *fakePinger) PingContext(ctx context.Context) error {
	f.checkedAt = time.Now()
	f.deadline, f.sawDeadline = ctx.Deadline()
	f.contextErr = ctx.Err()
	if f.err != nil {
		return f.err
	}
	return f.contextErr
}

func servePing(pinger Pinger) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, PingPath, nil)
	Ping(func() Pinger { return pinger })(rec, req)
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

	return report.Status, report.Details[DatabaseCheck]
}

func TestPing_DatabaseReachable(t *testing.T) {
	rec := servePing(&fakePinger{})

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "application/json")
	status, database := reportStatus(t, rec)
	assert.Equal(t, "up", status)
	assert.Equal(t, "up", database["status"])
}

func TestPing_DatabaseUnreachable(t *testing.T) {
	rec := servePing(&fakePinger{err: context.DeadlineExceeded})

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "application/json")
	status, database := reportStatus(t, rec)
	assert.Equal(t, "down", status)
	assert.Equal(t, "down", database["status"])
}

func TestPing_NoDatabase(t *testing.T) {
	rec := servePing(&fakePinger{err: errors.New("database is unavailable")})

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	status, database := reportStatus(t, rec)
	assert.Equal(t, "down", status)
	assert.Contains(t, database["error"], "database is unavailable")
}

func TestPing_UsesCurrentDatabaseOnEveryRequest(t *testing.T) {
	var current Pinger = &fakePinger{err: errors.New("database is unavailable")}
	getterCalls := 0
	handler := Ping(func() Pinger {
		getterCalls++
		return current
	})

	first := httptest.NewRecorder()
	handler(first, httptest.NewRequest(http.MethodGet, PingPath, nil))
	assert.Equal(t, http.StatusServiceUnavailable, first.Code)

	current = &fakePinger{}
	second := httptest.NewRecorder()
	handler(second, httptest.NewRequest(http.MethodGet, PingPath, nil))
	assert.Equal(t, http.StatusOK, second.Code)
	assert.Equal(t, 2, getterCalls)
}

func TestPing_AppliesDeadline(t *testing.T) {
	pinger := &fakePinger{}

	servePing(pinger)

	require.True(t, pinger.sawDeadline, "проверка обязана идти с таймаутом")
	assert.LessOrEqual(t, pinger.deadline.Sub(pinger.checkedAt), PingTimeout)
}

// На отменённый контекст запроса приходит отказ, а не успешный отчёт.
func TestPing_FailsOnCanceledRequestContext(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, PingPath, nil)
	ctx, cancel := context.WithCancel(req.Context())
	cancel()

	Ping(func() Pinger { return &fakePinger{} })(rec, req.WithContext(ctx))

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	status, _ := reportStatus(t, rec)
	assert.Equal(t, "down", status)
}

// Один обработчик на оба пути даёт побайтово одинаковые ответы.
func TestPing_SameHandlerAnswersBothRoutes(t *testing.T) {
	handler := Ping(func() Pinger { return &fakePinger{} })

	ready := httptest.NewRecorder()
	handler(ready, httptest.NewRequest(http.MethodGet, ReadyPath, nil))
	ping := httptest.NewRecorder()
	handler(ping, httptest.NewRequest(http.MethodGet, PingPath, nil))

	require.Equal(t, ready.Code, ping.Code)
	assert.Equal(t, ready.Header().Get("Content-Type"), ping.Header().Get("Content-Type"))

	readyStatus, _ := reportStatus(t, ready)
	pingStatus, _ := reportStatus(t, ping)
	assert.Equal(t, readyStatus, pingStatus)
}
