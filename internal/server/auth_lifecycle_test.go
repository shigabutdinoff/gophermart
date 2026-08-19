package server

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/shigabutdinoff/gophermart/internal/auth"
	config "github.com/shigabutdinoff/gophermart/internal/config/gophermart"
	ordersroute "github.com/shigabutdinoff/gophermart/internal/handlers/route/orders"
	"github.com/shigabutdinoff/gophermart/internal/order"
)

func TestIndependentSecretsAreIsolatedOnProtectedOrderRoute(t *testing.T) {
	const firstSecret = "0123456789abcdef0123456789abcdef"
	now := func() time.Time {
		return time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	}
	firstTokens, err := auth.NewJWTManager([]byte(firstSecret), now)
	require.NoError(t, err)
	secondTokens, err := auth.NewJWTManager(
		[]byte(strings.Repeat("a", len(firstSecret))),
		now,
	)
	require.NoError(t, err)
	issued, err := firstTokens.Issue(42)
	require.NoError(t, err)

	firstCalls := 0
	first := &Server{
		logger:      zap.NewNop(),
		tokenParser: firstTokens.ParseRequest,
		orderDeps: ordersroute.Deps{List: func(context.Context, int64) ([]order.Order, error) {
			firstCalls++
			return nil, nil
		}},
		Config: config.Default(),
	}
	first.setupRoutes()
	secondCalls := 0
	second := &Server{
		logger:      zap.NewNop(),
		tokenParser: secondTokens.ParseRequest,
		orderDeps: ordersroute.Deps{List: func(context.Context, int64) ([]order.Order, error) {
			secondCalls++
			return nil, nil
		}},
		Config: config.Default(),
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

func TestRouterMissingAuthSchemaAfterMigrationFailureReturnsControlledError(t *testing.T) {
	core, logs := observer.New(zap.InfoLevel)
	cfg := config.Default()
	cfg.DatabaseURI = "postgresql://user:password@database/gophermart"
	gormDB, sqlDB, _ := newControllableDatabase(t, true, false)
	require.NoError(t, gormDB.Callback().Create().Before("gorm:create").Register(
		"test:missing-users-table",
		func(tx *gorm.DB) {
			tx.AddError(missingUsersTableError())
		},
	))
	require.NoError(t, gormDB.Callback().Query().Before("gorm:query").Register(
		"test:query-missing-users-table",
		func(tx *gorm.DB) {
			tx.AddError(missingUsersTableError())
		},
	))
	server, err := newServer(zap.New(core), cfg, time.Now, gormDB, sqlDB)
	require.NoError(t, err)
	t.Cleanup(server.closeDatabase)
	migrationErr := errors.New("migration failed")

	server.initDatabase(
		context.Background(),
		func(context.Context, *sql.DB) error { return migrationErr },
	)

	require.Equal(t, 1, logs.FilterMessage("Не удалось применить миграции").Len())
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

type controllableConnector struct {
	available atomic.Bool
}

func (c *controllableConnector) Connect(context.Context) (driver.Conn, error) {
	return &controllableConnection{availability: &c.available}, nil
}

func (*controllableConnector) Driver() driver.Driver {
	return controllableDriver{}
}

// Драйвер нужен интерфейсу коннектора, соединения выдаёт только Connect.
type controllableDriver struct{}

func (controllableDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("connections come from Connect")
}

type controllableConnection struct {
	availability *atomic.Bool
}

func (*controllableConnection) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("query is not supported")
}

func (*controllableConnection) Close() error { return nil }

func (*controllableConnection) Begin() (driver.Tx, error) {
	return nil, errors.New("transaction is not supported")
}

func (c *controllableConnection) Ping(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !c.availability.Load() {
		return errors.New("database is unavailable")
	}
	return nil
}

func newControllableDatabase(
	t *testing.T,
	available bool,
	dryRun bool,
) (*gorm.DB, *sql.DB, *atomic.Bool) {
	t.Helper()
	connector := &controllableConnector{}
	connector.available.Store(available)
	sqlDB := sql.OpenDB(connector)
	gormDB, err := gorm.Open(
		postgres.New(postgres.Config{Conn: sqlDB, PreferSimpleProtocol: true}),
		&gorm.Config{
			DisableAutomaticPing: true,
			DryRun:               dryRun,
			Logger:               gormlogger.Default.LogMode(gormlogger.Silent),
			// как в database.Connection, иначе дубль логина не станет ErrDuplicatedKey
			TranslateError:         true,
			SkipDefaultTransaction: true,
		},
	)
	require.NoError(t, err)
	return gormDB, sqlDB, &connector.available
}

// registerMissingUserQuery заставляет поиск пользователя отвечать «не найден».
// В DryRun запрос не выполняется, поэтому иначе поиск считался бы успешным.
func registerMissingUserQuery(t *testing.T, gormDB *gorm.DB, availability *atomic.Bool) {
	t.Helper()

	require.NoError(t, gormDB.Callback().Query().Before("gorm:query").Register(
		"test:query-missing-user",
		func(tx *gorm.DB) {
			if availability != nil && !availability.Load() {
				tx.AddError(errors.New("database is unavailable"))
				return
			}
			tx.AddError(gorm.ErrRecordNotFound)
		},
	))
}

func missingUsersTableError() error {
	return &pgconn.PgError{
		Code:    "42P01",
		Message: `relation "users" does not exist`,
	}
}
