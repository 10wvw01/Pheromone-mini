//go:build !examplefixture

package main

import (
	"triggerEngine/api"
	"triggerEngine/internal/engine"
)

func newExampleRuntime(pool api.TaskPool) *engine.Runtime {
	return engine.New(engine.Dependencies{Pool: pool})
}
