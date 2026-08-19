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
	"github.com/stretchr/testify/assert"
)

func TestNewHidesFrameworkRoutes(t *testing.T) {
	config := New()

	assert.Empty(t, config.OpenAPIPath)
	assert.Empty(t, config.DocsPath)
	assert.Empty(t, config.SchemasPath)
	assert.Empty(t, config.CreateHooks)
	assert.Empty(t, config.Transformers)
}

func TestNewMapsMarkedValidationErrorsToBadRequest(t *testing.T) {
	tests := []struct {
		name       string
		metadata   map[string]any
		wantStatus int
	}{
		{
			name:       "marked operation",
			metadata:   ValidationErrorsAsBadRequest(),
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "unmarked operation",
			wantStatus: http.StatusUnprocessableEntity,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			type validationInput struct {
				Body struct {
					Value string `json:"value" minLength:"5"`
				}
			}

			router := chi.NewRouter()
			api := humachi.New(router, New())
			huma.Register(api, huma.Operation{
				OperationID: "validate-" + strings.ReplaceAll(test.name, " ", "-"),
				Method:      http.MethodPost,
				Path:        "/validate",
				Metadata:    test.metadata,
			}, func(context.Context, *validationInput) (*struct{}, error) {
				return nil, nil
			})

			response := httptest.NewRecorder()
			request := httptest.NewRequest(
				http.MethodPost,
				"/validate",
				strings.NewReader(`{"value":"no"}`),
			)
			request.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(response, request)

			assert.Equal(t, test.wantStatus, response.Code)
		})
	}
}

func TestNewKeepsMarkedOperationContextErrorsUnprocessable(t *testing.T) {
	tests := []struct {
		name    string
		message string
	}{
		{name: "other message", message: "middleware rejected request"},
		{name: "reserved message without details", message: humaValidationErrorMessage},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			type validInput struct {
				Body struct {
					Value string `json:"value" minLength:"5"`
				}
			}

			router := chi.NewRouter()
			api := humachi.New(router, New())
			huma.Register(api, huma.Operation{
				OperationID: "context-error-" + strings.ReplaceAll(test.name, " ", "-"),
				Method:      http.MethodPost,
				Path:        "/context-error",
				Metadata:    ValidationErrorsAsBadRequest(),
				Middlewares: huma.Middlewares{
					func(ctx huma.Context, _ func(huma.Context)) {
						_ = huma.WriteErr(
							api,
							ctx,
							http.StatusUnprocessableEntity,
							test.message,
						)
					},
				},
			}, func(context.Context, *validInput) (*struct{}, error) {
				return nil, nil
			})

			response := httptest.NewRecorder()
			request := httptest.NewRequest(
				http.MethodPost,
				"/context-error",
				strings.NewReader(`{"value":"valid"}`),
			)
			request.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(response, request)

			assert.Equal(t, http.StatusUnprocessableEntity, response.Code)
			assert.Contains(t, response.Body.String(), test.message)
		})
	}
}

type probeContextKey struct{}

// probeAPI публикует операцию с посредниками маршрутизатора внутри huma.
func probeAPI(visited *[]string, middlewares ...func(http.Handler) http.Handler) http.Handler {
	router := chi.NewRouter()
	api := humachi.New(router, New())
	huma.Register(api, huma.Operation{
		OperationID: "probe",
		Method:      http.MethodGet,
		Path:        "/probe",
		Middlewares: FromHTTP(middlewares...),
	}, func(ctx context.Context, _ *struct{}) (*struct{}, error) {
		passed, _ := ctx.Value(probeContextKey{}).(string)
		*visited = append(*visited, "handler:"+passed)

		return nil, nil
	})

	return router
}

// mark отмечает проход посредника и обогащает контекст запроса.
func mark(visited *[]string, name string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			*visited = append(*visited, name)
			ctx := context.WithValue(r.Context(), probeContextKey{}, name)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func serveProbe(handler http.Handler) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/probe", http.NoBody))

	return response
}

func TestFromHTTPKeepsOrderAndRequestContext(t *testing.T) {
	var visited []string
	handler := probeAPI(&visited, mark(&visited, "outer"), mark(&visited, "inner"))

	response := serveProbe(handler)

	assert.Equal(t, http.StatusNoContent, response.Code)
	assert.Equal(t, []string{"outer", "inner", "handler:inner"}, visited)
}

func TestFromHTTPLetsMiddlewareAnswerInsteadOfHandler(t *testing.T) {
	var visited []string
	handler := probeAPI(&visited, func(http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte("denied"))
		})
	})

	response := serveProbe(handler)

	assert.Equal(t, http.StatusUnauthorized, response.Code)
	assert.Equal(t, "denied", response.Body.String())
	assert.Empty(t, visited)
}
