package server

import "go.uber.org/zap"

// Run работает, пока сервер не завершится с ошибкой.
func (s *Server) Run() error {
	if s.ln == nil {
		if err := s.listen(); err != nil {
			return err
		}
	}

	srv, err := s.newHTTPServer()
	if err != nil {
		_ = s.ln.Close()
		return err
	}
	s.srv = srv

	s.logger.Info("Сервер запущен", zap.String("address", s.Addr()))

	return s.srv.Serve(s.ln)
}
