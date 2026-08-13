package auth

import (
	"bytes"
	"fmt"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/golang-jwt/jwt/v5/request"
)

const (
	TokenTTL = time.Hour
	// допуск на расхождение часов при проверке токена
	TokenLeeway = time.Minute
	UserIDClaim = "user_id"
)

type jwtClaims struct {
	UserID int64 `json:"user_id"`
	jwt.RegisteredClaims
}

// JWTManager выпускает и проверяет токены HS256.
type JWTManager struct {
	secret []byte
	parser *jwt.Parser
	now    Clock
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
		secret: bytes.Clone(secret),
		parser: jwt.NewParser(
			jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
			jwt.WithExpirationRequired(),
			jwt.WithIssuedAt(),
			jwt.WithTimeFunc(now),
			jwt.WithLeeway(TokenLeeway),
		),
		now: now,
	}, nil
}

func (m *JWTManager) Issue(userID int64) (IssuedToken, error) {
	if userID <= 0 {
		return IssuedToken{}, fmt.Errorf("user ID must be positive")
	}

	issuedAt := m.now().UTC().Truncate(time.Second)
	expiresAt := issuedAt.Add(TokenTTL)
	claims := jwtClaims{
		UserID: userID,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(issuedAt),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
	}
	value, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(m.secret)
	if err != nil {
		return IssuedToken{}, fmt.Errorf("sign JWT: %w", err)
	}

	return IssuedToken{
		Value:     value,
		IssuedAt:  issuedAt,
		ExpiresAt: expiresAt,
	}, nil
}

// ParseRequest достаёт из запроса ровно один токен и проверяет его.
func (m *JWTManager) ParseRequest(r *http.Request, extractor request.Extractor) (int64, error) {
	claims := jwtClaims{}
	_, err := request.ParseFromRequest(
		r,
		extractor,
		func(*jwt.Token) (any, error) { return m.secret, nil },
		request.WithClaims(&claims),
		request.WithParser(m.parser),
	)
	if err != nil {
		return 0, fmt.Errorf("parse JWT: %w", err)
	}
	if claims.IssuedAt == nil {
		return 0, fmt.Errorf("missing iat claim")
	}
	if claims.UserID <= 0 {
		return 0, fmt.Errorf("invalid %s claim", UserIDClaim)
	}

	return claims.UserID, nil
}
