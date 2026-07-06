package main

import (
	"go.uber.org/zap"

	"github.com/shigabutdinoff/gophermart/internal/server"
)

func main() {
	logger, err := zap.NewProduction()
	if err != nil {
		panic(err)
	}
	defer func() { _ = logger.Sync() }()

	s := server.New(logger)

	if err := s.Run(); err != nil {
		logger.Fatal("Сервер завершился с ошибкой", zap.Error(err))
	}
}
