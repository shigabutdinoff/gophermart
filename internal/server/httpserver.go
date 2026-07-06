package server

import (
	"net/http"

	"go.uber.org/zap"
)

// newHTTPServer собирает HTTP-сервер с таймаутами по умолчанию.
func (s *Server) newHTTPServer() (*http.Server, error) {
	errorLog, err := zap.NewStdLogAt(s.logger, zap.ErrorLevel)
	if err != nil {
		return nil, err
	}
	return &http.Server{
		Handler:           s.router,
		ErrorLog:          errorLog,
		ReadHeaderTimeout: DefaultReadHeaderTimeout,
		ReadTimeout:       DefaultReadTimeout,
		WriteTimeout:      DefaultWriteTimeout,
		IdleTimeout:       DefaultIdleTimeout,
	}, nil
}
