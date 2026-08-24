package money

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPointsIsNegative(t *testing.T) {
	assert.True(t, Points(-1).IsNegative())
	assert.False(t, Points(0).IsNegative())
	assert.False(t, Points(1).IsNegative())
}

func TestPointsFloat64(t *testing.T) {
	assert.InDelta(t, 500.5, Points(50050).Float64(), 1e-9)
	assert.InDelta(t, 0.05, Points(5).Float64(), 1e-9)
}

func TestPointsMarshalJSON(t *testing.T) {
	tests := []struct {
		name   string
		points Points
		want   string
	}{
		{name: "ноль", points: 0, want: "0"},
		{name: "целые рубли", points: 4200, want: "42"},
		{name: "один рубль", points: 100, want: "1"},
		{name: "десятые доли", points: 50050, want: "500.5"},
		{name: "копейки", points: 5, want: "0.05"},
		{name: "копейки без хвостового нуля", points: 55, want: "0.55"},
		{name: "отрицательная сумма", points: -50050, want: "-500.5"},
		{name: "отрицательные копейки", points: -5, want: "-0.05"},
		{name: "предел int64", points: math.MaxInt64, want: "92233720368547758.07"},
		{name: "нижний предел int64", points: math.MinInt64, want: "-92233720368547758.08"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			encoded, err := json.Marshal(test.points)
			require.NoError(t, err)
			assert.Equal(t, test.want, string(encoded))
		})
	}
}

func TestPointsUnmarshalJSON(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  Points
	}{
		{name: "целое число", input: "42", want: 4200},
		{name: "один знак после точки", input: "500.5", want: 50050},
		{name: "два знака после точки", input: "500.55", want: 50055},
		{name: "хвостовой ноль", input: "500.50", want: 50050},
		{name: "ноль", input: "0", want: 0},
		{name: "только копейки", input: "0.05", want: 5},
		{name: "отрицательная сумма", input: "-1.5", want: -150},
		{name: "предел int64", input: "92233720368547758.07", want: math.MaxInt64},
		{name: "нижний предел int64", input: "-92233720368547758.08", want: math.MinInt64},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var points Points
			require.NoError(t, json.Unmarshal([]byte(test.input), &points))
			assert.Equal(t, test.want, points)
		})
	}
}

func TestPointsUnmarshalJSONRejectsInexactValues(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "три знака после точки", input: "500.555"},
		{name: "строка вместо числа", input: `"42"`},
		{name: "экспоненциальная запись", input: "1e2"},
		{name: "дробная экспонента", input: "5.005e2"},
		{name: "логическое значение", input: "true"},
		{name: "null", input: "null"},
		{name: "рубли за пределом int64", input: "99999999999999999"},
		{name: "копейки за пределом int64", input: "92233720368547758.08"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var points Points
			require.Error(t, json.Unmarshal([]byte(test.input), &points))
		})
	}
}

func TestPointsJSONRoundTrip(t *testing.T) {
	values := []Points{0, 5, 55, 100, 4200, 50050, -50050, math.MaxInt64, math.MinInt64}
	for _, want := range values {
		encoded, err := json.Marshal(want)
		require.NoError(t, err)

		var got Points
		require.NoError(t, json.Unmarshal(encoded, &got))
		assert.Equal(t, want, got)
	}
}

func TestPointsArithmetic(t *testing.T) {
	sum, err := Points(50050).Add(5)
	require.NoError(t, err)
	assert.Equal(t, Points(50055), sum)

	difference, err := Points(50050).Sub(5)
	require.NoError(t, err)
	assert.Equal(t, Points(50045), difference)

	negative, err := Points(0).Sub(50)
	require.NoError(t, err)
	assert.Equal(t, Points(-50), negative)
}

func TestPointsArithmeticRejectsOverflow(t *testing.T) {
	tests := []struct {
		name string
		call func() (Points, error)
	}{
		{name: "сумма выше предела", call: func() (Points, error) {
			return Points(math.MaxInt64).Add(1)
		}},
		{name: "сумма ниже предела", call: func() (Points, error) {
			return Points(math.MinInt64).Add(-1)
		}},
		{name: "разность выше предела", call: func() (Points, error) {
			return Points(math.MaxInt64).Sub(-1)
		}},
		{name: "разность ниже предела", call: func() (Points, error) {
			return Points(math.MinInt64).Sub(1)
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := test.call()

			require.Error(t, err)
			assert.Zero(t, got)
		})
	}
}
