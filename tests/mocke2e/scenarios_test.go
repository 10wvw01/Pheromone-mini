//go:build linux

package mocke2e

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"triggerEngine/api"
	"triggerEngine/internal/config"
	"triggerEngine/internal/resource"
)

const step = 10 * time.Millisecond

type scenario struct {
	id  string
	run func(*testing.T)
}

func scenarios() []scenario {
	var cases []scenario
	add := func(n int, variant string, run func(*testing.T)) {
		cases = append(cases, scenario{scenarioID(n, variant), run})
	}
	add(1, "", func(t *testing.T) { pipeline(t, false) })
	add(2, "", byName)
	add(3, "", func(t *testing.T) { pipeline(t, true) })
	add(4, "/trigger", func(t *testing.T) { fanOut(t, false) })
	add(4, "/direct", func(t *testing.T) { fanOut(t, true) })
	add(5, "", inputModes)
	for _, data := range []struct {
		id                     int
		name, expected, reason string
		failure                probeStep
	}{
		{6, "cpu", "RESOURCE_INSUFFICIENT", "", lowCPU},
		{6, "memory", "RESOURCE_INSUFFICIENT", "", lowMem},
		{7, "read", "RESOURCE_PROBE_FAILED", "read", probeRead},
		{7, "parse", "RESOURCE_PROBE_FAILED", "parse", probeParse},
		{8, "compute", "RESOURCE_PROBE_FAILED", "calculation", probeCompute},
	} {
		for _, enabled := range []bool{false, true} {
			add(data.id, fmt.Sprintf("/%s/enabled=%t", data.name, enabled), func(t *testing.T) {
				recoverResources(t, data.failure, data.expected, data.reason, enabled)
			})
		}
	}
	for _, enabled := range []bool{false, true} {
		add(9, fmt.Sprintf("/enabled=%t", enabled), func(t *testing.T) { capacityRecovery(t, enabled) })
	}
	add(10, "/resources", func(t *testing.T) { expiry(t, false) })
	add(10, "/capacity", func(t *testing.T) { expiry(t, true) })
	add(11, "", mixedReasons)
	add(12, "/deadline_tie", deadlineTie)
	add(12, "/during_probe", deadlineDuringProbe)
	add(13, "/restore", func(t *testing.T) { partial(t, true) })
	add(13, "/expire", func(t *testing.T) { partial(t, false) })
	add(14, "", terminalFailures)
	add(15, "", delayedMetadata)
	for _, c := range []struct{ events, workers int }{{100, 1}, {100, 8}, {100, 32}, {1000, 1}, {1000, 8}, {1000, 32}} {
		add(16, fmt.Sprintf("/%d_events_%d_workers", c.events, c.workers), func(t *testing.T) { concurrent(t, c.events, c.workers) })
	}
	add(16, "/bounded_queue", concurrentBoundedPool)
	add(17, "", errorCodes)
	add(18, "", cleanup)
	add(19, "", multiApp)
	add(20, "", repeatedEvent)
	for _, check := range []struct {
		id, dimension    string
		enabled, recover bool
	}{
		{"gpu_recovery", "gpu", true, true},
		{"disk_recovery", "disk", true, true},
		{"network_rejection", "network", false, false},
		{"network_expiry", "network", true, false},
		{"telemetry_error", "telemetry", false, false},
	} {
		cases = append(cases, scenario{"TEST-RTE-31/" + check.id, func(t *testing.T) {
			optionalResources(t, check.dimension, check.enabled, check.recover)
		}})
	}
	return cases
}

func TestMockAcceptance(t *testing.T) {
	runMockScenarios(t, scenarios())
}

func runMockScenarios(t *testing.T, cases []scenario) {
	t.Helper()
	for _, s := range cases {
		t.Run(s.id, func(t *testing.T) {
			cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestMockChild$", "-test.v", "-test.timeout=20s")
			cmd.Env = append(os.Environ(), "PHEROMONE_MOCK_SCENARIO="+s.id)
			body, err := cmd.CombinedOutput()
			if err != nil || !strings.Contains(string(body), "EXECUTED "+s.id+"\n") {
				t.Fatalf("scenario %s failed: %v\n%s", s.id, err, body)
			}
			t.Logf("%s", body)
		})
	}
}

func TestMockChild(t *testing.T) {
	id := os.Getenv("PHEROMONE_MOCK_SCENARIO")
	if id == "" {
		t.Skip("subprocess entry for the Mock acceptance suites")
	}
	for _, s := range slices.Concat(scenarios(), complexScenarios(), routingScenarios()) {
		if s.id == id {
			s.run(t)
			t.Log("EXECUTED " + id)
			return
		}
	}
	t.Fatalf("unknown Mock scenario %q", id)
}

func TestFakePoolSelfCheck(t *testing.T) {
	const capacity = 8
	p := newPool(capacity, capacity)
	p.execute[[2]string{"lib.so", "A"}] = (&mockSDK{}).logicalInput
	p.script["rejected"] = []api.EnqueueResult{api.EnqueueError, api.EnqueueNoCapacity}
	for _, result := range p.script["rejected"] {
		if p.TryEnqueue(api.TaskInvocation{Function: api.FunctionInfo{FuncName: "rejected"}}) != result {
			t.Fatal("wrong scripted refusal")
		}
	}
	var workers sync.WaitGroup
	start := make(chan struct{})
	for i := range 32 {
		workers.Go(func() {
			<-start
			task := api.TaskInvocation{Function: api.FunctionInfo{SOName: "lib.so", FuncName: "A"},
				InputMode: api.InputKeyOnly, Args: []api.TaskArgument{{Key: fmt.Sprint(i)}}}
			p.TryEnqueue(task)
			task.Args[0].Key = "overwritten"
		})
	}
	close(start)
	workers.Wait()
	if len(p.queue) != capacity || len(p.output) != 0 {
		t.Fatal("atomic capacity, rejection or async boundary broken")
	}
	p.start()
	p.close()
	seen := make(map[string]bool)
	for range capacity {
		out := <-p.output
		if out.err != nil || out.bytes == "overwritten" || out.bytes == "" || seen[out.bytes] {
			t.Fatalf("bad copy/execution: %+v", out)
		}
		seen[out.bytes] = true
	}
	if len(p.output) != 0 {
		t.Fatal("refused or duplicate work reached the consumer")
	}
}

func pipeline(t *testing.T, startWorkerEarly bool) {
	h := setup(t, 8, 8)
	h.init(true)
	out := api.DataReadyEvent{Bucket: "output", Key: "mid", SessionID: "s1"}
	h.p.execute[[2]string{"first.so", "A"}] = func(task api.TaskInvocation) (string, error) {
		in, err := h.sdk.logicalInput(task)
		if err != nil {
			return "", err
		}
		value := "upper:" + strings.ToUpper(in)
		_, err = h.sdk.commit(out, api.FunctionIdentity{SOName: task.Function.SOName, FuncName: task.Function.FuncName}, value)
		return value, err
	}
	if startWorkerEarly {
		h.p.start()
	}
	a := h.send("input", "seed", api.FunctionIdentity{}, "abc")
	if !startWorkerEarly {
		if len(h.p.output) != 0 {
			t.Fatal("work executed synchronously")
		}
		h.p.start()
	}
	h.expect("A", api.InputValue, a, "upper:ABC")
	h.expect("B", api.InputKeyValue, h.sdk.committed[out], "B[mid=upper:ABC]")
	h.count("A", 1, 1)
	h.count("B", 1, 1)
	h.count("C", 0, 0)
	h.checkFinalCount()
	t.Log("final bytes=B[mid=upper:ABC]; A=1, B=1, suppressed C=0")
}

func byName(t *testing.T) {
	h := setup(t, 8, 8)
	h.init(true)
	h.p.start()
	h.send("named", "Ready", api.FunctionIdentity{}, "negative")
	h.count("A", 0, 0)
	a := h.send("named", "ready", api.FunctionIdentity{}, "ignored")
	h.expect("A", api.InputKeyOnly, a, "A[ready]")
	h.count("A", 1, 1)
}

func fanOut(t *testing.T, direct bool) {
	h := setup(t, 8, 8)
	if direct {
		h.init(true, "\"tgt_functions\":[\"B\"]", "\"tgt_functions\":[\"B\",\"C\"]")
	} else {
		h.init(true)
	}
	h.p.start()
	if direct {
		event := api.DataReadyEvent{Bucket: "fan", Key: "k", SessionID: "s1"}
		addr := unsafe.Pointer(new(byte))
		for _, source := range []api.FunctionIdentity{
			{SOName: "second.so", FuncName: "A"},
			{FuncName: "A"},
			{SOName: "first.so"},
		} {
			checkCode(t, api.EventTriggerNotify(event, source, addr), api.ErrInvalidArgument)
		}
		a := h.send("fan", "k", api.FunctionIdentity{SOName: "first.so", FuncName: "A"}, "v")
		h.expect("B", api.InputKeyValue, a, "B[k=v]")
		h.expect("C", api.InputKeyValue, a, "C[k=v]")
		h.count("A", 0, 0)
	} else {
		a := h.send("fan", "k", api.FunctionIdentity{}, "v")
		h.expect("A", api.InputValue, a, "A[v]")
		h.expect("B", api.InputValue, a, "B[v]")
		h.count("C", 0, 0)
	}
	attempts := h.p.audit()
	if len(attempts) != 2 || &attempts[0].task.Args[0] == &attempts[1].task.Args[0] {
		t.Fatal("fan-out descriptors aliased or wrong count")
	}
	h.checkFinalCount()
}

func inputModes(t *testing.T) {
	h := setup(t, 8, 8)
	extra := `{"type":"DIRECT","src_functions":["C"],"tgt_functions":["A"]},
		{"type":"DIRECT","src_functions":["C"],"tgt_functions":["B"],"trigger_option":2},
		{"type":"DIRECT","src_functions":["B"],"tgt_functions":["A"],"trigger_option":0},`
	h.init(true, "\"dependencies\": [", "\"dependencies\": ["+extra)
	h.p.start()
	a := h.send("fan", "key", api.FunctionIdentity{SOName: "second.so", FuncName: "C"}, "value")
	h.expect("A", api.InputValue, a, "A[value]")
	h.expect("B", api.InputKeyOnly, a, "B[key]")
	a = h.send("fan", "k1", api.FunctionIdentity{SOName: "first.so", FuncName: "A"}, "v1")
	h.expect("B", api.InputKeyValue, a, "B[k1=v1]")
	a = h.send("fan", "k2", api.FunctionIdentity{SOName: "second.so", FuncName: "B"}, "v2")
	h.expect("A", api.InputValue, a, "A[v2]")
	h.count("A", 2, 2)
	h.count("B", 2, 2)
	h.count("C", 0, 0)
}

func recoverResources(t *testing.T, failure probeStep, log, reason string, enabled bool) {
	h := setup(t, 8, 8)
	h.probe.steps["A"] = []probeStep{failure, enough}
	h.init(enabled)
	h.p.start()
	a := h.send("input", "k", api.FunctionIdentity{}, "x")
	first := h.log.await(t, log, "A", 1)
	if first["delay_enabled"] != enabled || (reason != "" && first["probe_failure_reason"] != reason) {
		t.Fatalf("bad resource log: %+v", first)
	}
	h.count("A", 0, 0)
	if enabled {
		h.advance(step)
		h.expect("A", api.InputValue, a, "A[x]")
		h.count("A", 1, 1)
		if h.probe.count("A") != 2 {
			t.Fatal("expected fresh resource check")
		}
	} else {
		h.clock.Advance(time.Second)
		if h.clock.Pending() != 0 || h.probe.count("A") != 1 {
			t.Fatal("disabled delay retried")
		}
		h.count("A", 0, 0)
	}
}

func capacityRecovery(t *testing.T, enabled bool) {
	h := setup(t, 1, 8)
	h.init(enabled)
	first := h.send("input", "first", api.FunctionIdentity{}, "x")
	second := h.send("input", "second", api.FunctionIdentity{}, "y")
	h.count("A", 2, 1)
	logName := "POOL_NO_CAPACITY"
	if !enabled {
		logName = "PROCESS_DISPATCH_REQUIRED"
	}
	if rec := h.log.await(t, logName, "A", 1); rec["delay_reason"] != "POOL_NO_CAPACITY" || rec["delay_enabled"] != enabled {
		t.Fatalf("invalid queue rejection: %+v", rec)
	}
	h.p.start()
	h.expect("A", api.InputValue, first, "A[x]")
	if enabled {
		h.advance(step)
		h.expect("A", api.InputValue, second, "A[y]")
		h.count("A", 3, 2)
		if h.probe.count("A") != 3 {
			t.Fatal("queue full retry skipped resource check")
		}
	} else {
		h.clock.Advance(time.Second)
		h.count("A", 2, 1)
	}
}

func expiry(t *testing.T, poolFull bool) {
	h := setup(t, 8, 8)
	reason := "RESOURCE_INSUFFICIENT"
	if poolFull {
		h.p.script["A"] = []api.EnqueueResult{api.EnqueueNoCapacity, api.EnqueueNoCapacity, api.EnqueueNoCapacity, api.EnqueueNoCapacity}
		reason = "POOL_NO_CAPACITY"
	} else {
		h.probe.steps["A"] = []probeStep{lowCPU}
	}
	h.init(true)
	h.p.start()
	h.send("input", "k", api.FunctionIdentity{}, "x")
	original := h.log.await(t, reason, "A", 1)
	for i := 2; i <= 4; i++ {
		h.advance(step)
		again := h.log.await(t, reason, "A", i)
		if original["deadline"] != again["deadline"] || original["first_deferred"] != again["first_deferred"] {
			t.Fatalf("deadline changed: %v -> %v", original, again)
		}
	}
	h.advance(step)
	h.log.await(t, "TASK_DELAY_EXPIRED", "A", 1)
	h.clock.Advance(time.Second)
	want := 0
	if poolFull {
		want = 4
	}
	h.count("A", want, 0)
	if h.probe.count("A") != 4 {
		t.Fatal("resource read on or after deadline")
	}
}

func mixedReasons(t *testing.T) {
	h := setup(t, 8, 1)
	h.probe.steps["A"] = []probeStep{enough, lowCPU, probeRead, probeCompute, enough, enough}
	h.p.script["A"] = []api.EnqueueResult{api.EnqueueNoCapacity, api.EnqueueNoCapacity}
	h.init(true, "\"max_wait_ms\":40", "\"max_wait_ms\":60")
	h.p.start()
	a := h.send("input", "k", api.FunctionIdentity{}, "x")
	first := h.log.await(t, "POOL_NO_CAPACITY", "A", 1)
	for _, event := range []struct {
		msg, reason string
		occurrence  int
	}{{"RESOURCE_INSUFFICIENT", "", 1}, {"RESOURCE_PROBE_FAILED", "read", 1},
		{"RESOURCE_PROBE_FAILED", "calculation", 2}, {"POOL_NO_CAPACITY", "", 2}} {
		h.advance(step)
		rec := h.log.await(t, event.msg, "A", event.occurrence)
		if rec["deadline"] != first["deadline"] || rec["first_deferred"] != first["first_deferred"] ||
			(event.reason != "" && event.reason != rec["probe_failure_reason"]) {
			t.Fatalf("fixed deadline/reason broken: %+v", rec)
		}
	}
	h.advance(step)
	h.expect("A", api.InputValue, a, "A[x]")
	h.count("A", 3, 1)
	if h.probe.count("A") != 6 {
		t.Fatal("unexpected resource checks")
	}
}

func deadlineTie(t *testing.T) {
	h := setup(t, 8, 8)
	h.probe.steps["A"] = []probeStep{lowCPU, enough}
	h.init(true, "\"max_wait_ms\":40", "\"max_wait_ms\":10")
	h.send("input", "k", api.FunctionIdentity{}, "x")
	h.advance(step)
	h.log.await(t, "TASK_DELAY_EXPIRED", "A", 1)
	if h.probe.count("A") != 1 {
		t.Fatal("deadline tie probed resources")
	}
	h.count("A", 0, 0)
}

func deadlineDuringProbe(t *testing.T) {
	h := setup(t, 8, 8)
	h.probe.steps["A"] = []probeStep{lowCPU, enough}
	h.probe.onRead = func(_ string, i int) {
		if i == 1 {
			h.clock.Advance(30 * time.Millisecond)
		}
	}
	h.init(true)
	h.send("input", "k", api.FunctionIdentity{}, "x")
	h.advance(step)
	h.log.await(t, "TASK_DELAY_EXPIRED", "A", 1)
	h.count("A", 0, 0)
}

func partial(t *testing.T, restore bool) {
	h := setup(t, 8, 8)
	h.probe.steps["B"] = []probeStep{lowMem}
	if restore {
		h.probe.steps["B"] = append(h.probe.steps["B"], enough)
	}
	h.init(true)
	h.p.start()
	a := h.send("fan", "k", api.FunctionIdentity{}, "x")
	h.expect("A", api.InputValue, a, "A[x]")
	h.count("A", 1, 1)
	h.count("B", 0, 0)
	if restore {
		h.advance(step)
		h.expect("B", api.InputValue, a, "B[x]")
		h.count("B", 1, 1)
	} else {
		for range 4 {
			h.advance(step)
		}
		h.log.await(t, "TASK_DELAY_EXPIRED", "B", 1)
		h.count("B", 0, 0)
	}
	h.count("A", 1, 1)
}

func terminalFailures(t *testing.T) {
	h := setup(t, 8, 1)
	h.p.script["B"] = []api.EnqueueResult{api.EnqueueError}
	h.probe.steps["A"] = []probeStep{lowCPU, lowCPU, enough}
	h.init(true)
	h.p.start()
	a := h.send("fan", "kept", api.FunctionIdentity{}, "x")
	h.log.await(t, "POOL_ERROR", "B", 1)
	h.send("input", "rejected", api.FunctionIdentity{}, "y")
	h.log.await(t, "TASK_DELAY_FULL", "A", 1)
	h.advance(step)
	h.expect("A", api.InputValue, a, "A[x]")
	for _, attempt := range h.p.audit() {
		if attempt.task.Args[0].Bucket != "fan" {
			t.Fatalf("Pool received bucket %q, want fan", attempt.task.Args[0].Bucket)
		}
	}
	h.clock.Advance(time.Second)
	h.count("A", 1, 1)
	h.count("B", 1, 0)
	h.checkFinalCount()
}

func delayedMetadata(t *testing.T) {
	h := setup(t, 8, 8)
	h.init(true)
	key := strings.Repeat("m", 1024)
	ev := api.DataReadyEvent{Bucket: "input", Key: key, SessionID: "s1"}
	a, err := h.sdk.commit(ev, api.FunctionIdentity{}, "x")
	checkCode(t, err, 0)
	ev, key = api.DataReadyEvent{}, ""
	runtime.KeepAlive(ev)
	runtime.KeepAlive(key)
	runtime.GC()
	h.p.start()
	h.expect("A", api.InputValue, a, "A[x]")
	if value, err := h.sdk.readObject(a.DataAddr); err != nil || value != "x" {
		t.Fatal("borrowed bytes changed")
	}
	h.count("A", 1, 1)
}

func concurrent(t *testing.T, n, workers int) {
	h := setup(t, n*2, 8)
	h.init(true)
	h.p.start()
	var wg sync.WaitGroup
	var sendErr = make(chan error, n)
	start := make(chan struct{})
	for worker := range workers {
		wg.Go(func() {
			<-start
			for i := worker; i < n; i += workers {
				ev := api.DataReadyEvent{Bucket: "fan", SessionID: fmt.Sprintf("s%d", i%7), Key: fmt.Sprint(i)}
				_, err := h.sdk.commit(ev, api.FunctionIdentity{}, "data-"+ev.Key)
				if err != nil {
					sendErr <- err
				}
			}
		})
	}
	close(start)
	wg.Wait()
	close(sendErr)
	for err := range sendErr {
		t.Fatal(err)
	}
	got := make(map[[2]string]bool, n*2)
	watchdog := time.NewTimer(5 * time.Second)
	defer watchdog.Stop()
	for range n * 2 {
		select {
		case e := <-h.p.output:
			id := e.task.Function.FuncName
			if len(e.task.Args) != 1 || (id != "A" && id != "B") || e.err != nil {
				t.Fatalf("wrong target or descriptor: %+v", e)
			}
			arg := e.task.Args[0]
			pair := [2]string{id, arg.Key}
			ev := api.DataReadyEvent{Bucket: arg.Bucket, SessionID: arg.SessionID, Key: arg.Key}
			if got[pair] || h.sdk.committed[ev] != arg || e.bytes != id+"[data-"+arg.Key+"]" {
				t.Fatalf("wrong address, bytes or duplicate execution: %+v", e)
			}
			got[pair] = true
		case <-watchdog.C:
			t.Fatalf("ran %d of %d expected executions", len(got), n*2)
		}
	}
	h.count("A", n, n)
	h.count("B", n, n)
	h.checkFinalCount()
	t.Logf("source events=%d senders=%d executions=%d; distinct target/event pairs", n, workers, n*2)
}

func concurrentBoundedPool(t *testing.T) {
	const capacity, senders = 8, 32
	h := setup(t, capacity, 8)
	h.init(false)
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make(chan error, senders)
	for i := range senders {
		wg.Go(func() {
			<-start
			event := api.DataReadyEvent{Bucket: "input", Key: fmt.Sprint(i), SessionID: "s1"}
			_, err := h.sdk.commit(event, api.FunctionIdentity{}, "data-"+event.Key)
			errs <- err
		})
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		checkCode(t, err, 0)
	}
	h.count("A", senders, capacity)
	h.log.await(t, "PROCESS_DISPATCH_REQUIRED", "A", senders-capacity)
	h.clock.Advance(time.Second)
	if h.clock.Pending() != 0 || h.probe.count("A") != senders {
		t.Fatal("concurrent rejection unexpectedly retried")
	}
	h.p.start()
	accepted := make(map[string]api.TaskArgument, capacity)
	for _, attempt := range h.p.audit() {
		if attempt.result == api.EnqueueAccepted {
			a := attempt.task.Args[0]
			if _, duplicate := accepted[a.Key]; duplicate {
				t.Fatalf("duplicate acceptance for %s", a.Key)
			}
			accepted[a.Key] = a
		}
	}
	watchdog := time.NewTimer(5 * time.Second)
	defer watchdog.Stop()
	for range capacity {
		select {
		case result := <-h.p.output:
			if result.err != nil || len(result.task.Args) != 1 {
				t.Fatalf("bad asynchronous result: %+v", result)
			}
			arg := result.task.Args[0]
			if accepted[arg.Key] != arg || result.bytes != "A[data-"+arg.Key+"]" {
				t.Fatalf("duplicate or invalid execution: %+v", result)
			}
			delete(accepted, arg.Key)
		case <-watchdog.C:
			t.Fatal("accepted tasks did not finish")
		}
	}
	if len(accepted) != 0 {
		t.Fatalf("unconsumed tasks: %v", accepted)
	}
	h.checkFinalCount()
	t.Log("32 concurrent notifications to capacity 8: 8 accepted/consumed, 24 terminal rejections")
}

func errorCodes(t *testing.T) {
	h := setup(t, 8, 8)
	body := appBody(t)
	ev := api.DataReadyEvent{Bucket: "input", Key: "k", SessionID: "s1"}
	checkCode(t, api.EventTriggerNotify(ev, api.FunctionIdentity{}, unsafe.Pointer(new(byte))), api.ErrNotInitialized)
	checkCode(t, api.TriggerInit(""), api.ErrInvalidArgument)
	checkCode(t, api.TriggerInit("missing.json"), api.ErrConfigRead)
	checkCode(t, api.TriggerInit(put(t, "[]")), api.ErrInvalidConfig)
	checkCode(t, api.TriggerInit(put(t, strings.Replace(body, "40", "9223372036855", 1))), api.ErrInvalidConfig)
	checkCode(t, api.EventTriggerNotify(ev, api.FunctionIdentity{}, unsafe.Pointer(new(byte))), api.ErrNotInitialized)
	path := h.init(true)
	conflict := strings.NewReplacer("\"e2e\"", "\"other\"", "\"enabled\":true", "\"enabled\":false").Replace(body)
	checkCode(t, api.TriggerInit(put(t, conflict)), api.ErrInvalidConfig)
	checkCode(t, api.EventTriggerNotify(ev, api.FunctionIdentity{}, nil), api.ErrInvalidArgument)
	checkCode(t, api.EventTriggerNotify(ev, api.FunctionIdentity{SOName: "unknown.so", FuncName: "unknown"}, unsafe.Pointer(new(byte))), api.ErrInvalidArgument)
	ev.Key = ""
	checkCode(t, api.EventTriggerNotify(ev, api.FunctionIdentity{}, unsafe.Pointer(new(byte))), api.ErrInvalidArgument)
	ev.Bucket, ev.Key = "missing", "k"
	checkCode(t, api.EventTriggerNotify(ev, api.FunctionIdentity{}, unsafe.Pointer(new(byte))), api.ErrInvalidArgument)
	h.init(true, "\"e2e\"", "\"third\"", "\"input\"", "\"in3\"", "\"named\"", "\"name3\"",
		"\"output\"", "\"out3\"", "\"fan\"", "\"fan3\"", "\"idle\"", "\"idle3\"")
	ev.Bucket = "idle"
	checkCode(t, api.EventTriggerNotify(ev, api.FunctionIdentity{}, unsafe.Pointer(new(byte))), 0)
	checkCode(t, api.TriggerInit(path), api.ErrInvalidState)
	if len(h.p.audit()) != 0 {
		t.Fatal("invalid notifications submitted work")
	}
}

func cleanup(t *testing.T) {
	h := setup(t, 8, 8)
	h.probe.steps["B"] = []probeStep{lowCPU}
	h.init(true)
	a := h.send("fan", "k", api.FunctionIdentity{}, "x")
	h.advance(step)
	h.log.await(t, "RESOURCE_INSUFFICIENT", "B", 2)
	h.closeRuntime()
	h.log.await(t, "TASK_CANCELLED", "B", 1)
	checkCode(t, api.EventTriggerNotify(api.DataReadyEvent{}, api.FunctionIdentity{}, unsafe.Pointer(new(byte))), api.ErrNotInitialized)
	h.p.start()
	h.expect("A", api.InputValue, a, "A[x]")
	h.count("B", 0, 0)
}

func multiApp(t *testing.T) {
	h := setup(t, 8, 8)
	h.init(true)
	renamer := strings.NewReplacer("\"e2e\"", "\"other\"", "first.so", "other.so",
		"\"input\"", "\"in2\"", "\"named\"", "\"name2\"", "\"output\"", "\"out2\"",
		"\"fan\"", "\"fan2\"", "\"idle\"", "\"idle2\"")
	other := renamer.Replace(appBody(t))
	other = strings.Replace(other, `"functions": [`, `"functions": [`+
		`{"so_name":"other.so","func_name":"app2_only","resource":{"cpu_cores":1,"memory_mb":1}},`, 1)
	checkCode(t, api.TriggerInit(put(t, strings.Replace(other, "idle2", "idle", 1))), api.ErrInvalidConfig)
	checkCode(t, api.TriggerInit(put(t, other)), 0)
	checkCode(t, api.EventTriggerNotify(api.DataReadyEvent{Bucket: "input", Key: "k", SessionID: "s1"}, api.FunctionIdentity{SOName: "other.so", FuncName: "app2_only"}, unsafe.Pointer(new(byte))), api.ErrInvalidArgument)
	h.p.start()
	main := h.send("input", "k", api.FunctionIdentity{}, "x")
	h.expect("A", api.InputValue, main, "A[x]")
	arg := h.send("in2", "k", api.FunctionIdentity{}, "y")
	select {
	case x := <-h.p.output:
		if x.err != nil || x.task.Function.SOName != "other.so" || x.bytes != "A[y]" ||
			!slices.Equal(x.task.Args, []api.TaskArgument{arg}) {
			t.Fatalf("wrong owning App: %+v", x)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no output from App 2")
	}
}

func repeatedEvent(t *testing.T) {
	h := setup(t, 8, 8)
	h.init(true)
	h.p.start()
	a := h.send("input", "same", api.FunctionIdentity{SOName: "second.so", FuncName: "B"}, "x")
	b := h.send("input", "same", api.FunctionIdentity{SOName: "second.so", FuncName: "B"}, "x")
	if a != b {
		t.Fatal("SDK object identity changed")
	}
	h.expect("A", api.InputValue, a, "A[x]")
	h.expect("A", api.InputValue, a, "A[x]")
	h.count("A", 2, 2)
	h.checkFinalCount()
}

func optionalResources(t *testing.T, dimension string, enabled, recover bool) {
	h := setup(t, 8, 8)
	good := enough
	good.s.Devices = []resource.Device{{ID: "g0", FreeBytes: 8 * config.MiB, Visible: true}}
	good.s.DiskAvailable = 10 * config.MiB
	good.s.NetworkAvailable = 80
	bad := good
	switch dimension {
	case "gpu":
		bad.s.Devices = []resource.Device{{ID: "g0", FreeBytes: config.MiB, Visible: true}}
	case "disk":
		bad.s.DiskAvailable = 1
	case "network":
		bad.s.NetworkAvailable = 30
	case "telemetry":
		bad = probeRead
	}
	h.probe.steps["A"] = []probeStep{bad}
	if recover {
		h.probe.steps["A"] = append(h.probe.steps["A"], good)
	}

	baseRequest := `"cpu_cores":1,"memory_mb":1`
	request := baseRequest + `,"gpu":{"required":true,"memory_mb":4,"device_count":1},"disk_mb":2,"network_mbps":40`
	baseDomain := `"scheduler": {`
	domain := baseDomain + `"resource_probe":{"disk_path":` + strconv.Quote(t.TempDir()) +
		`,"network_interface":"eth0","network_limit_mbps":100},`
	changes := []string{baseRequest, request, baseDomain, domain}
	if enabled && !recover {
		changes = append(changes, `"max_wait_ms":40`, `"max_wait_ms":10`)
	}
	h.init(enabled, changes...)
	h.p.start()
	a := h.send("input", "k", api.FunctionIdentity{}, "x")
	if dimension == "telemetry" {
		out := h.log.await(t, "RESOURCE_PROBE_FAILED", "A", 1)
		if out["probe_failure_reason"] != "read" {
			t.Fatalf("wrong telemetry reason: %+v", out)
		}
	} else if out := h.log.await(t, "RESOURCE_INSUFFICIENT", "A", 1); out["dimension"] != dimension {
		t.Fatalf("wrong optional dimension: %+v", out)
	}
	h.count("A", 0, 0)
	if enabled {
		h.advance(step)
	}
	if !recover {
		if enabled {
			h.log.await(t, "TASK_DELAY_EXPIRED", "A", 1)
		}
		if h.probe.count("A") != 1 {
			t.Fatal("unexpected probe after rejection/expiry")
		}
		h.count("A", 0, 0)
		h.checkFinalCount()
		return
	}
	select {
	case x := <-h.p.output:
		want := api.ResourceSpec{
			CPUCores: 1, MemoryMB: 1, DiskMB: 2, NetworkMbps: 40,
			GPU: api.GPUResourceSpec{Required: true, DeviceCount: 1, MemoryMB: 4},
		}
		if x.err != nil || x.bytes != "A[x]" || !slices.Equal(x.task.Args, []api.TaskArgument{a}) ||
			x.task.Function.Resource != want {
			t.Fatalf("joint resource recovery descriptor mismatch: %+v", x)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("optional resource retry did not recover")
	}
	h.count("A", 1, 1)
}
