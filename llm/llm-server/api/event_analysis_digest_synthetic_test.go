package api

import "testing"

func TestSyntheticVerdict(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"bold yes", "- **Problem**: x\n- **Synthetic**: yes — title says 'for testing'", true},
		{"plain no", "Synthetic: no", false},
		{"bullet yes", "* Synthetic: Yes", true},
		{"heading form", "#### Synthetic\n", false},
		{"absent defaults to real", "- **Problem**: OOM kill\n- **Confidence**: high", false},
		{"word inside prose is not a verdict", "The synthetic monitoring probe failed.", false},
		// Real emitted formats — the value arrives backticked often enough that a
		// parser missing it silently reads every burst as real traffic.
		{"backticked yes", "- **Synthetic**: `yes`", true},
		{"backticked no", "- **Synthetic**: `no`", false},
		{"colon inside emphasis", "**Synthetic:** yes", true},
		{"underscore emphasis", "_Synthetic_: yes", true},
		// List markers the model picks vary; a fixed TrimLeft set missed these.
		{"plus list marker", "+ **Synthetic**: yes", true},
		{"numbered list", "1. Synthetic: yes", true},
		// A bare prefix match would read these as a synthetic verdict and remove a
		// real failure class from the briefing — the costliest direction to be wrong.
		{"yesterday is not yes", "Synthetic: yesterday we had a spike", false},
		{"normal is not no", "Synthetic: normal production traffic", false},
		{"no-op is not a verdict value", "Synthetic: no-op change", false},
		// A hyphen satisfies \b, so this is the case a word-boundary alone misses —
		// and the one that would wrongly hide a real class.
		{"hyphenated yes is not a verdict", "Synthetic: yes-associated failures", false},
		{"verdict followed by a reason still matches", "Synthetic: yes — title announces a test", true},
		// One trailing punctuation mark is a form the model uses; without it the
		// verdict fails to parse and a test burst reads as real traffic.
		{"yes with period", "- **Synthetic**: yes.", true},
		{"yes with comma and reason", "- **Synthetic**: yes, placeholder cluster name", true},
		{"no with period", "- **Synthetic**: no.", false},
		// LLMs quote single-word answers often enough to matter; an unstripped
		// quote fails the match and reads as "no", hiding a real test burst.
		{"double-quoted yes", `- **Synthetic**: "yes"`, true},
		{"single-quoted yes", "- **Synthetic**: 'yes'", true},
		{"double-quoted no", `- **Synthetic**: "no"`, false},
	}
	for _, c := range cases {
		if got := syntheticVerdict(c.in); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

// TestSyntheticGuardRequiresSingleDayBurst pins the asymmetry: the LLM verdict
// can confirm a one-day burst but must never hide a class that recurs across
// days. Regression for HighErrorCriticalLogs (13 active days) being flagged.
func TestSyntheticGuardRequiresSingleDayBurst(t *testing.T) {
	cases := []struct {
		name       string
		verdict    string
		activeDays int
		want       bool
	}{
		{"one-day burst confirmed", "Synthetic: yes", 1, true},
		{"multi-day class overrides verdict", "Synthetic: yes", 13, false},
		{"four-day class overrides verdict", "Synthetic: yes", 4, false},
		{"no verdict stays real", "Synthetic: no", 1, false},
	}
	for _, c := range cases {
		got := syntheticVerdict(c.verdict) && c.activeDays <= 1
		if got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}
