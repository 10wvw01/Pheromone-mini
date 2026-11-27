package engine

import (
	"triggerEngine/internal/contract"
	"triggerEngine/mockpool"
)

// downstreamPool forwards to the process-wide downstream entry point.
// When the real Pool is available, switch the downstream binding here.
type downstreamPool struct{}

func (downstreamPool) TryEnqueue(task contract.TaskInvocation) contract.EnqueueResult {
	return mockpool.TryEnqueue(task)
}
