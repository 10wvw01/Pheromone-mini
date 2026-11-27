package main

import (
	"context"
	"fmt"
	"log"
	"slices"
	"unsafe"

	"triggerEngine/api"
	"triggerEngine/examples/mockpool"
	"triggerEngine/internal/config"
	"triggerEngine/internal/engine"
	"triggerEngine/internal/resource"
	"triggerEngine/internal/wiring"
)

type batch struct {
	values []int
}

// demoProbe keeps the example reproducible while exercising the real Router's
// admission check. Production uses the Linux probe.
type demoProbe struct{}

func (demoProbe) Read(context.Context, config.Domain, api.FunctionInfo) (resource.Snapshot, error) {
	return resource.Snapshot{CPUAvailable: 4, MemoryAvailable: 64 * config.MiB}, nil
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	pool := mockpool.New(4)
	scheduler := engine.New(engine.Dependencies{Pool: pool, Probe: demoProbe{}})
	defer scheduler.Close()
	if err := wiring.Install(scheduler); err != nil {
		return err
	}

	for _, path := range []string{"examples/app.json", "examples/app-copy.json"} {
		if err := api.TriggerInit(path); err != nil {
			return fmt.Errorf("initialize %s: %w", path, err)
		}
		fmt.Println("init", path)
	}

	const session = "session-001"
	for _, lane := range []struct {
		app   string
		input []int
		want  []int
	}{
		{"batch_demo", []int{1, 2, 3}, []int{3, 5, 7}},
		{"batch_demo_copy", []int{10, 20, 30}, []int{21, 41, 61}},
	} {
		if err := runScenario(pool, lane.app, session, lane.input, lane.want); err != nil {
			return err
		}
	}
	fmt.Println("E2E passed")
	return nil
}

// notify models the Data SDK after a complete, immutable batch has been written.
// These Go-owned fixtures remain reachable through the queued unsafe.Pointer.
func notify(app, bucket, key, session string, data *batch, source api.FunctionIdentity) error {
	event := api.DataReadyEvent{
		AppName: app, Bucket: bucket, Key: key, SessionID: session, DataAddr: unsafe.Pointer(data),
	}
	if err := api.EventTriggerNotify(event, source); err != nil {
		return fmt.Errorf("notify %s/%s/%s: %w", app, bucket, key, err)
	}
	return nil
}

func runScenario(pool *mockpool.Pool, app, session string, input, want []int) error {
	actor := api.FunctionIdentity{}
	if err := notify(app, "prepared", "Ready", session, &batch{values: input}, actor); err != nil {
		return err
	}
	if task, ok := pool.TryDequeue(); ok {
		return fmt.Errorf("%s: unexpected task for nonmatching key: %+v", app, task)
	}

	data := &batch{values: input}
	if err := notify(app, "input", "job-001", session, data, actor); err != nil {
		return err
	}

	// The producer is triggered by IMMEDIATE, the transform by BY_NAME and
	// the final collector by DIRECT. Each task consumes the previous batch.
	for _, step := range []struct {
		library, function string
	}{
		{"prepare.so", "prepare"},
		{"transform.so", "transform"},
		{"collect.so", "collect"},
	} {
		task, ok := pool.TryDequeue()
		expected := api.TaskInvocation{
			AppName: app, SOName: step.library, FuncName: step.function,
			SessionID: session, DataAddr: unsafe.Pointer(data),
		}
		if !ok || task != expected {
			return fmt.Errorf("%s: want queued task %+v, got %+v (present=%t)", app, expected, task, ok)
		}
		current := (*batch)(task.DataAddr)
		fmt.Printf("%s/%s session=%s batch=%v\n", task.AppName, task.FuncName, task.SessionID, current.values)
		source := api.FunctionIdentity{AppName: task.AppName, SOName: task.SOName, FuncName: task.FuncName}

		switch task.FuncName {
		case "prepare":
			data = &batch{values: make([]int, len(current.values))}
			for i, value := range current.values {
				data.values[i] = value * 2
			}
			if err := notify(task.AppName, "prepared", "ready", task.SessionID, data, source); err != nil {
				return err
			}
		case "transform":
			data = &batch{values: make([]int, len(current.values))}
			for i, value := range current.values {
				data.values[i] = value + 1
			}
			if err := notify(task.AppName, "transformed", "done", task.SessionID, data, source); err != nil {
				return err
			}
		case "collect":
			if !slices.Equal(current.values, want) {
				return fmt.Errorf("%s: final batch = %v, want %v", app, current.values, want)
			}
		}
	}
	if extra, ok := pool.TryDequeue(); ok {
		return fmt.Errorf("%s: unexpected extra task: %+v", app, extra)
	}
	return nil
}
