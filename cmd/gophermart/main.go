package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Повторный сигнал завершает процесс сразу, не дожидаясь graceful shutdown
	go func() {
		<-ctx.Done()
		stop()
	}()

	if err := s.Run(ctx); err != nil {
		logger.Fatal("Сервер завершился с ошибкой", zap.Error(err))
	}
}
