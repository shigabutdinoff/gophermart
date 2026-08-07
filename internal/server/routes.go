package server

import (
	"compress/gzip"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/shigabutdinoff/gophermart/internal/handlers/apiconfig"
	"github.com/shigabutdinoff/gophermart/internal/handlers/middleware/authorization"
	"github.com/shigabutdinoff/gophermart/internal/handlers/middleware/decompress"
	"github.com/shigabutdinoff/gophermart/internal/handlers/middleware/logging"
	"github.com/shigabutdinoff/gophermart/internal/handlers/route"
	"github.com/shigabutdinoff/gophermart/internal/handlers/route/authentication"
	balanceroute "github.com/shigabutdinoff/gophermart/internal/handlers/route/balance"
	"github.com/shigabutdinoff/gophermart/internal/handlers/route/healthcheck"
	ordersroute "github.com/shigabutdinoff/gophermart/internal/handlers/route/orders"
)

func (s *Server) setupRoutes() {
	router := chi.NewRouter()
	s.installMiddleware(router)
	s.registerHealthcheck(router)
	s.registerAPI(router)

	s.router = router
}

func (s *Server) registerHealthcheck(router *chi.Mux) {
	router.Get("/ping", healthcheck.Ping(s.pinger))
}

func (s *Server) installMiddleware(router *chi.Mux) {
	router.Use(logging.WithLogging(s.logger))
	router.Use(middleware.GetHead)
	router.Use(middleware.Heartbeat("/live"))
	router.Use(middleware.Compress(gzip.DefaultCompression))
	router.Use(middleware.AllowContentEncoding(decompress.Encodings...))
	router.Use(decompress.Gzip)
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
