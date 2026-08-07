package authorization

import (
	"net/http"

	"github.com/go-chi/jwtauth/v5"

	"github.com/shigabutdinoff/gophermart/internal/auth"
)

// SessionCookieName называет cookie с тем же токеном, что и заголовок Bearer.
const SessionCookieName = "gophermart_session"

// Middleware пропускает запрос с токеном в заголовке Bearer или в cookie.
func Middleware(verifier *jwtauth.JWTAuth) func(http.Handler) http.Handler {
	verify := jwtauth.Verify(verifier, jwtauth.TokenFromHeader, tokenFromSessionCookie)

	return func(next http.Handler) http.Handler {
		return verify(authenticate(next))
	}
}

// authenticate пропускает запрос с проверенным токеном и положительным user_id.
func authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, _, err := jwtauth.FromContext(r.Context())
		if err == nil {
			if userID, ok := auth.UserIDFromToken(token); ok {
				next.ServeHTTP(w, r.WithContext(withUserID(r.Context(), userID)))
				return
			}
		}

		w.WriteHeader(http.StatusUnauthorized)
	})
}

func tokenFromSessionCookie(r *http.Request) string {
	cookie, err := r.Cookie(SessionCookieName)
	if err != nil {
		return ""
	}

	return cookie.Value
}
