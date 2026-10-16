package toolapproval

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"nudgebee/llm/common"
)

const selectColumns = `id, tenant_id, account_id, name, enabled, tool_name, tool_config_id, match_kind, command_pattern, working_dir, conditions, allow_unattended, revision, created_by, updated_by, created_at, updated_at`

var (
	ErrRevisionConflict = errors.New("tool approval: rule revision conflict")
	ErrRuleLimit        = errors.New("tool approval: rule limit reached")
)

// Integration is a tenant integration a rule can be pinned to.
type Integration struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// MCPIntegration is an enabled MCP integration and its write_tools setting:
// the server's tool names, comma separated, whose calls ask for approval.
type MCPIntegration struct {
	ID         string
	Name       string
	WriteTools string
}

// ListTenantMCPIntegrations returns the tenant's enabled MCP integrations with
// their saved write_tools, read from the database without contacting the
// servers. write_tools is a plain setting, never a secret.
func ListTenantMCPIntegrations(ctx context.Context, tenantID uuid.UUID) ([]MCPIntegration, error) {
	db, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, fmt.Errorf("toolapproval.ListTenantMCPIntegrations: db: %w", err)
	}
	rows, err := db.Db.QueryContext(ctx, `SELECT i.id::text, i.name::text, COALESCE(icv.value, '')
		FROM public.integrations i
		LEFT JOIN public.integration_config_values icv
			ON icv.integration_id = i.id AND icv.name = 'write_tools' AND NOT COALESCE(icv.is_encrypted, false)
		WHERE i.tenant_id=$1 AND i.type='mcp' AND i.status='enabled' ORDER BY i.name`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("toolapproval.ListTenantMCPIntegrations: query: %w", err)
	}
	defer func() { _ = rows.Close() }()
	integrations := []MCPIntegration{}
	for rows.Next() {
		var integration MCPIntegration
		if err := rows.Scan(&integration.ID, &integration.Name, &integration.WriteTools); err != nil {
			return nil, fmt.Errorf("toolapproval.ListTenantMCPIntegrations: scan: %w", err)
		}
		integrations = append(integrations, integration)
	}
	return integrations, rows.Err()
}

// TenantIntegrationNames maps each of the tenant's integrations, of any type or
// status, to its name, so a rule pinned to one can be shown by name.
func TenantIntegrationNames(ctx context.Context, tenantID uuid.UUID) (map[string]string, error) {
	db, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, fmt.Errorf("toolapproval.TenantIntegrationNames: db: %w", err)
	}
	rows, err := db.Db.QueryContext(ctx, `SELECT id::text, name::text FROM public.integrations WHERE tenant_id=$1`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("toolapproval.TenantIntegrationNames: query: %w", err)
	}
	defer func() { _ = rows.Close() }()
	names := map[string]string{}
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, fmt.Errorf("toolapproval.TenantIntegrationNames: scan: %w", err)
		}
		names[id] = name
	}
	return names, rows.Err()
}

// ListTenantIntegrations returns the tenant's enabled integrations of the given
// types, by id and name only (never their configuration values).
func ListTenantIntegrations(ctx context.Context, tenantID uuid.UUID, types []string) ([]Integration, error) {
	db, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, fmt.Errorf("toolapproval.ListTenantIntegrations: db: %w", err)
	}
	rows, err := db.Db.QueryContext(ctx, `SELECT id::text, name FROM public.integrations WHERE tenant_id=$1 AND type = ANY($2) AND status='enabled' ORDER BY name`, tenantID, pq.Array(types))
	if err != nil {
		return nil, fmt.Errorf("toolapproval.ListTenantIntegrations: query: %w", err)
	}
	defer func() { _ = rows.Close() }()
	integrations := []Integration{}
	for rows.Next() {
		var integration Integration
		if err := rows.Scan(&integration.ID, &integration.Name); err != nil {
			return nil, fmt.Errorf("toolapproval.ListTenantIntegrations: scan: %w", err)
		}
		integrations = append(integrations, integration)
	}
	return integrations, rows.Err()
}

func Get(ctx context.Context, tenantID, id uuid.UUID) (*Rule, error) {
	db, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, fmt.Errorf("toolapproval.Get: db: %w", err)
	}
	rule, err := scanRule(db.Db.QueryRowContext(ctx, `SELECT `+selectColumns+` FROM public.llm_tool_approval_rules WHERE tenant_id=$1 AND id=$2`, tenantID, id).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return rule, err
}

func List(ctx context.Context, tenantID uuid.UUID) ([]Rule, error) {
	db, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, fmt.Errorf("toolapproval.List: db: %w", err)
	}
	rows, err := db.Db.QueryContext(ctx, `SELECT `+selectColumns+` FROM public.llm_tool_approval_rules WHERE tenant_id=$1 ORDER BY name`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("toolapproval.List: query: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var rules []Rule
	for rows.Next() {
		rule, err := scanRule(rows.Scan)
		if err != nil {
			return nil, err
		}
		rules = append(rules, *rule)
	}
	if rules == nil {
		rules = []Rule{}
	}
	return rules, rows.Err()
}

// lookupLimit bounds one lookup: a call sees its account's rules and the
// organisation's rules for the tool, each capped at MaxRulesPerTool on save.
const lookupLimit = 2 * MaxRulesPerTool

// FindMatch returns the first rule that waives this call. With no signed-in
// person (attended false), only rules that allow unattended runs are considered.
func FindMatch(ctx context.Context, tenantID, accountID uuid.UUID, toolName, input, toolConfigID string, attended bool) (*Match, error) {
	db, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, fmt.Errorf("toolapproval.FindMatch: db: %w", err)
	}
	rows, err := db.Db.QueryContext(ctx, `SELECT `+selectColumns+` FROM public.llm_tool_approval_rules WHERE tenant_id=$1 AND (account_id=$2 OR account_id IS NULL) AND enabled=TRUE AND lower(tool_name)=lower($3) AND ($5 OR allow_unattended) ORDER BY (account_id IS NULL), updated_at DESC LIMIT $4`, tenantID, accountID, toolName, lookupLimit+1, attended)
	if err != nil {
		return nil, fmt.Errorf("toolapproval.FindMatch: query: %w", err)
	}
	defer func() { _ = rows.Close() }()
	rules := make([]Rule, 0, lookupLimit+1)
	for rows.Next() {
		rule, err := scanRule(rows.Scan)
		if err != nil {
			return nil, err
		}
		rules = append(rules, *rule)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(rules) > lookupLimit {
		return nil, ErrRuleLimit
	}
	for _, rule := range rules {
		if matched, _ := MatchRule(rule, toolName, input, toolConfigID); matched {
			return &Match{RuleID: rule.ID, Revision: rule.Revision, RuleName: rule.Name, Kind: rule.MatchKind}, nil
		}
	}
	return nil, nil
}

func Upsert(ctx context.Context, rule *Rule, actor uuid.UUID) (*Rule, error) {
	if err := ValidateRule(*rule); err != nil {
		return nil, err
	}
	db, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, fmt.Errorf("toolapproval.Upsert: db: %w", err)
	}
	if rule.ID == uuid.Nil {
		created, createErr := Create(ctx, []*Rule{rule}, actor)
		if createErr != nil {
			return nil, createErr
		}
		return created[0], nil
	} else {
		if rule.Revision <= 0 {
			return nil, ErrRevisionConflict
		}
		tx, txErr := db.Db.BeginTx(ctx, nil)
		if txErr != nil {
			return nil, fmt.Errorf("toolapproval.Upsert: begin: %w", txErr)
		}
		defer func() { _ = tx.Rollback() }()
		if _, txErr = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1::text, 0))`, rule.TenantID); txErr != nil {
			return nil, fmt.Errorf("toolapproval.Upsert: lock: %w", txErr)
		}
		var toolCount int
		if txErr = tx.QueryRowContext(ctx, `SELECT count(*) FROM public.llm_tool_approval_rules WHERE tenant_id=$1 AND account_id IS NOT DISTINCT FROM $2 AND lower(tool_name)=lower($3) AND id<>$4`, rule.TenantID, rule.AccountID, rule.ToolName, rule.ID).Scan(&toolCount); txErr != nil {
			return nil, fmt.Errorf("toolapproval.Upsert: count: %w", txErr)
		}
		if toolCount >= MaxRulesPerTool {
			return nil, ErrRuleLimit
		}
		err = tx.QueryRowContext(ctx, `UPDATE public.llm_tool_approval_rules SET account_id=$3,name=$4,enabled=$5,tool_name=$6,tool_config_id=$7,match_kind=$8,command_pattern=$9,working_dir=$10,conditions=$11,updated_by=$12,allow_unattended=$14,revision=revision+1,updated_at=NOW() WHERE id=$1 AND tenant_id=$2 AND revision=$13 RETURNING `+selectColumns,
			rule.ID, rule.TenantID, rule.AccountID, rule.Name, rule.Enabled, rule.ToolName, rule.ToolConfigID, rule.MatchKind, rule.CommandPattern, rule.WorkingDir, rule.Conditions, nullableUUID(actor), rule.Revision, rule.AllowUnattended).Scan(ruleScanTargets(rule)...)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrRevisionConflict
		}
		if duplicate := duplicateNameError(err, rule); duplicate != nil {
			return nil, duplicate
		}
		if err == nil {
			err = recordRuleEvent(ctx, tx, rule, actor, "update")
		}
		if err == nil {
			err = tx.Commit()
		}
	}
	if err != nil {
		return nil, fmt.Errorf("toolapproval.Upsert: exec: %w", err)
	}
	return rule, nil
}

// duplicateNameError is the message for a rule name already taken in the
// rule's scope, or nil when err is not that unique violation.
func duplicateNameError(err error, rule *Rule) error {
	var pgErr *pq.Error
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		return nil
	}
	if rule.AllAccounts() {
		return fmt.Errorf("tool approval: an all-accounts rule named %q already exists", rule.Name)
	}
	return fmt.Errorf("tool approval: a rule named %q already exists for account %s", rule.Name, rule.AccountID)
}

// Create inserts one rule per entry in a single transaction, so a rule added to
// several accounts is either saved for all of them or for none.
func Create(ctx context.Context, rules []*Rule, actor uuid.UUID) ([]*Rule, error) {
	if len(rules) == 0 {
		return nil, errors.New("tool approval: at least one account is required")
	}
	tenantID := rules[0].TenantID
	for _, rule := range rules {
		if rule.ID != uuid.Nil || rule.TenantID != tenantID {
			return nil, errors.New("tool approval: invalid rule for create")
		}
		if err := ValidateRule(*rule); err != nil {
			return nil, err
		}
	}
	db, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, fmt.Errorf("toolapproval.Create: db: %w", err)
	}
	tx, err := db.Db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("toolapproval.Create: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1::text, 0))`, tenantID); err != nil {
		return nil, fmt.Errorf("toolapproval.Create: lock: %w", err)
	}
	var tenantCount int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM public.llm_tool_approval_rules WHERE tenant_id=$1`, tenantID).Scan(&tenantCount); err != nil {
		return nil, fmt.Errorf("toolapproval.Create: count: %w", err)
	}
	if tenantCount+len(rules) > MaxRulesTenant {
		return nil, ErrRuleLimit
	}
	for _, rule := range rules {
		var toolCount int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM public.llm_tool_approval_rules WHERE tenant_id=$1 AND account_id IS NOT DISTINCT FROM $2 AND lower(tool_name)=lower($3)`, rule.TenantID, rule.AccountID, rule.ToolName).Scan(&toolCount); err != nil {
			return nil, fmt.Errorf("toolapproval.Create: count: %w", err)
		}
		if toolCount >= MaxRulesPerTool {
			return nil, ErrRuleLimit
		}
		rule.ID = uuid.New()
		if err = tx.QueryRowContext(ctx, `INSERT INTO public.llm_tool_approval_rules (id,tenant_id,account_id,name,enabled,tool_name,tool_config_id,match_kind,command_pattern,working_dir,conditions,created_by,updated_by,allow_unattended) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$12,$13) RETURNING `+selectColumns,
			rule.ID, rule.TenantID, rule.AccountID, rule.Name, rule.Enabled, rule.ToolName, rule.ToolConfigID, rule.MatchKind, rule.CommandPattern, rule.WorkingDir, rule.Conditions, nullableUUID(actor), rule.AllowUnattended).Scan(ruleScanTargets(rule)...); err != nil {
			if duplicate := duplicateNameError(err, rule); duplicate != nil {
				return nil, duplicate
			}
			return nil, fmt.Errorf("toolapproval.Create: exec: %w", err)
		}
		if err = recordRuleEvent(ctx, tx, rule, actor, "create"); err != nil {
			return nil, fmt.Errorf("toolapproval.Create: %w", err)
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("toolapproval.Create: commit: %w", err)
	}
	return rules, nil
}

func Delete(ctx context.Context, tenantID, id uuid.UUID, revision int, actor uuid.UUID) (*Rule, error) {
	db, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, fmt.Errorf("toolapproval.Delete: db: %w", err)
	}
	if revision <= 0 {
		return nil, ErrRevisionConflict
	}
	tx, err := db.Db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("toolapproval.Delete: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1::text, 0))`, tenantID); err != nil {
		return nil, fmt.Errorf("toolapproval.Delete: lock: %w", err)
	}
	rule, err := scanRule(tx.QueryRowContext(ctx, `SELECT `+selectColumns+` FROM public.llm_tool_approval_rules WHERE tenant_id=$1 AND id=$2 AND revision=$3 FOR UPDATE`, tenantID, id, revision).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrRevisionConflict
	}
	if err != nil {
		return nil, fmt.Errorf("toolapproval.Delete: select: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM public.llm_tool_approval_rules WHERE tenant_id=$1 AND id=$2`, tenantID, id); err != nil {
		return nil, fmt.Errorf("toolapproval.Delete: exec: %w", err)
	}
	if err = recordRuleEvent(ctx, tx, rule, actor, "delete"); err != nil {
		return nil, fmt.Errorf("toolapproval.Delete: audit: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("toolapproval.Delete: commit: %w", err)
	}
	return rule, nil
}

func recordRuleEvent(ctx context.Context, tx *sql.Tx, rule *Rule, actor uuid.UUID, action string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO public.llm_tool_approval_rule_events
		(tenant_id,account_id,rule_id,rule_revision,action,actor_id,rule_name,tool_name,tool_config_id,match_kind,enabled,
		 command_pattern,working_dir,conditions,allow_unattended)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
		rule.TenantID, rule.AccountID, rule.ID, rule.Revision, action, nullableUUID(actor),
		rule.Name, rule.ToolName, rule.ToolConfigID, rule.MatchKind, rule.Enabled,
		rule.CommandPattern, rule.WorkingDir, ruleConditions(rule), rule.AllowUnattended)
	if err != nil {
		return fmt.Errorf("toolapproval: record rule event: %w", err)
	}
	return nil
}

// RecordDecision durably records the confirmation waiver. inserted=false means
// this exact invocation/rule revision was already recorded by a retry or
// another replica; the existing row is sufficient authorization evidence.
func RecordDecision(ctx context.Context, decision Decision) (inserted bool, err error) {
	db, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return false, fmt.Errorf("toolapproval.RecordDecision: db: %w", err)
	}
	result, err := db.Db.ExecContext(ctx, `
		INSERT INTO public.llm_tool_approval_decisions
				(tenant_id, account_id, rule_id, rule_revision, rule_name, match_kind,
				 tool_name, tool_config_id, input_fingerprint, user_id, conversation_id, message_id,
				 agent_id, tool_call_id)
			SELECT $1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14
		FROM public.llm_tool_approval_rules
			WHERE id=$3 AND tenant_id=$1 AND (account_id=$2 OR account_id IS NULL)
			  AND revision=$4 AND enabled=TRUE
			  AND (tool_config_id IS NOT DISTINCT FROM $8 OR (account_id IS NULL AND tool_config_id IS NULL))
		ON CONFLICT (tenant_id, conversation_id, message_id, agent_id, tool_call_id, input_fingerprint, rule_id, rule_revision)
		DO NOTHING`, decision.TenantID, decision.AccountID, decision.RuleID,
		decision.RuleRevision, decision.RuleName, decision.MatchKind,
		decision.ToolName, decision.ToolConfigID, decision.InputFingerprint, decision.UserID,
		decision.ConversationID, decision.MessageID, decision.AgentID,
		decision.ToolCallID)
	if err != nil {
		return false, fmt.Errorf("toolapproval.RecordDecision: exec: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil || count > 0 {
		return count > 0, err
	}
	var exists bool
	err = db.Db.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM public.llm_tool_approval_decisions
		WHERE tenant_id=$1 AND conversation_id=$2 AND message_id=$3 AND agent_id=$4
		  AND tool_call_id=$5 AND input_fingerprint=$6 AND rule_id=$7 AND rule_revision=$8
	)`, decision.TenantID, decision.ConversationID, decision.MessageID,
		decision.AgentID, decision.ToolCallID, decision.InputFingerprint,
		decision.RuleID, decision.RuleRevision).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("toolapproval.RecordDecision: verify conflict: %w", err)
	}
	if !exists {
		return false, errors.New("toolapproval.RecordDecision: rule was changed or disabled before the decision was recorded")
	}
	return false, nil
}

type scanner func(dest ...any) error

func scanRule(scan scanner) (*Rule, error) {
	rule := &Rule{}
	if err := scan(ruleScanTargets(rule)...); err != nil {
		return nil, fmt.Errorf("toolapproval: scan rule: %w", err)
	}
	return rule, nil
}

func ruleScanTargets(rule *Rule) []any {
	return []any{&rule.ID, &rule.TenantID, &rule.AccountID, &rule.Name, &rule.Enabled, &rule.ToolName, &rule.ToolConfigID, &rule.MatchKind, &rule.CommandPattern, &rule.WorkingDir, &rule.Conditions, &rule.AllowUnattended, &rule.Revision, &rule.CreatedBy, &rule.UpdatedBy, &rule.CreatedAt, &rule.UpdatedAt}
}

// ruleConditions is the rule's conditions for storage, never JSON null.
func ruleConditions(rule *Rule) json.RawMessage {
	if len(rule.Conditions) == 0 {
		return json.RawMessage("[]")
	}
	return rule.Conditions
}

func nullableUUID(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	return id
}
