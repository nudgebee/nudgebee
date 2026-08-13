package planners

import (
	"fmt"
	"strings"
	"testing"

	"github.com/tmc/langchaingo/llms"
)

// buildConvo makes a system+human header followed by n (AI, Tool) round pairs.
// Each Tool message carries one big observation with a distinct ToolCallID
// (c1..cn) so the distillation gate can be exercised per step.
func buildConvo(n, obsSize int) []llms.MessageContent {
	big := strings.Repeat("x", obsSize)
	msgs := []llms.MessageContent{
		{Role: llms.ChatMessageTypeSystem, Parts: []llms.ContentPart{llms.TextContent{Text: "system"}}},
		{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextContent{Text: "query"}}},
	}
	for i := 1; i <= n; i++ {
		id := fmt.Sprintf("c%d", i)
		msgs = append(msgs, llms.MessageContent{
			Role: llms.ChatMessageTypeAI,
			Parts: []llms.ContentPart{
				llms.TextContent{Text: "thought"},
				llms.ToolCall{ID: id, FunctionCall: &llms.FunctionCall{Name: "file_view", Arguments: "{}"}},
			},
		})
		msgs = append(msgs, llms.MessageContent{
			Role:  llms.ChatMessageTypeTool,
			Parts: []llms.ContentPart{llms.ToolCallResponse{ToolCallID: id, Name: "file_view", Content: big}},
		})
	}
	return msgs
}

// plannerWithSteps returns a planner whose Tool messages 1..n map positionally
// to step numbers 1..n, with the distillation watermark at `reflected`.
func plannerWithSteps(window, n, reflected int) *ReActPlanner {
	p := &ReActPlanner{recentObservationWindow: window, lastReflectedStep: reflected}
	for i := 1; i <= n; i++ {
		p.toolMsgMeta = append(p.toolMsgMeta, toolMsgMeta{maxStep: i, desc: fmt.Sprintf("file_view f%d.go", i)})
	}
	return p
}

func toolContent(m llms.MessageContent) string {
	for _, p := range m.Parts {
		if tr, ok := p.(llms.ToolCallResponse); ok {
			return tr.Content
		}
	}
	return ""
}

func toolMsgs(msgs []llms.MessageContent) []llms.MessageContent {
	var out []llms.MessageContent
	for _, m := range msgs {
		if m.Role == llms.ChatMessageTypeTool {
			out = append(out, m)
		}
	}
	return out
}

func TestAgeOldObservations_KeepsRecentStubsDistilledOld(t *testing.T) {
	const obs = 5000
	// 6 rounds, watermark at 6 (everything distilled), window 3.
	p := plannerWithSteps(3, 6, 6)
	in := buildConvo(6, obs)
	out := p.ageOldObservations(in)

	// original untouched (we returned a copy)
	for _, m := range toolMsgs(in) {
		if len(toolContent(m)) != obs {
			t.Fatalf("original mutated: got %d want %d", len(toolContent(m)), obs)
		}
	}

	tools := toolMsgs(out)
	if len(tools) != 6 {
		t.Fatalf("tool msg count changed: %d", len(tools))
	}
	// oldest 3 stubbed (old + distilled), newest 3 full. Stubs must be
	// self-describing: tool+args identity plus the ledger pointer.
	for i, m := range tools {
		c := toolContent(m)
		if i < 3 {
			if len(c) >= obs || !strings.Contains(c, "ELIDED") || !strings.Contains(c, "LEDGER") ||
				!strings.Contains(c, fmt.Sprintf("file_view f%d.go", i+1)) {
				t.Errorf("old tool %d not aged to identified ledger-stub: len=%d", i, len(c))
			}
		} else {
			if len(c) != obs {
				t.Errorf("recent tool %d was shrunk: len=%d", i, len(c))
			}
		}
	}
}

func TestAgeOldObservations_UndistilledNeverStubbed(t *testing.T) {
	const obs = 5000
	// 8 rounds, but reflection has only integrated steps 1-2 (watermark=2).
	// Steps 3-5 are old (beyond window) yet NOT distilled — must stay full.
	p := plannerWithSteps(3, 8, 2)
	out := toolMsgs(p.ageOldObservations(buildConvo(8, obs)))
	for i, m := range out {
		c := toolContent(m)
		if i < 2 { // steps 1-2: old + distilled -> stubbed
			if len(c) >= obs {
				t.Errorf("distilled old tool %d should be stubbed (len=%d)", i, len(c))
			}
		} else { // steps 3-8: undistilled or recent -> full
			if len(c) != obs {
				t.Errorf("undistilled/recent tool %d must stay full: len=%d", i, len(c))
			}
		}
	}
}

func TestAgeOldObservations_UnmappedToolMessageNotStubbed(t *testing.T) {
	const obs = 5000
	// Watermark high, but planner has NO positional step records — unmapped
	// Tool messages must never be dropped (can't prove they were preserved).
	p := &ReActPlanner{recentObservationWindow: 1, lastReflectedStep: 99}
	for _, m := range toolMsgs(p.ageOldObservations(buildConvo(5, obs))) {
		if len(toolContent(m)) != obs {
			t.Errorf("unmapped tool message was stubbed")
		}
	}
}

func TestAgeOldObservations_ShrinksTotalAndIsDeterministic(t *testing.T) {
	p := plannerWithSteps(3, 10, 10)
	in := buildConvo(10, 5000)

	sz := func(msgs []llms.MessageContent) int {
		t := 0
		for _, m := range msgs {
			t += len(toolContent(m))
		}
		return t
	}
	out1 := p.ageOldObservations(in)
	out2 := p.ageOldObservations(in)

	if sz(out1) >= sz(in) {
		t.Fatalf("no shrink: before=%d after=%d", sz(in), sz(out1))
	}
	tm1, tm2 := toolMsgs(out1), toolMsgs(out2)
	for i := range tm1 {
		if toolContent(tm1[i]) != toolContent(tm2[i]) {
			t.Fatalf("non-deterministic aging at tool %d", i)
		}
	}
	reduction := 1 - float64(sz(out1))/float64(sz(in))
	if reduction < 0.5 {
		t.Errorf("weak reduction: %.0f%%", reduction*100)
	}
	t.Logf("total obs chars %d -> %d (%.0f%% reduction)", sz(in), sz(out1), reduction*100)
}

func TestAgeOldObservations_AImessagesUntouched(t *testing.T) {
	p := plannerWithSteps(2, 5, 5)
	in := buildConvo(5, 5000)
	out := p.ageOldObservations(in)
	for i := range in {
		if in[i].Role != llms.ChatMessageTypeAI {
			continue
		}
		if len(out[i].Parts) != len(in[i].Parts) {
			t.Fatalf("AI message %d part count changed", i)
		}
	}
}

func TestAgeOldObservations_DisabledAndNoReflectionNoop(t *testing.T) {
	// window 0 => disabled even with a watermark
	p0 := plannerWithSteps(0, 6, 6)
	in := buildConvo(6, 5000)
	if got := p0.ageOldObservations(in); len(toolContent(toolMsgs(got)[0])) != 5000 {
		t.Errorf("window=0 should be a no-op")
	}
	// no reflection yet (watermark 0) => nothing may age
	p := plannerWithSteps(3, 6, 0)
	for _, m := range toolMsgs(p.ageOldObservations(in)) {
		if len(toolContent(m)) != 5000 {
			t.Errorf("before first reflection nothing should age")
		}
	}
}

func TestAgeOldObservations_PressureGate(t *testing.T) {
	const obs = 5000
	// Same fully-distilled conversation; only the budget differs.
	in := buildConvo(6, obs) // ~30K chars of observations

	// Budget far above the prompt estimate -> strict no-op (normal runs).
	pHigh := plannerWithSteps(3, 6, 6)
	pHigh.agingBudgetTokens = 1_000_000
	for _, m := range toolMsgs(pHigh.ageOldObservations(in)) {
		if len(toolContent(m)) != obs {
			t.Fatalf("below budget, aging must be a no-op")
		}
	}

	// Budget below the prompt estimate -> aging fires on distilled-old msgs.
	pLow := plannerWithSteps(3, 6, 6)
	pLow.agingBudgetTokens = 1000
	tools := toolMsgs(pLow.ageOldObservations(in))
	if len(toolContent(tools[0])) >= obs {
		t.Fatalf("above budget, distilled-old observations must be stubbed")
	}
}

func TestInjectLedgerBlock(t *testing.T) {
	in := buildConvo(2, 100)

	// no ledger -> unchanged
	p := plannerWithSteps(3, 2, 2)
	if got := p.injectLedgerBlock(in); len(got) != len(in) {
		t.Fatalf("empty ledger must not inject: %d -> %d", len(in), len(got))
	}

	// with findings + a citation snippet -> one extra tail human message
	// containing the ledger INCLUDING the verbatim snippet (the snippet is the
	// evidence that prevents identifier confabulation once raw text is aged).
	p.ledger = &Ledger{
		Findings:  []LedgerFinding{{Claim: "pool limit set in rdbms.go"}},
		Citations: []LedgerCitation{{FilePath: "rdbms.go", LineStart: 42, Snippet: "db.SetMaxOpenConns(cfg.ServiceDBMaxConnection)"}},
	}
	got := p.injectLedgerBlock(in)
	if len(got) != len(in)+1 {
		t.Fatalf("expected one injected message, got %d -> %d", len(in), len(got))
	}
	last := got[len(got)-1]
	if last.Role != llms.ChatMessageTypeHuman {
		t.Errorf("ledger block must be a human message, got %s", last.Role)
	}
	txt := ""
	if tc, ok := last.Parts[0].(llms.TextContent); ok {
		txt = tc.Text
	}
	if !strings.Contains(txt, "INVESTIGATION LEDGER") || !strings.Contains(txt, "rdbms.go") {
		t.Errorf("ledger block missing content")
	}
	if !strings.Contains(txt, "SetMaxOpenConns(cfg.ServiceDBMaxConnection)") {
		t.Errorf("ledger block must render verbatim citation snippets")
	}
	// injection is deliberately independent of the aging flag (quality win on
	// its own — best arm in the pinned A/B even with full raw context).
	p.recentObservationWindow = 0
	if got := p.injectLedgerBlock(in); len(got) != len(in)+1 {
		t.Errorf("injection must NOT be gated on the aging flag")
	}
}
