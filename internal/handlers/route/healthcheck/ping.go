package healthcheck

import (
	"context"
	"net/http"
	"time"
)

// PingTimeout ограничивает время проверки соединения с БД.
const PingTimeout = time.Second

// Pinger проверяет связь с хранилищем.
type Pinger interface {
	PingContext(ctx context.Context) error
}

// Ping проверяет доступность БД.
func Ping(getDB func() Pinger) http.HandlerFunc {
	return func(res http.ResponseWriter, req *http.Request) {
		db := getDB()
		if db == nil {
			http.Error(res, "БД не подключена", http.StatusInternalServerError)
			return
		}

		ctx, cancel := context.WithTimeout(req.Context(), PingTimeout)
		defer cancel()
		if err := db.PingContext(ctx); err != nil {
			http.Error(res, "Нет соединения с БД", http.StatusInternalServerError)
			return
		}

		res.Header().Set("Content-Type", "application/json")
		res.WriteHeader(http.StatusOK)
		_, _ = res.Write([]byte(`{"status":"ok"}`))
	}
}
