package authentication

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/shigabutdinoff/gophermart/internal/auth"
)

type credentialsRequest struct {
	Login    string `json:"login"`
	Password string `json:"password"`
}

// NormalizeFunc validates raw credentials by the rules of its route.
type NormalizeFunc func(login, password string) (auth.Credentials, error)

func decodeCredentials(r *http.Request, normalize NormalizeFunc) (auth.Credentials, *apiError) {
	// Тело читается целиком, иначе превышение лимита за концом JSON теряется.
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return auth.Credentials{}, bodyError(err)
	}

	var input credentialsRequest
	if err := json.Unmarshal(body, &input); err != nil {
		return auth.Credentials{}, badRequest(MessageBadRequest)
	}

	credentials, err := normalize(input.Login, input.Password)
	if err != nil {
		return auth.Credentials{}, badRequest(messageForCredentials(err))
	}
	return credentials, nil
}

// messageForCredentials переводит ошибку формата credentials в текст ответа.
func messageForCredentials(err error) string {
	var format *auth.CredentialsFormatError
	if !errors.As(err, &format) {
		return MessageBadRequest
	}
	if message, ok := credentialsMessages[*format]; ok {
		return message
	}
	return MessageBadRequest
}

// bodyError переводит ошибку чтения тела запроса в несостоявшийся ответ.
func bodyError(err error) *apiError {
	if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
		return &apiError{status: http.StatusRequestEntityTooLarge, message: MessageBodyTooLarge}
	}
	return badRequest(MessageBadRequest)
}
