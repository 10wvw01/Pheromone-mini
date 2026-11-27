package main

import (
	"context"

	"triggerEngine/api"
	"triggerEngine/internal/config"
	"triggerEngine/internal/engine"
	"triggerEngine/internal/resource"
	"triggerEngine/internal/wiring"
	"triggerEngine/mockpool"
)

// demoProbe 给出固定资源快照，示例仍通过真实 Router 的资源准入。
type demoProbe struct{}

func (demoProbe) Read(context.Context, config.Domain, api.FunctionInfo) (resource.Snapshot, error) {
	return resource.Snapshot{CPUAvailable: 4, MemoryAvailable: 64 * config.MiB}, nil
}

// taskConsumer 供 E2E 验收驱动检查已入队的任务。
type taskConsumer interface {
	TryDequeue() (api.TaskInvocation, bool)
}

// run 是本地验收驱动，负责装配 Trigger 和模拟下游消费。
func run() error {
	pool := mockpool.New(4)
	scheduler := engine.New(engine.Dependencies{Pool: pool, Probe: demoProbe{}})
	defer scheduler.Close()
	if err := wiring.Install(scheduler); err != nil {
		return err
	}

	return runScenario(pool)
}
