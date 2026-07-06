package server

import "net"

// listen занимает адрес заранее, чтобы ошибка привязки вернулась сразу.
func (s *Server) listen() error {
	ln, err := net.Listen("tcp", s.RunAddress)
	if err != nil {
		return err
	}
	s.ln = ln
	return nil
}

// Addr возвращает фактический адрес прослушивания (важно при порте 0 в тестах).
func (s *Server) Addr() string {
	return s.ln.Addr().String()
}
