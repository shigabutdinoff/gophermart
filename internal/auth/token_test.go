package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/golang-jwt/jwt/v5/request"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJWTManagerIssueAndVerify(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		manager := newJWTTestManager(t, jwtTestSecret())
		now := time.Now()

		issued, err := manager.Issue(42)
		require.NoError(t, err)
		require.NotEmpty(t, issued.Value)
		assert.WithinDuration(t, now, issued.IssuedAt, 0)
		assert.WithinDuration(t, now.Add(time.Hour), issued.ExpiresAt, 0)

		userID, err := verifyJWTTestToken(manager, issued.Value)
		require.NoError(t, err)
		assert.Equal(t, int64(42), userID)

		parsed, _, err := jwt.NewParser().ParseUnverified(issued.Value, jwt.MapClaims{})
		require.NoError(t, err)
		assert.Equal(t, jwt.SigningMethodHS256.Alg(), parsed.Method.Alg())

		claims := jwt.MapClaims{}
		_, _, err = jwt.NewParser().ParseUnverified(issued.Value, claims)
		require.NoError(t, err)
		issuedAt, err := claims.GetIssuedAt()
		require.NoError(t, err)
		require.NotNil(t, issuedAt)
		assert.WithinDuration(t, now, issuedAt.Time, 0)
		expiration, err := claims.GetExpirationTime()
		require.NoError(t, err)
		require.NotNil(t, expiration)
		assert.WithinDuration(t, now.Add(time.Hour), expiration.Time, 0)
	})
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

func TestJWTManagerRejectsShortSecret(t *testing.T) {
	_, err := NewJWTManager([]byte("short"))
	require.Error(t, err)
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
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				manager := newJWTTestManager(t, jwtTestSecret())
				now := time.Now()
				token := signJWTTestToken(t, jwtTestSecret(), map[string]any{
					UserIDClaim: int64(7),
					"iat":       now.Add(tt.offset).Unix(),
					"exp":       now.Add(5 * time.Minute).Unix(),
				})

				_, err := verifyJWTTestToken(manager, token)
				if tt.wantErr {
					require.Error(t, err)
					return
				}
				require.NoError(t, err)
			})
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
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				manager := newJWTTestManager(t, jwtTestSecret())
				now := time.Now()
				token := signJWTTestToken(t, jwtTestSecret(), map[string]any{
					UserIDClaim: int64(7),
					"iat":       now.Add(-time.Hour).Unix(),
					"exp":       now.Add(tt.offset).Unix(),
				})

				_, err := verifyJWTTestToken(manager, token)
				if tt.wantErr {
					require.Error(t, err)
					return
				}
				require.NoError(t, err)
			})
		})
	}
}

func TestJWTManagerDoesNotRequireIssuedTTL(t *testing.T) {
	manager := newJWTTestManager(t, jwtTestSecret())
	now := time.Now()
	token := signJWTTestToken(t, jwtTestSecret(), map[string]any{
		UserIDClaim: int64(7),
		"iat":       now.Add(-2 * time.Hour).Unix(),
		"exp":       now.Add(30 * time.Second).Unix(),
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
		{"missing iat", "iat"},
		{"missing exp", "exp"},
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

func TestJWTManagerParseRequestRejectsInvalidUserID(t *testing.T) {
	tests := []struct {
		name    string
		value   any
		omitted bool
	}{
		{name: "missing", omitted: true},
		{name: "zero", value: int64(0)},
		{name: "negative", value: int64(-7)},
		{name: "fractional", value: 7.5},
		{name: "exponent", value: json.Number("7e0")},
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
			require.Error(t, err)
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
	token := signJWTTestTokenWith(t, jwt.SigningMethodHS384, jwtTestSecret(), validJWTTestClaims())

	_, err := verifyJWTTestToken(manager, token)
	require.Error(t, err)
}

func verifyJWTTestToken(manager *JWTManager, token string) (int64, error) {
	r := httptest.NewRequest(http.MethodGet, "/protected", http.NoBody)
	r.Header.Set("Authorization", "Bearer "+token)

	return manager.ParseRequest(r, request.BearerExtractor{})
}

func newJWTTestManager(t *testing.T, secret []byte) *JWTManager {
	t.Helper()

	manager, err := NewJWTManager(secret)
	require.NoError(t, err)

	return manager
}

func jwtTestSecret() []byte {
	return []byte(strings.Repeat("s", 32))
}

func validJWTTestClaims() map[string]any {
	now := time.Now()

	return map[string]any{
		UserIDClaim: int64(7),
		"iat":       now.Unix(),
		"exp":       now.Add(5 * time.Minute).Unix(),
	}
}

func signJWTTestToken(t *testing.T, secret []byte, claims map[string]any) string {
	t.Helper()

	return signJWTTestTokenWith(t, jwt.SigningMethodHS256, secret, claims)
}

func signJWTTestTokenWith(
	t *testing.T,
	algorithm jwt.SigningMethod,
	secret []byte,
	claims map[string]any,
) string {
	t.Helper()

	token := jwt.NewWithClaims(algorithm, jwt.MapClaims(claims))
	signed, err := token.SignedString(secret)
	require.NoError(t, err)

	return signed
}
