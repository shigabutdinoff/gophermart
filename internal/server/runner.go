package server

import (
	"context"
	"errors"
	"fmt"
)

// riverLifecycle оставляет wrapper зависимым только от жизненного цикла River.
type riverLifecycle interface {
	Start(context.Context) error
	Stop(context.Context) error
	StopAndCancel(context.Context) error
	Stopped() <-chan struct{}
}

type throttleLifecycle interface {
	Resume(context.Context) error
	Stop(context.Context) error
}

type riverRunnerState uint8

const (
	riverRunnerIdle riverRunnerState = iota
	riverRunnerStarting
	riverRunnerStarted
	riverRunnerStopping
	riverRunnerStopped
)

type riverRunnerCommandKind uint8

const (
	riverRunnerStart riverRunnerCommandKind = iota
	riverRunnerCancelStart
	riverRunnerStop
	riverRunnerStartFinished
	riverRunnerStopFinished
)

type riverRunnerStartResult struct {
	err          error
	riverStarted bool
	stopped      <-chan struct{}
}

type riverRunnerCommand struct {
	kind        riverRunnerCommandKind
	ctx         context.Context
	reply       chan error
	startResult riverRunnerStartResult
	stopResult  error
}

type riverRunnerStopCompletion struct {
	ready chan struct{}
	err   error
}

func newRiverRunnerStopCompletion() *riverRunnerStopCompletion {
	return &riverRunnerStopCompletion{ready: make(chan struct{})}
}

func (c *riverRunnerStopCompletion) resolve(err error) {
	c.err = err
	close(c.ready)
}

func (c *riverRunnerStopCompletion) wait() error {
	<-c.ready

	return c.err
}

type riverRunnerActorState struct {
	phase           riverRunnerState
	cancelRequested bool
	startPending    bool
	startCtx        context.Context
	startCancel     context.CancelFunc
	startWaiters    []chan error
	riverConfirmed  bool
	riverStopped    <-chan struct{}
	watchStopped    <-chan struct{}
	stopCtx         context.Context
	stopWaiters     []chan error
}

// riverRunner не ждёт Stopped, если River так и не удалось запустить.
// Его изменяемым состоянием единолично владеет event loop.
type riverRunner struct {
	river    riverLifecycle
	throttle throttleLifecycle

	commands   chan riverRunnerCommand
	completion *riverRunnerStopCompletion
}

func newRiverRunner(
	river riverLifecycle,
	throttle throttleLifecycle,
) *riverRunner {
	runner := &riverRunner{
		river:      river,
		throttle:   throttle,
		commands:   make(chan riverRunnerCommand),
		completion: newRiverRunnerStopCompletion(),
	}
	go runner.run()

	return runner
}

func (r *riverRunner) Start(ctx context.Context) error {
	reply := make(chan error, 1)
	if !r.send(riverRunnerCommand{kind: riverRunnerStart, ctx: ctx, reply: reply}) {
		return context.Canceled
	}

	return <-reply
}

// CancelStart устанавливает latch до подтверждения вызова. Поднятую очередь
// этот сигнал не отменяет: её сворачивает Stop с общим shutdown context.
func (r *riverRunner) CancelStart() {
	reply := make(chan error, 1)
	if !r.send(riverRunnerCommand{kind: riverRunnerCancelStart, reply: reply}) {
		return
	}
	<-reply
}

func (r *riverRunner) Stop(ctx context.Context) error {
	reply := make(chan error, 1)
	if !r.send(riverRunnerCommand{kind: riverRunnerStop, ctx: ctx, reply: reply}) {
		return r.completion.wait()
	}

	return <-reply
}

func (r *riverRunner) Stopped() <-chan struct{} {
	return r.river.Stopped()
}

func (r *riverRunner) send(command riverRunnerCommand) bool {
	select {
	case r.commands <- command:
		return true
	case <-r.completion.ready:
		return false
	}
}

func (r *riverRunner) run() {
	state := riverRunnerActorState{phase: riverRunnerIdle}
	for {
		select {
		case command := <-r.commands:
			if r.handleCommand(&state, command) {
				return
			}
		case <-state.watchStopped:
			r.handleUnexpectedStop(&state)
		}
	}
}

func (r *riverRunner) handleCommand(
	state *riverRunnerActorState,
	command riverRunnerCommand,
) bool {
	switch command.kind {
	case riverRunnerStart:
		r.handleStart(state, command)
	case riverRunnerCancelStart:
		r.handleCancelStart(state, command)
	case riverRunnerStop:
		r.handleStop(state, command)
	case riverRunnerStartFinished:
		r.handleStartFinished(state, command.startResult)
	case riverRunnerStopFinished:
		return r.handleStopFinished(state, command.stopResult)
	}

	return false
}

func (r *riverRunner) handleStart(
	state *riverRunnerActorState,
	command riverRunnerCommand,
) {
	if state.phase == riverRunnerStarted && channelClosed(state.watchStopped) {
		r.handleUnexpectedStop(state)
	}
	if state.cancelRequested || state.phase == riverRunnerStopping ||
		state.phase == riverRunnerStopped {
		command.reply <- context.Canceled

		return
	}

	switch state.phase {
	case riverRunnerStarting:
		state.startWaiters = append(state.startWaiters, command.reply)
	case riverRunnerStarted:
		command.reply <- nil
	case riverRunnerIdle:
		if state.startCancel != nil {
			state.startCancel()
		}
		state.startCtx, state.startCancel = context.WithCancel(
			context.WithoutCancel(command.ctx),
		)
		state.phase = riverRunnerStarting
		state.startPending = true
		state.startWaiters = append(state.startWaiters, command.reply)
		state.riverConfirmed = false
		state.riverStopped = nil
		state.watchStopped = nil
		go r.runStart(state.startCtx)
	}
}

func (r *riverRunner) runStart(ctx context.Context) {
	result := riverRunnerStartResult{}
	if err := r.throttle.Resume(ctx); err != nil {
		result.err = err
	} else if err := context.Cause(ctx); err != nil {
		result.err = err
	} else {
		result.err = r.river.Start(ctx)
		result.riverStarted = result.err == nil
		if result.riverStarted {
			result.stopped = r.river.Stopped()
		}
	}

	r.send(riverRunnerCommand{
		kind:        riverRunnerStartFinished,
		startResult: result,
	})
}

func (r *riverRunner) handleStartFinished(
	state *riverRunnerActorState,
	result riverRunnerStartResult,
) {
	if !state.startPending {
		return
	}
	state.startPending = false

	cancelled := state.cancelRequested || state.phase == riverRunnerStopping
	if cancelled {
		var stopWaitErr error
		if state.stopCtx != nil {
			if cause := context.Cause(state.stopCtx); cause != nil {
				stopWaitErr = fmt.Errorf("wait job queue start: %w", cause)
			}
		}
		if result.riverStarted {
			state.riverConfirmed = true
			state.riverStopped = result.stopped
			state.watchStopped = nil
			state.phase = riverRunnerStopping
			cleanupCtx := state.stopCtx
			if cleanupCtx == nil {
				cleanupCtx = state.startCtx
				state.stopCtx = cleanupCtx
			}
			go r.runStop(cleanupCtx, true, result.stopped, stopWaitErr)
		} else {
			state.riverConfirmed = false
			state.riverStopped = nil
			state.watchStopped = nil
			if state.stopCtx != nil {
				state.phase = riverRunnerStopping
				go r.runStop(state.stopCtx, false, nil, stopWaitErr)
			} else {
				state.phase = riverRunnerIdle
				if state.startCancel != nil {
					state.startCancel()
				}
				state.startCtx = nil
				state.startCancel = nil
			}
		}
		resolveRiverRunnerWaiters(state.startWaiters, context.Canceled)
		state.startWaiters = nil

		return
	}

	if result.err != nil {
		state.phase = riverRunnerIdle
		state.riverConfirmed = false
		if state.startCancel != nil {
			state.startCancel()
		}
		state.startCtx = nil
		state.startCancel = nil
		resolveRiverRunnerWaiters(state.startWaiters, result.err)
		state.startWaiters = nil

		return
	}

	state.phase = riverRunnerStarted
	state.riverConfirmed = true
	state.riverStopped = result.stopped
	state.watchStopped = state.riverStopped
	resolveRiverRunnerWaiters(state.startWaiters, nil)
	state.startWaiters = nil
}

func (r *riverRunner) handleCancelStart(
	state *riverRunnerActorState,
	command riverRunnerCommand,
) {
	state.cancelRequested = true
	if state.startPending && state.startCancel != nil {
		state.startCancel()
	}
	command.reply <- nil
}

func (r *riverRunner) handleStop(
	state *riverRunnerActorState,
	command riverRunnerCommand,
) {
	state.cancelRequested = true
	state.stopWaiters = append(state.stopWaiters, command.reply)

	if state.phase == riverRunnerStopping {
		return
	}
	state.stopCtx = command.ctx
	if state.phase != riverRunnerStarted && state.startCancel != nil {
		state.startCancel()
	}
	if state.phase == riverRunnerStarting {
		state.phase = riverRunnerStopping
		return
	}

	state.phase = riverRunnerStopping
	state.watchStopped = nil
	go r.runStop(state.stopCtx, state.riverConfirmed, state.riverStopped, nil)
}

func (r *riverRunner) runStop(
	ctx context.Context,
	riverStarted bool,
	stopped <-chan struct{},
	precedingErr error,
) {
	var stopErr error
	var hardStopErr error
	var waitErr error
	if riverStarted {
		stopErr = r.river.Stop(ctx)
		if stopErr != nil {
			hardStopErr = r.river.StopAndCancel(ctx)
		}
		waitErr = waitRiverStopped(ctx, stopped)
	}
	throttleErr := r.throttle.Stop(ctx)

	r.send(riverRunnerCommand{
		kind: riverRunnerStopFinished,
		stopResult: errors.Join(
			precedingErr,
			stopErr,
			hardStopErr,
			waitErr,
			throttleErr,
		),
	})
}

func (r *riverRunner) handleStopFinished(
	state *riverRunnerActorState,
	result error,
) bool {
	state.phase = riverRunnerStopped
	state.riverConfirmed = false
	state.riverStopped = nil
	state.watchStopped = nil
	if state.startCancel != nil {
		state.startCancel()
	}
	state.startCtx = nil
	state.startCancel = nil
	r.completion.resolve(result)
	resolveRiverRunnerWaiters(state.stopWaiters, result)
	state.stopWaiters = nil

	return true
}

func (r *riverRunner) handleUnexpectedStop(state *riverRunnerActorState) {
	if state.phase != riverRunnerStarted {
		state.watchStopped = nil

		return
	}
	state.phase = riverRunnerIdle
	state.riverConfirmed = false
	state.riverStopped = nil
	state.watchStopped = nil
	if state.startCancel != nil {
		state.startCancel()
	}
	state.startCtx = nil
	state.startCancel = nil
}

func resolveRiverRunnerWaiters(waiters []chan error, err error) {
	for _, waiter := range waiters {
		waiter <- err
	}
}

func channelClosed(ch <-chan struct{}) bool {
	if ch == nil {
		return false
	}
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

// waitRiverStopped ждёт очередь ограниченно: закрытие её канала не зависит от
// сроков остановки, а застрявшее задание иначе держало бы весь процесс.
func waitRiverStopped(ctx context.Context, stopped <-chan struct{}) error {
	select {
	case <-stopped:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("wait job queue stop: %w", ctx.Err())
	}
}
