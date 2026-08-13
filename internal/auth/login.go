package auth

import (
	"context"
	"errors"
	"fmt"

	"go.uber.org/zap"
)

// LoginService проверяет учётные данные и выпускает токен.
type LoginService struct {
	users     UserFinder
	verifier  PasswordVerifier
	tokens    TokenIssuer
}

// NewLoginService constructs a LoginService with its required collaborators.
func NewLoginService(
	_ *zap.Logger,
	users UserFinder,
	verifier PasswordVerifier,
	tokens TokenIssuer,
) (*LoginService, error) {
	return &LoginService{
		users:     users,
		verifier:  verifier,
		tokens:    tokens,
	}, nil
}

// Login authenticates credentials and returns a newly issued token.
func (s *LoginService) Login(ctx context.Context, credentials Credentials) (IssuedToken, error) {
	user, err := s.users.FindByLogin(ctx, credentials.Login)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return IssuedToken{}, ErrInvalidCredentials
		}
		return IssuedToken{}, fmt.Errorf("find user: %w", err)
	}

	if err := s.verifier.Verify(user.PasswordHash, credentials.Password); err != nil {
		if errors.Is(err, ErrPasswordMismatch) {
			return IssuedToken{}, ErrInvalidCredentials
		}
		return IssuedToken{}, fmt.Errorf("verify password: %w", err)
	}

	token, err := s.tokens.Issue(user.ID)
	if err != nil {
		return IssuedToken{}, fmt.Errorf("issue token: %w", err)
	}

	return token, nil
}
