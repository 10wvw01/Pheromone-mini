package config

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"triggerEngine/internal/contract"
)

const MiB = 1024 * 1024

// Delay 保存进程级延时策略。各应用的值必须一致，见 V0.6 §9.5 和 §14.1。
type Delay struct {
	Enabled       bool
	RetryInterval time.Duration
	MaxWait       time.Duration
}

// Domain 指定磁盘路径、网络接口及带宽上限，见 V0.6 附录 B §B.4。
type Domain struct {
	DiskPath         string `json:"disk_path"`
	NetworkInterface string `json:"network_interface"`
	NetworkLimitMbps uint64 `json:"network_limit_mbps"`
}

// Action 保存目标函数，供直接依赖和数据桶规则共用，见 V0.6 第 6–8 章。
type Action struct {
	// Function 保存应用内嵌的函数定义，包含执行标识和资源需求，见 V0.6 §5.3。
	Function contract.FunctionInfo
}

// Rule 描述一个数据桶上的触发条件及目标动作，见 V0.6 §7.2–7.4。
type Rule struct {
	Name string
	// 空键表示立即触发。按名称触发要求配置非空键。
	Key    string
	Action Action
}

type Function struct {
	contract.FunctionInfo
	Description string
	Version     string
}

// App 保存已校验的应用配置。发布后仅供查询，见 V0.6 §5.5 和 §13.1。
type App struct {
	Name        string
	Description string
	Version     string
	Delay       Delay
	Domain      Domain
	Functions   map[contract.FunctionIdentity]Function
	Buckets     map[string][]Rule
	Direct      map[contract.FunctionIdentity][]Action
}

// fileConfig 对应 V0.6 附录 B §B.4 的 app.json 顶层结构，保留文件字段的原始形式。
type fileConfig struct {
	Name         string       `json:"app_name"`
	Description  string       `json:"description"`
	Version      string       `json:"version"`
	Scheduler    scheduler    `json:"scheduler"`
	Functions    []function   `json:"functions"`
	Dependencies []dependency `json:"dependencies"`
	Triggers     []trigger    `json:"triggers"`
}

// scheduler 对应附录 B §B.4 中延时队列和资源采集的配置对象。
type scheduler struct {
	Delay  delayConfig `json:"delay_queue"`
	Domain Domain      `json:"resource_probe"`
}

// delayConfig 保存以毫秒表示的延时参数。Enabled 使用指针，以区分省略与显式关闭。
type delayConfig struct {
	Enabled         *bool  `json:"enabled"`
	RetryIntervalMS uint64 `json:"retry_interval_ms"`
	MaxWaitMS       uint64 `json:"max_wait_ms"`
}

// function 对应附录 B §B.4 的 functions[] 项，资源需求随各函数声明。
type function struct {
	SOName      string   `json:"so_name"`
	FuncName    string   `json:"func_name"`
	Description string   `json:"description"`
	Version     string   `json:"version"`
	Resource    resource `json:"resource"`
}

// resource 对应附录 B §B.4 的函数资源字段，容量沿用配置使用的 MiB。
type resource struct {
	CPUCores    float64 `json:"cpu_cores"`
	MemoryMB    uint64  `json:"memory_mb"`
	GPU         gpu     `json:"gpu"`
	DiskMB      uint64  `json:"disk_mb"`
	NetworkMbps uint64  `json:"network_mbps"`
}

// gpu 对应附录 B §B.4 的 GPU 配置，显存要求适用于每张有效设备。
type gpu struct {
	Required    bool   `json:"required"`
	DeviceCount uint32 `json:"device_count"`
	MemoryMB    uint64 `json:"memory_mb"`
}

// dependency 对应附录 B §B.4 的直接依赖配置，用一个来源声明一个或多个目标。
type dependency struct {
	Type    string        `json:"type"`
	Sources []functionRef `json:"src_functions"`
	Targets []functionRef `json:"tgt_functions"`
}

// trigger 对应附录 B §B.4 的数据桶规则配置，包含规则类型、目标及匹配条件。
type trigger struct {
	Bucket    string    `json:"bucket"`
	Name      string    `json:"trigger_name"`
	Type      string    `json:"primitive_type"`
	Primitive primitive `json:"primitive"`
}

// primitive 保存规则目标函数和名称条件。Key 使用指针，以区分省略与显式空字符串。
type primitive struct {
	Function functionRef `json:"function"`
	Key      *string     `json:"key_name"`
}

// Parse 按 V0.6 附录 B §B.4–B.5 校验配置，并构建 §5.5 要求的只读查询索引。
func Parse(data []byte) (*App, error) {
	var raw fileConfig
	if err := decode(data, &raw); err != nil {
		return nil, err
	}
	if blank(raw.Name) || blank(raw.Version) || len(raw.Functions) == 0 {
		return nil, fmt.Errorf("app_name, version, and a nonempty functions array are required")
	}
	if raw.Scheduler.Delay.Enabled == nil {
		return nil, fmt.Errorf("scheduler.delay_queue.enabled is required")
	}
	// 按附录 B §B.5 先校验毫秒数及上下界，再换算成 Go 时长。
	delay := raw.Scheduler.Delay
	if delay.RetryIntervalMS == 0 || delay.MaxWaitMS < delay.RetryIntervalMS ||
		delay.MaxWaitMS > uint64(math.MaxInt64/int64(time.Millisecond)) {
		return nil, fmt.Errorf("scheduler.delay_queue: require 1 <= retry_interval_ms <= max_wait_ms <= 9223372036854")
	}

	app := &App{
		Name: raw.Name, Description: raw.Description, Version: raw.Version,
		Domain:    raw.Scheduler.Domain,
		Delay:     Delay{*delay.Enabled, time.Duration(delay.RetryIntervalMS) * time.Millisecond, time.Duration(delay.MaxWaitMS) * time.Millisecond},
		Functions: make(map[contract.FunctionIdentity]Function, len(raw.Functions)),
		Buckets:   make(map[string][]Rule, len(raw.Triggers)),
		Direct:    make(map[contract.FunctionIdentity][]Action),
	}
	// 按 §5.4 先登记函数，再解析直接依赖和触发规则中的函数引用。
	if err := app.addFunctions(raw.Functions); err != nil {
		return nil, err
	}
	names := make(map[string][]contract.FunctionIdentity, len(app.Functions))
	for id := range app.Functions {
		names[id.FuncName] = append(names[id.FuncName], id)
	}
	if err := app.addDependencies(raw.Dependencies, names); err != nil {
		return nil, err
	}
	if err := app.addRules(raw.Triggers, names); err != nil {
		return nil, err
	}
	return app, nil
}

// addFunctions 按 V0.6 §5.3、§13.2 和附录 B 校验函数标识、资源数量及可选采集配置。
func (app *App) addFunctions(functions []function) error {
	var disk, network bool
	for i, fn := range functions {
		if !safeName(fn.SOName) || blank(fn.FuncName) {
			return fmt.Errorf("functions[%d]: unsafe so_name or empty func_name", i)
		}
		id := contract.FunctionIdentity{AppName: app.Name, SOName: fn.SOName, FuncName: fn.FuncName}
		if _, exists := app.Functions[id]; exists {
			return fmt.Errorf("functions[%d]: duplicate so_name and func_name pair", i)
		}
		r := fn.Resource
		if r.CPUCores <= 0 || math.IsNaN(r.CPUCores) || math.IsInf(r.CPUCores, 0) {
			return fmt.Errorf("functions[%d].resource.cpu_cores: must be finite and positive", i)
		}
		if r.MemoryMB == 0 || r.MemoryMB > math.MaxUint64/MiB ||
			r.GPU.MemoryMB > math.MaxUint64/MiB || r.DiskMB > math.MaxUint64/MiB {
			return fmt.Errorf("functions[%d].resource: invalid or overflowing MiB quantity", i)
		}
		app.Functions[id] = Function{
			Description: fn.Description, Version: fn.Version,
			FunctionInfo: contract.FunctionInfo{
				SOName: fn.SOName, FuncName: fn.FuncName,
				Resource: contract.ResourceSpec{
					CPUCores: r.CPUCores, MemoryMB: r.MemoryMB, DiskMB: r.DiskMB, NetworkMbps: r.NetworkMbps,
					GPU: contract.GPUResourceSpec(r.GPU),
				},
			},
		}
		disk = disk || r.DiskMB != 0
		network = network || r.NetworkMbps != 0
	}
	// 任一函数使用磁盘或网络时，应用必须提供对应的采集配置。
	domain := app.Domain
	if disk || domain.DiskPath != "" {
		if !filepath.IsAbs(domain.DiskPath) {
			return fmt.Errorf("scheduler.resource_probe.disk_path: require an absolute existing path")
		}
		if _, err := os.Stat(domain.DiskPath); err != nil {
			return fmt.Errorf("scheduler.resource_probe.disk_path: path is unavailable")
		}
	}
	if network || domain.NetworkInterface != "" || domain.NetworkLimitMbps != 0 {
		if !validInterfaceName(domain.NetworkInterface) || domain.NetworkLimitMbps == 0 {
			return fmt.Errorf("scheduler.resource_probe: require a valid network_interface and positive network_limit_mbps")
		}
	}
	return nil
}

// resolveFunction resolves explicit pairs and legacy names that identify a single function.
func (app *App) resolveFunction(ref functionRef, names map[string][]contract.FunctionIdentity) (contract.FunctionIdentity, error) {
	id := contract.FunctionIdentity{AppName: app.Name, SOName: ref.SOName, FuncName: ref.FuncName}
	if id.SOName == "" {
		matches := names[id.FuncName]
		switch len(matches) {
		case 0:
			return id, fmt.Errorf("unknown function %q", id.FuncName)
		case 1:
			return matches[0], nil
		default:
			return id, fmt.Errorf("ambiguous function %q: specify so_name and func_name", id.FuncName)
		}
	}
	if _, ok := app.Functions[id]; !ok {
		return id, fmt.Errorf("unknown function %q in %q", id.FuncName, id.SOName)
	}
	return id, nil
}

// addDependencies 按 V0.6 §6.1–6.3 校验并建立来源函数到多个目标动作的索引。
func (app *App) addDependencies(deps []dependency, names map[string][]contract.FunctionIdentity) error {
	seen := make(map[[2]contract.FunctionIdentity]bool)
	for i, dep := range deps {
		if dep.Type != "DIRECT" || len(dep.Sources) != 1 || len(dep.Targets) == 0 {
			return fmt.Errorf("dependencies[%d]: require DIRECT with one source, at least one target", i)
		}
		source, err := app.resolveFunction(dep.Sources[0], names)
		if err != nil {
			return fmt.Errorf("dependencies[%d].src_functions[0]: %w", i, err)
		}
		for j, ref := range dep.Targets {
			target, err := app.resolveFunction(ref, names)
			if err != nil {
				return fmt.Errorf("dependencies[%d].tgt_functions[%d]: %w", i, j, err)
			}
			pair := [2]contract.FunctionIdentity{source, target}
			if seen[pair] {
				return fmt.Errorf("dependencies[%d].tgt_functions[%d]: duplicate DIRECT pair", i, j)
			}
			seen[pair] = true
			app.Direct[source] = append(app.Direct[source], Action{Function: app.Functions[target].FunctionInfo})
		}
	}
	return nil
}

// addRules 按 V0.6 §7.2–7.4 校验规则，保留同一数据桶的各条规则供独立匹配。
func (app *App) addRules(rules []trigger, names map[string][]contract.FunctionIdentity) error {
	seen := make(map[string]bool)
	for i, t := range rules {
		if blank(t.Bucket) {
			return fmt.Errorf("triggers[%d].bucket: must be nonempty", i)
		}
		if blank(t.Name) || seen[t.Name] {
			return fmt.Errorf("triggers[%d].trigger_name: empty or duplicate name", i)
		}
		id, err := app.resolveFunction(t.Primitive.Function, names)
		if err != nil {
			return fmt.Errorf("triggers[%d].primitive.function: %w", i, err)
		}
		var key string
		// 立即触发不设名称条件，按名称触发保留配置中的原始键值。
		switch t.Type {
		case "IMMEDIATE":
			if t.Primitive.Key != nil {
				return fmt.Errorf("triggers[%d].primitive.key_name: forbidden for IMMEDIATE", i)
			}
		case "BY_NAME":
			if t.Primitive.Key == nil || *t.Primitive.Key == "" {
				return fmt.Errorf("triggers[%d].primitive.key_name: required for BY_NAME", i)
			}
			key = *t.Primitive.Key
		default:
			return fmt.Errorf("triggers[%d].primitive_type: require IMMEDIATE or BY_NAME", i)
		}
		seen[t.Name] = true
		app.Buckets[t.Bucket] = append(app.Buckets[t.Bucket], Rule{Name: t.Name, Key: key, Action: Action{Function: app.Functions[id].FunctionInfo}})
	}
	return nil
}

// blank 判断字符串是否为空或只含空白字符。
func blank(s string) bool { return strings.TrimSpace(s) == "" }

// safeName 检查名称是否适合用作单个路径组件，拒绝路径分隔符、目录引用和控制字符。
func safeName(s string) bool {
	return !blank(s) && s != "." && s != ".." &&
		!strings.ContainsAny(s, "/\\") && strings.IndexFunc(s, func(r rune) bool { return r < ' ' || r == 127 }) < 0
}

// validInterfaceName 按 Linux 网卡命名约束校验附录 B §B.4 的 network_interface。
func validInterfaceName(name string) bool {
	return len(name) < 16 && safeName(name) && !strings.ContainsAny(name, " :")
}
