package compress

import "net/http"

// sniffLen задаёт буфер определения Content-Type и порог сжатия ответа.
const sniffLen = 512

// sniffer буферизует начало тела, чтобы определить Content-Type и порог.
type sniffer struct {
	buf []byte
}

func (s *sniffer) buffered() int {
	return len(s.buf)
}

func (s *sniffer) add(p []byte) {
	if s.buf == nil {
		s.buf = make([]byte, 0, sniffLen)
	}
	s.buf = append(s.buf, p...)
}

func (s *sniffer) take() []byte {
	body := s.buf
	s.buf = nil
	return body
}

// ensureContentType проставляет тип по содержимому, если он не задан.
func ensureContentType(h http.Header, body []byte) {
	if h.Get("Content-Type") == "" {
		h.Set("Content-Type", http.DetectContentType(body))
	}
}

func compressibleStatus(status int) bool {
	return status < 300 && status != http.StatusNoContent
}
