package authorization

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/golang-jwt/jwt/v5/request"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shigabutdinoff/gophermart/internal/auth"
)

var middlewareTestNow = time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC)

type middlewareResult struct {
	response      *httptest.ResponseRecorder
	request       *http.Request
	nextCalls     int
	contextUserID int64
	contextHasID  bool
}

func serveAuthorized(t *testing.T, configure func(*http.Request)) middlewareResult {
	t.Helper()

	r := httptest.NewRequest(http.MethodGet, "/protected", http.NoBody)
	if configure != nil {
		configure(r)
	}
	response := httptest.NewRecorder()

	result := middlewareResult{response: response, request: r}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		result.nextCalls++
		result.contextUserID, result.contextHasID = UserID(r.Context())
		w.WriteHeader(http.StatusNoContent)
	})

	Middleware(testManager(t).ParseRequest)(next).ServeHTTP(response, r)

	return result
}

func TestMiddleware_PrioritizesValidNonemptyBearer(t *testing.T) {
	tests := []struct {
		name        string
		cookieToken string
	}{
		{name: "invalid cookie", cookieToken: "invalid"},
		{name: "cookie for another user", cookieToken: issuedToken(t, 8)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := serveAuthorized(t, func(r *http.Request) {
				r.Header.Set("Authorization", "Bearer "+issuedToken(t, 7))
				r.AddCookie(&http.Cookie{
					Name:  SessionCookieName,
					Value: tt.cookieToken,
				})
			})

			assert.Equal(t, http.StatusNoContent, result.response.Code)
			assert.Empty(t, result.response.Body.Bytes())
			assert.Equal(t, 1, result.nextCalls)
			assert.Equal(t, int64(7), result.contextUserID)
			assert.True(t, result.contextHasID)
		})
	}
}

func TestMiddleware_RejectsInvalidSelectedBearerWithoutCookieFallback(t *testing.T) {
	result := serveAuthorized(t, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer invalid")
		r.AddCookie(&http.Cookie{
			Name:  SessionCookieName,
			Value: issuedToken(t, 7),
		})
	})

	assert.Equal(t, http.StatusUnauthorized, result.response.Code)
	assert.Empty(t, result.response.Body.Bytes())
	assert.Zero(t, result.nextCalls)
}

func TestMiddleware_FallsBackToCookieWhenBearerIsAbsentOrUnrecognized(t *testing.T) {
	tests := []struct {
		name          string
		authorization string
	}{
		{name: "absent"},
		{name: "wrong scheme", authorization: "Basic ignored"},
		{name: "scheme without token separator", authorization: "Bearer"},
		{name: "tab separator", authorization: "Bearer\tignored"},
		{name: "unicode separator", authorization: "Bearer\u00a0ignored"},
		{name: "empty bearer", authorization: "Bearer "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := serveAuthorized(t, func(r *http.Request) {
				if tt.authorization != "" {
					r.Header.Set("Authorization", tt.authorization)
				}
				r.AddCookie(&http.Cookie{
					Name:  SessionCookieName,
					Value: issuedToken(t, 7),
				})
			})

			assert.Equal(t, http.StatusNoContent, result.response.Code)
			assert.Empty(t, result.response.Body.Bytes())
			assert.Equal(t, 1, result.nextCalls)
			assert.Equal(t, int64(7), result.contextUserID)
			assert.True(t, result.contextHasID)
		})
	}
}

func TestMiddleware_AcceptsCaseInsensitiveBearerScheme(t *testing.T) {
	for _, scheme := range []string{"bearer", "BEARER", "bEaReR"} {
		t.Run(scheme, func(t *testing.T) {
			result := serveAuthorized(t, func(r *http.Request) {
				r.Header.Set("Authorization", scheme+" "+issuedToken(t, 7))
			})

			assert.Equal(t, http.StatusNoContent, result.response.Code)
			assert.Equal(t, 1, result.nextCalls)
			assert.Equal(t, int64(7), result.contextUserID)
		})
	}
}

func TestMiddleware_UsesFirstAuthorizationValue(t *testing.T) {
	tests := []struct {
		name       string
		values     []string
		cookie     string
		wantStatus int
		wantUserID int64
		wantNext   int
	}{
		{
			name:       "first bearer",
			values:     []string{"Bearer " + issuedToken(t, 7), "Bearer " + issuedToken(t, 8)},
			wantStatus: http.StatusNoContent,
			wantUserID: 7,
			wantNext:   1,
		},
		{
			name:       "first unrecognized value permits cookie fallback",
			values:     []string{"Basic ignored", "Bearer " + issuedToken(t, 8)},
			cookie:     issuedToken(t, 7),
			wantStatus: http.StatusNoContent,
			wantUserID: 7,
			wantNext:   1,
		},
		{
			name:       "first invalid bearer rejects later credentials",
			values:     []string{"Bearer invalid", "Bearer " + issuedToken(t, 7)},
			cookie:     issuedToken(t, 7),
			wantStatus: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := serveAuthorized(t, func(r *http.Request) {
				for _, value := range tt.values {
					r.Header.Add("Authorization", value)
				}
				if tt.cookie != "" {
					r.AddCookie(&http.Cookie{
						Name:  SessionCookieName,
						Value: tt.cookie,
					})
				}
			})

			assert.Equal(t, tt.wantStatus, result.response.Code)
			assert.Empty(t, result.response.Body.Bytes())
			assert.Equal(t, tt.wantNext, result.nextCalls)
			if tt.wantNext > 0 {
				assert.Equal(t, tt.wantUserID, result.contextUserID)
				assert.True(t, result.contextHasID)
			}
		})
	}
}

func TestMiddleware_UsesFirstSessionCookie(t *testing.T) {
	name := SessionCookieName
	tests := []struct {
		name        string
		cookieLines []string
	}{
		{
			name:        "within one header value",
			cookieLines: []string{name + "=" + issuedToken(t, 7) + "; " + name + "=" + issuedToken(t, 8)},
		},
		{
			name: "across header values",
			cookieLines: []string{
				name + "=" + issuedToken(t, 7),
				name + "=" + issuedToken(t, 8),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := serveAuthorized(t, func(r *http.Request) {
				for _, line := range tt.cookieLines {
					r.Header.Add("Cookie", line)
				}
			})

			assert.Equal(t, http.StatusNoContent, result.response.Code)
			assert.Empty(t, result.response.Body.Bytes())
			assert.Equal(t, 1, result.nextCalls)
			assert.Equal(t, int64(7), result.contextUserID)
			assert.True(t, result.contextHasID)
		})
	}
}

func TestMiddleware_RejectsMissingInvalidOrNonpositiveCredentials(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*http.Request)
	}{
		{name: "neither credential"},
		{
			name: "invalid selected cookie",
			configure: func(r *http.Request) {
				r.AddCookie(&http.Cookie{
					Name:  SessionCookieName,
					Value: "invalid",
				})
			},
		},
		{
			name: "empty session cookie",
			configure: func(r *http.Request) {
				r.Header.Set("Cookie", SessionCookieName+"=")
			},
		},
		{
			name: "expired token",
			configure: func(r *http.Request) {
				r.Header.Set("Authorization", "Bearer "+signedToken(map[string]any{
					auth.UserIDClaim: int64(7),
					"iat":            middlewareTestNow.Add(-2 * time.Hour).Unix(),
					"exp":            middlewareTestNow.Add(-time.Hour).Unix(),
				}))
			},
		},
		{
			name: "zero user id",
			configure: func(r *http.Request) {
				r.Header.Set("Authorization", "Bearer "+signedToken(validClaims(int64(0))))
			},
		},
		{
			name: "negative user id",
			configure: func(r *http.Request) {
				r.AddCookie(&http.Cookie{
					Name:  SessionCookieName,
					Value: signedToken(validClaims(int64(-1))),
				})
			},
		},
		{
			name: "missing user id",
			configure: func(r *http.Request) {
				r.Header.Set("Authorization", "Bearer "+signedToken(map[string]any{
					"iat": middlewareTestNow.Unix(),
					"exp": middlewareTestNow.Add(time.Hour).Unix(),
				}))
			},
		},
		{
			name: "foreign signature",
			configure: func(r *http.Request) {
				r.Header.Set("Authorization", "Bearer "+signedTokenWith(
					[]byte(strings.Repeat("w", 32)),
					validClaims(int64(7)),
				))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := serveAuthorized(t, tt.configure)

			assert.Equal(t, http.StatusUnauthorized, result.response.Code)
			assert.Empty(t, result.response.Body.Bytes())
			assert.Zero(t, result.nextCalls)
		})
	}
}

func TestMiddleware_StoresUserIDWithoutTouchingOriginalRequest(t *testing.T) {
	result := serveAuthorized(t, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+issuedToken(t, 7))
	})

	_, ok := UserID(result.request.Context())
	assert.False(t, ok)
	assert.Equal(t, 1, result.nextCalls)
	assert.Equal(t, int64(7), result.contextUserID)
	assert.True(t, result.contextHasID)
}

func TestMiddleware_CallsParserOnceWithOrderedExtractor(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/protected", http.NoBody)
	r.Header.Set("Authorization", "Bearer selected")
	r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "fallback"})
	w := httptest.NewRecorder()
	parserCalls := 0
	selected := ""
	parser := func(r *http.Request, extractor request.Extractor) (int64, error) {
		parserCalls++
		var err error
		selected, err = extractor.ExtractToken(r)
		return 7, err
	}
	nextCalls := 0
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { nextCalls++ })

	Middleware(parser)(next).ServeHTTP(w, r)

	assert.Equal(t, 1, parserCalls)
	assert.Equal(t, "selected", selected)
	assert.Equal(t, 1, nextCalls)
}

func TestMiddleware_MalformedOrUnrelatedCookiesDoNotDisturbSelectedBearer(t *testing.T) {
	cookieLines := []string{
		`gophermart_session="unterminated`,
		"unrelated",
		`unrelated="unterminated`,
		"theme=dark; malformed",
	}

	for _, cookieLine := range cookieLines {
		t.Run(cookieLine, func(t *testing.T) {
			result := serveAuthorized(t, func(r *http.Request) {
				r.Header.Set("Authorization", "Bearer "+issuedToken(t, 7))
				r.Header.Set("Cookie", cookieLine)
			})

			assert.Equal(t, http.StatusNoContent, result.response.Code)
			assert.Empty(t, result.response.Body.Bytes())
			assert.Equal(t, 1, result.nextCalls)
			assert.Equal(t, int64(7), result.contextUserID)
			assert.True(t, result.contextHasID)
		})
	}
}

func TestUserID_ReturnsOnlyPositiveTypedValue(t *testing.T) {
	tests := []struct {
		name  string
		value any
		want  int64
		ok    bool
	}{
		{name: "positive", value: int64(7), want: 7, ok: true},
		{name: "zero", value: int64(0)},
		{name: "negative", value: int64(-1), want: -1},
		{name: "wrong type", value: 7},
		{name: "missing"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			if tt.value != nil {
				ctx = context.WithValue(ctx, contextKey{}, tt.value)
			}

			got, ok := UserID(ctx)

			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.ok, ok)
		})
	}
}

func testSecret() []byte {
	return []byte(strings.Repeat("s", 32))
}

func testManager(t *testing.T) *auth.JWTManager {
	t.Helper()

	manager, err := auth.NewJWTManager(testSecret(), func() time.Time { return middlewareTestNow })
	require.NoError(t, err)

	return manager
}

func issuedToken(t *testing.T, userID int64) string {
	t.Helper()

	issued, err := testManager(t).Issue(userID)
	require.NoError(t, err)

	return issued.Value
}

func validClaims(userID int64) map[string]any {
	return map[string]any{
		auth.UserIDClaim: userID,
		"iat":            middlewareTestNow.Unix(),
		"exp":            middlewareTestNow.Add(time.Hour).Unix(),
	}
}

func signedToken(claims map[string]any) string {
	return signedTokenWith(testSecret(), claims)
}

func signedTokenWith(secret []byte, claims map[string]any) string {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims(claims))
	signed, err := token.SignedString(secret)
	if err != nil {
		panic(err)
	}

	return signed
}
