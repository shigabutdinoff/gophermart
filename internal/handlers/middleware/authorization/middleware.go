package authorization

import (
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/golang-jwt/jwt/v5/request"

	"github.com/shigabutdinoff/gophermart/internal/handlers/route/message"
)

// SessionCookieName называет cookie с тем же токеном, что и заголовок Bearer.
const SessionCookieName = "gophermart_session"

// TokenParser проверяет один токен из запроса.
type TokenParser func(*http.Request, request.Extractor) (int64, error)

// Middleware пропускает запрос с токеном в заголовке Bearer или в cookie.
func Middleware(api huma.API, parse TokenParser) func(huma.Context, func(huma.Context)) {
	extractor := request.MultiExtractor{
		nonemptyExtractor{Extractor: request.BearerExtractor{}},
		sessionCookieExtractor{},
	}

	return func(ctx huma.Context, next func(huma.Context)) {
		r, _ := humachi.Unwrap(ctx)
		userID, err := parse(r, extractor)
		if err != nil || userID <= 0 {
			_ = huma.WriteErr(api, ctx, http.StatusUnauthorized, message.Unauthorized)
			return
		}

		next(huma.WithContext(ctx, withUserID(ctx.Context(), userID)))
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
