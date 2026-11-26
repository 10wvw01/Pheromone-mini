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

type Delay struct {
	Enabled       bool
	RetryInterval time.Duration
	MaxWait       time.Duration
}

type Domain struct {
	DiskPath         string `json:"disk_path"`
	NetworkInterface string `json:"network_interface"`
	NetworkLimitMbps uint64 `json:"network_limit_mbps"`
}

type Action struct {
	Function contract.FunctionInfo
	Mode     contract.InputMode
}

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

type App struct {
	Name        string
	Description string
	Version     string
	Delay       Delay
	Domain      Domain
	Functions   map[string]Function
	Buckets     map[string][]Rule
	Direct      map[string][]Action
}

type fileConfig struct {
	Name         string       `json:"app_name"`
	Description  string       `json:"description"`
	Version      string       `json:"version"`
	Scheduler    scheduler    `json:"scheduler"`
	Functions    []function   `json:"functions"`
	Buckets      []string     `json:"buckets"`
	Dependencies []dependency `json:"dependencies"`
	Triggers     []trigger    `json:"triggers"`
}

type scheduler struct {
	Delay  delayConfig `json:"delay_queue"`
	Domain Domain      `json:"resource_probe"`
}

type delayConfig struct {
	Enabled         *bool  `json:"enabled"`
	RetryIntervalMS uint64 `json:"retry_interval_ms"`
	MaxWaitMS       uint64 `json:"max_wait_ms"`
}

type function struct {
	SOName      string   `json:"so_name"`
	FuncName    string   `json:"func_name"`
	Description string   `json:"description"`
	Version     string   `json:"version"`
	Resource    resource `json:"resource"`
}

type resource struct {
	CPUCores    float64 `json:"cpu_cores"`
	MemoryMB    uint64  `json:"memory_mb"`
	GPU         gpu     `json:"gpu"`
	DiskMB      uint64  `json:"disk_mb"`
	NetworkMbps uint64  `json:"network_mbps"`
}

type gpu struct {
	Required    bool   `json:"required"`
	DeviceCount uint32 `json:"device_count"`
	MemoryMB    uint64 `json:"memory_mb"`
}

type dependency struct {
	Type    string             `json:"type"`
	Sources []string           `json:"src_functions"`
	Targets []string           `json:"tgt_functions"`
	Mode    contract.InputMode `json:"trigger_option"`
}

type trigger struct {
	Bucket    string             `json:"bucket"`
	Name      string             `json:"trigger_name"`
	Type      string             `json:"primitive_type"`
	Primitive primitive          `json:"primitive"`
	Mode      contract.InputMode `json:"trigger_option"`
}

type primitive struct {
	Function string  `json:"function"`
	Key      *string `json:"key_name"`
}

// Parse 校验配置文件并建立按函数、数据桶和直接依赖查询的索引。
func Parse(data []byte) (*App, error) {
	var raw fileConfig
	if err := decode(data, &raw); err != nil {
		return nil, err
	}
	if blank(raw.Name) || blank(raw.Version) || len(raw.Functions) == 0 || raw.Buckets == nil {
		return nil, fmt.Errorf("app_name, version, a nonempty functions array and a buckets array are required")
	}
	if raw.Scheduler.Delay.Enabled == nil {
		return nil, fmt.Errorf("scheduler.delay_queue.enabled is required")
	}
	// 转换为 Go 时长前先检查范围，避免毫秒换算溢出。
	delay := raw.Scheduler.Delay
	if delay.RetryIntervalMS == 0 || delay.MaxWaitMS < delay.RetryIntervalMS ||
		delay.MaxWaitMS > uint64(math.MaxInt64/int64(time.Millisecond)) {
		return nil, fmt.Errorf("scheduler.delay_queue: require 1 <= retry_interval_ms <= max_wait_ms <= 9223372036854")
	}

	app := &App{
		Name: raw.Name, Description: raw.Description, Version: raw.Version,
		Domain:    raw.Scheduler.Domain,
		Delay:     Delay{*delay.Enabled, time.Duration(delay.RetryIntervalMS) * time.Millisecond, time.Duration(delay.MaxWaitMS) * time.Millisecond},
		Functions: make(map[string]Function, len(raw.Functions)),
		Buckets:   make(map[string][]Rule, len(raw.Buckets)),
		Direct:    make(map[string][]Action),
	}
	// 先建立函数和数据桶索引，再校验规则对它们的引用。
	if err := app.addFunctions(raw.Functions); err != nil {
		return nil, err
	}
	for i, name := range raw.Buckets {
		if _, exists := app.Buckets[name]; exists || blank(name) {
			return nil, fmt.Errorf("buckets[%d]: empty or duplicate bucket", i)
		}
		app.Buckets[name] = nil
	}
	if err := app.addDependencies(raw.Dependencies); err != nil {
		return nil, err
	}
	if err := app.addRules(raw.Triggers); err != nil {
		return nil, err
	}
	return app, nil
}

// addFunctions 校验函数定义和资源要求，登记函数并核对资源采集配置。
func (app *App) addFunctions(functions []function) error {
	var disk, network bool
	for i, fn := range functions {
		if !safeName(fn.SOName) || blank(fn.FuncName) {
			return fmt.Errorf("functions[%d]: unsafe so_name or empty func_name", i)
		}
		if _, exists := app.Functions[fn.FuncName]; exists {
			return fmt.Errorf("functions[%d].func_name: duplicate function", i)
		}
		r := fn.Resource
		if r.CPUCores <= 0 || math.IsNaN(r.CPUCores) || math.IsInf(r.CPUCores, 0) {
			return fmt.Errorf("functions[%d].resource.cpu_cores: must be finite and positive", i)
		}
		if r.MemoryMB == 0 || r.MemoryMB > math.MaxUint64/MiB ||
			r.GPU.MemoryMB > math.MaxUint64/MiB || r.DiskMB > math.MaxUint64/MiB {
			return fmt.Errorf("functions[%d].resource: invalid or overflowing MiB quantity", i)
		}
		app.Functions[fn.FuncName] = Function{
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
		if !safeName(domain.NetworkInterface) || len(domain.NetworkInterface) >= 16 || domain.NetworkLimitMbps == 0 {
			return fmt.Errorf("scheduler.resource_probe: require a valid network_interface and positive network_limit_mbps")
		}
	}
	return nil
}

// addDependencies 校验直接依赖并按来源函数登记目标动作。
func (app *App) addDependencies(deps []dependency) error {
	seen := make(map[[2]string]bool)
	for i, dep := range deps {
		if dep.Type != "DIRECT" || len(dep.Sources) != 1 || len(dep.Targets) == 0 || dep.Mode > contract.InputKeyOnly {
			return fmt.Errorf("dependencies[%d]: require DIRECT with one source, at least one target and trigger_option in 0..2", i)
		}
		source := dep.Sources[0]
		if _, ok := app.Functions[source]; !ok {
			return fmt.Errorf("dependencies[%d].src_functions: unknown source", i)
		}
		for j, target := range dep.Targets {
			fn, ok := app.Functions[target]
			// 同一来源和目标的组合只能声明一次。
			pair := [2]string{source, target}
			if !ok || seen[pair] {
				return fmt.Errorf("dependencies[%d].tgt_functions[%d]: unknown or duplicate DIRECT pair", i, j)
			}
			seen[pair] = true
			app.Direct[source] = append(app.Direct[source], Action{Function: fn.FunctionInfo, Mode: dep.Mode})
		}
	}
	return nil
}

// addRules 校验触发规则并按数据桶登记规则。
func (app *App) addRules(rules []trigger) error {
	seen := make(map[string]bool)
	for i, t := range rules {
		if _, ok := app.Buckets[t.Bucket]; !ok {
			return fmt.Errorf("triggers[%d].bucket: unknown bucket", i)
		}
		if blank(t.Name) || seen[t.Name] {
			return fmt.Errorf("triggers[%d].trigger_name: empty or duplicate name", i)
		}
		fn, ok := app.Functions[t.Primitive.Function]
		if !ok || t.Mode > contract.InputKeyOnly {
			return fmt.Errorf("triggers[%d]: unknown function or trigger_option outside 0..2", i)
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
		app.Buckets[t.Bucket] = append(app.Buckets[t.Bucket], Rule{Name: t.Name, Key: key, Action: Action{Function: fn.FunctionInfo, Mode: t.Mode}})
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
