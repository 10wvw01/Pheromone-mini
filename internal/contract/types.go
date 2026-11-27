// Package contract 定义调用方与任务池使用的数据结构和状态码。
package contract

import (
	"fmt"
	"unsafe"
)

// DataReadyEvent 只携带已提交对象的数据桶、键和会话标识，见 V0.6 §10.5.1。
type DataReadyEvent struct {
	Bucket    string
	Key       string
	SessionID string
}

// FunctionIdentity 标识产出数据的共享库和函数。零值表示外部 Actor。
type FunctionIdentity struct {
	SOName   string `json:"so_name"`
	FuncName string `json:"func_name"`
}

// InputMode 决定任务向函数提供值、键和值，还是仅提供键，见 V0.6 §10.5.3。
type InputMode uint8

const (
	InputValue InputMode = iota
	InputKeyValue
	InputKeyOnly
)

// GPUResourceSpec 描述可见设备的数量和单设备可用显存需求，见 V0.6 §14.7。
type GPUResourceSpec struct {
	Required    bool
	DeviceCount uint32
	MemoryMB    uint64
}

// ResourceSpec 保存函数的静态资源需求，CPU 按核数、内存和磁盘按 MiB、带宽按 Mbps 表示，见 V0.6 §10.5.4。
type ResourceSpec struct {
	CPUCores    float64
	MemoryMB    uint64
	GPU         GPUResourceSpec
	DiskMB      uint64
	NetworkMbps uint64
}

// FunctionInfo 将配置中的共享库、函数名称和资源需求随任务一并传给任务池，见 V0.6 §10.5.4。
type FunctionInfo struct {
	SOName   string
	FuncName string
	Resource ResourceSpec
}

// TaskArgument 保存输入键及由 SDK 管理的借用地址，见 V0.6 §10.5.2、§11.5。
type TaskArgument struct {
	Key      string
	DataAddr unsafe.Pointer
}

// TaskInvocation 描述一次独立的函数调用。V0.6 每个派生任务携带一项 Args，见 §8.2–8.4。
type TaskInvocation struct {
	SessionID string
	Function  FunctionInfo
	InputMode InputMode
	Args      []TaskArgument
}

// StatusCode 表示 V0.6 附录 D §D.8 定义的接口错误类别。
type StatusCode uint16

const (
	ErrNotInitialized  StatusCode = 1001
	ErrInvalidArgument StatusCode = 1002
	ErrConfigRead      StatusCode = 1003
	ErrInvalidConfig   StatusCode = 1004
	ErrInvalidState    StatusCode = 1005
	ErrInternal        StatusCode = 1500
)

// APIError 按 V0.6 §10.5.6 携带供程序判定的状态码及诊断文字。
type APIError struct {
	StatusCode StatusCode `json:"status_code"`
	Msg        string     `json:"msg"`
}

// Error 将 V0.6 §10.5.6 的结构化错误转换为含状态码的可读文本。
func (e *APIError) Error() string {
	return fmt.Sprintf("%d: %s", e.StatusCode, e.Msg)
}

// EnqueueResult 区分已接收、暂时无容量和任务池错误，见 V0.6 §10.5.7。
type EnqueueResult uint8

const (
	EnqueueAccepted EnqueueResult = iota
	EnqueueNoCapacity
	EnqueueError
)

// TaskPool 按 V0.6 §10.7 原子地检查容量并接收任务。接收成功后由任务池持有任务描述的副本，
// DataAddr 指向的数据仍由 SDK 管理。
type TaskPool interface {
	TryEnqueue(task TaskInvocation) EnqueueResult
}
