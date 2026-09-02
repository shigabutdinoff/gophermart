package authentication

import (
	"github.com/danielgtaylor/huma/v2"

	"github.com/shigabutdinoff/gophermart/internal/auth"
)

// loginField несёт логин обоих тел вместе с его разбором.
type loginField struct {
	Login string `json:"login" doc:"Логин пользователя"`
}

// normalize приводит логин и меряет границы уже приведённого значения.
// Схема видит сырую строку, а хранилищу и лимитеру нужна приведённая.
func (f *loginField) normalize() []error {
	f.Login = auth.NormalizeLogin(f.Login)

	return validateLogin(f.Login)
}

// Границы пароля меряет резолвер, а не схема: huma кладёт в ErrorDetail.Value
// само значение, и отказ по схеме вернул бы пароль клиенту в теле 400.
type registerBody struct {
	// лишние поля тела игнорируются, как и до перехода на схему
	_ struct{} `additionalProperties:"true"`
	loginField
	Password string `json:"password" doc:"Пароль пользователя"`
}

func (b *registerBody) Resolve(huma.Context) []error {
	return append(b.normalize(), validateNewPassword(b.Password)...)
}

// loginBody не применяет парольную политику, вход требует лишь непустых полей.
type loginBody struct {
	_ struct{} `additionalProperties:"true"`
	loginField
	Password string `json:"password" doc:"Пароль пользователя"`
}

func (b *loginBody) Resolve(huma.Context) []error {
	return append(b.normalize(), validatePassword(b.Password)...)
}
