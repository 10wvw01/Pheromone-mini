package example

import "unsafe"

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
}

type TaskInvocation struct {
	SessionID string
	Function  FunctionInfo
	InputMode InputMode
	Args      []TaskArgument
}

type EnqueueResult uint8

const (
	EnqueueAccepted EnqueueResult = iota
	EnqueueNoCapacity
	EnqueueError
)

// TriggerModule mirrors the two upstream business APIs.
type TriggerModule interface {
	TriggerInit(appJSONPath string) error
	EventTriggerNotify(
		event DataReadyEvent,
		sourceFuncName string,
		dataAddr unsafe.Pointer,
	) error
}

// TaskPool is the only downstream contract. It intentionally has no ctx parameter.
type TaskPool interface {
	TryEnqueue(task TaskInvocation) EnqueueResult
}
