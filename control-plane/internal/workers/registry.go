// Package workers manages the worker pool and the gRPC WorkerSession stream.
//
// V1 is single-worker: the registry holds at most one connected worker. The
// API is shaped for a pool so V2 can extend it without callers changing.
package workers

import (
	"errors"
	"sync"

	pb "github.com/vinylSummer/dota-cuck/gen/spectator/v1"
)

// ErrNoWorker is returned when a command is sent but no worker is connected.
var ErrNoWorker = errors.New("no worker connected")

// Worker is a connected worker's control handle. Commands written to send are
// delivered to the worker over its gRPC stream by the session goroutine.
type Worker struct {
	ID   string
	send chan *pb.Command
}

// Registry tracks connected workers. V1: at most one (current).
type Registry struct {
	mu      sync.Mutex
	current *Worker
}

func NewRegistry() *Registry { return &Registry{} }

func (r *Registry) register(w *Worker) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.current = w
}

// unregister clears w only if it is still the current worker, so a stale
// session ending after a newer one connected does not evict the new worker.
func (r *Registry) unregister(w *Worker) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.current == w {
		r.current = nil
	}
}

// Current returns the connected worker, or nil if none.
func (r *Registry) Current() *Worker {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.current
}

// Send delivers cmd to the current worker. Returns ErrNoWorker if none is
// connected.
func (r *Registry) Send(cmd *pb.Command) error {
	w := r.Current()
	if w == nil {
		return ErrNoWorker
	}
	w.send <- cmd
	return nil
}
