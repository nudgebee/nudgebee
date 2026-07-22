package triage

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/jmoiron/sqlx"
	"github.com/prometheus/prometheus/promql/parser"
)

// threshold_apply.go implements the INVERSE of the parsing in threshold_suggestion.go:
// given a cached suggestion + the source rule, produce the mutated rule definition that
// writes the new threshold (and/or duration) back to the alert source. The engine only
// ever PARSES thresholds; this is the only place that writes them.

const floatEpsilon = 1e-9

// promQLSources are the suggestion sources whose rule body is a PromQL expression.
var promQLSources = map[string]bool{
	"prometheus":        true,
	"pagerduty_webhook": true,
}

// azureSources are the suggestion sources whose rule body is Azure criteria JSON.
var azureSources = map[string]bool{
	"azure_monitor_webhook": true,
	"Azure_Monitor_Alert":   true,
}

// SourceRule is the source-of-truth alert rule loaded from event_rules for applying a change.
type SourceRule struct {
	ID                   string // event_rules.id (for DisableEventRule)
	Alert                string
	AccountID            string
	Source               string // event_rules.source — drives UpdateEventRule routing
	Expr                 string // PromQL (prometheus/pagerduty) or criteria/policy JSON (azure/gcp)
	Duration             string
	Severity             string
	Category             string
	AlertType            string
	MetricProvider       string
	MetricProviderSource string
	ExternalRuleID       string
	ProviderConfig       map[string]interface{}
	Enabled              bool
	// Existing annotations/labels — carried through so a write-back does not wipe them.
	AnnDescription string
	AnnSummary     string
	AnnRunbook     string
	LabelSeverity  string
	Found          bool
}

// RewrittenRule is the mutated rule definition to write back via UpdateEventRule / pr_raise.
type RewrittenRule struct {
	Source            string                 // event_rules source to route the write
	Expr              string                 // new PromQL OR provider criteria/policy JSON
	Duration          string                 // new for:/window string, "" = unchanged
	ProviderConfig    map[string]interface{} // cloud threshold/duration patch
	Disable           bool                   // recommendation_type == "disable"
	PreviousThreshold float64                // current threshold, captured for revert
	PreviousDuration  int                    // current duration minutes (best-effort), for revert
	NewThreshold      float64                // threshold actually written
	ChangedThreshold  bool
	ChangedDuration   bool
}

// ApplySuggestionRow holds the apply-relevant columns of a cached suggestion.
type ApplySuggestionRow struct {
	AlertRuleKey       string
	CloudAccountID     string
	TenantID           string
	Source             string
	AlertName          string
	Operator           string
	CurrentThreshold   float64
	SuggestedThreshold float64
	RecommendationType string
	RiskLevel          string
	SuggestedDuration  int
	ApplyStatus        string
	ApplyMethod        string
	PreviousThreshold  *float64
	PreviousDuration   *int
	Found              bool
}

// getSuggestionForApply loads the apply-relevant fields of a cached suggestion row.
func getSuggestionForApply(ctx context.Context, db *sqlx.DB, alertRuleKey, cloudAccountID string) (*ApplySuggestionRow, error) {
	var row struct {
		TenantID           string   `db:"tenant_id"`
		Source             string   `db:"source"`
		AlertName          *string  `db:"alert_name"`
		Operator           *string  `db:"operator"`
		CurrentThreshold   *float64 `db:"current_threshold"`
		SuggestedThreshold *float64 `db:"suggested_threshold"`
		MetricStats        *string  `db:"metric_stats"`
		ApplyStatus        *string  `db:"apply_status"`
		ApplyMethod        *string  `db:"apply_method"`
		PreviousThreshold  *float64 `db:"previous_threshold"`
		PreviousDuration   *int     `db:"previous_duration"`
	}
	err := db.GetContext(ctx, &row, `
		SELECT tenant_id, source, alert_name, operator, current_threshold, suggested_threshold,
		       metric_stats, apply_status, apply_method, previous_threshold, previous_duration
		FROM event_threshold_suggestions
		WHERE alert_rule_key = $1 AND cloud_account_id = $2`,
		alertRuleKey, cloudAccountID)
	if err != nil {
		return nil, err
	}

	out := &ApplySuggestionRow{
		AlertRuleKey:      alertRuleKey,
		CloudAccountID:    cloudAccountID,
		TenantID:          row.TenantID,
		Source:            row.Source,
		PreviousThreshold: row.PreviousThreshold,
		PreviousDuration:  row.PreviousDuration,
		Found:             true,
	}
	if row.AlertName != nil {
		out.AlertName = *row.AlertName
	}
	if row.Operator != nil {
		out.Operator = *row.Operator
	}
	if row.CurrentThreshold != nil {
		out.CurrentThreshold = *row.CurrentThreshold
	}
	if row.SuggestedThreshold != nil {
		out.SuggestedThreshold = *row.SuggestedThreshold
	}
	if row.ApplyStatus != nil {
		out.ApplyStatus = *row.ApplyStatus
	}
	if row.ApplyMethod != nil {
		out.ApplyMethod = *row.ApplyMethod
	}
	// recommendation_type / risk_level / suggested_duration live in the metric_stats JSONB.
	out.RecommendationType = "tune_threshold"
	if row.MetricStats != nil {
		var stats map[string]interface{}
		if err := json.Unmarshal([]byte(*row.MetricStats), &stats); err == nil {
			if v, ok := stats["recommendation_type"].(string); ok && v != "" {
				out.RecommendationType = v
			}
			if v, ok := stats["risk_level"].(string); ok {
				out.RiskLevel = v
			}
			if v, ok := stats["suggested_duration"].(float64); ok {
				out.SuggestedDuration = int(v)
			}
		}
	}
	return out, nil
}

// LoadSourceRule loads the event_rules row that backs a suggestion, by (alert, account_id).
// alert is the cached alert_name (the AlarmName the engine extracted) which is what
// event_rules keys on for every source (including GCP, where alert_name is the policy name).
func LoadSourceRule(ctx context.Context, db *sqlx.DB, accountID, alert string) (*SourceRule, error) {
	var row struct {
		ID                   string  `db:"id"`
		Source               *string `db:"source"`
		Expr                 *string `db:"expr"`
		Duration             *string `db:"duration"`
		Severity             *string `db:"severity"`
		Category             *string `db:"category"`
		AlertType            *string `db:"alert_type"`
		MetricProvider       *string `db:"metric_provider"`
		MetricProviderSource *string `db:"metric_provider_source"`
		ExternalRuleID       *string `db:"external_rule_id"`
		ProviderConfig       *string `db:"provider_config"`
		Annotations          *string `db:"annotations"`
		Labels               *string `db:"labels"`
		Enabled              *bool   `db:"enabled"`
	}
	err := db.GetContext(ctx, &row, `
		SELECT id, source, expr, duration, severity, category, alert_type,
		       metric_provider, metric_provider_source, external_rule_id, provider_config,
		       annotations, labels, enabled
		FROM event_rules
		WHERE alert = $1 AND account_id = $2
		ORDER BY enabled DESC
		LIMIT 1`, alert, accountID)
	if err != nil {
		return nil, err
	}

	sr := &SourceRule{ID: row.ID, Alert: alert, AccountID: accountID, Found: true}
	if row.Source != nil {
		sr.Source = *row.Source
	}
	if row.Expr != nil {
		sr.Expr = *row.Expr
	}
	if row.Duration != nil {
		sr.Duration = *row.Duration
	}
	if row.Severity != nil {
		sr.Severity = *row.Severity
	}
	if row.Category != nil {
		sr.Category = *row.Category
	}
	if row.AlertType != nil {
		sr.AlertType = *row.AlertType
	}
	if row.MetricProvider != nil {
		sr.MetricProvider = *row.MetricProvider
	}
	if row.MetricProviderSource != nil {
		sr.MetricProviderSource = *row.MetricProviderSource
	}
	if row.ExternalRuleID != nil {
		sr.ExternalRuleID = *row.ExternalRuleID
	}
	if row.Enabled != nil {
		sr.Enabled = *row.Enabled
	}
	if row.ProviderConfig != nil && *row.ProviderConfig != "" {
		var pc map[string]interface{}
		if err := json.Unmarshal([]byte(*row.ProviderConfig), &pc); err == nil {
			sr.ProviderConfig = pc
		}
	}
	if row.Annotations != nil && *row.Annotations != "" {
		var ann struct {
			Description string `json:"description"`
			Summary     string `json:"summary"`
			Runbook     string `json:"runbook"`
		}
		if err := json.Unmarshal([]byte(*row.Annotations), &ann); err == nil {
			sr.AnnDescription = ann.Description
			sr.AnnSummary = ann.Summary
			sr.AnnRunbook = ann.Runbook
		}
	}
	if row.Labels != nil && *row.Labels != "" {
		var lbl struct {
			Severity string `json:"severity"`
		}
		if err := json.Unmarshal([]byte(*row.Labels), &lbl); err == nil {
			sr.LabelSeverity = lbl.Severity
		}
	}
	return sr, nil
}

// BuildRewrittenRule produces the mutated rule definition for a suggestion.
// newThreshold/newDurationMin are the effective values to apply (after any user override);
// pass the suggestion's own suggested values for the default path.
func BuildRewrittenRule(sug *ApplySuggestionRow, rule *SourceRule, newThreshold float64, newDurationMin int) (*RewrittenRule, error) {
	recType := sug.RecommendationType
	changeThreshold := recType == "tune_threshold" || recType == "tune_both"
	changeDuration := recType == "increase_duration" || recType == "tune_both"

	out := &RewrittenRule{
		Source:            rule.Source,
		Expr:              rule.Expr,
		ProviderConfig:    cloneConfig(rule.ProviderConfig),
		PreviousThreshold: sug.CurrentThreshold,
		PreviousDuration:  parseDurationMinutes(rule.Duration),
		NewThreshold:      newThreshold,
		ChangedThreshold:  changeThreshold,
		ChangedDuration:   changeDuration,
	}

	if recType == "disable" {
		out.Disable = true
		return out, nil
	}

	switch {
	case promQLSources[sug.Source] || isPromQLExpr(rule.Expr):
		if changeThreshold {
			newExpr, err := RewritePromQLThreshold(rule.Expr, newThreshold)
			if err != nil {
				return nil, fmt.Errorf("rewrite PromQL threshold: %w", err)
			}
			out.Expr = newExpr
		}
		if changeDuration && newDurationMin > 0 {
			out.Duration = fmt.Sprintf("%dm", newDurationMin)
		}

	case azureSources[sug.Source]:
		newExpr, err := setAzureCriteriaThreshold(rule.Expr, newThreshold, changeThreshold, newDurationMin, changeDuration)
		if err != nil {
			return nil, fmt.Errorf("patch azure criteria: %w", err)
		}
		out.Expr = newExpr
		applyCloudProviderConfig(out, newThreshold, changeThreshold, newDurationMin, changeDuration)

	case sug.Source == "GCP_Metric_Alert":
		newExpr, err := setGCPPolicyThreshold(rule.Expr, newThreshold, changeThreshold, newDurationMin, changeDuration)
		if err != nil {
			return nil, fmt.Errorf("patch gcp policy: %w", err)
		}
		out.Expr = newExpr
		applyCloudProviderConfig(out, newThreshold, changeThreshold, newDurationMin, changeDuration)

	case sug.Source == "AWS_CloudWatch_Alarm":
		// AWS alarm config is carried in provider_config; the cloud-collector update handler
		// describes the existing alarm and re-puts it with the new threshold. alert_rule_key
		// is the alarm ARN (carries the region + alarm name the collector needs).
		applyCloudProviderConfig(out, newThreshold, changeThreshold, newDurationMin, changeDuration)
		if out.ProviderConfig == nil {
			out.ProviderConfig = map[string]interface{}{}
		}
		out.ProviderConfig["alarm_arn"] = sug.AlertRuleKey

	default:
		return nil, fmt.Errorf("apply not supported for source %q", sug.Source)
	}

	return out, nil
}

// applyCloudProviderConfig records the new threshold/duration on the rewritten rule's
// provider_config so the cloud-collector update handler can apply them.
func applyCloudProviderConfig(out *RewrittenRule, newThreshold float64, changeThreshold bool, newDurationMin int, changeDuration bool) {
	if out.ProviderConfig == nil {
		out.ProviderConfig = map[string]interface{}{}
	}
	if changeThreshold {
		out.ProviderConfig["threshold"] = newThreshold
	}
	if changeDuration && newDurationMin > 0 {
		out.ProviderConfig["duration_minutes"] = newDurationMin
	}
}

// RewritePromQLThreshold returns origExpr with its tunable threshold constant replaced by
// newThreshold, preserving operator direction, compound legs, guard conditions, and reversed
// comparisons. It mirrors extractPromQLThreshold's descent to locate the exact constant node,
// then splices by byte position so formatting is preserved. A mandatory round-trip re-parse
// asserts the result yields exactly the target threshold with an unchanged query/operator —
// if not, it refuses (returns an error) rather than risk corrupting the rule.
func RewritePromQLThreshold(origExpr string, newThreshold float64) (string, error) {
	orig, err := extractPromQLThreshold(origExpr)
	if err != nil {
		return "", fmt.Errorf("cannot locate threshold to rewrite: %w", err)
	}

	expr, err := promQLParser.ParseExpr(origExpr)
	if err != nil {
		return "", fmt.Errorf("invalid PromQL: %w", err)
	}
	_, node, err := resolveThresholdNode(expr)
	if err != nil {
		return "", err
	}

	pr := node.PositionRange()
	start, end := int(pr.Start), int(pr.End)
	if start < 0 || end > len(origExpr) || start >= end {
		return "", fmt.Errorf("invalid position range [%d,%d) for threshold literal in %q", start, end, origExpr)
	}

	newExpr := origExpr[:start] + formatPromFloat(newThreshold) + origExpr[end:]

	// Validation gate — the core safety net.
	reparsed, rerr := extractPromQLThreshold(newExpr)
	if rerr != nil {
		return "", fmt.Errorf("rewritten PromQL failed to re-parse (%q): %w", newExpr, rerr)
	}
	if math.Abs(reparsed.Value-newThreshold) > floatEpsilon {
		return "", fmt.Errorf("rewrite verification failed: wanted threshold %v, got %v", newThreshold, reparsed.Value)
	}
	if reparsed.Operator != orig.Operator || reparsed.QueryExpr != orig.QueryExpr {
		return "", fmt.Errorf("rewrite altered the rule beyond the threshold: operator %q->%q, query %q->%q",
			orig.Operator, reparsed.Operator, orig.QueryExpr, reparsed.QueryExpr)
	}
	return newExpr, nil
}

// resolveThresholdNode mirrors extractPromQLThreshold but returns the AST node holding the
// constant to replace (so we can splice it by position). It walks the original tree directly
// — never re-parsing substrings — so PositionRange offsets stay valid against the input string.
func resolveThresholdNode(expr parser.Expr) (*promQLThreshold, parser.Expr, error) {
	for {
		if paren, ok := expr.(*parser.ParenExpr); ok {
			expr = paren.Expr
		} else {
			break
		}
	}

	binExpr, ok := expr.(*parser.BinaryExpr)
	if !ok {
		return nil, nil, fmt.Errorf("expression is not a binary comparison")
	}

	if binExpr.Op.IsSetOperator() {
		return resolveThresholdNodeCompound(binExpr)
	}
	if !binExpr.Op.IsComparisonOperator() {
		return nil, nil, fmt.Errorf("top-level operator %q is not a comparison", binExpr.Op.String())
	}

	switch {
	case isPromQLNumberLiteral(binExpr.RHS):
		return &promQLThreshold{
			QueryExpr: binExpr.LHS.String(),
			Operator:  binExpr.Op.String(),
			Value:     getPromQLNumberValue(binExpr.RHS),
		}, binExpr.RHS, nil
	case isPromQLNumberLiteral(binExpr.LHS):
		return &promQLThreshold{
			QueryExpr: binExpr.RHS.String(),
			Operator:  flipComparisonOperator(binExpr.Op.String()),
			Value:     getPromQLNumberValue(binExpr.LHS),
		}, binExpr.LHS, nil
	default:
		return nil, nil, fmt.Errorf("dynamic threshold — both sides are expressions")
	}
}

// resolveThresholdNodeCompound mirrors extractFromCompoundExpr's leg-selection so the rewrite
// targets the SAME leg the parser identified as the alerting threshold (not the guard).
func resolveThresholdNodeCompound(binExpr *parser.BinaryExpr) (*promQLThreshold, parser.Expr, error) {
	lhsRes, lhsNode, lhsErr := resolveThresholdNode(binExpr.LHS)
	rhsRes, rhsNode, rhsErr := resolveThresholdNode(binExpr.RHS)

	switch {
	case lhsErr == nil && rhsErr != nil:
		return lhsRes, lhsNode, nil
	case rhsErr == nil && lhsErr != nil:
		return rhsRes, rhsNode, nil
	case lhsErr == nil && rhsErr == nil:
		if binExpr.Op == parser.LAND {
			lhsIsGuard := isGuardCondition(lhsRes)
			rhsIsGuard := isGuardCondition(rhsRes)
			if lhsIsGuard && !rhsIsGuard {
				return rhsRes, rhsNode, nil
			}
			if rhsIsGuard && !lhsIsGuard {
				return lhsRes, lhsNode, nil
			}
		}
		return lhsRes, lhsNode, nil
	default:
		return nil, nil, fmt.Errorf("compound %s: no extractable threshold in either leg", binExpr.Op.String())
	}
}

// setAzureCriteriaThreshold patches allOf[0].threshold (and windowSize for duration) in the
// Azure criteria JSON stored in event_rules.expr — the symmetric write to extractAzureMetricCondition.
func setAzureCriteriaThreshold(exprJSON string, newThreshold float64, changeThreshold bool, newDurationMin int, changeDuration bool) (string, error) {
	if exprJSON == "" {
		return "", fmt.Errorf("empty azure criteria")
	}
	var criteria map[string]interface{}
	if err := json.Unmarshal([]byte(exprJSON), &criteria); err != nil {
		return "", fmt.Errorf("parse azure criteria JSON: %w", err)
	}
	allOf, ok := criteria["allOf"].([]interface{})
	if !ok || len(allOf) == 0 {
		return "", fmt.Errorf("azure criteria has no allOf conditions")
	}
	cond, ok := allOf[0].(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("azure criteria allOf[0] is not an object")
	}
	if changeThreshold {
		cond["threshold"] = newThreshold
	}
	if changeDuration && newDurationMin > 0 {
		criteria["windowSize"] = fmt.Sprintf("PT%dM", newDurationMin)
	}
	allOf[0] = cond
	criteria["allOf"] = allOf
	out, err := json.Marshal(criteria)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// setGCPPolicyThreshold patches conditions[0].conditionThreshold.thresholdValue (and .duration)
// in the GCP AlertPolicy JSON stored in event_rules.expr — symmetric to extractGCPAlertDefinition.
func setGCPPolicyThreshold(exprJSON string, newThreshold float64, changeThreshold bool, newDurationMin int, changeDuration bool) (string, error) {
	if exprJSON == "" {
		return "", fmt.Errorf("empty gcp policy")
	}
	var policy map[string]interface{}
	if err := json.Unmarshal([]byte(exprJSON), &policy); err != nil {
		return "", fmt.Errorf("parse gcp policy JSON: %w", err)
	}
	conditions, ok := policy["conditions"].([]interface{})
	if !ok || len(conditions) == 0 {
		return "", fmt.Errorf("gcp policy has no conditions")
	}
	cond, ok := conditions[0].(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("gcp policy condition[0] is not an object")
	}
	condThreshold, ok := cond["conditionThreshold"].(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("gcp policy condition[0] has no conditionThreshold")
	}
	if changeThreshold {
		condThreshold["thresholdValue"] = newThreshold
	}
	if changeDuration && newDurationMin > 0 {
		condThreshold["duration"] = fmt.Sprintf("%ds", newDurationMin*60)
	}
	cond["conditionThreshold"] = condThreshold
	conditions[0] = cond
	policy["conditions"] = conditions
	out, err := json.Marshal(policy)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// formatPromFloat renders a float as a plain decimal that PromQL can parse (no scientific notation).
func formatPromFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// isPromQLExpr reports whether expr looks like a PromQL comparison (vs cloud criteria JSON).
func isPromQLExpr(expr string) bool {
	if expr == "" {
		return false
	}
	if _, err := extractPromQLThreshold(expr); err == nil {
		return true
	}
	return false
}

// parseDurationMinutes best-effort converts a duration to whole minutes, for revert
// bookkeeping. Handles Prometheus-style ("10m", "300s", "1h", "2h30m") and ISO8601
// ("PT10M"). Returns 0 when it cannot parse.
func parseDurationMinutes(d string) int {
	d = strings.TrimSpace(d)
	if d == "" {
		return 0
	}
	// ISO8601 (Azure windowSize, GCP-ish) — reuse the engine's parser.
	if strings.HasPrefix(strings.ToUpper(d), "PT") {
		if secs := parseISODurationToSeconds(d); secs > 0 {
			return secs / 60
		}
		return 0
	}
	// Prometheus-style: sequence of <int><unit> tokens (s,m,h,d,w,y).
	totalSeconds := 0
	current := ""
	for _, c := range d {
		if c >= '0' && c <= '9' {
			current += string(c)
			continue
		}
		val, err := strconv.Atoi(current)
		current = ""
		if err != nil {
			continue
		}
		switch c {
		case 's', 'S':
			totalSeconds += val
		case 'm':
			totalSeconds += val * 60
		case 'h', 'H':
			totalSeconds += val * 3600
		case 'd', 'D':
			totalSeconds += val * 86400
		case 'w', 'W':
			totalSeconds += val * 604800
		case 'y', 'Y':
			totalSeconds += val * 31536000
		}
	}
	return totalSeconds / 60
}

func cloneConfig(in map[string]interface{}) map[string]interface{} {
	if in == nil {
		return nil
	}
	out := make(map[string]interface{}, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
