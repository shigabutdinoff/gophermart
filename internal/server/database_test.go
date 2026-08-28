package server

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	config "github.com/shigabutdinoff/gophermart/internal/config/gophermart"
	"github.com/shigabutdinoff/gophermart/internal/repository/database"
	"github.com/shigabutdinoff/gophermart/internal/repository/testkit"
)

const unavailableDatabaseDSN = "postgresql://postgres:postgres@localhost:1/praktikum?sslmode=disable"

// newPingMock собирает пул, где каждый Ping отвечает своей ошибкой из errs
func newPingMock(t *testing.T, errs ...error) (*sql.DB, sqlmock.Sqlmock) {
	t.Helper()

	db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	for _, pingErr := range errs {
		mock.ExpectPing().WillReturnError(pingErr)
	}
	return db, mock
}

func newObservedServer(t *testing.T, dsn string) (*Server, *observer.ObservedLogs) {
	t.Helper()
	core, logs := observer.New(zap.InfoLevel)
	cfg := config.Default()
	cfg.DatabaseURI = dsn
	return mustNew(t, zap.New(core), cfg), logs
}

func newServerWithDatabase(t *testing.T) (*Server, *sql.DB) {
	t.Helper()
	session, err := database.Open(unavailableDatabaseDSN)
	require.NoError(t, err)
	sqlDB, err := session.Pool()
	require.NoError(t, err)
	server, err := newServer(zap.NewNop(), newTestConfig(), session, sqlDB)
	require.NoError(t, err)
	return server, sqlDB
}

func TestOpenDatabase_EmptyDSN(t *testing.T) {
	s, logs := newObservedServer(t, "")
	assert.Nil(t, s.sqlDB)
	require.Equal(t, 1, logs.FilterMessage("Не удалось открыть соединение с БД").Len())
}

func TestPinger_WithoutDatabaseReportsUnavailable(t *testing.T) {
	s, _ := newObservedServer(t, "")

	pinger := s.pinger()

	require.NotNil(t, pinger, "проверка готовности не должна встречать пустой Pinger")
	assert.ErrorIs(t, pinger.PingContext(context.Background()), database.ErrUnavailable)
}

func TestOpenDatabase_MalformedDSNDoesNotLeakCredentials(t *testing.T) {
	s, logs := newObservedServer(t, "postgresql://user:SECRETPW@bad host:5432/praktikum")
	assert.Nil(t, s.sqlDB)
	entries := logs.FilterMessage("Не удалось открыть соединение с БД").All()
	require.Len(t, entries, 1)
	assert.Equal(t, "некорректная строка подключения к БД", entries[0].ContextMap()["error"])
}

func TestInitDatabase_UnavailableDatabaseKeepsHandleAndSkipsMigration(t *testing.T) {
	s, logs := newObservedServer(t, unavailableDatabaseDSN)
	s.retryDelay = time.Millisecond
	migrationCalls := 0

	initErr := s.initDatabase(context.Background(), func(context.Context, *sql.DB) error {
		migrationCalls++
		return nil
	})

	require.Error(t, initErr)
	require.NotNil(t, s.sqlDB, "хэндл сохраняется для самовосстановления")
	t.Cleanup(s.closeDatabase)
	assert.Zero(t, migrationCalls)

	entries := logs.All()
	require.Len(t, entries, 4)
	assert.Equal(t, 3, logs.FilterMessage("Повторная попытка подключения к БД").Len())
	skipped := logs.FilterMessage("БД недоступна, миграции пропущены").All()
	require.Len(t, skipped, 1)
	assert.Equal(t, zap.ErrorLevel, skipped[0].Level)
	assert.NotEmpty(t, skipped[0].ContextMap()["error"])
	assert.Zero(t, logs.FilterMessage("Не удалось применить миграции").Len())
	assert.Zero(t, logs.FilterMessage("Миграции выполнены").Len())
}

func TestInitDatabase_SuccessfulPingHandlesMigrationResult(t *testing.T) {
	migrationErr := errors.New("migration failed")
	tests := []struct {
		name            string
		migrationErr    error
		expectedMessage string
		expectedLevel   zapcore.Level
	}{
		{
			name:            "migration error",
			migrationErr:    migrationErr,
			expectedMessage: "Не удалось применить миграции",
			expectedLevel:   zap.ErrorLevel,
		},
		{
			name:            "migrations applied",
			expectedMessage: "Миграции выполнены",
			expectedLevel:   zap.InfoLevel,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const dsn = "postgresql://user:password@db:5432/gophermart"
			s, logs := newObservedServer(t, dsn)
			handle, mock := newPingMock(t, nil)
			migrationCalls := 0
			var gotDB *sql.DB
			var gotCtx context.Context
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			s.sqlDB = handle
			initErr := s.initDatabase(
				ctx,
				func(migrateCtx context.Context, db *sql.DB) error {
					migrationCalls++
					gotCtx = migrateCtx
					gotDB = db
					return tt.migrationErr
				},
			)

			if tt.migrationErr == nil {
				require.NoError(t, initErr)
			} else {
				require.ErrorIs(t, initErr, tt.migrationErr)
			}
			require.NoError(t, mock.ExpectationsWereMet())
			assert.Equal(t, 1, migrationCalls)
			assert.Same(t, handle, gotDB)
			require.NoError(t, gotCtx.Err())
			cancel()
			assert.Error(t, gotCtx.Err(), "миграции обязаны видеть отмену контекста запуска")

			entries := logs.All()
			require.Len(t, entries, 1)
			assert.Equal(t, tt.expectedMessage, entries[0].Message)
			assert.Equal(t, tt.expectedLevel, entries[0].Level)
			if tt.migrationErr != nil {
				assert.Equal(t, tt.migrationErr.Error(), entries[0].ContextMap()["error"])
			}
			assert.Zero(t, logs.FilterMessage("БД недоступна, миграции пропущены").Len())
		})
	}
}

func TestInitDatabase_RetriesUntilDatabaseAppears(t *testing.T) {
	s, logs := newObservedServer(t, "postgresql://user:password@db:5432/gophermart")
	s.retryDelay = time.Millisecond
	refused := errors.New("connection refused")
	handle, mock := newPingMock(t, refused, nil)
	s.sqlDB = handle
	migrationCalls := 0

	s.initDatabase(context.Background(), func(context.Context, *sql.DB) error {
		migrationCalls++
		return nil
	})

	require.NoError(t, mock.ExpectationsWereMet())
	assert.Equal(t, 1, migrationCalls)
	assert.Equal(t, 1, logs.FilterMessage("Повторная попытка подключения к БД").Len())
	assert.Equal(t, 1, logs.FilterMessage("Миграции выполнены").Len())
}

func TestCloseDatabase_ClosedPoolFailsReadiness(t *testing.T) {
	s, _ := newServerWithDatabase(t)
	require.NotNil(t, s.pinger())

	s.closeDatabase()

	err := s.pinger().PingContext(context.Background())
	assert.EqualError(t, err, "sql: database is closed", "закрытый пул не проходит проверку готовности")
	assert.NotPanics(t, s.closeDatabase, "повторное закрытие безопасно")
}

func TestInitDatabase_LimitsEachPingAttempt(t *testing.T) {
	s, logs := newObservedServer(t, "postgresql://user:password@db:5432/gophermart")
	s.retryDelay = time.Millisecond
	handle, mock := newPingMock(t)
	// 800 мс меньше исходной секунды, но не укладывается в лимит одной попытки.
	mock.ExpectPing().WillDelayFor(800 * time.Millisecond)
	mock.ExpectPing()
	s.sqlDB = handle
	migrationCalls := 0

	s.initDatabase(context.Background(), func(context.Context, *sql.DB) error {
		migrationCalls++
		return nil
	})

	require.NoError(t, mock.ExpectationsWereMet())
	assert.Equal(t, 1, migrationCalls)
	assert.Equal(t, 1, logs.FilterMessage("Повторная попытка подключения к БД").Len())
}

func TestInitDatabase_GivesUpAfterConfiguredAttempts(t *testing.T) {
	s, logs := newObservedServer(t, "postgresql://user:SECRETPW@db:5432/gophermart")
	s.retryDelay = time.Millisecond
	refused := errors.New("connection refused")
	handle, mock := newPingMock(t, refused, refused, refused)
	s.sqlDB = handle
	migrationCalls := 0

	s.initDatabase(context.Background(), func(context.Context, *sql.DB) error {
		migrationCalls++
		return nil
	})

	require.NoError(t, mock.ExpectationsWereMet())
	assert.Zero(t, migrationCalls)
	assert.Equal(t, 1, logs.FilterMessage("БД недоступна, миграции пропущены").Len())
	assert.Equal(t, databaseConnectAttempts, logs.FilterMessage("Повторная попытка подключения к БД").Len())
	for _, entry := range logs.All() {
		assert.NotContains(t, entry.ContextMap()["error"], "SECRETPW")
	}
}

func TestInitDatabase_StopsRetryingOnCanceledContext(t *testing.T) {
	s, logs := newObservedServer(t, "postgresql://user:password@db:5432/gophermart")
	s.retryDelay = time.Hour
	refused := errors.New("connection refused")
	handle, mock := newPingMock(t, refused)
	s.sqlDB = handle
	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		require.Eventually(
			t,
			func() bool { return mock.ExpectationsWereMet() == nil },
			time.Second,
			time.Millisecond,
		)
		cancel()
	}()

	start := time.Now()
	s.initDatabase(ctx, func(context.Context, *sql.DB) error { return nil })

	assert.Less(t, time.Since(start), 5*time.Second, "ожидание обязано прерваться отменой")
	assert.Equal(t, 1, logs.FilterMessage("Повторная попытка подключения к БД").Len())
}

func TestOpenDatabase_AppliesPoolLimits(t *testing.T) {
	s, _ := newObservedServer(t, unavailableDatabaseDSN)
	require.NotNil(t, s.sqlDB)
	t.Cleanup(s.closeDatabase)
	assert.Equal(t, databaseMaxConns, s.sqlDB.Stats().MaxOpenConnections)
}

func TestServerSharesOnePoolAcrossRepositoryMigrationsAndShutdown(t *testing.T) {
	sqlDB, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)
	gormDB := testkit.OpenDryRunGORM(t, sqlDB)
	repositoryPool := testkit.ObserveCreatePool(t, gormDB)

	server, err := newServer(
		zap.NewNop(),
		newTestConfig(),
		database.NewSession(gormDB),
		sqlDB,
	)
	require.NoError(t, err)
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/user/register",
		strings.NewReader(`{"login":"user","password":"password"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	server.router.ServeHTTP(httptest.NewRecorder(), request)

	assert.Same(t, sqlDB, repositoryPool())
	assert.Same(t, sqlDB, server.pinger())

	mock.ExpectPing()
	var migrationPool *sql.DB
	server.initDatabase(context.Background(), func(_ context.Context, db *sql.DB) error {
		migrationPool = db
		return nil
	})
	assert.Same(t, sqlDB, migrationPool)

	mock.ExpectClose()
	server.closeDatabase()
	require.NoError(t, mock.ExpectationsWereMet())
	assert.EqualError(t, sqlDB.PingContext(context.Background()), "sql: database is closed")
}

func TestServer_Run_ClosesDatabaseOnListenError(t *testing.T) {
	s, oldDB := newServerWithDatabase(t)
	s.runAddress = "bad::addr"
	require.Error(t, s.Run(context.Background()))
	assert.EqualError(t, oldDB.PingContext(context.Background()), "sql: database is closed")
}

func TestCloseDatabaseWithin_ReturnsAtSharedDeadlineAndLeavesAttemptReleasable(t *testing.T) {
	core, logs := observer.New(zap.WarnLevel)
	called := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	err := closeDatabaseWithin(ctx, zap.New(core), func() error {
		close(called)
		defer close(finished)
		<-release
		return nil
	})

	require.ErrorIs(t, err, context.DeadlineExceeded)
	receiveWithin(t, called)
	assert.Equal(t, 1, logs.FilterMessage("Не удалось завершить закрытие соединения с БД").Len())

	close(release)
	receiveWithin(t, finished)
}

func TestCloseDatabaseWithin_ReportsCloseResult(t *testing.T) {
	closeErr := errors.New("close database")
	tests := []struct {
		name        string
		closeErr    error
		wantMessage string
	}{
		{name: "success"},
		{
			name:        "close error",
			closeErr:    closeErr,
			wantMessage: "Не удалось закрыть соединение с БД",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			core, logs := observer.New(zap.WarnLevel)
			calls := 0

			err := closeDatabaseWithin(context.Background(), zap.New(core), func() error {
				calls++
				return tt.closeErr
			})

			require.ErrorIs(t, err, tt.closeErr)
			assert.Equal(t, 1, calls)
			if tt.wantMessage == "" {
				assert.Zero(t, logs.Len())
			} else {
				assert.Equal(t, 1, logs.FilterMessage(tt.wantMessage).Len())
			}
		})
	}
}
