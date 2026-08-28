package server

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"

	servermocks "github.com/shigabutdinoff/gophermart/internal/server/mocks"
)

type shutdownCallRecorder struct {
	mu    sync.Mutex
	calls []string
}

func (r *shutdownCallRecorder) record(call string) {
	r.mu.Lock()
	r.calls = append(r.calls, call)
	r.mu.Unlock()
}

func (r *shutdownCallRecorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]string(nil), r.calls...)
}

type shutdownRiverLifecycle struct {
	mu sync.Mutex

	calls              []string
	startCtx           context.Context
	stopCtx            context.Context
	hardStopCtx        context.Context
	startCtxLiveAtStop bool
	stopCtxWasLive     bool
	stopCalledAt       time.Time
	stopDeadline       time.Time

	waitForStartContext bool
	waitForStopContext  bool
	closeOnStop         bool

	startCalled  chan struct{}
	stopped      chan struct{}
	startRelease chan struct{}
	external     *shutdownCallRecorder
}

func newShutdownRiverLifecycle() *shutdownRiverLifecycle {
	return &shutdownRiverLifecycle{
		startCalled:  make(chan struct{}, 1),
		stopped:      make(chan struct{}),
		startRelease: make(chan struct{}),
	}
}

func (f *shutdownRiverLifecycle) start(ctx context.Context) error {
	f.record("start")
	f.mu.Lock()
	f.startCtx = ctx
	f.mu.Unlock()
	notify(f.startCalled)
	if f.waitForStartContext {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-f.startRelease:
			return errors.New("blocked Start released by test cleanup")
		}
	}

	return nil
}

func (f *shutdownRiverLifecycle) stop(ctx context.Context) error {
	f.record("stop")
	f.mu.Lock()
	f.stopCtx = ctx
	f.stopCtxWasLive = ctx.Err() == nil
	f.startCtxLiveAtStop = f.startCtx != nil && f.startCtx.Err() == nil
	f.stopCalledAt = time.Now()
	f.stopDeadline, _ = ctx.Deadline()
	f.mu.Unlock()
	if f.waitForStopContext {
		<-ctx.Done()

		return ctx.Err()
	}
	if f.closeOnStop {
		f.closeStopped()
	}

	return nil
}

func (f *shutdownRiverLifecycle) stopAndCancel(ctx context.Context) error {
	f.record("stop_and_cancel")
	f.mu.Lock()
	f.hardStopCtx = ctx
	f.mu.Unlock()

	return nil
}

func (f *shutdownRiverLifecycle) stoppedChannel() <-chan struct{} {
	f.record("stopped")
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.stopped
}

func (f *shutdownRiverLifecycle) mock(t *testing.T) *servermocks.MockRiverLifecycle {
	t.Helper()
	lifecycle := servermocks.NewMockRiverLifecycle(t)
	lifecycle.EXPECT().Start(mock.Anything).RunAndReturn(f.start).Maybe()
	lifecycle.EXPECT().Stop(mock.Anything).RunAndReturn(f.stop).Maybe()
	lifecycle.EXPECT().StopAndCancel(mock.Anything).RunAndReturn(f.stopAndCancel).Maybe()
	lifecycle.EXPECT().Stopped().RunAndReturn(f.stoppedChannel).Maybe()

	return lifecycle
}

func (f *shutdownRiverLifecycle) record(call string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
	if f.external != nil {
		f.external.record(call)
	}
}

func (f *shutdownRiverLifecycle) callLog() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]string(nil), f.calls...)
}

func (f *shutdownRiverLifecycle) closeStopped() {
	close(f.stopped)
}

func (f *shutdownRiverLifecycle) releaseStart() {
	close(f.startRelease)
}

type shutdownThrottleLifecycle struct {
	external           *shutdownCallRecorder
	resumeErr          error
	stopCtx            context.Context
	stopCtxWasLive     bool
	stopCalledAt       time.Time
	stopDeadline       time.Time
	stopHasDeadline    bool
	waitForStopContext bool
	stopHook           func(context.Context)
	stopAfterContext   func(context.Context)
}

func (t *shutdownThrottleLifecycle) resume(context.Context) error {
	if t.external != nil {
		t.external.record("resume")
	}

	return t.resumeErr
}

func (t *shutdownThrottleLifecycle) stop(ctx context.Context) error {
	t.stopCtx = ctx
	t.stopCtxWasLive = ctx.Err() == nil
	t.stopCalledAt = time.Now()
	t.stopDeadline, t.stopHasDeadline = ctx.Deadline()
	if t.external != nil {
		t.external.record("throttle_stop")
	}
	if t.stopHook != nil {
		t.stopHook(ctx)
	}
	if t.waitForStopContext {
		<-ctx.Done()
		if t.stopAfterContext != nil {
			t.stopAfterContext(ctx)
		}

		return ctx.Err()
	}

	return nil
}

func (t *shutdownThrottleLifecycle) mock(testingT *testing.T) *servermocks.MockThrottleLifecycle {
	testingT.Helper()
	lifecycle := servermocks.NewMockThrottleLifecycle(testingT)
	lifecycle.EXPECT().Resume(mock.Anything).RunAndReturn(t.resume).Maybe()
	lifecycle.EXPECT().Stop(mock.Anything).RunAndReturn(t.stop).Maybe()

	return lifecycle
}

func newMockedShutdownRunner(
	t *testing.T,
	river *shutdownRiverLifecycle,
	throttle *shutdownThrottleLifecycle,
) *riverRunner {
	t.Helper()

	return newRiverRunner(river.mock(t), throttle.mock(t))
}
