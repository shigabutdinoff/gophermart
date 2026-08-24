package accrual

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shigabutdinoff/gophermart/internal/order"
)

func TestStatusToOrderStatus(t *testing.T) {
	tests := []struct {
		name   string
		status Status
		want   order.Status
	}{
		{name: "зарегистрирован", status: StatusRegistered, want: order.StatusProcessing},
		{name: "расчёт идёт", status: StatusProcessing, want: order.StatusProcessing},
		{name: "отказ в расчёте", status: StatusInvalid, want: order.StatusInvalid},
		{name: "расчёт окончен", status: StatusProcessed, want: order.StatusProcessed},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := test.status.ToOrderStatus()

			require.NoError(t, err)
			assert.Equal(t, test.want, got)
		})
	}
}

func TestStatusToOrderStatusRejectsUnknown(t *testing.T) {
	tests := []struct {
		name   string
		status Status
	}{
		{name: "пустой статус", status: ""},
		{name: "чужой статус", status: "FOO"},
		{name: "нижний регистр", status: "processed"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := test.status.ToOrderStatus()

			require.ErrorIs(t, err, ErrUnknownStatus)
		})
	}
}
