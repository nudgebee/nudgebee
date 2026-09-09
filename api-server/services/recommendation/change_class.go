package recommendation

import (
	"encoding/json"
	"strings"
)

// ChangeClass grades what a recommendation does to its resource, independent of
// who depends on it. It is the first axis of the safety verdict: an additive
// change cannot take capacity away from callers, a reductive one can degrade
// them, a destructive one removes the resource outright. Unknown means the rule
// was not classified — the band then behaves exactly as it did before change
// awareness existed, so an unclassified rule is never accidentally loosened.
type ChangeClass string

const (
	// ChangeClassAdditive — only adds capacity or commitments (request
	// increases, RI/SP purchases). Callers cannot be starved by it; the
	// remaining risk is the mechanics of applying (e.g. a rolling restart).
	ChangeClassAdditive ChangeClass = "additive"
	// ChangeClassReductive — shrinks or reshapes something callers rely on
	// (request cuts, replica/volume downsizing, instance-family moves, spot).
	ChangeClassReductive ChangeClass = "reductive"
	// ChangeClassDestructive — removes the resource (volume deletes, idle
	// instance cleanup). Irreversible, so it floors the band at risky.
	ChangeClassDestructive ChangeClass = "destructive"
	// ChangeClassUnknown — unclassified; the band falls back to the
	// pre-change-aware policy.
	ChangeClassUnknown ChangeClass = ""
)

// destructiveRules are cleanup rules whose remediation removes (or terminates)
// the resource. Exact names only, verified against the emitting collector's
// RuleName literal (not the drifted constants tables) — a wrong entry here
// over-alarms, a missing one just keeps today's behavior. GCP-native rules are
// persisted with the collector's gcp_native_ prefix, so the prefixed spelling
// is the one that exists in the recommendation table.
var destructiveRules = map[string]bool{
	"unused_pvc":                                true,
	"abandoned_resource":                        true,
	"abandoned-resources":                       true,
	"aws_ec2_idle_instance":                     true,
	"aws_ec2_orphaned_volume":                   true,
	"aws_rds_idle_instance":                     true,
	"aws_elasticache_idle_instance":             true,
	"aws_redshift_idle_cluster":                 true,
	"azure_disk_unattached_volume":              true,
	"gcp_native_compute_instance_idle_resource": true,
	"gcp_native_compute_disk_idle_resource":     true,
}

// additiveRules are pure purchasing/commitment recommendations — nothing about
// the running resource changes, so callers are untouched by construction.
var additiveRules = map[string]bool{
	"aws_native_purchase_reserved_instances":    true,
	"aws_native_purchase_savings_plans":         true,
	"aws_native_ce_savings_plan_recommendation": true,
	"aws_native_database_savings_plan":          true,
}

// reductiveRulePatterns catch the rightsizing/downsizing family by substring
// (pv_rightsize, replica-rightsizing, azure_sql_large_dtu_rightsizing,
// *_oversized_compute, spot conversions). pod_right_sizing is handled before
// patterns run because its direction is decidable from the payload.
var reductiveRulePatterns = []string{"rightsiz", "right_siz", "oversized", "spot"}

// ClassifyChange grades a recommendation's change from its rule name and, for
// pod_right_sizing, its payload deltas. recommendation may be nil for rules
// that classify statically.
func ClassifyChange(ruleName string, recommendation []byte) ChangeClass {
	name := strings.ToLower(strings.TrimSpace(ruleName))
	if destructiveRules[name] {
		return ChangeClassDestructive
	}
	if additiveRules[name] {
		return ChangeClassAdditive
	}
	if name == "pod_right_sizing" {
		return classifyPodRightSizing(recommendation)
	}
	for _, p := range reductiveRulePatterns {
		if strings.Contains(name, p) {
			return ChangeClassReductive
		}
	}
	return ChangeClassUnknown
}

// podResourceEntry is one per-resource change in the pod_right_sizing payload —
// the same shape the UI's rightSizingData.ts parses: the blob is keyed by
// container name, each value an array of {resource, allocated{request},
// recommended{request}} entries (legacy shape: a top-level `notifications`
// array holding one container's entries).
type podResourceEntry struct {
	Resource    string          `json:"resource"`
	Allocated   podResourceSpec `json:"allocated"`
	Recommended podResourceSpec `json:"recommended"`
}

type podResourceSpec struct {
	Request *float64 `json:"request"`
}

// classifyPodRightSizing decides direction from the request deltas: any
// decrease makes the change reductive; only increases (or setting a request
// where none exists) makes it additive; a payload with no decidable delta —
// including KRR-null data — stays unknown rather than guessing.
func classifyPodRightSizing(recommendation []byte) ChangeClass {
	entries := parsePodRightSizingEntries(recommendation)
	decreases, increases := 0, 0
	for _, e := range entries {
		rec := e.Recommended.Request
		cur := e.Allocated.Request
		switch {
		case rec == nil:
		case cur == nil:
			increases++ // setting an unset request only adds a guarantee
		case *rec < *cur:
			decreases++
		case *rec > *cur:
			increases++
		}
	}
	switch {
	case decreases > 0:
		return ChangeClassReductive
	case increases > 0:
		return ChangeClassAdditive
	default:
		return ChangeClassUnknown
	}
}

func parsePodRightSizingEntries(recommendation []byte) []podResourceEntry {
	if len(recommendation) == 0 {
		return nil
	}
	// Legacy shape first: {"notifications": [...]}.
	var legacy struct {
		Notifications []podResourceEntry `json:"notifications"`
	}
	if err := json.Unmarshal(recommendation, &legacy); err == nil && len(legacy.Notifications) > 0 {
		return legacy.Notifications
	}
	// Current shape: {"<container>": [entries], ...} with non-container keys
	// (strings, numbers) interleaved — decode per key, then per element: the
	// rightsizer emits "?" as its NaN placeholder for an unreadable request,
	// which fails the *float64 decode, and one bad entry must not discard its
	// container's other, decidable deltas.
	var byContainer map[string]json.RawMessage
	if err := json.Unmarshal(recommendation, &byContainer); err != nil {
		return nil
	}
	var out []podResourceEntry
	for _, raw := range byContainer {
		var elements []json.RawMessage
		if err := json.Unmarshal(raw, &elements); err != nil {
			continue
		}
		for _, el := range elements {
			var e podResourceEntry
			if err := json.Unmarshal(el, &e); err != nil {
				continue
			}
			if e.Resource != "" {
				out = append(out, e)
			}
		}
	}
	return out
}
