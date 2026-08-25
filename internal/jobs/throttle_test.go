package jobs

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/riverqueue/river"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

type fakeThrottleClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeThrottleClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.now
}

func (c *fakeThrottleClock) Advance(delta time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(delta)
	c.mu.Unlock()
}

// fakeThrottleTimer ведёт срок паузы за тестом, а колбэк отдаёт своей
// горутиной, как это делает time.AfterFunc.
type fakeThrottleTimer struct {
	mu     sync.Mutex
	fire   func()
	active bool

	created chan time.Duration
	resets  chan time.Duration
}

func newFakeThrottleTimer() *fakeThrottleTimer {
	return &fakeThrottleTimer{
		created: make(chan time.Duration, 8),
		resets:  make(chan time.Duration, 8),
	}
}

func (t *fakeThrottleTimer) arm(delay time.Duration, fire func()) timerHandle {
	t.mu.Lock()
	t.fire = fire
	t.active = true
	t.mu.Unlock()
	t.created <- delay

	return t
}

func (t *fakeThrottleTimer) Stop() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	wasActive := t.active
	t.active = false

	return wasActive
}

func (t *fakeThrottleTimer) Reset(delay time.Duration) bool {
	t.mu.Lock()
	wasActive := t.active
	t.active = true
	t.mu.Unlock()
	t.resets <- delay

	return wasActive
}

func (t *fakeThrottleTimer) Fire() {
	t.mu.Lock()
	fire := t.fire
	t.active = false
	t.mu.Unlock()
	go fire()
}

func (t *fakeThrottleTimer) Callback() func() {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.fire
}

func (t *fakeThrottleTimer) IsActive() bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.active
}

type pausedQueue struct {
	mu sync.Mutex

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

func newPausedQueue() *pausedQueue {
	return &pausedQueue{
		pauseCalls:  make(chan struct{}, 8),
		resumeCalls: make(chan struct{}, 8),
	}
}

func (q *pausedQueue) QueuePause(ctx context.Context, name string, _ *river.QueuePauseOpts) error {
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

func (q *pausedQueue) QueueResume(ctx context.Context, name string, _ *river.QueuePauseOpts) error {
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

func newDeterministicThrottle(
	t *testing.T,
	queue QueueController,
) (*Throttle, *fakeThrottleClock, *fakeThrottleTimer) {
	t.Helper()
	throttle, clock, timer := newObservedThrottle(t, zap.NewNop())
	require.NoError(t, throttle.Attach(queue))

	return throttle, clock, timer
}

func newObservedThrottle(
	t *testing.T,
	logger *zap.Logger,
) (*Throttle, *fakeThrottleClock, *fakeThrottleTimer) {
	t.Helper()
	clock := &fakeThrottleClock{now: time.Date(2026, time.August, 25, 12, 0, 0, 0, time.UTC)}
	timer := newFakeThrottleTimer()
	throttle := newThrottle(logger, "orders", clock.Now, timer.arm, nil)

	return throttle, clock, timer
}

func newObservedThrottleWithLifecycle(
	t *testing.T,
	logger *zap.Logger,
) (*Throttle, *fakeThrottleClock, *fakeThrottleTimer, <-chan throttleLoopEvent) {
	t.Helper()
	clock := &fakeThrottleClock{now: time.Date(2026, time.August, 25, 12, 0, 0, 0, time.UTC)}
	timer := newFakeThrottleTimer()
	events := make(chan throttleLoopEvent, 4)
	throttle := newThrottle(logger, "orders", clock.Now, timer.arm, func(event throttleLoopEvent) {
		events <- event
	})

	return throttle, clock, timer, events
}

func TestThrottle_AutomaticallyResumesAtDeadline(t *testing.T) {
	queue := newPausedQueue()
	throttle, clock, timer := newDeterministicThrottle(t, queue)

	throttle.Pause(context.Background(), time.Minute)
	waitThrottleValue(t, queue.pauseCalls)
	assert.Equal(t, time.Minute, waitThrottleValue(t, timer.created))
	clock.Advance(time.Minute)
	timer.Fire()
	waitThrottleValue(t, queue.resumeCalls)

	paused, resumed := queue.counts()
	assert.Equal(t, 1, paused)
	assert.Equal(t, 1, resumed)
	require.NoError(t, throttle.Stop(context.Background()))
}

func TestThrottle_SerializesConcurrentQueueCalls(t *testing.T) {
	queue := newPausedQueue()
	pauseRelease := make(chan struct{})
	queue.setPauseBlock(pauseRelease)
	throttle, _, timer := newDeterministicThrottle(t, queue)
	pauseDone := make(chan struct{})
	resumeDone := make(chan error, 1)

	go func() {
		throttle.Pause(context.Background(), time.Minute)
		close(pauseDone)
	}()
	waitThrottleValue(t, queue.pauseCalls)
	go func() { resumeDone <- throttle.Resume(context.Background()) }()
	requireNoThrottleValue(t, queue.resumeCalls)
	requireNoThrottleValue(t, pauseDone)

	close(pauseRelease)
	waitThrottleValue(t, pauseDone)
	waitThrottleValue(t, timer.created)
	waitThrottleValue(t, queue.resumeCalls)
	require.NoError(t, waitThrottleValue(t, resumeDone))
	require.NoError(t, throttle.Stop(context.Background()))
}

func TestThrottle_TimerCallbackOnlyDeliversActorEvent(t *testing.T) {
	resumeRelease := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(resumeRelease) }) })
	queue := newPausedQueue()
	queue.setResumeBlock(resumeRelease)
	throttle, clock, timer := newDeterministicThrottle(t, queue)

	throttle.Pause(context.Background(), time.Minute)
	waitThrottleValue(t, queue.pauseCalls)
	waitThrottleValue(t, timer.created)
	clock.Advance(time.Minute)
	callbackDone := make(chan struct{})
	go func() {
		timer.mu.Lock()
		fire := timer.fire
		timer.mu.Unlock()
		fire()
		close(callbackDone)
	}()
	waitThrottleValue(t, queue.resumeCalls)
	waitThrottleValue(t, callbackDone)

	releaseOnce.Do(func() { close(resumeRelease) })
	require.NoError(t, throttle.Stop(context.Background()))
}

func TestThrottle_LazilyStartsAndTerminatesActorLoop(t *testing.T) {
	throttle, _, _, lifecycle := newObservedThrottleWithLifecycle(t, zap.NewNop())
	queue := newPausedQueue()
	requireNoThrottleValue(t, lifecycle)

	require.NoError(t, throttle.Attach(queue))
	require.Equal(t, throttleLoopStarted, waitThrottleValue(t, lifecycle))
	require.NoError(t, throttle.Stop(context.Background()))
	waitThrottleValue(t, queue.resumeCalls)
	require.Equal(t, throttleLoopTerminated, waitThrottleValue(t, lifecycle))

	throttle.Pause(context.Background(), time.Minute)
	require.NoError(t, throttle.Resume(context.Background()))
	require.NoError(t, throttle.Stop(context.Background()))
	require.ErrorContains(t, throttle.Attach(newPausedQueue()), "already stopped")
	requireNoThrottleValue(t, queue.pauseCalls)
	requireNoThrottleValue(t, queue.resumeCalls)
	requireNoThrottleValue(t, lifecycle)
}

func TestThrottleStopCompletionBroadcastsImmutableResult(t *testing.T) {
	stopErr := errors.New("stop")
	completion := newThrottleStopCompletion()
	const waiters = 16
	results := make(chan error, waiters)
	for range waiters {
		go func() { results <- completion.wait() }()
	}
	requireNoThrottleValue(t, results)

	completion.resolve(stopErr)
	for range waiters {
		require.ErrorIs(t, waitThrottleValue(t, results), stopErr)
	}
	require.ErrorIs(t, completion.wait(), stopErr)
}

func TestThrottle_StopCancelsBlockedAutomaticResume(t *testing.T) {
	block := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(block) }) })
	queue := newPausedQueue()
	queue.setResumeBlock(block)
	core, logs := observer.New(zap.ErrorLevel)
	throttle, clock, timer, lifecycle := newObservedThrottleWithLifecycle(t, zap.New(core))
	require.NoError(t, throttle.Attach(queue))
	require.Equal(t, throttleLoopStarted, waitThrottleValue(t, lifecycle))

	throttle.Pause(context.Background(), time.Minute)
	waitThrottleValue(t, queue.pauseCalls)
	waitThrottleValue(t, timer.created)
	clock.Advance(time.Minute)
	timer.Fire()
	waitThrottleValue(t, queue.resumeCalls)
	queue.setResumeBlock(nil)
	pauseDone := make(chan struct{})
	go func() {
		throttle.Pause(context.Background(), 2*time.Minute)
		close(pauseDone)
	}()
	requireNoThrottleValue(t, pauseDone)

	stopDone := make(chan error, 1)
	go func() { stopDone <- throttle.Stop(context.Background()) }()

	select {
	case err := <-stopDone:
		require.NoError(t, err)
	case <-time.After(time.Second):
		releaseOnce.Do(func() { close(block) })
		<-stopDone
		require.FailNow(t, "Stop did not cancel a blocked automatic resume")
	}
	waitThrottleValue(t, pauseDone)
	require.Equal(t, throttleLoopTerminated, waitThrottleValue(t, lifecycle))
	assert.Zero(t, logs.FilterMessage("Не удалось автоматически вернуть опрос расчёта к работе").Len())
}

func TestThrottle_ExtendedDeadlineIsNotResumedByStaleTick(t *testing.T) {
	queue := newPausedQueue()
	throttle, clock, timer := newDeterministicThrottle(t, queue)

	throttle.Pause(context.Background(), time.Minute)
	waitThrottleValue(t, queue.pauseCalls)
	assert.Equal(t, time.Minute, waitThrottleValue(t, timer.created))
	clock.Advance(10 * time.Second)
	throttle.Pause(context.Background(), 2*time.Minute)
	waitThrottleValue(t, queue.pauseCalls)
	assert.Equal(t, 2*time.Minute, waitThrottleValue(t, timer.resets))

	timer.Fire()
	assert.Equal(t, 2*time.Minute, waitThrottleValue(t, timer.resets), "срок ведёт тот же таймер")
	requireNoThrottleValue(t, queue.resumeCalls)

	clock.Advance(2 * time.Minute)
	timer.Fire()
	waitThrottleValue(t, queue.resumeCalls)
	require.NoError(t, throttle.Stop(context.Background()))
	requireNoThrottleValue(t, timer.created)
}

func TestThrottle_EarlierDeadlineDoesNotShortenPause(t *testing.T) {
	queue := newPausedQueue()
	throttle, _, timer := newDeterministicThrottle(t, queue)

	throttle.Pause(context.Background(), 2*time.Minute)
	waitThrottleValue(t, queue.pauseCalls)
	waitThrottleValue(t, timer.created)
	throttle.Pause(context.Background(), time.Minute)

	requireNoThrottleValue(t, queue.pauseCalls)
	requireNoThrottleValue(t, timer.resets)
	require.NoError(t, throttle.Stop(context.Background()))
}

func TestThrottle_RepeatsFailedPause(t *testing.T) {
	queueErr := errors.New("pause")
	queue := newPausedQueue()
	queue.setPauseError(queueErr)
	core, logs := observer.New(zap.ErrorLevel)
	throttle, clock, timer := newObservedThrottle(t, zap.New(core))
	require.NoError(t, throttle.Attach(queue))

	throttle.Pause(context.Background(), time.Minute)
	waitThrottleValue(t, queue.pauseCalls)
	// без повтора воркеры продолжали бы опрашивать расчёт весь срок отказа
	assert.Equal(t, retryDelay, waitThrottleValue(t, timer.created))
	assert.Equal(t, 1, logs.FilterMessage("Не удалось придержать опрос расчёта").Len())

	queue.setPauseError(nil)
	clock.Advance(retryDelay)
	timer.Fire()
	waitThrottleValue(t, queue.pauseCalls)
	assert.Equal(
		t,
		time.Minute-retryDelay,
		waitThrottleValue(t, timer.resets),
		"принятая пауза держит остаток названного срока",
	)

	clock.Advance(time.Minute)
	timer.Fire()
	waitThrottleValue(t, queue.resumeCalls)
	require.NoError(t, throttle.Stop(context.Background()))
}

func TestThrottle_IgnoresStaleTimerEventBeforeRearmedPauseRetry(t *testing.T) {
	queue := newPausedQueue()
	queue.setPauseError(errors.New("pause"))
	throttle, _, timer := newDeterministicThrottle(t, queue)

	throttle.Pause(context.Background(), time.Minute)
	waitThrottleValue(t, queue.pauseCalls)
	waitThrottleValue(t, timer.created)
	staleCallback := timer.Callback()

	throttle.Pause(context.Background(), 2*time.Minute)
	waitThrottleValue(t, queue.pauseCalls)
	waitThrottleValue(t, timer.resets)
	callbackDone := make(chan struct{})
	go func() {
		staleCallback()
		close(callbackDone)
	}()
	waitThrottleValue(t, callbackDone)
	assert.Equal(t, retryDelay, waitThrottleValue(t, timer.resets))
	requireNoThrottleValue(t, queue.pauseCalls)
	requireNoThrottleValue(t, queue.resumeCalls)

	queue.setPauseError(nil)
	require.NoError(t, throttle.Stop(context.Background()))
}

func TestThrottle_IgnoresDuplicateTimerEventDuringAutomaticResumeRetry(t *testing.T) {
	resumeRelease := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(resumeRelease) }) })
	queue := newPausedQueue()
	queue.setResumeError(errors.New("resume"))
	queue.setResumeBlock(resumeRelease)
	throttle, clock, timer := newDeterministicThrottle(t, queue)

	throttle.Pause(context.Background(), time.Minute)
	waitThrottleValue(t, queue.pauseCalls)
	waitThrottleValue(t, timer.created)
	duplicateCallback := timer.Callback()
	clock.Advance(time.Minute)
	timer.Fire()
	waitThrottleValue(t, queue.resumeCalls)
	duplicateDone := make(chan struct{})
	go func() {
		duplicateCallback()
		close(duplicateDone)
	}()
	waitThrottleValue(t, duplicateDone)

	releaseOnce.Do(func() { close(resumeRelease) })
	waitThrottleValue(t, timer.resets)
	assert.Equal(t, retryDelay, waitThrottleValue(t, timer.resets))
	requireNoThrottleValue(t, queue.resumeCalls)

	queue.setResumeError(nil)
	require.NoError(t, throttle.Stop(context.Background()))
}

// Несостоявшуюся паузу снимать нечем: срок вышел, а очередь её не принимала.
func TestThrottle_ForgetsDeadlineOfPauseQueueNeverAccepted(t *testing.T) {
	queue := newPausedQueue()
	queue.setPauseError(errors.New("pause"))
	throttle, clock, timer := newDeterministicThrottle(t, queue)

	throttle.Pause(context.Background(), time.Minute)
	waitThrottleValue(t, queue.pauseCalls)
	waitThrottleValue(t, timer.created)

	clock.Advance(time.Minute)
	timer.Fire()

	requireNoThrottleValue(t, queue.pauseCalls)
	requireNoThrottleValue(t, queue.resumeCalls)
	queue.setPauseError(nil)
	require.NoError(t, throttle.Stop(context.Background()))
}

func TestThrottle_RepeatsAutomaticResumeAfterFailure(t *testing.T) {
	queue := newPausedQueue()
	queue.setResumeError(errors.New("resume"))
	core, logs := observer.New(zap.ErrorLevel)
	throttle, clock, timer := newObservedThrottle(t, zap.New(core))
	require.NoError(t, throttle.Attach(queue))
	throttle.Pause(context.Background(), time.Minute)
	waitThrottleValue(t, queue.pauseCalls)
	waitThrottleValue(t, timer.created)

	clock.Advance(time.Minute)
	timer.Fire()
	waitThrottleValue(t, queue.resumeCalls)

	// приостановленная очередь не запустит воркер, и позвать Pause
	// со свежим сроком станет некому
	assert.Equal(t, retryDelay, waitThrottleValue(t, timer.resets))
	assert.Equal(t, 1, logs.Len())

	queue.setResumeError(nil)
	clock.Advance(retryDelay)
	timer.Fire()
	waitThrottleValue(t, queue.resumeCalls)
	requireNoThrottleValue(t, timer.resets)
	require.NoError(t, throttle.Stop(context.Background()))
}

func TestThrottle_ResumeReturnsFailureToCaller(t *testing.T) {
	resumeErr := errors.New("resume")
	queue := newPausedQueue()
	queue.setResumeError(resumeErr)
	throttle, _, _ := newDeterministicThrottle(t, queue)

	err := throttle.Resume(context.Background())

	require.ErrorIs(t, err, resumeErr)
	waitThrottleValue(t, queue.resumeCalls)
	queue.setResumeError(nil)
	require.NoError(t, throttle.Stop(context.Background()))
}

func TestThrottle_ResumeTreatsMissingNewQueueAsAlreadyResumed(t *testing.T) {
	queue := newPausedQueue()
	queue.resumeErr = river.ErrNotFound
	core, logs := observer.New(zap.ErrorLevel)
	throttle := NewThrottle(zap.New(core), "orders", time.Now)
	require.NoError(t, throttle.Attach(queue))

	require.NoError(t, throttle.Resume(context.Background()))
	assert.Zero(t, logs.Len())

	queue.resumeErr = nil
	require.NoError(t, throttle.Stop(context.Background()))
}

func TestThrottle_StopClearsPersistedPauseAndIgnoresFuturePause(t *testing.T) {
	queue := newPausedQueue()
	throttle, _, timer := newDeterministicThrottle(t, queue)
	throttle.Pause(context.Background(), time.Minute)
	waitThrottleValue(t, queue.pauseCalls)
	waitThrottleValue(t, timer.created)

	require.NoError(t, throttle.Stop(context.Background()))
	waitThrottleValue(t, queue.resumeCalls)
	assert.False(t, timer.IsActive(), "Stop left the timer active")
	requireNoThrottleValue(t, timer.created)
	throttle.Pause(context.Background(), 2*time.Minute)
	require.NoError(t, throttle.Resume(context.Background()))

	requireNoThrottleValue(t, queue.pauseCalls)
	requireNoThrottleValue(t, queue.resumeCalls)
	paused, resumed := queue.counts()
	assert.Equal(t, 1, paused)
	assert.Equal(t, 1, resumed)
}

func TestThrottle_StopTreatsMissingQueueAsClean(t *testing.T) {
	queue := newPausedQueue()
	queue.resumeErr = river.ErrNotFound
	core, logs := observer.New(zap.ErrorLevel)
	throttle := NewThrottle(zap.New(core), "orders", time.Now)
	require.NoError(t, throttle.Attach(queue))

	require.NoError(t, throttle.Stop(context.Background()))
	assert.Zero(t, logs.Len())
}

func TestThrottle_ConcurrentStopWaitsAndReturnsStoredResult(t *testing.T) {
	resumeErr := errors.New("resume")
	release := make(chan struct{})
	queue := newPausedQueue()
	queue.resumeErr = resumeErr
	queue.resumeBlock = release
	throttle, _, _ := newDeterministicThrottle(t, queue)
	first := make(chan error, 1)
	second := make(chan error, 1)

	go func() { first <- throttle.Stop(context.Background()) }()
	waitThrottleValue(t, queue.resumeCalls)
	go func() { second <- throttle.Stop(context.Background()) }()
	requireNoThrottleValue(t, second)
	close(release)

	require.ErrorIs(t, waitThrottleValue(t, first), resumeErr)
	require.ErrorIs(t, waitThrottleValue(t, second), resumeErr)
	require.ErrorIs(t, throttle.Stop(context.Background()), resumeErr)
	_, resumed := queue.counts()
	assert.Equal(t, 1, resumed)
}

func TestThrottle_UnattachedStopIsHarmlessAndDoesNotCreateTimer(t *testing.T) {
	created := make(chan struct{}, 1)
	throttle := newThrottle(
		zap.NewNop(),
		"orders",
		time.Now,
		func(time.Duration, func()) timerHandle {
			created <- struct{}{}

			return newFakeThrottleTimer()
		},
		nil,
	)

	throttle.Pause(context.Background(), time.Minute)
	require.NoError(t, throttle.Stop(context.Background()))
	require.NoError(t, throttle.Stop(context.Background()))
	requireNoThrottleValue(t, created)
}

func TestThrottle_AttachReportsUnusableWiring(t *testing.T) {
	throttle := NewThrottle(zap.NewNop(), "orders", time.Now)

	require.ErrorContains(t, throttle.Attach(nil), "nil queue controller")

	queue := newPausedQueue()
	require.NoError(t, throttle.Attach(queue))
	require.ErrorContains(t, throttle.Attach(newPausedQueue()), "already attached")

	require.NoError(t, throttle.Stop(context.Background()))
	require.ErrorContains(t, throttle.Attach(newPausedQueue()), "already stopped")
	require.ErrorContains(t, throttle.Attach(nil), "nil queue controller")
}
