package healthcheck

import (
	"context"
	"net/http"
	"time"

	"github.com/alexliesenfeld/health"
)

// LivePath отвечает, что процесс жив, не трогая хранилище.
const LivePath = "/live"

// PingTimeout ограничивает время проверки соединения с БД.
const PingTimeout = time.Second

// DatabaseCheck называет проверку хранилища в отчёте готовности.
const DatabaseCheck = "database"

// Pinger проверяет связь с хранилищем.
type Pinger interface {
	PingContext(ctx context.Context) error
}

// Ping проверяет доступность БД, getDB возвращает непустой Pinger.
func Ping(getDB func() Pinger) http.HandlerFunc {
	checker := health.NewChecker(
		// проверка идёт по запросу, иначе ответ отражал бы прошлое состояние
		health.WithDisabledAutostart(),
		health.WithDisabledCache(),
		health.WithTimeout(PingTimeout),
		health.WithCheck(health.Check{
			Name: DatabaseCheck,
			Check: func(ctx context.Context) error {
				return getDB().PingContext(ctx)
			},
		}),
	)

	return health.NewHandler(checker)
}
