package core

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"nudgebee/services/audit"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
)

// MCPWriteToolsField is the MCP integration setting listing the server's
// tools that ask for approval before Nubi runs them (#40399).
const MCPWriteToolsField = "write_tools"

// UpdateMCPWriteTools saves only an MCP integration's write_tools, so the MCP
// Servers view can change which tools ask for approval without re-submitting
// (and re-testing) the whole integration. writeTools is the server's own tool
// names, comma separated; empty means every tool runs without asking.
func UpdateMCPWriteTools(ctx *security.RequestContext, integrationID, writeTools string) error {
	if _, err := uuid.Parse(integrationID); err != nil {
		return errors.New("integrations: integration_id must be a valid id")
	}
	names := []string{}
	for _, name := range strings.Split(writeTools, ",") {
		if name = strings.TrimSpace(name); name != "" && !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	value := strings.Join(names, ",")

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return fmt.Errorf("integrations: failed to get database manager: %w", err)
	}
	tenantID := ctx.GetSecurityContext().GetTenantId()
	userID := ctx.GetSecurityContext().GetUserId()

	var name string
	err = dbms.Db.QueryRowxContext(ctx.GetContext(), `SELECT name FROM integrations WHERE id = $1 AND tenant_id = $2 AND lower(type) = 'mcp'`, integrationID, tenantID).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return errors.New("integrations: MCP integration not found")
	}
	if err != nil {
		return fmt.Errorf("integrations: failed to find MCP integration: %w", err)
	}
	var previous sql.NullString
	_ = dbms.Db.QueryRowxContext(ctx.GetContext(), `SELECT value FROM integration_config_values WHERE integration_id = $1 AND name = $2`, integrationID, MCPWriteToolsField).Scan(&previous)

	now := time.Now()
	if _, err = dbms.Db.ExecContext(ctx.GetContext(), `
		INSERT INTO integration_config_values(id, integration_id, name, value, is_encrypted, created_at, updated_at, created_by, updated_by)
		VALUES ($1, $2, $3, $4, false, $5, $5, $6, $6)
		ON CONFLICT(integration_id, name)
		DO UPDATE SET value = excluded.value, is_encrypted = false, updated_at = excluded.updated_at, updated_by = excluded.updated_by`,
		uuid.NewString(), integrationID, MCPWriteToolsField, value, now, userID); err != nil {
		return fmt.Errorf("integrations: failed to save write_tools: %w", err)
	}
	InvalidateIntegrationCache()

	auditEvent := audit.Audit{
		UserId:         userID,
		TenantId:       tenantID,
		EventTime:      now.UTC(),
		EventCategory:  audit.EventCategoryIntegration,
		EventTarget:    "integration",
		EventType:      audit.EventTypeIntegrationUpdate,
		EventState:     map[string]any{"type": "mcp", "name": name, MCPWriteToolsField: value},
		EventPrevState: map[string]any{MCPWriteToolsField: previous.String},
		EventActor:     audit.EventActorUiService,
		EventAction:    audit.EventActionUpdate,
		EventStatus:    audit.EventStatusSuccess,
		EventAttr:      map[string]any{"integration_id": integrationID},
	}
	if auditErr := audit.CreateAudit(ctx, &audit.AuditRequest{Audits: []audit.Audit{auditEvent}}); auditErr != nil {
		slog.Error("integrations: failed to audit MCP write_tools update", "error", auditErr)
	}
	return nil
}

// deleteToolApprovalRulesForIntegration removes the Tool Approvals rules pinned
// to an integration being deleted: they can never match again (#40399). Each is
// recorded in the rules' change log as deleted by whoever deleted the
// integration, as a rule deleted in Tool Approvals is; a deleter with no users
// row (a service caller) is recorded as no one rather than failing the delete.
func deleteToolApprovalRulesForIntegration(ctx *security.RequestContext, tx *sqlx.Tx, tenantID, integrationID string) error {
	var present bool
	if err := tx.QueryRowxContext(ctx.GetContext(), `SELECT to_regclass('public.llm_tool_approval_rules') IS NOT NULL`).Scan(&present); err != nil {
		return fmt.Errorf("integrations: failed to check for the tool approval rules table: %w", err)
	}
	if !present {
		return nil
	}
	var actor any
	if userID, err := uuid.Parse(ctx.GetSecurityContext().GetUserId()); err == nil {
		actor = userID
	}
	if _, err := tx.ExecContext(ctx.GetContext(), `
		WITH removed AS (
			DELETE FROM public.llm_tool_approval_rules
			WHERE tenant_id = $1 AND tool_config_id = $2
			RETURNING *
		)
		INSERT INTO public.llm_tool_approval_rule_events
			(tenant_id, account_id, rule_id, rule_revision, action, actor_id, rule_name, tool_name, tool_config_id,
			 match_kind, enabled, command_pattern, working_dir, conditions, allow_unattended)
		SELECT tenant_id, account_id, id, revision, 'delete', (SELECT u.id FROM public.users u WHERE u.id = $3::uuid), name, tool_name, tool_config_id,
			match_kind, enabled, command_pattern, working_dir, conditions, allow_unattended
		FROM removed`, tenantID, integrationID, actor); err != nil {
		return fmt.Errorf("integrations: failed to remove tool approval rules for the integration: %w", err)
	}
	return nil
}
