package auth

import (
	"cmp"
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestLoginServiceLoginIssuesTokenForNormalizedLogin(t *testing.T) {
	t.Parallel()

	credentials := Credentials{Login: "alice", Password: "original-password"}
	var gotLogin, gotHash, gotPassword string
	var gotUserID int64
	users := &fakeUserRepository{findFunc: func(_ context.Context, login string) (User, error) {
		gotLogin = login
		return User{ID: 7, PasswordHash: "stored-hash"}, nil
	}}
	passwords := &fakePasswords{verifyFunc: func(hash, password string) error {
		gotHash, gotPassword = hash, password
		return nil
	}}
	issuer := &fakeTokenIssuer{issueFunc: func(userID int64) (IssuedToken, error) {
		gotUserID = userID
		return IssuedToken{Value: "exact-token"}, nil
	}}

	service, err := NewLoginService(zap.NewNop(), users, passwords, issuer)
	require.NoError(t, err)

	got, err := service.Login(context.Background(), credentials)
	require.NoError(t, err)
	assert.Equal(t, "exact-token", got.Value)
	assert.Equal(t, "alice", gotLogin)
	assert.Equal(t, "stored-hash", gotHash)
	assert.Equal(t, "original-password", gotPassword)
	assert.Equal(t, int64(7), gotUserID)
}

func TestLoginServiceLoginRejectsUnknownUserAndWrongPasswordIdentically(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name       string
		findErr    error
		verifyErr  error
		wantVerify int
	}{
		{name: "unknown user", findErr: ErrUserNotFound},
		{name: "wrong password", verifyErr: ErrPasswordMismatch, wantVerify: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			users := &fakeUserRepository{findFunc: func(context.Context, string) (User, error) {
				return User{ID: 7, PasswordHash: "stored-hash"}, test.findErr
			}}
			passwords := &fakePasswords{verifyFunc: func(string, string) error { return test.verifyErr }}
			issuer := &fakeTokenIssuer{}

			service, err := NewLoginService(zap.NewNop(), users, passwords, issuer)
			require.NoError(t, err)

			got, err := service.Login(context.Background(), Credentials{Login: "alice", Password: "password"})
			assert.ErrorIs(t, err, ErrInvalidCredentials)
			assert.Zero(t, got)
			assert.Equal(t, test.wantVerify, passwords.verifyCalls)
			assert.Zero(t, issuer.calls)
		})
	}
}

func TestLoginServiceLoginReturnsInternalFailures(t *testing.T) {
	t.Parallel()

	repositoryErr := errors.New("repository unavailable")
	damagedHashErr := errors.New("damaged password hash")
	issuerErr := errors.New("issuer unavailable")
	for _, test := range []struct {
		name       string
		findErr    error
		verifyErr  error
		issueErr   error
		wantVerify int
		wantIssue  int
	}{
		{name: "repository", findErr: repositoryErr},
		{name: "damaged hash", verifyErr: damagedHashErr, wantVerify: 1},
		{name: "issuer", issueErr: issuerErr, wantVerify: 1, wantIssue: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			users := &fakeUserRepository{findFunc: func(context.Context, string) (User, error) {
				return User{ID: 7, PasswordHash: "stored-hash"}, test.findErr
			}}
			passwords := &fakePasswords{verifyFunc: func(string, string) error { return test.verifyErr }}
			issuer := &fakeTokenIssuer{issueFunc: func(int64) (IssuedToken, error) { return IssuedToken{}, test.issueErr }}

			service, err := NewLoginService(zap.NewNop(), users, passwords, issuer)
			require.NoError(t, err)

			got, err := service.Login(context.Background(), Credentials{Login: "alice", Password: "password"})
			assert.ErrorIs(t, err, cmp.Or(test.findErr, test.verifyErr, test.issueErr))
			assert.NotErrorIs(t, err, ErrInvalidCredentials)
			assert.Zero(t, got)
			assert.Equal(t, test.wantVerify, passwords.verifyCalls)
			assert.Equal(t, test.wantIssue, issuer.calls)
		})
	}
}
