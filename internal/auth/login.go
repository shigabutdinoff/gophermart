package auth

import (
	"context"
	"errors"
	"fmt"

	"go.uber.org/zap"
)

const dummyPassword = "password of a user that does not exist"

// LoginService проверяет учётные данные и выпускает токен.
type LoginService struct {
	logger    *zap.Logger
	users     UserFinder
	verifier  PasswordVerifier
	tokens    TokenIssuer
	limiter   *LoginLimiter
	dummyHash string
}

type loginPasswords interface {
	PasswordHasher
	PasswordVerifier
}

// NewLoginService собирает службу входа из её обязательных участников.
// Без своего ограничителя служба берёт умолчания NewLoginLimiter.
func NewLoginService(
	logger *zap.Logger,
	users UserFinder,
	passwords loginPasswords,
	tokens TokenIssuer,
	limiters ...*LoginLimiter,
) (*LoginService, error) {
	dummyHash, err := passwords.Hash(dummyPassword)
	if err != nil {
		return nil, fmt.Errorf("hash dummy password: %w", err)
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	limiter := NewLoginLimiter()
	if len(limiters) != 0 && limiters[0] != nil {
		limiter = limiters[0]
	}

	return &LoginService{
		logger: logger, users: users, verifier: passwords, tokens: tokens,
		limiter: limiter, dummyHash: dummyHash,
	}, nil
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
	release := s.limiter.Acquire(key)
	defer release()
	if retryAfter, limited := s.limiter.Check(key); limited {
		return LoginResult{RetryAfter: retryAfter}, ErrRateLimited
	}

	user, err := s.users.FindByLogin(ctx, credentials.Login)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			dummyErr := s.verifier.Verify(s.dummyHash, credentials.Password)
			if dummyErr != nil && !errors.Is(dummyErr, ErrPasswordMismatch) {
				s.logger.Error("Не удалось проверить хеш-пустышку", zap.Error(dummyErr))
			}
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
