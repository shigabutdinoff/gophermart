package order

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeTrimsSurroundingWhitespace(t *testing.T) {
	assert.Equal(t, "12345678903", Normalize(" 12345678903\r\n"))
	assert.Empty(t, Normalize("\n\t "))
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name   string
		number string
		want   error
	}{
		{name: "пустой номер", number: "", want: ErrEmptyNumber},
		{name: "номер из ТЗ", number: "12345678903"},
		{name: "второй номер из ТЗ", number: "9278923470"},
		{name: "длинный номер", number: "79927398713"},
		{name: "неверная контрольная сумма", number: "12345678902", want: ErrInvalidNumber},
		{name: "буква в номере", number: "1234567890a", want: ErrInvalidNumber},
		{name: "пробел внутри", number: "123 45678903", want: ErrInvalidNumber},
		{name: "одноразрядный ноль", number: "0"},
		{name: "невалидный одноразрядный номер", number: "7", want: ErrInvalidNumber},
		{name: "номер длиннее индексного предела PostgreSQL", number: numberOfLength(t, 4096)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := Validate(test.number)

			if test.want == nil {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, test.want)
		})
	}
}

// numberOfLength собирает номер нужной длины с верной контрольной цифрой.
func numberOfLength(t *testing.T, length int) string {
	t.Helper()

	seed := strings.Repeat("1", length-1)
	check, err := luhn.Generate(seed)
	require.NoError(t, err)

	return seed + strconv.Itoa(check)
}
