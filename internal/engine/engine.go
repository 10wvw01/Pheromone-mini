// Package engine 管理应用配置的注册与任务调度。
package engine

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unsafe"

	"triggerEngine/internal/config"
	"triggerEngine/internal/contract"
	"triggerEngine/internal/resource"
	"triggerEngine/internal/timing"
)

// stage 记录 V0.6 §13.1 的注册和运行状态，另含 §14.6 所需的关闭状态。
type stage uint8

const (
	uninitialized stage = iota
	initializing
	ready
	running
	closed
)

// bucket 保存全局数据桶索引的一项，将名称关联到所属应用和规则，见 V0.6 §5.2、§7.1。
type bucket struct {
	app   *config.App
	rules []config.Rule
}

// functionID 表示 V0.6 §13.2 定义的共享库和函数组合标识。
type functionID struct {
	soName, funcName string
}

// Runtime 持有进程的应用索引与调度器。应用注册结束后索引只读，见 V0.6 §13.1–13.3。
type Runtime struct {
	mu      sync.Mutex
	stage   stage
	active  sync.WaitGroup
	apps    map[string]*config.App
	buckets map[string]bucket
	funcs   map[functionID]contract.ResourceSpec
	delay   config.Delay

	readConfig func(string) ([]byte, error)
	router     *router
}

// Dependencies 由集成方提供任务池等依赖，也支持附录 E §E.2 所列的测试替换。
type Dependencies struct {
	Pool       contract.TaskPool
	Probe      resource.Probe
	Clock      timing.Clock
	Logger     *slog.Logger
	DelayLimit int
	// ReadConfig 可由集成方提供，用于读取指定路径的配置。
	ReadConfig func(string) ([]byte, error)
}

// New 按 V0.6 §4.3 组装运行时，建立应用索引和进程级调度器。
func New(d Dependencies) *Runtime {
	// 为省略的依赖使用默认实现，保留集成方显式传入的设置。
	if d.Clock == nil {
		d.Clock = timing.System{}
	}
	if d.Probe == nil {
		d.Probe = resource.NewLinux(d.Clock, nil)
	}
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.DelayLimit <= 0 {
		d.DelayLimit = 4096
	}
	if d.ReadConfig == nil {
		d.ReadConfig = readConfig
	}
	return &Runtime{
		apps: make(map[string]*config.App), buckets: make(map[string]bucket),
		funcs: make(map[functionID]contract.ResourceSpec), readConfig: d.ReadConfig,
		router: newRouter(d),
	}
}

// readConfig 限量读取配置文件，多读一个字节以便识别文件是否超出上限。
func readConfig(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, config.MaxFileBytes+1))
}

// apiError 按公共接口约定构造带状态码和说明的错误。
func apiError(code contract.StatusCode, format string, args ...any) error {
	return &contract.APIError{StatusCode: code, Msg: fmt.Sprintf(format, args...)}
}

// Init 按 V0.6 §13.1 完成一次配置注册。只有解析和跨应用校验均成功时才发布。
func (r *Runtime) Init(path string) error {
	r.mu.Lock()
	// 按 §13.1 先登记初始化状态，防止并发请求访问尚未发布的配置。
	previous := r.stage
	if previous != uninitialized && previous != ready {
		r.mu.Unlock()
		return apiError(contract.ErrInvalidState, "initialization is unavailable while initializing, running or closed")
	}
	r.stage = initializing
	r.active.Add(1)
	r.mu.Unlock()
	defer r.active.Done()
	defer func() {
		r.mu.Lock()
		if r.stage == initializing {
			r.stage = previous
		}
		r.mu.Unlock()
	}()

	// 将文件读取和校验放在锁外，避免持锁执行文件操作。
	if strings.TrimSpace(path) == "" || strings.ContainsRune(path, 0) {
		return apiError(contract.ErrInvalidArgument, "app.json path is empty or invalid")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return apiError(contract.ErrInvalidArgument, "cannot normalize app.json path: %v", err)
	}
	body, err := r.readConfig(abs)
	if err != nil {
		return apiError(contract.ErrConfigRead, "read %q: %v", abs, err)
	}
	app, err := config.Parse(body)
	if err != nil {
		return apiError(contract.ErrInvalidConfig, "%q: %v", abs, err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	// 重新获取锁后确认运行时仍可注册配置。
	if r.stage == closed {
		return apiError(contract.ErrInvalidState, "runtime closed during initialization")
	}
	if err := r.publish(app); err != nil {
		return apiError(contract.ErrInvalidConfig, "%q: %v", abs, err)
	}
	r.stage = ready
	return nil
}

// publish 按 V0.6 §13.1–13.2 先检查跨应用约束，再更新全部索引。调用方须持有运行时的互斥锁。
func (r *Runtime) publish(app *config.App) error {
	if r.apps[app.Name] != nil {
		return fmt.Errorf("app_name: duplicate App")
	}
	if len(r.apps) != 0 && r.delay != app.Delay {
		return fmt.Errorf("scheduler.delay_queue: differs from the process configuration")
	}
	for name := range app.Buckets {
		if _, exists := r.buckets[name]; exists {
			return fmt.Errorf("buckets: name already registered in another App")
		}
	}
	for _, fn := range app.Functions {
		id := functionID{fn.SOName, fn.FuncName}
		if spec, exists := r.funcs[id]; exists && spec != fn.Resource {
			return fmt.Errorf("functions: conflicting resource for a shared SO/function pair")
		}
	}

	// 按 §13.1 在所有冲突检查通过后一次更新索引，失败的应用不会留下部分记录。
	r.apps[app.Name] = app
	r.delay = app.Delay
	for name, rules := range app.Buckets {
		r.buckets[name] = bucket{app: app, rules: rules}
	}
	for _, fn := range app.Functions {
		r.funcs[functionID{fn.SOName, fn.FuncName}] = fn.Resource
	}
	return nil
}

// beginNotify 按 V0.6 §13.1 核对状态、通知参数和数据桶归属，首条有效通知将运行时切换为运行状态。
func (r *Runtime) beginNotify(event contract.DataReadyEvent, source string, addr unsafe.Pointer, size uint64) (bucket, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stage != ready && r.stage != running {
		return bucket{}, apiError(contract.ErrNotInitialized, "runtime is not ready to receive events")
	}
	if event.Bucket == "" || event.Key == "" || event.SessionID == "" || (addr == nil && size != 0) {
		return bucket{}, apiError(contract.ErrInvalidArgument, "event requires bucket, key, session_id and an address for nonempty data")
	}
	b, ok := r.buckets[event.Bucket]
	if !ok {
		return bucket{}, apiError(contract.ErrInvalidArgument, "bucket is not registered")
	}
	if b.app == nil {
		return bucket{}, apiError(contract.ErrInternal, "bucket index has no owning App")
	}
	if _, ok := b.app.Functions[source]; source != "" && !ok {
		return bucket{}, apiError(contract.ErrInvalidArgument, "source function is not registered in the bucket's App")
	}
	// 仅在所有参数检查通过后进入运行状态。
	r.stage = running
	r.active.Add(1)
	return b, nil
}

// Notify 按 V0.6 §6.2、§7.5 和 §8.4 选择触发路径，为每个命中项构建独立任务。
func (r *Runtime) Notify(event contract.DataReadyEvent, source string, addr unsafe.Pointer, size uint64) error {
	b, err := r.beginNotify(event, source, addr, size)
	if err != nil {
		return err
	}
	defer r.active.Done()

	// 按 §8.2–8.4 复制各任务的字符串和参数描述，原始数据地址继续由 SDK 管理。
	dispatch := func(action config.Action, path, rule string) {
		fn := action.Function
		fn.SOName, fn.FuncName = strings.Clone(fn.SOName), strings.Clone(fn.FuncName)
		task := contract.TaskInvocation{
			SessionID: strings.Clone(event.SessionID), Function: fn, InputMode: action.Mode,
			Args: []contract.TaskArgument{{Key: strings.Clone(event.Key), DataAddr: addr, DataSize: size}},
		}
		log := r.router.log.With("app_name", b.app.Name, "bucket", strings.Clone(event.Bucket),
			"source_func_name", strings.Clone(source), "route_path", path, "trigger_name", rule,
			"so_name", fn.SOName, "func_name", fn.FuncName, "session_id", task.SessionID,
			"key", task.Args[0].Key, "input_mode", task.InputMode, "args_count", len(task.Args))
		r.router.submit(task, b.app.Domain, b.app.Delay, log)
	}
	// 按 §6.2 和 §7.5 优先处理直接依赖。该路径命中后，跳过数据桶触发规则。
	if actions := b.app.Direct[source]; len(actions) != 0 {
		for _, action := range actions {
			dispatch(action, "DIRECT", "")
		}
		return nil
	}
	for _, rule := range b.rules {
		if rule.Key == "" || rule.Key == event.Key {
			dispatch(rule.Action, "TRIGGER", rule.Name)
		}
	}
	return nil
}

// Close 按 V0.6 §14.6 停止接收通知、取消内部调度并等待活动请求结束。任务池及 SDK 由集成方关闭。
func (r *Runtime) Close() {
	r.mu.Lock()
	r.stage = closed
	r.mu.Unlock()
	r.router.close()
	r.active.Wait()
}
