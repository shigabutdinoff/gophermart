package accrual

import (
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// retryAfterHeader сообщает задержку вместе с кодом 429.
const retryAfterHeader = "Retry-After"

// maxRetryAfterSeconds ограничивает секунды заголовка пределом time.Duration.
const maxRetryAfterSeconds = math.MaxInt64 / int64(time.Second)

// retryAfter читает задержку из заголовка в секундах или в формате HTTP-даты.
// Ноль означает, что задержка не названа или уже истекла.
// Паузу опроса выбирает вызывающий, клиент лишь передаёт названный срок.
func retryAfter(header http.Header) time.Duration {
	value := strings.TrimSpace(header.Get(retryAfterHeader))
	if value == "" {
		return 0
	}
	// на переполнении ParseInt отдаёт край int64 вместе с ErrRange
	seconds, err := strconv.ParseInt(value, 10, 64)
	if err == nil || errors.Is(err, strconv.ErrRange) {
		// секунды обрезаются до умножения, иначе они переполнят Duration
		bounded := max(0, min(seconds, maxRetryAfterSeconds))

		return time.Duration(bounded) * time.Second
	}
	if date, err := http.ParseTime(value); err == nil {
		return max(0, time.Until(date))
	}

	return 0
}
