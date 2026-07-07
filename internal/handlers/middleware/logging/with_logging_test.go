package logging

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestWithLogging_LogsOneEntryPerRequest(t *testing.T) {
	core, logs := observer.New(zap.InfoLevel)

	h := WithLogging(zap.New(core))(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Set-Cookie", "session=super-secret-setcookie")
		w.WriteHeader(http.StatusNotImplemented)
		_, _ = w.Write([]byte("resp-body-super-secret"))
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/user/orders",
		strings.NewReader("req-body-super-secret"))
	req.Header.Set("Authorization", "Bearer super-secret-token")
	req.Header.Set("Cookie", "jwt=super-secret-cookie")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	require.Equal(t, 1, logs.Len())
	entry := logs.All()[0]
	fields := entry.ContextMap()

	assert.Equal(t, http.MethodPost, fields["method"])
	assert.Equal(t, "/api/user/orders", fields["uri"])
	assert.EqualValues(t, http.StatusNotImplemented, fields["status"])
	assert.EqualValues(t, len("resp-body-super-secret"), fields["size"])
	assert.Contains(t, fields, "duration")

	dump := fmt.Sprintf("%s %v", entry.Message, fields)
	assert.NotContains(t, dump, "super-secret")
}

func TestWithLogging_ImplicitStatusLoggedAs200(t *testing.T) {
	core, logs := observer.New(zap.InfoLevel)

	h := WithLogging(zap.New(core))(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	require.Equal(t, 1, logs.Len())
	assert.EqualValues(t, http.StatusOK, logs.All()[0].ContextMap()["status"])
}

func TestWithLogging_AbortedRequestStillLogged(t *testing.T) {
	core, logs := observer.New(zap.InfoLevel)

	h := WithLogging(zap.New(core))(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/user/orders", nil)

	require.PanicsWithValue(t, http.ErrAbortHandler, func() { h.ServeHTTP(rec, req) })

	require.Equal(t, 1, logs.Len())
	assert.Equal(t, "/api/user/orders", logs.All()[0].ContextMap()["uri"])
}

func TestWithLogging_QueryNotLogged(t *testing.T) {
	core, logs := observer.New(zap.InfoLevel)

	h := WithLogging(zap.New(core))(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/user/orders?token=super-secret-token", nil)
	h.ServeHTTP(rec, req)

	require.Equal(t, 1, logs.Len())
	entry := logs.All()[0]

	assert.Equal(t, "/api/user/orders", entry.ContextMap()["uri"])
	assert.NotContains(t, fmt.Sprintf("%v", entry.ContextMap()), "super-secret")
}

func TestWithLogging_UnwrapReachesUnderlyingWriter(t *testing.T) {
	core, _ := observer.New(zap.InfoLevel)

	var flushErr error
	h := WithLogging(zap.New(core))(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		flushErr = http.NewResponseController(w).Flush()
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	assert.NoError(t, flushErr)
	assert.True(t, rec.Flushed)
}

func TestWithLogging_LogsEvery404(t *testing.T) {
	core, logs := observer.New(zap.InfoLevel)

	h := WithLogging(zap.New(core))(http.NotFoundHandler())

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/unknown", nil))

	require.Equal(t, 1, logs.Len())
	assert.EqualValues(t, http.StatusNotFound, logs.All()[0].ContextMap()["status"])
}
