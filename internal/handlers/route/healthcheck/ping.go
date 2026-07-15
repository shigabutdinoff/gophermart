package healthcheck

import "net/http"

// Ping подтверждает, что сервис жив и обрабатывает запросы.
func Ping(res http.ResponseWriter, _ *http.Request) {
	res.Header().Set("Content-Type", "application/json")
	res.WriteHeader(http.StatusOK)
	_, _ = res.Write([]byte(`{"status":"ok"}`))
}
