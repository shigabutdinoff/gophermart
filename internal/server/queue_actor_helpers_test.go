package server

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/mock"

	servermocks "github.com/shigabutdinoff/gophermart/internal/server/mocks"
)

type queueActorContextKey struct{}

type queueActorRiverLifecycle struct {
	mu sync.Mutex

	calls         []string
	startCtx      context.Context
	startFailures int
	startCalled   chan struct{}
	stoppedCalled chan struct{}
	stopped       chan struct{}
	stoppedClosed bool
}

func newQueueActorRiverLifecycle() *queueActorRiverLifecycle {
	return &queueActorRiverLifecycle{
		startCalled:   make(chan struct{}, 1),
		stoppedCalled: make(chan struct{}, 1),
		stopped:       make(chan struct{}),
	}
}

func (f *queueActorRiverLifecycle) start(ctx context.Context) error {
	f.record("start")
	f.mu.Lock()
	f.startCtx = ctx
	failing := f.startFailures > 0
	if failing {
		f.startFailures--
	}
	if f.stoppedClosed {
		f.stopped = make(chan struct{})
		f.stoppedClosed = false
	}
	f.mu.Unlock()
	notify(f.startCalled)
	if failing {
		return errors.New("job queue is not ready")
	}

	return nil
}

func (f *queueActorRiverLifecycle) stop(context.Context) error {
	f.record("stop")

	return nil
}

func (f *queueActorRiverLifecycle) stopAndCancel(context.Context) error {
	f.record("stop_and_cancel")

	return nil
}

func (f *queueActorRiverLifecycle) stoppedChannel() <-chan struct{} {
	f.record("stopped")
	notify(f.stoppedCalled)
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.stopped
}

func (f *queueActorRiverLifecycle) mock(t *testing.T) *servermocks.MockRiverLifecycle {
	t.Helper()
	lifecycle := servermocks.NewMockRiverLifecycle(t)
	lifecycle.EXPECT().Start(mock.Anything).RunAndReturn(f.start).Maybe()
	lifecycle.EXPECT().Stop(mock.Anything).RunAndReturn(f.stop).Maybe()
	lifecycle.EXPECT().StopAndCancel(mock.Anything).RunAndReturn(f.stopAndCancel).Maybe()
	lifecycle.EXPECT().Stopped().RunAndReturn(f.stoppedChannel).Maybe()

	return lifecycle
}

func (f *queueActorRiverLifecycle) record(call string) {
	f.mu.Lock()
	f.calls = append(f.calls, call)
	f.mu.Unlock()
}

func (f *queueActorRiverLifecycle) callLog() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]string(nil), f.calls...)
}

func (f *queueActorRiverLifecycle) closeStopped() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.stoppedClosed {
		return
	}

	close(f.stopped)
	f.stoppedClosed = true
}

func newQueueActorThrottle(t *testing.T) *servermocks.MockThrottleLifecycle {
	t.Helper()
	throttle := servermocks.NewMockThrottleLifecycle(t)
	throttle.EXPECT().Resume(mock.Anything).Return(nil).Maybe()
	throttle.EXPECT().Stop(mock.Anything).Return(nil).Maybe()

	return throttle
}

type queueActorPendingOrderResumer struct {
	mu       sync.Mutex
	inserted int
	err      error
	calls    int
}

func (f *queueActorPendingOrderResumer) resume(context.Context) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++

	return f.inserted, f.err
}

func (f *queueActorPendingOrderResumer) mock(t *testing.T) *servermocks.MockPendingOrderResumer {
	t.Helper()
	resumer := servermocks.NewMockPendingOrderResumer(t)
	resumer.EXPECT().Resume(mock.Anything).RunAndReturn(f.resume).Maybe()

	return resumer
}

func (f *queueActorPendingOrderResumer) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.calls
}

func closedDatabaseReadiness() <-chan struct{} {
	ready := make(chan struct{})
	close(ready)

	return ready
}
