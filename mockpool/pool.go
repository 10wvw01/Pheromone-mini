// Package mockpool provides the temporary process-wide downstream queue.
package mockpool

import "triggerEngine/internal/contract"

const capacity = 4

// The queue is ready at package initialization and lives until process exit.
var queue = make(chan contract.TaskInvocation, capacity)

// TryEnqueue accepts a request by value and borrows its batch address.
func TryEnqueue(task contract.TaskInvocation) contract.EnqueueResult {
	select {
	case queue <- task:
		return contract.EnqueueAccepted
	default:
		return contract.EnqueueNoCapacity
	}
}

// TryDequeue is used by the demo consumer to retrieve an accepted request.
func TryDequeue() (contract.TaskInvocation, bool) {
	select {
	case task := <-queue:
		return task, true
	default:
		return contract.TaskInvocation{}, false
	}
}
