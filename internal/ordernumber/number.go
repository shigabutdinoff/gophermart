package ordernumber

import (
	"errors"
	"strings"

	"github.com/osamingo/checkdigit"
)

var (
	ErrEmpty   = errors.New("order number is empty")
	ErrInvalid = errors.New("order number is invalid")
)

// luhn не хранит состояния, поэтому создаётся один раз на пакет
var luhn = checkdigit.NewLuhn()

// Parse снимает окружающие пробелы и проверяет контрольную сумму номера.
func Parse(raw string) (string, error) {
	number := strings.TrimSpace(raw)
	if number == "" {
		return "", ErrEmpty
	}
	// контрольная сумма одного нуля сходится, а Verify требует двух цифр
	if number == "0" {
		return number, nil
	}
	if !luhn.Verify(number) {
		return "", ErrInvalid
	}

	return number, nil
}
