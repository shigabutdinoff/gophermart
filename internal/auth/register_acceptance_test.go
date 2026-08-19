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
	"testing"
	"testing/synctest"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/shigabutdinoff/gophermart/internal/auth"
	authmocks "github.com/shigabutdinoff/gophermart/internal/auth/mocks"
	"github.com/shigabutdinoff/gophermart/internal/handlers/apiconfig"
	"github.com/shigabutdinoff/gophermart/internal/handlers/route/authentication"
	"github.com/shigabutdinoff/gophermart/internal/handlers/route/message"
	"github.com/shigabutdinoff/gophermart/internal/testsupport"
)

// registerHandler собирает маршрут регистрации так же, как это делает сервер.
func registerHandler(register authentication.CredentialsFunc) http.Handler {
	router := chi.NewRouter()
	api := apiconfig.NewAPI(router)
	authentication.RegisterRoutes(
		api,
		zap.NewNop(),
		authentication.Deps{Register: register},
		authentication.Options{BodyLimit: 1 << 20},
	)

	return router
}

// Обе регистрации доходят до хранилища и ждут там: пузырь подтверждает это
// сам, без сторожа на настоящих часах.
func TestRegisterConcurrentEquivalentLoginsCreateExactlyOneUser(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bodies := []string{
			`{"login":" ConcurrentUser ","password":"password"}`,
			`{"login":"concurrentuser","password":"password"}`,
		}
		state := newAcceptanceUserState()
		createArrived := make(chan struct{}, len(bodies))
		createRelease := make(chan struct{})
		releaseCreates := sync.OnceFunc(func() { close(createRelease) })
		defer releaseCreates()
		state.createArrived = createArrived
		state.createRelease = createRelease
		users := authmocks.NewMockUserCreator(t)
		users.EXPECT().Create(
			mock.Anything,
			"concurrentuser",
			mock.MatchedBy(func(passwordHash string) bool {
				return passwordHash != "" && passwordHash != "password"
			}),
		).RunAndReturn(state.create).Twice()
		tokens := authmocks.NewMockTokenIssuer(t)
		tokens.EXPECT().Issue(int64(1)).Return(acceptanceIssuedToken(1), nil).Once()
		service := auth.NewRegisterService(users, auth.Argon2Passwords{}, tokens)
		handler := registerHandler(service.Register)
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
			testsupport.WaitValue(t, createArrived, "регистрация не дошла до хранилища")
		}
		releaseCreates()
		wait.Wait()

		statuses := []int{responses[0].Code, responses[1].Code}
		assert.ElementsMatch(t, []int{http.StatusOK, http.StatusConflict}, statuses)
		for _, response := range responses {
			if response.Code == http.StatusOK {
				assert.NotEmpty(t, response.Header().Get("Authorization"))
				assert.Len(t, response.Header().Values("Set-Cookie"), 1)
				continue
			}
			assert.Contains(t, response.Body.String(), authentication.MessageLoginTaken)
			assert.Equal(t, "application/problem+json", response.Header().Get("Content-Type"))
			assert.Empty(t, response.Header().Get("Authorization"))
			assert.Empty(t, response.Header().Values("Set-Cookie"))
		}

		persisted := state.snapshot()
		require.Len(t, persisted, 1)
		assert.Equal(t, "concurrentuser", persisted[0].Login)
		assert.NotEqual(t, "password", persisted[0].PasswordHash)
	})
}

func TestRegisterFailuresDoNotPersistUserOrReturnToken(t *testing.T) {
	tests := []struct {
		name       string
		hashErr    error
		storageErr error
		wantCreate int
	}{
		{
			name:       "hash failure",
			hashErr:    errors.New("hash failed"),
			wantCreate: 0,
		},
		{
			name:       "storage failure",
			storageErr: errors.New("insert failed"),
			wantCreate: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := newAcceptanceUserState()
			state.createErr = test.storageErr
			users := authmocks.NewMockUserCreator(t)
			passwords := authmocks.NewMockPasswordHasher(t)
			if test.hashErr != nil {
				passwords.EXPECT().Hash("password").Return("", test.hashErr).Once()
			} else {
				passwords.EXPECT().Hash("password").Return("password-hash", nil).Once()
				users.EXPECT().Create(mock.Anything, "user", "password-hash").
					RunAndReturn(state.create).
					Once()
			}
			tokens := authmocks.NewMockTokenIssuer(t)
			service := auth.NewRegisterService(users, passwords, tokens)
			handler := registerHandler(service.Register)
			request := httptest.NewRequest(
				http.MethodPost,
				"/api/user/register",
				strings.NewReader(`{"login":" User ","password":"password"}`),
			)
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			assert.Equal(t, http.StatusInternalServerError, response.Code)
			assert.Contains(
				t,
				response.Body.String(),
				message.Internal,
			)
			assert.Empty(t, response.Header().Get("Authorization"))
			assert.Empty(t, response.Header().Values("Set-Cookie"))
			assert.Empty(t, state.snapshot())
			users.AssertNumberOfCalls(t, "Create", test.wantCreate)
		})
	}
}

type acceptanceUserState struct {
	mu            sync.Mutex
	users         map[string]auth.User
	createErr     error
	createArrived chan<- struct{}
	createRelease <-chan struct{}
}

func newAcceptanceUserState() *acceptanceUserState {
	return &acceptanceUserState{users: make(map[string]auth.User)}
}

func (s *acceptanceUserState) create(
	_ context.Context,
	login string,
	passwordHash string,
) (auth.User, error) {
	if s.createArrived != nil {
		s.createArrived <- struct{}{}
		<-s.createRelease
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.createErr != nil {
		return auth.User{}, s.createErr
	}
	if _, exists := s.users[login]; exists {
		return auth.User{}, auth.ErrLoginTaken
	}

	user := auth.User{
		ID:           int64(len(s.users) + 1),
		Login:        login,
		PasswordHash: passwordHash,
	}
	s.users[login] = user
	return user, nil
}

func (s *acceptanceUserState) snapshot() []auth.User {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Collect(maps.Values(s.users))
}

func acceptanceIssuedToken(userID int64) auth.IssuedToken {
	issuedAt := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	return auth.IssuedToken{
		Value:     fmt.Sprintf("token-%d", userID),
		IssuedAt:  issuedAt,
		ExpiresAt: issuedAt.Add(auth.TokenTTL),
	}
}
