package order

import (
	"strings"

	"github.com/osamingo/checkdigit"
)

// luhn не хранит состояния, поэтому создаётся один раз на пакет
var luhn = checkdigit.NewLuhn()

// Normalize снимает пробелы и переводы строки вокруг номера в теле запроса.
func Normalize(number string) string {
	return strings.TrimSpace(number)
}

// Validate ждёт уже нормализованный номер.
// Нецифровой символ и несошедшаяся контрольная сумма неотличимы для ТЗ.
func Validate(number string) error {
	if number == "" {
		return ErrEmptyNumber
	}
	// контрольная сумма одного нуля сходится, а Verify требует двух цифр
	if number == "0" {
		return nil
	}
	if !luhn.Verify(number) {
		return ErrInvalidNumber
	}

	return nil
}
