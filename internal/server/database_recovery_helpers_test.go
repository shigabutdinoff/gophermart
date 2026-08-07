package server

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/mock"

	servermocks "github.com/shigabutdinoff/gophermart/internal/server/mocks"
)

type scriptedPingConnector struct {
	ping  func(int32) error
	calls atomic.Int32
}

func (c *scriptedPingConnector) Connect(context.Context) (driver.Conn, error) {
	return &scriptedPingConnection{connector: c}, nil
}

func (*scriptedPingConnector) Driver() driver.Driver {
	return scriptedPingDriver{}
}

type scriptedPingDriver struct{}

func (scriptedPingDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("scripted ping driver requires its connector")
}

type scriptedPingConnection struct {
	connector *scriptedPingConnector
}

func (*scriptedPingConnection) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare is not supported")
}

func (*scriptedPingConnection) Close() error {
	return nil
}

func (*scriptedPingConnection) Begin() (driver.Tx, error) {
	return nil, errors.New("transactions are not supported")
}

func (c *scriptedPingConnection) Ping(context.Context) error {
	call := c.connector.calls.Add(1)
	return c.connector.ping(call)
}

type databaseEventLog struct {
	mu     sync.Mutex
	events []string
}

type blockingTestListener struct {
	closed chan struct{}
	once   sync.Once
}

func newBlockingTestListener() *blockingTestListener {
	return &blockingTestListener{closed: make(chan struct{})}
}

func (l *blockingTestListener) Accept() (net.Conn, error) {
	<-l.closed
	return nil, net.ErrClosed
}

func (l *blockingTestListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (*blockingTestListener) Addr() net.Addr {
	return staticTestAddress("database-recovery")
}

type staticTestAddress string

func (staticTestAddress) Network() string {
	return "test"
}

func (a staticTestAddress) String() string {
	return string(a)
}

func (l *databaseEventLog) add(event string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, event)
}

func (l *databaseEventLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.events...)
}

type databaseRecoveryRiverLifecycle struct {
	events        *databaseEventLog
	startCalled   chan struct{}
	stoppedCalled chan struct{}
	stopped       chan struct{}
}

func newDatabaseRecoveryRiverLifecycle(events *databaseEventLog) *databaseRecoveryRiverLifecycle {
	return &databaseRecoveryRiverLifecycle{
		events:        events,
		startCalled:   make(chan struct{}, 1),
		stoppedCalled: make(chan struct{}, 1),
		stopped:       make(chan struct{}),
	}
}

func (f *databaseRecoveryRiverLifecycle) start(context.Context) error {
	f.events.add("start")
	notify(f.startCalled)

	return nil
}

func (f *databaseRecoveryRiverLifecycle) stop(context.Context) error {
	f.events.add("stop")
	close(f.stopped)

	return nil
}

func (f *databaseRecoveryRiverLifecycle) stopAndCancel(context.Context) error {
	f.events.add("stop_and_cancel")

	return nil
}

func (f *databaseRecoveryRiverLifecycle) stoppedChannel() <-chan struct{} {
	f.events.add("stopped")
	notify(f.stoppedCalled)

	return f.stopped
}

func (f *databaseRecoveryRiverLifecycle) mock(t *testing.T) *servermocks.MockRiverLifecycle {
	t.Helper()
	lifecycle := servermocks.NewMockRiverLifecycle(t)
	lifecycle.EXPECT().Start(mock.Anything).RunAndReturn(f.start).Maybe()
	lifecycle.EXPECT().Stop(mock.Anything).RunAndReturn(f.stop).Maybe()
	lifecycle.EXPECT().StopAndCancel(mock.Anything).RunAndReturn(f.stopAndCancel).Maybe()
	lifecycle.EXPECT().Stopped().RunAndReturn(f.stoppedChannel).Maybe()

	return lifecycle
}

func newDatabaseRecoveryThrottle(
	t *testing.T,
	events *databaseEventLog,
) *servermocks.MockThrottleLifecycle {
	t.Helper()
	throttle := servermocks.NewMockThrottleLifecycle(t)
	throttle.EXPECT().Resume(mock.Anything).Run(func(context.Context) {
		events.add("resume")
	}).Return(nil).Maybe()
	throttle.EXPECT().Stop(mock.Anything).Run(func(context.Context) {
		events.add("throttle_stop")
	}).Return(nil).Maybe()

	return throttle
}

func newScriptedPingDatabase(
	t *testing.T,
	ping func(int32) error,
) (*sql.DB, *scriptedPingConnector) {
	t.Helper()
	connector := &scriptedPingConnector{ping: ping}
	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db, connector
}
