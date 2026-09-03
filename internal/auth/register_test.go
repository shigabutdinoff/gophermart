package auth_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/shigabutdinoff/gophermart/internal/auth"
	authmocks "github.com/shigabutdinoff/gophermart/internal/auth/mocks"
)

func TestRegisterServiceUsesNarrowCollaboratorsInHashCreateIssueOrder(t *testing.T) {
	var calls []string
	ctx := context.Background()
	users := authmocks.NewMockUserCreator(t)
	passwords := authmocks.NewMockPasswordHasher(t)
	tokens := authmocks.NewMockTokenIssuer(t)
	passwords.EXPECT().Hash("password").Run(func(string) {
		calls = append(calls, "hash")
	}).Return("hash", nil).Once()
	users.EXPECT().Create(ctx, "user", "hash").Run(func(context.Context, string, string) {
		calls = append(calls, "create")
	}).Return(auth.User{ID: 7}, nil).Once()
	tokens.EXPECT().Issue(int64(7)).Run(func(int64) {
		calls = append(calls, "issue")
	}).Return(auth.IssuedToken{Value: "token"}, nil).Once()
	service := auth.NewRegisterService(users, passwords, tokens)

	token, err := service.Register(
		ctx,
		auth.Credentials{Login: "user", Password: "password"},
	)

	require.NoError(t, err)
	assert.Equal(t, "token", token.Value)
	assert.Equal(t, []string{"hash", "create", "issue"}, calls)
}

func TestRegisterServiceHashesEachPasswordBeforeCreatingUser(t *testing.T) {
	ctx := context.Background()
	var hashes []string
	users := authmocks.NewMockUserCreator(t)
	users.EXPECT().Create(ctx, "normalized-user", mock.AnythingOfType("string")).
		RunAndReturn(func(_ context.Context, _ string, passwordHash string) (auth.User, error) {
			hashes = append(hashes, passwordHash)
			return auth.User{ID: int64(len(hashes))}, nil
		}).
		Twice()
	issuedTokens := []auth.IssuedToken{{Value: "token-1"}, {Value: "token-2"}}
	var issuedUserIDs []int64
	issuer := authmocks.NewMockTokenIssuer(t)
	issuer.EXPECT().Issue(mock.AnythingOfType("int64")).
		RunAndReturn(func(userID int64) (auth.IssuedToken, error) {
			issuedUserIDs = append(issuedUserIDs, userID)
			return issuedTokens[len(issuedUserIDs)-1], nil
		}).
		Twice()
	service := auth.NewRegisterService(users, auth.Argon2Passwords{}, issuer)
	credentials := auth.Credentials{Login: "normalized-user", Password: "same-password"}
	var returnedTokens []auth.IssuedToken

	for range 2 {
		token, err := service.Register(ctx, credentials)
		require.NoError(t, err)
		returnedTokens = append(returnedTokens, token)
	}

	for index, userID := range issuedUserIDs {
		assert.Equal(t, int64(index+1), userID)
	}
	for index, token := range returnedTokens {
		assert.Equal(t, issuedTokens[index], token)
	}
	require.Len(t, hashes, 2)
	assert.NotEqual(t, credentials.Password, hashes[0])
	assert.NotEqual(t, credentials.Password, hashes[1])
	assert.NotEqual(t, hashes[0], hashes[1])
	passwords := auth.Argon2Passwords{}
	for _, hash := range hashes {
		assert.NoError(t, passwords.Verify(hash, credentials.Password))
		assert.ErrorIs(t, passwords.Verify(hash, "wrong-password"), auth.ErrPasswordMismatch)
	}
}

func TestRegisterServiceStopsBeforeIssueWhenLoginTaken(t *testing.T) {
	ctx := context.Background()
	users := authmocks.NewMockUserCreator(t)
	users.EXPECT().Create(ctx, "user", "hash").Return(auth.User{}, auth.ErrLoginTaken).Once()
	passwords := authmocks.NewMockPasswordHasher(t)
	passwords.EXPECT().Hash("password").Return("hash", nil).Once()
	issuer := authmocks.NewMockTokenIssuer(t)
	service := auth.NewRegisterService(users, passwords, issuer)

	token, err := service.Register(ctx, auth.Credentials{Login: "user", Password: "password"})

	require.ErrorIs(t, err, auth.ErrLoginTaken)
	assert.Zero(t, token)
}

func TestRegisterServiceDoesNotCreateOrIssueWhenHashFails(t *testing.T) {
	hashErr := errors.New("hash failed")
	users := authmocks.NewMockUserCreator(t)
	passwords := authmocks.NewMockPasswordHasher(t)
	passwords.EXPECT().Hash("password").Return("", hashErr).Once()
	issuer := authmocks.NewMockTokenIssuer(t)
	service := auth.NewRegisterService(users, passwords, issuer)

	token, err := service.Register(
		context.Background(),
		auth.Credentials{Login: "user", Password: "password"},
	)

	require.ErrorIs(t, err, hashErr)
	assert.Zero(t, token)
}

func TestRegisterServiceDoesNotIssueWhenCreateFails(t *testing.T) {
	createErr := errors.New("storage failed")
	ctx := context.Background()
	users := authmocks.NewMockUserCreator(t)
	users.EXPECT().Create(ctx, "user", "hash").Return(auth.User{}, createErr).Once()
	passwords := authmocks.NewMockPasswordHasher(t)
	passwords.EXPECT().Hash("password").Return("hash", nil).Once()
	issuer := authmocks.NewMockTokenIssuer(t)
	service := auth.NewRegisterService(users, passwords, issuer)

	token, err := service.Register(ctx, auth.Credentials{Login: "user", Password: "password"})

	require.ErrorIs(t, err, createErr)
	assert.Zero(t, token)
}

func TestRegisterServiceKeepsCreatedUserWhenIssueFails(t *testing.T) {
	issueErr := errors.New("issue failed")
	created := false
	users := authmocks.NewMockUserCreator(t)
	users.EXPECT().Create(mock.Anything, "user", "hash").
		RunAndReturn(func(context.Context, string, string) (auth.User, error) {
			if created {
				return auth.User{}, auth.ErrLoginTaken
			}
			created = true
			return auth.User{ID: 42}, nil
		}).
		Twice()
	passwords := authmocks.NewMockPasswordHasher(t)
	passwords.EXPECT().Hash("password").Return("hash", nil).Twice()
	issuer := authmocks.NewMockTokenIssuer(t)
	issuer.EXPECT().Issue(int64(42)).Return(auth.IssuedToken{}, issueErr).Once()
	service := auth.NewRegisterService(users, passwords, issuer)
	credentials := auth.Credentials{Login: "user", Password: "password"}

	firstToken, err := service.Register(context.Background(), credentials)
	require.ErrorIs(t, err, issueErr)
	assert.Zero(t, firstToken)
	secondToken, err := service.Register(context.Background(), credentials)
	require.ErrorIs(t, err, auth.ErrLoginTaken)
	assert.Zero(t, secondToken)
	assert.True(t, created)
}
