package auth

import (
	"bytes"
	"fmt"
	"time"

	"github.com/go-chi/jwtauth/v5"
	"github.com/lestrrat-go/jwx/v3/jwt"
)

const (
	// TokenTTL is the validity period of newly issued tokens.
	TokenTTL = time.Hour
	// UserIDClaim carries the authenticated user ID.
	UserIDClaim = "user_id"
)

// JWTManager выпускает и проверяет токены HS256.
type JWTManager struct {
	auth *jwtauth.JWTAuth
	now  Clock
}

// NewJWTManager забирает копию ключа, чужие правки на неё не влияют.
func NewJWTManager(secret []byte, now Clock) (*JWTManager, error) {
	if len(secret) < SecretSize {
		return nil, fmt.Errorf("JWT secret must be at least %d bytes", SecretSize)
	}
	if now == nil {
		return nil, fmt.Errorf("JWT clock must not be nil")
	}

	return &JWTManager{
		auth: jwtauth.New(
			"HS256",
			bytes.Clone(secret),
			nil,
			jwt.WithClock(jwt.ClockFunc(now)),
			jwt.WithAcceptableSkew(time.Minute),
			jwt.WithRequiredClaim(jwt.IssuedAtKey),
			jwt.WithRequiredClaim(jwt.ExpirationKey),
		),
		now: now,
	}, nil
}

// Auth returns the verifier backing the authorization middleware.
func (m *JWTManager) Auth() *jwtauth.JWTAuth {
	return m.auth
}

func (m *JWTManager) Issue(userID int64) (IssuedToken, error) {
	if userID <= 0 {
		return IssuedToken{}, fmt.Errorf("user ID must be positive")
	}

	issuedAt := m.now().UTC().Truncate(time.Second)
	expiresAt := issuedAt.Add(TokenTTL)
	_, value, err := m.auth.Encode(map[string]any{
		UserIDClaim:       userID,
		jwt.IssuedAtKey:   issuedAt.Unix(),
		jwt.ExpirationKey: expiresAt.Unix(),
	})
	if err != nil {
		return IssuedToken{}, fmt.Errorf("sign JWT: %w", err)
	}

	return IssuedToken{
		Value:     value,
		IssuedAt:  issuedAt,
		ExpiresAt: expiresAt,
	}, nil
}

// UserIDFromToken returns the positive user ID stored in a verified token.
func UserIDFromToken(token jwt.Token) (int64, bool) {
	if token == nil {
		return 0, false
	}

	// Разобранный claim приходит числом JSON, выпущенный — целым Go.
	var claim any
	if err := token.Get(UserIDClaim, &claim); err != nil {
		return 0, false
	}

	switch value := claim.(type) {
	case int64:
		return value, value > 0
	case float64:
		userID := int64(value)
		return userID, float64(userID) == value && userID > 0
	default:
		return 0, false
	}
}
