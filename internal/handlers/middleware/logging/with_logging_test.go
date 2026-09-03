package logging

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// Имена полей приходят из httplog.SchemaECS.
const (
	fieldMethod   = "http.request.method"
	fieldPath     = "url.path"
	fieldStatus   = "http.response.status_code"
	fieldSize     = "http.response.body.bytes"
	fieldDuration = "event.duration"
	fieldError    = "error.message"
	fieldStack    = "error.stack_trace"
)

func TestWithLogging_LogsOneEntryPerRequest(t *testing.T) {
	core, logs := observer.New(zap.InfoLevel)

	h := WithLogging(zap.New(core))(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Set-Cookie", "session=super-secret-setcookie")
		w.WriteHeader(http.StatusNotImplemented)
		_, _ = io.WriteString(w, "resp-body-super-secret")
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

	assert.Equal(t, http.MethodPost, fields[fieldMethod])
	assert.Equal(t, "/api/user/orders", fields[fieldPath])
	assert.EqualValues(t, http.StatusNotImplemented, fields[fieldStatus])
	assert.EqualValues(t, len("resp-body-super-secret"), fields[fieldSize])
	assert.Contains(t, fields, fieldDuration)

	dump := fmt.Sprintf("%s %v", entry.Message, fields)
	assert.NotContains(t, dump, "super-secret")
}

func TestWithLogging_ImplicitStatusLoggedAs200(t *testing.T) {
	core, logs := observer.New(zap.InfoLevel)

	h := WithLogging(zap.New(core))(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	require.Equal(t, 1, logs.Len())
	assert.EqualValues(t, http.StatusOK, logs.All()[0].ContextMap()[fieldStatus])
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
	assert.Equal(t, "/api/user/orders", logs.All()[0].ContextMap()[fieldPath])
}

func TestWithLogging_QueryNotLogged(t *testing.T) {
	core, logs := observer.New(zap.InfoLevel)

	h := WithLogging(zap.New(core))(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/user/orders?token=super-secret-token", nil)
	h.ServeHTTP(rec, req)

	require.Equal(t, 1, logs.Len())
	entry := logs.All()[0]

	assert.Equal(t, "/api/user/orders", entry.ContextMap()[fieldPath])
	dump := fmt.Sprintf("%s %v", entry.Message, entry.ContextMap())
	assert.NotContains(t, dump, "super-secret")
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
	assert.EqualValues(t, http.StatusNotFound, logs.All()[0].ContextMap()[fieldStatus])
}

func TestWithLogging_PanicAnswersInternalError(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	h := WithLogging(zap.New(core))(http.HandlerFunc(
		func(http.ResponseWriter, *http.Request) {
			panic("boom")
		},
	))
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/user/login?token=query-secret",
		strings.NewReader("body-secret"),
	)
	request.Header.Set("Authorization", "Bearer header-secret")
	request.Header.Set("Cookie", "gophermart_session=cookie-secret")
	response := httptest.NewRecorder()

	assert.NotPanics(t, func() {
		h.ServeHTTP(response, request)
	})
	assert.Equal(t, http.StatusInternalServerError, response.Code)

	require.Equal(t, 1, logs.Len())
	fields := logs.All()[0].ContextMap()
	assert.Equal(t, http.MethodPost, fields[fieldMethod])
	assert.Equal(t, "/api/user/login", fields[fieldPath])
	assert.EqualValues(t, http.StatusInternalServerError, fields[fieldStatus])
	assert.Contains(t, fields[fieldError], "boom")
	assert.Contains(t, fmt.Sprintf("%v", fields[fieldStack]), "with_logging_test.go")

	dump := fmt.Sprintf("%v", logs.All())
	for _, secret := range []string{
		"query-secret",
		"body-secret",
		"header-secret",
		"cookie-secret",
	} {
		assert.NotContains(t, dump, secret)
	}
}
