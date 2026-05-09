package account

import (
	"database/sql"
	"fmt"
	"nudgebee/collector/cloud/common"
	"nudgebee/collector/cloud/providers"
	"nudgebee/collector/cloud/security"
	"sync"
	"time"
)

// tenantLookupCache caches the (account_id -> tenant_id) mapping. The mapping
// is stable for the lifetime of a cloud_accounts row (tenant is set at create
// time and never changes), so a 5-minute TTL is conservative; we bound staleness
// without paying a PG round-trip per EventBridge message.
const tenantLookupTTL = 5 * time.Minute

type tenantLookupEntry struct {
	tenantId string
	expiry   time.Time
}

var (
	tenantLookupCache   = make(map[string]tenantLookupEntry)
	tenantLookupCacheMu sync.RWMutex
)

type AsyncEventHandler struct {
}

func NewAsyncEventHandler() *AsyncEventHandler {
	return &AsyncEventHandler{}
}

// ProcessEvent implements providers.ProcessedEventHandler
func (h *AsyncEventHandler) ProcessEvent(pCtx providers.CloudProviderContext, event providers.Event, originatingAccount providers.Account) error {
	ctxLogger := pCtx.GetLogger().With("originatingAccount", originatingAccount.AccountNumber, "eventId", event.EventId)

	// Use the already-resolved originatingAccount.ID when available.
	// The caller (EventBridge/EventGrid/PubSub handler) resolves the account using tenant-safe
	// lookups (external_id + account_number). Re-resolving by account_number alone is ambiguous
	// when multiple tenants share the same cloud account number.
	var internalDBAccountID, internalTenantId string
	if originatingAccount.ID != "" {
		internalDBAccountID = originatingAccount.ID
		tenant, err := getTenantForAccountId(originatingAccount.ID)
		if err != nil {
			ctxLogger.Error("eventhandler: failed to get tenant for account", "error", err, "accountId", originatingAccount.ID)
			return err
		}
		internalTenantId = tenant
	} else {
		// Fallback for callers that don't set originatingAccount.ID
		var err error
		internalDBAccountID, internalTenantId, err = getInternalAccountAndTenant(originatingAccount.AccountNumber, originatingAccount.CloudProvider)
		if err != nil {
			ctxLogger.Error("eventhandler: failed to get internal DB account ID for originating account", "error", err, "originatingAccount", originatingAccount.AccountNumber)
			return err
		}
	}

	secCtx := security.NewSecurityContextForSuperAdminWithTenant(internalTenantId)
	reqCtx := security.NewRequestContext(pCtx.GetContext(), secCtx, ctxLogger, nil, nil)

	dbms, err := common.GetDatabaseManager(common.Metastore) // dbms needed for processEventsInternal
	if err != nil {
		ctxLogger.Error("eventhandler: unable to get dbms for storing event", "error", err)
		return err
	}

	// Call the centralized processing function with a single event in a slice
	err = processEventsInternal(reqCtx, dbms, []providers.Event{event}, originatingAccount, internalDBAccountID)
	if err != nil {
		// Error is already logged by processEventsInternal or its sub-functions
		return err
	}
	ctxLogger.Info("eventhandler: successfully processed event (DB store & MQ publish)", "processedEventId", event.EventId)

	return nil
}

// GetAccountFromCloudProviderAccountId implements providers.ProcessedEventHandler
// It fetches the account details (including AssumeRole, keys etc.) from the internal database
// based on the AWS account number provided (e.g., from an EventBridge event).
func (h *AsyncEventHandler) GetAccountFromCloudProviderAccountId(pCtx providers.CloudProviderContext, awsAccountNumber string) (providers.Account, error) {
	ctxLogger := pCtx.GetLogger().With("awsAccountNumber", awsAccountNumber)

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		ctxLogger.Error("GetAccountFromCloudProviderAccountId: unable to get dbms", "error", err)
		return providers.Account{}, err
	}

	// Guard against ambiguous lookups when multiple tenants share the same cloud account number
	var count int
	err = dbms.QueryRowAndScan(&count, "SELECT count(*) FROM cloud_accounts WHERE account_number = $1 AND status = 'active' AND lower(cloud_provider) IN ('azure', 'gcp', 'aws', 'cloudfoundry')", awsAccountNumber)
	if err != nil {
		ctxLogger.Error("GetAccountFromCloudProviderAccountId: unable to count accounts", "error", err)
		return providers.Account{}, err
	}
	if count > 1 {
		ctxLogger.Error("GetAccountFromCloudProviderAccountId: multiple active accounts found, cannot resolve without tenant context", "count", count, "awsAccountNumber", awsAccountNumber)
		return providers.Account{}, fmt.Errorf("multiple active accounts (%d) found for account_number %s — use nudgebeeAccountToken for tenant-safe lookup", count, awsAccountNumber)
	}

	// Query cloud_accounts table by account_number and provider
	row, err := dbms.QueryRow("SELECT id, assume_role, access_key, access_secret, region, data::varchar, cloud_provider, account_number, account_name FROM cloud_accounts WHERE account_number = $1 AND status = 'active' AND lower(cloud_provider) IN ('azure', 'gcp', 'aws', 'cloudfoundry') LIMIT 1", awsAccountNumber)
	if err != nil {
		ctxLogger.Error("GetAccountFromCloudProviderAccountId: unable to fetch account by account_number", "error", err)
		return providers.Account{}, err
	}

	var id *string
	var assumeRole, accessKey, accessSecret, region, data, cloudProvider, fetchedAccountNumber, accountName *string
	err = row.Scan(&id, &assumeRole, &accessKey, &accessSecret, &region, &data, &cloudProvider, &fetchedAccountNumber, &accountName)
	if err != nil {
		ctxLogger.Error("GetAccountFromCloudProviderAccountId: unable to scan account details", "error", err)
		return providers.Account{}, err
	}

	// Use account_number as fallback for account_name if not provided
	if accountName == nil {
		accountName = &awsAccountNumber
	}

	// Check if id is nil to prevent panic
	if id == nil {
		ctxLogger.Error("GetAccountFromCloudProviderAccountId: account id is NULL in database", "awsAccountNumber", awsAccountNumber)
		return providers.Account{}, fmt.Errorf("account id is NULL for AWS account number %s", awsAccountNumber)
	}

	// Construct the providers.Account struct
	var cloudProviderStr string
	if cloudProvider != nil {
		cloudProviderStr = *cloudProvider
	}

	accountToReturn := providers.Account{
		ID:            *id,
		AssumeRole:    assumeRole,
		AccessKey:     accessKey,
		AccessSecret:  accessSecret,
		Region:        region,
		Data:          data,
		AccountNumber: awsAccountNumber, // Use the input awsAccountNumber
		AccountName:   *accountName,
		CloudProvider: cloudProviderStr,
	}

	return accountToReturn, nil
}

// GetAccountFromExternalId implements providers.ProcessedEventHandler
// It fetches the account details using external_id (nudgebeeAccountToken) and cloud provider account number
// This provides tenant-safe account resolution for GCP/Azure events with token-based routing
func (h *AsyncEventHandler) GetAccountFromExternalId(pCtx providers.CloudProviderContext, externalId string, accountNumber string) (providers.Account, error) {
	ctxLogger := pCtx.GetLogger().With("external_id", fmt.Sprintf("%.8s...", externalId), "account_number", accountNumber)

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		ctxLogger.Error("GetAccountFromExternalId: unable to get dbms", "error", err)
		return providers.Account{}, err
	}

	// Query cloud_accounts table by external_id and account_number
	// This ensures tenant-safe lookup even when same GCP project is used by multiple Nudgebee tenants
	query := `
		SELECT id, assume_role, access_key, access_secret, region, data::varchar, cloud_provider, account_number, account_name, tenant
		FROM cloud_accounts
		WHERE external_id = $1
		  AND account_number = $2
		  AND status = 'active'
		  AND lower(cloud_provider) IN ('gcp', 'azure', 'aws')
		LIMIT 1
	`
	row, err := dbms.QueryRow(query, externalId, accountNumber)
	if err != nil {
		ctxLogger.Error("GetAccountFromExternalId: unable to fetch account by external_id", "error", err)
		return providers.Account{}, err
	}

	var id *string
	var assumeRole, accessKey, accessSecret, region, data, cloudProvider, fetchedAccountNumber, accountName, tenant *string
	err = row.Scan(&id, &assumeRole, &accessKey, &accessSecret, &region, &data, &cloudProvider, &fetchedAccountNumber, &accountName, &tenant)
	if err != nil {
		ctxLogger.Error("GetAccountFromExternalId: unable to scan account details", "error", err)
		return providers.Account{}, err
	}

	// Use account_number as fallback for account_name if not provided
	if accountName == nil {
		accountName = &accountNumber
	}

	// Check if id is nil to prevent panic
	if id == nil {
		ctxLogger.Error("GetAccountFromExternalId: account id is NULL in database", "external_id", fmt.Sprintf("%.8s...", externalId), "account_number", accountNumber)
		return providers.Account{}, fmt.Errorf("account id is NULL for external_id %s and account_number %s", fmt.Sprintf("%.8s...", externalId), accountNumber)
	}

	// Construct the providers.Account struct
	var cloudProviderStr string
	if cloudProvider != nil {
		cloudProviderStr = *cloudProvider
	}

	accountToReturn := providers.Account{
		ID:            *id,
		AssumeRole:    assumeRole,
		AccessKey:     accessKey,
		AccessSecret:  accessSecret,
		Region:        region,
		Data:          data,
		AccountNumber: accountNumber,
		AccountName:   *accountName,
		CloudProvider: cloudProviderStr,
	}

	ctxLogger.Info("GetAccountFromExternalId: successfully resolved account", "account_id", *id, "tenant", *tenant)
	return accountToReturn, nil
}

// getTenantForAccountId retrieves the tenant ID for a given internal account ID.
// Cached with a TTL since (account_id -> tenant) is stable per-account.
func getTenantForAccountId(accountId string) (string, error) {
	tenantLookupCacheMu.RLock()
	if v, ok := tenantLookupCache[accountId]; ok && time.Now().Before(v.expiry) {
		tid := v.tenantId
		tenantLookupCacheMu.RUnlock()
		return tid, nil
	}
	tenantLookupCacheMu.RUnlock()

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return "", fmt.Errorf("getTenantForAccountId: failed to get DB manager: %w", err)
	}
	var tenant string
	err = dbms.QueryRowAndScan(&tenant, "SELECT tenant FROM cloud_accounts WHERE id = $1", accountId)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", fmt.Errorf("getTenantForAccountId: account with id %s not found", accountId)
		}
		return "", fmt.Errorf("getTenantForAccountId: failed to query tenant for account %s: %w", accountId, err)
	}

	tenantLookupCacheMu.Lock()
	tenantLookupCache[accountId] = tenantLookupEntry{tenantId: tenant, expiry: time.Now().Add(tenantLookupTTL)}
	tenantLookupCacheMu.Unlock()

	return tenant, nil
}

// getInternalAccountAndTenant maps a cloud provider account number to the internal database ID and tenant.
// This lookup is ambiguous when multiple cloud_accounts share the same account_number.
// It returns an error if multiple active accounts are found. Prefer using the account ID directly when available.
func getInternalAccountAndTenant(awsAccountNumber string, providerName string) (string, string, error) {
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return "", "", fmt.Errorf("getInternalAccountAndTenant: failed to get DB manager: %w", err)
	}

	// Check for multiple matches to prevent silent cross-tenant data leakage
	var count int
	err = dbms.QueryRowAndScan(&count, "SELECT count(*) FROM cloud_accounts WHERE account_number = $1 AND lower(cloud_provider) = lower($2) AND status = 'active'", awsAccountNumber, providerName)
	if err != nil {
		return "", "", fmt.Errorf("getInternalAccountAndTenant: failed to count accounts for %s, provider %s: %w", awsAccountNumber, providerName, err)
	}
	if count > 1 {
		return "", "", fmt.Errorf("getInternalAccountAndTenant: multiple active accounts (%d) found for account_number %s, provider %s — cannot resolve without tenant context", count, awsAccountNumber, providerName)
	}

	var internalID, tenant string
	row, err := dbms.QueryRow("SELECT id, tenant FROM cloud_accounts WHERE account_number = $1 AND lower(cloud_provider) = lower($2) AND status = 'active' LIMIT 1", awsAccountNumber, providerName)
	if err != nil {
		return "", "", fmt.Errorf("getInternalAccountAndTenant: failed to find internal ID for account %s, provider %s: %w", awsAccountNumber, providerName, err)
	}
	err = row.Scan(&internalID, &tenant)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", "", fmt.Errorf("getInternalAccountAndTenant: no active account found for account_number %s, provider %s", awsAccountNumber, providerName)
		}
		return "", "", fmt.Errorf("getInternalAccountAndTenant: failed to scan account for %s, provider %s: %w", awsAccountNumber, providerName, err)
	}
	if internalID == "" {
		return "", "", fmt.Errorf("getInternalAccountAndTenant: no internal ID found for account %s, provider %s", awsAccountNumber, providerName)
	}
	return internalID, tenant, nil
}
