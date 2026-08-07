package auth

import (
	"context"
	"errors"
	"fmt"
)

// RegisterService заводит пользователя и сразу выпускает ему токен.
type RegisterService struct {
	users     UserRepository
	passwords Passwords
	tokens    TokenIssuer
}

// NewRegisterService builds a RegisterService with its collaborators
func NewRegisterService(users UserRepository, passwords Passwords, tokens TokenIssuer) *RegisterService {
	return &RegisterService{
		users:     users,
		passwords: passwords,
		tokens:    tokens,
	}
}

func (s *RegisterService) Register(ctx context.Context, credentials Credentials) (IssuedToken, error) {
	// Хеширование дороже поиска, поэтому занятый логин отсекается раньше.
	_, err := s.users.FindByLogin(ctx, credentials.Login)
	switch {
	case err == nil:
		return IssuedToken{}, ErrLoginTaken
	case !errors.Is(err, ErrUserNotFound):
		return IssuedToken{}, fmt.Errorf("find user: %w", err)
	}

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
