package auth

import (
	"math"
	"net"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const (
	// столько неудач подряд допускается до отказа
	MaxLoginAttempts = 5
	// за столько исчерпанный лимит восстанавливается целиком
	LoginWindow = time.Minute
)

// loginRefill возвращает по одной попытке за LoginWindow/MaxLoginAttempts.
var loginRefill = rate.Every(LoginWindow / MaxLoginAttempts)

// LoginLimiter хранит неудачные попытки входа в памяти процесса.
type LoginLimiter struct {
	mu        sync.Mutex
	limiters  map[string]*rate.Limiter
	lastSweep time.Time
}

func NewLoginLimiter() *LoginLimiter {
	return &LoginLimiter{limiters: make(map[string]*rate.Limiter)}
}

// Check сообщает, исчерпан ли лимит, и Retry-After в целых секундах.
func (l *LoginLimiter) Check(key string) (int, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	l.maintainLocked(now)
	limiter, ok := l.limiters[key]
	if !ok || limiter.TokensAt(now) >= 1 {
		return 0, false
	}

	// резервация только измеряет задержку, потратить токен она не должна
	reservation := limiter.ReserveN(now, 1)
	delay := reservation.DelayFrom(now)
	reservation.CancelAt(now)

	retryAfter := int(math.Ceil(delay.Seconds()))
	if retryAfter < 1 {
		retryAfter = 1
	}

	return retryAfter, true
}

// Hit отмечает одну неудачную проверку пароля.
func (l *LoginLimiter) Hit(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	l.maintainLocked(now)
	limiter, ok := l.limiters[key]
	if !ok {
		limiter = rate.NewLimiter(loginRefill, MaxLoginAttempts)
		l.limiters[key] = limiter
	}
	limiter.AllowN(now, 1)
}

func (l *LoginLimiter) Clear(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.maintainLocked(time.Now())
	delete(l.limiters, key)
}

// maintainLocked изредка убирает ключи с полностью восстановленным лимитом.
func (l *LoginLimiter) maintainLocked(now time.Time) {
	if !l.lastSweep.IsZero() && now.Sub(l.lastSweep) < LoginWindow {
		return
	}

	for key, limiter := range l.limiters {
		if limiter.TokensAt(now) >= MaxLoginAttempts {
			delete(l.limiters, key)
		}
	}
	l.lastSweep = now
}

// DirectIP берёт адрес пира и отбрасывает порт.
func DirectIP(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err == nil {
		if ip := net.ParseIP(host); ip != nil {
			return ip.String()
		}
		return host
	}
	if ip := net.ParseIP(remoteAddr); ip != nil {
		return ip.String()
	}

	return remoteAddr
}
