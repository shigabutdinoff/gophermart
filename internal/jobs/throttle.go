package jobs

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/riverqueue/river"
	"go.uber.org/zap"
)

// retryDelay отделяет повторные попытки распорядиться паузой очереди.
const retryDelay = time.Second

const (
	throttleLoopDormant uint32 = iota
	throttleLoopActive
	throttleLoopStopped
)

// QueueController приостанавливает очередь опроса и возвращает её к работе.
type QueueController interface {
	QueuePause(ctx context.Context, name string, opts *river.QueuePauseOpts) error
	QueueResume(ctx context.Context, name string, opts *river.QueuePauseOpts) error
}

// Throttler отводит опрос расчёта в сторону на названный срок.
type Throttler interface {
	Pause(ctx context.Context, pause time.Duration)
}

type throttleCommandKind uint8

const (
	throttleAttach throttleCommandKind = iota
	throttlePause
	throttleResume
	throttleStop
	throttleTimerFired
	throttleAutomaticFinished
	throttleStopFinished
)

type throttleAutomaticKind uint8

const (
	throttleAutomaticPause throttleAutomaticKind = iota
	throttleAutomaticResume
)

type throttleStopCompletion struct {
	ready chan struct{}
	err   error
}

func newThrottleStopCompletion() *throttleStopCompletion {
	return &throttleStopCompletion{ready: make(chan struct{})}
}

func (c *throttleStopCompletion) resolve(err error) {
	c.err = err
	close(c.ready)
}

func (c *throttleStopCompletion) wait() error {
	// закрытие ready отдаёт готовый исход всем ждущим, нынешним и будущим
	<-c.ready

	return c.err
}

type throttleCommand struct {
	kind      throttleCommandKind
	ctx       context.Context
	queue     QueueController
	deadline  time.Time
	reply     chan error
	automatic throttleAutomaticKind
	err       error
	canceled  bool
}

type throttleActorState struct {
	queue          QueueController
	deadline       time.Time
	pauseConfirmed bool
	timer          *time.Timer
	nextTimerAt    time.Time
	timerArmed     bool

	automaticCancel context.CancelFunc
	automaticActive bool

	stopped     bool
	stopActive  bool
	stopContext context.Context
	stopWaiters []chan error
}

// Throttle придерживает всю очередь, пока расчёт отказывает по частоте.
// Изменяемым состоянием единолично владеет лениво запускаемый event loop.
type Throttle struct {
	logger *zap.Logger
	name   string

	commands   chan throttleCommand
	loopState  atomic.Uint32
	completion *throttleStopCompletion
}

func NewThrottle(logger *zap.Logger, name string) *Throttle {
	return &Throttle{
		logger:     logger,
		name:       name,
		commands:   make(chan throttleCommand),
		completion: newThrottleStopCompletion(),
	}
}

// Attach один раз связывает ограничитель с построенным River.
func (t *Throttle) Attach(queue QueueController) error {
	reply := make(chan error, 1)
	if !t.send(throttleCommand{kind: throttleAttach, queue: queue, reply: reply}) {
		if queue == nil {
			return errors.New("cannot attach nil queue controller")
		}

		return errors.New("throttle is already stopped")
	}

	return <-reply
}

// Pause продлевает общую паузу, не сокращая уже принятый срок.
func (t *Throttle) Pause(ctx context.Context, pause time.Duration) {
	if pause <= 0 {
		return
	}

	reply := make(chan error, 1)
	if !t.send(throttleCommand{
		kind:     throttlePause,
		ctx:      ctx,
		deadline: time.Now().Add(pause),
		reply:    reply,
	}) {
		return
	}
	<-reply
}

// Resume снимает сохранённую паузу; ошибку решает вызывающий lifecycle.
func (t *Throttle) Resume(ctx context.Context) error {
	reply := make(chan error, 1)
	if !t.send(throttleCommand{kind: throttleResume, ctx: ctx, reply: reply}) {
		return nil
	}

	return <-reply
}

// Stop завершает ограничитель и очищает persisted pause ровно один раз.
func (t *Throttle) Stop(ctx context.Context) error {
	reply := make(chan error, 1)
	if !t.send(throttleCommand{kind: throttleStop, ctx: ctx, reply: reply}) {
		return t.completion.wait()
	}

	return <-reply
}

func (t *Throttle) send(command throttleCommand) bool {
	for {
		switch t.loopState.Load() {
		case throttleLoopDormant:
			if t.loopState.CompareAndSwap(throttleLoopDormant, throttleLoopActive) {
				go t.loop()
			}
		case throttleLoopActive:
			select {
			case t.commands <- command:
				return true
			case <-t.completion.ready:
				return false
			}
		case throttleLoopStopped:
			return false
		}
	}
}

func (t *Throttle) emit(command throttleCommand) {
	if t.loopState.Load() != throttleLoopActive {
		return
	}

	select {
	case t.commands <- command:
	case <-t.completion.ready:
	}
}

func (t *Throttle) loop() {
	state := throttleActorState{}
	pending := make([]throttleCommand, 0)

	for {
		var command throttleCommand
		if !state.automaticActive && !state.stopActive && len(pending) > 0 {
			command = pending[0]
			pending = pending[1:]
		} else {
			command = <-t.commands
		}

		if state.automaticActive && command.kind != throttleAutomaticFinished {
			if command.kind == throttleStop {
				t.acceptStop(&state, command)
			} else if state.stopped {
				t.replyAfterStop(&state, command)
			} else {
				pending = append(pending, command)
			}

			continue
		}

		if state.stopActive && command.kind != throttleStopFinished {
			t.replyAfterStop(&state, command)

			continue
		}

		switch command.kind {
		case throttleAttach:
			t.attach(&state, command)
		case throttlePause:
			t.pause(&state, command)
		case throttleResume:
			t.resume(&state, command)
		case throttleStop:
			t.acceptStop(&state, command)
		case throttleTimerFired:
			t.onTimer(&state)
		case throttleAutomaticFinished:
			t.automaticFinished(&state, command)
		case throttleStopFinished:
			t.replyPendingAfterStop(&state, pending)
			for _, waiter := range state.stopWaiters {
				waiter <- command.err
			}
			t.completion.resolve(command.err)
			t.loopState.Store(throttleLoopStopped)

			return
		}
	}
}

func (t *Throttle) attach(state *throttleActorState, command throttleCommand) {
	var err error
	switch {
	case command.queue == nil:
		err = errors.New("cannot attach nil queue controller")
	case state.stopped:
		err = errors.New("throttle is already stopped")
	case state.queue != nil:
		err = errors.New("queue controller is already attached")
	default:
		state.queue = command.queue
	}
	command.reply <- err
}

func (t *Throttle) pause(state *throttleActorState, command throttleCommand) {
	if state.stopped || state.queue == nil ||
		(!command.deadline.After(state.deadline) && state.pauseConfirmed) {
		command.reply <- nil

		return
	}

	err := state.queue.QueuePause(command.ctx, t.name, nil)
	if command.deadline.After(state.deadline) {
		state.deadline = command.deadline
	}
	if err != nil {
		t.logPauseError(err)
		t.arm(state, time.Now().Add(retryDelay))
		command.reply <- nil

		return
	}

	state.pauseConfirmed = true
	t.arm(state, state.deadline)
	command.reply <- nil
}

func (t *Throttle) resume(state *throttleActorState, command throttleCommand) {
	if state.stopped || state.queue == nil {
		command.reply <- nil

		return
	}

	err := resumeQueue(command.ctx, state.queue, t.name)
	if err != nil {
		t.logger.Error(
			"Не удалось вернуть опрос расчёта к работе",
			zap.String("queue", t.name),
			zap.Error(err),
		)
	} else {
		t.clearPause(state)
	}
	command.reply <- err
}

func (t *Throttle) onTimer(state *throttleActorState) {
	if state.stopped || !state.timerArmed {
		return
	}

	now := time.Now()
	timerRemaining := state.nextTimerAt.Sub(now)
	if timerRemaining > 0 {
		state.timer.Reset(timerRemaining)

		return
	}
	state.nextTimerAt = time.Time{}
	state.timerArmed = false
	if state.deadline.IsZero() {
		return
	}

	remaining := state.deadline.Sub(now)
	if state.pauseConfirmed {
		if remaining > 0 {
			t.arm(state, state.deadline)

			return
		}
		t.startAutomatic(state, throttleAutomaticResume)

		return
	}
	if remaining <= 0 {
		t.clearPause(state)

		return
	}
	t.startAutomatic(state, throttleAutomaticPause)
}

func (t *Throttle) startAutomatic(state *throttleActorState, kind throttleAutomaticKind) {
	ctx, cancel := context.WithCancel(context.Background())
	state.automaticCancel = cancel
	state.automaticActive = true
	queue := state.queue

	go func() {
		var err error
		if kind == throttleAutomaticPause {
			err = queue.QueuePause(ctx, t.name, nil)
		} else {
			err = resumeQueue(ctx, queue, t.name)
		}
		t.emit(throttleCommand{
			kind:      throttleAutomaticFinished,
			automatic: kind,
			err:       err,
			canceled:  ctx.Err() != nil,
		})
	}()
}

func (t *Throttle) automaticFinished(state *throttleActorState, command throttleCommand) {
	state.automaticCancel = nil
	state.automaticActive = false

	if state.stopped {
		t.startStop(state)

		return
	}

	if command.automatic == throttleAutomaticPause {
		if command.err != nil {
			if !command.canceled {
				t.logPauseError(command.err)
			}
			t.arm(state, time.Now().Add(retryDelay))

			return
		}

		state.pauseConfirmed = true
		t.arm(state, state.deadline)

		return
	}

	if command.err != nil {
		if !command.canceled {
			t.logger.Error(
				"Не удалось автоматически вернуть опрос расчёта к работе",
				zap.String("queue", t.name),
				zap.Error(command.err),
			)
			t.arm(state, time.Now().Add(retryDelay))
		}

		return
	}

	t.clearPause(state)
}

func (t *Throttle) acceptStop(state *throttleActorState, command throttleCommand) {
	if state.stopped {
		state.stopWaiters = append(state.stopWaiters, command.reply)

		return
	}

	state.stopped = true
	state.stopContext = command.ctx
	state.stopWaiters = append(state.stopWaiters, command.reply)
	state.nextTimerAt = time.Time{}
	state.timerArmed = false
	if state.timer != nil {
		state.timer.Stop()
	}
	if state.automaticActive {
		state.automaticCancel()

		return
	}
	t.startStop(state)
}

func (t *Throttle) startStop(state *throttleActorState) {
	state.stopActive = true
	queue := state.queue
	ctx := state.stopContext

	go func() {
		var err error
		if queue != nil {
			err = resumeQueue(ctx, queue, t.name)
			if err != nil {
				t.logger.Error(
					"Не удалось очистить паузу опроса расчёта",
					zap.String("queue", t.name),
					zap.Error(err),
				)
			}
		}
		t.emit(throttleCommand{kind: throttleStopFinished, err: err})
	}()
}

func (t *Throttle) replyAfterStop(state *throttleActorState, command throttleCommand) {
	switch command.kind {
	case throttleAttach:
		if command.queue == nil {
			command.reply <- errors.New("cannot attach nil queue controller")
		} else {
			command.reply <- errors.New("throttle is already stopped")
		}
	case throttlePause, throttleResume:
		command.reply <- nil
	case throttleStop:
		state.stopWaiters = append(state.stopWaiters, command.reply)
	}
}

func (t *Throttle) replyPendingAfterStop(state *throttleActorState, pending []throttleCommand) {
	for _, command := range pending {
		t.replyAfterStop(state, command)
	}
}

func (t *Throttle) arm(state *throttleActorState, deadline time.Time) {
	delay := max(time.Duration(0), time.Until(deadline))
	state.nextTimerAt = deadline
	state.timerArmed = true
	if state.timer == nil {
		state.timer = time.AfterFunc(delay, func() {
			t.emit(throttleCommand{kind: throttleTimerFired})
		})

		return
	}

	state.timer.Stop()
	state.timer.Reset(delay)
}

func (t *Throttle) clearPause(state *throttleActorState) {
	state.deadline = time.Time{}
	state.pauseConfirmed = false
	state.nextTimerAt = time.Time{}
	state.timerArmed = false
	if state.timer != nil {
		state.timer.Stop()
	}
}

func (t *Throttle) logPauseError(err error) {
	t.logger.Error(
		"Не удалось придержать опрос расчёта",
		zap.String("queue", t.name),
		zap.Error(err),
	)
}

// River создаёт строку очереди только при первом Start. До этого отсутствие
// строки означает, что persisted pause у очереди заведомо нет.
func resumeQueue(ctx context.Context, queue QueueController, name string) error {
	err := queue.QueueResume(ctx, name, nil)
	if errors.Is(err, river.ErrNotFound) {
		return nil
	}

	return err
}
