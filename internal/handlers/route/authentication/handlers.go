package authentication

import (
	"context"
	"errors"
	"net/http"

	"go.uber.org/zap"

	"github.com/shigabutdinoff/gophermart/internal/auth"
)

// CredentialsFunc заводит учётную запись по нормализованным данным.
type CredentialsFunc func(context.Context, auth.Credentials) (auth.IssuedToken, error)

// Register returns the public user-registration HTTP handler.
func Register(logger *zap.Logger, register CredentialsFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		credentials, apiErr := decodeCredentials(r, auth.NormalizeRegisterCredentials)
		if apiErr != nil {
			writeError(w, r, apiErr)
			return
		}
		token, err := register(r.Context(), credentials)
		switch {
		case err == nil:
			writeAuthenticated(w, r, token, MessageRegistered)
		case errors.Is(err, auth.ErrLoginTaken):
			writeMessage(w, r, http.StatusConflict, MessageLoginTaken)
		default:
			logger.Error("Не удалось зарегистрировать пользователя", zap.Error(err))
			writeMessage(w, r, http.StatusInternalServerError, MessageInternalError)
		}
	}
}

// Login returns the public user-login HTTP handler.
func Login(logger *zap.Logger, login CredentialsFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		credentials, apiErr := decodeCredentials(r, auth.NormalizeLoginCredentials)
		if apiErr != nil {
			writeError(w, r, apiErr)
			return
		}
		token, err := login(r.Context(), credentials)
		switch {
		case err == nil:
			writeAuthenticated(w, r, token, MessageLoggedIn)
		case errors.Is(err, auth.ErrInvalidCredentials):
			writeMessage(w, r, http.StatusUnauthorized, MessageInvalidCredentials)
		default:
			logger.Error("Не удалось выполнить вход пользователя", zap.Error(err))
			writeMessage(w, r, http.StatusInternalServerError, MessageInternalError)
		}
	}
}
