package server

import (
	"compress/gzip"
	"net/http"

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

func (s *Server) installMiddleware(router *chi.Mux) {
	// журнал стоит первым, он же восстанавливается после паник
	router.Use(logging.WithLogging(s.logger))
	// chi держит HEAD и GET разными маршрутами, GET-обработчик отвечает на оба
	router.Use(middleware.GetHead)
	router.Use(middleware.Heartbeat(healthcheck.LivePath))
	router.Use(middleware.Compress(gzip.DefaultCompression))
	router.Use(middleware.AllowContentEncoding(decompress.Encodings...))
	router.Use(middleware.RequestSize(apiconfig.RouterLimit(s.requestBodyLimit)))
	router.Use(decompress.Gzip)
}

func (s *Server) registerHealthcheck(router *chi.Mux) {
	// ответ даёт посредник выше, маршрут держит 405 для прочих методов
	router.Get(healthcheck.LivePath, func(http.ResponseWriter, *http.Request) {})

	ready := healthcheck.Ping(s.pinger)
	router.Get(healthcheck.ReadyPath, ready)
	router.Get(healthcheck.PingPath, ready)
}

func (s *Server) registerAPI(router *chi.Mux) {
	api := apiconfig.NewAPI(router)

	// huma отвечает 415 только на неизвестный ей формат, тип фильтрует chi
	authentication.RegisterRoutes(api, s.logger, s.deps.auth, s.routeOptions(
		apiconfig.AllowContentType("application/json"),
		apiconfig.ClientIPFromRemoteAddr,
	))

	// номер заказа приходит текстом, запрос без типа содержимого проходит фильтр
	ordersroute.RegisterRoutes(api, s.logger, s.deps.orders, s.routeOptions(
		authorization.Middleware(api, s.deps.tokenParser),
		apiconfig.AllowContentType("text/plain", ""),
	))

	balanceroute.RegisterRoutes(api, s.logger, s.deps.balance, s.routeOptions(
		authorization.Middleware(api, s.deps.tokenParser),
		apiconfig.AllowContentType("application/json"),
	))
}

// routeOptions дополняет посредники маршрута общим пределом тела запроса.
func (s *Server) routeOptions(middlewares ...func(huma.Context, func(huma.Context))) route.Options {
	return route.Options{
		BodyLimit:   s.requestBodyLimit,
		Middlewares: middlewares,
	}
}
