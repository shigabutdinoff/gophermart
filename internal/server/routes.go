package server

import (
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"

	"github.com/shigabutdinoff/gophermart/internal/handlers/apiconfig"
	"github.com/shigabutdinoff/gophermart/internal/handlers/middleware/authorization"
	"github.com/shigabutdinoff/gophermart/internal/handlers/middleware/logging"
	"github.com/shigabutdinoff/gophermart/internal/handlers/route/authentication"
	ordersroute "github.com/shigabutdinoff/gophermart/internal/handlers/route/orders"
)

func (s *Server) setupRoutes() {
	router := chi.NewRouter()

	router.Use(logging.WithLogging(s.logger))
	apiconfig.InstallErrorPolicy()
	api := humachi.New(router, apiconfig.New())
	authentication.RegisterRoutes(api, s.logger, s.authDeps, nil)
	ordersroute.RegisterRoutes(api, s.logger, s.orderDeps, ordersroute.Options{
		Middlewares: huma.Middlewares{authorization.Middleware(api, s.tokenParser)},
	})

	s.router = router
}
