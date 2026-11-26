// Package resource 采集资源数据，并判断任务所需资源是否充足。
package resource

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/bits"
	"time"

	"triggerEngine/internal/config"
	"triggerEngine/internal/contract"
)

type Device struct {
	ID        string
	FreeBytes uint64
	Visible   bool
}

// GPU 由集成方提供，用于报告当前进程可见的设备及其可用显存。
type GPU interface {
	Devices(context.Context) ([]Device, error)
}

type networkWindow struct {
	bytes   uint64
	elapsed time.Duration
}

// fits 用整数比较请求速率与采样窗口的剩余带宽，避免浮点舍入影响边界判断。
func (w networkWindow) fits(limit, requested uint64) bool {
	if requested > limit {
		return false
	}
	// 字节增量乘以 8000 后除以经过的纳秒数，得到以 Mbps 表示的速率。
	rateHi, rateLo := bits.Mul64(w.bytes, 8000)
	headroomHi, headroomLo := bits.Mul64(limit-requested, uint64(w.elapsed))
	return rateHi < headroomHi || (rateHi == headroomHi && rateLo <= headroomLo)
}

type Snapshot struct {
	CPULimit, CPUUsed, CPUAvailable          float64
	MemoryLimit, MemoryUsed, MemoryAvailable uint64
	Devices                                  []Device
	DiskAvailable                            uint64
	NetworkAvailable                         float64
	SampleTime                               time.Time
	Source                                   string
	network                                  networkWindow
	networkLimit                             uint64
}

type Probe interface {
	Read(context.Context, config.Domain, contract.FunctionInfo) (Snapshot, error)
}

type failure struct {
	reason string
	err    error
}

// Error 返回资源采集错误的原始说明。
func (f *failure) Error() string { return f.err.Error() }

// Unwrap 返回原始错误，供标准库检查错误链。
func (f *failure) Unwrap() error { return f.err }

// ReadFailure 将错误归类为资源读取失败。
func ReadFailure(err error) error { return &failure{"read", err} }

// ParseFailure 将错误归类为采样数据格式无效。
func ParseFailure(err error) error { return &failure{"parse", err} }

// ComputeFailure 将错误归类为资源计算失败。
func ComputeFailure(err error) error { return &failure{"calculation", err} }

// Reason 返回资源错误类别，普通错误归为读取失败。
func Reason(err error) string {
	var f *failure
	if errors.As(err, &f) {
		return f.reason
	}
	return "read"
}

// finite 检查数值是否有限。
func finite(n float64) bool { return !math.IsNaN(n) && !math.IsInf(n, 0) }

// requiresGPU 判断任务是否声明了 GPU 需求。
func requiresGPU(g contract.GPUResourceSpec) bool {
	return g.Required || g.DeviceCount != 0 || g.MemoryMB != 0
}

// Evaluate 检查资源快照能否满足任务要求，返回首项不足的资源或采集、计算错误。
func Evaluate(r contract.ResourceSpec, s Snapshot) (string, error) {
	// 先核对数量的合法性，避免单位换算溢出或比较结果失真。
	if !finite(r.CPUCores) || r.CPUCores <= 0 ||
		!finite(s.CPUAvailable) || s.CPUAvailable < 0 || r.MemoryMB == 0 ||
		r.MemoryMB > math.MaxUint64/config.MiB ||
		r.GPU.MemoryMB > math.MaxUint64/config.MiB || r.DiskMB > math.MaxUint64/config.MiB {
		return "", ComputeFailure(fmt.Errorf("invalid CPU or MiB quantity"))
	}
	if r.NetworkMbps > 0 && (!finite(s.NetworkAvailable) || s.NetworkAvailable < 0) {
		return "", ComputeFailure(fmt.Errorf("invalid network bandwidth quantity"))
	}
	if s.CPUAvailable < r.CPUCores {
		return "cpu", nil
	}
	if s.MemoryAvailable < r.MemoryMB*config.MiB {
		return "memory", nil
	}
	if requiresGPU(r.GPU) {
		if s.Devices == nil {
			return "", ReadFailure(fmt.Errorf("GPU telemetry unavailable"))
		}
		// 仅统计进程可见的设备，同一设备的重复记录合并判断。
		qualifying := make(map[string]bool)
		for _, device := range s.Devices {
			if !device.Visible {
				continue
			}
			if device.ID == "" {
				return "", ParseFailure(fmt.Errorf("visible GPU has no stable ID"))
			}
			enough := device.FreeBytes >= r.GPU.MemoryMB*config.MiB
			if previous, exists := qualifying[device.ID]; exists {
				enough = previous && enough
			}
			qualifying[device.ID] = enough
		}
		var count uint64
		for _, enough := range qualifying {
			if enough {
				count++
			}
		}
		if count < uint64(max(1, r.GPU.DeviceCount)) {
			return "gpu", nil
		}
	}
	if r.DiskMB > 0 && s.DiskAvailable < r.DiskMB*config.MiB {
		return "disk", nil
	}
	if r.NetworkMbps > 0 {
		// 有原始网络采样时使用整数运算，兼容只提供带宽值的采集器。
		if s.network.elapsed > 0 {
			if !s.network.fits(s.networkLimit, r.NetworkMbps) {
				return "network", nil
			}
		} else if s.NetworkAvailable < float64(r.NetworkMbps) {
			return "network", nil
		}
	}
	return "", nil
}
