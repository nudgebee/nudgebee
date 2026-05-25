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
	roles           []string
	scopedEntityIds map[string][]string
	k8sUser         map[string]string
	k8sGroup        map[string][]string
	k8sNamespaces   map[string][]string
	// isServerInternal marks contexts constructed by NewSecurityContextForSuperAdmin
	// for synthetic server-side calls (e.g. NextAuth callbacks). It can only be
	// set inside this package — never derived from a user's role string — so a
	// user assigned a stray role name can't impersonate the synthetic admin.
	isServerInternal bool
}

type scPub struct {
	TenantId         string
	AccountIds       []string
	UserId           string
	Roles            []string
	ScopedEntityIds  map[string][]string
	K8sUser          map[string]string
	K8sGroup         map[string][]string
	K8sNamespaces    map[string][]string
	IsServerInternal bool
}

func (sc *SecurityContext) MarshalJSON() ([]byte, error) {
	data := scPub{
		TenantId:         sc.tenantId,
		AccountIds:       sc.accountIds,
		UserId:           sc.userId,
		Roles:            sc.roles,
		ScopedEntityIds:  sc.scopedEntityIds,
		K8sUser:          sc.k8sUser,
		K8sGroup:         sc.k8sGroup,
		K8sNamespaces:    sc.k8sNamespaces,
		IsServerInternal: sc.isServerInternal,
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

	if slices.Contains(sc.roles, AUTH_ACCOUNT_ADMIN_ROLE) {
		return sc.scopedEntityIds[AUTH_ACCOUNT_ADMIN_ROLE]
	}

	if slices.Contains(sc.roles, AUTH_ACCOUNT_READ_ADMIN_ROLE) {
		return sc.scopedEntityIds[AUTH_ACCOUNT_READ_ADMIN_ROLE]
	}

	if slices.Contains(sc.roles, AUTH_K8S_NAMESPACE_ADMIN_ROLE) {
		return sc.scopedEntityIds[AUTH_K8S_NAMESPACE_ADMIN_ROLE]
	}

	if slices.Contains(sc.roles, AUTH_K8S_NAMESPACE_READ_ADMIN_ROLE) {
		return sc.scopedEntityIds[AUTH_K8S_NAMESPACE_READ_ADMIN_ROLE]
	}

	return []string{}
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
	if resourceType == K8sObjectNamespaces && slices.Contains(sc.roles, AUTH_K8S_NAMESPACE_ADMIN_ROLE) || slices.Contains(sc.roles, AUTH_K8S_NAMESPACE_READ_ADMIN_ROLE) {
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

// NewSecurityContextForTenantAdmin returns a tenant-scoped admin context
// (roles: tenant_admin + account_admin). Despite its previous name
// (`NewSecurityContextForSuperAdminAndTenant`), this is NOT a super-admin
// context — IsSuperAdmin() returns false for the returned context.
// Used by:
//   - Hasura action wire-protocol bridge (actions.go) when role="admin" + tenantId set
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

	roles = lo.Uniq(roles)
	for role, ids := range scopedEntityIds {
		scopedEntityIds[role] = lo.Uniq(ids)
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
	}

	sc := SecurityContext{tenantId: tenantId, userId: userId, roles: roles, accountIds: accountIds, scopedEntityIds: scopedEntityIds, k8sUser: k8sUsers, k8sGroup: k8sGroups, k8sNamespaces: k8sNamespaces}
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
	return accountIdStr, nil
}
