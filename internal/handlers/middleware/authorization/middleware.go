package authorization

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/golang-jwt/jwt/v5/request"
	"go.uber.org/zap"
)

// SessionCookieName называет cookie с тем же токеном, что и заголовок Bearer.
const SessionCookieName = "gophermart_session"

// Тексты отказов посредника.
const (
	MessageUnauthorized  = "Пользователь не аутентифицирован"
	MessageInternalError = "Внутренняя ошибка сервиса"
)

// Тела отказов в формате ошибок huma готовятся на старте
var (
	unauthorizedBody = mustMarshal(
		huma.NewError(http.StatusUnauthorized, MessageUnauthorized),
	)
	internalBody = mustMarshal(
		huma.NewError(http.StatusInternalServerError, MessageInternalError),
	)
)

// mustMarshal готовит тело на старте, пустой ответ отказа недопустим
func mustMarshal(value any) []byte {
	body, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}

	return body
}

// TokenParser проверяет один токен из запроса.
type TokenParser func(*http.Request, request.Extractor) (int64, error)

// UserChecker подтверждает, что учётная запись из токена ещё существует.
type UserChecker interface {
	Exists(ctx context.Context, userID int64) (bool, error)
}

// Middleware пропускает запрос с токеном в заголовке Bearer или в cookie.
// Токен переживает пересоздание базы, поэтому владелец ещё и проверяется.
func Middleware(
	logger *zap.Logger,
	parse TokenParser,
	users UserChecker,
) func(http.Handler) http.Handler {
	extractor := request.MultiExtractor{
		nonemptyExtractor{Extractor: request.BearerExtractor{}},
		sessionCookieExtractor{},
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userID, err := parse(r, extractor)
			if err != nil || userID <= 0 {
				respond(w, http.StatusUnauthorized, unauthorizedBody)
				return
			}

			exists, err := users.Exists(r.Context(), userID)
			if err != nil {
				logger.Error("Не удалось проверить пользователя", zap.Error(err))
				respond(w, http.StatusInternalServerError, internalBody)
				return
			}
			if !exists {
				respond(w, http.StatusUnauthorized, unauthorizedBody)
				return
			}

			next.ServeHTTP(w, r.WithContext(withUserID(r.Context(), userID)))
		})
	}
}

// respond отвечает так же, как отказали бы хендлеры за посредником.
func respond(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
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
