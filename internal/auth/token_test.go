package auth

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/jwtauth/v5"
	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jws"
	"github.com/lestrrat-go/jwx/v3/jwt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var jwtTestNow = time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC)

func TestJWTManagerIssueAndVerify(t *testing.T) {
	manager := newJWTTestManager(t, jwtTestSecret())

	issued, err := manager.Issue(42)
	require.NoError(t, err)
	require.NotEmpty(t, issued.Value)
	assert.WithinDuration(t, jwtTestNow, issued.IssuedAt, 0)
	assert.WithinDuration(t, jwtTestNow.Add(time.Hour), issued.ExpiresAt, 0)

	userID, err := verifyJWTTestToken(manager, issued.Value)
	require.NoError(t, err)
	assert.Equal(t, int64(42), userID)

	message, err := jws.Parse([]byte(issued.Value))
	require.NoError(t, err)
	require.Len(t, message.Signatures(), 1)
	algorithm, ok := message.Signatures()[0].ProtectedHeaders().Algorithm()
	require.True(t, ok)
	assert.Equal(t, jwa.HS256(), algorithm)

	token, err := jwt.Parse([]byte(issued.Value), jwt.WithVerify(false), jwt.WithValidate(false))
	require.NoError(t, err)
	issuedAt, ok := token.IssuedAt()
	require.True(t, ok)
	assert.WithinDuration(t, jwtTestNow, issuedAt, 0)
	expiration, ok := token.Expiration()
	require.True(t, ok)
	assert.WithinDuration(t, jwtTestNow.Add(time.Hour), expiration, 0)
}

func TestJWTManagerIssueRejectsNonPositiveUserID(t *testing.T) {
	manager := newJWTTestManager(t, jwtTestSecret())

	for _, userID := range []int64{-1, 0} {
		t.Run(strconv.FormatInt(userID, 10), func(t *testing.T) {
			_, err := manager.Issue(userID)
			require.Error(t, err)
		})
	}
}

func TestJWTManagerRejectsInvalidConfiguration(t *testing.T) {
	t.Run("short secret", func(t *testing.T) {
		_, err := NewJWTManager([]byte("short"), jwtTestClock)
		require.Error(t, err)
	})

	t.Run("nil clock", func(t *testing.T) {
		_, err := NewJWTManager(jwtTestSecret(), nil)
		require.Error(t, err)
	})
}

func TestJWTManagerCopiesSecret(t *testing.T) {
	secret := jwtTestSecret()
	manager := newJWTTestManager(t, secret)

	issued, err := manager.Issue(7)
	require.NoError(t, err)
	secret[0] ^= 0xff

	userID, err := verifyJWTTestToken(manager, issued.Value)
	require.NoError(t, err)
	assert.Equal(t, int64(7), userID)
}

func TestJWTManagerAppliesLeewayToIssuedAt(t *testing.T) {
	tests := []struct {
		name    string
		offset  time.Duration
		wantErr bool
	}{
		{"at sixty seconds", time.Minute, false},
		{"after sixty seconds", time.Minute + time.Second, true},
	}
	manager := newJWTTestManager(t, jwtTestSecret())
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token := signJWTTestToken(t, jwtTestSecret(), map[string]any{
				UserIDClaim:       int64(7),
				jwt.IssuedAtKey:   jwtTestNow.Add(tt.offset),
				jwt.ExpirationKey: jwtTestNow.Add(5 * time.Minute),
			})

			_, err := verifyJWTTestToken(manager, token)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestJWTManagerAppliesLeewayToExpiration(t *testing.T) {
	tests := []struct {
		name    string
		offset  time.Duration
		wantErr bool
	}{
		{"expired fifty nine seconds ago", -59 * time.Second, false},
		{"expired sixty seconds ago", -time.Minute, true},
	}
	manager := newJWTTestManager(t, jwtTestSecret())
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token := signJWTTestToken(t, jwtTestSecret(), map[string]any{
				UserIDClaim:       int64(7),
				jwt.IssuedAtKey:   jwtTestNow.Add(-time.Hour),
				jwt.ExpirationKey: jwtTestNow.Add(tt.offset),
			})

			_, err := verifyJWTTestToken(manager, token)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestJWTManagerDoesNotRequireIssuedTTL(t *testing.T) {
	manager := newJWTTestManager(t, jwtTestSecret())
	token := signJWTTestToken(t, jwtTestSecret(), map[string]any{
		UserIDClaim:       int64(7),
		jwt.IssuedAtKey:   jwtTestNow.Add(-2 * time.Hour),
		jwt.ExpirationKey: jwtTestNow.Add(30 * time.Second),
	})

	userID, err := verifyJWTTestToken(manager, token)
	require.NoError(t, err)
	assert.Equal(t, int64(7), userID)
}

func TestJWTManagerRequiresIssuedAtAndExpiration(t *testing.T) {
	tests := []struct {
		name    string
		omitted string
	}{
		{"missing iat", jwt.IssuedAtKey},
		{"missing exp", jwt.ExpirationKey},
	}
	manager := newJWTTestManager(t, jwtTestSecret())
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claims := validJWTTestClaims()
			delete(claims, tt.omitted)
			token := signJWTTestToken(t, jwtTestSecret(), claims)

			_, err := verifyJWTTestToken(manager, token)
			require.Error(t, err)
		})
	}
}

func TestUserIDFromTokenRejectsInvalidUserID(t *testing.T) {
	tests := []struct {
		name    string
		value   any
		omitted bool
	}{
		{name: "missing", omitted: true},
		{name: "zero", value: int64(0)},
		{name: "negative", value: int64(-7)},
		{name: "fractional", value: 7.5},
		{name: "string", value: "7"},
	}

	manager := newJWTTestManager(t, jwtTestSecret())
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claims := validJWTTestClaims()
			if tt.omitted {
				delete(claims, UserIDClaim)
			} else {
				claims[UserIDClaim] = tt.value
			}
			token := signJWTTestToken(t, jwtTestSecret(), claims)

			_, err := verifyJWTTestToken(manager, token)
			require.ErrorIs(t, err, errInvalidUserIDClaim)
		})
	}
}

func TestJWTManagerRejectsWrongSignature(t *testing.T) {
	manager := newJWTTestManager(t, jwtTestSecret())
	wrongSecret := []byte(strings.Repeat("w", 32))
	token := signJWTTestToken(t, wrongSecret, validJWTTestClaims())

	_, err := verifyJWTTestToken(manager, token)
	require.Error(t, err)
}

func TestJWTManagerRejectsNonHS256Algorithm(t *testing.T) {
	manager := newJWTTestManager(t, jwtTestSecret())
	token := signJWTTestTokenWith(t, jwa.HS384(), jwtTestSecret(), validJWTTestClaims())

	_, err := verifyJWTTestToken(manager, token)
	require.Error(t, err)
}

var errInvalidUserIDClaim = errors.New("invalid user_id claim")

// verifyJWTTestToken повторяет путь мидлвари с чтением user_id
func verifyJWTTestToken(manager *JWTManager, token string) (int64, error) {
	verified, err := jwtauth.VerifyToken(manager.Auth(), token)
	if err != nil {
		return 0, err
	}

	userID, ok := UserIDFromToken(verified)
	if !ok {
		return 0, errInvalidUserIDClaim
	}

	return userID, nil
}

func newJWTTestManager(t *testing.T, secret []byte) *JWTManager {
	t.Helper()

	manager, err := NewJWTManager(secret, jwtTestClock)
	require.NoError(t, err)

	return manager
}

func jwtTestClock() time.Time {
	return jwtTestNow
}

func jwtTestSecret() []byte {
	return []byte(strings.Repeat("s", 32))
}

func validJWTTestClaims() map[string]any {
	return map[string]any{
		UserIDClaim:       int64(7),
		jwt.IssuedAtKey:   jwtTestNow,
		jwt.ExpirationKey: jwtTestNow.Add(5 * time.Minute),
	}
}

func signJWTTestToken(t *testing.T, secret []byte, claims map[string]any) string {
	t.Helper()

	return signJWTTestTokenWith(t, jwa.HS256(), secret, claims)
}

func signJWTTestTokenWith(
	t *testing.T,
	algorithm jwa.SignatureAlgorithm,
	secret []byte,
	claims map[string]any,
) string {
	t.Helper()

	builder := jwt.NewBuilder()
	for name, value := range claims {
		builder = builder.Claim(name, value)
	}
	token, err := builder.Build()
	require.NoError(t, err)

	signed, err := jwt.Sign(token, jwt.WithKey(algorithm, secret))
	require.NoError(t, err)

	return string(signed)
}
