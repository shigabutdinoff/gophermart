package ordernumber

import (
	"strconv"
	"strings"
	"testing"

	"github.com/osamingo/checkdigit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse(t *testing.T) {
	longNumber := numberOfLength(t, 4096)
	tests := []struct {
		name       string
		raw        string
		wantNumber string
		wantErr    error
	}{
		{name: "пустой номер", raw: " \n\t", wantErr: ErrEmpty},
		{name: "номер из ТЗ с окружающими пробелами", raw: " 12345678903\r\n", wantNumber: "12345678903"},
		{name: "второй номер из ТЗ", raw: "9278923470", wantNumber: "9278923470"},
		{name: "длинный номер", raw: "79927398713", wantNumber: "79927398713"},
		{name: "неверная контрольная сумма", raw: "12345678902", wantErr: ErrInvalid},
		{name: "буква в номере", raw: "1234567890a", wantErr: ErrInvalid},
		{name: "пробел внутри", raw: "123 45678903", wantErr: ErrInvalid},
		{name: "одноразрядный ноль", raw: " 0 ", wantNumber: "0"},
		{name: "невалидный одноразрядный номер", raw: "7", wantErr: ErrInvalid},
		{
			name:       "номер длиннее индексного предела PostgreSQL",
			raw:        longNumber,
			wantNumber: longNumber,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			number, err := Parse(test.raw)

			if test.wantErr != nil {
				require.ErrorIs(t, err, test.wantErr)
				assert.Empty(t, number)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.wantNumber, number)
		})
	}
}

// numberOfLength собирает номер нужной длины с верной контрольной цифрой.
func numberOfLength(t *testing.T, length int) string {
	t.Helper()

	seed := strings.Repeat("1", length-1)
	check, err := checkdigit.NewLuhn().Generate(seed)
	require.NoError(t, err)

	return seed + strconv.Itoa(check)
}
