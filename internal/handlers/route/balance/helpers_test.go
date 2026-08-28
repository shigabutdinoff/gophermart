package balance

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
	testUserID          = 42
	testBodyLimit int64 = 1 << 20
)

func newRouter(logger *zap.Logger, deps Deps) http.Handler {
	return newRouterWithBodyLimit(logger, deps, testUserID, testBodyLimit)
}

func newRouterWithBodyLimit(
	logger *zap.Logger,
	deps Deps,
	userID int64,
	bodyLimit int64,
) http.Handler {
	router := chi.NewRouter()
	api := apiconfig.NewAPI(router)
	authorize := authorization.Middleware(
		api,
		func(*http.Request, request.Extractor) (int64, error) { return userID, nil },
	)
	RegisterRoutes(api, logger, deps, Options{
		BodyLimit: bodyLimit,
		Middlewares: huma.Middlewares{
			authorize,
			apiconfig.AllowContentType("application/json"),
		},
	})

	return router
}

func newUnprotectedRouter(logger *zap.Logger, deps Deps) http.Handler {
	router := chi.NewRouter()
	RegisterRoutes(apiconfig.NewAPI(router), logger, deps, Options{BodyLimit: testBodyLimit})

	return router
}
