//go:build examplefixture

package main

import (
	"context"

	"triggerEngine/api"
	"triggerEngine/internal/config"
	"triggerEngine/internal/engine"
	"triggerEngine/internal/resource"
)

func newExampleRuntime(pool api.TaskPool) *engine.Runtime {
	return engine.New(engine.Dependencies{Pool: pool, Probe: fixedSnapshot{}})
}

// fixedSnapshot replaces sampling for CI. The Router still decides admission.
type fixedSnapshot struct{}

func (fixedSnapshot) Read(context.Context, config.Domain, api.FunctionInfo) (resource.Snapshot, error) {
	return resource.Snapshot{CPUAvailable: 4, MemoryAvailable: 64 * config.MiB}, nil
}
