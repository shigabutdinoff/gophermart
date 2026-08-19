package apiconfig

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/stretchr/testify/assert"
)

func specializedProbeAPI(middlewares huma.Middlewares, handle func(context.Context)) http.Handler {
	router := chi.NewRouter()
	api := humachi.New(router, New())
	huma.Register(api, huma.Operation{
		OperationID: "specialized-probe",
		Method:      http.MethodGet,
		Path:        "/probe",
		Middlewares: middlewares,
	}, func(ctx context.Context, _ *struct{}) (*struct{}, error) {
		handle(ctx)

		return nil, nil
	})

	return router
}

func TestAllowContentTypeAnswersBeforeHandler(t *testing.T) {
	called := false
	handler := specializedProbeAPI(
		huma.Middlewares{AllowContentType("text/plain")},
		func(context.Context) { called = true },
	)
	request := httptest.NewRequest(http.MethodGet, "/probe", strings.NewReader("body"))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	assert.Equal(t, http.StatusUnsupportedMediaType, response.Code)
	assert.False(t, called)
}

func TestClientIPFromRemoteAddrPassesAddressInRequestContext(t *testing.T) {
	var clientIP string
	handler := specializedProbeAPI(
		huma.Middlewares{ClientIPFromRemoteAddr},
		func(ctx context.Context) { clientIP = middleware.GetClientIP(ctx) },
	)
	request := httptest.NewRequest(http.MethodGet, "/probe", http.NoBody)
	request.RemoteAddr = "192.0.2.10:4321"
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	assert.Equal(t, http.StatusNoContent, response.Code)
	assert.Equal(t, "192.0.2.10", clientIP)
}
