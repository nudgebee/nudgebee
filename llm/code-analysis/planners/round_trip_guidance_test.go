package planners

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"nudgebee/code-analysis-agent/tools"
	"nudgebee/code-analysis-agent/tools/core"
)

// probeTool reports how many probe tools were running at once and in which
// order they started.
type probeTool struct {
	name     string
	readOnly bool
	running  *int32
	peak     *int32
	mu       *sync.Mutex
	started  *[]string
	// together, when set, is closed once every expected call has started; a
	// call waits on it, so calls that are run one after another never see it.
	together chan struct{}
	expected int32
}

func (t *probeTool) Name() string                 { return t.name }
func (t *probeTool) Description() string          { return "" }
func (t *probeTool) InputSchema() core.ToolSchema { return core.ToolSchema{} }
func (t *probeTool) GetType() core.NBToolType     { return core.NBToolTypeTool }
func (t *probeTool) IsReadOnly() bool             { return t.readOnly }

func (t *probeTool) Execute(_ context.Context, _ map[string]any) core.NBToolResponse {
	n := atomic.AddInt32(t.running, 1)
	defer atomic.AddInt32(t.running, -1)
	for {
		peak := atomic.LoadInt32(t.peak)
		if n <= peak || atomic.CompareAndSwapInt32(t.peak, peak, n) {
			break
		}
	}
	t.mu.Lock()
	*t.started = append(*t.started, t.name)
	all := int32(len(*t.started)) == t.expected
	t.mu.Unlock()
	if t.together != nil {
		if all {
			close(t.together)
		}
		select {
		case <-t.together:
		case <-time.After(2 * time.Second):
			return core.NBToolResponse{Status: "error", Error: "the other calls never started"}
		}
	}
	return core.NBToolResponse{Status: "success", Observation: "ok " + t.name}
}

func probePlanner(names []string, readOnly map[string]bool, together bool) (*ReActPlanner, []Step, *int32, *[]string) {
	var running, peak int32
	var mu sync.Mutex
	started := []string{}
	var gate chan struct{}
	if together {
		gate = make(chan struct{})
	}
	toolList := make([]core.NBTool, 0, len(names))
	steps := make([]Step, 0, len(names))
	for i, name := range names {
		toolList = append(toolList, &probeTool{name: name, readOnly: readOnly[name], running: &running, peak: &peak,
			mu: &mu, started: &started, together: gate, expected: int32(len(names))})
		steps = append(steps, Step{Number: i + 1, Action: name, ActionInput: map[string]any{"n": i}, Status: "parsing"})
	}
	return NewReActPlanner(nil, toolList, 10), steps, &peak, &started
}

// The system prompt tells the model that independent lookups sent in one
// response run at the same time. That has to be true.
func TestLookupsFromOneResponseRunTogether(t *testing.T) {
	names := []string{"find_a", "search_b", "read_c"}
	p, steps, peak, _ := probePlanner(names, map[string]bool{"find_a": true, "search_b": true, "read_c": true}, true)

	p.executeSteps(context.Background(), steps)

	for _, s := range steps {
		if s.Status != "completed" {
			t.Fatalf("%s: status %q, error %q — the lookups were not running at the same time", s.Action, s.Status, s.Error)
		}
	}
	if got := atomic.LoadInt32(peak); got != int32(len(names)) {
		t.Fatalf("%d lookups ran at once, want %d", got, len(names))
	}
}

// A response that carries anything other than lookups runs its calls one after
// another, in the order the model wrote them: an edit must not race the read
// next to it.
func TestAResponseWithAnEditRunsInOrder(t *testing.T) {
	names := []string{"read_a", "edit_b", "read_c"}
	p, steps, peak, started := probePlanner(names, map[string]bool{"read_a": true, "read_c": true}, false)

	p.executeSteps(context.Background(), steps)

	if got := atomic.LoadInt32(peak); got != 1 {
		t.Fatalf("%d calls ran at once next to an edit, want 1", got)
	}
	if got := strings.Join(*started, ","); got != strings.Join(names, ",") {
		t.Fatalf("calls ran in the order %s, want %s", got, strings.Join(names, ","))
	}
}

// The lookups the guidance names — searches, file finds, file reads — have to
// be the tools the planner is willing to run at the same time.
func TestTheLookupsNamedInTheGuidanceAreRunTogether(t *testing.T) {
	dir := t.TempDir()
	lookups := []core.NBTool{tools.NewRipgrepTool(dir), tools.NewFileFindTool(dir), tools.NewFileViewTool(dir)}
	for _, tool := range lookups {
		ro, ok := tool.(core.ReadOnlyTool)
		if !ok || !ro.IsReadOnly() {
			t.Errorf("%s is named as a lookup that runs at the same time as others, but the planner would run it alone", tool.Name())
		}
	}
}

func TestRoundTripGuidanceNamesOnlyLookups(t *testing.T) {
	for _, word := range []string{"edit", "replace", "write", "commit", "push"} {
		if strings.Contains(strings.ToLower(roundTripGuidance), word) {
			t.Errorf("the guidance must not ask for %q calls to be sent together", word)
		}
	}
	if !strings.Contains(roundTripGuidance, "do not depend on each other") {
		t.Error("the guidance must limit itself to lookups that do not depend on each other")
	}
}
