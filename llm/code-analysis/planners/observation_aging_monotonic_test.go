package planners

import (
	"fmt"
	"strings"
	"testing"

	"github.com/tmc/langchaingo/llms"
)

// A pressure-gate flip (prompt dips back under budget) must NOT un-stub
// previously stubbed messages: un-stubbing rewrites the sent prefix and
// invalidates Gemini's implicit prompt cache from that point.
func TestAgeOldObservations_PressureGateFlipKeepsPriorStubs(t *testing.T) {
	const obs = 5000
	p := plannerWithSteps(3, 8, 8)
	p.agingBudgetTokens = 1000 // far below the prompt size → phase 2 fires
	in := buildConvo(8, obs)

	first := p.ageOldObservations(in)
	firstTools := toolMsgs(first)
	if got := len(p.stubbedObs); got != 5 {
		t.Fatalf("expected 5 stubs admitted (8 - window 3), got %d", got)
	}

	// Gate un-fires (huge budget). Prior stubs must replay byte-identically.
	p.agingBudgetTokens = 100_000_000
	second := p.ageOldObservations(in)
	secondTools := toolMsgs(second)
	for i := 0; i < 5; i++ {
		if toolContent(secondTools[i]) != toolContent(firstTools[i]) {
			t.Errorf("stub %d changed after gate flip — prefix instability", i)
		}
		if !strings.Contains(toolContent(secondTools[i]), "ELIDED") {
			t.Errorf("stub %d was un-stubbed after gate flip", i)
		}
	}
	for i := 5; i < 8; i++ {
		if len(toolContent(secondTools[i])) != obs {
			t.Errorf("never-stubbed message %d must stay full", i)
		}
	}
	if got := len(p.stubbedObs); got != 5 {
		t.Errorf("sticky set must not change on gate flip, got %d", got)
	}
}

// The sticky set may only grow as the conversation and watermark advance.
func TestAgeOldObservations_NewStubsMonotonic(t *testing.T) {
	const obs = 5000
	p := plannerWithSteps(3, 8, 3) // watermark 3 → only steps 1-3 stubbable
	p.agingBudgetTokens = 1000
	p.ageOldObservations(buildConvo(8, obs))
	if got := len(p.stubbedObs); got != 3 {
		t.Fatalf("expected 3 stubs at watermark 3, got %d", got)
	}

	// Conversation grows to 10 rounds, watermark advances to 10.
	for i := 9; i <= 10; i++ {
		p.toolMsgMeta = append(p.toolMsgMeta, toolMsgMeta{maxStep: i, desc: fmt.Sprintf("file_view f%d.go", i)})
	}
	p.lastReflectedStep = 10
	p.ageOldObservations(buildConvo(10, obs))
	if got := len(p.stubbedObs); got != 7 {
		t.Fatalf("expected 7 stubs (10 - window 3), got %d", got)
	}
	for k := 0; k < 3; k++ {
		if !p.stubbedObs[k] {
			t.Errorf("earlier stub %d must remain in the set", k)
		}
	}
}

// Sticky stubs apply even when phase-2 gates would take an early-out
// (no reflection yet, tool count within the window, gate un-fired).
func TestAgeOldObservations_StickyAppliedDespiteEarlyOuts(t *testing.T) {
	const obs = 5000
	p := plannerWithSteps(3, 3, 0) // watermark 0 → phase 2 never fires
	p.stubbedObs = map[int]bool{0: true}
	p.agingBudgetTokens = 100_000_000

	out := toolMsgs(p.ageOldObservations(buildConvo(3, obs)))
	if !strings.Contains(toolContent(out[0]), "ELIDED") {
		t.Errorf("sticky stub must apply despite watermark early-out and recent-K exemption")
	}
	for i := 1; i < 3; i++ {
		if len(toolContent(out[i])) != obs {
			t.Errorf("non-sticky message %d must stay full", i)
		}
	}
}

// After a semantic compaction removes early messages, surviving stubs (keyed
// by meta index, suffix-aligned) must stay stubbed — even when compaction
// shrinks the tool count into the recent-K window.
func TestAgeOldObservations_StickySurvivesCompaction(t *testing.T) {
	const obs = 5000
	p := plannerWithSteps(3, 8, 8)
	p.agingBudgetTokens = 1000
	p.ageOldObservations(buildConvo(8, obs))
	if len(p.stubbedObs) != 5 {
		t.Fatalf("precondition: expected 5 stubs, got %d", len(p.stubbedObs))
	}

	// Simulate semantic compaction: header + summary + last 3 rounds survive.
	// toolMsgMeta is untouched (append-only) — suffix alignment maps the 3
	// surviving Tool messages to meta indices 5,6,7 (never stubbed).
	full := buildConvo(8, obs)
	compacted := append([]llms.MessageContent{}, full[0], full[1],
		llms.MessageContent{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextContent{Text: "<investigation_summary>...</investigation_summary>"}}})
	compacted = append(compacted, full[len(full)-6:]...) // last 3 (AI, Tool) pairs

	p.agingBudgetTokens = 100_000_000 // gate off — only sticky application runs
	out := toolMsgs(p.ageOldObservations(compacted))
	if len(out) != 3 {
		t.Fatalf("expected 3 surviving tool messages, got %d", len(out))
	}
	for i, m := range out {
		if len(toolContent(m)) != obs {
			t.Errorf("surviving message %d (meta %d, never stubbed) must stay full — misattribution", i, 5+i)
		}
	}
	if len(p.stubbedObs) != 5 {
		t.Errorf("sticky set must survive compaction untouched, got %d", len(p.stubbedObs))
	}

	// Now a stubbed survivor: compact away rounds 1-4, keeping 5-8 → meta 4
	// (stubbed) is inside the surviving window and must STAY stubbed even
	// though it now falls within recent-K.
	compacted2 := append([]llms.MessageContent{}, full[0], full[1])
	compacted2 = append(compacted2, full[len(full)-8:]...) // last 4 (AI, Tool) pairs
	out2 := toolMsgs(p.ageOldObservations(compacted2))
	if len(out2) != 4 {
		t.Fatalf("expected 4 surviving tool messages, got %d", len(out2))
	}
	if !strings.Contains(toolContent(out2[0]), "ELIDED") {
		t.Errorf("surviving stubbed message (meta 4) must stay stubbed after compaction")
	}
	for i := 1; i < 4; i++ {
		if len(toolContent(out2[i])) != obs {
			t.Errorf("surviving unstubbed message %d must stay full", i)
		}
	}
}
