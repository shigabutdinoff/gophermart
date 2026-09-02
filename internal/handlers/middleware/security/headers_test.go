package security_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/shigabutdinoff/gophermart/internal/handlers/middleware/security"
)

func TestHeadersMarksEveryResponse(t *testing.T) {
	handler := security.Headers(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/user/orders", http.NoBody))

	assert.Equal(t, http.StatusTeapot, response.Code)
	assert.Equal(t, "nosniff", response.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
}

// Заголовки обязаны стоять и на ответе, который хендлер не дописал сам.
func TestHeadersMarksHandlerFailure(t *testing.T) {
	handler := security.Headers(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/user/balance", http.NoBody))

	assert.Equal(t, http.StatusInternalServerError, response.Code)
	assert.Equal(t, "nosniff", response.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
}
