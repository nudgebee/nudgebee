package triage

import (
	"context"
	"math"
	"strings"

	"nudgebee/services/internal/database/models"

	"github.com/jmoiron/sqlx"
)

// SignalVerdict is the per-signal-class semantic categorization produced by the LLM
// and cached in triage_signal_class. The LLM produces ONLY this verdict; the numeric
// score is derived deterministically by computeVerdictScore. This split keeps scoring
// reproducible and auditable (the LLM interprets, a rule decides).
type SignalVerdict struct {
	Category            string  `db:"category" json:"category"`
	Intrinsic           string  `db:"intrinsic" json:"intrinsic"`                       // critical|high|medium|low|info
	Blast               string  `db:"blast" json:"blast"`                               // control_plane|customer_facing|data_durability|monitoring_backbone|single_workload|expected_change
	RecurrenceSemantics string  `db:"recurrence_semantics" json:"recurrence_semantics"` // escalating|neutral|noise
	EnvSensitivity      string  `db:"env_sensitivity" json:"env_sensitivity"`           // none|partial|full
	BandFloor           string  `db:"band_floor" json:"band_floor"`                     // P0..P3
	BandCeiling         string  `db:"band_ceiling" json:"band_ceiling"`
	Confidence          float64 `db:"confidence" json:"confidence"`
	Reasoning           string  `db:"reasoning" json:"reasoning"`
	SourceModel         string  `db:"source_model" json:"source_model"`
	// PromptVersion stamps the classifier prompt the verdict was minted under. A served verdict
	// older than the current classificationPromptVersion is lazily re-minted (flag-gated).
	PromptVersion int `db:"prompt_version" json:"prompt_version"`
}

// Deterministic policy weights. env is ADDITIVE (not a multiplicative cap, which floored
// 94% of alerts to P3); recurrence ESCALATES crash/chronic classes and DAMPENS known noise;
// blast radius adjusts; the final score is clamped inside the LLM-chosen priority band.
var intrinsicBase = map[string]int{
	"critical": 80, "high": 62, "medium": 42, "low": 24, "info": 10,
}

var envPenalty = map[string]int{
	"prod": 0, "unknown": -6, "non_prod": -15,
}

var envSensitivityScale = map[string]float64{
	"none": 0.0, "partial": 0.5, "full": 1.0,
}

var blastAdj = map[string]int{
	"control_plane": 10, "customer_facing": 10, "data_durability": 8,
	"monitoring_backbone": 6, "single_workload": 0, "expected_change": -8,
}

var bandFloorScore = map[string]int{"P0": 80, "P1": 60, "P2": 40, "P3": 0}
var bandCeilScore = map[string]int{"P0": 100, "P1": 79, "P2": 59, "P3": 39}

// clampToBand re-asserts the LLM verdict's priority band on a score. score_factors carries the
// band as "<floor>..<ceiling>" (least-severe..most-severe). Additive scoring rules run AFTER the
// verdict policy and could otherwise push the score past the ceiling the model chose (e.g. a dev
// disk alert the model capped at P1 landing at P0). This re-clamps after those rules. Events with
// no band (legacy path) are returned unchanged.
func clampToBand(score int, factors map[string]interface{}) int {
	band, ok := factors["band"].(string)
	if !ok || band == "" {
		return score
	}
	parts := strings.SplitN(band, "..", 2)
	if len(parts) != 2 {
		return score
	}
	floor, okFloor := bandFloorScore[strings.ToUpper(strings.TrimSpace(parts[0]))]
	ceil, okCeil := bandCeilScore[strings.ToUpper(strings.TrimSpace(parts[1]))]
	// Reject a malformed (typo'd key) or inverted (floor > ceiling) band rather than clamp to a
	// nonsensical range — a bad verdict band should leave the score untouched, not corrupt it.
	if !okFloor || !okCeil || floor > ceil {
		return score
	}
	return clamp(score, floor, ceil)
}

// recurrenceAdjustment raises severity for crash/chronic classes and lowers it for known
// noise, by recurrence-count tier. The current formula does the opposite (a flat penalty),
// which zeroes recurring OOMs — the exact bug this replaces.
func recurrenceAdjustment(count int, semantics string) int {
	if semantics == "neutral" || count <= 1 {
		return 0
	}
	tier := 2
	switch {
	case count >= 50:
		tier = 15
	case count >= 10:
		tier = 10
	case count >= 3:
		tier = 6
	}
	if semantics == "noise" {
		return -tier
	}
	return tier // escalating (and any unknown semantics default to neutral handling above)
}

// correlationTypeAdjustment maps a correlation type to a contextual cascade adjustment applied
// AFTER the intrinsic band: a likely root cause stays prominent; downstream/upstream symptoms
// and same-resource/same-service co-occurrences are dampened so a cascade collapses toward its
// root instead of flooding the queue. NOTE: `same_resource` is non-directional, so it only gets
// a mild dampen here — true root-vs-downstream identification within a same_resource cluster is
// the Phase-2 LLM causal DAG. (Also fixes the legacy bug where same_resource fell through to 0.)
func correlationTypeAdjustment(corrType string) int {
	switch corrType {
	case "likely_root_cause":
		return CorrelationBonusRootCause // +15, surface the root
	case "downstream_impact", "upstream_dependency":
		return CorrelationPenaltyDownstream // -10, dampen the symptom
	case "same_service", "same_resource":
		return CorrelationPenaltySameService // -5, mild dampen for co-occurrence
	default:
		return 0
	}
}

// computeVerdictScore turns a (cached) LLM verdict + live context into a 0-100 score and
// a P-level, bounded inside the verdict's priority band. Pure function — no I/O.
func computeVerdictScore(v *SignalVerdict, envCategory string, recurrenceCount int) (int, string, map[string]interface{}) {
	base, ok := intrinsicBase[strings.ToLower(v.Intrinsic)]
	if !ok {
		base = intrinsicBase["low"]
	}

	envPen := 0
	if p, ok := envPenalty[strings.ToLower(envCategory)]; ok {
		envPen = p
	}
	sens := envSensitivityScale["partial"]
	if s, ok := envSensitivityScale[strings.ToLower(v.EnvSensitivity)]; ok {
		sens = s
	}
	envTerm := int(math.Round(float64(envPen) * sens))

	blast := blastAdj[strings.ToLower(v.Blast)] // missing -> 0 (single_workload)
	recur := recurrenceAdjustment(recurrenceCount, strings.ToLower(v.RecurrenceSemantics))

	raw := base + envTerm + blast + recur

	floor := bandFloorScore[strings.ToUpper(v.BandFloor)]     // missing -> P3 floor (0)
	ceil, ok := bandCeilScore[strings.ToUpper(v.BandCeiling)] // missing -> treat as no ceiling
	if !ok {
		ceil = 100
	}
	score := clamp(clamp(raw, floor, ceil), 0, 100)

	factors := map[string]interface{}{
		"scoring_path":         "llm_verdict",
		"category":             v.Category,
		"intrinsic":            v.Intrinsic,
		"intrinsic_base":       base,
		"env_category":         envCategory,
		"env_sensitivity":      v.EnvSensitivity,
		"env_adjustment":       envTerm,
		"blast":                v.Blast,
		"blast_adjustment":     blast,
		"recurrence_semantics": v.RecurrenceSemantics,
		"recurrence_count":     recurrenceCount,
		"recurrence_adjust":    recur,
		"raw_score":            raw,
		"band":                 v.BandFloor + ".." + v.BandCeiling,
		"verdict_source":       v.SourceModel,
		"verdict_confidence":   v.Confidence,
		"reasoning":            v.Reasoning, // human-readable "why" for the UI/score breakdown
		"final_score":          score,
	}
	return score, scoreToPriority(score), factors
}

// fallbackVerdict derives a conservative verdict from the alert's own priority when no
// LLM verdict is cached yet (first event of a never-seen class). It applies the env-additive
// and band fixes but cannot semantically discriminate noise — so it is intentionally bounded
// and flagged source="fallback". Subsequent events of the class use the minted verdict.
func fallbackVerdict(event *models.Event) *SignalVerdict {
	intrinsic := "low"
	if event.Priority != nil {
		switch strings.ToUpper(*event.Priority) {
		case "HIGH":
			intrinsic = "high"
		case "MEDIUM":
			intrinsic = "medium"
		case "LOW":
			intrinsic = "low"
		case "INFO", "DEBUG":
			intrinsic = "info"
		}
	}
	return &SignalVerdict{
		Category:            "unclassified",
		Intrinsic:           intrinsic,
		Blast:               "single_workload",
		RecurrenceSemantics: "neutral",
		EnvSensitivity:      "partial",
		BandFloor:           "P3",
		BandCeiling:         "P1",
		Confidence:          0.3,
		SourceModel:         "fallback",
	}
}

// isProviderLifecycleNotification reports whether an alert is a cloud-provider lifecycle /
// state-change NOTIFICATION (e.g. AWS EventBridge "EC2 Instance State-change Notification",
// EBS/AutoScaling state-change events) rather than a real failure alarm. These are informational
// by nature — an instance entering 'stopped' is not itself an incident; the impact, if any,
// surfaces as separate symptom alerts (a workload going unschedulable, a service alerting). The
// per-class LLM verdict can't see the source and sometimes mis-rates these as control-plane
// incidents, so they need a deterministic floor. Real metric alarms (CloudWatch / GCP metric
// alerts) are deliberately excluded — those carry a genuine condition.
func isProviderLifecycleNotification(event *models.Event) bool {
	if event == nil || event.AggregationKey == nil {
		return false
	}
	agg := strings.ToLower(*event.AggregationKey)
	// A CloudWatch/metric alarm rides EventBridge too, but it represents a real threshold breach,
	// not a lifecycle transition — never floor it.
	if strings.Contains(agg, "alarm") || strings.Contains(agg, "cloudwatch") {
		return false
	}
	// Lifecycle / state-change / scheduled-change notifications.
	return strings.Contains(agg, "state-change notification") ||
		strings.Contains(agg, "state change notification") ||
		strings.Contains(agg, "instance interruption") || // spot interruption warning
		strings.Contains(agg, "scheduled change")
}

// applyDeterministicGuardrails adjusts a (cached, per-class) verdict for deterministic, event-level
// facts the LLM verdict can't see, returning a possibly-new verdict plus the names of the guardrails
// applied (for the score breakdown / audit). It NEVER mutates the input verdict — a copy is taken
// before any change, so the shared cached verdict pointer is left intact.
//
// Currently one guardrail: provider lifecycle/state-change notifications are floored to
// info/expected_change and pinned to the P3 band, regardless of how the LLM classified the class.
// A genuinely impactful one is still free to rise via correlation cascade dampening downstream (if
// it is recorded as a correlated root cause), which is the "unless there is correlated failure
// evidence" carve-out.
func applyDeterministicGuardrails(v *SignalVerdict, event *models.Event) (*SignalVerdict, []string) {
	var applied []string
	if isProviderLifecycleNotification(event) {
		nv := *v
		nv.Intrinsic = "info"
		nv.Blast = "expected_change"
		nv.BandFloor = "P3"
		nv.BandCeiling = "P3"
		v = &nv
		applied = append(applied, "lifecycle_floor")
	}
	return v, applied
}

// getEnvironmentCategory returns prod|non_prod|unknown for a cloud account (the additive
// policy needs the category, not the legacy multiplier).
func getEnvironmentCategory(ctx context.Context, db *sqlx.DB, cloudAccountID string) string {
	if cloudAccountID == "" {
		return "unknown"
	}
	var accountEnv string
	if err := db.GetContext(ctx, &accountEnv, `SELECT account_env FROM cloud_accounts WHERE id = $1`, cloudAccountID); err != nil {
		return "unknown"
	}
	switch strings.ToLower(accountEnv) {
	case "prod":
		return "prod"
	case "non_prod", "non-prod":
		return "non_prod"
	default:
		return "unknown"
	}
}
