//go:build linux

package resource

import (
	"errors"
	"fmt"
	"io/fs"
	"math"
	"path"
	"strconv"
	"strings"
)

type hierarchy struct {
	version int
	// 路径从进程所在控制组依次排列到挂载根目录。
	paths         []string
	accounting    []string
	mountedAtRoot bool
}

type mount struct {
	root, point, filesystem, options string
}

// groups 根据进程所属组和可见挂载点查找 CPU、内存及 CPU 集合的控制组路径。
func (l *Linux) groups() (cpu, memory, cpuset hierarchy, err error) {
	body, err := l.text("/proc/self/cgroup")
	if err != nil {
		return cpu, memory, cpuset, err
	}
	members := make(map[string]string)
	for line := range strings.SplitSeq(body, "\n") {
		if line == "" {
			continue
		}
		_, rest, first := strings.Cut(line, ":")
		controllers, group, second := strings.Cut(rest, ":")
		if !first || !second || !safeAbsolute(group) {
			return cpu, memory, cpuset, ParseFailure(fmt.Errorf("/proc/self/cgroup: invalid membership"))
		}
		for name := range strings.SplitSeq(controllers, ",") {
			if _, exists := members[name]; exists {
				return cpu, memory, cpuset, ParseFailure(fmt.Errorf("/proc/self/cgroup: duplicate controller membership"))
			}
			members[name] = group
		}
	}
	if len(members) == 0 {
		return cpu, memory, cpuset, nil
	}
	data, err := l.text("/proc/self/mountinfo")
	if err != nil {
		return cpu, memory, cpuset, err
	}
	mounts, err := parseMounts(data)
	if err != nil {
		return cpu, memory, cpuset, err
	}

	// 优先查找独立控制器，缺失时使用 v2 的统一控制组。
	resolve := func(controller string) (hierarchy, error) {
		if group, ok := members[controller]; ok {
			return locate(mounts, group, controller, 1)
		}
		if group, ok := members[""]; ok {
			return locate(mounts, group, "", 2)
		}
		if controller == "cpuset" {
			return hierarchy{}, nil
		}
		return hierarchy{}, ReadFailure(fmt.Errorf("missing cgroup membership for %s", controller))
	}
	if cpu, err = resolve("cpu"); err != nil {
		return
	}
	if memory, err = resolve("memory"); err != nil {
		return
	}
	if cpuset, err = resolve("cpuset"); err != nil {
		return
	}
	// v1 的 CPU 配额和用量可能位于不同挂载点，需要核对两套层级。
	if cpu.version == 1 {
		accounting, accountErr := resolve("cpuacct")
		if accountErr != nil {
			err = accountErr
		} else if accounting.version != 1 || len(cpu.paths) != len(accounting.paths) || members["cpu"] != members["cpuacct"] {
			err = ReadFailure(fmt.Errorf("CPU quota and CPU accounting groups do not align"))
		} else {
			cpu.accounting = accounting.paths
		}
	}
	return
}

// safeAbsolute 检查路径是否为已规范化且不含空字符的绝对路径。
func safeAbsolute(p string) bool {
	return strings.HasPrefix(p, "/") && path.Clean(p) == p && !strings.ContainsRune(p, 0)
}

// parseMounts 从 Linux 挂载记录中提取控制组挂载点并还原路径转义。
func parseMounts(body string) ([]mount, error) {
	var out []mount
	escapes := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	for line := range strings.SplitSeq(body, "\n") {
		if line == "" {
			continue
		}
		left, right, ok := strings.Cut(line, " - ")
		attrs, fs := strings.Fields(left), strings.Fields(right)
		if !ok || len(attrs) < 6 || len(fs) < 3 {
			return nil, ParseFailure(fmt.Errorf("/proc/self/mountinfo: invalid record"))
		}
		if fs[0] != "cgroup" && fs[0] != "cgroup2" {
			continue
		}
		root, point := escapes.Replace(attrs[3]), escapes.Replace(attrs[4])
		if !safeAbsolute(root) || !safeAbsolute(point) {
			return nil, ParseFailure(fmt.Errorf("/proc/self/mountinfo: invalid cgroup path"))
		}
		out = append(out, mount{root, point, fs[0], fs[2]})
	}
	return out, nil
}

// locate 为进程所属控制组选择挂载点，列出从该组到挂载根目录的路径。
func locate(mounts []mount, group, controller string, version int) (hierarchy, error) {
	var selected mount
	for _, m := range mounts {
		if version == 2 && m.filesystem != "cgroup2" {
			continue
		}
		if version == 1 && (m.filesystem != "cgroup" || !strings.Contains(","+m.options+",", ","+controller+",")) {
			continue
		}
		if m.root != "/" && group != m.root && !strings.HasPrefix(group, m.root+"/") {
			continue
		}
		// 优先选择能读取最多祖先控制组的挂载点。
		if selected.point == "" || len(m.root) < len(selected.root) {
			selected = m
		}
	}
	if selected.point == "" {
		return hierarchy{}, ReadFailure(fmt.Errorf("cgroup v%d %s membership has no visible mount", version, controller))
	}
	h := hierarchy{version: version, mountedAtRoot: selected.root == "/"}
	// 将每层控制组路径映射到当前进程可见的挂载目录。
	for current := group; ; current = path.Dir(current) {
		h.paths = append(h.paths, path.Join(selected.point, strings.TrimPrefix(current, selected.root)))
		if current == selected.root {
			return h, nil
		}
	}
}

// readCPUSet 查找进程控制组或其祖先提供的有效 CPU 列表。
func (l *Linux) readCPUSet(h hierarchy) (cpuList, error) {
	for _, dir := range h.paths {
		name := "cpuset.cpus.effective"
		if h.version == 1 {
			name = "cpuset.effective_cpus"
		}
		value, err := l.text(path.Join(dir, name))
		if h.version == 1 && errors.Is(err, fs.ErrNotExist) {
			value, err = l.text(path.Join(dir, "cpuset.cpus"))
		}
		if h.version == 2 && errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(value) != "" {
			return parseCPUList(value)
		}
	}
	// 内核已在 /proc/self/status 中反映当前进程实际可用的 CPU 集合。
	return nil, nil
}

// readCPUGroup 读取控制组各层的 CPU 配额与累计使用时间。
func (l *Linux) readCPUGroup(h hierarchy, samples map[string]cpuSample) error {
	for i, dir := range h.paths {
		limit, err := l.cpuLimit(dir, h.version)
		// v2 根组没有 cpu.max，但仍可从 cpu.stat 读取使用量。
		if err != nil && h.version == 2 && h.mountedAtRoot && i == len(h.paths)-1 && errors.Is(err, fs.ErrNotExist) {
			limit, err = 0, nil
		}
		if err != nil {
			return err
		}
		var ns uint64
		if h.version == 1 {
			ns, err = l.number(path.Join(h.accounting[i], "cpuacct.usage"))
		} else {
			ns, err = l.cpuUsageV2(dir)
		}
		if err != nil {
			return err
		}
		samples[dir] = cpuSample{limit: limit, ns: ns}
	}
	return nil
}

// cpuLimit 将控制组的 CPU 配额换算成核数，返回零表示没有配额限制。
func (l *Linux) cpuLimit(dir string, version int) (float64, error) {
	var q, p, unlimited string
	// v1 和 v2 使用不同的文件和不限额标记。
	if version == 2 {
		raw, err := l.text(path.Join(dir, "cpu.max"))
		if err != nil {
			return 0, err
		}
		f := strings.Fields(raw)
		if len(f) != 2 {
			return 0, ParseFailure(fmt.Errorf("%s/cpu.max: expected quota and period", dir))
		}
		q, p, unlimited = f[0], f[1], "max"
	} else {
		var err error
		q, err = l.text(path.Join(dir, "cpu.cfs_quota_us"))
		if err != nil {
			return 0, err
		}
		p, err = l.text(path.Join(dir, "cpu.cfs_period_us"))
		if err != nil {
			return 0, err
		}
		q, p, unlimited = strings.TrimSpace(q), strings.TrimSpace(p), "-1"
	}
	// 用配额除以调度周期，得到等效 CPU 核数。
	period, err := strconv.ParseUint(p, 10, 64)
	if err != nil || period == 0 {
		return 0, ParseFailure(fmt.Errorf("%s: invalid CPU quota period", dir))
	}
	if q == unlimited {
		return 0, nil
	}
	quota, err := strconv.ParseUint(q, 10, 64)
	if err != nil || quota == 0 {
		return 0, ParseFailure(fmt.Errorf("%s: invalid CPU quota", dir))
	}
	return float64(quota) / float64(period), nil
}

// cpuUsageV2 读取控制组累计使用的 CPU 时间，并从微秒换算为纳秒。
func (l *Linux) cpuUsageV2(dir string) (uint64, error) {
	file := path.Join(dir, "cpu.stat")
	raw, err := l.text(file)
	if err != nil {
		return 0, err
	}
	seen := false
	var ns uint64
	for line := range strings.SplitSeq(raw, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != "usage_usec" {
			continue
		}
		if seen || len(fields) != 2 {
			return 0, ParseFailure(fmt.Errorf("%s: invalid usage_usec field", file))
		}
		us, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return 0, ParseFailure(fmt.Errorf("%s: invalid usage_usec counter", file))
		}
		if us > math.MaxUint64/1000 {
			return 0, ComputeFailure(fmt.Errorf("%s: usage_usec overflows nanoseconds", file))
		}
		seen, ns = true, us*1000
	}
	if !seen {
		return 0, ParseFailure(fmt.Errorf("%s: usage_usec missing", file))
	}
	return ns, nil
}

// readMemoryGroup 逐层检查控制组的内存限制和使用量，保留最小的可用内存。
func (l *Linux) readMemoryGroup(h hierarchy, s *baseSample) error {
	for i, dir := range h.paths {
		limitFile, usageFile := "memory.max", "memory.current"
		if h.version == 1 {
			limitFile, usageFile = "memory.limit_in_bytes", "memory.usage_in_bytes"
		}
		raw, err := l.text(path.Join(dir, limitFile))
		// v2 根组没有内存控制器限额文件。
		if err != nil && h.version == 2 && h.mountedAtRoot && i == len(h.paths)-1 && errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		var limit uint64
		unlimited := strings.TrimSpace(raw) == "max" && h.version == 2
		if !unlimited {
			limit, err = strconv.ParseUint(strings.TrimSpace(raw), 10, 64)
			if err != nil {
				return ParseFailure(fmt.Errorf("%s/%s: invalid memory limit", dir, limitFile))
			}
			// v1 使用按页对齐的 PAGE_COUNTER_MAX 表示不限额。
			unlimited = h.version == 1 && limit >= 0x7ffffffffffff000
		}
		usage, err := l.number(path.Join(dir, usageFile))
		if err != nil {
			return err
		}
		// 不限额的层级不收紧已有的内存限制。
		if !unlimited {
			s.memoryLimit = min(s.memoryLimit, limit)
			free := limit - min(limit, usage)
			if free < s.memoryAvail {
				s.memoryAvail, s.memoryUsed = free, usage
			}
		}
	}
	return nil
}
