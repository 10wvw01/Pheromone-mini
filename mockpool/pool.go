// Package mockpool provides a simple in-process TaskPool for Trigger integration.
package mockpool

import "triggerEngine/api"

// Pool stores accepted requests by value and borrows their batch addresses.
type Pool struct {
	queue chan api.TaskInvocation
}

var _ api.TaskPool = (*Pool)(nil)

func New(capacity int) *Pool {
	return &Pool{queue: make(chan api.TaskInvocation, capacity)}
}

// TryEnqueue records the request if capacity is available, without waiting.
func (p *Pool) TryEnqueue(task api.TaskInvocation) api.EnqueueResult {
	select {
	case p.queue <- task:
		return api.EnqueueAccepted
	default:
		return api.EnqueueNoCapacity
	}
}

// TryDequeue retrieves an accepted task without blocking the caller.
func (p *Pool) TryDequeue() (api.TaskInvocation, bool) {
	select {
	case task := <-p.queue:
		return task, true
	default:
		return api.TaskInvocation{}, false
	}
}
