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

type LoginDeps struct {
	Logger    *zap.Logger
	Users     UserFinder
	Verifier  PasswordVerifier
	Tokens    TokenIssuer
	Limiter   *LoginLimiter
	DummyHash string
}

// NewLoginService собирает службу входа из готовых зависимостей.
func NewLoginService(deps LoginDeps) (*LoginService, error) {
	if deps.Limiter == nil {
		return nil, errors.New("login limiter must not be nil")
	}
	if deps.DummyHash == "" {
		return nil, errors.New("dummy password hash must not be empty")
	}
	if deps.Logger == nil {
		deps.Logger = zap.NewNop()
	}

	return &LoginService{
		logger: deps.Logger, users: deps.Users, verifier: deps.Verifier, tokens: deps.Tokens,
		limiter: deps.Limiter, dummyHash: deps.DummyHash,
	}, nil
}

// NewLoginDummyHash считает хеш пароля несуществующего пользователя.
func NewLoginDummyHash(hasher PasswordHasher) (string, error) {
	dummyHash, err := hasher.Hash(dummyPassword)
	if err != nil {
		return "", fmt.Errorf("hash dummy password: %w", err)
	}
	if dummyHash == "" {
		return "", errors.New("dummy password hash must not be empty")
	}

	return dummyHash, nil
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
