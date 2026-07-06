package server

import "github.com/go-chi/chi/v5"

func (s *Server) setupRoutes() {
	r := chi.NewRouter()

	s.router = r
}
