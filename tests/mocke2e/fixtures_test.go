//go:build linux

package mocke2e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"triggerEngine/api"
	"triggerEngine/internal/config"
	"triggerEngine/internal/engine"
	"triggerEngine/internal/resource"
	"triggerEngine/internal/wiring"
	"triggerEngine/tests/testclock"
)

type poolAttempt struct {
	task   api.TaskInvocation
	result api.EnqueueResult
}

type execution struct {
	task  api.TaskInvocation
	bytes string
	err   error
}

type fakePool struct {
	mu       sync.Mutex
	script   map[string][]api.EnqueueResult
	calls    map[string]int
	attempts []poolAttempt
	queue    chan api.TaskInvocation
	output   chan execution
	worker   sync.WaitGroup
	execute  map[[2]string]func(api.TaskInvocation) (string, error)
}

var _ api.TaskPool = (*fakePool)(nil)

func newPool(capacity, outputs int) *fakePool {
	return &fakePool{
		queue: make(chan api.TaskInvocation, capacity), output: make(chan execution, outputs),
		calls: make(map[string]int), script: make(map[string][]api.EnqueueResult),
		execute: make(map[[2]string]func(api.TaskInvocation) (string, error)),
	}
}

func (p *fakePool) TryEnqueue(task api.TaskInvocation) api.EnqueueResult {
	p.mu.Lock()
	defer p.mu.Unlock()
	id := task.Function.FuncName
	attempt := p.calls[id]
	p.calls[id]++
	result := api.EnqueueAccepted
	if attempt < len(p.script[id]) {
		result = p.script[id][attempt]
	}
	task.Args = slices.Clone(task.Args)
	if result == api.EnqueueAccepted {
		select {
		case p.queue <- task:
		default:
			result = api.EnqueueNoCapacity
		}
	}
	// Audit metadata is independent of the queue and the producer's slice.
	task.Args = slices.Clone(task.Args)
	p.attempts = append(p.attempts, poolAttempt{task, result})
	return result
}

func (p *fakePool) start() {
	p.worker.Go(func() {
		for task := range p.queue {
			f := p.execute[[2]string{task.Function.SOName, task.Function.FuncName}]
			if f == nil {
				p.output <- execution{task: task, err: errors.New("unknown SO/function")}
				continue
			}
			bytes, err := f(task)
			p.output <- execution{task, bytes, err}
		}
	})
}

func (p *fakePool) close() {
	close(p.queue)
	p.worker.Wait()
}

func (p *fakePool) audit() []poolAttempt {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.attempts)
}

type mockSDK struct {
	mu        sync.Mutex
	mem       [][]byte
	committed map[api.DataReadyEvent]api.TaskArgument
	lengths   map[unsafe.Pointer]int
}

func (s *mockSDK) commit(event api.DataReadyEvent, source api.FunctionIdentity, data string) (api.TaskArgument, error) {
	s.mu.Lock()
	a, seen := s.committed[event]
	if seen {
		if string(unsafe.Slice((*byte)(a.DataAddr), s.lengths[a.DataAddr])) != data {
			s.mu.Unlock()
			return api.TaskArgument{}, errors.New("attempt to overwrite immutable object")
		}
	} else {
		buf, err := syscall.Mmap(-1, 0, max(1, len(data)), syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_PRIVATE|syscall.MAP_ANON)
		if err != nil {
			s.mu.Unlock()
			return api.TaskArgument{}, err
		}
		copy(buf, data)
		s.mem = append(s.mem, buf)
		a = api.TaskArgument{Key: event.Key, DataAddr: unsafe.Pointer(&buf[0])}
		s.committed[event] = a
		s.lengths[a.DataAddr] = len(data)
	}
	s.mu.Unlock()
	return a, api.EventTriggerNotify(event, source, a.DataAddr)
}

func (s *mockSDK) close(t *testing.T) {
	for _, b := range s.mem {
		if err := syscall.Munmap(b); err != nil {
			t.Error(err)
		}
	}
}

// readObject uses the test SDK's own object metadata to decode the borrowed address.
func (s *mockSDK) readObject(addr unsafe.Pointer) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	length, ok := s.lengths[addr]
	if !ok {
		return "", errors.New("unknown object address")
	}
	return string(unsafe.Slice((*byte)(addr), length)), nil
}

func (s *mockSDK) logicalInput(task api.TaskInvocation) (string, error) {
	if len(task.Args) != 1 || task.Args[0].Key == "" {
		return "", errors.New("incorrect argument count or missing key")
	}
	a := task.Args[0]
	switch task.InputMode {
	case api.InputKeyOnly:
		return a.Key, nil
	case api.InputValue:
		return s.readObject(a.DataAddr)
	case api.InputKeyValue:
		value, err := s.readObject(a.DataAddr)
		return a.Key + "=" + value, err
	}
	return "", errors.New("invalid input mode")
}

type probeStep struct {
	s resource.Snapshot
	e error
}

var (
	enough       = probeStep{s: resource.Snapshot{CPUAvailable: 8, MemoryAvailable: 16 * config.MiB}}
	lowCPU       = probeStep{s: resource.Snapshot{CPUAvailable: 0.5, MemoryAvailable: 16 * config.MiB}}
	lowMem       = probeStep{s: resource.Snapshot{CPUAvailable: 8, MemoryAvailable: config.MiB / 2}}
	probeRead    = probeStep{e: resource.ReadFailure(errors.New("fixture cannot read resource file"))}
	probeParse   = probeStep{e: resource.ParseFailure(errors.New("fixture malformed sample"))}
	probeCompute = probeStep{e: resource.ComputeFailure(errors.New("fixture accounting counter moved backward"))}
)

type probeScript struct {
	mu     sync.Mutex
	steps  map[string][]probeStep
	calls  map[string]int
	onRead func(string, int)
}

func (p *probeScript) Read(ctx context.Context, domain config.Domain, fn api.FunctionInfo) (resource.Snapshot, error) {
	id := fn.FuncName
	p.mu.Lock()
	i := p.calls[id]
	p.calls[id]++
	seq := p.steps[id]
	p.mu.Unlock()
	if p.onRead != nil {
		p.onRead(id, i)
	}
	if len(seq) == 0 {
		return enough.s, nil
	}
	step := seq[min(i, len(seq)-1)]
	return step.s, step.e
}

func (p *probeScript) count(id string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls[id]
}

type logSink struct {
	mu   sync.Mutex
	seen []map[string]any
	wake chan struct{}
}

func (l *logSink) Write(body []byte) (int, error) {
	var rec map[string]any
	if err := json.Unmarshal(body, &rec); err != nil {
		return 0, err
	}
	l.mu.Lock()
	l.seen = append(l.seen, rec)
	l.mu.Unlock()
	select {
	case l.wake <- struct{}{}:
	default:
	}
	return len(body), nil
}

func (l *logSink) await(t *testing.T, message, target string, occurrence int) map[string]any {
	t.Helper()
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	for {
		l.mu.Lock()
		n := 0
		for _, rec := range l.seen {
			if rec["msg"] == message && rec["func_name"] == target {
				n++
				if n == occurrence {
					l.mu.Unlock()
					return rec
				}
			}
		}
		l.mu.Unlock()
		select {
		case <-l.wake:
		case <-timeout.C:
			t.Fatalf("no log %q for %q (#%d)", message, target, occurrence)
		}
	}
}

type harness struct {
	t     *testing.T
	p     *fakePool
	sdk   mockSDK
	probe *probeScript
	clock *testclock.Clock
	log   *logSink
	r     *engine.Runtime
}

func setup(t *testing.T, queue, pending int) *harness {
	t.Helper()
	h := &harness{t: t, p: newPool(queue, queue+16), clock: testclock.New(),
		sdk:   mockSDK{committed: make(map[api.DataReadyEvent]api.TaskArgument), lengths: make(map[unsafe.Pointer]int)},
		probe: &probeScript{steps: make(map[string][]probeStep), calls: make(map[string]int)},
		log:   &logSink{wake: make(chan struct{}, 1)},
	}
	h.r = engine.New(engine.Dependencies{Clock: h.clock, DelayLimit: pending, Pool: h.p, Probe: h.probe,
		Logger: slog.New(slog.NewJSONHandler(h.log, &slog.HandlerOptions{Level: slog.LevelDebug}))})
	if err := wiring.Install(h.r); err != nil {
		t.Fatal(err)
	}
	for _, id := range [][2]string{{"first.so", "A"}, {"second.so", "B"}, {"second.so", "C"}, {"other.so", "A"}} {
		h.p.execute[id] = func(task api.TaskInvocation) (string, error) {
			in, err := h.sdk.logicalInput(task)
			return id[1] + "[" + in + "]", err
		}
	}
	t.Cleanup(func() {
		h.closeRuntime()
		h.p.close()
		h.sdk.close(t)
	})
	return h
}

func (h *harness) closeRuntime() {
	h.r.Close()
	if h.clock.Pending() != 0 {
		h.t.Error("timer remained after runtime close")
	}
}

func appBody(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("testdata/app.json")
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func put(t *testing.T, body string) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "app.json")
	if err := os.WriteFile(file, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return file
}

func (h *harness) init(enabled bool, changes ...string) string {
	h.t.Helper()
	body := appBody(h.t)
	if !enabled {
		body = strings.Replace(body, "\"enabled\":true", "\"enabled\":false", 1)
	}
	body = strings.NewReplacer(changes...).Replace(body)
	path := put(h.t, body)
	checkCode(h.t, api.TriggerInit(path), 0)
	return path
}

func checkCode(t *testing.T, err error, code api.StatusCode) {
	t.Helper()
	if code == 0 && err == nil {
		return
	}
	var e *api.APIError
	if code == 0 || !errors.As(err, &e) || e.StatusCode != code || e.Msg == "" {
		t.Fatalf("want code=%d got %v", code, err)
	}
}

func (h *harness) send(bucket, key string, source api.FunctionIdentity, data string) api.TaskArgument {
	h.t.Helper()
	a, err := h.sdk.commit(api.DataReadyEvent{Bucket: bucket, Key: key, SessionID: "s1"}, source, data)
	checkCode(h.t, err, 0)
	return a
}

func (h *harness) expect(target string, mode api.InputMode, a api.TaskArgument, bytes string) {
	h.t.Helper()
	select {
	case result := <-h.p.output:
		expectedSO := map[string]string{"A": "first.so", "B": "second.so", "C": "second.so"}[target]
		wantCPU, wantMem := 1.0, uint64(1)
		if target == "B" {
			wantCPU, wantMem = 2, 2
		}
		if result.err != nil || result.bytes != bytes || result.task.SessionID != "s1" ||
			result.task.Function.FuncName != target || result.task.Function.SOName != expectedSO ||
			result.task.Function.Resource != (api.ResourceSpec{CPUCores: wantCPU, MemoryMB: wantMem}) ||
			result.task.InputMode != mode || !slices.Equal(result.task.Args, []api.TaskArgument{a}) {
			h.t.Fatalf("output want %s/%d/%q/%+v, got %+v", target, mode, bytes, a, result)
		}
	case <-time.After(5 * time.Second):
		h.t.Fatalf("no asynchronous output for %s", target)
	}
}

func (h *harness) count(target string, calls, accepted int) {
	h.t.Helper()
	gotCalls, gotAccepted := 0, 0
	for _, a := range h.p.audit() {
		if a.task.Function.FuncName == target {
			gotCalls++
			if a.result == api.EnqueueAccepted {
				gotAccepted++
			}
		}
	}
	if gotCalls != calls || gotAccepted != accepted {
		h.t.Fatalf("%s attempts=%d accepted=%d, want %d/%d", target, gotCalls, gotAccepted, calls, accepted)
	}
}

func (h *harness) advance(delta time.Duration) {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(h.t.Context(), 5*time.Second)
	defer cancel()
	if err := h.clock.WaitTimer(ctx, h.clock.Now().Add(delta)); err != nil {
		h.t.Fatalf("missing retry timer at +%s: %v", delta, err)
	}
	h.clock.Advance(delta)
}

func (h *harness) checkFinalCount() {
	h.t.Helper()
	if n := len(h.p.output); n != 0 {
		h.t.Fatalf("unexpected %d extra executions", n)
	}
}

func scenarioID(n int, variant string) string {
	return fmt.Sprintf("MOCK-E2E-%02d%s", n, variant)
}
