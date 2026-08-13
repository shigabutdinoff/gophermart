package auth

import (
	"context"
	"errors"
	"fmt"
)

// LoginService проверяет учётные данные и выпускает токен.
type LoginService struct {
	users    UserFinder
	verifier PasswordVerifier
	tokens   TokenIssuer
	limiter  *LoginLimiter
}

// NewLoginService собирает службу входа из её обязательных участников.
// Без своего ограничителя служба берёт умолчания NewLoginLimiter.
func NewLoginService(
	users UserFinder,
	verifier PasswordVerifier,
	tokens TokenIssuer,
	limiters ...*LoginLimiter,
) *LoginService {
	limiter := NewLoginLimiter()
	if len(limiters) != 0 && limiters[0] != nil {
		limiter = limiters[0]
	}

	return &LoginService{
		users:    users,
		verifier: verifier,
		tokens:   tokens,
		limiter:  limiter,
	}
}

// Login проверяет учётные данные и выпускает новый токен.
func (s *LoginService) Login(
	ctx context.Context,
	credentials Credentials,
	remoteAddr ...string,
) (LoginResult, error) {
	client := ""
	if len(remoteAddr) != 0 {
		client = DirectIP(remoteAddr[0])
	}
	key := credentials.Login + "\x00" + client
	if retryAfter, limited := s.limiter.Check(key); limited {
		return LoginResult{RetryAfter: retryAfter}, ErrRateLimited
	}

	user, err := s.users.FindByLogin(ctx, credentials.Login)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			s.limiter.Hit(key)
			return LoginResult{}, ErrInvalidCredentials
		}
		return LoginResult{}, fmt.Errorf("find user: %w", err)
	}

	if err := s.verifier.Verify(user.PasswordHash, credentials.Password); err != nil {
		if errors.Is(err, ErrPasswordMismatch) {
			s.limiter.Hit(key)
			return LoginResult{}, ErrInvalidCredentials
		}
		return LoginResult{}, fmt.Errorf("verify password: %w", err)
	}

	token, err := s.tokens.Issue(user.ID)
	if err != nil {
		return LoginResult{}, fmt.Errorf("issue token: %w", err)
	}
	s.limiter.Clear(key)

	return LoginResult{Token: token}, nil
}
