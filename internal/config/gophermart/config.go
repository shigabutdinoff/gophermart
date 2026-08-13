package gophermart

import "fmt"

// Значения параметров запуска по умолчанию.
const DefaultRunAddress = "localhost:8080"

// Config хранит параметры запуска приложения.
type Config struct {
	RunAddress     string `env:"RUN_ADDRESS"`
	DatabaseURI    string `env:"DATABASE_URI"`
	AccrualAddress string `env:"ACCRUAL_SYSTEM_ADDRESS"`
	JWTSecret      string `env:"JWT_SECRET"`
}

// Default возвращает конфигурацию со значениями по умолчанию.
func Default() Config {
	return Config{RunAddress: DefaultRunAddress}
}

// validate проверяет корректность значений конфигурации.
func (c Config) validate() error {
	if c.RunAddress == "" {
		return fmt.Errorf("run address must not be empty")
	}
	return nil
}
