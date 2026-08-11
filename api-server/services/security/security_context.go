package security

import (
	"errors"
	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"strings"
	"time"

	"log/slog"

	"slices"

	"github.com/samber/lo"
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
	SecurityAccessTypeUsage  SecurityAccessType = "usage"
)

// Bumped from "security_context" to "security_context_v2" when scope storage
// moved from per-role slice fields to a generic map keyed by role. The old
// namespace's entries become orphans and expire via TTL.
const securityContextCacheNamespace = "security_context_v2"

func init() {
	common.CacheCreateNamespace(securityContextCacheNamespace)
}

type SecurityContext struct {
	tenantId        string
	accountIds      []string
	userId          string
	displayName     string
	roles           []string
	scopedEntityIds map[string][]string
	k8sUser         map[string]string
	k8sGroup        map[string][]string
	k8sNamespaces   map[string][]string
	// customPermissions is the set of dynamic-RBAC custom-role grants the user
	// holds, keyed "<module>:<class>" (e.g. "notifications:Write"). Resolved from
	// custom_role_assignments → custom_role_permissions (direct + via groups).
	// Additive to the built-in roles; never widens data scope (operation-surface
	// only). See HasPermission / CanManage.
	customPermissions map[string]bool
	// scopedCustomPermissions holds custom-role grants scoped to a specific
	// account, keyed accountId -> ("<module>:<class>" -> true). Populated from
	// custom_role_permissions rows whose entity_type='account' (V798 scope-on-
	// grant). A permission for account X is granted iff it is in customPermissions
	// (tenant-global) OR in scopedCustomPermissions[X]. See HasScopedPermission.
	// Additive; never widens data scope (the built-in HasAccountAccess still gates
	// which accounts exist).
	scopedCustomPermissions map[string]map[string]bool
	// isServerInternal marks contexts constructed by NewSecurityContextForSuperAdmin
	// for synthetic server-side calls (e.g. NextAuth callbacks). It can only be
	// set inside this package — never derived from a user's role string — so a
	// user assigned a stray role name can't impersonate the synthetic admin.
	isServerInternal bool
}

type scPub struct {
	TenantId                string
	AccountIds              []string
	UserId                  string
	DisplayName             string
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
		DisplayName:             sc.displayName,
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
	sc.displayName = scPub1.DisplayName
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

// GetAccountIds returns every account id in the caller's tenant (the tenant
// membership set, populated for every user regardless of role). Distinct from
// ListAccountIds, which returns only the accounts a built-in account/namespace
// role is scoped to (empty for a pure custom-role user). Handlers that read
// tenant-wide under a custom-role grant use this as the account set — mirroring
// the PermissionModule tenant-wide skip in the query engine.
func (sc *SecurityContext) GetAccountIds() []string {
	return sc.accountIds
}

// User display name (best-effort; "" for synthetic/stale contexts). Callers tolerate "".
func (sc *SecurityContext) GetDisplayName() string {
	return sc.displayName
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
// read-only super admin. Distinct from IsSuperAdmin — destructive gates
// (tenant_delete, etc.) must NOT accept readonly. Read-only paths
// (HasTenantAccess(Read), HasAccountAccess(Read), audit/export views)
// should accept both flavors.
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
// app/src/lib/permissionCatalog.ts — both sides resolve the same key.
// This is purely additive: it never widens data scope (HasAccountAccess /
// ListAccountIds are unchanged) — it only answers "may this operation run".
func (sc *SecurityContext) HasPermission(module string, class string) bool {
	if sc.customPermissions == nil {
		return false
	}
	return sc.customPermissions[module+":"+class]
}

// HasScopedPermission answers "does the holder have a custom-role grant for
// (module, class) that applies to accountId" — true if a tenant-global grant
// (HasPermission) exists OR an account-scoped grant for accountId exists.
// This is the check account-scoped resource handlers use additively alongside
// HasAccountAccess (the built-in path). It grants the OPERATION only; the
// caller is still responsible for any data-scope requirement (custom roles
// never widen which accounts a user can see).
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

// GetCustomPermissions returns the holder's TENANT-GLOBAL custom-role grant keys
// ("<module>:<class>"). Used to bake the set into the session at login so the
// gateway gate can check it without a per-request DB hit.
func (sc *SecurityContext) GetCustomPermissions() []string {
	keys := make([]string, 0, len(sc.customPermissions))
	for k := range sc.customPermissions {
		keys = append(keys, k)
	}
	return keys
}

// ScopedPermissionPrefix marks an account-scoped grant key baked into the
// session (e.g. "scoped:events:Read"). The gateway honors a scoped key ONLY for
// /rpc/query routes (the query engine enforces the specific account downstream);
// the account dimension is intentionally dropped from the session key. Keep in
// lockstep with the constant of the same value in app/src/lib/rpcGateway.ts.
const ScopedPermissionPrefix = "scoped:"

// GetScopedPermissionKeys returns the DEDUPED union of account-scoped custom
// grant keys ("<module>:<class>") the holder carries across every scoped account.
// Baked into the session (prefixed with ScopedPermissionPrefix) so the gateway
// can admit a scoped grant for /rpc/query reads without knowing the specific
// account — the query engine restricts the read to the granted accounts
// (ScopedAccountIdsForModule). Additive; never widens data scope.
func (sc *SecurityContext) GetScopedPermissionKeys() []string {
	seen := map[string]bool{}
	keys := []string{}
	for _, perms := range sc.scopedCustomPermissions {
		for k := range perms {
			if !seen[k] {
				seen[k] = true
				keys = append(keys, k)
			}
		}
	}
	return keys
}

// CanManage gates a non-privilege tenant-config operation: a full tenant admin,
// a full super admin, or a matching dynamic-RBAC custom-role grant. NOTE: do NOT
// use this for privilege-administration handlers (role / group / user-role
// assignment) — those must stay IsTenantAdmin()-only so a custom role can never
// be used to escalate its own privileges.
//
// IsSuperAdmin is part of the gate because a super admin is a strict superset of
// a tenant admin everywhere else in this file, and the per-key privilege
// denylists these same handlers apply already read
// `!IsTenantAdmin() && !IsSuperAdmin()` (see tenant/privileged_config.go) — i.e.
// they assume a super admin has already cleared the coarse gate. Without it a
// super admin who is not separately a tenant admin cleared the gateway
// (rpcGateway elevates the session into allowed_roles) and was then rejected here
// by every CanManage call site.
//
// The READ-ONLY flavors are deliberately excluded: IsTenantAdmin() is false for
// tenant_admin_readonly and IsSuperAdminReadonly() is not consulted, so neither
// can reach a write through this gate.
func (sc *SecurityContext) CanManage(module string, class string) bool {
	return sc.IsTenantAdmin() || sc.IsSuperAdmin() || sc.HasPermission(module, class)
}

// CanReadAccountData reports whether the caller may READ data for accountId in
// `module` — via a built-in account role (HasAccountAccess read) OR a
// dynamic-RBAC custom grant for the module (Read, or Write which implies Read).
// Account-scoped via HasScopedPermission; never widens which accounts exist.
// Use in account-scoped read handlers so a pure custom-role user (whose built-in
// account scope is empty, so HasAccountAccess/ListAccountIds return nothing) can
// still read the data their custom grant authorizes — mirroring the query
// engine's PermissionModule skip.
func (sc *SecurityContext) CanReadAccountData(accountId string, module string) bool {
	return sc.HasAccountAccess(accountId, SecurityAccessTypeRead) ||
		sc.HasScopedPermission(accountId, module, "Read") ||
		sc.HasScopedPermission(accountId, module, "Write")
}

// ScopedAccountIdsForModule returns the account ids for which the holder has an
// account-scoped custom-role grant (Read or Write, since Write implies Read) for
// `module`. Empty when the user holds no scoped grant for the module. The query
// engine uses this to restrict a scoped-grant read to exactly the granted
// accounts — the account-scoped analog of ListAccountIds for built-in roles.
// Additive: never widens which accounts exist (the returned ids are only ever
// intersected into an account filter). Order is unspecified.
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
	if sc.IsSuperAdmin() {
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
	// of every scoped role's accounts, not just the first one that matches —
	// otherwise the lower-priority roles' accounts become invisible.
	// Write-vs-read enforcement stays in HasAccountAccess, which checks the
	// specific scoped role per account.
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

func (sc *SecurityContext) HasK8sAccess(accountId string, resourceType string, resourceName string, permission K8sRbacPermissionType) (bool, error) {
	if resourceType == K8sObjectNamespaces && len(sc.k8sNamespaces[accountId]) > 0 {
		if slices.Contains(sc.roles, AUTH_K8S_NAMESPACE_ADMIN_ROLE) {
			return true, nil
		} else if slices.Contains(sc.roles, AUTH_K8S_NAMESPACE_READ_ADMIN_ROLE) {
			if permission == K8sRbacPermissionTypeGet || permission == K8sRbacPermissionTypeList {
				return true, nil
			}
			return false, nil
		}
	}
	user, groups := sc.GetK8sUserAndGroup(accountId)
	if user == "" && len(groups) == 0 {
		return false, errors.New("K8s user/group not found")
	}
	return k8sVarifyPermission(sc, accountId, K8sRbacSubjectTypeUser, user, resourceType, resourceName, permission)
}

func (sc *SecurityContext) ListK8sPermissions(accountId string, resourceType string, resourceNames []string) (map[string][]K8sRbacPermissionType, error) {
	user, groups := sc.GetK8sUserAndGroup(accountId)
	if user == "" && len(groups) == 0 {
		return nil, errors.New("K8s user/group not found")
	}
	return k8sGetPermissions(sc, accountId, K8sRbacSubjectTypeUser, user, resourceType, resourceNames)
}

func (sc *SecurityContext) ListK8sObjectNames(accountId string, resourceType string, permission K8sRbacPermissionType) ([]string, error) {
	if resourceType == K8sObjectNamespaces && (slices.Contains(sc.roles, AUTH_K8S_NAMESPACE_ADMIN_ROLE) || slices.Contains(sc.roles, AUTH_K8S_NAMESPACE_READ_ADMIN_ROLE)) {
		return sc.k8sNamespaces[accountId], nil
	} else {
		user, groups := sc.GetK8sUserAndGroup(accountId)
		if user == "" && len(groups) == 0 {
			return nil, errors.New("K8s user/group not found")
		}
		return k8sListResourceNames(sc, accountId, K8sRbacSubjectTypeUser, user, resourceType, permission)
	}
}

func (sc *SecurityContext) InvalidateCache() error {
	err := common.CacheDelete(securityContextCacheNamespace, sc.tenantId+":"+sc.userId)
	if err != nil {
		slog.Error("Failed to invalidate cache", "error", err)
	}
	return err
}

func InvalidateCacheForTenant(tenantId string) error {
	err := common.CacheDeleteWithTag(securityContextCacheNamespace, "tenant:"+tenantId)
	if err != nil {
		slog.Error("Failed to invalidate cache", "error", err)
	}
	return err
}

func InvalidateCacheForUser(userId string) error {
	err := common.CacheDeleteWithTag(securityContextCacheNamespace, "user:"+userId)
	if err != nil {
		slog.Error("Failed to invalidate cache", "error", err)
	}
	return err
}

func IsValidTenantRole(role string) bool {
	if role == AUTH_TENANT_ADMIN_ROLE || role == AUTH_TENANT_READ_ADMIN_ROLE {
		return true
	}

	return false
}

// NewSecurityContextForSuperAdmin returns the synthetic admin context used by
// server-side callers (NextAuth callbacks, internal RPC bypass). The
// `isServerInternal` typed flag is the only way to obtain super-admin
// privileges from a non-licensed-role path, so a user assigned a stray
// role name in user_role can't impersonate this context.
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

// NewSecurityContextForAuditRelay returns a minimal, NON-super-admin context for
// the /v1/audit ingest endpoint. That endpoint is a service-to-service relay
// (X-ACTION-TOKEN authenticated) whose payload carries each audit's own actor;
// CreateAudit persists the row from the payload's user_id/tenant_id and uses this
// context only for the super-admin skip and trace/logging. We deliberately build a
// non-super-admin, no-role context (and do NO DB lookup — this is a hot path) so
// relayed operational audits are recorded. The skip still suppresses genuine
// super-admin contexts everywhere else. tenantId/userId are carried for logging.
func NewSecurityContextForAuditRelay(tenantId string, userId string) *SecurityContext {
	return &SecurityContext{
		tenantId:        tenantId,
		userId:          userId,
		roles:           []string{},
		accountIds:      []string{},
		scopedEntityIds: map[string][]string{},
		// isServerInternal stays false → IsSuperAdmin() == false → not skipped.
	}
}

// NewSecurityContextForTenantAdmin returns a tenant-scoped admin context
// (roles: tenant_admin + account_admin). Despite its previous name
// (`NewSecurityContextForSuperAdminAndTenant`), this is NOT a super-admin
// context — IsSuperAdmin() returns false for the returned context.
// Used by:
//   - RPC action wire-protocol bridge (actions.go) when role="admin" + tenantId set
//   - ETL / background flows that operate on behalf of a tenant
//
// Returns nil for an empty tenant. The resulting context would otherwise
// have tenantId="" + tenant_admin role — downstream calls that read
// `ctx.GetTenantId()` and splice it into SQL filters or HTTP headers would
// then operate against the empty-string tenant scope, which is meaningless
// and a latent footgun. Fail fast at the boundary instead.
func NewSecurityContextForTenantAdmin(tenant string) *SecurityContext {
	if tenant == "" {
		slog.Error("NewSecurityContextForTenantAdmin called with empty tenant")
		return nil
	}
	accountIds, err := GetAccountIdsByTenantId(tenant)
	if err != nil {
		slog.Error("Failed to get account ids by tenant id", "error", err)
		return nil
	}
	return &SecurityContext{tenantId: tenant, userId: "", roles: []string{AUTH_ACCOUNT_ADMIN_ROLE, AUTH_TENANT_ADMIN_ROLE}, accountIds: accountIds, scopedEntityIds: map[string][]string{}}
}

// NewSecurityContextForTenantAdminWithUserId scopes the tenant-admin context
// to a specific user. Same caveat as NewSecurityContextForTenantAdmin —
// NOT a super-admin context. Also fails fast on empty tenant; see above.
func NewSecurityContextForTenantAdminWithUserId(tenant string, userId string) *SecurityContext {
	if tenant == "" {
		slog.Error("NewSecurityContextForTenantAdminWithUserId called with empty tenant", "userId", userId)
		return nil
	}
	accountIds, err := GetAccountIdsByTenantId(tenant)
	if err != nil {
		slog.Error("Failed to get account ids by tenant id", "error", err)
		return nil
	}
	return &SecurityContext{tenantId: tenant, userId: userId, roles: []string{AUTH_ACCOUNT_ADMIN_ROLE, AUTH_TENANT_ADMIN_ROLE}, accountIds: accountIds, scopedEntityIds: map[string][]string{}}
}

const (
	RBAC_ENTITY_TYPE_TENANT        = "tenant"
	RBAC_ENTITY_TYPE_ACCOUNT       = "account"
	RBAC_ENTITY_TYPE_K8S_NAMESPACE = "k8s_namespace"
	RBAC_ENTITY_TYPE_K8S_USER      = "k8s_user"
	RBAC_ENTITY_TYPE_K8S_GROUP     = "k8s_group"
)

func NewSecurityContext(tenantId string, userId string) (*SecurityContext, error) {
	t0 := time.Now()
	defer func() {
		slog.Info("NewSecurityContext Build Time", "time", time.Since(t0).String())
	}()

	if data, ok := common.CacheGet(securityContextCacheNamespace, tenantId+":"+userId); ok {
		sc := &SecurityContext{}
		err := common.UnmarshalJson(data, &sc)
		if err != nil {
			slog.Error("Failed to unmarshal security context", "error", err)
		} else {
			return sc, nil
		}
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, err
	}
	accountIds, err := GetAccountIdsByTenantId(tenantId)
	if err != nil {
		return nil, err
	}

	roles := []string{}
	scopedEntityIds := map[string][]string{}
	k8sNamespaces := map[string][]string{}

	// accumulate role + entity_id into the generic scope map. K8s namespace
	// grants encode "<accountId>:<namespace>" in entity_id; only the accountId
	// portion is the scope key (matches the per-account access checks),
	// while the namespace half feeds the separate namespaces map.
	accumulate := func(role, entityType, entityId string) {
		roles = append(roles, role)
		switch entityType {
		case RBAC_ENTITY_TYPE_ACCOUNT:
			scopedEntityIds[role] = append(scopedEntityIds[role], entityId)
		case RBAC_ENTITY_TYPE_K8S_NAMESPACE:
			parts := strings.Split(entityId, ":")
			scopedEntityIds[role] = append(scopedEntityIds[role], parts[0])
			if len(parts) > 1 {
				k8sNamespaces[parts[0]] = append(k8sNamespaces[parts[0]], parts[1])
			}
		}
	}

	// Get Roles for the User
	if userId == "" {
		return nil, errors.New("userId is empty")
	}
	rows2, err := dbms.Db.Queryx("SELECT user_id::varchar, entity_type, entity_id::varchar, role FROM user_roles WHERE tenant_id = $1 and user_id = $2", tenantId, userId)
	if err != nil {
		return nil, err
	}
	defer func() {
		err := rows2.Close()
		if err != nil {
			slog.Error("Error closing rows", "error", err)
		}
	}()

	for rows2.Next() {
		var user_id, entity_type, entity_id, role string
		err = rows2.Scan(&user_id, &entity_type, &entity_id, &role)
		if err != nil {
			return nil, err
		}
		accumulate(role, entity_type, entity_id)
	}
	// A mid-stream read error ends the loop exactly like end-of-rows, so without
	// this check a truncated read would build — and cache for 30 minutes — an
	// under-privileged context. Same reason for every rows.Err() below.
	if err = rows2.Err(); err != nil {
		return nil, err
	}

	// Groups roles for the user
	rows3, err := dbms.Db.Queryx(`select group_id, role, entity_id, entity_type
	from group_roles gr
	where gr.group_id in (
		select distinct ug.id
		from user_groups ug
		join usergroup_users ugg on ug.id = ugg.group
		where ug.tenant = $1 and ugg.user = $2
	)`, tenantId, userId)
	if err != nil {
		return nil, err
	}
	defer func() {
		err := rows3.Close()
		if err != nil {
			slog.Error("Error closing rows", "error", err)
		}
	}()

	for rows3.Next() {
		var group_id, role, entity_id, entity_type string
		err = rows3.Scan(&group_id, &role, &entity_id, &entity_type)
		if err != nil {
			return nil, err
		}
		accumulate(role, entity_type, entity_id)
	}
	if err = rows3.Err(); err != nil {
		return nil, err
	}

	roles = lo.Uniq(roles)
	for role, ids := range scopedEntityIds {
		scopedEntityIds[role] = lo.Uniq(ids)
	}

	// Dynamic-RBAC custom-role grants — resolved only when the tenant has the
	// CUSTOM_ROLES feature enabled. With the feature off both maps stay empty,
	// which makes every custom-grant gate in the product inert and leaves
	// authorization identical to the built-in roles resolved above; the
	// custom_role_* tables are not even touched, so a database that has not run
	// the custom-role migrations still builds a context. See CustomRolesEnabled.
	customPermissions := map[string]bool{}
	scopedCustomPermissions := map[string]map[string]bool{}
	if CustomRolesEnabled(tenantId) {
		customPermissions, scopedCustomPermissions, err = resolveCustomGrants(dbms, tenantId, userId)
		if err != nil {
			return nil, err
		}
	}

	k8sUsers := map[string]string{}
	k8sGroups := map[string][]string{}
	rows4, err := dbms.Db.Queryx(`select name, value from user_attrs where "user" = $1 and (name like $2 or name like $3)`, userId, "k8s_user:"+tenantId+":%", "k8s_group:"+tenantId+":%")
	if err != nil {
		return nil, err
	}
	defer func() {
		err := rows4.Close()
		if err != nil {
			slog.Error("Error closing rows", "error", err)
		}
	}()

	for rows4.Next() {
		var name, value string
		err = rows4.Scan(&name, &value)
		if err != nil {
			return nil, err
		}
		components := strings.Split(name, ":")
		if len(components) == 3 {
			switch components[0] {
			case RBAC_ENTITY_TYPE_K8S_USER:
				k8sUsers[components[2]] = value
			case RBAC_ENTITY_TYPE_K8S_GROUP:
				k8sGroups[components[2]] = strings.Split(value, ",")
			}
		}
	}
	if err = rows4.Err(); err != nil {
		return nil, err
	}

	// get account level default user/group and merge that user groups
	if len(accountIds) > 0 {
		accountIdsAny := make([]any, len(accountIds))
		for i, accountId := range accountIds {
			accountIdsAny[i] = accountId
		}
		rows5, err := dbms.Query(`select name, value, cloud_account_id::varchar from cloud_account_attrs caa where caa.cloud_account_id in (?) and (caa.name = 'k8s_user:default' or caa.name = 'k8s_group:default')`, accountIdsAny)
		if err != nil {
			return nil, err
		}
		defer func() {
			err := rows5.Close()
			if err != nil {
				slog.Error("Error closing rows", "error", err)
			}
		}()

		for rows5.Next() {
			var name, value, cloudAccountId string
			err = rows5.Scan(&name, &value, &cloudAccountId)
			if err != nil {
				return nil, err
			}
			if name == "k8s_user:default" && value != "" && k8sUsers[cloudAccountId] == "" {
				k8sUsers[cloudAccountId] = value
			} else if name == "k8s_group:default" && value != "" && len(k8sGroups[cloudAccountId]) == 0 {
				k8sGroups[cloudAccountId] = strings.Split(value, ",")
			}
		}
		if err = rows5.Err(); err != nil {
			return nil, err
		}
	}

	// Display name for personalisation → best-effort, never fails auth; cached 30m with the ctx.
	var displayName string
	if derr := dbms.Db.QueryRowx(`SELECT COALESCE(display_name, '') FROM users WHERE id = $1::uuid`, userId).Scan(&displayName); derr != nil {
		slog.Debug("NewSecurityContext: no display_name for user", "user_id", userId, "error", derr)
	}

	sc := SecurityContext{tenantId: tenantId, userId: userId, displayName: displayName, roles: roles, accountIds: accountIds, scopedEntityIds: scopedEntityIds, k8sUser: k8sUsers, k8sGroup: k8sGroups, k8sNamespaces: k8sNamespaces, customPermissions: customPermissions, scopedCustomPermissions: scopedCustomPermissions}
	scdata, err := common.MarshalJson(&sc)
	if err != nil {
		slog.Error("Failed to marshal security context", "error", err)
		return nil, err
	}
	err = common.CacheSet(securityContextCacheNamespace, tenantId+":"+userId, scdata, common.CacheSetWithTags("tenant:"+tenantId, "user:"+userId), common.CacheSetWithExpiration(30*time.Minute))
	if err != nil {
		slog.Error("Failed to cache security context", "error", err)
	}

	return &sc, nil
}

func GetAccountIdsByTenantId(tenantId string) ([]string, error) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, err
	}
	// Get account ids for the user
	rows1, err := dbms.Db.Queryx("SELECT id FROM cloud_accounts WHERE tenant = $1", tenantId)
	if err != nil {
		return nil, err
	}
	defer func() {
		err := rows1.Close()
		if err != nil {
			slog.Error("Error closing rows", "error", err)
		}
	}()

	var accountIdStr []string
	for rows1.Next() {
		var accountId string
		err = rows1.Scan(&accountId)
		if err != nil {
			return nil, err
		}
		accountIdStr = append(accountIdStr, accountId)
	}
	if err = rows1.Err(); err != nil {
		return nil, err
	}
	return accountIdStr, nil
}
