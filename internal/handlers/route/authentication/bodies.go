package authentication

import (
	"github.com/danielgtaylor/huma/v2"

	"github.com/shigabutdinoff/gophermart/internal/auth"
)

// loginField несёт логин обоих тел вместе с его разбором.
// границы логина меряет Resolve, схеме видна строка до нормализации
type loginField struct {
	Login string `json:"login" doc:"Логин пользователя"`
}

// Resolve нормализует логин и меряет границы уже приведённого значения.
// Схема видит сырую строку, а хранилищу и лимитеру нужна приведённая.
func (f *loginField) Resolve(huma.Context) []error {
	f.Login = auth.NormalizeLogin(f.Login)

	return validateLogin(f.Login)
}

// registerBody описывает вход регистрации, границы проверяет схема операции.
type registerBody struct {
	// лишние поля тела игнорируются, как и до перехода на схему
	_ struct{} `additionalProperties:"true"`
	loginField
	Password string `json:"password" minLength:"1" maxLength:"128" doc:"Пароль пользователя"`
}

// loginBody не применяет парольную политику, вход требует лишь непустых полей.
type loginBody struct {
	_ struct{} `additionalProperties:"true"`
	loginField
	Password string `json:"password" minLength:"1" doc:"Пароль пользователя"`
}
