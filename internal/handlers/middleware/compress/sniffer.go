package compress

import "net/http"

// ensureContentType проставляет тип по содержимому, если он не задан.
func ensureContentType(h http.Header, body []byte) {
	if h.Get("Content-Type") == "" {
		h.Set("Content-Type", http.DetectContentType(body))
	}
}

func compressibleStatus(status int) bool {
	return status < 300 && status != http.StatusNoContent
}
