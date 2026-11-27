package engine_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
	"time"
	"unsafe"

	"triggerEngine/internal/config"
	"triggerEngine/internal/contract"
	"triggerEngine/internal/engine"
	"triggerEngine/internal/resource"
	"triggerEngine/tests/testclock"
)

type probeFunc func(context.Context, config.Domain, contract.FunctionInfo) (resource.Snapshot, error)

func (f probeFunc) Read(ctx context.Context, d config.Domain, fn contract.FunctionInfo) (resource.Snapshot, error) {
	return f(ctx, d, fn)
}

type poolFunc func(contract.TaskInvocation) contract.EnqueueResult

func (f poolFunc) TryEnqueue(task contract.TaskInvocation) contract.EnqueueResult { return f(task) }

func assertCode(t testing.TB, err error, want contract.StatusCode) {
	t.Helper()
	if want == 0 {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return
	}
	var apiErr *contract.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != want || apiErr.Msg == "" {
		t.Fatalf("error=%v, expected *APIError %d", err, want)
	}
}

const samplePath = "../config/testdata/app.json"

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "app.json")
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestInitialization(t *testing.T) {
	appBody, err := os.ReadFile(samplePath)
	if err != nil {
		t.Fatal(err)
	}
	absSample, err := filepath.Abs(samplePath)
	if err != nil {
		t.Fatal(err)
	}
	read := make(chan struct{})
	release := make(chan struct{})
	r := engine.New(engine.Dependencies{ReadConfig: func(path string) ([]byte, error) {
		if !filepath.IsAbs(path) {
			return nil, errors.New("relative path")
		}
		if path != absSample {
			return os.ReadFile(path)
		}
		close(read)
		<-release
		return appBody, nil
	}})
	t.Cleanup(r.Close)
	notice := contract.DataReadyEvent{Bucket: "b_result", Key: "k", SessionID: "s"}
	assertCode(t, r.Notify(notice, contract.FunctionIdentity{}, unsafe.Pointer(new(byte))), contract.ErrNotInitialized)
	assertCode(t, r.Init(""), contract.ErrInvalidArgument)
	assertCode(t, r.Init("\x00"), contract.ErrInvalidArgument)
	result := make(chan error, 1)
	go func() { result <- r.Init(samplePath) }()
	<-read
	assertCode(t, r.Notify(notice, contract.FunctionIdentity{}, unsafe.Pointer(new(byte))), contract.ErrNotInitialized)
	assertCode(t, r.Init(samplePath), contract.ErrInvalidState)
	close(release)
	assertCode(t, <-result, 0)
	assertCode(t, r.Notify(notice, contract.FunctionIdentity{SOName: "unknown.so", FuncName: "unknown"}, unsafe.Pointer(new(byte))), contract.ErrInvalidArgument)
	assertCode(t, r.Notify(notice, contract.FunctionIdentity{}, nil), contract.ErrInvalidArgument)
	// Invalid events must leave initialization open. Use a distinct app to avoid duplicate-name validation.
	other := strings.NewReplacer("smc_demo", "other", "b_", "x_").Replace(string(appBody))
	assertCode(t, r.Init(writeConfig(t, other)), 0)
	assertCode(t, r.Notify(notice, contract.FunctionIdentity{}, unsafe.Pointer(new(byte))), 0)
	assertCode(t, r.Init(samplePath), contract.ErrInvalidState)
}

func TestPublication(t *testing.T) {
	raw, err := os.ReadFile(samplePath)
	if err != nil {
		t.Fatal(err)
	}
	var tasks []contract.TaskInvocation
	r := engine.New(engine.Dependencies{
		Pool: poolFunc(func(task contract.TaskInvocation) contract.EnqueueResult {
			tasks = append(tasks, task)
			return contract.EnqueueAccepted
		}),
		Probe: probeFunc(func(context.Context, config.Domain, contract.FunctionInfo) (resource.Snapshot, error) {
			return resource.Snapshot{CPUAvailable: 10, MemoryAvailable: 1 << 30}, nil
		}),
	})
	t.Cleanup(r.Close)
	tmp := t.TempDir()
	put := func(name, data string) string {
		t.Helper()
		file := filepath.Join(tmp, name)
		if err := os.WriteFile(file, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		return file
	}
	assertCode(t, r.Init(filepath.Join(tmp, "absent.json")), contract.ErrConfigRead)
	assertCode(t, r.Init(put("invalid.json", string(raw)+"{}")), contract.ErrInvalidConfig)
	assertCode(t, r.Notify(contract.DataReadyEvent{}, contract.FunctionIdentity{}, unsafe.Pointer(new(byte))), contract.ErrNotInitialized)
	assertCode(t, r.Init(samplePath), 0)

	other := strings.NewReplacer("smc_demo", "other", "b_", "x_", "producer.so", "other.so").Replace(string(raw))
	conflicts := []struct{ id, config string }{
		{"TEST-APP-14", strings.Replace(other, "x_result", "b_result", 1)},
		{"TEST-APP-16/enabled", strings.Replace(other, "true", "false", 1)},
		{"TEST-APP-16/retry_interval", strings.Replace(other, "10,", "11,", 1)},
		{"TEST-APP-16/max_wait", strings.Replace(other, "1000", "1001", 1)},
		{"TEST-APP-09/shared_resource", strings.Replace(other, "256", "257", 1)},
		{"TEST-API-13/invalid_rule", strings.Replace(other, "IMMEDIATE", "BY_TIME", 1)},
		{"TEST-API-13/duplicate_app", strings.Replace(other, "other", "smc_demo", 1)},
	}
	for _, tt := range conflicts {
		t.Run(tt.id, func(t *testing.T) {
			assertCode(t, r.Init(put("conflict.json", tt.config)), contract.ErrInvalidConfig)
			assertCode(t, r.Notify(contract.DataReadyEvent{Bucket: "x_input", Key: "k", SessionID: "s"}, contract.FunctionIdentity{}, unsafe.Pointer(new(byte))), contract.ErrInvalidArgument)
		})
	}
	assertCode(t, r.Init(put("other.json", other)), 0)
	for _, name := range []string{"b_input", "x_input"} {
		assertCode(t, r.Notify(contract.DataReadyEvent{Bucket: name, Key: "k", SessionID: "s"}, contract.FunctionIdentity{}, unsafe.Pointer(new(byte))), 0)
	}
	if len(tasks) != 2 || tasks[0].Function.SOName != "producer.so" || tasks[1].Function.SOName != "other.so" ||
		tasks[0].Function.FuncName != tasks[1].Function.FuncName {
		t.Fatalf("wrong App/function routing: %+v", tasks)
	}
}

func FuzzNotify(f *testing.F) {
	for _, chain := range [][]byte{{0, 0, 0}, {1, 0, 0}, {2, 0, 2}, {3, 0, 1}, {4, 0, 0}, {5, 0, 0}, {0, 0, 2, 0, 0, 0}} {
		f.Add(chain)
	}
	f.Fuzz(func(t *testing.T, chain []byte) {
		if len(chain) > 384 {
			return
		}
		r := engine.New(engine.Dependencies{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
			Pool: poolFunc(func(task contract.TaskInvocation) contract.EnqueueResult {
				if task.SessionID == "" || len(task.Args) != 1 || task.Args[0].Key == "" || task.Args[0].DataAddr == nil {
					t.Fatal("invalid task descriptor")
				}
				return contract.EnqueueAccepted
			}),
			Probe: probeFunc(func(context.Context, config.Domain, contract.FunctionInfo) (resource.Snapshot, error) {
				return resource.Snapshot{CPUAvailable: 10, MemoryAvailable: 1 << 30}, nil
			}),
		})
		defer r.Close()
		assertCode(t, r.Init(samplePath), 0)
		for len(chain) >= 3 {
			event := contract.DataReadyEvent{Bucket: "b_input", Key: string([]byte{chain[1]}), SessionID: "s"}
			var source contract.FunctionIdentity
			addr := unsafe.Pointer(new(byte))
			var want contract.StatusCode
			switch chain[0] % 6 {
			case 1:
				event.Bucket, want = "", contract.ErrInvalidArgument
			case 2:
				event.Key, want = "", contract.ErrInvalidArgument
			case 3:
				event.SessionID, want = "", contract.ErrInvalidArgument
			case 4:
				addr, want = nil, contract.ErrInvalidArgument
			case 5:
				source, want = contract.FunctionIdentity{SOName: "missing.so", FuncName: "missing"}, contract.ErrInvalidArgument
			}
			assertCode(t, r.Notify(event, source, addr), want)
			chain = chain[3:]
		}
	})
}

func TestIndependentRuleDescriptors(t *testing.T) {
	body, err := os.ReadFile(samplePath)
	if err != nil {
		t.Fatal(err)
	}
	var submitted []contract.TaskInvocation
	var slots []*contract.TaskArgument
	r := engine.New(engine.Dependencies{
		ReadConfig: func(string) ([]byte, error) { return body, nil },
		Pool: poolFunc(func(task contract.TaskInvocation) contract.EnqueueResult {
			slots = append(slots, &task.Args[0])
			submitted = append(submitted, task)
			return contract.EnqueueAccepted
		}),
		Probe: probeFunc(func(context.Context, config.Domain, contract.FunctionInfo) (resource.Snapshot, error) {
			return resource.Snapshot{CPUAvailable: 8, MemoryAvailable: 1 << 30}, nil
		}),
	})
	t.Cleanup(r.Close)
	assertCode(t, r.Init("app.json"), 0)
	// Both triggers address the same target and must still submit separately.
	evt := contract.DataReadyEvent{Bucket: "b_shared", Key: "ready", SessionID: "session"}
	assertCode(t, r.Notify(evt, contract.FunctionIdentity{}, unsafe.Pointer(new(byte))), 0)
	if len(submitted) != 2 || slots[0] == slots[1] ||
		submitted[0].InputMode != contract.InputValue || submitted[1].InputMode != contract.InputKeyValue {
		t.Fatalf("TEST-EVT-04/TSK-07: wrong independent tasks: %+v", submitted)
	}
}

func TestCloseInFlight(t *testing.T) {
	start := make(chan struct{})
	r := engine.New(engine.Dependencies{
		Pool: poolFunc(func(contract.TaskInvocation) contract.EnqueueResult {
			t.Error("pool called after shutdown cancelled the probe")
			return contract.EnqueueAccepted
		}),
		Probe: probeFunc(func(ctx context.Context, _ config.Domain, _ contract.FunctionInfo) (resource.Snapshot, error) {
			close(start)
			<-ctx.Done()
			return resource.Snapshot{CPUAvailable: 8, MemoryAvailable: 1 << 30}, nil
		}),
	})
	t.Cleanup(r.Close)
	assertCode(t, r.Init(samplePath), 0)
	done := make(chan error, 1)
	go func() {
		done <- r.Notify(contract.DataReadyEvent{Bucket: "b_input", Key: "k", SessionID: "s"}, contract.FunctionIdentity{}, unsafe.Pointer(new(byte)))
	}()
	<-start
	r.Close()
	assertCode(t, <-done, 0)
	assertCode(t, r.Notify(contract.DataReadyEvent{Bucket: "b_input", Key: "k", SessionID: "s"}, contract.FunctionIdentity{}, unsafe.Pointer(new(byte))), contract.ErrNotInitialized)
}

func TestStaticContract(t *testing.T) {
	eventType := reflect.TypeFor[contract.DataReadyEvent]()
	if eventType.NumField() != 3 || eventType.Field(0).Name != "Bucket" ||
		eventType.Field(1).Name != "Key" || eventType.Field(2).Name != "SessionID" {
		t.Fatal("TEST-API-01: changed DataReadyEvent fields")
	}
	poolType := reflect.TypeFor[contract.TaskPool]()
	if poolType.NumMethod() != 1 || poolType.Method(0).Name != "TryEnqueue" ||
		poolType.Method(0).Type != reflect.TypeFor[func(contract.TaskInvocation) contract.EnqueueResult]() {
		t.Fatal("TEST-API-07: changed Pool contract")
	}
	err := contract.APIError{StatusCode: contract.ErrInvalidArgument, Msg: "diagnostic"}
	out, marshalErr := json.Marshal(err)
	if string(out) != `{"status_code":1002,"msg":"diagnostic"}` || marshalErr != nil {
		t.Fatalf("changed structured error format: %s, %v", out, marshalErr)
	}
}

func FuzzDelaySequence(f *testing.F) {
	body := strings.Replace(string(mustReadConfig(f)), `"max_wait_ms": 1000`, `"max_wait_ms": 40`, 1)
	f.Add([]byte{1, 2, 3, 4, 0})
	f.Add([]byte{4, 4, 4, 4, 0})
	f.Add([]byte{1, 1, 1, 8, 0})
	f.Add([]byte{9, 5})
	f.Fuzz(func(t *testing.T, seq []byte) {
		synctest.Test(t, func(t *testing.T) {
			clock := testclock.New()
			var choice byte
			calls, accepted, probes := 0, 0, 0
			r := engine.New(engine.Dependencies{
				Clock: clock, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
				ReadConfig: func(string) ([]byte, error) { return []byte(body), nil },
				Pool: poolFunc(func(contract.TaskInvocation) contract.EnqueueResult {
					calls++
					switch choice % 6 {
					case 4:
						return contract.EnqueueNoCapacity
					case 5:
						return contract.EnqueueError
					default:
						accepted++
						return contract.EnqueueAccepted
					}
				}),
				Probe: probeFunc(func(context.Context, config.Domain, contract.FunctionInfo) (resource.Snapshot, error) {
					probes++
					switch choice % 6 {
					case 1:
						return resource.Snapshot{MemoryAvailable: 1 << 30}, nil
					case 2:
						return resource.Snapshot{}, resource.ParseFailure(errors.New("invalid sample"))
					case 3:
						return resource.Snapshot{CPUAvailable: math.NaN()}, nil
					}
					return resource.Snapshot{CPUAvailable: 8, MemoryAvailable: 1 << 30}, nil
				}),
			})
			defer r.Close()
			assertCode(t, r.Init(samplePath), 0)
			evt := contract.DataReadyEvent{Bucket: "b_input", Key: "k", SessionID: "s"}
			first := clock.Now()
			pending := false
			for i, value := range seq {
				if i > 0 && !pending {
					break
				}
				choice = value
				beforeCalls, beforeProbes := calls, probes
				if i == 0 {
					assertCode(t, r.Notify(evt, contract.FunctionIdentity{}, unsafe.Pointer(new(byte))), 0)
				} else {
					clock.Advance(time.Duration(10+value/6) * time.Millisecond)
				}
				synctest.Wait()
				expired := !clock.Now().Before(first.Add(40 * time.Millisecond))
				wantProbes, wantCalls := 0, 0
				if !expired {
					wantProbes = 1
					if choice%6 == 0 || choice%6 >= 4 {
						wantCalls = 1
					}
				}
				pending = !expired && choice%6 >= 1 && choice%6 <= 4
				wantTimers := 0
				if pending {
					wantTimers = 1
				}
				if probes-beforeProbes != wantProbes || calls-beforeCalls != wantCalls || accepted > 1 || clock.Pending() != wantTimers {
					t.Fatalf("unexpected retry at step %d: sequence=%x probes=%d pool calls=%d accepted=%d timers=%d", i, seq, probes, calls, accepted, clock.Pending())
				}
				if pending {
					next := clock.Now().Add(10 * time.Millisecond)
					deadline := first.Add(40 * time.Millisecond)
					if deadline.Before(next) {
						next = deadline
					}
					ctx, cancel := context.WithTimeout(t.Context(), time.Millisecond)
					err := clock.WaitTimer(ctx, next)
					cancel()
					if err != nil {
						t.Fatalf("retry timer not set for %s at step %d: sequence=%x", next, i, seq)
					}
				}
			}
		})
	})
}

func mustReadConfig(t testing.TB) []byte {
	t.Helper()
	body, err := os.ReadFile(samplePath)
	if err != nil {
		t.Fatal(err)
	}
	return body
}
