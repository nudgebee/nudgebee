package triage

import (
	"context"
	"fmt"

	"github.com/jmoiron/sqlx"
)

// Wait-time advice: how much longer a rule should wait before firing, so that firings which
// clear on their own within minutes never fire at all.
//
// It is derived from how long the rule's own firings lasted, not from the metric. A firing that
// lasted D seconds means the condition held for the rule's wait plus D; waiting Δ longer drops
// every firing with D < Δ and delays the rest by Δ. So the advice is "current wait + Δ", and how
// many firings it would have dropped is a count, not an estimate.

// ShortFiringCount is how many of a rule's firings in the analysis window cleared within a given
// time of starting.
type ShortFiringCount struct {
	WithinSeconds int `json:"within_seconds"`
	Count         int `json:"count"`
}

// waitLadder is the set of extra waits considered, shortest first. cutoff is the firing duration
// counted as "within" the step: the two longest carry slack because alert routers deliver the
// "resolved" notice on a fixed interval, so a firing that cleared at the five-minute mark is
// recorded a little past it.
var waitLadder = []struct{ within, cutoff int }{
	{60, 60},
	{120, 120},
	{180, 180},
	{300, 330},
	{600, transientCutoffSeconds},
}

const (
	// transientCutoffSeconds: a firing shorter than this is "transient" (10 minutes plus slack).
	transientCutoffSeconds = 660
	// maxSuggestedWaitMinutes: no advice makes a rule wait longer than this before it fires.
	maxSuggestedWaitMinutes = 30
	// shortFiringCoverage: the extra wait chosen is the shortest that covers this share of the
	// transient firings.
	shortFiringCoverage = 0.9
)

// waitAdvice is a suggested wait for one rule and the evidence behind it.
type waitAdvice struct {
	CurrentSeconds   int // the rule's wait today
	SuggestedMinutes int // the wait to set (the apply path writes whole minutes)
	WithinSeconds    int // the extra wait chosen from the ladder
	Removed          int // firings in the window that cleared within WithinSeconds
	Total            int // all firings in the window
}

// currentWait returns how long a rule waits before firing today, and whether that is known.
// Prometheus rules carry it as `for`; cloud alarms as period × evaluation periods.
func currentWait(alertDef *AlertDefinition) (int, bool) {
	if alertDef == nil {
		return 0, false
	}
	if alertDef.forKnown {
		return alertDef.ForSeconds, true
	}
	if alertDef.Period > 0 {
		periods := alertDef.EvaluationPeriods
		if periods < 1 {
			periods = 1
		}
		return alertDef.Period * periods, true
	}
	return 0, false
}

// suggestWait returns wait-time advice for a rule, or nil when its firing history does not
// support any: most firings must be transient, the rule's current wait must be known, and the
// result must stay within maxSuggestedWaitMinutes. At least half of all firings must have cleared
// at all.
func suggestWait(q *AlertQualityScore, currentSeconds int, known bool) *waitAdvice {
	if q == nil || !known || q.TransientRate <= 0.7 || len(q.ShortFirings) == 0 {
		return nil
	}
	// The transient rate is a share of the firings that have a start and a later end. When fewer
	// than half do (a rule that never resolves, or a source that reports no end time), a handful
	// of short firings says nothing about the rest, and waiting longer cannot be shown to help.
	if q.ResolvedCount*2 < q.TotalCount {
		return nil
	}
	transient := q.ShortFirings[len(q.ShortFirings)-1].Count
	if transient == 0 {
		return nil
	}
	for _, step := range q.ShortFirings {
		if float64(step.Count) < shortFiringCoverage*float64(transient) {
			continue
		}
		suggested := (currentSeconds + step.WithinSeconds + 59) / 60
		if suggested > maxSuggestedWaitMinutes {
			return nil
		}
		return &waitAdvice{
			CurrentSeconds:   currentSeconds,
			SuggestedMinutes: suggested,
			WithinSeconds:    step.WithinSeconds,
			Removed:          step.Count,
			Total:            q.TotalCount,
		}
	}
	return nil
}

// reason words the advice with its evidence.
func (a *waitAdvice) reason() string {
	instead := "instead of " + formatWait(a.CurrentSeconds)
	if a.CurrentSeconds == 0 {
		instead = "instead of firing at once"
	}
	return fmt.Sprintf("About %d of %d firings in the last 30 days cleared within %s of starting. Waiting %s before firing %s would have left them out.",
		a.Removed, a.Total, formatWait(a.WithinSeconds), formatWait(a.SuggestedMinutes*60), instead)
}

// applyTo records the advice on a suggestion.
func (a *waitAdvice) applyTo(s *ThresholdSuggestion) {
	s.SuggestedDuration = a.SuggestedMinutes
	s.CurrentDurationSeconds = a.CurrentSeconds
	s.DurationReason = a.reason()
}

// waitFromHistoryNote is added to a verdict's reason when wait-time advice is given on firing
// history alone, beside a metric-based verdict that changes nothing.
const waitFromHistoryNote = "Most of its firings clear within minutes, though, so a longer wait before firing is suggested from its firing history alone."

// offerWait turns a verdict that changes nothing into wait-time advice when the rule's firing
// history supports it. Whether a threshold can be judged depends on the metric (is it readable,
// does it line up with the firings, is the rule a level at all); whether firings are short does
// not. So "not enough data", "review", "investigate" and "nothing to tune" still get a wait to set
// when most firings clear within minutes — the remediation those verdicts otherwise leave unsaid.
func offerWait(s *ThresholdSuggestion, alertDef *AlertDefinition, q *AlertQualityScore) {
	if s == nil || offersNumber(s.RecommendationType) || s.SuggestedDuration > 0 {
		return
	}
	if s.RecommendationType == "increase_duration" || s.RecommendationType == "disable" {
		return
	}
	current, known := currentWait(alertDef)
	advice := suggestWait(q, current, known)
	if advice == nil {
		return
	}
	s.RecommendationType = "increase_duration"
	advice.applyTo(s)
	if s.Reason != "" {
		s.Reason += " "
	}
	s.Reason += waitFromHistoryNote
	// Any risk recorded was about a threshold value this verdict no longer carries.
	s.RiskLevel = ""
	s.RiskWarnings = nil
	s.Confidence = "low"
}

// formatWait renders a wait in the units a rule is written in: "30s", "2m", "1m30s", "1h".
func formatWait(seconds int) string {
	if seconds <= 0 {
		return "0s"
	}
	h, m, s := seconds/3600, (seconds%3600)/60, seconds%60
	out := ""
	if h > 0 {
		out += fmt.Sprintf("%dh", h)
	}
	if m > 0 {
		out += fmt.Sprintf("%dm", m)
	}
	if s > 0 {
		out += fmt.Sprintf("%ds", s)
	}
	return out
}

// lookupRuleWait reads a Prometheus-type rule's wait straight from its stored definition, for
// rules whose expression yields no threshold (several conditions, not a comparison). Wait advice
// needs only the firing history and this number, so such rules can still get it.
func lookupRuleWait(ctx context.Context, db *sqlx.DB, source string, labels map[string]interface{}, accountID string) (int, bool) {
	if db == nil || accountID == "" {
		return 0, false
	}
	var alertName string
	switch source {
	case "prometheus", "prometheus_alertmanager_webhook":
		alertName, _ = labels["alertname"].(string)
	case "pagerduty_webhook":
		alertName, _ = labels["nb_alert_name"].(string)
		if alertName == "" {
			alertName, _ = labels["alertname"].(string)
		}
	default:
		return 0, false
	}
	if alertName == "" {
		return 0, false
	}
	var duration string
	err := db.QueryRowContext(ctx,
		"SELECT COALESCE(duration, '') FROM event_rules WHERE alert = $1 AND account_id = $2 AND enabled = true AND expr IS NOT NULL AND expr != '' ORDER BY (right(source, 8) = '_webhook'), updated_at DESC LIMIT 1",
		alertName, accountID).Scan(&duration)
	if err != nil {
		return 0, false
	}
	return parseDurationSeconds(duration), true
}
