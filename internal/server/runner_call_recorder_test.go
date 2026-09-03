package server

import "sync"

type callRecorder struct {
	mu    sync.Mutex
	calls []string
}

func (r *callRecorder) record(call string) {
	r.mu.Lock()
	r.calls = append(r.calls, call)
	r.mu.Unlock()
}

func (r *callRecorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]string(nil), r.calls...)
}
