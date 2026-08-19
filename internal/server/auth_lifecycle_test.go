package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"gorm.io/gorm"

	"github.com/shigabutdinoff/gophermart/internal/auth"
	ordersroute "github.com/shigabutdinoff/gophermart/internal/handlers/route/orders"
	"github.com/shigabutdinoff/gophermart/internal/order"
	"github.com/shigabutdinoff/gophermart/internal/repository/database"
	"github.com/shigabutdinoff/gophermart/internal/repository/testkit"
)

func TestIndependentSecretsAreIsolatedOnProtectedOrderRoute(t *testing.T) {
	firstTokens, err := auth.NewJWTManager([]byte(testJWTSecret))
	require.NoError(t, err)
	secondTokens, err := auth.NewJWTManager(
		[]byte(strings.Repeat("a", len(testJWTSecret))),
	)
	require.NoError(t, err)
	issued, err := firstTokens.Issue(42)
	require.NoError(t, err)

	firstCalls := 0
	first := &Server{
		logger: zap.NewNop(),
		deps: deps{
			tokenParser: firstTokens.ParseRequest,
			orders: ordersroute.Deps{List: func(context.Context, int64) ([]order.Order, error) {
				firstCalls++
				return nil, nil
			}},
		},
	}
	first.setupRoutes()
	secondCalls := 0
	second := &Server{
		logger: zap.NewNop(),
		deps: deps{
			tokenParser: secondTokens.ParseRequest,
			orders: ordersroute.Deps{List: func(context.Context, int64) ([]order.Order, error) {
				secondCalls++
				return nil, nil
			}},
		},
	}
	second.setupRoutes()

	protected := func(server *Server) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, "/api/user/orders", http.NoBody)
		request.Header.Set("Authorization", "Bearer "+issued.Value)
		response := httptest.NewRecorder()
		server.router.ServeHTTP(response, request)
		return response
	}

	assert.Equal(t, http.StatusNoContent, protected(first).Code)
	assert.Equal(t, 1, firstCalls, "valid token reaches the business operation without a user lookup")
	assert.Equal(t, http.StatusUnauthorized, protected(second).Code)
	assert.Zero(t, secondCalls)
}

func TestRouterDatabaseHandleRecoversWithoutRestart(t *testing.T) {
	sqlDB, mock := newPingMock(t, errors.New("database is unavailable"))
	// второй пинг отвечает уже восстановленной базой
	mock.ExpectPing()
	session, gormDB := testkit.NewDryRunSession(t)
	var availability atomic.Bool
	require.NoError(t, gormDB.Callback().Create().Before("gorm:create").Register(
		"test:recoverable-users-table",
		func(tx *gorm.DB) {
			if !availability.Load() {
				tx.AddError(errors.New("database is unavailable"))
				return
			}
			tx.Statement.SetColumn("ID", int64(42))
		},
	))
	require.NoError(t, gormDB.Callback().Query().Before("gorm:query").Register(
		"test:query-missing-user",
		func(tx *gorm.DB) {
			if !availability.Load() {
				tx.AddError(errors.New("database is unavailable"))
				return
			}
			tx.AddError(gorm.ErrRecordNotFound)
		},
	))
	server, err := newServer(zap.NewNop(), newTestConfig(), session, sqlDB)
	require.NoError(t, err)
	t.Cleanup(server.closeDatabase)
	router := server.router

	pingBefore := serveLifecycleRequest(router, http.MethodGet, "/ping", "")
	assert.Equal(t, http.StatusServiceUnavailable, pingBefore.Code)
	registerBefore := serveLifecycleRequest(
		router,
		http.MethodPost,
		"/api/user/register",
		`{"login":"before-recovery","password":"password"}`,
	)
	assertLifecycleResponse(t, registerBefore, http.StatusInternalServerError)

	availability.Store(true)

	pingAfter := serveLifecycleRequest(router, http.MethodGet, "/ping", "")
	assert.Equal(t, http.StatusOK, pingAfter.Code)
	registerAfter := serveLifecycleRequest(
		router,
		http.MethodPost,
		"/api/user/register",
		`{"login":"after-recovery","password":"password"}`,
	)
	assertLifecycleResponse(t, registerAfter, http.StatusOK)
	assert.NotEmpty(t, registerAfter.Header().Get("Authorization"))
	assert.Len(t, registerAfter.Header().Values("Set-Cookie"), 1)
	assert.Same(t, router, server.router)
}

func TestRouterMissingAuthSchemaAfterMigrationFailureReturnsControlledError(t *testing.T) {
	core, logs := observer.New(zap.InfoLevel)
	cfg := newTestConfig()
	cfg.DatabaseURI = "postgresql://user:password@database/gophermart"
	sqlDB, schemaMock := newPingMock(t, nil)
	// проба готовности спрашивает базу после миграций
	schemaMock.ExpectPing()
	gormDB := testkit.OpenDryRunGORM(t, sqlDB)
	testkit.FailStatements(t, gormDB, missingUsersTableError())
	server, err := newServer(zap.New(core), cfg, database.NewSession(gormDB), sqlDB)
	require.NoError(t, err)
	t.Cleanup(server.closeDatabase)
	migrationErr := errors.New("migration failed")

	server.initDatabase(
		context.Background(),
		func(context.Context, *sql.DB) error { return migrationErr },
	)

	require.Equal(t, 1, logs.FilterMessage("Не удалось применить миграции").Len())
	ping := serveLifecycleRequest(server.router, http.MethodGet, "/ping", "")
	assert.Equal(t, http.StatusOK, ping.Code)
	for _, path := range []string{"/api/user/register", "/api/user/login"} {
		t.Run(path, func(t *testing.T) {
			response := serveLifecycleRequest(
				server.router,
				http.MethodPost,
				path,
				`{"login":"missing-schema","password":"password"}`,
			)
			assertLifecycleResponse(t, response, http.StatusInternalServerError)
		})
	}

	// оба маршрута доносят до лога именно отсутствующую таблицу
	assert.Equal(t, 2, logs.Filter(func(entry observer.LoggedEntry) bool {
		reason, ok := entry.ContextMap()["error"].(string)

		return ok && strings.Contains(reason, `relation "users" does not exist`)
	}).Len())
}

func serveLifecycleRequest(
	handler http.Handler,
	method string,
	path string,
	body string,
) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

// assertLifecycleResponse проверяет статус и тело ответа маршрута.
func assertLifecycleResponse(
	t *testing.T,
	response *httptest.ResponseRecorder,
	status int,
) {
	t.Helper()
	assert.Equal(t, status, response.Code)
	if status == http.StatusOK {
		// успех несёт токен заголовком и cookie, тело задано ТЗ пустым
		assert.Empty(t, response.Body.Bytes())
		return
	}

	assert.Equal(t, "application/problem+json", response.Header().Get("Content-Type"))
	var problem struct {
		Title  string `json:"title"`
		Detail string `json:"detail"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &problem))
	assert.NotEmpty(t, problem.Detail)
	assert.Empty(t, response.Header().Get("Authorization"))
	assert.Empty(t, response.Header().Values("Set-Cookie"))
}

func missingUsersTableError() error {
	return &pgconn.PgError{
		Code:    "42P01",
		Message: `relation "users" does not exist`,
	}
}
