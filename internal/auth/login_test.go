package auth_test

import (
	"cmp"
	"context"
	"errors"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/shigabutdinoff/gophermart/internal/auth"
	authmocks "github.com/shigabutdinoff/gophermart/internal/auth/mocks"
)

const testDummyHash = "dummy-hash"

var testClientIP = netip.MustParseAddr("192.0.2.1")

// loginCollaborators держит три узких зависимости входа. Незаявленный вызов
// мока валит тест сам, поэтому «не звали» отдельной проверки не требует.
type loginCollaborators struct {
	t         *testing.T
	users     *authmocks.MockUserFinder
	passwords *authmocks.MockPasswordVerifier
	issuer    *authmocks.MockTokenIssuer
}

func newLoginCollaborators(t *testing.T) loginCollaborators {
	t.Helper()

	return loginCollaborators{
		t:         t,
		users:     authmocks.NewMockUserFinder(t),
		passwords: authmocks.NewMockPasswordVerifier(t),
		issuer:    authmocks.NewMockTokenIssuer(t),
	}
}

func (c loginCollaborators) service() *auth.LoginService {
	return c.serviceWithLimiter(nil)
}

// serviceWithLimiter собирает вход поверх заданного ограничителя попыток.
func (c loginCollaborators) serviceWithLimiter(limiter *auth.LoginLimiter) *auth.LoginService {
	c.t.Helper()

	if limiter == nil {
		limiter = auth.NewLoginLimiter()
	}
	service, err := auth.NewLoginService(auth.LoginDeps{
		Logger:    zap.NewNop(),
		Users:     c.users,
		Verifier:  c.passwords,
		Tokens:    c.issuer,
		Limiter:   limiter,
		DummyHash: testDummyHash,
	})
	require.NoError(c.t, err)

	return service
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
		testClientIP,
	)

	require.NoError(t, err)
	assert.Equal(t, "exact-token", got.Token.Value)
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
				testClientIP,
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
				testClientIP,
			)

			assert.ErrorIs(t, err, cmp.Or(test.findErr, test.verifyErr, test.issueErr))
			assert.NotErrorIs(t, err, auth.ErrInvalidCredentials)
			assert.Zero(t, got)
		})
	}
}

// expectFindAndVerify заявляет ровно те вызовы, до которых вход доходит:
// неизвестный логин сверяет пароль с пустышкой, внутренний отказ поиска не сверяет.
func expectFindAndVerify(c loginCollaborators, findErr, verifyErr error) {
	c.users.EXPECT().FindByLogin(mock.Anything, "alice").
		Return(auth.User{ID: 7, PasswordHash: "stored-hash"}, findErr).Once()
	switch {
	case errors.Is(findErr, auth.ErrUserNotFound):
		c.passwords.EXPECT().Verify(testDummyHash, "password").
			Return(auth.ErrPasswordMismatch).Once()
	case findErr != nil:
	default:
		c.passwords.EXPECT().Verify("stored-hash", "password").Return(verifyErr).Once()
	}
}

func TestNewLoginServiceRejectsInvalidRuntimeDependencies(t *testing.T) {
	t.Parallel()

	collaborators := newLoginCollaborators(t)
	deps := auth.LoginDeps{
		Logger:    zap.NewNop(),
		Users:     collaborators.users,
		Verifier:  collaborators.passwords,
		Tokens:    collaborators.issuer,
		DummyHash: testDummyHash,
	}

	service, err := auth.NewLoginService(deps)
	require.Error(t, err)
	assert.Nil(t, service)

	deps.Limiter = auth.NewLoginLimiter()
	deps.DummyHash = ""
	service, err = auth.NewLoginService(deps)
	require.Error(t, err)
	assert.Nil(t, service)
}

func TestNewLoginDummyHashReportsBrokenHasher(t *testing.T) {
	t.Parallel()

	hasherErr := errors.New("hasher unavailable")
	hasher := authmocks.NewMockPasswordHasher(t)
	hasher.EXPECT().Hash(mock.Anything).Return("", hasherErr).Once()

	dummyHash, err := auth.NewLoginDummyHash(hasher)

	require.ErrorIs(t, err, hasherErr)
	assert.Empty(t, dummyHash)
}

// Ожидаемое несовпадение с пустышкой молчит, сломанный хеш уходит в лог.
func TestLoginServiceLogsBrokenDummyHashOnly(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name      string
		verifyErr error
		wantLogs  int
	}{
		{name: "ожидаемое несовпадение", verifyErr: auth.ErrPasswordMismatch},
		{name: "сломанный хеш", verifyErr: errors.New("invalid hash"), wantLogs: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			core, logs := observer.New(zap.ErrorLevel)
			collaborators := newLoginCollaborators(t)
			collaborators.users.EXPECT().FindByLogin(mock.Anything, "ghost").
				Return(auth.User{}, auth.ErrUserNotFound).Once()
			collaborators.passwords.EXPECT().Verify(testDummyHash, "password").
				Return(test.verifyErr).Once()
			service, err := auth.NewLoginService(auth.LoginDeps{
				Logger:    zap.New(core),
				Users:     collaborators.users,
				Verifier:  collaborators.passwords,
				Tokens:    collaborators.issuer,
				Limiter:   auth.NewLoginLimiter(),
				DummyHash: testDummyHash,
			})
			require.NoError(t, err)

			_, err = service.Login(
				context.Background(),
				auth.Credentials{Login: "ghost", Password: "password"},
				testClientIP,
			)

			require.ErrorIs(t, err, auth.ErrInvalidCredentials)
			assert.Equal(
				t,
				test.wantLogs,
				logs.FilterMessage("Не удалось проверить хеш-пустышку").Len(),
			)
		})
	}
}
