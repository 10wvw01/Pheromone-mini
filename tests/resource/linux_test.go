//go:build linux

package resource_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"triggerEngine/internal/config"
	"triggerEngine/internal/contract"
	"triggerEngine/internal/resource"
	"triggerEngine/internal/timing"
	"triggerEngine/tests/testclock"
)

type sampleResult struct {
	s   resource.Snapshot
	err error
}

func files(t testing.TB, name string) map[string]string {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var contents map[string]string
	if err := json.Unmarshal(data, &contents); err != nil {
		t.Fatal(err)
	}
	return contents
}

func fromFiles(clock timing.Clock, before, after map[string]string) *resource.Linux {
	return resource.NewLinuxWithSystemReaders(clock, nil, fileReader(clock, before, after), nil)
}

func fileReader(clock timing.Clock, before, after map[string]string) func(string) ([]byte, error) {
	start := clock.Now()
	return func(path string) ([]byte, error) {
		if clock.Now().After(start) {
			if s, ok := after[path]; ok {
				return []byte(s), nil
			}
		}
		if s, ok := before[path]; ok {
			return []byte(s), nil
		}
		return nil, fs.ErrNotExist
	}
}

func waitTimer(t *testing.T, clock *testclock.Clock, offset time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if err := clock.WaitTimer(ctx, clock.Now().Add(offset)); err != nil {
		t.Fatal(err)
	}
}

func getSample(t *testing.T, c <-chan sampleResult) sampleResult {
	t.Helper()
	select {
	case r := <-c:
		return r
	case <-time.After(3 * time.Second):
		t.Fatal("resource sample blocked")
		return sampleResult{}
	}
}

func TestLinuxFixtures(t *testing.T) {
	for _, name := range []string{"v1", "v2", "host"} {
		t.Run(name, func(t *testing.T) {
			after := map[string]string{
				"/cg/cpu/svc/cpuacct.usage": "450000000\n",
				"/cg/service/cpu.stat":      "usage_usec 450000\n",
			}
			clock := testclock.New()
			probe := fromFiles(clock, files(t, name), after)
			out := make(chan sampleResult, 1)
			go func() {
				s, err := probe.Read(t.Context(), config.Domain{}, contract.FunctionInfo{})
				out <- sampleResult{s, err}
			}()
			waitTimer(t, clock, 100*time.Millisecond)
			clock.Advance(100 * time.Millisecond)
			r := getSample(t, out)
			if r.err != nil {
				t.Fatal(r.err)
			}
			s := r.s
			if name == "host" {
				if s.Source != "procfs" || s.CPUAvailable != 6 || s.MemoryAvailable != 4096*config.MiB {
					t.Fatalf("TEST-RTE-19: %+v", s)
				}
				return
			}
			if s.CPULimit != 4 || s.CPUUsed != 1.5 || s.CPUAvailable != 2.5 ||
				s.MemoryLimit != 1024*config.MiB || s.MemoryUsed != 768*config.MiB || s.MemoryAvailable != 256*config.MiB {
				t.Fatalf("TEST-RTE-04/05/17: %+v", s)
			}
			for _, tt := range []struct {
				cpu float64
				mb  uint64
				dim string
			}{{2, 256, ""}, {2.5, 256, ""}, {3, 256, "cpu"}, {2, 512, "memory"}} {
				if got, err := resource.Evaluate(contract.ResourceSpec{CPUCores: tt.cpu, MemoryMB: tt.mb}, s); got != tt.dim || err != nil {
					t.Fatalf("TEST-RTE-17: %+v, dimension=%q err=%v", tt, got, err)
				}
			}
		})
	}
}

func TestLinuxInvalidSamples(t *testing.T) {
	cases := []struct {
		id, path, value, reason string
	}{
		{"TEST-RTE-06/missing_limit", "/cg/service/memory.max", "", "read"},
		{"TEST-RTE-06/missing_usage", "/cg/service/memory.current", "", "read"},
		{"TEST-RTE-12/parse", "/cg/service/cpu.max", "4 nope", "parse"},
		{"TEST-RTE-18/overflow", "/cg/service/cpu.stat", "usage_usec 18446744073709552\n", "calculation"},
		{"ENV-01/no_mount", "/proc/self/mountinfo", "4 2 0:2 / / rw - tmpfs tmpfs rw\n", "read"},
		{"ENV-01/memory_units", "/proc/meminfo", "MemTotal: 10 MB\nMemAvailable: 3 MB", "parse"},
		{"ENV-01/affinity", "/proc/self/status", "Cpus_allowed_list: 8-9\n", "calculation"},
	}
	for _, tt := range cases {
		t.Run(tt.id, func(t *testing.T) {
			input := files(t, "v2")
			if tt.value == "" {
				delete(input, tt.path)
			} else {
				input[tt.path] = tt.value
			}
			p := fromFiles(testclock.New(), input, nil)
			s, err := p.Read(t.Context(), config.Domain{}, contract.FunctionInfo{})
			if err == nil || resource.Reason(err) != tt.reason || s.CPUAvailable != 0 {
				t.Fatalf("fail-closed sample: %+v, %v (reason %s)", s, err, resource.Reason(err))
			}
		})
	}
	t.Run("TEST-RTE-18/counter_rollback", func(t *testing.T) {
		clock := testclock.New()
		p := fromFiles(clock, files(t, "v2"), map[string]string{"/cg/service/cpu.stat": "usage_usec 200000"})
		out := make(chan sampleResult, 1)
		go func() {
			s, err := p.Read(t.Context(), config.Domain{}, contract.FunctionInfo{})
			out <- sampleResult{s, err}
		}()
		waitTimer(t, clock, 100*time.Millisecond)
		clock.Advance(100 * time.Millisecond)
		if r := getSample(t, out); r.err == nil || resource.Reason(r.err) != "calculation" {
			t.Fatalf("expected rollback failure: %+v", r)
		}
	})
}

func TestSharedSnapshots(t *testing.T) {
	clock := testclock.New()
	read := fileReader(clock, files(t, "host"), nil)
	var reads atomic.Int64
	p := resource.NewLinuxWithSystemReaders(clock, nil, func(name string) ([]byte, error) {
		if name == "/proc/stat" {
			reads.Add(1)
		}
		return read(name)
	}, nil)
	const workers = 32
	out := make(chan sampleResult, workers)
	start := make(chan struct{})
	var done sync.WaitGroup
	for range workers {
		done.Go(func() {
			<-start
			s, err := p.Read(t.Context(), config.Domain{}, contract.FunctionInfo{})
			out <- sampleResult{s, err}
		})
	}
	close(start)
	waitTimer(t, clock, 100*time.Millisecond)
	clock.Advance(100 * time.Millisecond)
	done.Wait()
	first := clock.Now()
	for range workers {
		r := getSample(t, out)
		if r.err != nil || !r.s.SampleTime.Equal(first) || r.s.CPUAvailable != 6 {
			t.Fatalf("bad shared snapshot: %+v", r)
		}
	}
	if reads.Load() != 2 {
		t.Fatalf("expected one shared pair, %d reads", reads.Load())
	}
	clock.Advance(201 * time.Millisecond)
	go func() {
		s, err := p.Read(t.Context(), config.Domain{}, contract.FunctionInfo{})
		out <- sampleResult{s, err}
	}()
	waitTimer(t, clock, 100*time.Millisecond)
	clock.Advance(100 * time.Millisecond)
	r := getSample(t, out)
	if r.err != nil || !r.s.SampleTime.After(first) || reads.Load() != 4 {
		t.Fatalf("TEST-RTE-18: stale snapshot not refreshed: %+v, reads=%d", r, reads.Load())
	}
}

type gpuFunc func(context.Context) ([]resource.Device, error)

func (f gpuFunc) Devices(ctx context.Context) ([]resource.Device, error) { return f(ctx) }

func TestAdditionalResources(t *testing.T) {
	const mib = config.MiB
	r := contract.ResourceSpec{CPUCores: 1, MemoryMB: 1, GPU: contract.GPUResourceSpec{Required: true, DeviceCount: 2, MemoryMB: 4}}
	s := resource.Snapshot{CPUAvailable: 1, MemoryAvailable: mib}
	d := func(id string, mb uint64, visible bool) resource.Device {
		return resource.Device{ID: id, FreeBytes: mb * mib, Visible: visible}
	}
	for _, tt := range []struct {
		name string
		devs []resource.Device
		dim  string
		err  string
	}{
		{"two_cards_equal", []resource.Device{d("one", 4, true), d("two", 4, true)}, "", ""},
		{"total_enough", []resource.Device{d("one", 7, true), d("two", 1, true)}, "gpu", ""},
		{"duplicate", []resource.Device{d("one", 4, true), d("one", 4, true)}, "gpu", ""},
		{"hidden", []resource.Device{d("one", 4, true), d("two", 4, false)}, "gpu", ""},
		{"no_telemetry", nil, "", "read"},
		{"bad_id", []resource.Device{d("", 4, true)}, "", "parse"},
	} {
		t.Run("TEST-RTE-28/"+tt.name, func(t *testing.T) {
			s.Devices = tt.devs
			if dim, err := resource.Evaluate(r, s); dim != tt.dim || (err == nil) != (tt.err == "") || (err != nil && resource.Reason(err) != tt.err) {
				t.Fatalf("GPU admission: dim=%q err=%v", dim, err)
			}
		})
	}
	t.Run("TEST-RTE-29", func(t *testing.T) {
		clock := testclock.New()

		fn := contract.FunctionInfo{Resource: contract.ResourceSpec{CPUCores: 1, MemoryMB: 1, DiskMB: 2}}
		for _, tt := range []struct {
			name         string
			blocks, size uint64
			dim, failure string
		}{{"equal", 512, 4096, "", ""}, {"insufficient", 511, 4096, "disk", ""}, {"overflow", math.MaxUint64, 2, "", "calculation"}, {"error", 0, 0, "", "read"}} {
			t.Run(tt.name, func(t *testing.T) {
				probe := resource.NewLinuxWithSystemReaders(clock, nil, fileReader(clock, files(t, "host"), nil), func(path string) (uint64, uint64, error) {
					if path != "/task" || tt.name == "error" {
						return 0, 0, fs.ErrNotExist
					}
					return tt.blocks, tt.size, nil
				})
				primeBase(t, clock, probe)
				got, err := probe.Read(t.Context(), config.Domain{DiskPath: "/task"}, fn)
				if err == nil {
					var dim string
					dim, err = resource.Evaluate(fn.Resource, got)
					if dim != tt.dim {
						t.Fatalf("unexpected disk result %q", dim)
					}
				}
				if (err == nil) != (tt.failure == "") || (err != nil && resource.Reason(err) != tt.failure) {
					t.Fatalf("disk error %v", err)
				}
			})
		}
	})
	t.Run("TEST-RTE-30", func(t *testing.T) {
		clock := testclock.New()
		for _, tc := range []struct{ name, reason string }{{"eth0", ""}, {"eth1", "calculation"}, {"missing", "read"}} {
			host := files(t, "host")
			var networkStart time.Time
			probe := resource.NewLinuxWithSystemReaders(clock, nil, func(path string) ([]byte, error) {
				if path == "/proc/net/dev" && clock.Now().After(networkStart) {
					return []byte("eth0: 125100 0 0 0 0 0 0 0 250200 0 0 0 0 0 0 0\neth1: 50 0 0 0 0 0 0 0 200 0 0 0 0 0 0 0\n"), nil
				}
				if value, ok := host[path]; ok {
					return []byte(value), nil
				}
				return nil, fs.ErrNotExist
			}, nil)
			primeBase(t, clock, probe)
			networkStart = clock.Now()
			out := make(chan sampleResult, 1)
			fn := contract.FunctionInfo{Resource: contract.ResourceSpec{CPUCores: 1, MemoryMB: 1, NetworkMbps: 30}}
			go func() {
				got, err := probe.Read(t.Context(), config.Domain{NetworkInterface: tc.name, NetworkLimitMbps: 50}, fn)
				out <- sampleResult{got, err}
			}()
			if tc.name != "missing" {
				waitTimer(t, clock, 100*time.Millisecond)
				clock.Advance(100 * time.Millisecond)
			}
			got := getSample(t, out)
			if tc.reason != "" {
				if got.err == nil || resource.Reason(got.err) != tc.reason {
					t.Fatalf("%s: %v", tc.name, got.err)
				}
				continue
			}
			if got.err != nil || got.s.NetworkAvailable != 30 {
				t.Fatalf("network TX/RX limit: %+v", got)
			}
			if dim, err := resource.Evaluate(fn.Resource, got.s); dim != "" || err != nil {
				t.Fatalf("network equality: %q %v", dim, err)
			}
		}
	})
	t.Run("TEST-RTE-31/undeclared", func(t *testing.T) {
		clock := testclock.New()
		read := fileReader(clock, files(t, "host"), nil)
		probe := resource.NewLinuxWithSystemReaders(clock, gpuFunc(func(context.Context) ([]resource.Device, error) {
			t.Error("undeclared GPU probe")
			return nil, nil
		}), func(path string) ([]byte, error) {
			if path == "/proc/net/dev" {
				return nil, fmt.Errorf("unexpected file %s", path)
			}
			return read(path)
		}, func(string) (uint64, uint64, error) {
			return 0, 0, errors.New("unexpected disk")
		})
		base := primeBase(t, clock, probe)

		got, err := probe.Read(t.Context(), config.Domain{}, contract.FunctionInfo{})
		if err != nil || got.CPUAvailable != base.CPUAvailable || got.MemoryAvailable != base.MemoryAvailable {
			t.Fatalf("unrequested resources probed: %+v %v", got, err)
		}
	})
}

func TestLinuxEnvironment(t *testing.T) {
	l := resource.NewLinux(timing.System{}, nil)
	full, err := l.Read(t.Context(), config.Domain{}, contract.FunctionInfo{})
	if err == nil {
		if (math.IsNaN(full.CPUAvailable) || math.IsInf(full.CPUAvailable, 0)) || full.CPUAvailable < 0 || full.CPUAvailable > full.CPULimit ||
			full.MemoryAvailable > full.MemoryLimit {
			t.Fatalf("invalid live snapshot: %+v", full)
		}
		t.Logf("ENV-02 live snapshot: source=%s cpu_limit=%g cpu_used=%g cpu_available=%g memory_limit=%d memory_used=%d memory_available=%d",
			full.Source, full.CPULimit, full.CPUUsed, full.CPUAvailable, full.MemoryLimit, full.MemoryUsed, full.MemoryAvailable)
		if source := os.Getenv("PHEROMONE_EXPECT_SOURCE"); source != "" && full.Source != source {
			t.Fatalf("expected resource source %q, got %q", source, full.Source)
		}
		if raw := os.Getenv("PHEROMONE_EXPECT_CPU_LIMIT"); raw != "" {
			want, err := strconv.ParseFloat(raw, 64)
			if err != nil || full.CPULimit != want {
				t.Fatalf("expected actual effective CPU limit %s, got %g (%s)", raw, full.CPULimit, full.Source)
			}
		}
		if raw := os.Getenv("PHEROMONE_EXPECT_MEMORY_LIMIT"); raw != "" {
			want, err := strconv.ParseUint(raw, 10, 64)
			if err != nil || full.MemoryLimit != want || full.MemoryAvailable > want-min(want, full.MemoryUsed) {
				t.Fatalf("expected actual cgroup memory limit %s, got %+v", raw, full)
			}
		}
		diskPath := t.TempDir()
		disk, err := l.Read(t.Context(), config.Domain{DiskPath: diskPath}, contract.FunctionInfo{Resource: contract.ResourceSpec{DiskMB: 1}})
		if err != nil {
			t.Fatalf("ENV-02 statfs: %v", err)
		}
		t.Logf("ENV-02 statfs available bytes=%d", disk.DiskAvailable)
		_, err = l.Read(t.Context(), config.Domain{NetworkInterface: "lo", NetworkLimitMbps: 1000}, contract.FunctionInfo{Resource: contract.ResourceSpec{NetworkMbps: 1}})
		if os.Getenv("PHEROMONE_EXPECT_LOOPBACK") != "" && err != nil {
			t.Fatalf("ENV-02 loopback sampling: %v", err)
		}
		t.Logf("ENV-02 loopback sampling error=%v", err)
	} else {
		if os.Getenv("PHEROMONE_EXPECT_SOURCE") != "" || os.Getenv("PHEROMONE_EXPECT_CPU_LIMIT") != "" || os.Getenv("PHEROMONE_EXPECT_MEMORY_LIMIT") != "" {
			t.Fatalf("expected constrained live cgroup: %v", err)
		}
		if resource.Reason(err) != "read" || !strings.Contains(err.Error(), "no visible mount") || full.CPUAvailable != 0 {
			t.Fatal(err)
		}
		t.Logf("ENV-02 target cgroup calculation blocked (fail-closed verified): %v", err)
	}
}

func TestBandwidthIntegerPrecision(t *testing.T) {
	clock := testclock.New()
	probe := fromFiles(clock, files(t, "host"), nil)
	primeBase(t, clock, probe)
	req := contract.FunctionInfo{Resource: contract.ResourceSpec{CPUCores: 1, MemoryMB: 1, NetworkMbps: math.MaxUint64}}
	out := make(chan sampleResult, 1)
	go func() {
		s, err := probe.Read(t.Context(), config.Domain{NetworkInterface: "eth0", NetworkLimitMbps: math.MaxUint64 - 1}, req)
		out <- sampleResult{s, err}
	}()
	waitTimer(t, clock, 100*time.Millisecond)
	clock.Advance(100 * time.Millisecond)
	result := getSample(t, out)
	if result.err != nil {
		t.Fatal(result.err)
	}
	if dim, err := resource.Evaluate(req.Resource, result.s); err != nil || dim != "network" {
		t.Fatalf("one Mbps missing at uint64 max: dimension=%q error=%v", dim, err)
	}
}

func TestOptionalSamplingRefreshesAgedBase(t *testing.T) {
	clock := testclock.New()
	probe := fromFiles(clock, files(t, "host"), nil)
	primeBase(t, clock, probe)
	clock.Advance(199 * time.Millisecond)
	req := contract.FunctionInfo{Resource: contract.ResourceSpec{CPUCores: 1, MemoryMB: 1, NetworkMbps: 1}}
	out := make(chan sampleResult, 1)
	go func() {
		s, err := probe.Read(t.Context(), config.Domain{NetworkInterface: "eth0", NetworkLimitMbps: 10}, req)
		out <- sampleResult{s, err}
	}()
	waitTimer(t, clock, 100*time.Millisecond)
	clock.Advance(100 * time.Millisecond)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := clock.WaitTimer(ctx, clock.Now().Add(100*time.Millisecond)); err != nil {
		select {
		case result := <-out:
			t.Fatalf("expired base reused after network sampling: %+v", result)
		default:
			t.Fatal(err)
		}
	}
	clock.Advance(100 * time.Millisecond)
	result := getSample(t, out)
	if result.err != nil || result.s.CPUAvailable != 6 || !result.s.SampleTime.Equal(clock.Now()) {
		t.Fatalf("base was not refreshed: %+v", result)
	}
}

func primeBase(t *testing.T, clock *testclock.Clock, probe *resource.Linux) resource.Snapshot {
	t.Helper()
	out := make(chan sampleResult, 1)
	go func() {
		s, err := probe.Read(t.Context(), config.Domain{}, contract.FunctionInfo{})
		out <- sampleResult{s, err}
	}()
	waitTimer(t, clock, 100*time.Millisecond)
	clock.Advance(100 * time.Millisecond)
	result := getSample(t, out)
	if result.err != nil {
		t.Fatal(result.err)
	}
	return result.s
}

func TestLinuxResourceCompatibility(t *testing.T) {
	const mib = config.MiB
	v1Files := files(t, "v1")
	tests := []struct {
		name, fixture  string
		change         func(map[string]string)
		after          map[string]string
		cpu, available float64
		memory         uint64
		failure        string
	}{
		{
			name: "host without cgroup filesystem", fixture: "host",
			change: func(f map[string]string) {
				delete(f, "/proc/self/cgroup")
				f["/proc/filesystems"] = "nodev\tproc\nnodev\ttmpfs\n"
			},
			cpu: 6, available: 6, memory: 4096 * mib,
		},
		{
			name: "missing membership on cgroup capable kernel", fixture: "host",
			change: func(f map[string]string) {
				delete(f, "/proc/self/cgroup")
				f["/proc/filesystems"] = "nodev\tproc\nnodev\tcgroup2\n"
			},
			failure: "read",
		},
		{
			name: "older procfs without MemAvailable", fixture: "host",
			change: func(f map[string]string) {
				f["/proc/meminfo"] = "MemTotal: 8388608 kB\nMemFree: 1048576 kB\nCached: 4194304 kB\n"
			},
			cpu: 6, available: 6, memory: 1024 * mib,
		},
		{
			name: "invalid MemAvailable cannot fall back", fixture: "host",
			change: func(f map[string]string) {
				f["/proc/meminfo"] = "MemTotal: 8388608 kB\nMemFree: 1048576 kB\nMemAvailable: bad\n"
			},
			failure: "parse",
		},
		{
			name: "same CPU count with different affinity", fixture: "host",
			change:  func(f map[string]string) { f["/proc/self/status"] = "Cpus_allowed_list: 0-2\n" },
			after:   map[string]string{"/proc/self/status": "Cpus_allowed_list: 3-5\n"},
			failure: "calculation",
		},
		{
			name: "v1 named hierarchy only", fixture: "host",
			change: func(f map[string]string) { f["/proc/self/cgroup"] = "1:name=systemd:/service\n" },
			cpu:    6, available: 6, memory: 4096 * mib,
		},
		{
			name: "v1 memory only", fixture: "v1",
			change: func(f map[string]string) { f["/proc/self/cgroup"] = "3:memory:/svc/worker\n" },
			cpu:    6, available: 6, memory: 256 * mib,
		},
		{
			name: "v1 split accounting and shadowed mount", fixture: "v1",
			change: func(f map[string]string) {
				f["/proc/self/cgroup"] = "2:cpu:/svc/worker\n3:memory:/svc/worker\n4:cpuset:/svc/worker\n5:cpuacct:/svc/worker\n"
				f["/proc/self/mountinfo"] = strings.ReplaceAll(f["/proc/self/mountinfo"], "rw,cpu,cpuacct", "rw,cpu")
				f["/proc/self/mountinfo"] += "34 20 0:13 / /cg/acct rw - cgroup cgroup rw,cpuacct\n35 34 0:13 /svc /cg/acct ro - cgroup cgroup rw,cpuacct\n"
				f["/cg/acct/cpuacct.usage"] = "300000000"
				f["/cg/acct/worker/cpuacct.usage"] = "200000000"
			},
			after: map[string]string{"/cg/acct/cpuacct.usage": "450000000"},
			cpu:   4, available: 2.5, memory: 256 * mib,
		},
		{
			name: "v1 quota without accounting", fixture: "v1",
			change:  func(f map[string]string) { f["/proc/self/cgroup"] = "2:cpu:/svc/worker\n3:memory:/svc/worker\n" },
			failure: "read",
		},
		{
			name: "v1 cpuset without controller prefixes", fixture: "v1",
			change: func(f map[string]string) {
				f["/proc/self/mountinfo"] = strings.ReplaceAll(f["/proc/self/mountinfo"], "rw,cpuset", "rw,cpuset,noprefix")
				delete(f, "/cg/set/svc/worker/cpuset.effective_cpus")
				f["/cg/set/svc/worker/cpus"] = "0-2"
			},
			cpu: 3, available: 3, memory: 256 * mib,
		},
		{
			name: "mixed v1 CPU and v2 memory", fixture: "v2",
			change: func(f map[string]string) {
				v1 := v1Files
				f["/proc/self/cgroup"] += "2:cpu,cpuacct:/svc/worker\n"
				f["/proc/self/mountinfo"] += v1["/proc/self/mountinfo"]
				for name, value := range v1 {
					if strings.HasPrefix(name, "/cg/cpu/") {
						f[name] = value
					}
				}
				// CPU is attached to v1, so v2 CPU files are not needed.
				delete(f, "/cg/service/cpu.max")
				delete(f, "/cg/service/worker/cpu.max")
			},
			after: map[string]string{"/cg/cpu/svc/cpuacct.usage": "450000000"},
			cpu:   4, available: 2.5, memory: 256 * mib,
		},
		{
			name: "v2 mount with root outside current namespace", fixture: "v2",
			change: func(f map[string]string) {
				f["/proc/self/mountinfo"] += "34 20 0:9 /.. /hidden rw - cgroup2 cgroup rw\n"
			},
			after: map[string]string{"/cg/service/cpu.stat": "usage_usec 450000"},
			cpu:   4, available: 2.5, memory: 256 * mib,
		},
		{
			name: "v2 leaf without controllers retains ancestor limits", fixture: "v2",
			change: func(f map[string]string) {
				delete(f, "/cg/service/worker/cpu.max")
				delete(f, "/cg/service/worker/memory.max")
				f["/cg/service/cgroup.subtree_control"] = "pids\n"
				f["/cg/service/worker/cgroup.type"] = "domain\n"
			},
			after: map[string]string{"/cg/service/cpu.stat": "usage_usec 450000"},
			cpu:   4, available: 2.5, memory: 256 * mib,
		},
		{
			name: "v2 private root lacks limit and hides parent", fixture: "v2",
			change: func(f map[string]string) {
				f["/proc/self/cgroup"] = "0::/\n"
				f["/cg/cgroup.type"] = "domain\n"
			},
			failure: "read",
		},
		{
			name: "v2 enabled controller has missing quota", fixture: "v2",
			change: func(f map[string]string) {
				delete(f, "/cg/service/worker/cpu.max")
				f["/cg/service/cgroup.subtree_control"] = "cpu pids\n"
				f["/cg/service/worker/cgroup.type"] = "domain\n"
			},
			failure: "read",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := files(t, tt.fixture)
			tt.change(input)
			clock := testclock.New()
			probe := fromFiles(clock, input, tt.after)
			switch tt.failure {
			case "":
				got := primeBase(t, clock, probe)
				if got.CPULimit != tt.cpu || got.CPUAvailable != tt.available || got.MemoryAvailable != tt.memory {
					t.Fatalf("unexpected host/cgroup limits: %+v", got)
				}
			case "calculation":
				out := make(chan sampleResult, 1)
				go func() {
					got, err := probe.Read(t.Context(), config.Domain{}, contract.FunctionInfo{})
					out <- sampleResult{got, err}
				}()
				waitTimer(t, clock, 100*time.Millisecond)
				clock.Advance(100 * time.Millisecond)
				got := getSample(t, out)
				if got.err == nil || resource.Reason(got.err) != tt.failure || got.s.CPUAvailable != 0 {
					t.Fatalf("expected %s failure, got %+v", tt.failure, got)
				}
			default:
				got, err := probe.Read(t.Context(), config.Domain{}, contract.FunctionInfo{})
				if err == nil || resource.Reason(err) != tt.failure || got.CPUAvailable != 0 {
					t.Fatalf("expected %s failure, got %+v, %v", tt.failure, got, err)
				}
			}
		})
	}
}

func TestNamespaceResourceInputs(t *testing.T) {
	t.Run("duplicate network record", func(t *testing.T) {
		clock := testclock.New()
		input := files(t, "host")
		input["/proc/net/dev"] += "eth0: 100 0 0 0 0 0 0 0 200 0 0 0 0 0 0 0\n"
		probe := fromFiles(clock, input, nil)
		primeBase(t, clock, probe)
		fn := contract.FunctionInfo{Resource: contract.ResourceSpec{NetworkMbps: 1}}
		_, err := probe.Read(t.Context(), config.Domain{NetworkInterface: "eth0", NetworkLimitMbps: 10}, fn)
		if err == nil || resource.Reason(err) != "parse" {
			t.Fatalf("expected invalid per-interface counters, got %v", err)
		}
	})
	t.Run("requested GPU without adapter", func(t *testing.T) {
		clock := testclock.New()
		probe := fromFiles(clock, files(t, "host"), nil)
		primeBase(t, clock, probe)
		fn := contract.FunctionInfo{Resource: contract.ResourceSpec{GPU: contract.GPUResourceSpec{Required: true}}}
		_, err := probe.Read(t.Context(), config.Domain{}, fn)
		if err == nil || resource.Reason(err) != "read" {
			t.Fatalf("expected unavailable GPU telemetry, got %v", err)
		}
	})
}
