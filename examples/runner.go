package main

import (
	"triggerEngine/api"
	"triggerEngine/internal/wiring"
	"triggerEngine/mockpool"
)

// taskConsumer 供 E2E 验收驱动检查已入队的任务。
type taskConsumer interface {
	TryDequeue() (api.TaskInvocation, bool)
}

// run 是本地验收驱动，负责装配 Trigger 和模拟下游消费。
func run() error {
	pool := mockpool.New(4)
	scheduler := newExampleRuntime(pool)
	defer scheduler.Close()
	if err := wiring.Install(scheduler); err != nil {
		return err
	}

	return runScenario(pool)
}
