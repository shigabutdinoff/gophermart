package gophermart

import (
	"fmt"
	"time"
)

// Значения параметров запуска по умолчанию.
const DefaultRunAddress = "localhost:8080"

// Значения очереди заданий по умолчанию.
const (
	// DefaultQueueName повторяет имя очереди по умолчанию у самой очереди
	DefaultQueueName                = "default"
	DefaultQueueWorkers             = 4
	DefaultQueueFetchPollInterval   = time.Second
	DefaultQueueAccrualPollInterval = time.Second
	DefaultQueueThrottleBackoff     = 10 * time.Second
	// DefaultQueueMaxAttempts короче щедрых 25 попыток очереди: опрос расчёта
	// попыток не тратит, и остаются они только страховкой от паники в задании
	DefaultQueueMaxAttempts = 5
)

// Config хранит параметры запуска приложения.
type Config struct {
	RunAddress     string `env:"RUN_ADDRESS"`
	DatabaseURI    string `env:"DATABASE_URI"`
	AccrualAddress string `env:"ACCRUAL_SYSTEM_ADDRESS"`
	JWTSecret      string `env:"JWT_SECRET"`
	Queue          QueueConfig
}

// QueueConfig настраивает очередь заданий и повторы опроса расчёта.
type QueueConfig struct {
	Name                string
	Workers             int
	FetchPollInterval   time.Duration
	AccrualPollInterval time.Duration
	ThrottleBackoff     time.Duration
	MaxAttempts         int
}

// Default возвращает конфигурацию со значениями по умолчанию.
func Default() Config {
	return Config{
		RunAddress: DefaultRunAddress,
		Queue: QueueConfig{
			Name:                DefaultQueueName,
			Workers:             DefaultQueueWorkers,
			FetchPollInterval:   DefaultQueueFetchPollInterval,
			AccrualPollInterval: DefaultQueueAccrualPollInterval,
			ThrottleBackoff:     DefaultQueueThrottleBackoff,
			MaxAttempts:         DefaultQueueMaxAttempts,
		},
	}
}

// validate проверяет корректность значений конфигурации.
func (c Config) validate() error {
	if c.RunAddress == "" {
		return fmt.Errorf("run address must not be empty")
	}
	return nil
}
