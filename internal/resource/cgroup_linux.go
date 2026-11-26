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

// hierarchy 保存一个资源控制器从当前进程到可见挂载根的路径。参见 V0.6 工程实现设计 §14.2。
type hierarchy struct {
	version int
	// 路径从进程所在控制组依次排列到挂载根目录。
	paths         []string
	accounting    []string
	mountedAtRoot bool
	noPrefix      bool
}

// mount 保存 /proc/self/mountinfo 中用于还原控制组目录的挂载信息。
type mount struct {
	id, root, point, filesystem, options string
}

// groups 按 V0.6 §14.2 从进程所属组和可见挂载点定位各资源控制器。
func (l *Linux) groups() (cpu, memory, cpuset hierarchy, err error) {
	body, err := l.text("/proc/self/cgroup")
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) && l.noCgroupFilesystem() {
			return cpu, memory, cpuset, nil
		}
		return cpu, memory, cpuset, err
	}
	members := make(map[string]string)
	for line := range strings.SplitSeq(body, "\n") {
		if line == "" {
			continue
		}
		id, rest, first := strings.Cut(line, ":")
		controllers, group, second := strings.Cut(rest, ":")
		hierarchyID, idErr := strconv.ParseUint(id, 10, 32)
		if !first || !second || idErr != nil || (hierarchyID == 0) != (controllers == "") || !safeAbsolute(group) {
			return cpu, memory, cpuset, ParseFailure(fmt.Errorf("/proc/self/cgroup: invalid membership"))
		}
		for name := range strings.SplitSeq(controllers, ",") {
			if _, exists := members[name]; exists {
				return cpu, memory, cpuset, ParseFailure(fmt.Errorf("/proc/self/cgroup: duplicate controller membership"))
			}
			members[name] = group
		}
	}
	// 只有与资源检查相关的控制器才需要挂载信息。v1 命名层级（例如 name=systemd）本身不施加 CPU 或内存限额。
	if members[""] == "" && members["cpu"] == "" && members["memory"] == "" && members["cpuset"] == "" {
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
		// v1 各控制器分别挂载。没有该控制器的成员记录时，该维度没有对应的控制组限额。
		return hierarchy{}, nil
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
	// v1 的 CPU 配额与用量可以分别挂载。只有能对齐的层级才能用于计算有限配额。
	if cpu.version == 1 && members["cpuacct"] != "" {
		if members["cpu"] != members["cpuacct"] {
			err = ReadFailure(fmt.Errorf("CPU quota and CPU accounting groups do not align"))
			return
		}
		accounting, accountErr := locate(mounts, members["cpuacct"], "cpuacct", 1)
		if accountErr != nil {
			err = accountErr
			return
		}
		cpu.accounting = accounting.paths
	}
	return
}

// noCgroupFilesystem 用内核文件系统清单识别未提供 cgroup 的 Linux 环境。
// 若清单不可读或仍包含 cgroup，就保留原成员文件读取错误。
func (l *Linux) noCgroupFilesystem() bool {
	body, err := l.text("/proc/filesystems")
	if err != nil {
		return false
	}
	proc := false
	for line := range strings.SplitSeq(body, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		switch fields[len(fields)-1] {
		case "cgroup", "cgroup2", "cpuset":
			return false
		case "proc":
			proc = true
		}
	}
	return proc
}

// safeAbsolute 检查路径是否为已规范化且不含空字符的绝对路径。
func safeAbsolute(p string) bool {
	return strings.HasPrefix(p, "/") && path.Clean(p) == p && !strings.ContainsRune(p, 0)
}

// parseMounts 从 Linux 挂载记录中提取控制组挂载点并还原路径转义。
func parseMounts(body string) ([]mount, error) {
	var candidates []mount
	points := make(map[string]string)
	parents := make(map[string]string)
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
		point := escapes.Replace(attrs[4])
		points[attrs[0]], parents[attrs[0]] = point, attrs[1]
		if fs[0] != "cgroup" && fs[0] != "cgroup2" {
			continue
		}
		root := escapes.Replace(attrs[3])
		// cgroup 命名空间可以将旧挂载的根显示为 /..。无法据此定位当前成员，继续查找其他挂载。
		if root == "/.." || strings.HasPrefix(root, "/../") {
			continue
		}
		if !safeAbsolute(root) || !safeAbsolute(point) {
			return nil, ParseFailure(fmt.Errorf("/proc/self/mountinfo: invalid cgroup path"))
		}
		candidates = append(candidates, mount{attrs[0], root, point, fs[0], fs[2]})
	}
	// 同一路径的上层挂载以其覆盖的挂载为父项。只从可见的挂载读取限额。
	covered := make(map[string]bool)
	for id, parent := range parents {
		if id != parent && points[id] == points[parent] {
			covered[parent] = true
		}
	}
	visible := candidates[:0]
	for _, candidate := range candidates {
		if !covered[candidate.id] {
			visible = append(visible, candidate)
		}
	}
	return visible, nil
}

// locate 为进程所属控制组选择挂载点，列出从该组到挂载根目录的路径。
func locate(mounts []mount, group, controller string, version int) (hierarchy, error) {
	var selected mount
	for _, m := range mounts {
		if version == 2 && m.filesystem != "cgroup2" {
			continue
		}
		if version == 1 && (m.filesystem != "cgroup" || !hasMountOption(m.options, controller)) {
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
	h := hierarchy{version: version, mountedAtRoot: selected.root == "/", noPrefix: hasMountOption(selected.options, "noprefix")}
	// 将每层控制组路径映射到当前进程可见的挂载目录。
	for current := group; ; current = path.Dir(current) {
		h.paths = append(h.paths, path.Join(selected.point, strings.TrimPrefix(current, selected.root)))
		if current == selected.root {
			return h, nil
		}
	}
}

// hasMountOption 检查逗号分隔的挂载选项，避免只匹配到控制器名称的一部分。
func hasMountOption(options, name string) bool {
	for option := range strings.SplitSeq(options, ",") {
		if option == name {
			return true
		}
	}
	return false
}

// v2LimitAbsent 判断缺少的限额文件是否符合 v2 的控制器启用规则。
// 子组只有在父组向下启用该控制器后才有对应限额文件。挂载根之外无法核验父组。
func (l *Linux) v2LimitAbsent(h hierarchy, index int, controller string, err error) bool {
	if h.version != 2 || !errors.Is(err, fs.ErrNotExist) {
		return false
	}
	if index == len(h.paths)-1 {
		if !h.mountedAtRoot {
			return false
		}
		// 私有命名空间也可能将子组显示为 /。只有真实层级根没有 cgroup.type。
		_, err := l.text(path.Join(h.paths[index], "cgroup.type"))
		return errors.Is(err, fs.ErrNotExist)
	}
	parentControls, err := l.text(path.Join(h.paths[index+1], "cgroup.subtree_control"))
	if err != nil {
		return false
	}
	for _, name := range strings.Fields(parentControls) {
		if name == controller {
			return false
		}
	}
	// 父组未启用控制器仍须确认当前成员目录可读，避免把目录不可见当作无配额。
	groupType, err := l.text(path.Join(h.paths[index], "cgroup.type"))
	if err != nil {
		return false
	}
	switch strings.TrimSpace(groupType) {
	case "domain", "domain threaded", "threaded":
		return true
	default:
		return false
	}
}

// readCPUSet 查找进程控制组或其祖先提供的有效 CPU 列表。
func (l *Linux) readCPUSet(h hierarchy) (cpuList, error) {
	for _, dir := range h.paths {
		name := "cpuset.cpus.effective"
		if h.version == 1 {
			name = "cpuset.effective_cpus"
		}
		// v1 的 noprefix 挂载省略控制器名称，旧内核也可能没有有效 CPU 列表文件。
		fallback := "cpuset.cpus"
		if h.version == 1 && h.noPrefix {
			name, fallback = "effective_cpus", "cpus"
		}
		value, err := l.text(path.Join(dir, name))
		if h.version == 1 && errors.Is(err, fs.ErrNotExist) {
			value, err = l.text(path.Join(dir, fallback))
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
		// 已确认没有限额的层级仍登记为零，以便识别两次采样之间的配额变化。
		if l.v2LimitAbsent(h, i, "cpu", err) {
			samples[dir] = cpuSample{}
			continue
		}
		if err != nil {
			return err
		}
		var ns uint64
		if h.version == 1 {
			if i >= len(h.accounting) {
				if limit != 0 {
					return ReadFailure(fmt.Errorf("%s: finite CPU quota has no corresponding cpuacct usage", dir))
				}
			} else {
				ns, err = l.number(path.Join(h.accounting[i], "cpuacct.usage"))
			}
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
		// 仍需检查祖先限额，即使当前组未被分配内存控制器。
		if l.v2LimitAbsent(h, i, "memory", err) {
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
