package security

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"nudgebee/runbook/common"
	"nudgebee/runbook/config"
	"time"

	"slices"

	"github.com/google/uuid"
)

const (
	// AUTH_SUPER_ADMIN_FULL_ROLE is the only role string that grants super-admin
	// via the user_role table. The synthetic server-internal admin uses the
	// `isServerInternal` field on SecurityContext instead (see
	// NewSecurityContextForSuperAdmin) — there is no role-string equivalent.
	AUTH_SUPER_ADMIN_FULL_ROLE         = "super_admin"
	AUTH_SUPER_ADMIN_READONLY_ROLE     = "super_admin_readonly"
	AUTH_TENANT_ADMIN_ROLE             = "tenant_admin"
	AUTH_TENANT_READ_ADMIN_ROLE        = "tenant_admin_readonly"
	AUTH_TENANT_USAGE_ROLE             = "tenant_usage"
	AUTH_ACCOUNT_ADMIN_ROLE            = "account_admin"
	AUTH_ACCOUNT_READ_ADMIN_ROLE       = "account_admin_readonly"
	AUTH_ACCOUNT_USAGE_ROLE            = "account_usage"
	AUTH_K8S_NAMESPACE_ADMIN_ROLE      = "k8s_namespace_admin"
	AUTH_K8S_NAMESPACE_READ_ADMIN_ROLE = "k8s_namespace_admin_readonly"
)

type SecurityAccessType string

const (
	SecurityAccessTypeRead   SecurityAccessType = "read"
	SecurityAccessTypeCreate SecurityAccessType = "create"
	SecurityAccessTypeUpdate SecurityAccessType = "update"
	SecurityAccessTypeDelete SecurityAccessType = "delete"
)

type SecurityContext struct {
	tenantId        string
	accountIds      []string
	userId          string
	roles           []string
	scopedEntityIds map[string][]string
	k8sUser         map[string]string
	k8sGroup        map[string][]string
	k8sNamespaces   map[string][]string
	// customPermissions is the set of dynamic-RBAC custom-role grants the user
	// holds, keyed "<module>:<class>" (e.g. "notifications:Write"). Mirrors the
	// api-server's security_context_v2 wire shape (scPub.CustomPermissions); the
	// two structs are hand-kept in sync — dropping this field silently discards
	// every custom grant on the wire (see security_context_wire_test.go).
	// Additive: operation-surface only, never widens data scope.
	customPermissions map[string]bool
	// scopedCustomPermissions holds custom-role grants scoped to a specific
	// account (accountId -> "<module>:<class>" -> true). Mirrors api-server's
	// scPub.ScopedCustomPermissions wire field — hand-kept in sync (see wire
	// test). See HasScopedPermission. Additive; never widens data scope.
	scopedCustomPermissions map[string]map[string]bool
	// isServerInternal marks contexts constructed by NewSecurityContextForSuperAdmin
	// for synthetic server-side calls. Set only inside this package — never
	// derived from a user's role string — so a user assigned a stray role
	// name can't impersonate the synthetic admin.
	isServerInternal bool
}

type scPub struct {
	TenantId                string
	AccountIds              []string
	UserId                  string
	Roles                   []string
	ScopedEntityIds         map[string][]string
	K8sUser                 map[string]string
	K8sGroup                map[string][]string
	K8sNamespaces           map[string][]string
	CustomPermissions       map[string]bool
	ScopedCustomPermissions map[string]map[string]bool
	IsServerInternal        bool
}

func (sc *SecurityContext) MarshalJSON() ([]byte, error) {
	data := scPub{
		TenantId:                sc.tenantId,
		AccountIds:              sc.accountIds,
		UserId:                  sc.userId,
		Roles:                   sc.roles,
		ScopedEntityIds:         sc.scopedEntityIds,
		K8sUser:                 sc.k8sUser,
		K8sGroup:                sc.k8sGroup,
		K8sNamespaces:           sc.k8sNamespaces,
		CustomPermissions:       sc.customPermissions,
		ScopedCustomPermissions: sc.scopedCustomPermissions,
		IsServerInternal:        sc.isServerInternal,
	}

	j, err := common.MarshalJson(data)
	if err != nil {
		return nil, err
	}
	return j, nil
}

func (sc *SecurityContext) UnmarshalJSON(data []byte) error {
	scPub1 := scPub{}
	err := common.UnmarshalJson(data, &scPub1)
	if err != nil {
		return err
	}
	sc.tenantId = scPub1.TenantId
	sc.accountIds = scPub1.AccountIds
	sc.userId = scPub1.UserId
	sc.roles = scPub1.Roles
	sc.scopedEntityIds = scPub1.ScopedEntityIds
	sc.k8sUser = scPub1.K8sUser
	sc.k8sGroup = scPub1.K8sGroup
	sc.k8sNamespaces = scPub1.K8sNamespaces
	sc.customPermissions = scPub1.CustomPermissions
	sc.scopedCustomPermissions = scPub1.ScopedCustomPermissions
	sc.isServerInternal = scPub1.IsServerInternal

	return nil
}

func (sc *SecurityContext) GetTenantId() string {
	return sc.tenantId
}

func (sc *SecurityContext) GetUserId() string {
	return sc.userId
}

func (sc *SecurityContext) GetRoles() []string {
	return sc.roles
}

func (sc *SecurityContext) AddRole(role string) {
	if !slices.Contains(sc.roles, role) {
		sc.roles = append(sc.roles, role)
	}
}

func (sc *SecurityContext) IsSuperAdmin() bool {
	return sc.isServerInternal ||
		slices.Contains(sc.roles, AUTH_SUPER_ADMIN_FULL_ROLE)
}

// IsSuperAdminReadonly reports whether the session is a cross-tenant
// read-only super admin. Distinct from IsSuperAdmin — destructive paths
// must NOT accept readonly. Read-only paths accept both flavors via
// HasTenantAccess(Read) / HasAccountAccess(Read).
func (sc *SecurityContext) IsSuperAdminReadonly() bool {
	return slices.Contains(sc.roles, AUTH_SUPER_ADMIN_READONLY_ROLE)
}

func (sc *SecurityContext) IsTenantAdmin() bool {
	return slices.Contains(sc.roles, AUTH_TENANT_ADMIN_ROLE)
}

func (sc *SecurityContext) IsTenantReadAdmin() bool {
	return slices.Contains(sc.roles, AUTH_TENANT_READ_ADMIN_ROLE)
}

// HasPermission reports whether the user holds a dynamic-RBAC custom-role grant
// for the given (module, class), e.g. HasPermission("notifications", "Write").
// module/class MUST be the normalized values produced by
// app/src/lib/permissionCatalog.ts. Purely additive: never widens data scope
// (HasAccountAccess / ListAccountIds are unchanged) — only "may this op run".
func (sc *SecurityContext) HasPermission(module string, class string) bool {
	if sc.customPermissions == nil {
		return false
	}
	return sc.customPermissions[module+":"+class]
}

// HasScopedPermission reports whether the holder has a custom-role grant for
// (module, class) that applies to accountId — a tenant-global grant
// (HasPermission) OR an account-scoped grant for accountId. Account-scoped
// resource handlers use this additively alongside HasAccountAccess. Grants the
// OPERATION only; the caller still owns any data-scope requirement.
func (sc *SecurityContext) HasScopedPermission(accountId string, module string, class string) bool {
	// A tenant-global grant covers every account IN THE CALLER'S TENANT. The
	// membership check is load-bearing: unlike HasAccountAccess (whose first act
	// is this same check), a grant carries no account of its own, so without it any
	// account id the request supplies — including one belonging to another tenant —
	// would authorize. accountIds is every cloud_account of the context's tenant,
	// populated for every user regardless of role, so this never narrows an
	// in-tenant grant.
	if sc.HasPermission(module, class) && slices.Contains(sc.accountIds, accountId) {
		return true
	}
	if sc.scopedCustomPermissions == nil {
		return false
	}
	// Account-scoped grants are in-tenant by construction — the resolver reads them
	// from custom_role_assignments rows filtered by the context's tenant.
	perAccount, ok := sc.scopedCustomPermissions[accountId]
	if !ok {
		return false
	}
	return perAccount[module+":"+class]
}

// GetAccountIds returns every account id in the caller's tenant (the tenant
// membership set, populated for every user regardless of role). Distinct from
// ListAccountIds, which returns only the accounts a built-in account/namespace
// role is scoped to — empty for a pure custom-role user. Handlers that read
// tenant-wide under a tenant-global custom grant use this as the account set.
// Mirrors the api-server SecurityContext accessor of the same name.
func (sc *SecurityContext) GetAccountIds() []string {
	return sc.accountIds
}

// ScopedAccountIdsForModule returns the account ids for which the holder has an
// account-scoped custom-role grant (Read or Write, since Write implies Read) for
// `module`. Empty when the user holds no scoped grant for the module — the
// account-scoped analog of ListAccountIds for built-in roles. Additive: the
// returned ids are only ever intersected into an account filter, never used to
// widen one. Order is unspecified. Mirrors the api-server accessor of the same
// name.
func (sc *SecurityContext) ScopedAccountIdsForModule(module string) []string {
	if sc.scopedCustomPermissions == nil {
		return nil
	}
	readKey := module + ":Read"
	writeKey := module + ":Write"
	out := []string{}
	for accountId, perms := range sc.scopedCustomPermissions {
		if perms[readKey] || perms[writeKey] {
			out = append(out, accountId)
		}
	}
	return out
}

// GetCustomPermissions returns the holder's custom-role grant keys
// ("<module>:<class>").
func (sc *SecurityContext) GetCustomPermissions() []string {
	keys := make([]string, 0, len(sc.customPermissions))
	for k := range sc.customPermissions {
		keys = append(keys, k)
	}
	return keys
}

// CanManage gates a non-privilege tenant-config operation: tenant admin OR a
// matching custom-role grant. NOTE: do NOT use this for privilege-administration
// handlers (role / group / user-role assignment) — those must stay
// IsTenantAdmin()-only so a custom role can never escalate its own privileges.
func (sc *SecurityContext) CanManage(module string, class string) bool {
	return sc.IsTenantAdmin() || sc.HasPermission(module, class)
}

// CanReadAccountData reports whether the caller may READ data for accountId in
// `module` — via a built-in account role (HasAccountAccess read) OR a
// dynamic-RBAC custom grant for the module (Read, or Write which implies Read).
// Account-scoped via HasScopedPermission; never widens which accounts exist.
// Use in account-scoped read handlers so a pure custom-role user (whose built-in
// account scope is empty, so HasAccountAccess returns false) can still read the
// data their custom grant authorizes — mirroring the api-server helper of the
// same name.
func (sc *SecurityContext) CanReadAccountData(accountId string, module string) bool {
	return sc.HasAccountAccess(accountId, SecurityAccessTypeRead) ||
		sc.HasScopedPermission(accountId, module, "Read") ||
		sc.HasScopedPermission(accountId, module, "Write")
}

// HasScopedRole reports whether the user holds `role` scoped to `entityId`
// (an account id or a "k8s_namespace" account id, depending on the role).
func (sc *SecurityContext) HasScopedRole(role string, entityId string) bool {
	return slices.Contains(sc.roles, role) && slices.Contains(sc.scopedEntityIds[role], entityId)
}

func (sc *SecurityContext) HasAccountAccess(accountId string, access SecurityAccessType) bool {
	if sc.IsSuperAdmin() {
		return true
	}
	if sc.IsSuperAdminReadonly() {
		return access == SecurityAccessTypeRead
	}

	if !slices.Contains(sc.accountIds, accountId) {
		return false
	}

	if sc.IsTenantAdmin() {
		return true
	}
	if sc.IsTenantReadAdmin() {
		return access == SecurityAccessTypeRead
	}
	if sc.HasScopedRole(AUTH_ACCOUNT_ADMIN_ROLE, accountId) {
		return true
	}

	if sc.HasScopedRole(AUTH_ACCOUNT_READ_ADMIN_ROLE, accountId) {
		return access == SecurityAccessTypeRead
	}

	if sc.HasScopedRole(AUTH_K8S_NAMESPACE_ADMIN_ROLE, accountId) {
		return true
	}

	if sc.HasScopedRole(AUTH_K8S_NAMESPACE_READ_ADMIN_ROLE, accountId) {
		return access == SecurityAccessTypeRead
	}

	return false
}

func (sc *SecurityContext) HasTenantAccess(access SecurityAccessType) bool {
	if sc.IsSuperAdmin() {
		return true
	}
	if sc.IsSuperAdminReadonly() {
		return access == SecurityAccessTypeRead
	}
	if sc.IsTenantAdmin() {
		return true
	}
	if sc.IsTenantReadAdmin() {
		return access == SecurityAccessTypeRead
	}
	return false
}

func (sc *SecurityContext) ListAccountIds() []string {
	if sc.IsSuperAdmin() || sc.IsSuperAdminReadonly() {
		return sc.accountIds
	}
	if sc.IsTenantAdmin() {
		return sc.accountIds
	}
	if sc.IsTenantReadAdmin() {
		return sc.accountIds
	}

	// A user can hold several scoped roles at once (e.g. account_admin on
	// one account and account_admin_readonly on another). Return the union
	// of every scoped role's accounts, not just the first match — otherwise
	// the lower-priority roles' accounts become invisible. Write-vs-read
	// enforcement stays in HasAccountAccess, which checks the specific
	// scoped role per account.
	accountIds := []string{}
	seen := map[string]bool{}
	for _, role := range []string{
		AUTH_ACCOUNT_ADMIN_ROLE,
		AUTH_ACCOUNT_READ_ADMIN_ROLE,
		AUTH_K8S_NAMESPACE_ADMIN_ROLE,
		AUTH_K8S_NAMESPACE_READ_ADMIN_ROLE,
	} {
		if !slices.Contains(sc.roles, role) {
			continue
		}
		for _, accountId := range sc.scopedEntityIds[role] {
			if !seen[accountId] {
				seen[accountId] = true
				accountIds = append(accountIds, accountId)
			}
		}
	}

	return accountIds
}

func (sc *SecurityContext) GetK8sUserAndGroup(accountId string) (string, []string) {
	return sc.k8sUser[accountId], sc.k8sGroup[accountId]
}

func IsValidTenantRole(role string) bool {
	if role == AUTH_TENANT_ADMIN_ROLE || role == AUTH_TENANT_READ_ADMIN_ROLE {
		return true
	}

	return false
}

var tenantIdAccountIdCache = make(map[string]string)

func GetTenantIdFromAccountId(accountId string) (string, error) {
	if cachedTenantId, ok := tenantIdAccountIdCache[accountId]; ok {
		return cachedTenantId, nil
	}

	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return "", fmt.Errorf("GetTenantIdFromAccountId: failed to get database manager: %w", err)
	}

	query := "SELECT tenant FROM cloud_accounts WHERE id = $1"
	var tenantId string
	err = dbManager.Db.Get(&tenantId, query, accountId)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			slog.Warn("GetTenantIdFromAccountId: no tenant found for accountId", "accountId", accountId)
			return "", nil // Or return an error like common.ErrorNotFound("account not found")
		}
		slog.Error("GetTenantIdFromAccountId: failed to query tenant ID", "accountId", accountId, "error", err)
		return "", fmt.Errorf("GetTenantIdFromAccountId: db query failed: %w", err)
	}

	tenantIdAccountIdCache[accountId] = tenantId
	return tenantId, nil
}

func GetAccountIdsForTenant(tenantId string) ([]string, error) {
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, fmt.Errorf("GetAccountIdsForTenant: failed to get database manager: %w", err)
	}

	query := "SELECT id::text FROM cloud_accounts WHERE tenant = $1"
	rows, err := dbManager.Query(query, tenantId)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			slog.Warn("GetAccountIdsForTenant: no tenant found for tenantId", "tenantId", tenantId)
			return nil, nil
		}
		slog.Error("GetAccountIdsForTenant: failed to query tenant ID", "tenantId", tenantId, "error", err)
		return nil, fmt.Errorf("GetAccountIdsForTenant: db query failed: %w", err)
	}
	var accountIds []string
	var accountId string
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("security: failed to close rows", "error", err)
		}
	}()
	for rows.Next() {
		if err := rows.Scan(&accountId); err != nil {
			slog.Error("GetAccountIdsForTenant: failed to scan account ID", "tenantId", tenantId, "error", err)
			return nil, fmt.Errorf("GetAccountIdsForTenant: db scan failed: %w", err)
		}
		accountIds = append(accountIds, accountId)
		tenantIdAccountIdCache[accountId] = tenantId
	}
	return accountIds, nil
}

// NewSecurityContextForSuperAdmin returns the synthetic admin context used by
// server-side callers. The `isServerInternal` typed flag is the only way to
// obtain super-admin privileges from a non-licensed-role path, so a user
// assigned a stray role name in user_role can't impersonate this context.
func NewSecurityContextForSuperAdmin() *SecurityContext {
	return &SecurityContext{
		tenantId:         "",
		userId:           "",
		roles:            []string{},
		accountIds:       []string{},
		scopedEntityIds:  map[string][]string{},
		isServerInternal: true,
	}
}

func NewSecurityContextForTenantAdmin(tenantId string) *SecurityContext {
	accountIds, err := GetAccountIdsForTenant(tenantId)
	if err != nil {
		slog.Error("security: failed to get account IDs for tenant", "tenantId", tenantId, "error", err)
		return nil
	}
	return &SecurityContext{tenantId: tenantId, roles: []string{"tenant_admin"}, accountIds: accountIds, scopedEntityIds: map[string][]string{}}
}

func NewSecurityContextForTenantAccountAdmin(tenantId, userId string, accountIds []string) *SecurityContext {
	if len(accountIds) == 0 {
		accountIds1, err := GetAccountIdsForTenant(tenantId)
		if err != nil {
			slog.Error("security: failed to get account IDs for tenant", "tenantId", tenantId, "error", err)
			return nil
		}
		accountIds = accountIds1
	}
	return &SecurityContext{tenantId: tenantId, userId: userId, roles: []string{"tenant_admin"}, accountIds: accountIds, scopedEntityIds: map[string][]string{}}
}

func loadSecurityContextFromServicesServer(tenantId string, userId string) (*SecurityContext, error) {
	url := config.Config.ServiceEndpoint
	if url[len(url)-1] != '/' {
		url += "/"
	}
	url += "v1/authz/get_security_context"

	payload := map[string]string{
		"tenant_id": tenantId,
		"user_id":   userId,
	}

	payloadBytes, err := common.MarshalJson(payload)
	if err != nil {
		slog.Info("security: failed to marshal payload", "error", err)
		return nil, err
	}

	// Create a context with a 10-second timeout for the HTTP request
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(config.Config.ServiceApiServerTimeoutSeconds)*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(payloadBytes))
	if err != nil {
		slog.Info("security: failed to create request", "error", err)
		return nil, err
	}

	req.Header.Set("X-ACTION-TOKEN", config.Config.ServiceApiServerToken)
	req.Header.Set("Content-Type", "application/json")

	client := common.HttpClient()
	resp, err := client.Do(req)
	if err != nil {
		slog.Info("security: failed to send request", "error", err)
		return nil, err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			slog.Error("security: failed to close response body", "error", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		slog.Info("security: received non-OK response", "status_code", resp.StatusCode)
		return nil, errors.New("security: unable to get security details")
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		slog.Info("security: failed to read response body", "error", err)
		return nil, err
	}

	type SecurityContextWrapper struct {
		Context SecurityContext `json:"context"`
	}

	var res SecurityContextWrapper
	if err := common.UnmarshalJson(body, &res); err != nil {
		slog.Info("security: failed to decode response", "error", err)
		return nil, err
	}

	if res.Context.tenantId == "" {
		return nil, errors.New("security: unable to get security details")
	}

	return &res.Context, nil
}

func NewSecurityContext(tenantId string, userId string) (*SecurityContext, error) {
	sc, err := loadSecurityContextFromServicesServer(tenantId, userId)
	return sc, err
}

func GetSystemUserId() string {
	return uuid.Nil.String()
}
