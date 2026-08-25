package gophermart

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/riverqueue/river"
)

// Значения параметров запуска по умолчанию.
const (
	DefaultRunAddress             = "localhost:8080"
	DefaultRequestBodyLimit int64 = 1 << 20
)

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
	RunAddress       string `env:"RUN_ADDRESS"`
	DatabaseURI      string `env:"DATABASE_URI"`
	AccrualAddress   string `env:"ACCRUAL_SYSTEM_ADDRESS"`
	RequestBodyLimit int64  `env:"REQUEST_BODY_LIMIT"`
	JWTSecret        string `env:"JWT_SECRET"`
	Queue            QueueConfig
}

// QueueConfig настраивает очередь заданий и повторы опроса расчёта.
type QueueConfig struct {
	Name                string        `env:"QUEUE_NAME"`
	Workers             int           `env:"QUEUE_WORKERS"`
	FetchPollInterval   time.Duration `env:"QUEUE_FETCH_POLL_INTERVAL"`
	AccrualPollInterval time.Duration `env:"QUEUE_ACCRUAL_POLL_INTERVAL"`
	ThrottleBackoff     time.Duration `env:"QUEUE_THROTTLE_BACKOFF"`
	MaxAttempts         int           `env:"QUEUE_MAX_ATTEMPTS"`
}

// Default возвращает конфигурацию со значениями по умолчанию.
func Default() Config {
	return Config{
		RunAddress:       DefaultRunAddress,
		RequestBodyLimit: DefaultRequestBodyLimit,
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

// maxQueueNameLength повторяет предел очереди на длину имени.
const maxQueueNameLength = 64

// queueNameRegexp повторяет требование очереди к имени: иначе она
// отказала бы уже в работе, а не при разборе конфигурации.
var queueNameRegexp = regexp.MustCompile(`^[a-z0-9]+(?:[_-][a-z0-9]+)*$`)

// normalize приводит значения к виду, в котором их ждут потребители.
func (c *Config) normalize() {
	c.Queue.Name = strings.TrimSpace(c.Queue.Name)
}

// validate проверяет корректность значений конфигурации.
func (c Config) validate() error {
	if c.RunAddress == "" {
		return fmt.Errorf("run address must not be empty")
	}
	if c.RequestBodyLimit <= 0 {
		return fmt.Errorf("request body limit must be positive")
	}

	return c.Queue.validate()
}

func (q QueueConfig) validate() error {
	if q.Name == "" {
		return fmt.Errorf("queue name must not be empty")
	}
	if len(q.Name) > maxQueueNameLength {
		return fmt.Errorf("queue name must be at most %d characters", maxQueueNameLength)
	}
	if !queueNameRegexp.MatchString(q.Name) {
		return fmt.Errorf("queue name %q must match %s", q.Name, queueNameRegexp)
	}
	if q.Workers <= 0 {
		return fmt.Errorf("queue workers must be positive")
	}
	// предел очереди: иначе она отказала бы уже при сборке клиента
	if q.Workers > river.QueueNumWorkersMax {
		return fmt.Errorf("queue workers must be at most %d", river.QueueNumWorkersMax)
	}
	if q.MaxAttempts <= 0 {
		return fmt.Errorf("queue max attempts must be positive")
	}
	if q.FetchPollInterval < 100*time.Millisecond {
		return fmt.Errorf("queue fetch poll interval must be at least 100ms")
	}

	if q.AccrualPollInterval <= 0 {
		return fmt.Errorf("queue accrual poll interval must be positive")
	}
	if q.ThrottleBackoff <= 0 {
		return fmt.Errorf("queue throttle backoff must be positive")
	}
	return nil
}
