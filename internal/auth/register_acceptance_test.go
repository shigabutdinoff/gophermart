package auth_test

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/shigabutdinoff/gophermart/internal/auth"
	"github.com/shigabutdinoff/gophermart/internal/handlers/route/authentication"
)

func TestRegisterConcurrentEquivalentLoginsCreateExactlyOneUser(t *testing.T) {
	bodies := []string{
		`{"login":" ConcurrentUser ","password":"password"}`,
		`{"login":"concurrentuser","password":"password"}`,
	}
	users := newAcceptanceUserRepository()
	createArrived := make(chan struct{}, len(bodies))
	createRelease := make(chan struct{})
	releaseCreates := sync.OnceFunc(func() { close(createRelease) })
	defer releaseCreates()
	users.createArrived = createArrived
	users.createRelease = createRelease
	tokens := &acceptanceTokenIssuer{}
	service := auth.NewRegisterService(users, auth.Argon2Passwords{}, tokens)
	handler := authentication.Register(zap.NewNop(), time.Now, service.Register)
	responses := make([]*httptest.ResponseRecorder, len(bodies))
	start := make(chan struct{})
	var wait sync.WaitGroup

	for index, body := range bodies {
		wait.Go(func() {
			request := httptest.NewRequest(
				http.MethodPost,
				"/api/user/register",
				strings.NewReader(body),
			)
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			<-start
			handler.ServeHTTP(response, request)
			responses[index] = response
		})
	}
	close(start)
	for range bodies {
		select {
		case <-createArrived:
		case <-time.After(5 * time.Second):
			require.FailNow(t, "concurrent registration did not reach the repository")
		}
	}
	releaseCreates()
	wait.Wait()

	statuses := []int{responses[0].Code, responses[1].Code}
	assert.ElementsMatch(t, []int{http.StatusOK, http.StatusConflict}, statuses)
	for _, response := range responses {
		if response.Code == http.StatusOK {
			assert.JSONEq(
				t,
				`{"message":"`+authentication.MessageRegistered+`"}`,
				response.Body.String(),
			)
			assert.NotEmpty(t, response.Header().Get("Authorization"))
			assert.Len(t, response.Header().Values("Set-Cookie"), 1)
			continue
		}
		assert.JSONEq(
			t,
			`{"message":"`+authentication.MessageLoginTaken+`"}`,
			response.Body.String(),
		)
		assert.Empty(t, response.Header().Get("Authorization"))
		assert.Empty(t, response.Header().Values("Set-Cookie"))
	}

	persisted := users.snapshot()
	require.Len(t, persisted, 1)
	assert.Equal(t, "concurrentuser", persisted[0].Login)
	assert.NotEqual(t, "password", persisted[0].PasswordHash)
	assert.Equal(t, 2, users.createCallsCount())
	assert.Equal(t, int64(1), tokens.calls.Load())
}

func TestRegisterFailuresDoNotPersistUserOrReturnToken(t *testing.T) {
	tests := []struct {
		name       string
		passwords  auth.PasswordHasher
		storageErr error
		wantCreate int
	}{
		{
			name:       "hash failure",
			passwords:  acceptancePasswords{hashErr: errors.New("hash failed")},
			wantCreate: 0,
		},
		{
			name:       "storage failure",
			passwords:  acceptancePasswords{},
			storageErr: errors.New("insert failed"),
			wantCreate: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			users := newAcceptanceUserRepository()
			users.createErr = test.storageErr
			tokens := &acceptanceTokenIssuer{}
			service := auth.NewRegisterService(users, test.passwords, tokens)
			handler := authentication.Register(zap.NewNop(), time.Now, service.Register)
			request := httptest.NewRequest(
				http.MethodPost,
				"/api/user/register",
				strings.NewReader(`{"login":" User ","password":"password"}`),
			)
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			assert.Equal(t, http.StatusInternalServerError, response.Code)
			assert.JSONEq(
				t,
				`{"message":"`+authentication.MessageInternalError+`"}`,
				response.Body.String(),
			)
			assert.Empty(t, response.Header().Get("Authorization"))
			assert.Empty(t, response.Header().Values("Set-Cookie"))
			assert.Empty(t, users.snapshot())
			assert.Equal(t, test.wantCreate, users.createCallsCount())
			assert.Zero(t, tokens.calls.Load())
		})
	}
}

type acceptanceUserRepository struct {
	mu            sync.Mutex
	users         map[string]auth.User
	createCalls   int
	createErr     error
	createArrived chan<- struct{}
	createRelease <-chan struct{}
}

func newAcceptanceUserRepository() *acceptanceUserRepository {
	return &acceptanceUserRepository{users: make(map[string]auth.User)}
}

func (r *acceptanceUserRepository) Create(
	_ context.Context,
	login string,
	passwordHash string,
) (auth.User, error) {
	if r.createArrived != nil {
		r.createArrived <- struct{}{}
		<-r.createRelease
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.createCalls++
	if r.createErr != nil {
		return auth.User{}, r.createErr
	}
	if _, exists := r.users[login]; exists {
		return auth.User{}, auth.ErrLoginTaken
	}

	user := auth.User{
		ID:           int64(len(r.users) + 1),
		Login:        login,
		PasswordHash: passwordHash,
	}
	r.users[login] = user
	return user, nil
}

func (r *acceptanceUserRepository) FindByLogin(
	_ context.Context,
	login string,
) (auth.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	user, exists := r.users[login]
	if !exists {
		return auth.User{}, auth.ErrUserNotFound
	}
	return user, nil
}

func (r *acceptanceUserRepository) snapshot() []auth.User {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Collect(maps.Values(r.users))
}

func (r *acceptanceUserRepository) createCallsCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.createCalls
}

type acceptancePasswords struct {
	hashErr error
}

func (p acceptancePasswords) Hash(string) (string, error) {
	if p.hashErr != nil {
		return "", p.hashErr
	}
	return "password-hash", nil
}

func (acceptancePasswords) Verify(string, string) error { return nil }

type acceptanceTokenIssuer struct {
	calls atomic.Int64
}

func (i *acceptanceTokenIssuer) Issue(userID int64) (auth.IssuedToken, error) {
	i.calls.Add(1)
	issuedAt := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	return auth.IssuedToken{
		Value:     fmt.Sprintf("token-%d", userID),
		IssuedAt:  issuedAt,
		ExpiresAt: issuedAt.Add(auth.TokenTTL),
	}, nil
}
