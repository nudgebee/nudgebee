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
	tenantId                            string
	accountIds                          []string
	userId                              string
	roles                               []string
	accountAdminIds                     []string
	accountReadOnlyAdminIds             []string
	k8sUser                             map[string]string
	k8sGroup                            map[string][]string
	k8sNamespaceAdminAccountIds         []string
	k8sNamespaceReadOnlyAdminAccountIds []string
	k8sNamespaces                       map[string][]string
	// isServerInternal marks contexts constructed by NewSecurityContextForSuperAdmin
	// for synthetic server-side calls. Set only inside this package — never
	// derived from a user's role string — so a user assigned a stray role
	// name can't impersonate the synthetic admin.
	isServerInternal bool
}

type scPub struct {
	TenantId                            string
	AccountIds                          []string
	UserId                              string
	Roles                               []string
	AccountAdminIds                     []string
	AccountReadOnlyAdminIds             []string
	K8sUser                             map[string]string
	K8sGroup                            map[string][]string
	K8sNamespaceAdminAccountIds         []string
	K8sNamespaceReadOnlyAdminAccountIds []string
	K8sNamespaces                       map[string][]string
	IsServerInternal                    bool
}

func (sc *SecurityContext) MarshalJSON() ([]byte, error) {
	data := scPub{
		TenantId:                            sc.tenantId,
		AccountIds:                          sc.accountIds,
		UserId:                              sc.userId,
		Roles:                               sc.roles,
		AccountAdminIds:                     sc.accountAdminIds,
		AccountReadOnlyAdminIds:             sc.accountReadOnlyAdminIds,
		K8sUser:                             sc.k8sUser,
		K8sGroup:                            sc.k8sGroup,
		K8sNamespaceAdminAccountIds:         sc.k8sNamespaceAdminAccountIds,
		K8sNamespaceReadOnlyAdminAccountIds: sc.k8sNamespaceReadOnlyAdminAccountIds,
		K8sNamespaces:                       sc.k8sNamespaces,
		IsServerInternal:                    sc.isServerInternal,
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
	sc.k8sNamespaceAdminAccountIds = scPub1.K8sNamespaceAdminAccountIds
	sc.k8sNamespaceReadOnlyAdminAccountIds = scPub1.K8sNamespaceReadOnlyAdminAccountIds
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
	if slices.Contains(sc.roles, AUTH_ACCOUNT_ADMIN_ROLE) && slices.Contains(sc.accountAdminIds, accountId) {
		return true
	}

	if slices.Contains(sc.roles, AUTH_ACCOUNT_READ_ADMIN_ROLE) && slices.Contains(sc.accountReadOnlyAdminIds, accountId) {
		return access == SecurityAccessTypeRead
	}

	if slices.Contains(sc.roles, AUTH_K8S_NAMESPACE_ADMIN_ROLE) && slices.Contains(sc.k8sNamespaceAdminAccountIds, accountId) {
		return true
	}

	if slices.Contains(sc.roles, AUTH_K8S_NAMESPACE_READ_ADMIN_ROLE) && slices.Contains(sc.k8sNamespaceReadOnlyAdminAccountIds, accountId) {
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

	if slices.Contains(sc.roles, AUTH_ACCOUNT_ADMIN_ROLE) {
		return sc.accountAdminIds
	}

	if slices.Contains(sc.roles, AUTH_ACCOUNT_READ_ADMIN_ROLE) {
		return sc.accountReadOnlyAdminIds
	}

	if slices.Contains(sc.roles, AUTH_K8S_NAMESPACE_ADMIN_ROLE) {
		return sc.k8sNamespaceAdminAccountIds
	}

	if slices.Contains(sc.roles, AUTH_K8S_NAMESPACE_READ_ADMIN_ROLE) {
		return sc.k8sNamespaceReadOnlyAdminAccountIds
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
		tenantId:                "",
		userId:                  "",
		roles:                   []string{},
		accountIds:              []string{},
		accountAdminIds:         []string{},
		accountReadOnlyAdminIds: []string{},
		isServerInternal:        true,
	}
}

func NewSecurityContextForTenantAdmin(tenantId string) *SecurityContext {
	accountIds, err := GetAccountIdsForTenant(tenantId)
	if err != nil {
		slog.Error("security: failed to get account IDs for tenant", "tenantId", tenantId, "error", err)
		return nil
	}
	return &SecurityContext{tenantId: tenantId, roles: []string{"tenant_admin"}, accountIds: accountIds, accountAdminIds: []string{}, accountReadOnlyAdminIds: []string{}}
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
	return &SecurityContext{tenantId: tenantId, userId: userId, roles: []string{"tenant_admin"}, accountIds: accountIds, accountAdminIds: accountIds, accountReadOnlyAdminIds: accountIds}
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
