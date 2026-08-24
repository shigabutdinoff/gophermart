package accrual

import (
	"errors"
	"fmt"
	"time"
)

var (
	// ErrNotRegistered отвечает коду 204, заказ стоит опросить позже.
	ErrNotRegistered = errors.New("order is not registered")
	// ErrUnknownStatus закрывает домен от неизвестного статуса расчёта.
	// Как и ErrMalformedResponse, повторный запрос его не исправит.
	ErrUnknownStatus = errors.New("unknown status")
	// ErrMalformedResponse помечает ответ, который не разберётся и позже.
	ErrMalformedResponse = errors.New("malformed response")
	// ErrResponseTooLarge закрывает предел тела от типа HTTP-клиента.
	ErrResponseTooLarge = errors.New("response body is too large")
)

// TooManyRequestsError несёт задержку из заголовка Retry-After.
// Ноль означает, что система расчёта задержку не назвала.
type TooManyRequestsError struct {
	RetryAfter time.Duration
}

func (e *TooManyRequestsError) Error() string {
	if e.RetryAfter <= 0 {
		return "too many requests"
	}

	return fmt.Sprintf("too many requests, retry after %s", e.RetryAfter)
}

// UnexpectedStatusError несёт код ответа вне известных исходов.
type UnexpectedStatusError struct {
	StatusCode int
}

func (e *UnexpectedStatusError) Error() string {
	return fmt.Sprintf("unexpected status %d", e.StatusCode)
}
