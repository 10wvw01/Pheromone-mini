//go:build linux

package resource

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

type cpuSample struct {
	limit float64
	ns    uint64
}

type baseSample struct {
	cpus                                 map[string]cpuSample
	memoryLimit, memoryUsed, memoryAvail uint64
	source                               string
}

// sampleBase 用两次采样计算可用 CPU 核数，并取第二次采样的内存数据。
func (l *Linux) sampleBase(ctx context.Context) (Snapshot, time.Time, error) {
	a, b, elapsed, at, err := pair(ctx, l.clock, l.readBase)
	if err != nil {
		return Snapshot{}, at, err
	}
	s := Snapshot{CPULimit: math.Inf(1), CPUAvailable: math.Inf(1), SampleTime: at, Source: b.source,
		MemoryLimit: b.memoryLimit, MemoryUsed: b.memoryUsed, MemoryAvailable: b.memoryAvail}
	if len(a.cpus) != len(b.cpus) || len(b.cpus) == 0 {
		return Snapshot{}, at, ComputeFailure(fmt.Errorf("CPU resource domains changed during sampling"))
	}
	// 比较各层的两次计数，使用其中最紧的限制和最少的可用核数。
	for id, end := range b.cpus {
		start, ok := a.cpus[id]
		if !ok || end.ns < start.ns || end.limit != start.limit || end.limit < 0 || !finite(end.limit) {
			return Snapshot{}, at, ComputeFailure(fmt.Errorf("CPU counter or limit changed incompatibly in %s", id))
		}
		if end.limit == 0 {
			continue // 无配额限制的控制组仍须提供有效且不回退的计数
		}
		used := float64(end.ns-start.ns) / float64(elapsed)
		available := max(0, end.limit-used)
		s.CPULimit = min(s.CPULimit, end.limit)
		if available < s.CPUAvailable || (available == s.CPUAvailable && used < s.CPUUsed) {
			s.CPUAvailable, s.CPUUsed = available, used
		}
	}
	return s, at, nil
}

// readBase 读取主机及进程所属控制组的 CPU 和内存数据。
func (l *Linux) readBase() (baseSample, error) {
	total, avail, err := l.hostMemory()
	if err != nil {
		return baseSample{}, err
	}
	cpu, memory, cpuset, err := l.groups()
	if err != nil {
		return baseSample{}, err
	}
	allowed, err := l.allowedCPUs(cpuset)
	if err != nil {
		return baseSample{}, err
	}
	host, err := l.hostCPU(allowed)
	if err != nil {
		return baseSample{}, err
	}
	// 以宿主机数据为起点，再纳入控制组限制。
	s := baseSample{cpus: map[string]cpuSample{"host/affinity": host},
		memoryLimit: total, memoryUsed: total - avail, memoryAvail: avail, source: "procfs"}
	if cpu.version != 0 {
		s.source = fmt.Sprintf("procfs+cgroup(v%d CPU,v%d memory)", cpu.version, memory.version)
	}
	if err := l.readCPUGroup(cpu, s.cpus); err != nil {
		return baseSample{}, err
	}
	if err := l.readMemoryGroup(memory, &s); err != nil {
		return baseSample{}, err
	}
	return s, nil
}

// hostMemory 读取主机内存总量和可用量，并转换为字节。
func (l *Linux) hostMemory() (uint64, uint64, error) {
	body, err := l.text("/proc/meminfo")
	if err != nil {
		return 0, 0, err
	}
	// 只读取所需内存字段，并验证单位后统一换算为字节。
	vals := make(map[string]uint64, 2)
	for line := range strings.SplitSeq(body, "\n") {
		label, raw, _ := strings.Cut(line, ":")
		if label != "MemTotal" && label != "MemAvailable" {
			continue
		}
		f := strings.Fields(raw)
		if _, exists := vals[label]; exists || len(f) != 2 || f[1] != "kB" {
			return 0, 0, ParseFailure(fmt.Errorf("/proc/meminfo: invalid %s field", label))
		}
		n, err := strconv.ParseUint(f[0], 10, 64)
		if err != nil {
			return 0, 0, ParseFailure(fmt.Errorf("/proc/meminfo: invalid %s count", label))
		}
		if n > math.MaxUint64/1024 {
			return 0, 0, ComputeFailure(fmt.Errorf("/proc/meminfo: %s overflows bytes", label))
		}
		vals[label] = n * 1024
	}
	total, avail := vals["MemTotal"], vals["MemAvailable"]
	if _, ok := vals["MemAvailable"]; !ok || total == 0 || avail > total {
		return 0, 0, ParseFailure(fmt.Errorf("/proc/meminfo: missing or inconsistent memory fields"))
	}
	return total, avail, nil
}

type cpuRange struct{ from, to uint32 }
type cpuList []cpuRange

// parseCPUList 解析 Linux CPU 编号和编号区间，要求区间有序且不重叠。
func parseCPUList(s string) (cpuList, error) {
	list := make(cpuList, 0, 8)
	for field := range strings.SplitSeq(strings.TrimSpace(s), ",") {
		from, to, span := strings.Cut(field, "-")
		if !span {
			to = from
		}
		// 依赖区间排序快速判断某个核是否在列表内，因此拒绝重叠或乱序。
		first, err1 := strconv.ParseUint(from, 10, 32)
		last, err2 := strconv.ParseUint(to, 10, 32)
		if err1 != nil || err2 != nil || first > last || (len(list) > 0 && uint32(first) <= list[len(list)-1].to) {
			return nil, ParseFailure(fmt.Errorf("invalid Linux CPU list"))
		}
		list = append(list, cpuRange{uint32(first), uint32(last)})
	}
	return list, nil
}

// has 判断 CPU 编号是否在列表中。空切片值表示该来源未提供额外限制。
func (set cpuList) has(n uint32) bool {
	if set == nil {
		return true
	}
	for _, r := range set {
		if n < r.from {
			break
		}
		if n <= r.to {
			return true
		}
	}
	return false
}

// allowedCPUs 读取进程的 CPU 亲和性与控制组的有效 CPU 列表。
func (l *Linux) allowedCPUs(group hierarchy) ([]cpuList, error) {
	body, err := l.text("/proc/self/status")
	if err != nil {
		return nil, err
	}
	var lists []cpuList
	for line := range strings.SplitSeq(body, "\n") {
		if value, found := strings.CutPrefix(line, "Cpus_allowed_list:"); found {
			list, err := parseCPUList(value)
			if err != nil {
				return nil, err
			}
			lists = append(lists, list)
			break
		}
	}
	if len(lists) == 0 {
		return nil, ParseFailure(fmt.Errorf("/proc/self/status: missing Cpus_allowed_list"))
	}
	// 在线核必须同时满足进程亲和性和控制组的限制。
	list, err := l.readCPUSet(group)
	if err != nil {
		return nil, err
	}
	return append(lists, list), nil
}

// hostCPU 统计进程可用的在线 CPU 核数及其累计忙碌时间。
func (l *Linux) hostCPU(allowed []cpuList) (cpuSample, error) {
	body, err := l.text("/proc/stat")
	if err != nil {
		return cpuSample{}, err
	}
	// /proc/stat 使用 USER_HZ 统计时间，支持的 amd64 主机每秒计 100 个单位。
	const nsPerTick = uint64(time.Second) / 100
	var sample cpuSample
	for line := range strings.SplitSeq(body, "\n") {
		f := strings.Fields(line)
		if len(f) == 0 || !strings.HasPrefix(f[0], "cpu") || f[0] == "cpu" {
			continue
		}
		n, err := strconv.ParseUint(f[0][3:], 10, 32)
		if err != nil || len(f) < 9 {
			return cpuSample{}, ParseFailure(fmt.Errorf("/proc/stat: invalid per-CPU line"))
		}
		if !allowed[0].has(uint32(n)) || !allowed[1].has(uint32(n)) {
			continue
		}
		sample.limit++
		// 统计用户、低优先级用户、内核、硬中断、软中断和虚拟化占用时间。
		// 客户机时间已计入用户时间，空闲和 I/O 等待时间不计入。
		for _, i := range [...]int{1, 2, 3, 6, 7, 8} {
			ticks, err := strconv.ParseUint(f[i], 10, 64)
			if err != nil {
				return cpuSample{}, ParseFailure(fmt.Errorf("/proc/stat: invalid CPU counter"))
			}
			if ticks > (math.MaxUint64-sample.ns)/nsPerTick {
				return cpuSample{}, ComputeFailure(fmt.Errorf("/proc/stat: CPU time overflow"))
			}
			sample.ns += ticks * nsPerTick
		}
	}
	if sample.limit == 0 {
		return cpuSample{}, ComputeFailure(fmt.Errorf("no online CPU in process affinity and cpuset"))
	}
	return sample, nil
}
