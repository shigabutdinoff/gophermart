package auth_test

import (
	"cmp"
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/shigabutdinoff/gophermart/internal/auth"
	authmocks "github.com/shigabutdinoff/gophermart/internal/auth/mocks"
)

// loginCollaborators держит три узких зависимости входа. Незаявленный вызов
// мока валит тест сам, поэтому «не звали» отдельной проверки не требует.
type loginCollaborators struct {
	users     *authmocks.MockUserFinder
	passwords *authmocks.MockPasswordVerifier
	issuer    *authmocks.MockTokenIssuer
}

func newLoginCollaborators(t *testing.T) loginCollaborators {
	t.Helper()

	return loginCollaborators{
		users:     authmocks.NewMockUserFinder(t),
		passwords: authmocks.NewMockPasswordVerifier(t),
		issuer:    authmocks.NewMockTokenIssuer(t),
	}
}

func (c loginCollaborators) service() *auth.LoginService {
	return auth.NewLoginService(c.users, c.passwords, c.issuer)
}

func TestLoginServiceLoginIssuesTokenForNormalizedLogin(t *testing.T) {
	t.Parallel()

	collaborators := newLoginCollaborators(t)
	collaborators.users.EXPECT().FindByLogin(mock.Anything, "alice").
		Return(auth.User{ID: 7, PasswordHash: "stored-hash"}, nil).Once()
	collaborators.passwords.EXPECT().Verify("stored-hash", "original-password").
		Return(nil).Once()
	collaborators.issuer.EXPECT().Issue(int64(7)).
		Return(auth.IssuedToken{Value: "exact-token"}, nil).Once()

	got, err := collaborators.service().Login(
		context.Background(),
		auth.Credentials{Login: "alice", Password: "original-password"},
	)

	require.NoError(t, err)
	assert.Equal(t, "exact-token", got.Value)
}

func TestLoginServiceLoginRejectsUnknownUserAndWrongPasswordIdentically(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name      string
		findErr   error
		verifyErr error
	}{
		{name: "unknown user", findErr: auth.ErrUserNotFound},
		{name: "wrong password", verifyErr: auth.ErrPasswordMismatch},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			collaborators := newLoginCollaborators(t)
			expectFindAndVerify(collaborators, test.findErr, test.verifyErr)

			got, err := collaborators.service().Login(
				context.Background(),
				auth.Credentials{Login: "alice", Password: "password"},
			)

			assert.ErrorIs(t, err, auth.ErrInvalidCredentials)
			assert.Zero(t, got)
		})
	}
}

func TestLoginServiceLoginReturnsInternalFailures(t *testing.T) {
	t.Parallel()

	repositoryErr := errors.New("repository unavailable")
	damagedHashErr := errors.New("damaged password hash")
	issuerErr := errors.New("issuer unavailable")
	for _, test := range []struct {
		name      string
		findErr   error
		verifyErr error
		issueErr  error
	}{
		{name: "repository", findErr: repositoryErr},
		{name: "damaged hash", verifyErr: damagedHashErr},
		{name: "issuer", issueErr: issuerErr},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			collaborators := newLoginCollaborators(t)
			expectFindAndVerify(collaborators, test.findErr, test.verifyErr)
			if test.findErr == nil && test.verifyErr == nil {
				collaborators.issuer.EXPECT().Issue(int64(7)).
					Return(auth.IssuedToken{}, test.issueErr).Once()
			}

			got, err := collaborators.service().Login(
				context.Background(),
				auth.Credentials{Login: "alice", Password: "password"},
			)

			assert.ErrorIs(t, err, cmp.Or(test.findErr, test.verifyErr, test.issueErr))
			assert.NotErrorIs(t, err, auth.ErrInvalidCredentials)
			assert.Zero(t, got)
		})
	}
}

// expectFindAndVerify заявляет ровно те вызовы, до которых вход доходит:
// после отказа поиска пароль не сверяется.
func expectFindAndVerify(c loginCollaborators, findErr, verifyErr error) {
	c.users.EXPECT().FindByLogin(mock.Anything, "alice").
		Return(auth.User{ID: 7, PasswordHash: "stored-hash"}, findErr).Once()
	if findErr != nil {
		return
	}
	c.passwords.EXPECT().Verify("stored-hash", "password").Return(verifyErr).Once()
}
