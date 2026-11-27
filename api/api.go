// Package api 提供应用初始化和数据就绪通知接口。
package api

import (
	"unsafe"

	"triggerEngine/internal/contract"
	"triggerEngine/internal/wiring"
)

type (
	// DataReadyEvent 是 V0.6 §10.5.1 定义的数据就绪通知。
	DataReadyEvent = contract.DataReadyEvent
	// InputMode 是 V0.6 §10.5.3 定义的任务输入形式。
	InputMode = contract.InputMode
	// GPUResourceSpec 描述 V0.6 §14.7 的 GPU 资源需求。
	GPUResourceSpec = contract.GPUResourceSpec
	// ResourceSpec 描述 V0.6 §10.5.4 的函数资源需求。
	ResourceSpec = contract.ResourceSpec
	// FunctionInfo 携带 V0.6 §10.5.4 定义的执行入口和资源配置。
	FunctionInfo = contract.FunctionInfo
	// TaskArgument 描述 V0.6 §10.5.2 定义的单个任务输入。
	TaskArgument = contract.TaskArgument
	// TaskInvocation 是 V0.6 §10.5.5 定义的任务描述。
	TaskInvocation = contract.TaskInvocation
	// StatusCode 是 V0.6 附录 D §D.8 定义的接口状态码。
	StatusCode = contract.StatusCode
	// APIError 是 V0.6 §10.5.6 定义的结构化接口错误。
	APIError = contract.APIError
	// EnqueueResult 表示 V0.6 §10.5.7 定义的任务池接收结果。
	EnqueueResult = contract.EnqueueResult
	// TaskPool 是 V0.6 §10.7 定义的下游入队接口。
	TaskPool = contract.TaskPool
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

// TriggerInit 按 V0.6 接口设计 §10.6.1 读取并注册一个应用。调用方须在发送业务通知前完成所有应用的初始化。
func TriggerInit(appJSONPath string) error {
	return wiring.Instance().Init(appJSONPath)
}

// EventTriggerNotify 处理 SDK 成功提交后的数据就绪通知。
// 函数产出时，SDK 从协程池提供的本次调用上下文透传 sourceFuncName。外部 Actor 产出时传空字符串。
// dataAddr 必须非空，SDK 须按 V0.6 §11.5 保持该地址有效，直至相关排队和执行结束。
func EventTriggerNotify(event DataReadyEvent, sourceFuncName string, dataAddr unsafe.Pointer) error {
	return wiring.Instance().Notify(event, sourceFuncName, dataAddr)
}
