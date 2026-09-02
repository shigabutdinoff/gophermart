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

const testUserID = 42

func newRouter(logger *zap.Logger, deps Deps) http.Handler {
	router := chi.NewRouter()
	api := apiconfig.NewAPI(router)
	authorize := authorization.Middleware(
		api,
		func(*http.Request, request.Extractor) (int64, error) { return testUserID, nil },
	)
	RegisterRoutes(api, logger, deps, Options{Middlewares: huma.Middlewares{
		authorize,
	}})

	return router
}

func newUnprotectedRouter(logger *zap.Logger, deps Deps) http.Handler {
	router := chi.NewRouter()
	RegisterRoutes(apiconfig.NewAPI(router), logger, deps, Options{})

	return router
}
