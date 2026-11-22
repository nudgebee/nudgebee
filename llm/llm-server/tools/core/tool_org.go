package core

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/jmoiron/sqlx"

	"nudgebee/llm/common"
	"nudgebee/llm/security"
)

// Organisation-wide custom tools (#41073): who can use one, and each account's
// switch. The visibility rule itself is in org_items.go.

// SetCustomToolVisibility widens an account tool to the organisation (or
// selected accounts), switches between those two, or moves an organisation
// tool back to targetAccountId. Tenant admins, or a tenant-wide ai_tools:Write
// grant (CanShareOrgItem).
func SetCustomToolVisibility(context *security.RequestContext, toolId, visibility string, accountIds []string, targetAccountId string) error {
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return fmt.Errorf("tools: failed to get database manager: %w", err)
	}
	var owner OrgItemOwner
	_, err = dbms.DoInTransaction(func(tx *sqlx.Tx) (any, error) {
		o, err := LockOrgItemOwner(tx, OrgToolKind, toolId)
		if err != nil {
			return nil, err
		}
		owner = o
		if !CanShareOrgItem(context, OrgToolKind, owner.TenantId) {
			return nil, errors.New(errUnauthorized)
		}
		if visibility != VisibilityAccount && !owner.IsOrg() {
			// Widening: the name and settings must be fit for every account.
			if err := CheckOrgToolName(tx, owner.TenantId, owner.Name); err != nil {
				return nil, err
			}
			var config string
			if err := tx.Get(&config, `SELECT COALESCE(config::text, '{}') FROM llm_tools WHERE id = $1::uuid`, toolId); err != nil {
				return nil, fmt.Errorf("tools: failed to read tool config: %w", err)
			}
			cfg := map[string]any{}
			if err := common.UnmarshalJson([]byte(config), &cfg); err == nil {
				if err := CheckOrgToolSecrets(cfg); err != nil {
					return nil, err
				}
			}
		}
		return nil, SetOrgItemVisibility(tx, OrgToolKind, owner, visibility, accountIds, targetAccountId, context.GetSecurityContext().GetUserId())
	})
	if err != nil {
		return err
	}
	InvalidateOrgToolCaches(owner.TenantId, append([]string{owner.AccountId, targetAccountId}, accountIds...)...)
	return nil
}

// SetCustomToolAccountSwitch switches an organisation tool on or off for one
// account. An account admin may switch an organisation-wide tool for their
// account; choosing the accounts of a selected-accounts tool is for tenant
// admins.
func SetCustomToolAccountSwitch(context *security.RequestContext, toolId, accountId string, enabled bool) error {
	if accountId == "" {
		return errors.New(errAccountIDRequired)
	}
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return fmt.Errorf("tools: failed to get database manager: %w", err)
	}
	_, err = dbms.DoInTransaction(func(tx *sqlx.Tx) (any, error) {
		owner, err := LockOrgItemOwner(tx, OrgToolKind, toolId)
		if err != nil {
			return nil, err
		}
		if !CanSwitchOrgItem(context, OrgToolKind, owner, accountId) {
			return nil, errors.New(errUnauthorized)
		}
		return nil, SetOrgItemAccountSwitch(tx, OrgToolKind, owner, accountId, context.GetSecurityContext().GetUserId(), enabled)
	})
	if err != nil {
		return err
	}
	updateToolCache(accountId)
	return nil
}

// CanSwitchOrgItem reports whether the caller may change accountId's switch
// for an organisation item of kind k: whoever may share it (CanShareOrgItem),
// or an admin of accountId for an organisation-wide item.
func CanSwitchOrgItem(context *security.RequestContext, k OrgItemKind, owner OrgItemOwner, accountId string) bool {
	if CanShareOrgItem(context, k, owner.TenantId) {
		return true
	}
	sc := context.GetSecurityContext()
	return owner.Visibility == VisibilityOrganisation && sc.GetTenantId() == owner.TenantId &&
		sc.HasAccountAccess(accountId, security.SecurityAccessTypeUpdate)
}

// ListCustomToolAccountSwitches lists the per-account switches of one
// organisation tool for a caller of its tenant.
func ListCustomToolAccountSwitches(context *security.RequestContext, toolId string) ([]OrgItemAccountSwitch, error) {
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, fmt.Errorf("tools: failed to get database manager: %w", err)
	}
	owner, err := LoadOrgItemOwner(dbms.Db, OrgToolKind, toolId)
	if err != nil {
		return nil, err
	}
	if !owner.IsOrg() || !CanReadOrgItemSwitches(context, OrgToolKind, owner.TenantId) {
		return nil, errors.New(errUnauthorized)
	}
	return ListOrgItemAccountSwitches(dbms.Db, OrgToolKind, toolId)
}

// ListSwitchedOffOrgTools returns the organisation-wide tools accountId switched
// off, marked SwitchedOff, so the tool list can offer to switch them back on.
func ListSwitchedOffOrgTools(accountId string) []ToolDto {
	if accountId == "" {
		return nil
	}
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		slog.Error(logFailedDBManager, "error", err)
		return nil
	}
	rows, err := dbms.Db.Queryx("select lg.* from llm_tools lg where lg.type = $2 and "+OrgToolKind.SwitchedOffScope("lg", 1), accountId, ToolTypeCustom)
	if err != nil {
		slog.Error(logFailedGetTools, "error", err)
		return nil
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error(logFailedCloseRows, "error", err)
		}
	}()
	var tools []ToolDto
	for rows.Next() {
		tool, err := serializeRowToTool(rows)
		if err != nil {
			slog.Error(logFailedScanTool, "error", err)
			continue
		}
		tool.SwitchedOff = true
		tools = append(tools, tool)
	}
	return tools
}
