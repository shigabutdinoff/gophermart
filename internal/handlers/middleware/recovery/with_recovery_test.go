package recovery

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestWithRecovery_PanicLoggedAndAnswered500(t *testing.T) {
	core, logs := observer.New(zap.ErrorLevel)

	h := WithRecovery(zap.New(core))(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/panic", nil))

	assert.Equal(t, http.StatusInternalServerError, rec.Code)

	entries := logs.FilterMessage("Восстановление после паники").All()
	require.Len(t, entries, 1)
	fields := entries[0].ContextMap()
	assert.Equal(t, "boom", fields["panic"])
	assert.Contains(t, fields, "stack")
}

func TestWithRecovery_ErrAbortHandlerPassthrough(t *testing.T) {
	core, logs := observer.New(zap.ErrorLevel)

	h := WithRecovery(zap.New(core))(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/abort", nil)

	require.PanicsWithValue(t, http.ErrAbortHandler, func() { h.ServeHTTP(rec, req) })

	assert.Equal(t, 0, logs.Len())
}

func TestWithRecovery_PanicAfterResponseStartedAbortsRequest(t *testing.T) {
	core, logs := observer.New(zap.ErrorLevel)

	h := WithRecovery(zap.New(core))(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("partial body"))
		panic("late boom")
	}))

	rec := httptest.NewRecorder()
	require.PanicsWithValue(t, http.ErrAbortHandler, func() {
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/late-panic", nil))
	})

	entries := logs.FilterMessage("Восстановление после паники").All()
	require.Len(t, entries, 1)
	assert.Equal(t, "late boom", entries[0].ContextMap()["panic"])
}

func TestWithRecovery_PanicAfterSwitchingProtocolsAborts(t *testing.T) {
	core, _ := observer.New(zap.ErrorLevel)

	h := WithRecovery(zap.New(core))(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusSwitchingProtocols)
		panic("boom after upgrade")
	}))

	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := srv.Client().Get(srv.URL)
	if err == nil {
		resp.Body.Close()
	}
	assert.Error(t, err)
}

func TestWithRecovery_PanicAfterInterimStatusAnswers500(t *testing.T) {
	core, _ := observer.New(zap.ErrorLevel)

	h := WithRecovery(zap.New(core))(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusEarlyHints)
		panic("boom after hints")
	}))

	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := srv.Client().Get(srv.URL)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
}

func TestWithRecovery_UnwrapReachesUnderlyingWriter(t *testing.T) {
	core, _ := observer.New(zap.ErrorLevel)

	var flushErr error
	h := WithRecovery(zap.New(core))(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		flushErr = http.NewResponseController(w).Flush()
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	assert.NoError(t, flushErr)
	assert.True(t, rec.Flushed)
}

func TestWithRecovery_PassthroughWithoutPanic(t *testing.T) {
	core, logs := observer.New(zap.ErrorLevel)

	h := WithRecovery(zap.New(core))(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	assert.Equal(t, http.StatusTeapot, rec.Code)
	assert.Equal(t, 0, logs.Len())
}
