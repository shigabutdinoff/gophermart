package auth

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoginLimiterBlocksSixthAttemptWithCeilRetryAfter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		limiter := NewLoginLimiter()
		key := "user\x00192.0.2.1"

		for range MaxLoginAttempts {
			retryAfter, limited := limiter.Check(key)
			assert.False(t, limited)
			assert.Zero(t, retryAfter)
			limiter.Hit(key)
			time.Sleep(100 * time.Millisecond)
		}

		retryAfter, limited := limiter.Check(key)
		assert.True(t, limited)
		// одна попытка возвращается каждые LoginWindow/MaxLoginAttempts
		assert.Equal(t, 12, retryAfter)
	})
}

// Через время восстановления даётся ровно одна попытка, затем снова отказ.
func TestLoginLimiterReturnsOneAttemptPerRefillInterval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		limiter := NewLoginLimiter()
		key := "user\x00192.0.2.1"
		for range MaxLoginAttempts {
			limiter.Hit(key)
		}

		time.Sleep(LoginWindow / MaxLoginAttempts)

		_, limited := limiter.Check(key)
		require.False(t, limited)
		limiter.Hit(key)

		retryAfter, limitedAgain := limiter.Check(key)
		assert.True(t, limitedAgain)
		assert.Equal(t, 12, retryAfter)
	})
}

// За полное окно бездействия лимит восстанавливается целиком.
func TestLoginLimiterFullyRecoversAfterWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		limiter := NewLoginLimiter()
		key := "user\x00192.0.2.1"
		for range MaxLoginAttempts {
			limiter.Hit(key)
		}

		time.Sleep(LoginWindow)

		for range MaxLoginAttempts {
			_, limited := limiter.Check(key)
			require.False(t, limited)
			limiter.Hit(key)
		}
		_, limited := limiter.Check(key)
		assert.True(t, limited)
	})
}

// Retry-After не выходит за границы, заданные контрактом ответа.
func TestLoginLimiterRetryAfterStaysWithinBounds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		limiter := NewLoginLimiter()
		key := "user\x00192.0.2.1"
		for range MaxLoginAttempts {
			limiter.Hit(key)
		}

		for shift := range 12 {
			time.Sleep(time.Second)
			retryAfter, limited := limiter.Check(key)
			if !limited {
				break
			}
			assert.GreaterOrEqual(t, retryAfter, 1, "сдвиг %d", shift)
			assert.LessOrEqual(t, retryAfter, 12, "сдвиг %d", shift)
		}
	})
}

// Проверка лимита не должна тратить попытку, иначе отказ станет вечным.
func TestLoginLimiterCheckDoesNotConsumeAttempts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		limiter := NewLoginLimiter()
		key := "user\x00192.0.2.1"
		for range MaxLoginAttempts {
			limiter.Hit(key)
		}
		for range 10 {
			limiter.Check(key)
		}

		time.Sleep(LoginWindow)

		_, limited := limiter.Check(key)
		assert.False(t, limited)
	})
}

func TestLoginLimiterKeysAreIndependent(t *testing.T) {
	limiter := NewLoginLimiter()
	for range MaxLoginAttempts {
		limiter.Hit("user\x00192.0.2.1")
	}

	_, limitedByIP := limiter.Check("user\x00192.0.2.2")
	_, limitedByLogin := limiter.Check("other\x00192.0.2.1")

	assert.False(t, limitedByIP)
	assert.False(t, limitedByLogin)
}

func TestLoginLimiterClearResetsAttempts(t *testing.T) {
	limiter := NewLoginLimiter()
	for range 4 {
		limiter.Hit("key")
	}

	limiter.Clear("key")
	for range MaxLoginAttempts - 1 {
		limiter.Hit("key")
	}

	// после снятия ограничения ключу снова доступны все попытки
	_, limited := limiter.Check("key")
	assert.False(t, limited)
}

func TestLoginLimiterExpiresWindowAndSweepsUnusedKeys(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		limiter := NewLoginLimiter()
		limiter.Hit("stale")
		time.Sleep(LoginWindow)

		_, limited := limiter.Check("active")

		assert.False(t, limited)
		assert.NotContains(t, limiter.limiters, "stale")
	})
}

// Уборка снимает только те ключи, чей лимит восстановлен полностью.
func TestLoginLimiterSweepKeepsPartiallyRecoveredKey(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		limiter := NewLoginLimiter()
		for range MaxLoginAttempts {
			limiter.Hit("target")
		}

		// одна попытка вернулась, остальные ещё нет
		time.Sleep(LoginWindow/MaxLoginAttempts + LoginWindow)
		limiter.Hit("target")
		time.Sleep(LoginWindow)
		limiter.Check("sweep")

		assert.NotContains(t, limiter.limiters, "target")
	})
}

func TestDirectIPUsesOnlyRemoteAddressAndIgnoresPort(t *testing.T) {
	assert.Equal(t, "192.0.2.10", DirectIP("192.0.2.10:54321"))
	assert.Equal(t, "2001:db8::1", DirectIP("[2001:db8::1]:54321"))
	assert.Equal(t, "192.0.2.10", DirectIP("192.0.2.10"))
}
