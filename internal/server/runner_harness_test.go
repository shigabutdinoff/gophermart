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

type riverLifecycleHarness struct {
	mu sync.Mutex

	calls              []string
	startCtx           context.Context
	stopCtx            context.Context
	hardStopCtx        context.Context
	startCtxLiveAtStop bool
	stopDeadline       time.Time
	hardStopDeadline   time.Time

	startErr            error
	stopErr             error
	hardStopErr         error
	startHook           func()
	stopHook            func(context.Context)
	stoppedHook         func()
	waitForStartContext bool
	waitForStopContext  bool
	closeOnStop         bool

	startCalled      chan struct{}
	startContextDone chan struct{}
	stopCalled       chan struct{}
	stopped          chan struct{}
	stoppedClosed    bool
	startRelease     chan struct{}
	external         *callRecorder
}

func newRiverLifecycleHarness() *riverLifecycleHarness {
	return &riverLifecycleHarness{
		startCalled:  make(chan struct{}, 1),
		stopCalled:   make(chan struct{}, 1),
		stopped:      make(chan struct{}),
		startRelease: make(chan struct{}),
	}
}

func (f *riverLifecycleHarness) start(ctx context.Context) error {
	f.record("start")
	f.mu.Lock()
	if f.stoppedClosed {
		f.stopped = make(chan struct{})
		f.stoppedClosed = false
	}
	f.startCtx = ctx
	startContextDone := f.startContextDone
	f.mu.Unlock()
	if startContextDone != nil {
		go func() {
			<-ctx.Done()
			close(startContextDone)
		}()
	}
	notify(f.startCalled)
	if f.waitForStartContext {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-f.startRelease:
			return errors.New("blocked Start released by test cleanup")
		}
	}
	if f.startHook != nil {
		f.startHook()
	}

	return f.startErr
}

func (f *riverLifecycleHarness) stop(ctx context.Context) error {
	f.record("stop")
	f.mu.Lock()
	f.stopCtx = ctx
	f.startCtxLiveAtStop = f.startCtx != nil && f.startCtx.Err() == nil
	f.stopDeadline, _ = ctx.Deadline()
	f.mu.Unlock()
	notify(f.stopCalled)
	if f.waitForStopContext {
		<-ctx.Done()

		return ctx.Err()
	}
	if f.closeOnStop {
		f.closeStopped()
	}
	if f.stopHook != nil {
		f.stopHook(ctx)
	}

	return f.stopErr
}

func (f *riverLifecycleHarness) stopAndCancel(ctx context.Context) error {
	f.record("stop_and_cancel")
	f.mu.Lock()
	f.hardStopCtx = ctx
	f.hardStopDeadline, _ = ctx.Deadline()
	f.mu.Unlock()

	return f.hardStopErr
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

	external           *callRecorder
	resumeErr          error
	stopErr            error
	resumeCtx          context.Context
	stopCtx            context.Context
	stopCtxWasLive     bool
	stopDeadline       time.Time
	waitForStopContext bool
	resumeHook         func(context.Context)
	stopHook           func(context.Context)
	resumeCalled       chan struct{}
	stopCalled         chan struct{}
	resumeCalls        int
	stopCalls          int
}

type throttleSnapshotState struct {
	resumeCtx      context.Context
	stopCtx        context.Context
	stopCtxWasLive bool
	stopDeadline   time.Time
}

func (t *throttleLifecycleHarness) resume(ctx context.Context) error {
	t.mu.Lock()
	t.resumeCtx = ctx
	t.resumeCalls++
	err := t.resumeErr
	hook := t.resumeHook
	t.mu.Unlock()
	if t.external != nil {
		t.external.record("resume")
	}
	notify(t.resumeCalled)
	if hook != nil {
		hook(ctx)
	}

	return err
}

func (t *throttleLifecycleHarness) stop(ctx context.Context) error {
	t.mu.Lock()
	t.stopCtx = ctx
	t.stopCtxWasLive = ctx.Err() == nil
	t.stopDeadline, _ = ctx.Deadline()
	t.stopCalls++
	err := t.stopErr
	waitForContext := t.waitForStopContext
	t.mu.Unlock()
	if t.external != nil {
		t.external.record("throttle_stop")
	}
	notify(t.stopCalled)
	if t.stopHook != nil {
		t.stopHook(ctx)
	}
	if waitForContext {
		<-ctx.Done()

		return ctx.Err()
	}

	return err
}

func (t *throttleLifecycleHarness) mock(testingT *testing.T) *servermocks.MockThrottleLifecycle {
	testingT.Helper()
	lifecycle := servermocks.NewMockThrottleLifecycle(testingT)
	lifecycle.EXPECT().Resume(mock.Anything).RunAndReturn(t.resume).Maybe()
	lifecycle.EXPECT().Stop(mock.Anything).RunAndReturn(t.stop).Maybe()

	return lifecycle
}

func (t *throttleLifecycleHarness) setResumeError(err error) {
	t.mu.Lock()
	t.resumeErr = err
	t.mu.Unlock()
}

func (t *throttleLifecycleHarness) callCounts() (int, int) {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.resumeCalls, t.stopCalls
}

func (t *throttleLifecycleHarness) snapshot() throttleSnapshotState {
	t.mu.Lock()
	defer t.mu.Unlock()

	return throttleSnapshotState{
		resumeCtx:      t.resumeCtx,
		stopCtx:        t.stopCtx,
		stopCtxWasLive: t.stopCtxWasLive,
		stopDeadline:   t.stopDeadline,
	}
}

func newMockedRiverRunner(
	t *testing.T,
	riverState *riverLifecycleHarness, throttleState *throttleLifecycleHarness) *riverRunner {
	t.Helper()

	return newRiverRunner(riverState.mock(t), throttleState.mock(t))
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

func (f *riverLifecycleHarness) releaseStart() {
	close(f.startRelease)
}

type runnerContextKey struct{}
