package server

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/mock"

	servermocks "github.com/shigabutdinoff/gophermart/internal/server/mocks"
)

type riverLifecycleHarness struct {
	mu sync.Mutex

	calls              []string
	startCtx           context.Context
	startCtxLiveAtStop bool

	startErr    error
	startHook   func()
	stoppedHook func()
	closeOnStop bool

	stopped       chan struct{}
	stoppedClosed bool
	external      *callRecorder
}

func newRiverLifecycleHarness() *riverLifecycleHarness {
	return &riverLifecycleHarness{stopped: make(chan struct{})}
}

func (f *riverLifecycleHarness) start(ctx context.Context) error {
	f.record("start")
	f.mu.Lock()
	if f.stoppedClosed {
		f.stopped = make(chan struct{})
		f.stoppedClosed = false
	}
	f.startCtx = ctx
	f.mu.Unlock()
	if f.startHook != nil {
		f.startHook()
	}

	return f.startErr
}

func (f *riverLifecycleHarness) stop(context.Context) error {
	f.record("stop")
	f.mu.Lock()
	f.startCtxLiveAtStop = f.startCtx != nil && f.startCtx.Err() == nil
	f.mu.Unlock()
	if f.closeOnStop {
		f.closeStopped()
	}

	return nil
}

func (f *riverLifecycleHarness) stopAndCancel(context.Context) error {
	f.record("stop_and_cancel")

	return nil
}

func (f *riverLifecycleHarness) stoppedChannel() <-chan struct{} {
	f.record("stopped")
	if f.stoppedHook != nil {
		f.stoppedHook()
	}
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.stopped
}

func (f *riverLifecycleHarness) mock(t *testing.T) *servermocks.MockRiverLifecycle {
	t.Helper()
	lifecycle := servermocks.NewMockRiverLifecycle(t)
	lifecycle.EXPECT().Start(mock.Anything).RunAndReturn(f.start).Maybe()
	lifecycle.EXPECT().Stop(mock.Anything).RunAndReturn(f.stop).Maybe()
	lifecycle.EXPECT().StopAndCancel(mock.Anything).RunAndReturn(f.stopAndCancel).Maybe()
	lifecycle.EXPECT().Stopped().RunAndReturn(f.stoppedChannel).Maybe()

	return lifecycle
}

func (f *riverLifecycleHarness) record(call string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
	if f.external != nil {
		f.external.record(call)
	}
}

func countCalls(calls []string, target string) int {
	count := 0
	for _, call := range calls {
		if call == target {
			count++
		}
	}

	return count
}

type throttleLifecycleHarness struct {
	mu sync.Mutex

	external  *callRecorder
	resumeErr error
	stopCtx   context.Context
}

func (t *throttleLifecycleHarness) resume(context.Context) error {
	t.mu.Lock()
	err := t.resumeErr
	t.mu.Unlock()
	if t.external != nil {
		t.external.record("resume")
	}

	return err
}

func (t *throttleLifecycleHarness) stop(ctx context.Context) error {
	t.mu.Lock()
	t.stopCtx = ctx
	t.mu.Unlock()
	if t.external != nil {
		t.external.record("throttle_stop")
	}

	return nil
}

func (t *throttleLifecycleHarness) mock(testingT *testing.T) *servermocks.MockThrottleLifecycle {
	testingT.Helper()
	lifecycle := servermocks.NewMockThrottleLifecycle(testingT)
	lifecycle.EXPECT().Resume(mock.Anything).RunAndReturn(t.resume).Maybe()
	lifecycle.EXPECT().Stop(mock.Anything).RunAndReturn(t.stop).Maybe()

	return lifecycle
}

// stopContext отдаёт context, которым runner снял ограничитель.
func (t *throttleLifecycleHarness) stopContext() context.Context {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.stopCtx
}

func newMockedRiverRunner(
	t *testing.T,
	riverState *riverLifecycleHarness, throttleState *throttleLifecycleHarness) *riverRunner {
	t.Helper()
	runner := newRiverRunner(riverState.mock(t), throttleState.mock(t))
	// цикл заводится прямо в конструкторе, и упавший require оставил бы его
	// в пузыре: вторая паника про дедлок скрыла бы настоящую ошибку
	t.Cleanup(func() { _ = runner.Stop(context.Background()) })

	return runner
}

func (f *riverLifecycleHarness) callLog() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]string(nil), f.calls...)
}

func (f *riverLifecycleHarness) closeStopped() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.stoppedClosed {
		return
	}
	close(f.stopped)
	f.stoppedClosed = true
}

type runnerContextKey struct{}
