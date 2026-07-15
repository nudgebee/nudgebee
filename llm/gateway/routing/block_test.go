package routing

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResolve_Block(t *testing.T) {
	e := NewEngine([]Rule{{
		ID: "block-fable", Enabled: true, Priority: 10,
		Match:  Match{Provider: "anthropic", Model: "claude-fable-5"},
		Target: Target{Deny: true, Endpoint: Endpoint{Model: "claude-sonnet-4.6"}}, // suggested alternative
	}})
	d := e.Resolve(Input{Provider: "anthropic", Model: "claude-fable-5"})
	assert.True(t, d.Denied)
	assert.Equal(t, ReasonBlocked, d.Reason)
	assert.Equal(t, "block-fable", d.RuleID)
	assert.Equal(t, "claude-fable-5", d.RequestedModel)
	assert.Equal(t, "claude-sonnet-4.6", d.ResolvedModel) // surfaced as "use X instead"
}

func TestResolve_BlockNoSuggestion(t *testing.T) {
	e := NewEngine([]Rule{{
		ID: "block", Enabled: true, Priority: 10,
		Match:  Match{Model: "claude-fable-5"},
		Target: Target{Deny: true},
	}})
	d := e.Resolve(Input{Provider: "anthropic", Model: "claude-fable-5"})
	assert.True(t, d.Denied)
	assert.Equal(t, ReasonBlocked, d.Reason)
	assert.Empty(t, d.ResolvedModel) // no alternative offered
}

func TestResolve_BlockWinsPriorityTie(t *testing.T) {
	// A redirect and a block, SAME priority, both matching. Block must win — even
	// though the block's id sorts AFTER the redirect's (proves deny-first beats the
	// id tiebreak, not just input/id order).
	redirect := Rule{ID: "aaa-redirect", Enabled: true, Priority: 10,
		Match: Match{Model: "claude-fable-5"}, Target: Target{Endpoint: Endpoint{Model: "claude-sonnet-4.6"}}}
	block := Rule{ID: "zzz-block", Enabled: true, Priority: 10,
		Match: Match{Model: "claude-fable-5"}, Target: Target{Deny: true}}

	for _, order := range [][]Rule{{redirect, block}, {block, redirect}} {
		d := NewEngine(order).Resolve(Input{Provider: "anthropic", Model: "claude-fable-5"})
		assert.True(t, d.Denied, "block should win the priority tie regardless of input order")
		assert.Equal(t, "zzz-block", d.RuleID)
	}
}

func TestResolve_TiebreakStableInputOrder(t *testing.T) {
	// Two redirects, same priority + same (non-deny) status → the earlier rule in the
	// input wins (stable sort). The Store feeds a deterministic input (DB loader
	// ORDER BY + DB merged before config), so this is deterministic in practice — and
	// it's what lets DB rules win ties over config-file rules.
	a := Rule{ID: "aaa", Enabled: true, Priority: 10, Match: Match{Model: "m"}, Target: Target{Endpoint: Endpoint{Model: "A"}}}
	b := Rule{ID: "bbb", Enabled: true, Priority: 10, Match: Match{Model: "m"}, Target: Target{Endpoint: Endpoint{Model: "B"}}}
	assert.Equal(t, "A", NewEngine([]Rule{a, b}).Resolve(Input{Provider: "anthropic", Model: "m"}).ResolvedModel)
	assert.Equal(t, "B", NewEngine([]Rule{b, a}).Resolve(Input{Provider: "anthropic", Model: "m"}).ResolvedModel)
}

func TestResolve_HigherPriorityRedirectBeatsLowerPriorityBlock(t *testing.T) {
	// Deny only wins a TIE — a genuinely higher-priority (lower number) redirect still
	// wins over a lower-priority block.
	redirect := Rule{ID: "redirect", Enabled: true, Priority: 5,
		Match: Match{Model: "claude-fable-5"}, Target: Target{Endpoint: Endpoint{Model: "claude-sonnet-4.6"}}}
	block := Rule{ID: "block", Enabled: true, Priority: 10,
		Match: Match{Model: "claude-fable-5"}, Target: Target{Deny: true}}
	d := NewEngine([]Rule{block, redirect}).Resolve(Input{Provider: "anthropic", Model: "claude-fable-5"})
	assert.False(t, d.Denied)
	assert.Equal(t, "claude-sonnet-4.6", d.ResolvedModel)
}
