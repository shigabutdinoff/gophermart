package jobs

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/riverqueue/river"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	jobsmocks "github.com/shigabutdinoff/gophermart/internal/jobs/mocks"
	"github.com/shigabutdinoff/gophermart/internal/testsupport"
)

type pausedQueue struct {
	mu sync.Mutex

	controller *jobsmocks.MockQueueController

	pauses  int
	resumes int

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

func (q *pausedQueue) pause(ctx context.Context, _ string, _ *river.QueuePauseOpts) error {
	q.mu.Lock()
	q.pauses++
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

func (q *pausedQueue) resume(ctx context.Context, _ string, _ *river.QueuePauseOpts) error {
	q.mu.Lock()
	q.resumes++
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

const (
	throttleTimedOut      = "throttle coordination timed out"
	unexpectedThrottleRun = "unexpected throttle event"
)

func waitThrottleValue[T any](t *testing.T, ch <-chan T) T {
	t.Helper()

	return testsupport.WaitValue(t, ch, throttleTimedOut)
}

func requireNoThrottleValue[T any](t *testing.T, ch <-chan T) {
	t.Helper()
	testsupport.RequireNoValue(t, ch, unexpectedThrottleRun)
}

func waitThrottleValueAt[T any](t *testing.T, ch <-chan T, delay time.Duration) T {
	t.Helper()

	return testsupport.WaitValueAt(t, ch, delay, throttleTimedOut)
}

func newDeterministicThrottle(t *testing.T, logger *zap.Logger, queue *pausedQueue) *Throttle {
	t.Helper()
	throttle := NewThrottle(logger, "orders")
	require.NoError(t, throttle.Attach(queue.controller))
	// упавший require обрывает горутину теста, и без остановки поверх
	// настоящей ошибки прилетела бы вторая паника про дедлок
	t.Cleanup(func() { _ = throttle.Stop(context.Background()) })

	return throttle
}
