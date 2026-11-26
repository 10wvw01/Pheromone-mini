// Package contract 定义调用方与任务池使用的数据结构和状态码。
package contract

import (
	"fmt"
	"unsafe"
)

type DataReadyEvent struct {
	Bucket    string
	Key       string
	SessionID string
}

type InputMode uint8

const (
	InputValue InputMode = iota
	InputKeyValue
	InputKeyOnly
)

type GPUResourceSpec struct {
	Required    bool
	DeviceCount uint32
	MemoryMB    uint64
}

type ResourceSpec struct {
	CPUCores    float64
	MemoryMB    uint64
	GPU         GPUResourceSpec
	DiskMB      uint64
	NetworkMbps uint64
}

type FunctionInfo struct {
	SOName   string
	FuncName string
	Resource ResourceSpec
}

type TaskArgument struct {
	Key      string
	DataAddr unsafe.Pointer
	DataSize uint64
}

type TaskInvocation struct {
	SessionID string
	Function  FunctionInfo
	InputMode InputMode
	Args      []TaskArgument
}

type StatusCode uint16

const (
	ErrNotInitialized  StatusCode = 1001
	ErrInvalidArgument StatusCode = 1002
	ErrConfigRead      StatusCode = 1003
	ErrInvalidConfig   StatusCode = 1004
	ErrInvalidState    StatusCode = 1005
	ErrInternal        StatusCode = 1500
)

type APIError struct {
	StatusCode StatusCode `json:"status_code"`
	Msg        string     `json:"msg"`
}

// Error 将接口错误的状态码和说明格式化为可读文本。
func (e *APIError) Error() string {
	return fmt.Sprintf("%d: %s", e.StatusCode, e.Msg)
}

type EnqueueResult uint8

const (
	EnqueueAccepted EnqueueResult = iota
	EnqueueNoCapacity
	EnqueueError
)

// TaskPool 原子地检查容量并接收任务。接收成功后由任务池持有任务描述的副本，
// DataAddr 指向的数据仍由 SDK 管理。
type TaskPool interface {
	TryEnqueue(task TaskInvocation) EnqueueResult
}
