package main

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"

	"go.uber.org/zap"

	config "github.com/shigabutdinoff/gophermart/internal/config/gophermart"
	"github.com/shigabutdinoff/gophermart/internal/server"
)

func main() {
	logger := zap.Must(zap.NewProduction())
	defer func() { _ = logger.Sync() }()

	cfg, err := config.Parse(os.Args[1:])
	// Запрос справки не ошибка, описание флагов уже напечатано
	if errors.Is(err, config.ErrHelp) {
		return
	}
	if err != nil {
		logger.Fatal("Не удалось загрузить конфигурацию", zap.Error(err))
	}
	serverInstance, err := server.New(logger, cfg)
	if err != nil {
		logger.Fatal("Не удалось создать сервер", zap.Error(err))
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Повторный сигнал завершает процесс сразу, не дожидаясь graceful shutdown
	go func() {
		<-ctx.Done()
		stop()
	}()

	if err := serverInstance.Run(ctx); err != nil {
		logger.Fatal("Сервер завершился с ошибкой", zap.Error(err))
	}
}
