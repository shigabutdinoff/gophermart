package healthcheck

import (
	"context"
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
	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	Ping(func() Pinger { return pinger })(rec, req)
	return rec
}

func TestPing_DatabaseReachable(t *testing.T) {
	rec := servePing(&fakePinger{})

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	assert.JSONEq(t, `{"status":"ok"}`, rec.Body.String())
}

func TestPing_DatabaseUnreachable(t *testing.T) {
	rec := servePing(&fakePinger{err: context.DeadlineExceeded})

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Equal(t, "text/plain; charset=utf-8", rec.Header().Get("Content-Type"))
	assert.Equal(t, "Нет соединения с БД\n", rec.Body.String())
}

func TestPing_NoDatabase(t *testing.T) {
	rec := servePing(&fakePinger{err: errors.New("database is unavailable")})

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Equal(t, "text/plain; charset=utf-8", rec.Header().Get("Content-Type"))
	assert.Equal(t, "Нет соединения с БД\n", rec.Body.String())
}

func TestPing_UsesCurrentDatabaseOnEveryRequest(t *testing.T) {
	var current Pinger = &fakePinger{err: errors.New("database is unavailable")}
	getterCalls := 0
	handler := Ping(func() Pinger {
		getterCalls++
		return current
	})

	first := httptest.NewRecorder()
	handler(first, httptest.NewRequest(http.MethodGet, "/ping", nil))
	assert.Equal(t, http.StatusInternalServerError, first.Code)

	current = &fakePinger{}
	second := httptest.NewRecorder()
	handler(second, httptest.NewRequest(http.MethodGet, "/ping", nil))
	assert.Equal(t, http.StatusOK, second.Code)
	assert.Equal(t, 2, getterCalls)
}

func TestPing_AppliesDeadline(t *testing.T) {
	pinger := &fakePinger{}

	servePing(pinger)

	require.True(t, pinger.sawDeadline, "проверка обязана идти с таймаутом")
	assert.LessOrEqual(t, pinger.deadline.Sub(pinger.checkedAt), PingTimeout)
}

func TestPing_PropagatesCanceledRequestContext(t *testing.T) {
	pinger := &fakePinger{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	ctx, cancel := context.WithCancel(req.Context())
	cancel()

	Ping(func() Pinger { return pinger })(rec, req.WithContext(ctx))

	assert.ErrorIs(t, pinger.contextErr, context.Canceled)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}
