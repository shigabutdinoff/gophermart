package main

import (
	"context"
	"errors"
	"flag"
	"os"
	"os/signal"
	"syscall"

	"go.uber.org/zap"

	config "github.com/shigabutdinoff/gophermart/internal/config/gophermart"
	"github.com/shigabutdinoff/gophermart/internal/server"
)

func main() {
	logger, err := zap.NewProduction()
	if err != nil {
		panic(err)
	}
	defer func() { _ = logger.Sync() }()

	cfg, err := config.Parse(os.Args[1:])
	// Запрос справки не ошибка, описание флагов уже напечатано
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if err != nil {
		logger.Fatal("Не удалось загрузить конфигурацию", zap.Error(err))
	}
	s := server.New(logger, cfg)

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
