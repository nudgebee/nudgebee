package security

import (
	"nudgebee/collector/cloud/common"
	"slices"
)

const (
	AUTH_SUPER_ADMIN_ROLE        = "admin"
	AUTH_TENANT_ADMIN_ROLE       = "tenant_admin"
	AUTH_TENANT_READ_ADMIN_ROLE  = "tenant_admin_readonly"
	AUTH_TENANT_USAGE_ROLE       = "tenant_usage"
	AUTH_ACCOUNT_ADMIN_ROLE      = "account_admin"
	AUTH_ACCOUNT_READ_ADMIN_ROLE = "account_admin_readonly"
	AUTH_ACCOUNT_USAGE_ROLE      = "account_usage"
)

type SecurityAccessType string

const (
	SecurityAccessTypeRead   SecurityAccessType = "read"
	SecurityAccessTypeCreate SecurityAccessType = "create"
	SecurityAccessTypeUpdate SecurityAccessType = "update"
	SecurityAccessTypeDelete SecurityAccessType = "delete"
)

type SecurityContext struct {
	tenantId                string
	accountIds              []string
	userId                  string
	roles                   []string
	accountAdminIds         []string
	accountReadOnlyAdminIds []string
	k8sUser                 map[string]string
	k8sGroup                map[string][]string
}

type scPub struct {
	TenantId                string
	AccountIds              []string
	UserId                  string
	Roles                   []string
	AccountAdminIds         []string
	AccountReadOnlyAdminIds []string
	K8sUser                 map[string]string
	K8sGroup                map[string][]string
}

func (sc *SecurityContext) MarshalJSON() ([]byte, error) {
	data := scPub{
		TenantId:                sc.tenantId,
		AccountIds:              sc.accountIds,
		UserId:                  sc.userId,
		Roles:                   sc.roles,
		AccountAdminIds:         sc.accountAdminIds,
		AccountReadOnlyAdminIds: sc.accountReadOnlyAdminIds,
		K8sUser:                 sc.k8sUser,
		K8sGroup:                sc.k8sGroup,
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
	sc.accountAdminIds = scPub1.AccountAdminIds
	sc.accountReadOnlyAdminIds = scPub1.AccountReadOnlyAdminIds
	sc.k8sUser = scPub1.K8sUser
	sc.k8sGroup = scPub1.K8sGroup

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

func (sc *SecurityContext) IsSuperAdmin() bool {
	return slices.Contains(sc.roles, AUTH_SUPER_ADMIN_ROLE)
}

func (sc *SecurityContext) IsTenantAdmin() bool {
	return slices.Contains(sc.roles, AUTH_TENANT_ADMIN_ROLE)
}

func (sc *SecurityContext) IsTenantReadAdmin() bool {
	return slices.Contains(sc.roles, AUTH_TENANT_READ_ADMIN_ROLE)
}

func (sc *SecurityContext) HasAccountAccess(accountId string, access SecurityAccessType) bool {
	if sc.IsSuperAdmin() {
		return true
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
	if slices.Contains(sc.roles, AUTH_ACCOUNT_ADMIN_ROLE) && slices.Contains(sc.accountAdminIds, accountId) {
		return true
	}

	if slices.Contains(sc.roles, AUTH_ACCOUNT_READ_ADMIN_ROLE) && slices.Contains(sc.accountReadOnlyAdminIds, accountId) {
		return access == SecurityAccessTypeRead
	}

	return false
}

func (sc *SecurityContext) HasTenantAccess(access SecurityAccessType) bool {
	if sc.IsSuperAdmin() {
		return true
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
		return sc.accountAdminIds
	}

	if slices.Contains(sc.roles, AUTH_ACCOUNT_READ_ADMIN_ROLE) {
		return sc.accountReadOnlyAdminIds
	}

	return []string{}
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

func NewSecurityContextForSuperAdmin() *SecurityContext {
	return &SecurityContext{tenantId: "", userId: "", roles: []string{"admin"}, accountIds: []string{}, accountAdminIds: []string{}, accountReadOnlyAdminIds: []string{}}
}

func NewSecurityContextForSuperAdminWithTenant(tenantId string) *SecurityContext {
	return &SecurityContext{tenantId: tenantId, userId: "", roles: []string{"admin"}, accountIds: []string{}, accountAdminIds: []string{}, accountReadOnlyAdminIds: []string{}}
}

func NewSecurityContext(tenantId string, userId string) (*SecurityContext, error) {
	sc := SecurityContext{tenantId: tenantId, userId: userId, roles: []string{"tenant_admin"}, accountIds: []string{}, accountAdminIds: []string{}, accountReadOnlyAdminIds: []string{}, k8sUser: map[string]string{}, k8sGroup: map[string][]string{}}
	return &sc, nil
}
