package server

import (
	"context"
	"errors"
	"sync"
)

// riverLifecycle оставляет wrapper зависимым только от жизненного цикла River.
type riverLifecycle interface {
	Start(context.Context) error
	Stop(context.Context) error
	StopAndCancel(context.Context) error
	Stopped() <-chan struct{}
}

// riverRunner не ждёт Stopped, если River так и не удалось запустить.
type riverRunner struct {
	river riverLifecycle

	mu           sync.Mutex
	stopMu       sync.Mutex
	riverStarted bool
}

func newRiverRunner(river riverLifecycle) *riverRunner {
	return &riverRunner{river: river}
}

func (r *riverRunner) Start(ctx context.Context) error {
	if err := r.river.Start(ctx); err != nil {
		return err
	}

	r.mu.Lock()
	r.riverStarted = true
	r.mu.Unlock()

	return nil
}

func (r *riverRunner) Stop(ctx context.Context) error {
	r.stopMu.Lock()
	defer r.stopMu.Unlock()

	r.mu.Lock()
	started := r.riverStarted
	r.mu.Unlock()
	if !started {
		return nil
	}

	stopErr := r.river.Stop(ctx)
	var hardStopErr error
	if stopErr != nil {
		hardStopErr = r.river.StopAndCancel(context.Background())
	}
	<-r.river.Stopped()

	r.mu.Lock()
	r.riverStarted = false
	r.mu.Unlock()

	return errors.Join(stopErr, hardStopErr)
}

// Started отвечает, поднялась ли очередь: её контекст живёт всё время работы,
// и прерывание не должно отменять его у уже поднятой очереди.
func (r *riverRunner) Started() bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.riverStarted
}

func (r *riverRunner) Stopped() <-chan struct{} {
	return r.river.Stopped()
}
