//go:build linux

package mocke2e

import (
	"fmt"
	"maps"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"unsafe"

	"triggerEngine/api"
)

var routingMotifs = [...]string{
	"direct-suppresses-named-and-immediate",
	"dense-bucket-fanout",
	"three-rules-one-target",
	"exact-unicode-and-whitespace",
	"diamond-branches",
	"direct-fanout",
	"same-bucket-distinct-source-paths",
	"same-function-multiple-distinct-buckets",
	"two-output-buckets",
	"bounded-self-trigger",
}

type routingPlan struct {
	graph    flowCase
	produced []flowInput
	depth    int
}

type routingLane struct {
	plan *routingPlan
	app  flowApp
}

// Each motif declares routing rules and independently records its expected
// executions. The oracle never looks up triggers or follows a DIRECT edge.
func routingScenarios() []scenario {
	var cases []scenario
	for emphasis, name := range routingMotifs {
		for scale := 1; scale <= 10; scale++ {
			id := fmt.Sprintf("CONFIG-E2E-%03d_%s_scale-%02d", emphasis*10+scale, name, scale)
			cases = append(cases, scenario{id: id, run: func(t *testing.T) {
				runRoutingPlan(t, id, makeRoutingPlan(emphasis, scale))
			}})
		}
	}
	return cases
}

func TestComplexConfigRouting(t *testing.T) {
	cases := routingScenarios()
	if len(cases) != 100 {
		t.Fatalf("expected 100 configuration scenarios, got %d", len(cases))
	}
	runMockScenarios(t, cases)
}

func makeRoutingPlan(emphasis, scale int) routingPlan {
	plan := routingPlan{depth: 11 + scale}
	appCount := 2 + (emphasis+scale)%3
	generator := rand.New(rand.NewPCG(uint64(emphasis+1), uint64(scale)))
	for appIndex := range appCount {
		tag := fmt.Sprintf("app-%d", appIndex)
		lane := routingLane{
			plan: &plan,
			app:  flowApp{name: tag, library: tag + ".so", groupTargets: (scale+appIndex)%3 != 0},
		}
		session := fmt.Sprintf("session-%d", appIndex)
		first := lane.seed("input", "shared-key", session, "token"+tag)
		lane.rule(first.bucket, "", "Entry")
		current := lane.expect(first, "Entry")
		for stage := range plan.depth {
			motif := (stage + emphasis + appIndex) % len(routingMotifs)
			if stage >= len(routingMotifs) && stage%2 == 0 {
				motif = emphasis
			}
			width := 3 + (stage+scale+appIndex)%6
			current = lane.stage(stage, motif, width, current)
		}

		final := lane.emit(current, "result", "ready")
		lane.rule(final.bucket, "ready", "Finish")
		lane.rule(final.bucket, "Ready", "WrongFinish")
		lane.expect(final, "Finish")
		lane.rule("unused", "never", "OnlyIn"+fmt.Sprint(appIndex))
		// Function and bucket identities remain fixed while rule order varies.
		generator.Shuffle(len(lane.app.rules), func(i, j int) {
			lane.app.rules[i], lane.app.rules[j] = lane.app.rules[j], lane.app.rules[i]
		})
		generator.Shuffle(len(lane.app.edges), func(i, j int) {
			lane.app.edges[i], lane.app.edges[j] = lane.app.edges[j], lane.app.edges[i]
		})
		plan.graph.apps = append(plan.graph.apps, lane.app)
	}
	generator.Shuffle(len(plan.graph.inputs), func(i, j int) {
		plan.graph.inputs[i], plan.graph.inputs[j] = plan.graph.inputs[j], plan.graph.inputs[i]
	})
	return plan
}

func (l *routingLane) seed(bucket, key, session, data string) flowInput {
	event := flowInput{bucket, key, session, data, l.app.name}
	l.plan.graph.inputs = append(l.plan.graph.inputs, event)
	return event
}

func (l *routingLane) rule(bucket, key, target string) {
	l.app.rules = append(l.app.rules, flowRule{bucket: bucket, key: key, target: target})
}

func (l *routingLane) direct(source, target string) {
	l.app.edges = append(l.app.edges, flowEdge{source: source, target: target})
}

func (l *routingLane) expect(event flowInput, target string) flowResult {
	value := event.data
	want := flowResult{
		library: l.app.library, target: target, bucket: event.bucket, key: event.key,
		session: event.session, output: target + "[" + value + "]",
	}
	l.plan.graph.want = append(l.plan.graph.want, want)
	return want
}

func (l *routingLane) emit(source flowResult, bucket, key string) flowInput {
	event := flowInput{bucket, key, source.session, source.output, l.app.name}
	l.plan.graph.relays = append(l.plan.graph.relays, flowRelay{
		library: l.app.library, target: source.target, matchKey: source.key,
		bucket: event.bucket, nextKey: event.key,
	})
	l.plan.produced = append(l.plan.produced, event)
	return event
}

func (l *routingLane) stage(index, motif, width int, previous flowResult) flowResult {
	segment := fmt.Sprintf("stage-%02d", index)
	target := fmt.Sprintf("Stage%02d", index)
	slot := func(role string, i int) string { return fmt.Sprintf("%s_%s%d", target, role, i) }

	switch motif {
	case 0:
		in := l.emit(previous, segment, "matched")
		l.direct(previous.target, target)
		for i := range width {
			l.direct(previous.target, slot("direct", i))
			l.expect(in, slot("direct", i))
			l.rule(in.bucket, "", slot("ignored", i))
			l.rule(in.bucket, in.key, slot("named_ignored", i))
		}
		return l.expect(in, target)
	case 1:
		in := l.emit(previous, segment, "matched")
		l.rule(in.bucket, "", target)
		for i := range width {
			l.rule(in.bucket, "", slot("listener", i))
			l.expect(in, slot("listener", i))
			l.rule(in.bucket, "other", slot("ignored", i))
		}
		return l.expect(in, target)
	case 2:
		in := l.emit(previous, segment, "matched")
		observer := slot("observer", 0)
		l.rule(in.bucket, "", observer)
		l.rule(in.bucket, in.key, observer)
		l.rule(in.bucket, in.key, observer)
		l.rule(in.bucket, in.key, target)
		l.expect(in, observer)
		l.expect(in, observer)
		l.expect(in, observer)
		return l.expect(in, target)
	case 3:
		const exact = " Å键 "
		in := l.emit(previous, segment, exact)
		l.rule(in.bucket, exact, target)
		for i, near := range []string{" Å键", "Å键 ", " å键 ", " A\u030a键 ", " Å键\t"} {
			l.rule(in.bucket, near, slot("near", i))
		}
		l.seed(segment, "Å键", in.session, "no-match")
		l.seed(segment, " A\u030a键\t", in.session, "no-match")
		return l.expect(in, target)
	case 4:
		in := l.emit(previous, segment, "branch")
		for i := range width {
			branch := slot("branch", i)
			l.rule(in.bucket, "", branch)
			out := l.expect(in, branch)
			join := l.emit(out, segment+"-join", fmt.Sprintf("route-%02d", i))
			if i == width-1 {
				l.rule(join.bucket, "", target)
				return l.expect(join, target)
			}
			l.expect(join, target)
		}
	case 5:
		in := l.emit(previous, segment, "direct-key")
		l.direct(previous.target, target)
		for i := range width {
			l.direct(previous.target, slot("mode", i))
			l.expect(in, slot("mode", i))
		}
		return l.expect(in, target)
	case 6:
		in := l.emit(previous, segment, "from-function")
		l.direct(previous.target, target)
		l.rule(in.bucket, "", slot("external", 0))
		external := l.seed(segment, "from-sdk", in.session, "external")
		l.expect(external, slot("external", 0))
		return l.expect(in, target)
	case 7:
		in := l.emit(previous, segment, "main-key")
		l.rule(in.bucket, "", target)
		for i := range width {
			other := l.seed(segment+fmt.Sprint("-independent-", i), "side-key", in.session, fmt.Sprint("value-", i))
			l.rule(other.bucket, "", target)
			l.expect(other, target)
		}
		return l.expect(in, target)
	case 8:
		in := l.emit(previous, segment, "main-key")
		audit := l.emit(previous, segment+"-audit", "audit-key")
		l.rule(in.bucket, in.key, target)
		for i := range width {
			l.rule(audit.bucket, audit.key, slot("audit", i))
			l.expect(audit, slot("audit", i))
		}
		return l.expect(in, target)
	case 9:
		in := l.emit(previous, segment, "begin")
		l.rule(in.bucket, "begin", target)
		l.rule(in.bucket, "again", target)
		first := l.expect(in, target)
		next := l.emit(first, segment, "again")
		repeated := l.expect(next, target)
		// Keep the continuation on a different symbol. A DIRECT edge from the
		// feedback symbol would suppress its own bucket rule for "again".
		exit := l.emit(repeated, segment+"-exit", "done")
		l.rule(exit.bucket, exit.key, target+"_exit")
		return l.expect(exit, target+"_exit")
	}
	panic("invalid routing motif")
}

func runRoutingPlan(t *testing.T, id string, plan routingPlan) {
	graph := plan.graph
	h := setup(t, len(graph.want)+32, 8)
	total := flowConfigStats{}
	totalBytes := 0
	for _, app := range graph.apps {
		info := h.installFlow(app, graph.relays)
		total.functions += info.functions
		total.buckets += info.buckets
		total.rules += info.rules
		total.direct += info.direct
		totalBytes += len(info.json)
		if export := os.Getenv("PHEROMONE_ROUTING_CONFIG_DIR"); export != "" {
			dir := filepath.Join(export, id, app.name)
			if err := os.MkdirAll(dir, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "app.json"), info.json, 0644); err != nil {
				t.Fatal(err)
			}
		}
	}
	// The source function exists in App 1, but must be rejected by App 0.
	crossApp := api.DataReadyEvent{AppName: "app-0", Bucket: "input", Key: "foreign", SessionID: "rejected", DataAddr: unsafe.Pointer(new(byte))}
	checkCode(t, api.EventTriggerNotify(crossApp, api.FunctionIdentity{AppName: "app-1", SOName: "app-1.so", FuncName: "OnlyIn1"}), api.ErrInvalidArgument)

	h.p.start()
	submitFlowInputs(h, graph.inputs, true)
	h.collectFlowResults(graph.want)
	h.closeRuntime()
	attempts := h.p.audit()
	if len(attempts) != len(graph.want) {
		t.Fatalf("enqueue attempts=%d, expected exactly %d", len(attempts), len(graph.want))
	}
	for _, attempt := range attempts {
		if attempt.result != api.EnqueueAccepted {
			t.Fatalf("unexpected pool rejection: %+v", attempt)
		}
	}
	h.checkFinalCount()
	h.checkRoutingObjects(slices.Concat(graph.inputs, plan.produced))
	t.Logf("routing verified: apps=%d depth=%d functions=%d buckets=%d triggers=%d direct=%d bytes=%d executions=%d objects=%d",
		len(graph.apps), plan.depth, total.functions, total.buckets, total.rules, total.direct, totalBytes,
		len(attempts), len(graph.inputs)+len(plan.produced))
}

func (h *harness) checkRoutingObjects(objects []flowInput) {
	h.t.Helper()
	h.sdk.mu.Lock()
	committed := maps.Clone(h.sdk.committed)
	h.sdk.mu.Unlock()
	if len(committed) != len(objects) {
		h.t.Fatalf("committed objects=%d, want %d", len(committed), len(objects))
	}
	for _, want := range objects {
		event := api.DataReadyEvent{AppName: want.app, Bucket: want.bucket, Key: want.key, SessionID: want.session}
		arg, ok := committed[event]
		if !ok {
			h.t.Fatalf("missing object %v", event)
		}
		value, err := h.sdk.readObject(arg.DataAddr)
		if err != nil || value != want.data {
			h.t.Fatalf("object %v bytes=%q, want %q, err=%v", event, value, want.data, err)
		}
	}
}
