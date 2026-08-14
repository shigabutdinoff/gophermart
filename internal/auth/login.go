package auth

import (
	"context"
	"errors"
	"fmt"
	"net/netip"

	"go.uber.org/zap"
)

// dummyPassword хешируется один раз за процесс и никому не принадлежит
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
		logger:    deps.Logger,
		users:     deps.Users,
		verifier:  deps.Verifier,
		tokens:    deps.Tokens,
		limiter:   deps.Limiter,
		dummyHash: deps.DummyHash,
	}, nil
}

// NewLoginDummyHash считает хеш-пустышку один раз на процесс.
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

func (s *LoginService) Login(
	ctx context.Context,
	credentials Credentials,
	client netip.Addr,
) (LoginResult, error) {
	key := credentials.Login + "\x00" + client.String()
	release := s.limiter.Acquire(key)
	defer release()

	if retryAfter, limited := s.limiter.Check(key); limited {
		return LoginResult{RetryAfter: retryAfter}, ErrRateLimited
	}

	user, err := s.users.FindByLogin(ctx, credentials.Login)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			// Проверка пустышки уравнивает время ответа неизвестному логину.
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
