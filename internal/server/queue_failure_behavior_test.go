package server

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	orderrepository "github.com/shigabutdinoff/gophermart/internal/repository/order"
	servermocks "github.com/shigabutdinoff/gophermart/internal/server/mocks"
)

type queueStartupFailureThrottleLifecycle struct {
	resumeErr error
	stopCtx   context.Context
}

func (t *queueStartupFailureThrottleLifecycle) resume(context.Context) error {
	return t.resumeErr
}

func (t *queueStartupFailureThrottleLifecycle) stop(ctx context.Context) error {
	t.stopCtx = ctx

	return nil
}

func (t *queueStartupFailureThrottleLifecycle) mock(testingT interface {
	mock.TestingT
	Cleanup(func())
}) *servermocks.MockThrottleLifecycle {
	lifecycle := servermocks.NewMockThrottleLifecycle(testingT)
	lifecycle.EXPECT().Resume(mock.Anything).RunAndReturn(t.resume).Maybe()
	lifecycle.EXPECT().Stop(mock.Anything).RunAndReturn(t.stop).Once()

	return lifecycle
}

func TestNewQueueClient_ReportsUnusableQueueSettings(t *testing.T) {
	cfg := newTestConfig()
	// очередь не берёт задания чаще, чем раз в FetchCooldown
	cfg.Queue.FetchPollInterval = time.Millisecond
	session, sqlDB := openDatabase(zap.NewNop(), unavailableDatabaseDSN)
	require.NotNil(t, sqlDB)
	t.Cleanup(func() { _ = sqlDB.Close() })

	_, err := newQueueClient(queueOptions{
		logger:          zap.NewNop(),
		queue:           cfg.Queue,
		accrualAddress:  cfg.AccrualAddress,
		storedOrders:    orderrepository.New(session),
		sqlDB:           sqlDB,
		shutdownTimeout: DefaultShutdownTimeout,
	})

	require.Error(t, err, "иначе загрузка заказов молча отвечала бы отказом")
	assert.ErrorContains(t, err, "start job queue")
}

func TestNew_FailsOnUnusableQueueSettings(t *testing.T) {
	cfg := newTestConfig()
	cfg.DatabaseURI = unavailableDatabaseDSN
	cfg.Queue.FetchPollInterval = time.Millisecond

	_, err := New(zap.NewNop(), cfg)

	require.Error(t, err)
}

func TestServer_KeepsServingWhenQueueCannotStart(t *testing.T) {
	sqlDB, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	mock.ExpectPing()
	mock.ExpectClose()

	core, logs := observer.New(zap.InfoLevel)
	resumeErr := errors.New("resume queue")
	lifecycle := servermocks.NewMockRiverLifecycle(t)
	throttle := &queueStartupFailureThrottleLifecycle{resumeErr: resumeErr}
	s := &Server{
		router:          chi.NewRouter(),
		logger:          zap.New(core),
		shutdownTimeout: 50 * time.Millisecond,
		retryDelay:      time.Millisecond,
		migrateDatabase: successfulTestMigration,
		sqlDB:           sqlDB,
		deps: deps{
			runner: newRiverRunner(lifecycle, throttle.mock(t)),
		},
	}
	s.runAddress = "127.0.0.1:0"
	router := chi.NewRouter()
	router.Get("/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	s.router = router

	ctx, cancel := context.WithCancel(context.Background())
	done := startServer(t, ctx, s)
	require.Eventually(t, func() bool {
		return logs.FilterMessage("Повторная попытка запустить очередь заданий").Len() >= 1
	}, time.Second, time.Millisecond)

	resp, err := http.Get("http://" + s.Addr() + "/")
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)

	cancel()
	require.NoError(t, waitDone(t, done))
	require.NoError(t, mock.ExpectationsWereMet())
	assert.Zero(
		t,
		logs.FilterMessage("Не удалось запустить очередь заданий").Len(),
		"штатная остановка не отчитывается отказом запуска",
	)
	assert.NotNil(t, throttle.stopCtx, "shutdown повторяет cleanup через throttle.Stop")
}
