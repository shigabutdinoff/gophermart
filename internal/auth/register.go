package auth

import (
	"context"
	"fmt"
)

// RegisterService заводит пользователя и сразу выпускает ему токен.
type RegisterService struct {
	users     UserCreator
	passwords PasswordHasher
	tokens    TokenIssuer
}

func NewRegisterService(users UserCreator, passwords PasswordHasher, tokens TokenIssuer) *RegisterService {
	return &RegisterService{
		users:     users,
		passwords: passwords,
		tokens:    tokens,
	}
}

func (s *RegisterService) Register(ctx context.Context, credentials Credentials) (IssuedToken, error) {
	passwordHash, err := s.passwords.Hash(credentials.Password)
	if err != nil {
		return IssuedToken{}, fmt.Errorf("hash password: %w", err)
	}

	user, err := s.users.Create(ctx, credentials.Login, passwordHash)
	if err != nil {
		return IssuedToken{}, fmt.Errorf("create user: %w", err)
	}

	token, err := s.tokens.Issue(user.ID)
	if err != nil {
		return IssuedToken{}, fmt.Errorf("issue token: %w", err)
	}

	return token, nil
}
