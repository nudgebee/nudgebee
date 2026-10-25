package core

import (
	"errors"
	"fmt"

	"github.com/jmoiron/sqlx"

	"nudgebee/llm/common"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
)

// Organisation-wide custom agents (#41073): who can use one, and each
// account's switch. The visibility rule itself is in tools/core/org_items.go.

// SetCustomAgentVisibility widens an account agent to the organisation (or
// selected accounts), switches between those two, or moves an organisation
// agent back to targetAccountId. Tenant admins, or a tenant-wide
// ai_agents:Write grant (CanShareOrgItem).
func SetCustomAgentVisibility(context *security.RequestContext, agentId, visibility string, accountIds []string, targetAccountId string) error {
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return fmt.Errorf("agent: failed to get database manager: %w", err)
	}
	var owner toolcore.OrgItemOwner
	_, err = dbms.DoInTransaction(func(tx *sqlx.Tx) (any, error) {
		o, err := toolcore.LockOrgItemOwner(tx, toolcore.OrgAgentKind, agentId)
		if err != nil {
			return nil, err
		}
		owner = o
		if !toolcore.CanShareOrgItem(context, toolcore.OrgAgentKind, owner.TenantId) {
			return nil, errors.New("auth: unauthorized")
		}
		if visibility != toolcore.VisibilityAccount && !owner.IsOrg() {
			// Widening: its tools must work in every account.
			var toolsJSON string
			if err := tx.Get(&toolsJSON, `SELECT COALESCE(tools::text, '[]') FROM llm_agents WHERE id = $1::uuid`, agentId); err != nil {
				return nil, fmt.Errorf("agent: failed to read tools: %w", err)
			}
			var tools []string
			if err := common.UnmarshalJson([]byte(toolsJSON), &tools); err != nil {
				return nil, fmt.Errorf("agent: failed to read tools: %w", err)
			}
			if _, err := validateOrgAgentTools(tx, owner.TenantId, owner.AccountId, owner.Name, tools); err != nil {
				return nil, err
			}
		}
		return nil, toolcore.SetOrgItemVisibility(tx, toolcore.OrgAgentKind, owner, visibility, accountIds, targetAccountId, context.GetSecurityContext().GetUserId())
	})
	if err != nil {
		return err
	}
	invalidateOrgAgentCaches(owner.TenantId, append([]string{owner.AccountId, targetAccountId}, accountIds...), owner.Name)
	return nil
}

// SetCustomAgentAccountSwitch switches an organisation agent on or off for one
// account. An account admin may switch an organisation-wide agent for their
// account; choosing the accounts of a selected-accounts agent is for tenant
// admins.
func SetCustomAgentAccountSwitch(context *security.RequestContext, agentId, accountId string, enabled bool) error {
	if accountId == "" {
		return errors.New("accountId is required")
	}
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return fmt.Errorf("agent: failed to get database manager: %w", err)
	}
	var name string
	_, err = dbms.DoInTransaction(func(tx *sqlx.Tx) (any, error) {
		owner, err := toolcore.LockOrgItemOwner(tx, toolcore.OrgAgentKind, agentId)
		if err != nil {
			return nil, err
		}
		if !toolcore.CanSwitchOrgItem(context, toolcore.OrgAgentKind, owner, accountId) {
			return nil, errors.New("auth: unauthorized")
		}
		name = owner.Name
		return nil, toolcore.SetOrgItemAccountSwitch(tx, toolcore.OrgAgentKind, owner, accountId, context.GetSecurityContext().GetUserId(), enabled)
	})
	if err != nil {
		return err
	}
	InvalidateCustomAgentToolsCache(accountId, name)
	return nil
}

// ListCustomAgentAccountSwitches lists the per-account switches of one
// organisation agent for a caller of its tenant.
func ListCustomAgentAccountSwitches(context *security.RequestContext, agentId string) ([]toolcore.OrgItemAccountSwitch, error) {
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, fmt.Errorf("agent: failed to get database manager: %w", err)
	}
	owner, err := toolcore.LoadOrgItemOwner(dbms.Db, toolcore.OrgAgentKind, agentId)
	if err != nil {
		return nil, err
	}
	if !owner.IsOrg() || !toolcore.CanReadOrgItemSwitches(context, toolcore.OrgAgentKind, owner.TenantId) {
		return nil, errors.New("auth: unauthorized")
	}
	return toolcore.ListOrgItemAccountSwitches(dbms.Db, toolcore.OrgAgentKind, agentId)
}

// ListSwitchedOffOrgAgents returns the organisation-wide agents accountId
// switched off, marked SwitchedOff, so the agent list can offer to switch them
// back on. No other read returns them.
func ListSwitchedOffOrgAgents(context *security.RequestContext, accountId string) []AgentDto {
	if accountId == "" {
		return nil
	}
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		getLogger(context).Error("ListSwitchedOffOrgAgents: failed to get database manager", "error", err)
		return nil
	}
	rows, err := dbms.Db.Queryx("select lg.* from llm_agents lg where lg.type = $2 and "+toolcore.OrgAgentKind.SwitchedOffScope("lg", 1), accountId, AgentTypeCustom)
	if err != nil {
		getLogger(context).Error("ListSwitchedOffOrgAgents: query failed", "error", err, "accountId", accountId)
		return nil
	}
	defer func() {
		if err := rows.Close(); err != nil {
			getLogger(context).Error("ListSwitchedOffOrgAgents: failed to close rows", "error", err)
		}
	}()
	var agents []AgentDto
	for rows.Next() {
		agent, err := serializeRowToAgent(context, rows)
		if err != nil {
			getLogger(context).Error("ListSwitchedOffOrgAgents: failed to serialize row", "error", err)
			continue
		}
		agent.SwitchedOff = true
		agents = append(agents, agent)
	}
	return agents
}
