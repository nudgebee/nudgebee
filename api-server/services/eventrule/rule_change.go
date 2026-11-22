package eventrule

import (
	stdctx "context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"nudgebee/services/internal/database"
	"nudgebee/services/security"

	"github.com/jmoiron/sqlx"
	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/promql/parser"
)

// rule_change.go applies a change to one rule's expression and `for` without the
// caller rebuilding the whole rule (docs/nubi-alert-rule-fix-spec.md, D5/D8-D10).
// Nubi's remediation plan proposes such a change; the operator applies it here.

var rulePromQLParser = parser.NewParser(parser.Options{})

// NormalizePromQL returns the canonical form of a PromQL expression, or ok=false
// when it does not parse with the Prometheus parser. Two sync passes store the same
// rule with different whitespace; comparing canonical forms compares meaning.
func NormalizePromQL(expr string) (string, bool) {
	if strings.TrimSpace(expr) == "" {
		return "", false
	}
	node, err := rulePromQLParser.ParseExpr(expr)
	if err != nil {
		return "", false
	}
	return node.String(), true
}

// RuleDefinition is the editable part of a PrometheusRule-backed rule.
type RuleDefinition struct {
	Expr     string `json:"expr"`
	Duration string `json:"duration"`
	// ExprNormalized is the canonical PromQL, so a caller can tell whether a
	// proposed expression is already the rule's (the "Applied" badge).
	ExprNormalized string `json:"expr_normalized"`
}

// EditableDefinition returns the rule's definition when its expression can be
// changed from Nudgebee: edit is available and the current expression is PromQL.
// A rule in another dialect (VictoriaMetrics MetricsQL) is not offered, so a
// proposal is never refused for syntax Nudgebee cannot check (spec D9).
func (t *RuleTarget) EditableDefinition() *RuleDefinition {
	if t == nil || !t.Operations.EditDefinition.Available {
		return nil
	}
	norm, ok := NormalizePromQL(t.Rule.Expr)
	if !ok {
		return nil
	}
	return &RuleDefinition{Expr: t.Rule.Expr, Duration: t.Rule.Duration, ExprNormalized: norm}
}

// LoadEventConfig rebuilds the full update request for a stored rule, so a partial
// change (a new threshold, a new expression) is written without wiping the fields
// the caller did not mean to touch. The playbook params stay nil: UpdateEventRule
// then keeps the stored playbook.
func LoadEventConfig(ctx stdctx.Context, db *sqlx.DB, tenantID, ruleID string) (EventConfig, error) {
	var row struct {
		ID                   string         `db:"id"`
		AccountID            string         `db:"account_id"`
		Alert                string         `db:"alert"`
		Source               sql.NullString `db:"source"`
		Expr                 sql.NullString `db:"expr"`
		Duration             sql.NullString `db:"duration"`
		Severity             sql.NullString `db:"severity"`
		Category             sql.NullString `db:"category"`
		AlertType            sql.NullString `db:"alert_type"`
		MetricProvider       sql.NullString `db:"metric_provider"`
		MetricProviderSource sql.NullString `db:"metric_provider_source"`
		ProviderConfig       sql.NullString `db:"provider_config"`
		Annotations          sql.NullString `db:"annotations"`
		Labels               sql.NullString `db:"labels"`
		Enabled              sql.NullBool   `db:"enabled"`
	}
	err := db.GetContext(ctx, &row, `
		SELECT id, account_id, alert, source, expr, duration, severity, category, alert_type,
		       metric_provider, metric_provider_source, provider_config, annotations, labels, enabled
		FROM event_rules WHERE id = $1 AND tenant_id = $2`, ruleID, tenantID)
	if errors.Is(err, sql.ErrNoRows) {
		return EventConfig{}, ErrRuleNotFound
	}
	if err != nil {
		return EventConfig{}, fmt.Errorf("eventrule: load rule: %w", err)
	}
	cfg := EventConfig{
		Id:                   row.ID,
		AccountID:            row.AccountID,
		Alert:                row.Alert,
		Source:               row.Source.String,
		Expr:                 row.Expr.String,
		Duration:             row.Duration.String,
		Severity:             row.Severity.String,
		Category:             row.Category.String,
		AlertType:            row.AlertType.String,
		MetricProvider:       row.MetricProvider.String,
		MetricProviderSource: row.MetricProviderSource.String,
		Enabled:              row.Enabled.Bool,
	}
	if row.ProviderConfig.Valid && row.ProviderConfig.String != "" && row.ProviderConfig.String != "null" {
		if err := json.Unmarshal([]byte(row.ProviderConfig.String), &cfg.ProviderConfig); err != nil {
			return EventConfig{}, fmt.Errorf("eventrule: provider_config: %w", err)
		}
	}
	// A rule whose stored annotations or labels cannot be read is refused rather than
	// written back with an empty description and summary. (Reads from a nil map are
	// fine: a JSON null or an absent column just means none are set.)
	ann, err := decodeStringMap(row.Annotations)
	if err != nil {
		return EventConfig{}, fmt.Errorf("eventrule: annotations: %w", err)
	}
	lab, err := decodeStringMap(row.Labels)
	if err != nil {
		return EventConfig{}, fmt.Errorf("eventrule: labels: %w", err)
	}
	cfg.Annotations.Description = ann["description"]
	cfg.Annotations.Summary = ann["summary"]
	cfg.Annotations.Runbook = ann["runbook"]
	cfg.Labels.Severity = lab["severity"]
	if cfg.Severity == "" {
		cfg.Severity = cfg.Labels.Severity
	}
	return cfg, nil
}

func decodeStringMap(v sql.NullString) (map[string]string, error) {
	var m map[string]string
	if !v.Valid || strings.TrimSpace(v.String) == "" {
		return m, nil
	}
	if err := json.Unmarshal([]byte(v.String), &m); err != nil {
		return nil, err
	}
	return m, nil
}

// ApplyRuleChangeRequest is the eventrules_apply_rule_change input. Expected* is
// the definition the change was proposed against; a rule changed since then is
// not overwritten.
type ApplyRuleChangeRequest struct {
	RuleID           string `json:"rule_id"`
	AccountID        string `json:"account_id"`
	Expr             string `json:"expr"`
	Duration         string `json:"duration"`
	ExpectedExpr     string `json:"expected_expr"`
	ExpectedDuration string `json:"expected_duration"`
}

// ApplyRuleChangeResult is the rule after the change.
type ApplyRuleChangeResult struct {
	Capabilities RuleCapabilities `json:"capabilities"`
	Definition   *RuleDefinition  `json:"definition"`
}

// ErrRuleChanged is returned when the stored rule no longer matches the definition
// a change was proposed against.
var ErrRuleChanged = errors.New("the rule changed since this change was proposed; generate the plan again")

// planRuleChange checks a proposed change against the rule's current definition.
// It returns the expression and duration to write. Pure: no I/O.
func planRuleChange(current RuleDefinition, req ApplyRuleChangeRequest) (expr, duration string, err error) {
	expectedNorm, ok := NormalizePromQL(req.ExpectedExpr)
	if !ok || expectedNorm != current.ExprNormalized {
		return "", "", ErrRuleChanged
	}
	if strings.TrimSpace(req.ExpectedDuration) != strings.TrimSpace(current.Duration) {
		return "", "", ErrRuleChanged
	}
	expr, duration = current.Expr, current.Duration
	if strings.TrimSpace(req.Expr) != "" {
		norm, ok := NormalizePromQL(req.Expr)
		if !ok {
			return "", "", fmt.Errorf("%w: the proposed expression is not valid PromQL", ErrOperationUnavailable)
		}
		if norm != current.ExprNormalized {
			expr = strings.TrimSpace(req.Expr)
		}
	}
	if d := strings.TrimSpace(req.Duration); d != "" && d != current.Duration {
		if _, err := model.ParseDuration(d); err != nil {
			return "", "", fmt.Errorf("%w: %q is not a valid duration", ErrOperationUnavailable, d)
		}
		duration = d
	}
	if expr == current.Expr && duration == current.Duration {
		return "", "", fmt.Errorf("%w: the change does not modify the rule", ErrOperationUnavailable)
	}
	return expr, duration, nil
}

// ApplyRuleChange changes one rule's expression and/or `for` through the regular
// UpdateEventRule path (PrometheusRule patch, locator, Helm warning), keeping every
// other field of the stored rule.
func ApplyRuleChange(ctx *security.RequestContext, req ApplyRuleChangeRequest) (*ApplyRuleChangeResult, error) {
	sc := ctx.GetSecurityContext()
	if sc == nil || sc.GetTenantId() == "" || sc.GetUserId() == "" {
		return nil, fmt.Errorf("unauthorized")
	}
	if req.RuleID == "" || req.AccountID == "" {
		return nil, fmt.Errorf("rule_id and account_id are required")
	}
	if !ctx.GetSecurityContext().CanAccessAccount(req.AccountID, security.SecurityAccessTypeUpdate) {
		return nil, fmt.Errorf("account unauthorized")
	}
	target, err := ResolveRuleTarget(ctx, req.RuleID, ResolveOptions{})
	if err != nil {
		return nil, err
	}
	if target.AccountID != req.AccountID {
		return nil, ErrRuleNotFound
	}
	if err := requireOperation(target.Operations.EditDefinition, "edit"); err != nil {
		return nil, err
	}
	current := target.EditableDefinition()
	if current == nil {
		return nil, fmt.Errorf("%w: this rule's expression is not PromQL Nudgebee can check", ErrOperationUnavailable)
	}
	expr, duration, err := planRuleChange(*current, req)
	if err != nil {
		return nil, err
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, fmt.Errorf("eventrule: database manager: %w", err)
	}
	cfg, err := LoadEventConfig(ctx.GetContext(), dbms.Db, sc.GetTenantId(), req.RuleID)
	if err != nil {
		return nil, err
	}
	cfg.Expr, cfg.Duration = expr, duration
	if _, err := UpdateEventRule(ctx, cfg); err != nil {
		return nil, err
	}

	fresh, err := ResolveRuleTarget(ctx, req.RuleID, ResolveOptions{})
	if err != nil {
		return nil, err
	}
	return &ApplyRuleChangeResult{Capabilities: fresh.Capabilities(), Definition: fresh.EditableDefinition()}, nil
}
