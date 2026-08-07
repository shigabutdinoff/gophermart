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

	config "github.com/shigabutdinoff/gophermart/internal/config/gophermart"
)

// Два сервера собраны без общего env-файла, поэтому получают разные секреты.
func TestIndependentSecretsInvalidateTokenAcrossServers(t *testing.T) {
	now := func() time.Time {
		return time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	}
	gormDB, sqlDB, _ := newControllableDatabase(t, true, true)
	require.NoError(t, gormDB.Callback().Create().Before("gorm:create").Register(
		"test:set-ephemeral-user-id",
		func(tx *gorm.DB) { tx.Statement.SetColumn("ID", int64(42)) },
	))
	registerMissingUserQuery(t, gormDB, nil)
	first, err := newServer(
		zap.NewNop(),
		config.Default(),
		now,
		gormDB,
		sqlDB,
	)
	require.NoError(t, err)
	t.Cleanup(first.closeDatabase)
	registration := serveLifecycleRequest(
		first.router,
		http.MethodPost,
		"/api/user/register",
		`{"login":"ephemeral-user","password":"password"}`,
	)
	require.Equal(t, http.StatusOK, registration.Code)
	authorization := registration.Header().Get("Authorization")
	require.NotEmpty(t, authorization)

	second, err := newServer(
		zap.NewNop(),
		config.Default(),
		now,
		nil,
		nil,
	)
	require.NoError(t, err)
	protected := func(server *Server) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, "/protected", http.NoBody)
		request.Header.Set("Authorization", authorization)
		response := httptest.NewRecorder()
		server.authorize(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})).ServeHTTP(response, request)
		return response
	}

	assert.Equal(t, http.StatusNoContent, protected(first).Code)
	afterRestart := protected(second)
	assert.Equal(t, http.StatusUnauthorized, afterRestart.Code)
	assert.Empty(t, afterRestart.Body.Bytes())
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

	server.checkDatabaseAndMigrate(
		context.Background(),
		sqlDB,
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

// assertLifecycleResponse проверяет статус и конверт сообщения в теле ответа.
func assertLifecycleResponse(
	t *testing.T,
	response *httptest.ResponseRecorder,
	status int,
) {
	t.Helper()
	assert.Equal(t, status, response.Code)
	var body struct {
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	assert.NotEmpty(t, body.Message)
	if status != http.StatusOK {
		assert.Empty(t, response.Header().Get("Authorization"))
		assert.Empty(t, response.Header().Values("Set-Cookie"))
	}
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
