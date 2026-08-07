package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegisterServiceHashesEachPasswordBeforeCreatingUser(t *testing.T) {
	ctx := context.Background()
	var hashes []string
	users := &fakeUserRepository{
		createFunc: func(_ context.Context, login, passwordHash string) (User, error) {
			assert.Equal(t, "normalized-user", login)
			hashes = append(hashes, passwordHash)
			return User{ID: int64(len(hashes))}, nil
		},
	}
	issuedTokens := []IssuedToken{{Value: "token-1"}, {Value: "token-2"}}
	var issuedUserIDs []int64
	issuer := &fakeTokenIssuer{
		issueFunc: func(userID int64) (IssuedToken, error) {
			issuedUserIDs = append(issuedUserIDs, userID)
			return issuedTokens[len(issuedUserIDs)-1], nil
		},
	}
	service := NewRegisterService(users, Argon2Passwords{}, issuer)
	credentials := Credentials{Login: "normalized-user", Password: "same-password"}
	var returnedTokens []IssuedToken

	for i := 0; i < 2; i++ {
		token, err := service.Register(ctx, credentials)
		require.NoError(t, err)
		returnedTokens = append(returnedTokens, token)
	}

	assert.Equal(t, 2, users.createCalls)
	assert.Equal(t, 2, issuer.calls)
	assert.Equal(t, 2, users.findCalls)
	for i, userID := range issuedUserIDs {
		assert.Equal(t, int64(i+1), userID)
	}
	for i, token := range returnedTokens {
		assert.Equal(t, issuedTokens[i], token)
	}
	require.Len(t, hashes, 2)
	assert.NotEqual(t, credentials.Password, hashes[0])
	assert.NotEqual(t, credentials.Password, hashes[1])
	assert.NotEqual(t, hashes[0], hashes[1])
	passwords := Argon2Passwords{}
	for _, hash := range hashes {
		assert.NoError(t, passwords.Verify(hash, credentials.Password))
		assert.ErrorIs(t, passwords.Verify(hash, "wrong-password"), ErrPasswordMismatch)
	}
}

func TestRegisterServiceStopsBeforeIssueWhenLoginTaken(t *testing.T) {
	users := &fakeUserRepository{
		createFunc: func(context.Context, string, string) (User, error) {
			return User{}, ErrLoginTaken
		},
	}
	passwords := &fakePasswords{}
	issuer := &fakeTokenIssuer{}
	service := NewRegisterService(users, passwords, issuer)

	token, err := service.Register(context.Background(), Credentials{Login: "user", Password: "password"})
	require.ErrorIs(t, err, ErrLoginTaken)
	assert.Zero(t, token)
	assert.Equal(t, 1, passwords.hashCalls)
	assert.Equal(t, 1, users.createCalls)
	assert.Zero(t, issuer.calls)
	assert.Equal(t, 1, users.findCalls)
}

func TestRegisterServiceSkipsHashingWhenLoginAlreadyExists(t *testing.T) {
	users := &fakeUserRepository{
		findFunc: func(context.Context, string) (User, error) { return User{ID: 7}, nil },
	}
	passwords := &fakePasswords{}
	issuer := &fakeTokenIssuer{}
	service := NewRegisterService(users, passwords, issuer)

	token, err := service.Register(context.Background(), Credentials{Login: "user", Password: "password"})
	require.ErrorIs(t, err, ErrLoginTaken)
	assert.Zero(t, token)
	assert.Equal(t, 1, users.findCalls)
	assert.Zero(t, passwords.hashCalls)
	assert.Zero(t, users.createCalls)
	assert.Zero(t, issuer.calls)
}

func TestRegisterServiceDoesNotHashWhenLookupFails(t *testing.T) {
	lookupErr := errors.New("repository unavailable")
	users := &fakeUserRepository{
		findFunc: func(context.Context, string) (User, error) { return User{}, lookupErr },
	}
	passwords := &fakePasswords{}
	issuer := &fakeTokenIssuer{}
	service := NewRegisterService(users, passwords, issuer)

	token, err := service.Register(context.Background(), Credentials{Login: "user", Password: "password"})
	require.ErrorIs(t, err, lookupErr)
	assert.NotErrorIs(t, err, ErrLoginTaken)
	assert.Zero(t, token)
	assert.Zero(t, passwords.hashCalls)
	assert.Zero(t, users.createCalls)
	assert.Zero(t, issuer.calls)
}

func TestRegisterServiceDoesNotCreateOrIssueWhenHashFails(t *testing.T) {
	hashErr := errors.New("hash failed")
	users := &fakeUserRepository{}
	passwords := &fakePasswords{hashFunc: func(string) (string, error) { return "", hashErr }}
	issuer := &fakeTokenIssuer{}
	service := NewRegisterService(users, passwords, issuer)

	token, err := service.Register(context.Background(), Credentials{Login: "user", Password: "password"})
	require.ErrorIs(t, err, hashErr)
	assert.Zero(t, token)
	assert.Zero(t, users.createCalls)
	assert.Zero(t, issuer.calls)
	assert.Equal(t, 1, users.findCalls)
}

func TestRegisterServiceDoesNotIssueWhenCreateFails(t *testing.T) {
	createErr := errors.New("storage failed")
	users := &fakeUserRepository{
		createFunc: func(context.Context, string, string) (User, error) { return User{}, createErr },
	}
	passwords := &fakePasswords{}
	issuer := &fakeTokenIssuer{}
	service := NewRegisterService(users, passwords, issuer)

	token, err := service.Register(context.Background(), Credentials{Login: "user", Password: "password"})
	require.ErrorIs(t, err, createErr)
	assert.Zero(t, token)
	assert.Equal(t, 1, users.createCalls)
	assert.Zero(t, issuer.calls)
	assert.Equal(t, 1, users.findCalls)
}

func TestRegisterServiceKeepsCreatedUserWhenIssueFails(t *testing.T) {
	issueErr := errors.New("issue failed")
	created := false
	users := &fakeUserRepository{
		createFunc: func(_ context.Context, _ string, _ string) (User, error) {
			if created {
				return User{}, ErrLoginTaken
			}
			created = true
			return User{ID: 42}, nil
		},
	}
	issuer := &fakeTokenIssuer{issueFunc: func(int64) (IssuedToken, error) { return IssuedToken{}, issueErr }}
	service := NewRegisterService(users, &fakePasswords{}, issuer)
	credentials := Credentials{Login: "user", Password: "password"}

	firstToken, err := service.Register(context.Background(), credentials)
	require.ErrorIs(t, err, issueErr)
	assert.Zero(t, firstToken)
	secondToken, err := service.Register(context.Background(), credentials)
	require.ErrorIs(t, err, ErrLoginTaken)
	assert.Zero(t, secondToken)
	require.True(t, created)
	assert.Equal(t, 2, users.createCalls)
	assert.Equal(t, 1, issuer.calls)
	assert.Equal(t, 2, users.findCalls)
}
