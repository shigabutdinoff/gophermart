package server

import (
	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"

	"github.com/shigabutdinoff/gophermart/internal/handlers/apiconfig"
	"github.com/shigabutdinoff/gophermart/internal/handlers/middleware/authorization"
	"github.com/shigabutdinoff/gophermart/internal/handlers/middleware/logging"
	"github.com/shigabutdinoff/gophermart/internal/handlers/route"
	"github.com/shigabutdinoff/gophermart/internal/handlers/route/authentication"
	balanceroute "github.com/shigabutdinoff/gophermart/internal/handlers/route/balance"
	ordersroute "github.com/shigabutdinoff/gophermart/internal/handlers/route/orders"
)

func (s *Server) setupRoutes() {
	router := chi.NewRouter()
	s.installMiddleware(router)
	s.registerAPI(router)

	s.router = router
}

func (s *Server) installMiddleware(router *chi.Mux) {
	router.Use(logging.WithLogging(s.logger))
}

func (s *Server) registerAPI(router *chi.Mux) {
	api := apiconfig.NewAPI(router)
	authentication.RegisterRoutes(api, s.logger, s.deps.auth, s.routeOptions())
	ordersroute.RegisterRoutes(api, s.logger, s.deps.orders, s.routeOptions(
		authorization.Middleware(api, s.deps.tokenParser),
	))
	balanceroute.RegisterRoutes(api, s.logger, s.deps.balance, s.routeOptions(
		authorization.Middleware(api, s.deps.tokenParser),
	))
}

func (s *Server) routeOptions(middlewares ...func(huma.Context, func(huma.Context))) route.Options {
	return route.Options{Middlewares: middlewares}
}
