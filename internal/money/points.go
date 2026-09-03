package money

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Points хранит баллы лояльности в копейках.
type Points int64

// kopecksPerPoint задаёт масштаб хранения балла.
const kopecksPerPoint = 100

// fractionDigits ограничивает точность входящей суммы.
const fractionDigits = 2

// nullLiteral отделяет отсутствующую сумму от нуля.
const nullLiteral = "null"

// Float64 возвращает сумму в рублях.
func (p Points) Float64() float64 {
	return float64(p) / kopecksPerPoint
}

// IsNegative отвечает, что сумма меньше нуля.
func (p Points) IsNegative() bool {
	return p < 0
}

// Add возвращает сумму баллов, отвергая переполнение.
func (p Points) Add(other Points) (Points, error) {
	sum := p + other
	if (other > 0 && sum < p) || (other < 0 && sum > p) {
		return 0, fmt.Errorf("money: sum of %d and %d kopecks is out of range", p, other)
	}

	return sum, nil
}

// Sub возвращает разность баллов, отвергая переполнение.
func (p Points) Sub(other Points) (Points, error) {
	difference := p - other
	if (other < 0 && difference < p) || (other > 0 && difference > p) {
		return 0, fmt.Errorf(
			"money: difference of %d and %d kopecks is out of range",
			p,
			other,
		)
	}

	return difference, nil
}

// MarshalJSON собирает число из целых частей, float в выводе не участвует.
func (p Points) MarshalJSON() ([]byte, error) {
	sign := ""
	amount := uint64(p)
	if p < 0 {
		sign = "-"
		amount = -amount
	}

	rubles := strconv.FormatUint(amount/kopecksPerPoint, 10)
	kopecks := amount % kopecksPerPoint
	if kopecks == 0 {
		return []byte(sign + rubles), nil
	}

	fraction := strings.TrimRight(fmt.Sprintf("%02d", kopecks), "0")

	return []byte(sign + rubles + "." + fraction), nil
}

// UnmarshalJSON принимает сумму не точнее копейки, отвергая остальные формы.
func (p *Points) UnmarshalJSON(data []byte) error {
	// null и строку с числом json.Number принял бы молча
	if string(data) == nullLiteral {
		return fmt.Errorf("money: amount must not be null")
	}
	if len(data) > 0 && data[0] == '"' {
		return fmt.Errorf("money: amount must be a JSON number")
	}

	var number json.Number
	if err := json.Unmarshal(data, &number); err != nil {
		return err
	}

	points, err := parseAmount(number)
	if err != nil {
		return err
	}
	*p = points

	return nil
}

// parseAmount переводит десятичную запись суммы в копейки.
// Дробная часть дописывается к целой, предел int64 меряет сам ParseInt.
func parseAmount(number json.Number) (Points, error) {
	whole, fraction, _ := strings.Cut(number.String(), ".")
	if len(fraction) > fractionDigits {
		return 0, fmt.Errorf("money: amount is more precise than a kopeck")
	}

	padded := whole + fraction + strings.Repeat("0", fractionDigits-len(fraction))
	kopecks, err := strconv.ParseInt(padded, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("money: parse amount %s: %w", number, err)
	}

	return Points(kopecks), nil
}
