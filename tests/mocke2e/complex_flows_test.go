//go:build linux

package mocke2e

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"triggerEngine/api"
)

type flowRule struct {
	bucket, key, target string
}

type flowEdge struct {
	source, target string
}

type flowApp struct {
	name, library string
	rules         []flowRule
	edges         []flowEdge
	groupTargets  bool
}

type flowInput struct {
	bucket, key, session, data, app string
}

type flowRelay struct {
	library, target string
	matchKey        string

	bucket, nextKey string
}

type flowResult struct {
	library, target, bucket, key, session string

	output string
}

type flowCase struct {
	name       string
	apps       []flowApp
	inputs     []flowInput
	relays     []flowRelay
	want       []flowResult
	concurrent bool
}

const flowLibrary = "flow.so"

func complexFlows() []flowCase {
	immediate := func(bucket, target string) flowRule { return flowRule{bucket: bucket, target: target} }
	named := func(bucket, key, target string) flowRule {
		return flowRule{bucket: bucket, key: key, target: target}
	}
	link := func(source, target string) flowEdge { return flowEdge{source: source, target: target} }
	relay := func(target, bucket, key string) flowRelay {
		return flowRelay{library: flowLibrary, target: target, bucket: bucket, nextKey: key}
	}
	app := func(rules []flowRule, edges ...flowEdge) []flowApp {
		return []flowApp{{name: "flow", library: flowLibrary, rules: rules, edges: edges}}
	}
	want := func(target, bucket, key, output string) flowResult {
		return flowResult{library: flowLibrary, target: target, bucket: bucket, key: key, session: "s1", output: output}
	}
	seed := []flowInput{{"input", "k", "s1", "seed", "flow"}}
	return []flowCase{
		{
			name: "three-hop-transformation", apps: app([]flowRule{immediate("input", "A"), immediate("final", "Z")}, link("A", "B")),
			inputs: seed, relays: []flowRelay{relay("A", "mid", "a"), relay("B", "final", "b")},
			want: []flowResult{want("A", "input", "k", "A[seed]"), want("B", "mid", "a", "B[A[seed]]"), want("Z", "final", "b", "Z[B[A[seed]]]")},
		},
		{
			name: "direct-fanout-with-independent-sink",
			apps: app([]flowRule{immediate("input", "A"), immediate("final", "Z")},
				flowEdge{"A", "B"}, flowEdge{"A", "C"}),
			inputs: seed, relays: []flowRelay{relay("A", "mid", "a"), relay("B", "final", "b")},
			want: []flowResult{
				want("A", "input", "k", "A[seed]"),
				{flowLibrary, "B", "mid", "a", "s1", "B[A[seed]]"},
				{flowLibrary, "C", "mid", "a", "s1", "C[A[seed]]"},
				want("Z", "final", "b", "Z[B[A[seed]]]"),
			},
		},
		{
			name: "direct-priority-with-unrelated-trigger", apps: app([]flowRule{immediate("input", "A"), immediate("mid", "X"), immediate("audit", "C"), immediate("final", "Z")}, link("A", "B")),
			inputs: append([]flowInput{{"audit", "log", "s1", "independent", "flow"}}, seed...),
			relays: []flowRelay{relay("A", "mid", "a"), relay("B", "final", "b")},
			want:   []flowResult{want("C", "audit", "log", "C[independent]"), want("A", "input", "k", "A[seed]"), want("B", "mid", "a", "B[A[seed]]"), want("Z", "final", "b", "Z[B[A[seed]]]")},
		},
		{
			name: "case-sensitive-routing-on-two-hops",
			apps: []flowApp{{name: "flow", library: flowLibrary, rules: []flowRule{
				named("input", "Ready", "A"), named("final", "done", "Z"),
			}}},
			inputs: []flowInput{{"input", "ready", "s1", "ignored", "flow"}, {"input", "Ready", "s1", "seed", "flow"}, {"final", "Done", "s1", "ignored", "flow"}},
			relays: []flowRelay{relay("A", "final", "done")},
			want: []flowResult{
				{flowLibrary, "A", "input", "Ready", "s1", "A[seed]"},
				{flowLibrary, "Z", "final", "done", "s1", "Z[A[seed]]"},
			},
		},
		{
			name:   "same-target-immediate-and-by-name",
			apps:   app([]flowRule{immediate("input", "A"), immediate("mid", "B"), named("mid", "a", "B"), named("mid", "a", "Z")}),
			inputs: seed, relays: []flowRelay{relay("A", "mid", "a")},
			want: []flowResult{want("A", "input", "k", "A[seed]"), want("B", "mid", "a", "B[A[seed]]"),
				want("B", "mid", "a", "B[A[seed]]"), want("Z", "mid", "a", "Z[A[seed]]")},
		},
		{
			name: "concurrent-sessions-sharing-a-key", concurrent: true,
			apps:   app([]flowRule{immediate("input", "A"), named("final", "s2", "Z")}, link("A", "B")),
			inputs: []flowInput{{"input", "k", "s1", "red", "flow"}, {"input", "k", "s2", "blue", "flow"}, {"input", "k", "s3", "green", "flow"}},
			relays: []flowRelay{relay("A", "mid", "$key"), relay("B", "final", "$session")},
			want: []flowResult{
				want("A", "input", "k", "A[red]"), want("B", "mid", "k", "B[A[red]]"),
				{flowLibrary, "A", "input", "k", "s2", "A[blue]"},
				{flowLibrary, "B", "mid", "k", "s2", "B[A[blue]]"},
				{flowLibrary, "A", "input", "k", "s3", "A[green]"},
				{flowLibrary, "B", "mid", "k", "s3", "B[A[green]]"},
				{flowLibrary, "Z", "final", "s2", "s2", "Z[B[A[blue]]]"},
			},
		},
		{
			name:   "zero-length-and-binary-objects",
			apps:   app([]flowRule{immediate("input", "A"), immediate("audit", "C"), named("final", "键", "Z")}),
			inputs: []flowInput{{"input", "键", "s1", "汉\x00\xff", "flow"}, {"audit", "empty", "s1", "", "flow"}},
			relays: []flowRelay{relay("A", "final", "$key")},
			want: []flowResult{
				want("A", "input", "键", "A[汉\x00\xff]"), want("C", "audit", "empty", "C[]"),
				{flowLibrary, "Z", "final", "键", "s1", "Z[A[汉\x00\xff]]"},
			},
		},
		{
			name: "same-symbol-in-two-applications", concurrent: true,
			apps: []flowApp{
				{name: "flow", library: flowLibrary, rules: []flowRule{immediate("input", "A"), immediate("final", "Z")}},
				{name: "other", library: "other-flow.so", rules: []flowRule{immediate("other-in", "A"), immediate("other-out", "C")}},
			},
			inputs: []flowInput{{"input", "k", "s1", "left", "flow"}, {"other-in", "k", "s1", "right", "other"}},
			relays: []flowRelay{relay("A", "final", "z"), {library: "other-flow.so", target: "A", bucket: "other-out", nextKey: "c"}},
			want: []flowResult{
				want("A", "input", "k", "A[left]"), want("Z", "final", "z", "Z[A[left]]"),
				{"other-flow.so", "A", "other-in", "k", "s1", "A[right]"},
				{"other-flow.so", "C", "other-out", "c", "s1", "C[A[right]]"},
			},
		},
		{
			name: "duplicate-notices-on-independent-branch", concurrent: true,
			apps:   app([]flowRule{immediate("input", "A"), immediate("audit", "B"), immediate("final", "Z")}),
			inputs: []flowInput{{"input", "k", "s1", "seed", "flow"}, {"audit", "same", "s1", "repeat", "flow"}, {"audit", "same", "s1", "repeat", "flow"}},
			relays: []flowRelay{relay("A", "final", "done")},
			want:   []flowResult{want("A", "input", "k", "A[seed]"), want("B", "audit", "same", "B[repeat]"), want("B", "audit", "same", "B[repeat]"), want("Z", "final", "done", "Z[A[seed]]")},
		},
		{
			name:   "diamond-with-independent-reconvergence",
			apps:   app([]flowRule{immediate("input", "A"), immediate("join", "D"), immediate("final", "Z")}, link("A", "B"), link("A", "C")),
			inputs: seed,
			relays: []flowRelay{relay("A", "mid", "branch"), relay("B", "join", "left"), relay("C", "join", "right"),
				{library: flowLibrary, target: "D", matchKey: "right", bucket: "final", nextKey: "result"}},
			want: []flowResult{
				want("A", "input", "k", "A[seed]"), want("B", "mid", "branch", "B[A[seed]]"), want("C", "mid", "branch", "C[A[seed]]"),
				want("D", "join", "left", "D[B[A[seed]]]"), want("D", "join", "right", "D[C[A[seed]]]"), want("Z", "final", "result", "Z[D[C[A[seed]]]]"),
			},
		},
	}
}

type flowLog struct {
	message   string
	reason    string
	dimension string
}

type flowProfile struct {
	name         string
	samples      []probeStep
	pool         []api.EnqueueResult
	initial      flowLog
	retries      []flowLog
	wantSink     bool
	wantChecks   int
	wantAttempts int
}

func complexProfiles() []flowProfile {
	var (
		cpu         = flowLog{message: "RESOURCE_INSUFFICIENT", dimension: "cpu"}
		memory      = flowLog{message: "RESOURCE_INSUFFICIENT", dimension: "memory"}
		capacity    = flowLog{message: "POOL_NO_CAPACITY"}
		read        = flowLog{message: "RESOURCE_PROBE_FAILED", reason: "read"}
		parse       = flowLog{message: "RESOURCE_PROBE_FAILED", reason: "parse"}
		calculation = flowLog{message: "RESOURCE_PROBE_FAILED", reason: "calculation"}
	)
	recovery := func(name string, sample probeStep, log flowLog) flowProfile {
		return flowProfile{name: name, samples: []probeStep{sample, enough}, initial: log,
			retries: []flowLog{{}}, wantSink: true, wantChecks: 2, wantAttempts: 1}
	}
	return []flowProfile{
		{name: "ready", wantSink: true, wantChecks: 1, wantAttempts: 1},
		recovery("cpu-recovers", lowCPU, cpu),
		recovery("memory-recovers", lowMem, memory),
		recovery("resource-read-recovers", probeRead, read),
		recovery("resource-parse-recovers", probeParse, parse),
		recovery("resource-calculation-recovers", probeCompute, calculation),
		{name: "pool-recovers", pool: []api.EnqueueResult{api.EnqueueNoCapacity}, initial: capacity,
			retries: []flowLog{{}}, wantSink: true, wantChecks: 2, wantAttempts: 2},
		{name: "mixed-failures-recover", samples: []probeStep{lowCPU, enough, probeCompute, enough},
			pool: []api.EnqueueResult{api.EnqueueNoCapacity}, initial: cpu,
			retries: []flowLog{capacity, calculation, {}}, wantSink: true, wantChecks: 4, wantAttempts: 2},
		{name: "resource-deadline-expires", samples: []probeStep{lowCPU}, initial: cpu,
			retries: []flowLog{cpu, cpu, cpu, {message: "TASK_DELAY_EXPIRED"}}, wantChecks: 4},
		{name: "pool-error-is-terminal", pool: []api.EnqueueResult{api.EnqueueError},
			initial: flowLog{message: "POOL_ERROR"}, wantChecks: 1, wantAttempts: 1},
	}
}

func TestComplexDataDriven(t *testing.T) {
	cases := complexScenarios()
	if len(cases) != 100 {
		t.Fatalf("expected 100 new scenarios, got %d", len(cases))
	}
	runMockScenarios(t, cases)
}

func complexScenarios() []scenario {
	var cases []scenario
	for group, graph := range complexFlows() {
		for variant, profile := range complexProfiles() {
			id := fmt.Sprintf("COMPLEX-E2E-%03d_%s_%s", group*10+variant+1, graph.name, profile.name)
			cases = append(cases, scenario{id: id, run: func(t *testing.T) { runComplexFlow(t, graph, profile) }})
		}
	}
	return cases
}

func runComplexFlow(t *testing.T, graph flowCase, profile flowProfile) {
	h := setup(t, 32, 8)
	h.probe.steps["Z"] = profile.samples
	h.p.script["Z"] = profile.pool

	for _, app := range graph.apps {
		h.installFlow(app, graph.relays)
	}
	h.p.start()
	submitFlowInputs(h, graph.inputs, graph.concurrent)

	var healthy, sink []flowResult
	for _, expected := range graph.want {
		if expected.target == "Z" {
			sink = append(sink, expected)
		} else {
			healthy = append(healthy, expected)
		}
	}
	if len(sink) != 1 {
		t.Fatalf("each flow needs exactly one sink, got %d", len(sink))
	}
	if profile.initial.message == "" {
		h.collectFlowResults(append(healthy, sink...))
	} else {
		occurrences := map[string]int{}
		first := h.awaitFlowLog(profile.initial, occurrences)
		if len(profile.retries) != 0 {
			start, startErr := time.Parse(time.RFC3339Nano, fmt.Sprint(first["first_deferred"]))
			deadline, endErr := time.Parse(time.RFC3339Nano, fmt.Sprint(first["deadline"]))
			if startErr != nil || endErr != nil || deadline.Sub(start) != 40*time.Millisecond {
				t.Fatalf("Z must keep the first 40ms deadline: %+v", first)
			}
		}
		h.collectFlowResults(healthy)
		h.count("Z", initialPoolAttempts(profile.initial), 0)
		for _, log := range profile.retries {
			h.advance(step)
			if log.message != "" {
				current := h.awaitFlowLog(log, occurrences)
				if current["deadline"] != first["deadline"] || current["first_deferred"] != first["first_deferred"] {
					t.Fatalf("retry changed deadline: first=%v current=%v", first, current)
				}
			}
		}
		if profile.wantSink {
			h.collectFlowResults(sink)
		}
	}

	h.closeRuntime()
	wantAccept := 0
	if profile.wantSink {
		wantAccept = 1
	}
	h.count("Z", profile.wantAttempts, wantAccept)
	if checked := h.probe.count("Z"); checked != profile.wantChecks {
		t.Fatalf("Z resource checks=%d, want %d", checked, profile.wantChecks)
	}
	if attempts := len(h.p.audit()); attempts != len(healthy)+profile.wantAttempts {
		t.Fatalf("total enqueue attempts=%d, want %d", attempts, len(healthy)+profile.wantAttempts)
	}
	h.checkFinalCount()
	t.Logf("verified %d upstream executions, Z accepted=%d, resource checks=%d, enqueue attempts=%d", len(healthy), wantAccept, profile.wantChecks, profile.wantAttempts)
}

func initialPoolAttempts(log flowLog) int {
	switch log.message {
	case "POOL_NO_CAPACITY", "POOL_ERROR":
		return 1
	default:
		return 0
	}
}

func (h *harness) awaitFlowLog(log flowLog, occurrences map[string]int) map[string]any {
	h.t.Helper()
	occurrences[log.message]++
	rec := h.log.await(h.t, log.message, "Z", occurrences[log.message])
	if log.reason != "" && rec["probe_failure_reason"] != log.reason {
		h.t.Fatalf("Z fault reason=%v, want %s", rec["probe_failure_reason"], log.reason)
	}
	if log.dimension != "" && rec["dimension"] != log.dimension {
		h.t.Fatalf("Z insufficient resource=%v, want %s", rec["dimension"], log.dimension)
	}
	return rec
}

type flowConfigStats struct {
	functions, buckets, rules, direct int
	json                              []byte
}

type flowDependency struct {
	Type    string        `json:"type"`
	Sources []functionRef `json:"src_functions"`
	Targets []functionRef `json:"tgt_functions"`
}

type functionRef struct {
	SOName   string `json:"so_name"`
	FuncName string `json:"func_name"`
}

func (h *harness) installFlow(app flowApp, relays []flowRelay) flowConfigStats {
	h.t.Helper()
	var functions, rules []map[string]any
	var dependencies []flowDependency
	identity := func(name string) functionRef {
		return functionRef{SOName: app.library, FuncName: name}
	}
	knownFunction := map[string]bool{}
	knownBucket := map[string]bool{}
	addFunction := func(name string) {
		if !knownFunction[name] {
			functions = append(functions, map[string]any{
				"func_name": name, "so_name": app.library, "resource": map[string]int{"cpu_cores": 1, "memory_mb": 1},
			})
			knownFunction[name] = true
		}
	}
	for i, rule := range app.rules {
		addFunction(rule.target)
		knownBucket[rule.bucket] = true
		primitive := map[string]any{"function": identity(rule.target)}
		kind := "IMMEDIATE"
		if rule.key != "" {
			kind = "BY_NAME"
			primitive["key_name"] = rule.key
		}
		rules = append(rules, map[string]any{
			"bucket": rule.bucket, "trigger_name": fmt.Sprintf("rule-%d", i), "primitive_type": kind,
			"primitive": primitive,
		})
	}
	grouped := map[string]int{}
	for _, edge := range app.edges {
		addFunction(edge.source)
		addFunction(edge.target)
		key := edge.source
		if app.groupTargets {
			if i, ok := grouped[key]; ok && len(dependencies[i].Targets) < 4 {
				dependencies[i].Targets = append(dependencies[i].Targets, identity(edge.target))
				continue
			}
			grouped[key] = len(dependencies)
		}
		dependencies = append(dependencies, flowDependency{
			Type:    "DIRECT",
			Sources: []functionRef{identity(edge.source)},
			Targets: []functionRef{identity(edge.target)},
		})
	}
	for target := range knownFunction {
		h.p.execute[[2]string{app.library, target}] = h.flowFunction(app.library, target, relays)
	}
	body := map[string]any{
		"app_name": app.name, "version": "0.6",
		"functions": functions, "triggers": rules,
		"scheduler": map[string]any{"delay_queue": map[string]any{"enabled": true, "retry_interval_ms": 10, "max_wait_ms": 40}},
	}
	if len(dependencies) > 0 {
		body["dependencies"] = dependencies
	}
	data, err := json.Marshal(body)
	if err != nil {
		h.t.Fatal(err)
	}
	checkCode(h.t, api.TriggerInit(put(h.t, string(data))), 0)
	return flowConfigStats{len(functions), len(knownBucket), len(rules), len(app.edges), data}
}

func (h *harness) flowFunction(library, target string, relays []flowRelay) func(api.TaskInvocation) (string, error) {
	var outputs []flowRelay
	for _, relay := range relays {
		if relay.library == library && relay.target == target {
			outputs = append(outputs, relay)
		}
	}
	return func(task api.TaskInvocation) (string, error) {
		input, err := h.sdk.logicalInput(task)
		if err != nil {
			return "", err
		}
		output := target + "[" + input + "]"
		batch := h.sdk.batchEvent(task.DataAddr)
		for _, relay := range outputs {
			if relay.matchKey != "" && relay.matchKey != batch.Key {
				continue
			}
			key := relay.nextKey
			switch key {
			case "$key":
				key = batch.Key
			case "$session":
				key = task.SessionID
			}
			event := api.DataReadyEvent{AppName: task.AppName, Bucket: relay.bucket, Key: key, SessionID: task.SessionID}
			if _, err := h.sdk.commit(event, api.FunctionIdentity{AppName: task.AppName, SOName: task.SOName, FuncName: task.FuncName}, output); err != nil {
				return "", err
			}
		}
		return output, nil
	}
}

func submitFlowInputs(h *harness, inputs []flowInput, concurrent bool) {
	h.t.Helper()
	commit := func(in flowInput) error {
		_, err := h.sdk.commit(api.DataReadyEvent{AppName: in.app, Bucket: in.bucket, Key: in.key, SessionID: in.session}, api.FunctionIdentity{}, in.data)
		return err
	}
	if !concurrent {
		for _, in := range inputs {
			checkCode(h.t, commit(in), 0)
		}
		return
	}
	start := make(chan struct{})
	errs := make(chan error, len(inputs))
	var wg sync.WaitGroup
	for _, in := range inputs {
		wg.Go(func() {
			<-start
			errs <- commit(in)
		})
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		checkCode(h.t, err, 0)
	}
}

func (h *harness) collectFlowResults(wants []flowResult) {
	h.t.Helper()
	remaining := append([]flowResult(nil), wants...)
	watchdog := time.NewTimer(5 * time.Second)
	defer watchdog.Stop()
	for len(remaining) > 0 {
		select {
		case got := <-h.p.output:
			if got.err != nil {
				h.t.Fatalf("invalid execution result: %+v", got)
			}
			match := -1
			batch := h.sdk.batchEvent(got.task.DataAddr)
			for i, want := range remaining {
				if got.task.SOName != want.library || got.task.FuncName != want.target ||
					batch.Bucket != want.bucket || got.task.SessionID != want.session ||
					batch.SessionID != want.session || batch.Key != want.key || got.bytes != want.output {
					continue
				}
				if batch.DataAddr != nil && batch.AppName == got.task.AppName {
					match = i
					break
				}
			}
			if match < 0 {
				h.t.Fatalf("unexpected execution %+v, remaining expected results (up to 5 of %d): %+v",
					got, len(remaining), remaining[:min(5, len(remaining))])
			}
			remaining = append(remaining[:match], remaining[match+1:]...)
		case <-watchdog.C:
			h.t.Fatalf("missing execution results: %+v", remaining)
		}
	}
}
