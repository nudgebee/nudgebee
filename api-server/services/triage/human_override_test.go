package triage

import (
	"testing"
	"time"
)

func strptr(s string) *string { return &s }

func TestAbsoluteHumanResult(t *testing.T) {
	cases := []struct {
		priority  string
		authority string
		ruleID    string
		wantScore int
		wantPrio  string
	}{
		{"P0", authorityHumanEvent, "", 80, "P0"},
		{"p1", authorityHumanClass, "rule-1", 60, "P1"},
		{" P2 ", authorityHumanEvent, "", 40, "P2"},
		{"P3", authorityHumanClass, "rule-2", 20, "P3"},
	}
	for _, c := range cases {
		got := absoluteHumanResult(c.priority, c.authority, c.ruleID)
		if got.Score != c.wantScore {
			t.Errorf("priority %q: score = %d, want %d", c.priority, got.Score, c.wantScore)
		}
		if got.Priority != c.wantPrio {
			t.Errorf("priority %q: priority = %q, want %q", c.priority, got.Priority, c.wantPrio)
		}
		if got.Confidence != 1.0 {
			t.Errorf("priority %q: confidence = %v, want 1.0", c.priority, got.Confidence)
		}
		if !IsHumanAuthority(got) {
			t.Errorf("priority %q: IsHumanAuthority = false, want true", c.priority)
		}
		if c.ruleID != "" && got.Factors["winning_rule_id"] != c.ruleID {
			t.Errorf("priority %q: winning_rule_id = %v, want %q", c.priority, got.Factors["winning_rule_id"], c.ruleID)
		}
		if c.ruleID == "" {
			if _, ok := got.Factors["winning_rule_id"]; ok {
				t.Errorf("priority %q: winning_rule_id present but ruleID empty", c.priority)
			}
		}
	}
}

func TestIsHumanAuthority(t *testing.T) {
	if IsHumanAuthority(nil) {
		t.Error("nil result should not be human authority")
	}
	if IsHumanAuthority(&ScoreResult{}) {
		t.Error("nil factors should not be human authority")
	}
	machine := &ScoreResult{Factors: map[string]interface{}{"authority": "machine"}}
	if IsHumanAuthority(machine) {
		t.Error("machine authority should not be human")
	}
	noAuth := &ScoreResult{Factors: map[string]interface{}{"scoring_path": "llm_verdict"}}
	if IsHumanAuthority(noAuth) {
		t.Error("missing authority should not be human")
	}
	human := &ScoreResult{Factors: map[string]interface{}{"authority": authorityHumanClass}}
	if !IsHumanAuthority(human) {
		t.Error("human:class authority should be human")
	}
}

func TestPinScopeRank(t *testing.T) {
	acct := &TriageRule{AccountID: strptr("a"), TenantID: strptr("t")}
	tenant := &TriageRule{TenantID: strptr("t")}
	system := &TriageRule{}
	if pinScopeRank(acct) != 2 {
		t.Errorf("account rank = %d, want 2", pinScopeRank(acct))
	}
	if pinScopeRank(tenant) != 1 {
		t.Errorf("tenant rank = %d, want 1", pinScopeRank(tenant))
	}
	if pinScopeRank(system) != 0 {
		t.Errorf("system rank = %d, want 0", pinScopeRank(system))
	}
}

func TestPinMoreSpecific(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Hour)

	// Scope dominates: account beats tenant even with worse rule.priority and older timestamp.
	acct := &TriageRule{AccountID: strptr("a"), TenantID: strptr("t"), Priority: 100, CreatedAt: t0}
	tenant := &TriageRule{TenantID: strptr("t"), Priority: 1, CreatedAt: t1}
	if !pinMoreSpecific(acct, tenant) {
		t.Error("account-scoped pin should beat tenant-scoped pin regardless of priority/recency")
	}
	if pinMoreSpecific(tenant, acct) {
		t.Error("tenant-scoped pin should NOT beat account-scoped pin")
	}

	// Same scope: lower rule.priority wins.
	lowPrio := &TriageRule{TenantID: strptr("t"), Priority: 1, CreatedAt: t0}
	highPrio := &TriageRule{TenantID: strptr("t"), Priority: 50, CreatedAt: t1}
	if !pinMoreSpecific(lowPrio, highPrio) {
		t.Error("lower rule.priority should win within same scope")
	}

	// Same scope and priority: newer created_at wins.
	older := &TriageRule{TenantID: strptr("t"), Priority: 10, CreatedAt: t0}
	newer := &TriageRule{TenantID: strptr("t"), Priority: 10, CreatedAt: t1}
	if !pinMoreSpecific(newer, older) {
		t.Error("newer rule should win when scope and priority tie")
	}
	if pinMoreSpecific(older, newer) {
		t.Error("older rule should not win when scope and priority tie")
	}
}

// TestSelectWinningPin exercises the same selection loop resolvePriorityPin uses (best-of via
// pinMoreSpecific) over a mixed slice, verifying a single deterministic winner.
func TestSelectWinningPin(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rules := []TriageRule{
		{ID: "sys", RuleType: RuleTypePriorityPin, Priority: 1, CreatedAt: t0, ActionValue: pinAV("P3")},
		{ID: "tenant", RuleType: RuleTypePriorityPin, TenantID: strptr("t"), Priority: 5, CreatedAt: t0, ActionValue: pinAV("P2")},
		{ID: "acct-old", RuleType: RuleTypePriorityPin, AccountID: strptr("a"), TenantID: strptr("t"), Priority: 10, CreatedAt: t0, ActionValue: pinAV("P1")},
		{ID: "acct-new", RuleType: RuleTypePriorityPin, AccountID: strptr("a"), TenantID: strptr("t"), Priority: 10, CreatedAt: t0.Add(time.Hour), ActionValue: pinAV("P0")},
		{ID: "noise-scoring", RuleType: RuleTypeScoring, AccountID: strptr("a"), TenantID: strptr("t"), Priority: 0, CreatedAt: t0},
	}
	var best *TriageRule
	for i := range rules {
		r := &rules[i]
		if r.RuleType != RuleTypePriorityPin {
			continue
		}
		if best == nil || pinMoreSpecific(r, best) {
			best = r
		}
	}
	if best == nil || best.ID != "acct-new" {
		t.Fatalf("winning pin = %v, want acct-new", best)
	}
	av, err := ParseActionValue(best.ActionValue)
	if err != nil || av == nil || av.Priority == nil || *av.Priority != "P0" {
		t.Fatalf("winning pin priority = %v (err %v), want P0", av, err)
	}
}

func pinAV(priority string) *string {
	s := `{"priority":"` + priority + `"}`
	return &s
}

func TestIsValidPriority(t *testing.T) {
	valid := []string{"P0", "P1", "P2", "P3", "p0", " P2 ", "p3"}
	for _, s := range valid {
		if !isValidPriority(s) {
			t.Errorf("isValidPriority(%q) = false, want true", s)
		}
	}
	invalid := []string{"", "P4", "P", "0", "HIGH", "critical", "p"}
	for _, s := range invalid {
		if isValidPriority(s) {
			t.Errorf("isValidPriority(%q) = true, want false", s)
		}
	}
}
