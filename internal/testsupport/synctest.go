package testsupport

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
)

// WaitValue требует события, которое актор обязан выдать без хода часов.
// Голое чтение канала позволило бы часам подтянуть событие сработавшим
// таймером, и проверка выродилась бы в «когда-нибудь».
func WaitValue[T any](t *testing.T, ch <-chan T, message string) T {
	t.Helper()
	synctest.Wait()
	select {
	case value := <-ch:
		return value
	default:
		require.FailNow(t, message)
		var zero T

		return zero
	}
}

// RequireNoValue отвергает событие, которого быть не должно.
// Wait доводит до конца всё, что актор успел начать.
func RequireNoValue[T any](t *testing.T, ch <-chan T, message string) {
	t.Helper()
	synctest.Wait()
	select {
	case <-ch:
		require.FailNow(t, message)
	default:
	}
}

// WaitValueAt требует события ровно на сроке: часы сперва подходят к нему
// вплотную, и события ещё нет, а наносекунда сверху переступает срок.
func WaitValueAt[T any](t *testing.T, ch <-chan T, delay time.Duration, message string) T {
	t.Helper()
	time.Sleep(delay - time.Nanosecond)
	RequireNoValue(t, ch, message)
	time.Sleep(time.Nanosecond)

	return WaitValue(t, ch, message)
}
