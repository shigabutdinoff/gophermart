package security

import "net/http"

// Headers ставит заголовки, нужные каждому ответу приватного API.
// nosniff запрещает браузеру угадывать тип ответа, а no-store держит вне
// кешей и токен входа, и баланс пользователя.
func Headers(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := w.Header()
		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}
