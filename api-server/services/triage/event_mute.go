package triage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"nudgebee/services/eventrule"
	"nudgebee/services/internal/database"
	"nudgebee/services/internal/database/models"
	"nudgebee/services/security"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

// event_mute.go backs the "Stop this alert" section of the investigation screen
// (docs/alert-rule-controls-on-investigation-spec.md). A mute started from an
// event is built from that event: its source and alert key, optionally narrowed to
// its workload, always with an end unless the user asks for none. It is stored as
// ordinary suppression rules tagged in action_value, the same model as a mute from
// Alert Manager, and it is never recorded as a remediation.

// muteDurationsHours are the durations the screen offers; 0 = until unmuted.
var muteDurationsHours = []int{0, 24, 168}

// muteLookback is the window the scope counts cover.
const muteLookback = 7 * 24 * time.Hour

// EventAlertControlsRequest is the eventrules_get_event_alert_controls input.
type EventAlertControlsRequest struct {
	EventID   string `json:"event_id"`
	AccountID string `json:"account_id"`
}

// UpdateEventMuteRequest is the eventrules_update_event_mute input.
type UpdateEventMuteRequest struct {
	EventID       string `json:"event_id"`
	AccountID     string `json:"account_id"`
	Scope         string `json:"scope"`
	DurationHours int    `json:"duration_hours"`
	Muted         bool   `json:"muted"`
}

// EventMuteScope is one way to mute the event's alert, with how much it would
// have silenced over the last 7 days.
type EventMuteScope struct {
	Scope     string `json:"scope"`
	Namespace string `json:"namespace,omitempty"`
	Owner     string `json:"owner,omitempty"`
	// Matched7d: events in the last 7 days this scope matches.
	Matched7d int `json:"matched_7d"`
	// Workloads7d: distinct workloads among them.
	Workloads7d int  `json:"workloads_7d"`
	Muted       bool `json:"muted"`
	// ExpiresAt is when an active mute of this scope ends; nil while muted means
	// until unmuted.
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// EventSuppression is an active suppression rule that matches the event. Only
// mutes can be undone here; any other rule is managed in Triage Rules.
type EventSuppression struct {
	RuleID    string     `json:"rule_id"`
	Name      string     `json:"name"`
	IsMute    bool       `json:"is_mute"`
	MuteScope string     `json:"mute_scope,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// EventAlertControls is what the screen can do about the event's alert.
type EventAlertControls struct {
	EventID string `json:"event_id"`
	Alert   string `json:"alert"`
	Source  string `json:"source"`
	// Rule is the alert rule the event belongs to, with what can be changed at
	// the source; nil when the event maps to no single rule.
	Rule *eventrule.RuleCapabilities `json:"rule"`
	// RuleLocation is where a PrometheusRule-backed rule is defined.
	RuleLocation *EventRuleLocation `json:"rule_location,omitempty"`
	// RuleDefinition is the rule's expression and `for` when they can be changed
	// from Nudgebee (edit available, expression is PromQL). Nubi's remediation plan
	// proposes changes against it.
	RuleDefinition *eventrule.RuleDefinition `json:"rule_definition,omitempty"`
	Scopes         []EventMuteScope          `json:"scopes"`
	Suppressions   []EventSuppression        `json:"suppressions"`
}

// EventRuleLocation names the PrometheusRule a rule lives in and who manages it.
type EventRuleLocation struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	ManagedBy string `json:"managed_by,omitempty"`
}

// eventMuteBasis is everything a mute of one event depends on.
type eventMuteBasis struct {
	event     *models.Event
	source    string
	alert     string
	namespace string
	owner     string
	// ruleID is the event_rules row the event belongs to ("" when none); it tags
	// the mute rows so Alert Manager shows them.
	ruleID string
	// sources are the event sources a mute covers: every delivery path of the
	// rule when the rule can be muted as a whole, else the event's own source.
	sources    []string
	rule       *eventrule.RuleCapabilities
	location   *EventRuleLocation
	definition *eventrule.RuleDefinition
}

// muteSpec identifies one mute: the rows it owns and what they match.
type muteSpec struct {
	tenantID, accountID string
	ruleID, alert       string
	scope               string
	namespace, owner    string
	sources             []string
}

func (m muteSpec) servicePattern() string {
	if m.scope != eventrule.MuteScopeWorkload {
		return ""
	}
	return MuteAlertnamePattern(m.owner)
}

func (m muteSpec) namespaceMatch() string {
	if m.scope != eventrule.MuteScopeWorkload {
		return ""
	}
	return m.namespace
}

// matchRule is the suppression rule's match for one source: the anchored alert
// key and, for a workload, its exact namespace and anchored owner.
func (m muteSpec) matchRule(source string) *TriageRule {
	pattern := MuteAlertnamePattern(m.alert)
	r := &TriageRule{
		RuleType:       RuleTypeSuppression,
		Action:         ActionSuppress,
		MatchSource:    &source,
		MatchAlertname: &pattern,
	}
	if ns := m.namespaceMatch(); ns != "" {
		r.MatchNamespace = &ns
	}
	if svc := m.servicePattern(); svc != "" {
		r.MatchService = &svc
	}
	return r
}

// identity is the lock key part naming the rule or, without one, the alert.
func (m muteSpec) identity() string {
	if m.ruleID != "" {
		return m.ruleID
	}
	return "alert:" + m.alert
}

// ownedRowsSQL selects this mute's rows. Params: $1 tenant, $2 account, $3 scope,
// $4 rule id ("" = none), $5 alert, $6 sources, $7 namespace, $8 service pattern.
const ownedRowsSQL = `
	tenant_id = $1 AND account_id = $2
	AND COALESCE(action_value->>'` + eventrule.MuteScopeKey + `', '` + eventrule.MuteScopeRule + `') = $3
	AND (
	  ($4 <> '' AND action_value->>'` + eventrule.MuteMarkerKey + `' = $4)
	  OR ($4 = '' AND action_value->>'` + eventrule.MuteMarkerKey + `' IS NULL
	      AND action_value->>'` + eventrule.MuteAlertKey + `' = $5)
	)
	AND match_source = ANY($6)
	AND COALESCE(match_namespace, '') = $7 AND COALESCE(match_service, '') = $8`

func (m muteSpec) ownedArgs() []any {
	return []any{m.tenantID, m.accountID, m.scope, m.ruleID, m.alert, pq.Array(m.sources), m.namespaceMatch(), m.servicePattern()}
}

// upsertMute enables one suppression rule per source for the mute, reusing rows
// of an earlier mute with the same identity. expiresAt nil = until unmuted.
func upsertMute(ctx context.Context, db *sqlx.DB, m muteSpec, userID string, expiresAt *time.Time) error {
	if len(m.sources) == 0 {
		return fmt.Errorf("%w: mute: no event sources for this alert", eventrule.ErrOperationUnavailable)
	}
	if m.alert == "" {
		return fmt.Errorf("%w: mute: the event has no alert key", eventrule.ErrOperationUnavailable)
	}
	actionValue, err := muteActionValue(m.ruleID, m.scope, m.alert)
	if err != nil {
		return err
	}
	pattern := MuteAlertnamePattern(m.alert)
	name := "Muted alert rule: " + m.alert
	description := "Created by Mute in Alert Manager. Events from this alert rule are kept but marked suppressed."
	if m.scope == eventrule.MuteScopeWorkload {
		name = fmt.Sprintf("Muted alert %s for %s/%s", m.alert, m.namespace, m.owner)
		description = "Created by Mute on the investigation screen. Events from this alert for this workload are kept but marked suppressed."
	}

	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("mute: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockMute(ctx, tx, m.accountID, m.identity(), m.scope, m.namespace, m.owner); err != nil {
		return err
	}

	for _, source := range m.sources {
		one := m
		one.sources = []string{source}
		var existingID string
		err := tx.GetContext(ctx, &existingID,
			`SELECT id FROM event_triage_rules WHERE `+ownedRowsSQL+` ORDER BY created_at LIMIT 1`, one.ownedArgs()...)
		switch {
		case err == nil:
			if _, err := tx.ExecContext(ctx, `
				UPDATE event_triage_rules
				SET enabled = TRUE, match_alertname = $2, rule_type = $3, action = $4,
				    action_value = $5, effective_until = $6, updated_by = $7, updated_at = NOW()
				WHERE id = $1`, existingID, pattern, RuleTypeSuppression, ActionSuppress,
				actionValue, expiresAt, userID); err != nil {
				return fmt.Errorf("mute: re-enable rule for %s: %w", source, err)
			}
		case errors.Is(err, sql.ErrNoRows):
			now := time.Now().UTC()
			rule := m.matchRule(source)
			rule.ID = uuid.New().String()
			rule.TenantID = &m.tenantID
			rule.AccountID = &m.accountID
			rule.ActionValue = &actionValue
			rule.Priority = 200
			rule.IsEditable = true
			rule.CanOverride = true
			rule.Enabled = true
			rule.EffectiveUntil = expiresAt
			rule.Name = &name
			rule.Description = &description
			rule.CreatedBy = &userID
			rule.CreatedAt = now
			rule.UpdatedAt = now
			if err := insertTriageRuleTx(ctx, tx, rule); err != nil {
				return fmt.Errorf("mute: insert rule for %s: %w", source, err)
			}
		default:
			return fmt.Errorf("mute: look up rule for %s: %w", source, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("mute: commit: %w", err)
	}
	return nil
}

// disableMute turns off every row of the mute.
func disableMute(ctx context.Context, db *sqlx.DB, m muteSpec, userID string) error {
	args := append(m.ownedArgs(), userID)
	if _, err := db.ExecContext(ctx, `
		UPDATE event_triage_rules SET enabled = FALSE, updated_by = $9, updated_at = NOW()
		WHERE enabled AND `+ownedRowsSQL, args...); err != nil {
		return fmt.Errorf("unmute: %w", err)
	}
	return nil
}

// specFor builds the mute of the given scope for the event.
func (b eventMuteBasis) specFor(tenantID, accountID, scope string) (muteSpec, error) {
	m := muteSpec{
		tenantID: tenantID, accountID: accountID,
		ruleID: b.ruleID, alert: b.alert, scope: scope, sources: b.sources,
	}
	switch scope {
	case eventrule.MuteScopeRule:
	case eventrule.MuteScopeWorkload:
		// Both are required: webhook subject resolution can set the owner and leave
		// the namespace empty, and an owner-only match would mute that workload
		// name in every namespace.
		if b.namespace == "" || b.owner == "" {
			return muteSpec{}, fmt.Errorf("%w: this event names no workload", eventrule.ErrOperationUnavailable)
		}
		m.namespace, m.owner = b.namespace, b.owner
	default:
		return muteSpec{}, fmt.Errorf("unknown mute scope %q", scope)
	}
	return m, nil
}

// offeredScopes lists the scopes the event supports, narrowest first.
func (b eventMuteBasis) offeredScopes() []EventMuteScope {
	var out []EventMuteScope
	if b.namespace != "" && b.owner != "" {
		out = append(out, EventMuteScope{Scope: eventrule.MuteScopeWorkload, Namespace: b.namespace, Owner: b.owner})
	}
	return append(out, EventMuteScope{Scope: eventrule.MuteScopeRule})
}

// muteSourcesFor returns the event sources a mute of this event covers.
func muteSourcesFor(eventSource string, rule *eventrule.RuleCapabilities) []string {
	if rule != nil && rule.Operations.Mute.Available {
		if s := eventrule.MuteEventSources(rule.Source); slices.Contains(s, eventSource) {
			return s
		}
	}
	return []string{eventSource}
}

func loadEventForMute(ctx context.Context, db *sqlx.DB, tenantID, accountID, eventID string) (*models.Event, error) {
	var ev models.Event
	err := db.GetContext(ctx, &ev, `
		SELECT id, source, aggregation_key, subject_namespace, subject_owner, priority,
		       finding_type, labels, fingerprint, cloud_account_id, tenant
		FROM events WHERE id = $1 AND cloud_account_id = $2 AND tenant = $3`, eventID, accountID, tenantID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("event not found")
	}
	if err != nil {
		return nil, fmt.Errorf("mute: load event: %w", err)
	}
	return &ev, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// resolveEventMuteBasis loads the event and the rule it belongs to.
func resolveEventMuteBasis(ctx *security.RequestContext, db *sqlx.DB, tenantID, accountID, eventID string) (*eventMuteBasis, error) {
	ev, err := loadEventForMute(ctx.GetContext(), db, tenantID, accountID, eventID)
	if err != nil {
		return nil, err
	}
	b := &eventMuteBasis{
		event:     ev,
		source:    deref(ev.Source),
		alert:     deref(ev.AggregationKey),
		namespace: deref(ev.SubjectNamespace),
		owner:     deref(ev.SubjectOwner),
	}
	b.ruleID, err = eventrule.FindEventRuleID(ctx.GetContext(), db, tenantID, accountID, b.source, b.alert)
	if err != nil {
		return nil, err
	}
	if b.ruleID != "" {
		target, err := eventrule.ResolveRuleTarget(ctx, b.ruleID, eventrule.ResolveOptions{})
		switch {
		case err == nil:
			caps := target.Capabilities()
			b.rule = &caps
			b.definition = target.EditableDefinition()
			if target.PromRule.Name != "" {
				b.location = &EventRuleLocation{Namespace: target.PromRule.Namespace, Name: target.PromRule.Name, ManagedBy: target.PromRule.ManagedBy}
			}
		case errors.Is(err, eventrule.ErrRuleNotFound):
			b.ruleID = ""
		default:
			return nil, err
		}
	}
	b.sources = muteSourcesFor(b.source, b.rule)
	return b, nil
}

// countScope counts the last 7 days' events a scope matches, and their workloads.
func countScope(ctx context.Context, db *sqlx.DB, accountID string, m muteSpec) (int, int, error) {
	var out struct {
		Matched   int `db:"matched"`
		Workloads int `db:"workloads"`
	}
	err := db.GetContext(ctx, &out, `
		SELECT COUNT(*) AS matched,
		       COUNT(DISTINCT (subject_namespace, subject_owner))
		         FILTER (WHERE COALESCE(subject_owner, '') <> '') AS workloads
		FROM events
		WHERE cloud_account_id = $1 AND created_at > $2
		  AND source = ANY($3) AND aggregation_key = $4
		  AND ($5 = '' OR (subject_namespace = $5 AND subject_owner = $6))`,
		accountID, time.Now().UTC().Add(-muteLookback), pq.Array(m.sources), m.alert, m.namespace, m.owner)
	if err != nil {
		return 0, 0, fmt.Errorf("mute: count events: %w", err)
	}
	return out.Matched, out.Workloads, nil
}

// muteRowScope reads a suppression rule's mute tags.
func muteRowScope(rule *TriageRule) (isMute bool, scope string) {
	if !isMuteRule(rule) {
		return false, ""
	}
	var v map[string]any
	_ = json.Unmarshal([]byte(*rule.ActionValue), &v)
	if s, _ := v[eventrule.MuteScopeKey].(string); s != "" {
		return true, s
	}
	return true, eventrule.MuteScopeRule
}

// activeSuppressions lists the enabled, unexpired suppression rules matching the
// event, freshest state (the local rule cache is cleared on every mute write).
func activeSuppressions(ctx context.Context, db *sqlx.DB, tenantID, accountID string, ev *models.Event) ([]EventSuppression, error) {
	rules, err := LoadMatchingRules(ctx, db, tenantID, accountID)
	if err != nil {
		return nil, fmt.Errorf("mute: load triage rules: %w", err)
	}
	out := []EventSuppression{}
	for i := range rules {
		r := &rules[i]
		if r.RuleType != RuleTypeSuppression || !MatchesRule(ev, r) {
			continue
		}
		isMute, scope := muteRowScope(r)
		out = append(out, EventSuppression{
			RuleID: r.ID, Name: deref(r.Name), IsMute: isMute, MuteScope: scope, ExpiresAt: r.EffectiveUntil,
		})
	}
	return out, nil
}

// markMutedScopes flags the offered scopes an active mute covers. A scope muted by
// several rows ends with the latest of them; any row without an end means until
// unmuted.
func markMutedScopes(scopes []EventMuteScope, supps []EventSuppression) {
	for i := range scopes {
		var ends []*time.Time
		for _, s := range supps {
			if s.IsMute && s.MuteScope == scopes[i].Scope {
				ends = append(ends, s.ExpiresAt)
			}
		}
		if len(ends) == 0 {
			continue
		}
		scopes[i].Muted = true
		scopes[i].ExpiresAt = latestEnd(ends)
	}
}

func latestEnd(ends []*time.Time) *time.Time {
	var latest *time.Time
	for _, e := range ends {
		if e == nil {
			return nil
		}
		if latest == nil || e.After(*latest) {
			latest = e
		}
	}
	return latest
}

func buildEventAlertControls(ctx *security.RequestContext, db *sqlx.DB, tenantID, accountID string, b *eventMuteBasis) (*EventAlertControls, error) {
	out := &EventAlertControls{EventID: b.event.Id, Alert: b.alert, Source: b.source, Rule: b.rule, RuleLocation: b.location, RuleDefinition: b.definition}
	supps, err := activeSuppressions(ctx.GetContext(), db, tenantID, accountID, b.event)
	if err != nil {
		return nil, err
	}
	out.Suppressions = supps
	if b.alert == "" {
		out.Scopes = []EventMuteScope{}
		return out, nil
	}
	scopes := b.offeredScopes()
	for i := range scopes {
		m, err := b.specFor(tenantID, accountID, scopes[i].Scope)
		if err != nil {
			return nil, err
		}
		scopes[i].Matched7d, scopes[i].Workloads7d, err = countScope(ctx.GetContext(), db, accountID, m)
		if err != nil {
			return nil, err
		}
	}
	markMutedScopes(scopes, supps)
	out.Scopes = scopes
	return out, nil
}

// GetEventAlertControls is the eventrules_get_event_alert_controls handler body.
func GetEventAlertControls(ctx *security.RequestContext, req EventAlertControlsRequest) (*EventAlertControls, error) {
	sc := ctx.GetSecurityContext()
	if sc == nil || sc.GetTenantId() == "" {
		return nil, fmt.Errorf("unauthorized")
	}
	if req.EventID == "" || req.AccountID == "" {
		return nil, fmt.Errorf("event_id and account_id are required")
	}
	if !sc.HasAccountAccess(req.AccountID, security.SecurityAccessTypeRead) {
		return nil, fmt.Errorf("account unauthorized")
	}
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, fmt.Errorf("mute: database manager: %w", err)
	}
	b, err := resolveEventMuteBasis(ctx, dbms.Db, sc.GetTenantId(), req.AccountID, req.EventID)
	if err != nil {
		return nil, err
	}
	return buildEventAlertControls(ctx, dbms.Db, sc.GetTenantId(), req.AccountID, b)
}

// UpdateEventMute mutes or unmutes the event's alert at one scope and returns the
// fresh controls.
func UpdateEventMute(ctx *security.RequestContext, req UpdateEventMuteRequest) (*EventAlertControls, error) {
	sc := ctx.GetSecurityContext()
	if sc == nil || sc.GetTenantId() == "" || sc.GetUserId() == "" {
		return nil, fmt.Errorf("unauthorized")
	}
	if req.EventID == "" || req.AccountID == "" {
		return nil, fmt.Errorf("event_id and account_id are required")
	}
	if !sc.HasAccountAccess(req.AccountID, security.SecurityAccessTypeUpdate) {
		return nil, fmt.Errorf("account unauthorized")
	}
	if req.Muted && !slices.Contains(muteDurationsHours, req.DurationHours) {
		return nil, fmt.Errorf("duration_hours must be one of %v", muteDurationsHours)
	}
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, fmt.Errorf("mute: database manager: %w", err)
	}
	tenantID := sc.GetTenantId()
	b, err := resolveEventMuteBasis(ctx, dbms.Db, tenantID, req.AccountID, req.EventID)
	if err != nil {
		return nil, err
	}
	m, err := b.specFor(tenantID, req.AccountID, req.Scope)
	if err != nil {
		return nil, err
	}
	if req.Muted {
		var expiresAt *time.Time
		if req.DurationHours > 0 {
			t := time.Now().UTC().Add(time.Duration(req.DurationHours) * time.Hour)
			expiresAt = &t
		}
		err = upsertMute(ctx.GetContext(), dbms.Db, m, sc.GetUserId(), expiresAt)
	} else {
		err = disableMute(ctx.GetContext(), dbms.Db, m, sc.GetUserId())
	}
	if err != nil {
		return nil, err
	}
	ClearTriageRulesCache(sc.GetTenantId())
	if b.ruleID != "" {
		// Muted / muted_workloads changed; re-read the rule.
		if target, err := eventrule.ResolveRuleTarget(ctx, b.ruleID, eventrule.ResolveOptions{}); err == nil {
			caps := target.Capabilities()
			b.rule = &caps
			b.definition = target.EditableDefinition()
		}
	}
	return buildEventAlertControls(ctx, dbms.Db, tenantID, req.AccountID, b)
}
