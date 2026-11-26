// Package api 提供应用初始化和数据就绪通知接口。
package api

import (
	"unsafe"

	"triggerEngine/internal/contract"
	"triggerEngine/internal/wiring"
)

type (
	DataReadyEvent  = contract.DataReadyEvent
	InputMode       = contract.InputMode
	GPUResourceSpec = contract.GPUResourceSpec
	ResourceSpec    = contract.ResourceSpec
	FunctionInfo    = contract.FunctionInfo
	TaskArgument    = contract.TaskArgument
	TaskInvocation  = contract.TaskInvocation
	StatusCode      = contract.StatusCode
	APIError        = contract.APIError
	EnqueueResult   = contract.EnqueueResult
	TaskPool        = contract.TaskPool
)

const (
	InputValue    = contract.InputValue
	InputKeyValue = contract.InputKeyValue
	InputKeyOnly  = contract.InputKeyOnly

	ErrNotInitialized  = contract.ErrNotInitialized
	ErrInvalidArgument = contract.ErrInvalidArgument
	ErrConfigRead      = contract.ErrConfigRead
	ErrInvalidConfig   = contract.ErrInvalidConfig
	ErrInvalidState    = contract.ErrInvalidState
	ErrInternal        = contract.ErrInternal

	EnqueueAccepted   = contract.EnqueueAccepted
	EnqueueNoCapacity = contract.EnqueueNoCapacity
	EnqueueError      = contract.EnqueueError
)

// TriggerInit 校验应用配置，并在开始处理事件前将其注册到运行时。
func TriggerInit(appJSONPath string) error {
	return wiring.Instance().Init(appJSONPath)
}

// EventTriggerNotify 处理一条数据就绪通知。数据地址由 SDK 管理，在事件路由和异步任务执行期间须保持有效。
func EventTriggerNotify(event DataReadyEvent, sourceFuncName string, dataAddr unsafe.Pointer, dataSize uint64) error {
	return wiring.Instance().Notify(event, sourceFuncName, dataAddr, dataSize)
}
