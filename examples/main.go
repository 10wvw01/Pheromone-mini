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

const session = "session-001"

type batch struct {
	values []int
}

type batchKey struct {
	app, bucket, key string
}

type committedBatch struct {
	session string
	batch   *batch
}

// demoSDK 模拟 SDK 的提交和按 App 读取。示例串行消费队列，整个会话内保留所有批次。
type demoSDK map[batchKey]committedBatch

func (s demoSDK) commit(key batchKey, sessionID string, values []int, source api.FunctionIdentity) error {
	if _, exists := s[key]; exists {
		return fmt.Errorf("batch already committed: %+v", key)
	}
	// 先发布完整批次，再通知。复制切片以保证调用方后续修改不会影响已提交数据。
	data := &batch{values: slices.Clone(values)}
	s[key] = committedBatch{session: sessionID, batch: data}
	event := api.DataReadyEvent{
		AppName: key.app, Bucket: key.bucket, Key: key.key,
		SessionID: sessionID, DataAddr: unsafe.Pointer(data),
	}
	if err := api.EventTriggerNotify(event, source); err != nil {
		return fmt.Errorf("notify %+v: %w", key, err)
	}
	return nil
}

func (s demoSDK) read(key batchKey, sessionID string) ([]int, error) {
	record, ok := s[key]
	if !ok || record.session != sessionID {
		return nil, fmt.Errorf("batch not found for session %s: %+v", sessionID, key)
	}
	// 跨 App 读取发生在业务函数与 SDK 之间。返回副本以保护原批次。
	return slices.Clone(record.batch.values), nil
}

// demoProbe 给出固定资源快照，示例仍通过真实 Router 的资源准入。
type demoProbe struct{}

func (demoProbe) Read(context.Context, config.Domain, api.FunctionInfo) (resource.Snapshot, error) {
	return resource.Snapshot{CPUAvailable: 4, MemoryAvailable: 64 * config.MiB}, nil
}

type step struct {
	function string
	output   batchKey
	execute  func(api.TaskInvocation, []int) ([]int, error)
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

	// 一份配置注册一个 App；全部成功后才能发送业务通知。
	for _, path := range []string{"examples/app.json", "examples/app-score.json", "examples/app-report.json"} {
		if err := api.TriggerInit(path); err != nil {
			return fmt.Errorf("initialize %s: %w", path, err)
		}
		fmt.Println("init", path)
	}

	sdk := make(demoSDK)
	actor := api.FunctionIdentity{}
	// Key 区分大小写；此事件不应入队。后续 DIRECT 命中也不能触发 fallback。
	if err := sdk.commit(batchKey{"ingest", "normalized", "Ready"}, session, []int{999}, actor); err != nil {
		return err
	}
	if unexpected, ok := pool.TryDequeue(); ok {
		return fmt.Errorf("unexpected task for nonmatching key: %+v", unexpected)
	}

	ingestResult := batchKey{"ingest", "result", "ready"}
	scoreResult := batchKey{"score", "result", "ready"}
	reportResult := batchKey{"report", "result", "ready"}

	// App 1：IMMEDIATE → BY_NAME → DIRECT → BY_NAME，产生供其他 App 读取的数据。
	if err := runChain(pool, sdk, batchKey{"ingest", "input", "ready"}, []int{1, 2, 3}, []step{
		{"normalize", batchKey{"ingest", "normalized", "ready"}, mapValues(func(n int) int { return n * 2 })},
		{"validate", batchKey{"ingest", "validated", "ready"}, mapValues(func(n int) int { return n + 1 })},
		{"enrich", batchKey{"ingest", "enriched", "ready"}, mapValues(func(n int) int { return n * 10 })},
		{"publish", ingestResult, mapValues(func(n int) int { return n })},
	}, []int{30, 50, 70}); err != nil {
		return err
	}

	// App 2：外部 Actor 在 ingest 完成后提交本 App 的权重。
	// load 从 SDK 显式读取 ingest/result/ready，再和本地权重相乘。
	if err := runChain(pool, sdk, batchKey{"score", "input", "ready"}, []int{2, 3, 4}, []step{
		{"load", batchKey{"score", "loaded", "ready"}, combine(sdk, ingestResult, func(a, b int) int { return a * b })},
		{"weight", batchKey{"score", "weighted", "ready"}, mapValues(func(n int) int { return n + 5 })},
		{"publish", scoreResult, mapValues(func(n int) int { return n })},
	}, []int{65, 155, 285}); err != nil {
		return err
	}

	// App 3：Actor 提交本地基数。join 依次按 App 读取两个同名 result/ready，
	// 相加并提交到本 App，后续仍通过本 App 的 DIRECT 和 IMMEDIATE 继续执行。
	join := func(task api.TaskInvocation, input []int) ([]int, error) {
		total, err := combine(sdk, ingestResult, add)(task, input)
		if err != nil {
			return nil, err
		}
		return combine(sdk, scoreResult, add)(task, total)
	}
	if err := runChain(pool, sdk, batchKey{"report", "input", "ready"}, []int{1, 1, 1}, []step{
		{"join", batchKey{"report", "joined", "ready"}, join},
		{"summarize", batchKey{"report", "summary", "ready"}, mapValues(func(n int) int { return n })},
		{"publish", reportResult, mapValues(func(n int) int { return n })},
	}, []int{96, 206, 356}); err != nil {
		return err
	}

	fmt.Println("E2E passed")
	return nil
}

func runChain(pool *mockpool.Pool, sdk demoSDK, key batchKey, input []int, steps []step, want []int) error {
	// 跨 App 链的入口由 Actor 提交本地批次，来源函数标识为空。
	if err := sdk.commit(key, session, input, api.FunctionIdentity{}); err != nil {
		return err
	}
	for _, step := range steps {
		task, ok := pool.TryDequeue()
		expected := api.TaskInvocation{
			AppName: key.app, FuncName: step.function, SOName: step.function + ".so",
			SessionID: session, DataAddr: unsafe.Pointer(sdk[key].batch),
		}
		if !ok || task != expected {
			return fmt.Errorf("%s: want queued task %+v, got %+v (present=%t)", key.app, expected, task, ok)
		}
		// 执行侧从入队的 DataAddr 读取，而不是从当前步骤的变量获取输入。
		values := (*batch)(task.DataAddr).values
		fmt.Printf("%s/%s session=%s batch=%v\n", task.AppName, task.FuncName, task.SessionID, values)
		output, err := step.execute(task, values)
		if err != nil {
			return err
		}
		// 模拟 Pool 提供当前执行上下文，SDK 用它标识产出函数。
		source := api.FunctionIdentity{AppName: task.AppName, SOName: task.SOName, FuncName: task.FuncName}
		if err := sdk.commit(step.output, task.SessionID, output, source); err != nil {
			return err
		}
		key = step.output
	}
	if unexpected, ok := pool.TryDequeue(); ok {
		return fmt.Errorf("%s: unexpected extra task: %+v", key.app, unexpected)
	}
	got, err := sdk.read(key, session)
	if err != nil {
		return err
	}
	if !slices.Equal(got, want) {
		return fmt.Errorf("%s: result %v, want %v", key.app, got, want)
	}
	fmt.Printf("%s/%s/%s = %v\n", key.app, key.bucket, key.key, got)
	return nil
}

func mapValues(transform func(int) int) func(api.TaskInvocation, []int) ([]int, error) {
	return func(_ api.TaskInvocation, values []int) ([]int, error) {
		out := make([]int, len(values))
		for i, value := range values {
			out[i] = transform(value)
		}
		return out, nil
	}
}

func add(a, b int) int { return a + b }

func combine(sdk demoSDK, remote batchKey, operation func(int, int) int) func(api.TaskInvocation, []int) ([]int, error) {
	return func(task api.TaskInvocation, local []int) ([]int, error) {
		other, err := sdk.read(remote, task.SessionID)
		if err != nil {
			return nil, err
		}
		if len(local) != len(other) {
			return nil, fmt.Errorf("batch length mismatch: %d and %d", len(local), len(other))
		}
		fmt.Printf("  SDK read %s/%s/%s -> %s/%s\n", remote.app, remote.bucket, remote.key, task.AppName, task.FuncName)
		result := make([]int, len(local))
		for i, value := range local {
			result[i] = operation(value, other[i])
		}
		return result, nil
	}
}
