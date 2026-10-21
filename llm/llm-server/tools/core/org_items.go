package core

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"

	"nudgebee/llm/common"
	"nudgebee/llm/security"
)

// Organisation-wide custom agents and tools (#41073) follow the organisation
// KB rules with the same visibility values:
//
//   - account: owned by its single installation row, as before;
//   - organisation: no installation row; every account of tenant_id, minus
//     accounts with an override row enabled = false;
//   - selected_accounts: no installation row; only accounts with an override
//     row enabled = true.
//
// Once a name is organisation-wide it is unique in the tenant (case-insensitive),
// so a name means the same agent or tool in every account and name lookups need
// no notion of where an item came from.

// OrgItemKind describes the tables behind one kind of custom item.
type OrgItemKind struct {
	Label          string // "agent" or "tool", for messages
	Table          string
	InstallTable   string
	InstallCol     string
	InstallColText bool // llm_agents_installation.agent_id is TEXT
	OverrideTable  string
	OverrideCol    string
}

var (
	OrgAgentKind = OrgItemKind{
		Label: "agent", Table: "llm_agents",
		InstallTable: "llm_agents_installation", InstallCol: "agent_id", InstallColText: true,
		OverrideTable: "llm_agent_account_overrides", OverrideCol: "agent_id",
	}
	OrgToolKind = OrgItemKind{
		Label: "tool", Table: "llm_tools",
		InstallTable: "llm_tools_installation", InstallCol: "tool_id",
		OverrideTable: "llm_tool_account_overrides", OverrideCol: "tool_id",
	}
)

// article is "an agent" or "a tool".
func (k OrgItemKind) article() string {
	if strings.ContainsRune("aeiou", rune(k.Label[0])) {
		return "an " + k.Label
	}
	return "a " + k.Label
}

func (k OrgItemKind) idExpr(alias string) string {
	if k.InstallColText {
		return alias + ".id::text"
	}
	return alias + ".id"
}

// AccountScope returns the predicate limiting table alias `alias` to the items
// the account bound to $accountParam may use: its own account items plus the
// organisation items of its tenant that pass the switches.
func (k OrgItemKind) AccountScope(alias string, accountParam int) string {
	p := fmt.Sprintf("$%d", accountParam)
	return fmt.Sprintf(`((%[1]s.visibility = 'account' AND EXISTS (SELECT 1 FROM %[2]s inst WHERE inst.%[3]s = %[4]s AND inst.account_id = %[5]s))
	 OR (%[1]s.visibility <> 'account' AND %[1]s.tenant_id = (SELECT ca.tenant FROM cloud_accounts ca WHERE ca.id = %[5]s)
	     AND CASE WHEN %[1]s.visibility = 'organisation'
	              THEN NOT EXISTS (SELECT 1 FROM %[6]s ovr WHERE ovr.%[7]s = %[1]s.id AND ovr.account_id = %[5]s AND NOT ovr.enabled)
	              ELSE EXISTS (SELECT 1 FROM %[6]s ovr WHERE ovr.%[7]s = %[1]s.id AND ovr.account_id = %[5]s AND ovr.enabled) END))`,
		alias, k.InstallTable, k.InstallCol, k.idExpr(alias), p, k.OverrideTable, k.OverrideCol)
}

// SwitchedOffScope returns the predicate for the organisation-wide items of the
// account's tenant that the account switched off. Only the lists show them, so
// an account admin can switch them back on.
func (k OrgItemKind) SwitchedOffScope(alias string, accountParam int) string {
	p := fmt.Sprintf("$%d", accountParam)
	return fmt.Sprintf(`(%[1]s.visibility = 'organisation' AND %[1]s.tenant_id = (SELECT ca.tenant FROM cloud_accounts ca WHERE ca.id = %[2]s)
	 AND EXISTS (SELECT 1 FROM %[3]s ovr WHERE ovr.%[4]s = %[1]s.id AND ovr.account_id = %[2]s AND NOT ovr.enabled))`,
		alias, p, k.OverrideTable, k.OverrideCol)
}

// Visibility values, the same strings as organisation knowledge bases use.
const (
	VisibilityAccount          = "account"
	VisibilityOrganisation     = "organisation"
	VisibilitySelectedAccounts = "selected_accounts"
)

// ValidOrgItemVisibility reports whether v is a visibility a caller may request.
func ValidOrgItemVisibility(v string) bool {
	return v == VisibilityAccount || v == VisibilityOrganisation || v == VisibilitySelectedAccounts
}

// CanManageOrgItems reports whether the caller may create or change the
// organisation agents and tools of tenantId: a tenant admin of that tenant
// (or a super admin), as for organisation knowledge bases.
func CanManageOrgItems(sc *security.RequestContext, tenantId string) bool {
	s := sc.GetSecurityContext()
	if !s.HasTenantAccess(security.SecurityAccessTypeUpdate) {
		return false
	}
	return s.IsSuperAdmin() || s.GetTenantId() == tenantId
}

// OrgItemOwner is who owns one custom agent or tool.
type OrgItemOwner struct {
	Id         string `db:"id"`
	Name       string `db:"name"`
	TenantId   string `db:"tenant_id"`
	Visibility string `db:"visibility"`
	AccountId  string `db:"account_id"` // installation account; "" for an organisation item
}

// IsOrg reports whether the item is organisation-wide or shared with selected accounts.
func (o OrgItemOwner) IsOrg() bool { return o.Visibility != VisibilityAccount }

// LoadOrgItemOwner reads who owns item id of kind k.
func LoadOrgItemOwner(q sqlx.Queryer, k OrgItemKind, id string) (OrgItemOwner, error) {
	var o OrgItemOwner
	err := sqlx.Get(q, &o, fmt.Sprintf(`
		SELECT t.id::text AS id, t.name, COALESCE(t.tenant_id::text, '') AS tenant_id, t.visibility,
		       COALESCE((SELECT inst.account_id::text FROM %[2]s inst WHERE inst.%[3]s = %[4]s LIMIT 1), '') AS account_id
		FROM %[1]s t WHERE t.id = NULLIF($1, '')::uuid AND t.type = 'custom'`,
		k.Table, k.InstallTable, k.InstallCol, k.idExpr("t")), id)
	if err != nil {
		return OrgItemOwner{}, fmt.Errorf("%s not found - %s: %w", k.Label, id, err)
	}
	return o, nil
}

// LockOrgItemOwner locks item id's row until tx ends and then reads who owns
// it, so the checks made on the owner hold for the rest of tx. Every write to
// an existing item starts with it. The read is a separate statement: one that
// waited for the lock would otherwise see the installation row as it was
// before the other writer committed.
func LockOrgItemOwner(tx *sqlx.Tx, k OrgItemKind, id string) (OrgItemOwner, error) {
	if _, err := tx.Exec(fmt.Sprintf(`SELECT 1 FROM %s WHERE id = NULLIF($1, '')::uuid FOR UPDATE`, k.Table), id); err != nil {
		return OrgItemOwner{}, fmt.Errorf("%s: failed to lock %s: %w", k.Label, id, err)
	}
	return LoadOrgItemOwner(tx, k, id)
}

// CheckOrgItemName serialises name changes for (tenant, name) and refuses a
// name that would make one name mean two items:
//   - an organisation item (org = true) may not share its name with any other
//     custom item of the tenant, account-owned or not;
//   - an account item may not take the name of an organisation item.
//
// It must run inside the transaction that writes the name; the advisory lock is
// released when that transaction ends. excludeId is the item being renamed or
// widened ("" on create).
func CheckOrgItemName(tx *sqlx.Tx, k OrgItemKind, tenantId, name, excludeId string, org bool) error {
	lower := strings.ToLower(strings.TrimSpace(name))
	if _, err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtext($1))`, k.Table+":"+tenantId+":"+lower); err != nil {
		return fmt.Errorf("%s: failed to lock name: %w", k.Label, err)
	}
	var clashes []struct {
		Visibility  string `db:"visibility"`
		AccountName string `db:"account_name"`
	}
	err := tx.Select(&clashes, fmt.Sprintf(`
		SELECT t.visibility, COALESCE(ca.account_name, '') AS account_name
		FROM %[1]s t
		LEFT JOIN %[2]s inst ON inst.%[3]s = %[4]s
		LEFT JOIN cloud_accounts ca ON ca.id = inst.account_id
		WHERE t.type = 'custom' AND t.tenant_id = $1::uuid AND lower(t.name) = $2 AND t.id::text <> $3
		  AND (t.visibility <> 'account' OR inst.account_id IS NOT NULL)`,
		k.Table, k.InstallTable, k.InstallCol, k.idExpr("t")), tenantId, lower, excludeId)
	if err != nil {
		return fmt.Errorf("%s: failed to check name: %w", k.Label, err)
	}
	for _, c := range clashes {
		if c.Visibility != VisibilityAccount {
			return fmt.Errorf("an organisation %s named %s already exists; choose another name", k.Label, name)
		}
		if org {
			return fmt.Errorf("%s named %s already exists in account %s; rename it first", k.article(), name, c.AccountName)
		}
	}
	return nil
}

// ReplaceOrgItemSelection makes accountIds the only accounts switched on for a
// selected_accounts item. Every account must belong to tenantId.
func ReplaceOrgItemSelection(tx *sqlx.Tx, k OrgItemKind, itemId, tenantId, userId string, accountIds []string) error {
	if len(accountIds) == 0 {
		return fmt.Errorf("%s: account_ids is required for selected accounts", k.Label)
	}
	var inTenant int
	if err := tx.Get(&inTenant, `SELECT count(*) FROM cloud_accounts WHERE tenant = $1::uuid AND id = ANY($2::uuid[])`,
		tenantId, pq.Array(accountIds)); err != nil {
		return fmt.Errorf("%s: failed to check accounts: %w", k.Label, err)
	}
	if inTenant != len(uniqueIDs(accountIds)) {
		return fmt.Errorf("%s: every account must belong to the organisation", k.Label)
	}
	if _, err := tx.Exec(fmt.Sprintf(`DELETE FROM %s WHERE %s = $1`, k.OverrideTable, k.OverrideCol), itemId); err != nil {
		return fmt.Errorf("%s: failed to clear accounts: %w", k.Label, err)
	}
	for _, a := range uniqueIDs(accountIds) {
		if err := upsertOrgItemOverride(tx, k, itemId, a, tenantId, userId, true); err != nil {
			return err
		}
	}
	return nil
}

func upsertOrgItemOverride(tx *sqlx.Tx, k OrgItemKind, itemId, accountId, tenantId, userId string, enabled bool) error {
	_, err := tx.Exec(fmt.Sprintf(`
		INSERT INTO %[1]s (%[2]s, account_id, tenant_id, enabled, updated_by, updated_at)
		VALUES ($1, $2, $3, $4, NULLIF($5, '')::uuid, NOW())
		ON CONFLICT (%[2]s, account_id) DO UPDATE SET enabled = EXCLUDED.enabled, updated_by = EXCLUDED.updated_by, updated_at = NOW()`,
		k.OverrideTable, k.OverrideCol), itemId, accountId, tenantId, enabled, userId)
	if err != nil {
		return fmt.Errorf("%s: failed to save account switch: %w", k.Label, err)
	}
	return nil
}

// SetOrgItemVisibility changes who can use item id (already loaded as owner):
//   - widen an account item to organisation / selected_accounts: the name must
//     be free in the tenant, and its installation row is removed;
//   - switch between organisation and selected_accounts;
//   - move an organisation item back to targetAccountId (in the tenant).
//
// Organisation agents may only use organisation-wide items, so an item they use
// can't be narrowed. The caller has checked the tenant-admin permission.
func SetOrgItemVisibility(tx *sqlx.Tx, k OrgItemKind, owner OrgItemOwner, visibility string, accountIds []string, targetAccountId, userId string) error {
	if !ValidOrgItemVisibility(visibility) {
		return fmt.Errorf("%s: invalid visibility %q", k.Label, visibility)
	}
	if owner.Visibility == VisibilityOrganisation && visibility != VisibilityOrganisation {
		if err := CheckNotUsedByOrgAgents(tx, k, owner.TenantId, owner.Name); err != nil {
			return err
		}
	}
	idText := owner.Id
	switch visibility {
	case VisibilityAccount:
		if targetAccountId == "" {
			return fmt.Errorf("%s: target_account_id is required to move it to one account", k.Label)
		}
		var inTenant bool
		if err := tx.Get(&inTenant, `SELECT EXISTS (SELECT 1 FROM cloud_accounts WHERE id = $1::uuid AND tenant = $2::uuid)`, targetAccountId, owner.TenantId); err != nil {
			return fmt.Errorf("%s: failed to check account: %w", k.Label, err)
		}
		if !inTenant {
			return fmt.Errorf("%s: the account must belong to the organisation", k.Label)
		}
		if owner.IsOrg() {
			if _, err := tx.Exec(fmt.Sprintf(`DELETE FROM %s WHERE %s = $1`, k.OverrideTable, k.OverrideCol), idText); err != nil {
				return fmt.Errorf("%s: failed to clear account switches: %w", k.Label, err)
			}
			if _, err := tx.Exec(fmt.Sprintf(`INSERT INTO %s (id, %s, account_id) VALUES (gen_random_uuid(), $1, $2)`, k.InstallTable, k.InstallCol), idText, targetAccountId); err != nil {
				return fmt.Errorf("%s: failed to assign the account: %w", k.Label, err)
			}
		}
	default:
		if !owner.IsOrg() {
			if err := CheckOrgItemName(tx, k, owner.TenantId, owner.Name, owner.Id, true); err != nil {
				return err
			}
			if _, err := tx.Exec(fmt.Sprintf(`DELETE FROM %s WHERE %s = $1`, k.InstallTable, k.InstallCol), idText); err != nil {
				return fmt.Errorf("%s: failed to release the account: %w", k.Label, err)
			}
		}
		if visibility == VisibilitySelectedAccounts {
			if err := ReplaceOrgItemSelection(tx, k, idText, owner.TenantId, userId, accountIds); err != nil {
				return err
			}
		} else if owner.Visibility == VisibilitySelectedAccounts {
			// selected → organisation: the rows were the selection, not switch-offs.
			if _, err := tx.Exec(fmt.Sprintf(`DELETE FROM %s WHERE %s = $1`, k.OverrideTable, k.OverrideCol), idText); err != nil {
				return fmt.Errorf("%s: failed to clear selection: %w", k.Label, err)
			}
		}
	}
	if _, err := tx.Exec(fmt.Sprintf(`UPDATE %s SET visibility = $1, updated_at = NOW() WHERE id = $2`, k.Table), visibility, idText); err != nil {
		return fmt.Errorf("%s: failed to update visibility: %w", k.Label, err)
	}
	return nil
}

// SetOrgItemAccountSwitch switches an organisation item on or off for one
// account of its tenant.
func SetOrgItemAccountSwitch(tx *sqlx.Tx, k OrgItemKind, owner OrgItemOwner, accountId, userId string, enabled bool) error {
	if !owner.IsOrg() {
		return fmt.Errorf("%s: only an organisation %s has per-account switches", k.Label, k.Label)
	}
	var inTenant bool
	if err := tx.Get(&inTenant, `SELECT EXISTS (SELECT 1 FROM cloud_accounts WHERE id = $1::uuid AND tenant = $2::uuid)`, accountId, owner.TenantId); err != nil {
		return fmt.Errorf("%s: failed to check account: %w", k.Label, err)
	}
	if !inTenant {
		return fmt.Errorf("%s: the account must belong to the organisation", k.Label)
	}
	return upsertOrgItemOverride(tx, k, owner.Id, accountId, owner.TenantId, userId, enabled)
}

// OrgItemAccountSwitch is one account's switch for an organisation item.
type OrgItemAccountSwitch struct {
	AccountId   string `db:"account_id" json:"account_id"`
	AccountName string `db:"account_name" json:"account_name"`
	Enabled     bool   `db:"enabled" json:"enabled"`
}

// ListOrgItemAccountSwitches returns the switches of one organisation item.
func ListOrgItemAccountSwitches(q sqlx.Queryer, k OrgItemKind, itemId string) ([]OrgItemAccountSwitch, error) {
	out := []OrgItemAccountSwitch{}
	err := sqlx.Select(q, &out, fmt.Sprintf(`
		SELECT o.account_id::text AS account_id, COALESCE(ca.account_name, '') AS account_name, o.enabled
		FROM %s o LEFT JOIN cloud_accounts ca ON ca.id = o.account_id
		WHERE o.%s = $1 ORDER BY ca.account_name`, k.OverrideTable, k.OverrideCol), itemId)
	if err != nil {
		return nil, fmt.Errorf("%s: failed to list account switches: %w", k.Label, err)
	}
	return out, nil
}

// TenantAccountIDs returns every account of tenantId.
var TenantAccountIDs = func(tenantId string) []string {
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		slog.Error("org-items: failed to get database manager", "error", err)
		return nil
	}
	var ids []string
	if err := dbms.Db.Select(&ids, `SELECT id::text FROM cloud_accounts WHERE tenant = $1::uuid`, tenantId); err != nil {
		slog.Error("org-items: failed to list tenant accounts", "error", err, "tenant_id", tenantId)
		return nil
	}
	return ids
}

// ForEachTenantAccount runs fn for the accounts in now before returning and
// for the tenant's other accounts in the background: a tenant can have
// hundreds of accounts and each invalidation is a broadcast, which must not
// hold up the request.
func ForEachTenantAccount(tenantId string, now []string, fn func(accountId string)) {
	done := map[string]bool{}
	for _, id := range now {
		if id != "" && !done[id] {
			done[id] = true
			fn(id)
		}
	}
	go func() {
		for _, id := range TenantAccountIDs(tenantId) {
			if !done[id] {
				fn(id)
			}
		}
	}()
}

// InvalidateOrgToolCaches drops the custom-tool caches of every account of
// tenantId, on every replica (each per-account invalidation is broadcast).
// The accounts in now, typically the request's, are done before it returns.
func InvalidateOrgToolCaches(tenantId string, now ...string) {
	ForEachTenantAccount(tenantId, now, updateToolCache)
}

// secretEnvMarkers lists the environment-variable name fragments refused on an
// organisation container tool: its settings are visible to every tenant admin.
var secretEnvMarkers = []string{"PASSWORD", "PASSWD", "SECRET", "TOKEN", "API_KEY", "APIKEY", "PRIVATE_KEY", "ACCESS_KEY", "CREDENTIAL"}

// CheckOrgToolSecrets refuses an organisation container tool whose config
// carries environment variables that look like secrets.
func CheckOrgToolSecrets(config map[string]any) error {
	// The container tool reads env_vars as a name → value map only.
	envVars, _ := config["env_vars"].(map[string]any)
	for name := range envVars {
		upper := strings.ToUpper(name)
		for _, marker := range secretEnvMarkers {
			if strings.Contains(upper, marker) {
				return fmt.Errorf("tool: environment variable %s looks like a secret; an organisation tool's settings are visible to every tenant admin, so remove it first", name)
			}
		}
	}
	return nil
}

// CheckOrgToolName refuses an organisation tool name that would shadow a
// built-in tool or a tool of one of the tenant's MCP integrations.
func CheckOrgToolName(q sqlx.Queryer, tenantId, name string) error {
	lower := strings.ToLower(strings.TrimSpace(name))
	if isSystemToolName(lower) {
		return fmt.Errorf("tool: %s is a built-in tool name; choose another name", name)
	}
	if integration := tenantMCPIntegrationFor(q, tenantId, lower); integration != "" {
		return fmt.Errorf("tool: %s collides with the tools of MCP integration %s; choose another name", name, integration)
	}
	return nil
}

// IsTenantMCPToolName reports whether name carries the tool prefix of one of
// tenantId's MCP integrations (MCP tools are named <integration>_<tool>).
func IsTenantMCPToolName(q sqlx.Queryer, tenantId, name string) bool {
	return tenantMCPIntegrationFor(q, tenantId, strings.ToLower(strings.TrimSpace(name))) != ""
}

// tenantMCPIntegrationFor returns the MCP integration of tenantId whose tool
// prefix starts lowerName, or "".
func tenantMCPIntegrationFor(q sqlx.Queryer, tenantId, lowerName string) string {
	var mcpNames []string
	if err := sqlx.Select(q, &mcpNames, `SELECT name FROM integrations WHERE tenant_id = $1::uuid AND type = 'mcp'`, tenantId); err != nil {
		slog.Warn("tool: MCP integration lookup failed", "error", err, "tenant_id", tenantId)
		return ""
	}
	for _, n := range mcpNames {
		if strings.HasPrefix(lowerName, sanitizeToolPrefix(n)+"_") {
			return n
		}
	}
	return ""
}

func uniqueIDs(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// IsSystemToolName reports whether name is a built-in tool or one of its aliases.
func IsSystemToolName(name string) bool {
	return isSystemToolName(strings.TrimSpace(name))
}

// TenantItemVisibilities returns the visibilities of tenantId's custom items of
// kind k named name (case-insensitive): none, one organisation item, or account
// items, which may share a name across accounts.
func TenantItemVisibilities(q sqlx.Queryer, k OrgItemKind, tenantId, name string) ([]string, error) {
	var visibilities []string
	err := sqlx.Select(q, &visibilities, fmt.Sprintf(
		`SELECT visibility FROM %s WHERE type = 'custom' AND tenant_id = $1::uuid AND lower(name) = lower($2)`, k.Table), tenantId, name)
	if err != nil {
		return nil, fmt.Errorf("%s: failed to look up %s: %w", k.Label, name, err)
	}
	return visibilities, nil
}

// CheckNotUsedByOrgAgents refuses the change when organisation agents use name:
// an item they use may not be narrowed, renamed or deleted from under them.
func CheckNotUsedByOrgAgents(q sqlx.Queryer, k OrgItemKind, tenantId, name string) error {
	var agents []string
	err := sqlx.Select(q, &agents, `
		SELECT a.name FROM llm_agents a
		WHERE a.type = 'custom' AND a.visibility <> 'account' AND a.tenant_id = $1::uuid
		  AND EXISTS (SELECT 1 FROM json_array_elements_text(COALESCE(a.tools, '[]'::json)) t WHERE lower(t) = lower($2))
		ORDER BY a.name`, tenantId, name)
	if err != nil {
		return fmt.Errorf("failed to find organisation agents using %s: %w", name, err)
	}
	if len(agents) > 0 {
		return fmt.Errorf("%s %s is used by organisation agents %s; remove it from them first", k.Label, name, strings.Join(agents, ", "))
	}
	return nil
}
