package gophermart

import "fmt"

// Значения параметров запуска по умолчанию.
const (
	DefaultRunAddress             = "localhost:8080"
	DefaultRequestBodyLimit int64 = 1 << 20
)

// Config хранит параметры запуска приложения.
type Config struct {
	RunAddress       string `env:"RUN_ADDRESS"`
	DatabaseURI      string `env:"DATABASE_URI"`
	AccrualAddress   string `env:"ACCRUAL_SYSTEM_ADDRESS"`
	RequestBodyLimit int64  `env:"REQUEST_BODY_LIMIT"`
}

// Default возвращает конфигурацию со значениями по умолчанию.
func Default() Config {
	return Config{
		RunAddress:       DefaultRunAddress,
		RequestBodyLimit: DefaultRequestBodyLimit,
	}
}

// validate проверяет корректность значений конфигурации.
func (c Config) validate() error {
	if c.RunAddress == "" {
		return fmt.Errorf("run address must not be empty")
	}
	if c.RequestBodyLimit <= 0 {
		return fmt.Errorf("request body limit must be positive")
	}
	return nil
}
