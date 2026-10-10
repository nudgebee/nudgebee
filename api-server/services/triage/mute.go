package triage

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"nudgebee/services/eventrule"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"

	"github.com/jmoiron/sqlx"
)

// mute.go implements "Mute in Nudgebee" for an alert rule (epic #38553, spec D1):
// a set of ordinary suppression rules, one per event source the alert arrives on,
// tied to the event_rules row by action_value.mute_event_rule_id. Events keep being
// stored and resolve normally; they are marked SUPPRESSED, which already skips
// notifications and LLM analysis. The customer's rule is not touched.

// MuteAlertnamePattern is the match_alertname for a muted rule. match_alertname is
// an unanchored regex, so the name is escaped and anchored: "API (5xx) > 1%" must
// match itself and not "API (5xx) > 1% [staging]".
func MuteAlertnamePattern(alert string) string {
	return "^" + regexp.QuoteMeta(alert) + "$"
}

// muteActionValue tags a suppression rule as a mute. ruleID is the event_rules row
// it belongs to ("" when the event maps to none); alert is the matched alert key.
func muteActionValue(ruleID, scope, alert string) (string, error) {
	v := map[string]string{eventrule.MuteScopeKey: scope}
	if ruleID != "" {
		v[eventrule.MuteMarkerKey] = ruleID
	}
	if alert != "" {
		v[eventrule.MuteAlertKey] = alert
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("mute: marshal action_value: %w", err)
	}
	return string(b), nil
}

// isMuteRule reports whether a suppression rule was created by Mute.
func isMuteRule(rule *TriageRule) bool {
	if rule == nil || rule.ActionValue == nil || *rule.ActionValue == "" {
		return false
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(*rule.ActionValue), &v); err != nil {
		return false
	}
	_, marker := v[eventrule.MuteMarkerKey]
	_, scope := v[eventrule.MuteScopeKey]
	return marker || scope
}

// UpdateRuleMuteRequest is the eventrules_update_mute input.
type UpdateRuleMuteRequest struct {
	RuleID    string `json:"rule_id"`
	AccountID string `json:"account_id"`
	Muted     bool   `json:"muted"`
}

// UpdateRuleMute mutes or unmutes an alert rule and returns its fresh capabilities.
func UpdateRuleMute(ctx *security.RequestContext, req UpdateRuleMuteRequest) (eventrule.RuleCapabilities, error) {
	sc := ctx.GetSecurityContext()
	if sc == nil || sc.GetTenantId() == "" || sc.GetUserId() == "" {
		return eventrule.RuleCapabilities{}, fmt.Errorf("unauthorized")
	}
	if req.RuleID == "" || req.AccountID == "" {
		return eventrule.RuleCapabilities{}, fmt.Errorf("rule_id and account_id are required")
	}
	if !sc.HasAccountAccess(req.AccountID, security.SecurityAccessTypeUpdate) {
		return eventrule.RuleCapabilities{}, fmt.Errorf("account unauthorized")
	}

	target, err := eventrule.ResolveRuleTarget(ctx, req.RuleID, eventrule.ResolveOptions{})
	if err != nil {
		return eventrule.RuleCapabilities{}, err
	}
	if target.AccountID != req.AccountID {
		return eventrule.RuleCapabilities{}, eventrule.ErrRuleNotFound
	}
	if req.Muted && !target.Operations.Mute.Available {
		return eventrule.RuleCapabilities{}, fmt.Errorf("%w: mute: %s", eventrule.ErrOperationUnavailable, target.Operations.Mute.Reason)
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return eventrule.RuleCapabilities{}, fmt.Errorf("mute: database manager: %w", err)
	}

	if req.Muted {
		err = muteRule(ctx.GetContext(), dbms.Db, sc.GetTenantId(), req.AccountID, sc.GetUserId(),
			target.ID, target.Rule.Alert, eventrule.MuteEventSources(target.Rule.Source), nil)
	} else {
		err = unmuteRule(ctx.GetContext(), dbms.Db, sc.GetTenantId(), req.AccountID, sc.GetUserId(), target.ID)
	}
	if err != nil {
		return eventrule.RuleCapabilities{}, err
	}
	ClearTriageRulesCache(sc.GetTenantId())

	caps := target.Capabilities()
	caps.Muted = req.Muted
	return caps, nil
}

// muteRule enables the rule-wide mute of an alert rule on every given source,
// reusing rows from an earlier rule-wide mute so repeated mute/unmute does not
// pile up rules. expiresAt nil means until unmuted.
func muteRule(ctx context.Context, db *sqlx.DB, tenantID, accountID, userID, ruleID, alert string, sources []string, expiresAt *time.Time) error {
	return upsertMute(ctx, db, muteSpec{
		tenantID: tenantID, accountID: accountID,
		ruleID: ruleID, alert: alert, scope: eventrule.MuteScopeRule, sources: sources,
	}, userID, expiresAt)
}

// lockMute serialises mute writes for one (account, rule or alert, scope,
// workload), so a double click or two users cannot insert duplicate rows between
// the lookup and the insert. Held until the transaction ends.
func lockMute(ctx context.Context, tx *sqlx.Tx, accountID, identity, scope, namespace, owner string) error {
	key := accountID + "|" + identity + "|" + scope + "|" + namespace + "|" + owner
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 38553))`, key); err != nil {
		return fmt.Errorf("mute: lock: %w", err)
	}
	return nil
}

// unmuteRule disables every suppression rule tied to the alert rule, whatever its
// scope: Unmute in Alert Manager unmutes the rule everywhere.
func unmuteRule(ctx context.Context, db *sqlx.DB, tenantID, accountID, userID, ruleID string) error {
	if _, err := db.ExecContext(ctx, `
		UPDATE event_triage_rules
		SET enabled = FALSE, updated_by = $4, updated_at = NOW()
		WHERE tenant_id = $1 AND account_id = $2
		  AND action_value->>'`+eventrule.MuteMarkerKey+`' = $3 AND enabled`,
		tenantID, accountID, ruleID, userID); err != nil {
		return fmt.Errorf("unmute: %w", err)
	}
	return nil
}
