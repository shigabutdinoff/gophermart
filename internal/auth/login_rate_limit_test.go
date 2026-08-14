package auth_test

import (
	"context"
	"errors"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/shigabutdinoff/gophermart/internal/auth"
)

const (
	loginLimitKey  = "user\x00192.0.2.1"
	otherLimitKey  = "user\x00192.0.2.2"
	loginRetryWait = 12
)

var loginClient = netip.MustParseAddr("192.0.2.1")

func loginAttempt(service *auth.LoginService) (auth.LoginResult, error) {
	return service.Login(
		context.Background(),
		auth.Credentials{Login: "user", Password: "password"},
		loginClient,
	)
}

func TestLoginServiceSixthRequestStopsBeforeRepository(t *testing.T) {
	t.Parallel()

	collaborators := newLoginCollaborators(t)
	limiter := auth.NewLoginLimiter()
	for range auth.MaxLoginAttempts {
		limiter.Hit(loginLimitKey)
	}

	result, err := loginAttempt(collaborators.serviceWithLimiter(limiter))

	require.ErrorIs(t, err, auth.ErrRateLimited)
	assert.Equal(t, loginRetryWait, result.RetryAfter)
}

func TestLoginServiceInvalidCredentialsHitOnlyTheirExactKey(t *testing.T) {
	t.Parallel()

	collaborators := newLoginCollaborators(t)
	collaborators.users.EXPECT().FindByLogin(mock.Anything, "user").
		Return(auth.User{}, auth.ErrUserNotFound).Once()
	collaborators.passwords.EXPECT().Verify(testDummyHash, "password").
		Return(auth.ErrPasswordMismatch).Once()
	limiter := auth.NewLoginLimiter()

	_, err := loginAttempt(collaborators.serviceWithLimiter(limiter))

	require.ErrorIs(t, err, auth.ErrInvalidCredentials)
	// неудача занимает попытку своего ключа, соседним она ничего не стоит
	for range auth.MaxLoginAttempts - 1 {
		limiter.Hit(loginLimitKey)
	}
	_, limited := limiter.Check(loginLimitKey)
	assert.True(t, limited)
	_, limitedOther := limiter.Check(otherLimitKey)
	assert.False(t, limitedOther)
}

func TestLoginServiceSuccessClearsOnlyAfterTokenIssue(t *testing.T) {
	t.Parallel()

	collaborators := newLoginCollaborators(t)
	collaborators.users.EXPECT().FindByLogin(mock.Anything, "user").
		Return(auth.User{ID: 7, PasswordHash: "stored-hash"}, nil).Once()
	collaborators.passwords.EXPECT().Verify("stored-hash", "password").Return(nil).Once()
	collaborators.issuer.EXPECT().Issue(int64(7)).
		Return(auth.IssuedToken{Value: "token"}, nil).Once()
	limiter := auth.NewLoginLimiter()
	for range auth.MaxLoginAttempts - 1 {
		limiter.Hit(loginLimitKey)
	}

	result, err := loginAttempt(collaborators.serviceWithLimiter(limiter))

	require.NoError(t, err)
	assert.Equal(t, "token", result.Token.Value)
	// после очистки ключу снова доступны все попытки
	for range auth.MaxLoginAttempts - 1 {
		limiter.Hit(loginLimitKey)
	}
	_, limited := limiter.Check(loginLimitKey)
	assert.False(t, limited)
}

func TestLoginServiceInternalErrorsDoNotConsumeAttempts(t *testing.T) {
	t.Parallel()

	internalErr := errors.New("internal")
	tests := []struct {
		name  string
		setup func(loginCollaborators)
	}{
		{
			name: "repository",
			setup: func(c loginCollaborators) {
				c.users.EXPECT().FindByLogin(mock.Anything, "user").
					Return(auth.User{}, internalErr).Once()
			},
		},
		{
			name: "damaged password hash",
			setup: func(c loginCollaborators) {
				c.users.EXPECT().FindByLogin(mock.Anything, "user").
					Return(auth.User{ID: 7, PasswordHash: "damaged"}, nil).Once()
				c.passwords.EXPECT().Verify("damaged", "password").Return(internalErr).Once()
			},
		},
		{
			name: "token issuer",
			setup: func(c loginCollaborators) {
				c.users.EXPECT().FindByLogin(mock.Anything, "user").
					Return(auth.User{ID: 7, PasswordHash: "stored-hash"}, nil).Once()
				c.passwords.EXPECT().Verify("stored-hash", "password").Return(nil).Once()
				c.issuer.EXPECT().Issue(int64(7)).Return(auth.IssuedToken{}, internalErr).Once()
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			collaborators := newLoginCollaborators(t)
			test.setup(collaborators)
			limiter := auth.NewLoginLimiter()

			_, err := loginAttempt(collaborators.serviceWithLimiter(limiter))

			require.ErrorIs(t, err, internalErr)
			for range auth.MaxLoginAttempts - 1 {
				limiter.Hit(loginLimitKey)
			}
			_, limited := limiter.Check(loginLimitKey)
			assert.False(t, limited)
		})
	}
}
