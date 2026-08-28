package jobs

import (
	"testing"
	"time"

	"github.com/stretchr/testify/mock"

	"github.com/shigabutdinoff/gophermart/internal/accrual"
	jobsmocks "github.com/shigabutdinoff/gophermart/internal/jobs/mocks"
	ordermocks "github.com/shigabutdinoff/gophermart/internal/order/mocks"
)

func TestCheckAccrual_WaitsOutRefusalByRate(t *testing.T) {
	tests := []struct {
		name  string
		err   error
		pause time.Duration
	}{
		{
			name:  "срок назван системой расчёта",
			err:   &accrual.TooManyRequestsError{RetryAfter: 7 * time.Second},
			pause: 7 * time.Second,
		},
		{
			name:  "срок не назван",
			err:   &accrual.TooManyRequestsError{},
			pause: 4 * time.Second,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			orders := ordermocks.NewMockResultWriter(t)
			client := newAccrualClient(t, accrual.OrderInfo{}, test.err)

			options := testOptions()
			options.ThrottleBackoff = 4 * time.Second

			err := workCheckAccrual(t, client, orders, options)

			requireSnooze(t, err, test.pause)
		})
	}
}

func TestCheckAccrual_HoldsWholeQueueOnRefusalByRate(t *testing.T) {
	held := jobsmocks.NewMockThrottler(t)
	held.EXPECT().Pause(mock.Anything, 30*time.Second).Once()
	options := testOptions()
	options.Throttle = held
	client := newAccrualClient(
		t,
		accrual.OrderInfo{},
		&accrual.TooManyRequestsError{RetryAfter: 30 * time.Second},
	)
	orders := ordermocks.NewMockResultWriter(t)

	err := workCheckAccrual(t, client, orders, options)

	requireSnooze(t, err, 30*time.Second)
}

func TestCheckAccrual_LeavesQueueAloneOnOtherAnswers(t *testing.T) {
	held := jobsmocks.NewMockThrottler(t)
	options := testOptions()
	options.Throttle = held
	client := newAccrualClient(t, accrual.OrderInfo{}, accrual.ErrNotRegistered)
	orders := ordermocks.NewMockResultWriter(t)

	err := workCheckAccrual(t, client, orders, options)

	requireSnooze(t, err, testPollInterval)
}
