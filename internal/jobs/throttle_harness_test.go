package jobs

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/riverqueue/river"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	jobsmocks "github.com/shigabutdinoff/gophermart/internal/jobs/mocks"
)

var throttleClockStart = time.Date(2026, time.August, 25, 12, 0, 0, 0, time.UTC)

func newThrottleTimerFactory(
	clock *clockwork.FakeClock,
	callbackReturned chan<- struct{},
) func(time.Duration, func()) timerHandle {
	return func(delay time.Duration, fire func()) timerHandle {
		return clock.AfterFunc(delay, func() {
			fire()
			if callbackReturned != nil {
				callbackReturned <- struct{}{}
			}
		})
	}
}

type pausedQueue struct {
	mu sync.Mutex

	controller *jobsmocks.MockQueueController

	pauses  int
	resumes int
	names   []string

	pauseErr  error
	resumeErr error

	pauseCalls  chan struct{}
	resumeCalls chan struct{}
	pauseBlock  <-chan struct{}
	resumeBlock <-chan struct{}
}

func newPausedQueue(t *testing.T, pauses, resumes int) *pausedQueue {
	t.Helper()
	queue := &pausedQueue{
		pauseCalls:  make(chan struct{}, 8),
		resumeCalls: make(chan struct{}, 8),
	}
	queue.controller = jobsmocks.NewMockQueueController(t)
	if pauses > 0 {
		queue.controller.EXPECT().QueuePause(
			mock.Anything,
			"orders",
			(*river.QueuePauseOpts)(nil),
		).RunAndReturn(queue.pause).Times(pauses)
	}
	if resumes > 0 {
		queue.controller.EXPECT().QueueResume(
			mock.Anything,
			"orders",
			(*river.QueuePauseOpts)(nil),
		).RunAndReturn(queue.resume).Times(resumes)
	}

	return queue
}

func (q *pausedQueue) pause(ctx context.Context, name string, _ *river.QueuePauseOpts) error {
	q.mu.Lock()
	q.pauses++
	q.names = append(q.names, name)
	err := q.pauseErr
	block := q.pauseBlock
	q.mu.Unlock()
	q.pauseCalls <- struct{}{}
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return err
}

func (q *pausedQueue) resume(ctx context.Context, name string, _ *river.QueuePauseOpts) error {
	q.mu.Lock()
	q.resumes++
	q.names = append(q.names, name)
	err := q.resumeErr
	block := q.resumeBlock
	q.mu.Unlock()
	q.resumeCalls <- struct{}{}
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return err
}

func (q *pausedQueue) counts() (int, int) {
	q.mu.Lock()
	defer q.mu.Unlock()

	return q.pauses, q.resumes
}

func (q *pausedQueue) setResumeError(err error) {
	q.mu.Lock()
	q.resumeErr = err
	q.mu.Unlock()
}

func (q *pausedQueue) setPauseError(err error) {
	q.mu.Lock()
	q.pauseErr = err
	q.mu.Unlock()
}

func (q *pausedQueue) setResumeBlock(block <-chan struct{}) {
	q.mu.Lock()
	q.resumeBlock = block
	q.mu.Unlock()
}

func (q *pausedQueue) setPauseBlock(block <-chan struct{}) {
	q.mu.Lock()
	q.pauseBlock = block
	q.mu.Unlock()
}

func waitThrottleValue[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(time.Second):
		require.FailNow(t, "throttle coordination timed out")
		var zero T

		return zero
	}
}

func requireNoThrottleValue[T any](t *testing.T, ch <-chan T) {
	t.Helper()
	select {
	case <-ch:
		require.FailNow(t, "unexpected throttle event")
	case <-time.After(20 * time.Millisecond):
	}
}

func waitForThrottleTimers(t *testing.T, clock *clockwork.FakeClock, count int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, clock.BlockUntilContext(ctx, count))
}

func newDeterministicThrottle(
	t *testing.T,
	queue *pausedQueue,
) (*Throttle, *clockwork.FakeClock) {
	t.Helper()
	throttle, clock := newObservedThrottle(t, zap.NewNop())
	require.NoError(t, throttle.Attach(queue.controller))

	return throttle, clock
}

func newObservedThrottle(
	t *testing.T,
	logger *zap.Logger,
) (*Throttle, *clockwork.FakeClock) {
	t.Helper()
	clock := clockwork.NewFakeClockAt(throttleClockStart)
	throttle := newThrottle(logger, "orders", clock.Now, newThrottleTimerFactory(clock, nil), nil)

	return throttle, clock
}

func newObservedThrottleWithLifecycle(
	t *testing.T,
	logger *zap.Logger,
) (*Throttle, *clockwork.FakeClock, <-chan throttleLoopEvent) {
	t.Helper()
	clock := clockwork.NewFakeClockAt(throttleClockStart)
	events := make(chan throttleLoopEvent, 4)
	throttle := newThrottle(logger, "orders", clock.Now, newThrottleTimerFactory(clock, nil), func(event throttleLoopEvent) {
		events <- event
	})

	return throttle, clock, events
}
