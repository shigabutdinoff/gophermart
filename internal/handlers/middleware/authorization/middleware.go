package authorization

import (
	"errors"
	"net/http"

	"github.com/golang-jwt/jwt/v5/request"
)

// SessionCookieName называет cookie с тем же токеном, что и заголовок Bearer.
const SessionCookieName = "gophermart_session"

// TokenParser проверяет один токен из запроса.
type TokenParser func(*http.Request, request.Extractor) (int64, error)

// Middleware пропускает запрос с токеном в заголовке Bearer или в cookie.
func Middleware(parse TokenParser) func(http.Handler) http.Handler {
	extractor := request.MultiExtractor{
		nonemptyExtractor{Extractor: request.BearerExtractor{}},
		sessionCookieExtractor{},
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userID, err := parse(r, extractor)
			if err != nil || userID <= 0 {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}

			next.ServeHTTP(w, r.WithContext(withUserID(r.Context(), userID)))
		})
	}
}

type nonemptyExtractor struct {
	request.Extractor
}

func (e nonemptyExtractor) ExtractToken(r *http.Request) (string, error) {
	token, err := e.Extractor.ExtractToken(r)
	if token == "" && err == nil {
		return "", request.ErrNoTokenInRequest
	}

	return token, err
}

type sessionCookieExtractor struct{}

func (sessionCookieExtractor) ExtractToken(r *http.Request) (string, error) {
	cookie, err := r.Cookie(SessionCookieName)
	if errors.Is(err, http.ErrNoCookie) {
		return "", request.ErrNoTokenInRequest
	}
	if err != nil {
		return "", err
	}
	if cookie.Value == "" {
		return "", request.ErrNoTokenInRequest
	}

	return cookie.Value, nil
}
