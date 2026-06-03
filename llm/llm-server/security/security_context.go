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
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"sync"
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

func (sc *SecurityContext) GetAccountIds() []string {
	return sc.accountIds
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

var (
	tenantIdAccountIdCache = make(map[string]string)
	tenantCacheMutex       sync.RWMutex
)

func GetTenantIdFromAccountId(accountId string) (string, error) {
	tenantCacheMutex.RLock()
	if cachedTenantId, ok := tenantIdAccountIdCache[accountId]; ok {
		tenantCacheMutex.RUnlock()
		return cachedTenantId, nil
	}
	tenantCacheMutex.RUnlock()

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

	tenantCacheMutex.Lock()
	tenantIdAccountIdCache[accountId] = tenantId
	tenantCacheMutex.Unlock()
	return tenantId, nil
}

func IsAccountInTenant(accountId string, tenantId string) bool {
	actualTenantId, err := GetTenantIdFromAccountId(accountId)
	if err != nil {
		slog.Error("IsAccountInTenant: failed to get tenant id for account", "error", err)
		return false
	}
	return actualTenantId == tenantId
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
		tenantCacheMutex.Lock()
		tenantIdAccountIdCache[accountId] = tenantId
		tenantCacheMutex.Unlock()
	}
	return accountIds, nil
}

// AccountInfo holds minimal account identification data for account resolution.
type AccountInfo struct {
	ID          string
	AccountName string
}

// GetAccountsForTenant returns account IDs and names for all active accounts in a tenant.
func GetAccountsForTenant(tenantId string) ([]AccountInfo, error) {
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, fmt.Errorf("GetAccountsForTenant: failed to get database manager: %w", err)
	}

	query := "SELECT id::text, account_name FROM cloud_accounts WHERE tenant = $1 AND status = 'active'"
	rows, err := dbManager.Query(query, tenantId)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		slog.Error("GetAccountsForTenant: failed to query accounts", "tenantId", tenantId, "error", err)
		return nil, fmt.Errorf("GetAccountsForTenant: db query failed: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("security: failed to close rows", "error", err)
		}
	}()

	var accounts []AccountInfo
	for rows.Next() {
		var id string
		var name *string
		if err := rows.Scan(&id, &name); err != nil {
			slog.Error("GetAccountsForTenant: failed to scan account", "tenantId", tenantId, "error", err)
			return nil, fmt.Errorf("GetAccountsForTenant: db scan failed: %w", err)
		}
		accountName := id
		if name != nil && *name != "" {
			accountName = *name
		}
		accounts = append(accounts, AccountInfo{ID: id, AccountName: accountName})
	}
	return accounts, nil
}

// NewSecurityContextForSuperAdmin returns the synthetic admin context used by
// server-side callers (synthetic action invocations, internal handlers). The
// `isServerInternal` typed flag is the only way to obtain super-admin
// privileges from a non-licensed-role path, so a user assigned a stray
// role name in user_role can't impersonate this context.
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

type securityContextResponse struct {
	statusCode int
	body       []byte
	err        error
}

func buildSecurityContextURL() string {
	url := config.Config.ServiceEndpoint
	if url[len(url)-1] != '/' {
		url += "/"
	}
	return url + "v1/authz/get_security_context"
}

func createSecurityContextRequest(ctx context.Context, url string, payloadBytes []byte) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(payloadBytes))
	if err != nil {
		return nil, err
	}

	req.Header.Set("X-ACTION-TOKEN", config.Config.ServiceApiServerToken)
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

func executeSecurityContextRequest(req *http.Request) securityContextResponse {
	client := common.HttpClient()
	resp, err := client.Do(req)
	if err != nil {
		return securityContextResponse{err: err}
	}

	statusCode := resp.StatusCode
	body, err := io.ReadAll(resp.Body)
	if closeErr := resp.Body.Close(); closeErr != nil {
		slog.Error("security: failed to close response body", "error", closeErr)
	}

	return securityContextResponse{
		statusCode: statusCode,
		body:       body,
		err:        err,
	}
}

func parseSecurityContextResponse(body []byte) (*SecurityContext, error) {
	type SecurityContextWrapper struct {
		Context SecurityContext `json:"context"`
	}

	var res SecurityContextWrapper
	if err := common.UnmarshalJson(body, &res); err != nil {
		return nil, err
	}

	if res.Context.tenantId == "" {
		return nil, errors.New("security: unable to get security details")
	}

	return &res.Context, nil
}

func shouldRetrySecurityContext(statusCode int) (bool, error) {
	// Don't retry on 4xx client errors
	if statusCode >= 400 && statusCode < 500 {
		return false, fmt.Errorf("security: client error %d", statusCode)
	}

	// Retry on 5xx server errors
	if statusCode >= 500 {
		return true, fmt.Errorf("security: server error %d", statusCode)
	}

	// Non-OK status that's not 4xx or 5xx
	if statusCode != http.StatusOK {
		return false, errors.New("security: unable to get security details")
	}

	return false, nil
}

func performSecurityContextAttempt(url string, payloadBytes []byte, attempt int) (*SecurityContext, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(config.Config.ServiceApiServerTimeoutSeconds)*time.Second)
	defer cancel()

	req, err := createSecurityContextRequest(ctx, url, payloadBytes)
	if err != nil {
		slog.Info("security: failed to create request", "error", err, "attempt", attempt)
		return nil, err
	}

	response := executeSecurityContextRequest(req)
	if response.err != nil {
		slog.Info("security: failed to read response body", "error", response.err, "attempt", attempt)
		return nil, response.err
	}

	shouldRetry, statusErr := shouldRetrySecurityContext(response.statusCode)
	if statusErr != nil && !shouldRetry {
		slog.Info("security: received non-retryable error", "status_code", response.statusCode)
		return nil, statusErr
	}

	if shouldRetry {
		slog.Info("security: received server error", "status_code", response.statusCode, "attempt", attempt)
		return nil, statusErr
	}

	return parseSecurityContextResponse(response.body)
}

func loadSecurityContextFromServicesServer(tenantId string, userId string) (*SecurityContext, error) {
	maxRetries := config.Config.SecurityContextRetryAttempts
	initialBackoff := time.Duration(config.Config.SecurityContextInitialBackoffSeconds) * time.Second

	url := buildSecurityContextURL()
	payload := map[string]string{
		"tenant_id": tenantId,
		"user_id":   userId,
	}

	payloadBytes, err := common.MarshalJson(payload)
	if err != nil {
		slog.Info("security: failed to marshal payload", "error", err)
		return nil, err
	}

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			backoffDuration := initialBackoff * time.Duration(1<<uint(attempt-1))
			slog.Info("security: retrying request", "attempt", attempt, "backoff", backoffDuration)
			time.Sleep(backoffDuration)
		}

		sc, err := performSecurityContextAttempt(url, payloadBytes, attempt)
		if err != nil {
			lastErr = err
			continue
		}

		return sc, nil
	}

	slog.Error("security: all retry attempts failed", "attempts", maxRetries+1, "last_error", lastErr)
	return nil, fmt.Errorf("security: failed after %d attempts: %w", maxRetries+1, lastErr)
}

func NewSecurityContext(tenantId string, userId string) (*SecurityContext, error) {
	if config.Config.LlmServerSecurityMode == "local" {
		return NewSecurityContextForTenantAccountAdmin(tenantId, userId, []string{}), nil
	}
	sc, err := loadSecurityContextFromServicesServer(tenantId, userId)
	return sc, err
}

func GetSystemUserId() string {
	return uuid.Nil.String()
}

// SetTenantIdCacheForTest sets the cache for testing purposes
func SetTenantIdCacheForTest(accountId, tenantId string) {
	tenantCacheMutex.Lock()
	defer tenantCacheMutex.Unlock()
	tenantIdAccountIdCache[accountId] = tenantId
}
