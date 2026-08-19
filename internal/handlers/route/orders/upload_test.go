package orders

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/shigabutdinoff/gophermart/internal/handlers/apiconfig"
	"github.com/shigabutdinoff/gophermart/internal/handlers/route/message"
	"github.com/shigabutdinoff/gophermart/internal/order"
)

func serveUpload(handler http.Handler, contentType, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/user/orders", strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)

	return response
}

// uploadReturning собирает зависимости загрузки с заданным исходом.
func uploadReturning(err error, seen *string, userID *int64) Deps {
	return Deps{
		Upload: func(_ context.Context, number string, id int64) error {
			if seen != nil {
				*seen = number
			}
			if userID != nil {
				*userID = id
			}
			return err
		},
	}
}

func TestUploadAnswersBySpecification(t *testing.T) {
	tests := []struct {
		name       string
		uploadErr  error
		wantStatus int
		wantBody   string
	}{
		{name: "новый номер", wantStatus: http.StatusAccepted},
		{
			name:       "повтор своего номера",
			uploadErr:  order.ErrAlreadyUploaded,
			wantStatus: http.StatusOK,
		},
		{
			name:       "номер чужого пользователя",
			uploadErr:  order.ErrOwnedByAnother,
			wantStatus: http.StatusConflict,
			wantBody:   MessageNumberTaken,
		},
		{
			name:       "номер из пробелов",
			uploadErr:  order.ErrEmptyNumber,
			wantStatus: http.StatusBadRequest,
			wantBody:   MessageEmptyNumber,
		},
		{
			name:       "номер не проходит проверку",
			uploadErr:  order.ErrInvalidNumber,
			wantStatus: http.StatusUnprocessableEntity,
			wantBody:   MessageInvalidNumber,
		},
		{
			name:       "конфликтующая строка исчезла до чтения владельца",
			uploadErr:  errors.New("find order owner: record not found"),
			wantStatus: http.StatusInternalServerError,
			wantBody:   message.Internal,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			deps := uploadReturning(test.uploadErr, nil, nil)
			router := newRouter(zap.NewNop(), deps, testUserID)

			response := serveUpload(router, "text/plain", testNumber)

			assert.Equal(t, test.wantStatus, response.Code)
			if test.wantBody == "" {
				assert.Empty(t, response.Body.String())
				return
			}
			assert.Equal(t, "application/problem+json", response.Header().Get("Content-Type"))
			assert.Contains(t, response.Body.String(), test.wantBody)
		})
	}
}

// Пустое тело huma отклоняет сама, до службы загрузки запрос не доходит.
func TestUploadRejectsEmptyBody(t *testing.T) {
	calls := 0
	deps := Deps{Upload: func(context.Context, string, int64) error {
		calls++
		return nil
	}}
	router := newRouter(zap.NewNop(), deps, testUserID)

	response := serveUpload(router, "text/plain", "")

	assert.Equal(t, http.StatusBadRequest, response.Code)
	assert.Equal(t, "application/problem+json", response.Header().Get("Content-Type"))
	assert.Zero(t, calls)
}

func TestUploadPassesRawBodyAndUser(t *testing.T) {
	var number string
	var userID int64
	deps := uploadReturning(nil, &number, &userID)
	router := newRouter(zap.NewNop(), deps, testUserID)

	response := serveUpload(router, "text/plain", " "+testNumber+"\n")

	require.Equal(t, http.StatusAccepted, response.Code)
	assert.Equal(t, " "+testNumber+"\n", number)
	assert.Equal(t, int64(testUserID), userID)
}

func TestUploadAcceptsBodyWithoutContentType(t *testing.T) {
	var number string
	deps := uploadReturning(nil, &number, nil)
	router := newRouter(zap.NewNop(), deps, testUserID)

	response := serveUpload(router, "", testNumber)

	require.Equal(t, http.StatusAccepted, response.Code)
	assert.Equal(t, testNumber, number)
}

func TestUploadKeepsBodyReadDeadline(t *testing.T) {
	api := humachi.New(chi.NewRouter(), apiconfig.New())
	RegisterRoutes(api, zap.NewNop(), Deps{}, Options{})

	operation := api.OpenAPI().Paths[ordersPath].Post

	assert.Equal(t, bodyReadTimeout, operation.BodyReadTimeout)
}

func TestUploadOperationDoesNotAdvertiseNotFound(t *testing.T) {
	api := humachi.New(chi.NewRouter(), apiconfig.New())
	RegisterRoutes(api, zap.NewNop(), Deps{}, Options{})

	operation := api.OpenAPI().Paths[ordersPath].Post

	assert.NotContains(t, operation.Errors, http.StatusNotFound)
}

// Маршрут не полагается на посредника, пользователь берётся из контекста.
func TestUploadRejectsRequestWithoutUser(t *testing.T) {
	calls := 0
	deps := Deps{Upload: func(context.Context, string, int64) error {
		calls++
		return nil
	}}

	response := serveUpload(newUnprotectedRouter(deps), "text/plain", testNumber)

	assert.Equal(t, http.StatusUnauthorized, response.Code)
	assert.Equal(t, "application/problem+json", response.Header().Get("Content-Type"))
	assert.Contains(t, response.Body.String(), message.Unauthorized)
	assert.Zero(t, calls)
}

func TestUploadLogsInternalError(t *testing.T) {
	core, logs := observer.New(zapcore.ErrorLevel)
	deps := uploadReturning(errors.New("storage is down"), nil, nil)
	router := newRouter(zap.New(core), deps, testUserID)

	response := serveUpload(router, "text/plain", testNumber)

	require.Equal(t, http.StatusInternalServerError, response.Code)
	require.Equal(t, 1, logs.Len())
	assert.Contains(t, logs.All()[0].ContextMap()["error"], "storage is down")
}
