//go:build linux

package resource

import (
	"context"
	"fmt"
	"math"
	"math/bits"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"triggerEngine/internal/config"
	"triggerEngine/internal/contract"
	"triggerEngine/internal/timing"
)

type netCounters struct{ rx, tx uint64 }

type Linux struct {
	clock timing.Clock
	gpu   GPU
	read  func(string) ([]byte, error)
	disk  func(string) (uint64, uint64, error)

	base    sampleCache[Snapshot]
	mu      sync.Mutex
	network map[string]*sampleCache[networkWindow]
}

// NewLinux 使用 Linux 系统接口创建资源采集器。
func NewLinux(clock timing.Clock, gpu GPU) *Linux {
	return NewLinuxWithSystemReaders(clock, gpu, os.ReadFile, statDisk)
}

// NewLinuxWithSystemReaders 使用指定的文件读取和文件系统统计函数创建资源采集器。
// 未提供的读取函数使用 Linux 系统接口。
func NewLinuxWithSystemReaders(clock timing.Clock, gpu GPU, read func(string) ([]byte, error), disk func(string) (uint64, uint64, error)) *Linux {
	if read == nil {
		read = os.ReadFile
	}
	if disk == nil {
		disk = statDisk
	}
	return &Linux{clock: clock, gpu: gpu, read: read, disk: disk, network: make(map[string]*sampleCache[networkWindow])}
}

// Read 采集 CPU 和内存，并按任务声明采集 GPU、磁盘和网络数据。
func (l *Linux) Read(ctx context.Context, domain config.Domain, fn contract.FunctionInfo) (Snapshot, error) {
	readBase := func() (Snapshot, time.Time, error) { return l.sampleBase(ctx) }
	s, baseTime, err := l.base.get(ctx, l.clock, readBase)
	if err != nil {
		return Snapshot{}, err
	}
	r := fn.Resource
	// 可选资源按任务声明采集，并记录各自的采样时间。
	var devices []Device
	var gpuTime, diskTime, networkTime time.Time
	var diskAvailable uint64
	var network networkWindow
	if requiresGPU(r.GPU) {
		if l.gpu == nil {
			return s, ReadFailure(fmt.Errorf("GPU telemetry adapter unavailable"))
		}
		gpuTime = l.clock.Now()
		devices, err = l.gpu.Devices(ctx)
		if err != nil {
			return s, ReadFailure(fmt.Errorf("GPU telemetry: %w", err))
		}
		// 采集成功但未发现设备时，用空列表表示有效结果。
		if devices == nil {
			devices = []Device{}
		}
	}
	if r.DiskMB != 0 {
		diskTime = l.clock.Now()
		blocks, blockSize, err := l.disk(domain.DiskPath)
		if err != nil {
			return s, ReadFailure(fmt.Errorf("statfs: %w", err))
		}
		// 将可用块数换算为字节时检查乘积是否溢出。
		high, free := bits.Mul64(blocks, blockSize)
		if blockSize == 0 || high != 0 {
			return s, ComputeFailure(fmt.Errorf("invalid statfs block size or available byte overflow"))
		}
		diskAvailable = free
	}
	if r.NetworkMbps != 0 {
		if domain.NetworkInterface == "" || domain.NetworkLimitMbps == 0 {
			return s, ComputeFailure(fmt.Errorf("network resource domain incomplete"))
		}
		network, networkTime, err = l.sampleNetwork(ctx, domain.NetworkInterface)
		if err != nil {
			return s, err
		}
	}

	// 采集可选资源期间，先前的 CPU 和内存数据可能过期。
	// 遇到过期时重新采集一次，再检查各项采样时间。
	if !fresh(l.clock.Now(), baseTime) {
		s, baseTime, err = l.base.get(ctx, l.clock, readBase)
		if err != nil {
			return Snapshot{}, err
		}
	}
	// 用同一个时间点复核所有已采集数据的有效期。
	now := l.clock.Now()
	for _, at := range [...]time.Time{baseTime, gpuTime, diskTime, networkTime} {
		if !at.IsZero() && !fresh(now, at) {
			return Snapshot{}, ComputeFailure(fmt.Errorf("resource sample expired while checking other dimensions"))
		}
	}
	s.Devices, s.DiskAvailable = devices, diskAvailable
	if network.elapsed > 0 {
		s.network, s.networkLimit = network, domain.NetworkLimitMbps
		s.NetworkAvailable = math.Max(0, float64(domain.NetworkLimitMbps)-float64(network.bytes)*8000/float64(network.elapsed))
	}
	return s, nil
}

// statDisk 读取指定路径所在文件系统的可用块数和块大小。
func statDisk(path string) (uint64, uint64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, 0, err
	}
	size := stat.Frsize
	if size == 0 {
		size = stat.Bsize
	}
	if size <= 0 {
		return stat.Bavail, 0, nil
	}
	return stat.Bavail, uint64(size), nil
}

// sampleNetwork 对指定网卡的收发字节计数采样，计算窗口内较大的增量。
func (l *Linux) sampleNetwork(ctx context.Context, name string) (networkWindow, time.Time, error) {
	// 每张网卡独立保存采样结果，避免不同设备之间混用计数。
	l.mu.Lock()
	c := l.network[name]
	if c == nil {
		c = &sampleCache[networkWindow]{}
		l.network[name] = c
	}
	l.mu.Unlock()

	return c.get(ctx, l.clock, func() (networkWindow, time.Time, error) {
		a, b, elapsed, at, err := pair(ctx, l.clock, func() (netCounters, error) { return l.readNetwork(name) })
		if err != nil {
			return networkWindow{}, at, err
		}
		// 计数器回退时无法据差值计算带宽。
		if b.rx < a.rx || b.tx < a.tx {
			return networkWindow{}, at, ComputeFailure(fmt.Errorf("network byte counters moved backward"))
		}
		return networkWindow{bytes: max(b.rx-a.rx, b.tx-a.tx), elapsed: elapsed}, at, nil
	})
}

// readNetwork 从 /proc/net/dev 读取指定网卡的累计收发字节数。
func (l *Linux) readNetwork(name string) (netCounters, error) {
	body, err := l.text("/proc/net/dev")
	if err != nil {
		return netCounters{}, err
	}
	for line := range strings.SplitSeq(body, "\n") {
		device, stats, found := strings.Cut(line, ":")
		if !found || strings.TrimSpace(device) != name {
			continue
		}
		f := strings.Fields(stats)
		if len(f) != 16 {
			return netCounters{}, ParseFailure(fmt.Errorf("invalid /proc/net/dev statistics"))
		}
		rx, err1 := strconv.ParseUint(f[0], 10, 64)
		tx, err2 := strconv.ParseUint(f[8], 10, 64)
		if err1 != nil || err2 != nil {
			return netCounters{}, ParseFailure(fmt.Errorf("invalid /proc/net/dev byte counter"))
		}
		return netCounters{rx, tx}, nil
	}
	return netCounters{}, ReadFailure(fmt.Errorf("interface %q unavailable in /proc/net/dev", name))
}

// text 读取系统文件文本，并为读取错误补充路径。
func (l *Linux) text(path string) (string, error) {
	body, err := l.read(path)
	if err != nil {
		return "", ReadFailure(fmt.Errorf("read %s: %w", path, err))
	}
	return string(body), nil
}

// number 读取内容为一个无符号整数的系统文件。
func (l *Linux) number(path string) (uint64, error) {
	text, err := l.text(path)
	if err != nil {
		return 0, err
	}
	n, err := strconv.ParseUint(strings.TrimSpace(text), 10, 64)
	if err != nil {
		return 0, ParseFailure(fmt.Errorf("%s: expected unsigned integer", path))
	}
	return n, nil
}
