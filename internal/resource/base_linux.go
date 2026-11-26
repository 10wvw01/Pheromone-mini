//go:build linux

package resource

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
)

// cpuSample 保存某个资源约束域的核数上限和累计 CPU 纳秒数，供 V0.6 §14.2 的双采样计算使用。
type cpuSample struct {
	limit float64
	ns    uint64
	// online 仅用于系统采样，记录进程可用的在线 CPU 编号。
	online []uint32
}

// baseSample 保存一次未经差值计算的 CPU 累计量及内存数据。
type baseSample struct {
	cpus                                 map[string]cpuSample
	memoryLimit, memoryUsed, memoryAvail uint64
	source                               string
}

// sampleBase 按 V0.6 §14.2 分别计算每个约束域的可用核数，取其中最小值，并使用最新的内存样本。
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
	// 按 §14.2 对各约束域分别计算限额减使用量，再比较各域的可用核数。
	for id, end := range b.cpus {
		start, ok := a.cpus[id]
		if !ok || end.ns < start.ns || end.limit != start.limit || end.limit < 0 || !finite(end.limit) || !slices.Equal(start.online, end.online) {
			return Snapshot{}, at, ComputeFailure(fmt.Errorf("CPU counter, limit or online set changed incompatibly in %s", id))
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

// readBase 按 V0.6 §14.2 汇总系统统计和当前进程的控制组限制。已确认存在的控制组限制必须参与计算。
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
	// 以当前 Linux 实例的系统统计为起点，再逐层纳入控制组限制。
	s := baseSample{cpus: map[string]cpuSample{"host/affinity": host},
		memoryLimit: total, memoryUsed: total - avail, memoryAvail: avail, source: "procfs"}
	switch {
	case cpu.version != 0 && memory.version != 0:
		s.source = fmt.Sprintf("procfs+cgroup(v%d CPU,v%d memory)", cpu.version, memory.version)
	case cpu.version != 0:
		s.source = fmt.Sprintf("procfs+cgroup(v%d CPU)", cpu.version)
	case memory.version != 0:
		s.source = fmt.Sprintf("procfs+cgroup(v%d memory)", memory.version)
	case cpuset.version != 0:
		s.source = fmt.Sprintf("procfs+cgroup(v%d cpuset)", cpuset.version)
	}
	if err := l.readCPUGroup(cpu, s.cpus); err != nil {
		return baseSample{}, err
	}
	if err := l.readMemoryGroup(memory, &s); err != nil {
		return baseSample{}, err
	}
	return s, nil
}

// hostMemory 按 V0.6 §14.2 读取 Linux 实例的内存统计。
// 旧内核缺少 MemAvailable 时，使用不含可回收缓存的 MemFree 作为保守估计。
func (l *Linux) hostMemory() (uint64, uint64, error) {
	body, err := l.text("/proc/meminfo")
	if err != nil {
		return 0, 0, err
	}
	// 只读取所需内存字段，并验证单位后统一换算为字节。
	fields := make(map[string]string, 3)
	for line := range strings.SplitSeq(body, "\n") {
		label, raw, _ := strings.Cut(line, ":")
		if label != "MemTotal" && label != "MemAvailable" && label != "MemFree" {
			continue
		}
		if _, exists := fields[label]; exists {
			return 0, 0, ParseFailure(fmt.Errorf("/proc/meminfo: duplicate %s field", label))
		}
		fields[label] = raw
	}
	total, err := memoryBytes("MemTotal", fields["MemTotal"])
	if err != nil {
		return 0, 0, err
	}
	label := "MemAvailable"
	if _, ok := fields[label]; !ok {
		label = "MemFree"
	}
	avail, err := memoryBytes(label, fields[label])
	if err != nil {
		return 0, 0, err
	}
	if total == 0 || avail > total {
		return 0, 0, ParseFailure(fmt.Errorf("/proc/meminfo: inconsistent memory fields"))
	}
	return total, avail, nil
}

// memoryBytes 校验并换算 procfs 的内存字段。文件中的 kB 按 1024 字节计算。
func memoryBytes(label, raw string) (uint64, error) {
	f := strings.Fields(raw)
	if len(f) != 2 || f[1] != "kB" {
		return 0, ParseFailure(fmt.Errorf("/proc/meminfo: invalid %s field", label))
	}
	n, err := strconv.ParseUint(f[0], 10, 64)
	if err != nil {
		return 0, ParseFailure(fmt.Errorf("/proc/meminfo: invalid %s count", label))
	}
	if n > math.MaxUint64/1024 {
		return 0, ComputeFailure(fmt.Errorf("/proc/meminfo: %s overflows bytes", label))
	}
	return n * 1024, nil
}

// cpuRange 表示含首尾编号的 Linux CPU 区间。
type cpuRange struct{ from, to uint32 }

// cpuList 保存按起点递增且互不重叠的 CPU 区间，用于求 §14.2 的有效 CPU 集合。
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
		// CPU 上下线或亲和性变化可能保持核数不变，因此还记录实际编号。
		cpu := uint32(n)
		if len(sample.online) != 0 && cpu <= sample.online[len(sample.online)-1] {
			return cpuSample{}, ParseFailure(fmt.Errorf("/proc/stat: unordered or duplicate CPU IDs"))
		}
		sample.online = append(sample.online, cpu)
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
