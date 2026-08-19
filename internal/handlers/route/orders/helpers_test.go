package orders

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5/request"
	"go.uber.org/zap"

	"github.com/shigabutdinoff/gophermart/internal/handlers/apiconfig"
	"github.com/shigabutdinoff/gophermart/internal/handlers/middleware/authorization"
)

const (
	testBodyLimit = 1 << 20
	testUserID    = 42
	testNumber    = "12345678903"
)

// newRouter собирает маршруты так же, как это делает сервер.
func newRouter(logger *zap.Logger, deps Deps, userID int64) http.Handler {
	router := chi.NewRouter()
	api := apiconfig.NewAPI(router)
	authorize := authorization.Middleware(
		api,
		func(*http.Request, request.Extractor) (int64, error) { return userID, nil },
	)
	RegisterRoutes(api, logger, deps, Options{
		Middlewares: huma.Middlewares{authorize},
	})

	return router
}

// newUnprotectedRouter публикует маршруты без посредника авторизации.
func newUnprotectedRouter(deps Deps) http.Handler {
	router := chi.NewRouter()
	RegisterRoutes(apiconfig.NewAPI(router), zap.NewNop(), deps, Options{})

	return router
}
