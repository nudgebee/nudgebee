package core

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"nudgebee/services/audit"
	"nudgebee/services/common"
	"nudgebee/services/query"
	"nudgebee/services/relay"
	"nudgebee/services/security"
	"slices"
	"strings"
	"sync"
	"time"

	"nudgebee/services/internal/database"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

const integrationCacheTTL = 10 * time.Minute

// integrationByTypeCache caches GetIntegrationByType results per account+type
// with a TTL to avoid repeated DB lookups.
var integrationByTypeCache = struct {
	sync.RWMutex
	entries map[string]integrationCacheEntry
}{entries: make(map[string]integrationCacheEntry)}

type integrationCacheEntry struct {
	value     *IntegrationDto
	expiresAt time.Time
}

// InvalidateIntegrationCache clears all integration lookup caches.
// Call this after any integration create/update/delete operation.
func InvalidateIntegrationCache() {
	integrationByTypeCache.Lock()
	integrationByTypeCache.entries = make(map[string]integrationCacheEntry)
	integrationByTypeCache.Unlock()
	query.InvalidateTraceIntegrationCache()
}

type IntegrationStatus string

const (
	IntegrationStatusEnabled  IntegrationStatus = "enabled"
	IntegrationStatusDisabled IntegrationStatus = "disabled"
	IntegrationStatusDraft    IntegrationStatus = "draft"
	IntegrationStatusError    IntegrationStatus = "error"
)

const (
	DefaultTraceProvider   = "default_traces_provider"
	DefaultLogProvider     = "default_log_provider"
	DefaultMetricsProvider = "default_metrics_provider"
	AccountId              = "account_id"
	IntegrationConfigName  = "integration_config_name"
	AuthType               = "auth_type"

	// IntegrationConfigRCAWritebackEnabled is the per-tenant toggle that opts a
	// ticketing integration (e.g. ZenDuty) into posting NudgeBee RCA analysis
	// back onto the source incident. Declared here (rather than in the providers
	// or the event package) so the write side (ConfigSchema) and the read side
	// (event.loadRCAWritebackConfig) share one constant and cannot drift.
	IntegrationConfigRCAWritebackEnabled = "rca_writeback_enabled"
)

type IntegrationDto struct {
	Id      string                   `json:"id"`
	Name    string                   `json:"name"`
	Configs []IntegrationConfigValue `json:"configs"`
	Tags    map[string]any           `json:"tags"`
	Schema  IntegrationSchema        `json:"schema"`
	Source  string                   `json:"source"`
	Type    string                   `json:"type"`
}

func CreateIntegrationConfig(
	ctx *security.RequestContext,
	integrationId string,
	intgerationType string,
	integrationConfigName string,
	integrationConfigValues []IntegrationConfigValue,
	tags map[string]any,
	accountIds []string,
	skipValidation bool,
	source string,
) (IntegrationDto, error) {
	if intgerationType == "" {
		return IntegrationDto{}, errors.New("integrations: integrationName is required")
	}

	if integrationConfigName == "" {
		return IntegrationDto{}, errors.New("integrations: integrationConfig.name is required")
	}

	integration, found := GetIntegrationBySource(intgerationType, source)
	if !found {
		return IntegrationDto{}, errors.New("integrations: integration not found")
	}

	isUpdate := integrationId != ""

	// Make accountIds optional for ticketing category (tenant-level integrations)
	if integration.Category() != IntegrationCategoryTicketing && len(accountIds) == 0 {
		return IntegrationDto{}, errors.New("integrations: accountId is required")
	}

	// vm_agent requires exactly one account — validate before any DB writes
	if intgerationType == "vm_agent" && len(accountIds) != 1 {
		return IntegrationDto{}, errors.New("integrations: vm_agent requires exactly one account")
	}

	integrationConfigSchema := integration.ConfigSchema()

	// Auto-allow account_mapping on all incident webhook integrations. Mapping is
	// applied centrally in integration_webhook.go, so every webhook supports it
	// without each ConfigSchema having to declare it.
	if integration.Category() == IntegrationCategoryIncidentWebhook {
		if _, exists := integrationConfigSchema.Properties["account_mapping"]; !exists {
			if integrationConfigSchema.Properties == nil {
				integrationConfigSchema.Properties = map[string]IntegrationSchemaProperty{}
			}
			integrationConfigSchema.Properties["account_mapping"] = IntegrationSchemaProperty{
				Type:        ToolSchemaTypeString,
				Description: "JSON mapping of labels to account IDs",
				Default:     "",
				Hidden:      true,
			}
		}
	}

	// Inject schema defaults that the frontend doesn't send (e.g. hidden
	// connection_mode=vm_agent).  Without this, proxy routing breaks for
	// user-created integrations.
	integrationConfigValues = applySchemaDefaults(integration, integrationConfigValues)

	// validate config field names against schema
	// (index-based loop so the TrimSpace persists to the slice — downstream code
	// like IsProxyIntegration relies on normalized names/values)
	integrationConfigFields := []string{}
	for i := range integrationConfigValues {
		integrationConfigValues[i].Name = strings.TrimSpace(integrationConfigValues[i].Name)
		integrationConfigValues[i].Value = strings.TrimSpace(integrationConfigValues[i].Value)
		name := integrationConfigValues[i].Name
		if name == "" {
			return IntegrationDto{}, errors.New("integrations: integration config value name is required")
		}
		if _, ok := integrationConfigSchema.Properties[name]; !ok {
			// Integrations can opt into accepting dynamic keys that aren't enumerated
			// in their static schema — used by LLM to allow per-agent override keys
			// (llm_provider_<agent>, llm_model_name_<agent>, llm_tier_*) without
			// hard-coding every agent name in Go (the agent list lives in llm-server
			// and is fetched dynamically by the UI from ai_list_agents).
			if dyn, isDyn := integration.(interface{ AllowsDynamicKey(string) bool }); isDyn && dyn.AllowsDynamicKey(name) {
				// accepted
			} else {
				return IntegrationDto{}, fmt.Errorf("integrations: integration config (%s) not found in schema", name)
			}
		}
		integrationConfigFields = append(integrationConfigFields, name)
	}

	// Auto-inject token config for webhook integrations if schema defines "token" but caller didn't provide it
	if _, hasToken := integrationConfigSchema.Properties["token"]; hasToken {
		tokenFound := false
		for _, v := range integrationConfigValues {
			if v.Name == "token" {
				tokenFound = true
				break
			}
		}
		if !tokenFound {
			integrationConfigValues = append(integrationConfigValues, IntegrationConfigValue{Name: "token", Value: ""})
			integrationConfigFields = append(integrationConfigFields, "token")
		}
	}

	// Proxy integrations (always-proxy types like http_proxy/mongodb_proxy/kafka_proxy,
	// and dual-mode types like postgresql/mysql/redis/ssh when connection_mode=vm_agent)
	// represent a distinct backend each, addressed by name through the relay.
	// Consumed below by the proxy-connectivity test (which IS config-aware — only
	// vm_agent-mode dual rows need a live connectivity probe to the forager).
	isProxy := IsProxyIntegration(intgerationType, integrationConfigValues)

	// Check if an integration of the same type already exists for any of the given accounts.
	// Skip for vm_agent, workflow_webhook, and proxy integrations (which includes
	// mcp via `dualModeProxyMapping`) — multiple per account are legitimate.
	//
	// All dual-mode types are skipped via `IsProxyIntegrationType` (type-only),
	// regardless of `connection_mode`. This brings the create-path in line with
	// the enable-path below (`integration_config.go:~1500`), which has always
	// used the type-only check. Rationale:
	//
	//   - SSH: distinct targets need distinct keys; the post-#31532 per-call
	//     `host_name` override handles ephemeral hosts that share a key but not
	//     cases where each target has its own credential.
	//   - postgres / mysql / clickhouse / mssql / oracle / redis: distinct
	//     databases need distinct integrations even when they live on the same
	//     account; the integration is addressed by name at execute time via
	//     `IdentifyConfig` (llm-server/tools/) and each action injects its
	//     own `k8s_secret` via `injectK8sSecret` (relay-server/workspace.go),
	//     so multiple per account work end-to-end.
	//
	// (`isProxy` is kept above because it's still consumed by the proxy-
	// connectivity test path further down — that path IS config-aware.)
	if len(accountIds) > 0 &&
		intgerationType != "vm_agent" &&
		intgerationType != "workflow_webhook" &&
		!IsProxyIntegrationType(intgerationType) {
		// Defense-in-depth: refuse the check (and therefore the save) if the
		// request has no tenant. The duplicate SELECT below filters by
		// `i.tenant_id = ?` — without a tenant filter the match could span
		// tenants for the same type, which is both wrong and a cross-tenant
		// leak. Mirrors the matching guard in the enable-path below.
		if ctx.GetSecurityContext().GetTenantId() == "" {
			return IntegrationDto{}, errors.New("integrations: tenant ID is required to save an integration")
		}
		dbmsCheck, dbErr := database.GetDatabaseManager(database.Metastore)
		if dbErr != nil {
			return IntegrationDto{}, dbErr
		}
		effectiveSource := source
		if effectiveSource == "" {
			effectiveSource = "user"
		}
		// Exclude the row being updated by id (not name) — otherwise renames of
		// single-instance types fail the check against their own old name.
		excludeId := integrationId
		if excludeId == "" {
			excludeId = "00000000-0000-0000-0000-000000000000"
		}
		// Disabled rows are treated as soft-removed for the duplicate check —
		// the read paths in this file already filter `status != 'disabled'`
		// (see the resolver queries below), so a disabled integration doesn't
		// participate in resolution. Counting it here would block the operator
		// from adding a replacement after they intentionally disabled the
		// previous one, which is the documented disable-then-replace flow.
		checkQuery, checkArgs, buildErr := sqlx.In(`
			SELECT ca.account_name, i.name
			FROM integrations i
			JOIN integrations_cloud_accounts ica ON i.id = ica.integration_id
			JOIN cloud_accounts ca ON ca.id = ica.cloud_account_id
			WHERE i.type = ?
			  AND i.tenant_id = ?
			  AND i.source = ?
			  AND ca.tenant = ?
			  AND ica.cloud_account_id IN (?)
			  AND i.id::text != ?
			  AND i.status != 'disabled'
			LIMIT 1
		`, intgerationType, ctx.GetSecurityContext().GetTenantId(), effectiveSource,
			ctx.GetSecurityContext().GetTenantId(), accountIds, excludeId)
		if buildErr != nil {
			return IntegrationDto{}, fmt.Errorf("failed to build duplicate check query: %w", buildErr)
		}
		checkQuery = dbmsCheck.Db.Rebind(checkQuery)
		var existingAccountName, existingIntegrationName string
		scanErr := dbmsCheck.Db.QueryRow(checkQuery, checkArgs...).Scan(&existingAccountName, &existingIntegrationName)
		if scanErr == nil {
			return IntegrationDto{}, fmt.Errorf("account '%s' already has a '%s' integration ('%s'); only one '%s' integration per account is supported — edit the existing one or remove it before adding another", existingAccountName, intgerationType, existingIntegrationName, intgerationType)
		} else if !errors.Is(scanErr, sql.ErrNoRows) {
			return IntegrationDto{}, fmt.Errorf("failed to check for existing integration: %w", scanErr)
		}
	}

	// validate required config fields (skip when skipValidation is true,
	// e.g. agent-managed integrations toggling default provider)
	if !skipValidation && len(integrationConfigSchema.Required) > 0 {
		for _, r := range integrationConfigSchema.Required {
			found := false
			for _, f := range integrationConfigFields {
				if r == f {
					found = true
					break
				}
			}
			if !found {
				return IntegrationDto{}, fmt.Errorf("integrations: field %s is required", r)
			}
		}
	}

	// validate configs for each account (skip if skipValidation is true)
	if !skipValidation {
		// For ticketing integrations (tenant-level), validate without account context
		accountsToValidate := accountIds
		if len(accountIds) == 0 {
			accountsToValidate = []string{""} // Single validation without account ID
		}

		for _, accId := range accountsToValidate {
			for integrationconfig := range integrationConfigValues {
				integrationConfigValues[integrationconfig].Value = strings.TrimSpace(integrationConfigValues[integrationconfig].Value)
				if integrationConfigValues[integrationconfig].IsEncrypted {
					// encrypted values should not be empty
					decryptedValue, err := common.Decrypt(integrationConfigValues[integrationconfig].Value)
					if err != nil {
						return IntegrationDto{}, fmt.Errorf("failed to decrypt value for field %s: %w", integrationConfigValues[integrationconfig].Name, err)
					}
					integrationConfigValues[integrationconfig].Value = decryptedValue
					integrationConfigValues[integrationconfig].IsEncrypted = false
				}
			}
			// For LLM updates with omit-to-keep semantics: the UI omits secret
			// fields the user didn't retype, so RequiredWhen validation would
			// fail spuriously even when the stored value satisfies the contract.
			// Augment the validation-only view with stored secrets that aren't
			// in the payload. The save loop further down still skips upsert for
			// these (preserving the stored row), so this only affects what
			// ValidateConfig sees.
			configForValidation := integrationConfigValues
			if isUpdate && intgerationType == "llm" {
				configForValidation = augmentLLMConfigWithStoredSecrets(ctx.GetContext(), ctx.GetSecurityContext().GetTenantId(), integrationId, integrationConfigValues)
			}
			validationErrors := integration.ValidateConfig(ctx.GetSecurityContext(), configForValidation, accId)
			if len(validationErrors) > 0 {
				return IntegrationDto{}, validationErrors[0]
			}
		}
	}

	// For proxy integrations, test actual connectivity via the proxy agent before saving
	if !skipValidation && IsProxyIntegration(intgerationType, integrationConfigValues) {
		for _, accId := range accountIds {
			dsConfig, buildErr := BuildSingleDatasourceConfig(intgerationType, integrationConfigValues)
			if buildErr != nil {
				return IntegrationDto{}, fmt.Errorf("failed to build datasource config for testing: %w", buildErr)
			}
			if testErr := relay.TestProxyDatasourceConfig(accId, dsConfig); testErr != nil {
				return IntegrationDto{}, fmt.Errorf("connection test failed: %w", testErr)
			}
		}
	}

	// save configs && values
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		slog.Error("integrations: failed to get database manager", "error", err)
		return IntegrationDto{}, err
	}

	labels := map[string]any{}
	if tags != nil {
		labels = tags
	}
	labelsJson, err := common.MarshalJson(labels)
	if err != nil {
		return IntegrationDto{}, err
	}

	if source == "" {
		source = "user"
	}

	// Wrap the name-uniqueness check and INSERT/UPDATE in a transaction.
	// SELECT ... FOR UPDATE locks any existing row with the same name, preventing
	// concurrent requests from both passing the check and creating duplicates.
	tx, txErr := dbms.BeginTx()
	if txErr != nil {
		return IntegrationDto{}, fmt.Errorf("failed to begin transaction: %w", txErr)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// Check integration_config_name uniqueness per (tenant, source, type).
	// FOR UPDATE locks the row so a concurrent request cannot bypass this check.
	var conflictingId string
	nameCheckErr := tx.QueryRow(`
		SELECT id::text FROM integrations
		WHERE name = $1 AND tenant_id = $2 AND source = $3 AND type = $4
		FOR UPDATE
	`, integrationConfigName, ctx.GetSecurityContext().GetTenantId(), source, integration.Name()).Scan(&conflictingId)
	if nameCheckErr == nil {
		if !isUpdate {
			// Incident-webhook integrations are inherently idempotent — the config row
			// stores a tenant-scoped token and a set of account mappings, both of which
			// the upsert path handles correctly. A caller that lost track of an
			// existing row (stale UI state, orphaned mappings, partial onboarding)
			// should not be punished with a fatal duplicate-name error. Promote the
			// create to an update against the conflicting row in the same tenant.
			if integration.Category() == IntegrationCategoryIncidentWebhook {
				integrationId = conflictingId
				isUpdate = true
			} else {
				return IntegrationDto{}, fmt.Errorf("integration config name '%s' already exists for this integration type", integrationConfigName)
			}
		} else if conflictingId != integrationId {
			return IntegrationDto{}, fmt.Errorf("integration config name '%s' already exists for this integration type", integrationConfigName)
		}
	} else if !errors.Is(nameCheckErr, sql.ErrNoRows) {
		return IntegrationDto{}, fmt.Errorf("failed to check integration name uniqueness: %w", nameCheckErr)
	}

	// Remove existing account mappings for integrations of the same type
	// so the new integration can take over those accounts.
	// Skip for vm_agent, workflow_webhook, mcp, and proxy integrations — multiple per account
	// are legitimate (each represents a distinct backend), so existing mappings must not be
	// detached. Reuses isProxy from above.
	if len(accountIds) > 0 && intgerationType != "vm_agent" && intgerationType != "workflow_webhook" && intgerationType != "mcp" && !isProxy {
		effectiveSource := source
		if effectiveSource == "" {
			effectiveSource = "user"
		}
		// Exclude the row being updated by id (not name), so a rename of a
		// single-instance integration does not detach its own account mapping.
		excludeId := integrationId
		if excludeId == "" {
			excludeId = "00000000-0000-0000-0000-000000000000"
		}
		deleteQuery, deleteArgs, buildErr := sqlx.In(`
			DELETE FROM integrations_cloud_accounts
			WHERE integration_id IN (
				SELECT i.id FROM integrations i
				WHERE i.type = ?
				  AND i.tenant_id = ?
				  AND i.source = ?
				  AND i.id::text != ?
			)
			AND cloud_account_id IN (?)
		`, intgerationType, ctx.GetSecurityContext().GetTenantId(), effectiveSource,
			excludeId, accountIds)
		if buildErr != nil {
			return IntegrationDto{}, fmt.Errorf("failed to build duplicate cleanup query: %w", buildErr)
		}
		deleteQuery = dbms.Db.Rebind(deleteQuery)
		if _, execErr := tx.Exec(deleteQuery, deleteArgs...); execErr != nil {
			return IntegrationDto{}, fmt.Errorf("failed to clean up existing account mappings: %w", execErr)
		}
		defer InvalidateIntegrationCache()
	}

	var configId string
	var isNewIntegration bool

	if isUpdate {
		// Verify the integration exists and belongs to this tenant
		var existingCount int
		if chkErr := tx.QueryRow(`
			SELECT COUNT(*) FROM integrations WHERE id = $1 AND tenant_id = $2
		`, integrationId, ctx.GetSecurityContext().GetTenantId()).Scan(&existingCount); chkErr != nil || existingCount == 0 {
			return IntegrationDto{}, errors.New("integrations: integration not found or access denied")
		}

		_, err = tx.Exec(`
			UPDATE integrations
			SET name = $1, labels = $2, updated_at = $3, updated_by = $4
			WHERE id = $5 AND tenant_id = $6`,
			integrationConfigName,
			string(labelsJson),
			time.Now(),
			ctx.GetSecurityContext().GetUserId(),
			integrationId,
			ctx.GetSecurityContext().GetTenantId(),
		)
		if err != nil {
			return IntegrationDto{}, fmt.Errorf("failed to update integration: %w", err)
		}
		configId = integrationId
		isNewIntegration = false
	} else {
		newId := uuid.NewString()
		integrationResult, err := tx.Query(`
			INSERT INTO integrations(id, tenant_id, type, source, name, status, created_at, updated_at, created_by, updated_by, labels)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
			RETURNING id::text`,
			newId,
			ctx.GetSecurityContext().GetTenantId(),
			integration.Name(),
			source,
			integrationConfigName,
			IntegrationStatusEnabled,
			time.Now(),
			time.Now(),
			ctx.GetSecurityContext().GetUserId(),
			ctx.GetSecurityContext().GetUserId(),
			string(labelsJson),
		)
		if err != nil {
			var pqErr *pq.Error
			if errors.As(err, &pqErr) && pqErr.Code == "23505" {
				return IntegrationDto{}, fmt.Errorf("integration config name '%s' already exists for this integration type", integrationConfigName)
			}
			return IntegrationDto{}, fmt.Errorf("failed to create integration: %w", err)
		}
		defer func() {
			if closeErr := integrationResult.Close(); closeErr != nil {
				slog.Error("Error closing response body", "error", closeErr)
			}
		}()
		configId = newId
		if integrationResult.Next() {
			if err := integrationResult.Scan(&configId); err != nil {
				return IntegrationDto{}, fmt.Errorf("failed to scan integration id: %w", err)
			}
		}
		isNewIntegration = true
	}

	if err := tx.Commit(); err != nil {
		return IntegrationDto{}, fmt.Errorf("failed to commit integration transaction: %w", err)
	}
	committed = true

	if len(accountIds) > 0 {
		query, args, err := sqlx.In(`
			DELETE FROM integrations_cloud_accounts
			WHERE integration_id = ?
			AND cloud_account_id NOT IN (?)
		`, configId, accountIds)
		if err != nil {
			return IntegrationDto{}, fmt.Errorf("failed to build delete query for cloud_accounts: %w", err)
		}
		query = dbms.Db.Rebind(query)

		_, err = dbms.Exec(query, args...)
		if err != nil {
			return IntegrationDto{}, fmt.Errorf("failed to delete stale accounts: %w", err)
		}
	}

	parameters := []string{DefaultLogProvider, DefaultMetricsProvider, DefaultTraceProvider}
	// insert config values
	for i, v := range integrationConfigValues {
		value := v.Value
		// Preserve existing LLM credential rows when the caller sends an
		// empty value for a secret field. UI omit-to-keep semantics: the
		// LLM modal stops sending API key / access key / secret key /
		// session token when the user didn't type a new value (so the
		// stored secret never round-trips through the browser). A caller
		// that nonetheless sends an empty string here would otherwise
		// overwrite the stored ciphertext with "" and silently break the
		// integration. Skipping the upsert entirely keeps the existing
		// row intact with its per-row is_encrypted flag preserved.
		//
		// Scoped to LLM by integration type — other integrations may
		// legitimately use empty-value to clear a field.
		if intgerationType == "llm" && value == "" && isUpdate && IsLLMSecretFieldName(v.Name) {
			continue
		}
		// LLM tier / agent override DELETE: when the modal sends an empty
		// value for a non-secret LLM override key (e.g. user cleared a tier
		// model, or deleted an agent override row), interpret it as
		// "remove this row from the integration". Without this, the stored
		// row stays and the cleared field reappears the next time the
		// integration is loaded. Secrets are handled by the omit-to-keep
		// guard above and never reach here with value="".
		if isUpdate && intgerationType == "llm" && value == "" && isLLMOverrideKeyForDelete(v.Name) {
			if _, delErr := dbms.Exec(
				`DELETE FROM integration_config_values WHERE integration_id = $1 AND name = $2`,
				configId, v.Name,
			); delErr != nil {
				return IntegrationDto{}, fmt.Errorf("failed to delete cleared LLM override %s: %w", v.Name, delErr)
			}
			continue
		}
		if value == "" && v.Name == "token" {
			// On update: preserve the existing token instead of regenerating.
			// Webhook integrations expose their token to users (URL with
			// ?token=...) — clobbering it on a partial update (e.g. binding
			// workflow_id to an existing workflow_webhook) silently breaks
			// every external system pointed at that URL.
			if isUpdate {
				var existingToken string
				scanErr := dbms.Db.QueryRow(`
					SELECT value FROM integration_config_values
					WHERE integration_id = $1 AND name = 'token'
					LIMIT 1
				`, configId).Scan(&existingToken)
				if scanErr == nil && existingToken != "" {
					integrationConfigValues[i].Value = existingToken
					value = existingToken
				}
			}
		}
		// Same preservation logic for workflow_webhook bindings: the
		// integration form (edit-from-Integrations-tab) doesn't know
		// about workflow_id and never includes it intentionally. If an
		// empty value sneaks through, keep the existing binding so editing
		// the filter_expression doesn't silently unbind the automation.
		if value == "" && v.Name == "workflow_id" && isUpdate {
			var existingWorkflowId string
			scanErr := dbms.Db.QueryRow(`
				SELECT value FROM integration_config_values
				WHERE integration_id = $1 AND name = 'workflow_id'
				LIMIT 1
			`, configId).Scan(&existingWorkflowId)
			if scanErr == nil && existingWorkflowId != "" {
				integrationConfigValues[i].Value = existingWorkflowId
				value = existingWorkflowId
			}
		}
		if value == "" && v.Name == "token" {
			randomToken, err := common.GenerateRandomHexString(36)
			if err != nil {
				return IntegrationDto{}, err
			}
			integrations, err := dbms.Db.Queryx(`
				SELECT integration_id
				FROM integration_config_values
				WHERE value = $1 AND name = 'token'`, randomToken)
			if err != nil {
				return IntegrationDto{}, err
			}
			defer func() {
				err := integrations.Close()
				if err != nil {
					slog.Error("Error closing response body", "error", err)
				}
			}()
			if integrations.Next() {
				return IntegrationDto{}, errors.New("duplicate token found")
			}
			integrationConfigValues[i].Value = randomToken
			value = randomToken
		}
		// shouldEncrypt resolves per-field whether the row must land encrypted.
		// The static schema's IsEncrypted flag handles every hardcoded field,
		// but the LLM integration also accepts dynamic per-agent/per-tier
		// secret keys (llm_provider_api_key_<agent>, _access_key_<agent>, etc.)
		// that AllowsDynamicKey lets through without a static schema entry.
		// Those rows must still be encrypted at rest — otherwise the global
		// `llm_provider_api_key` lands encrypted but the per-agent variant
		// lands in plaintext, an asymmetry that defeats encryption-at-rest.
		shouldEncrypt := integrationConfigSchema.Properties[v.Name].IsEncrypted
		if intgerationType == "llm" && IsLLMSecretFieldName(v.Name) {
			shouldEncrypt = true
		}
		if shouldEncrypt && value != "" && !v.IsEncrypted {
			value, err = common.Encrypt(value)
			if err != nil {
				return IntegrationDto{}, err
			}
		}

		// Skip schema-metadata fields whose source of truth is a dedicated column
		// on the integrations row, not integration_config_values:
		//   - integration_config_name → integrations.name
		// Storing them again here is a duplicate the rest of the code never reads.
		if v.Name == IntegrationConfigName {
			continue
		}

		if !slices.Contains(parameters, v.Name) {
			_, err = dbms.Exec(`
			INSERT INTO integration_config_values(
				id, integration_id, name, value, is_encrypted, created_at, updated_at, created_by, updated_by
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
			ON CONFLICT(integration_id, name)
			DO UPDATE SET value=excluded.value, is_encrypted=excluded.is_encrypted, updated_at=excluded.updated_at, updated_by=excluded.updated_by`,
				uuid.NewString(),
				configId,
				v.Name,
				value,
				shouldEncrypt,
				time.Now(),
				time.Now(),
				ctx.GetSecurityContext().GetUserId(),
				ctx.GetSecurityContext().GetUserId(),
			)
			if err != nil {
				return IntegrationDto{}, fmt.Errorf("failed to insert config value: %w", err)
			}
		}

		// handle default provider flags in integrations_cloud_accounts
		if v.Name == DefaultLogProvider || v.Name == DefaultTraceProvider || v.Name == DefaultMetricsProvider {
			var column string
			switch v.Name {
			case DefaultLogProvider:
				column = "default_log_provider"
			case DefaultTraceProvider:
				column = "default_traces_provider"
			case DefaultMetricsProvider:
				column = "default_metrics_provider"
			}

			// Check if value is a per-account JSON map (e.g. {"acc-id-1":"true","acc-id-2":"false"})
			perAccountValues := map[string]string{}
			if err := common.UnmarshalJson([]byte(value), &perAccountValues); err != nil {
				for _, accId := range accountIds {
					perAccountValues[accId] = value
				}
			} else {
				// Filter to only allowed account IDs
				allowedSet := make(map[string]bool, len(accountIds))
				for _, id := range accountIds {
					allowedSet[id] = true
				}
				for k := range perAccountValues {
					if !allowedSet[k] {
						delete(perAccountValues, k)
					}
				}
			}

			for accId, accValue := range perAccountValues {
				if accValue == "true" {
					// upsert true
					_, err = dbms.Exec(fmt.Sprintf(`
						INSERT INTO integrations_cloud_accounts (
							integration_id, cloud_account_id, tenant_id, %s
						)
						VALUES ($1,$2,$3,true)
						ON CONFLICT (integration_id, cloud_account_id, tenant_id)
						DO UPDATE SET %s=EXCLUDED.%s`,
						column, column, column),
						configId,
						accId,
						ctx.GetSecurityContext().GetTenantId(),
					)
					if err != nil {
						return IntegrationDto{}, fmt.Errorf("failed to upsert %s=true: %w", column, err)
					}

					// reset others
					_, err = dbms.Exec(fmt.Sprintf(`
						UPDATE integrations_cloud_accounts
						SET %s=false
						WHERE cloud_account_id=$1 AND integration_id != $2`,
						column),
						accId,
						configId,
					)
					if err != nil {
						return IntegrationDto{}, fmt.Errorf("failed to reset others for %s: %w", column, err)
					}
				} else {
					// explicitly set to false
					_, err = dbms.Exec(fmt.Sprintf(`
						UPDATE integrations_cloud_accounts
						SET %s=false
						WHERE integration_id=$1 AND cloud_account_id=$2`,
						column),
						configId,
						accId,
					)
					if err != nil {
						return IntegrationDto{}, fmt.Errorf("failed to update %s=false: %w", column, err)
					}
				}
			}
		}
	}

	if len(accountIds) > 0 {
		for _, accId := range accountIds {
			_, err = dbms.Exec(`
				INSERT INTO integrations_cloud_accounts (
					integration_id, cloud_account_id, tenant_id
				) VALUES ($1, $2, $3)
				ON CONFLICT (integration_id, cloud_account_id, tenant_id) DO NOTHING
			`,
				configId,
				accId,
				ctx.GetSecurityContext().GetTenantId(),
			)
			if err != nil {
				return IntegrationDto{}, fmt.Errorf("failed to ensure mapping for integration %s: %w", configId, err)
			}
		}
	}

	// Link agent-sourced proxy datasources to their vm_agent integration
	if source == "agent" {
		isVMAgentDatasource := false
		for _, v := range integrationConfigValues {
			if v.Name == "connection_mode" && v.Value == "vm_agent" {
				isVMAgentDatasource = true
				break
			}
		}
		if isVMAgentDatasource && len(accountIds) > 0 {
			var vmAgentConfigId string
			if qErr := dbms.Db.QueryRow(`
				SELECT i.id FROM integrations i
				JOIN integrations_cloud_accounts ica ON ica.integration_id = i.id
				WHERE i.tenant_id = $1
				  AND i.type = 'vm_agent'
				  AND ica.cloud_account_id = $2
				LIMIT 1
			`, ctx.GetSecurityContext().GetTenantId(), accountIds[0]).Scan(&vmAgentConfigId); qErr != nil {
				slog.Warn("integrations: could not find parent vm_agent for datasource",
					"integration_id", configId, "account_id", accountIds[0], "error", qErr)
			}
			if vmAgentConfigId != "" {
				if _, execErr := dbms.Exec(`
					INSERT INTO integration_config_values(
						id, integration_id, name, value, is_encrypted,
						created_at, updated_at, created_by, updated_by
					) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
					ON CONFLICT(integration_id, name)
					DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
					uuid.NewString(), configId, "vm_agent_config_id", vmAgentConfigId, false,
					time.Now(), time.Now(),
					ctx.GetSecurityContext().GetUserId(), ctx.GetSecurityContext().GetUserId(),
				); execErr != nil {
					slog.Error("integrations: failed to store vm_agent_config_id",
						"integration_id", configId, "vm_agent_config_id", vmAgentConfigId, "error", execErr)
				}
			}
		}
	}

	// audit
	auditEvent := audit.Audit{
		UserId:         ctx.GetSecurityContext().GetUserId(),
		TenantId:       ctx.GetSecurityContext().GetTenantId(),
		EventTime:      time.Now().UTC(),
		EventCategory:  audit.EventCategoryIntegration,
		EventTarget:    "integration",
		EventType:      audit.EventTypeIntegrationCreate,
		EventState:     integrationConfigName,
		EventPrevState: nil,
		EventActor:     audit.EventActorUiService,
		EventAction:    audit.EventActionCreate,
		EventStatus:    audit.EventStatusSuccess,
		EventAttr:      map[string]any{},
	}
	_ = audit.CreateAudit(ctx, &audit.AuditRequest{Audits: []audit.Audit{auditEvent}})

	// Auto-create proxy agent for vm_agent integration type
	if intgerationType == "vm_agent" {
		if isNewIntegration {
			creds, agentErr := CreateProxyAgent(ctx, accountIds[0])
			if agentErr != nil {
				return IntegrationDto{}, fmt.Errorf("failed to create proxy agent: %w", agentErr)
			}
			// Store access_key as a config value (persistent, queryable for deletion)
			_, err = dbms.Exec(`
				INSERT INTO integration_config_values(
					id, integration_id, name, value, is_encrypted, created_at, updated_at, created_by, updated_by
				) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
				ON CONFLICT(integration_id, name) DO UPDATE SET value=excluded.value`,
				uuid.NewString(), configId, "access_key", creds.AccessKey, false,
				time.Now(), time.Now(),
				ctx.GetSecurityContext().GetUserId(), ctx.GetSecurityContext().GetUserId(),
			)
			if err != nil {
				return IntegrationDto{}, fmt.Errorf("failed to store agent access_key: %w", err)
			}
			integrationConfigValues = append(integrationConfigValues,
				IntegrationConfigValue{Name: "access_key", Value: creds.AccessKey},
			)
			if creds.AccessSecret != "" {
				integrationConfigValues = append(integrationConfigValues,
					IntegrationConfigValue{Name: "access_secret", Value: creds.AccessSecret},
				)
			}
		} else {
			// Existing integration — return stored access_key so caller knows agent exists
			var existingKey string
			scanErr := dbms.Db.QueryRow(`
				SELECT value FROM integration_config_values
				WHERE integration_id = $1 AND name = 'access_key'
			`, configId).Scan(&existingKey)
			if scanErr == nil && existingKey != "" {
				integrationConfigValues = append(integrationConfigValues,
					IntegrationConfigValue{Name: "access_key", Value: existingKey},
				)
			}
		}
	}

	// Trigger proxy config push if this is a proxy integration
	if IsProxyIntegration(intgerationType, integrationConfigValues) {
		for _, accId := range accountIds {
			TriggerProxyConfigPush(accId)
		}
	}

	return IntegrationDto{
		Configs: integrationConfigValues,
		Name:    integrationConfigName,
		Tags:    tags,
		Schema:  integrationConfigSchema,
		Id:      configId,
	}, nil
}

func ListIntegrationConfigs(context *security.RequestContext, accountId string, integrationName string) ([]IntegrationDto, error) {
	configs := []IntegrationDto{}

	if integrationName == "" {
		return configs, errors.New("integrations: integrationName is required")
	}

	tenantId := context.GetSecurityContext().GetTenantId()
	if tenantId == "" {
		// integrations.tenant_id is uuid NOT NULL — interpolating '' would surface
		// as opaque "invalid input syntax for type uuid" from Postgres.
		slog.Error("integrations: missing tenant id on security context", "integration", integrationName, "account_id", accountId)
		return configs, errors.New("integrations: tenant id is required")
	}

	_, found := GetIntegration(integrationName)
	if !found {
		slog.Error("integrations: not found")
		return configs, errors.New("integrations: not found")
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		slog.Error("integrations: failed to get database manager", "error", err)
		return configs, err
	}

	// Use string interpolation via BuildInClause instead of parameterized queries ($1, $2, $3)
	// to avoid lib/pq unnamed prepared statement collisions under concurrent goroutines.
	query := fmt.Sprintf(`
		SELECT i.id::text,
		       i.tenant_id::text,
		       ica.cloud_account_id::text AS account_id,
		       i.name::text,
		       i.labels::text,
		       i.source::text,
		       i.type::text
		FROM integrations i
		JOIN integrations_cloud_accounts ica
		  ON i.id = ica.integration_id
		WHERE i.type = %s
		  AND i.tenant_id = %s
	`, dbms.BuildInClause(integrationName), dbms.BuildInClause(tenantId))

	var rows *sqlx.Rows
	if accountId != "" {
		query += fmt.Sprintf(" AND ica.cloud_account_id = %s", dbms.BuildInClause(accountId))
	}
	rows, err = dbms.Db.Queryx(query)
	if err != nil {
		slog.Error("integrations: failed to get", "error", err)
		return configs, err
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil {
			slog.Error("integrations: failed to close config rows", "error", cerr)
		}
	}()

	for rows.Next() {
		var configId, tenantId, accId, configName, configLabels, source, integrationType *string
		err = rows.Scan(&configId, &tenantId, &accId, &configName, &configLabels, &source, &integrationType)
		if err != nil {
			slog.Error("integrations: failed to scan", "error", err)
			return configs, err
		}

		labels := map[string]any{}
		if configLabels != nil && *configLabels != "" {
			err = common.UnmarshalJson([]byte(*configLabels), &labels)
			if err != nil {
				slog.Error("integrations: failed to unmarshal integration config labels", "error", err)
				return configs, err
			}
		}

		values, err := loadIntegrationConfigValues(dbms, *configId)
		if err != nil {
			slog.Error("integrations: failed to get config values", "error", err)
			return configs, err
		}

		configs = append(configs, IntegrationDto{
			Id:      *configId,
			Name:    *configName,
			Configs: values,
			Tags:    labels,
			Source:  *source,
			Type:    *integrationType,
		})
	}

	return configs, nil
}

// ListIntegrationConfigsByTenant returns the config values for every integration
// of the given type for the caller's tenant, WITHOUT joining
// integrations_cloud_accounts. Ticketing integrations (zenduty/pagerduty/jira/…)
// are tenant-scoped and never get a cloud-account link row (ticket-server's
// SaveTicketConfiguration writes only integrations + integration_config_values),
// so ListIntegrationConfigs — which inner-joins on integrations_cloud_accounts —
// returns zero rows for them. Use this for any tenant-level (non-cloud-account)
// integration config read; it mirrors the webhook ingestion read path
// (getIntegrationSettings reads integration_config_values by integration id).
func ListIntegrationConfigsByTenant(context *security.RequestContext, integrationName string) ([]IntegrationDto, error) {
	configs := []IntegrationDto{}

	if integrationName == "" {
		return configs, errors.New("integrations: integrationName is required")
	}

	tenantId := context.GetSecurityContext().GetTenantId()
	if tenantId == "" {
		slog.Error("integrations: missing tenant id on security context", "integration", integrationName)
		return configs, errors.New("integrations: tenant id is required")
	}

	if _, found := GetIntegration(integrationName); !found {
		slog.Error("integrations: not found", "integration", integrationName)
		return configs, errors.New("integrations: not found")
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		slog.Error("integrations: failed to get database manager", "error", err)
		return configs, err
	}

	// String interpolation via BuildInClause (not $1/$2) to avoid lib/pq unnamed
	// prepared-statement collisions under concurrent goroutines — same rationale
	// as ListIntegrationConfigs.
	// Exclude disabled integrations: the disable mutation flips integrations.status
	// to 'disabled' without clearing integration_config_values, so a disabled
	// integration would otherwise still surface its config (consistent with the
	// other tenant-scoped reads in this file that filter `status != 'disabled'`).
	query := fmt.Sprintf(`
		SELECT i.id::text, i.name::text, i.labels::text, i.source::text, i.type::text
		FROM integrations i
		WHERE i.type = %s
		  AND i.tenant_id = %s
		  AND i.status != 'disabled'
	`, dbms.BuildInClause(integrationName), dbms.BuildInClause(tenantId))

	rows, err := dbms.Db.Queryx(query)
	if err != nil {
		slog.Error("integrations: failed to get", "error", err)
		return configs, err
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil {
			slog.Error("integrations: failed to close config rows", "error", cerr)
		}
	}()

	for rows.Next() {
		var configId, configName, configLabels, source, integrationType *string
		if err = rows.Scan(&configId, &configName, &configLabels, &source, &integrationType); err != nil {
			slog.Error("integrations: failed to scan", "error", err)
			return configs, err
		}

		labels := map[string]any{}
		if configLabels != nil && *configLabels != "" {
			if err = common.UnmarshalJson([]byte(*configLabels), &labels); err != nil {
				slog.Error("integrations: failed to unmarshal integration config labels", "error", err)
				return configs, err
			}
		}

		values, err := loadIntegrationConfigValues(dbms, *configId)
		if err != nil {
			slog.Error("integrations: failed to get config values", "error", err)
			return configs, err
		}

		configs = append(configs, IntegrationDto{
			Id:      *configId,
			Name:    *configName,
			Configs: values,
			Tags:    labels,
			Source:  *source,
			Type:    *integrationType,
		})
	}

	return configs, nil
}

// loadIntegrationConfigValues reads all config values for a single integration
// id. Shared by ListIntegrationConfigs and ListIntegrationConfigsByTenant so the
// query, scan loop, and close handling can't drift between them.
func loadIntegrationConfigValues(dbms *database.DatabaseManager, integrationId string) ([]IntegrationConfigValue, error) {
	rows, err := dbms.Db.Queryx(fmt.Sprintf(`
		SELECT name::text, value::text, is_encrypted
		FROM integration_config_values
		WHERE integration_id = %s`, dbms.BuildInClause(integrationId)))
	if err != nil {
		return nil, err
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil {
			slog.Error("integrations: failed to close config value rows", "error", cerr)
		}
	}()

	values := []IntegrationConfigValue{}
	for rows.Next() {
		var name, value string
		var isEncrypted bool
		if err := rows.Scan(&name, &value, &isEncrypted); err != nil {
			slog.Error("integrations: failed to scan integration config value", "error", err, "configId", integrationId)
			continue
		}
		values = append(values, IntegrationConfigValue{Name: name, Value: value, IsEncrypted: isEncrypted})
	}
	return values, nil
}

// checkWorkflowWebhookNotInUse blocks delete/disable of a workflow_webhook
// integration when the workflow it references still exists. The frontend has
// the same guard but is bypassable via direct GraphQL calls.
func checkWorkflowWebhookNotInUse(tx *sqlx.Tx, integrationType, integrationId, tenantId, action string) error {
	if integrationType != "workflow_webhook" {
		return nil
	}
	// integration_config_values has no tenant_id column. The integrationId
	// passed in is already tenant-scoped: callers resolve it from
	// `integrations` WHERE tenant_id = ? (DeleteIntegrationConfig and
	// UpdateIntegrationConfigStatus both do this), so a direct lookup by
	// integration_id is safe here. Cast the param to uuid so the
	// (integration_id, name) unique index is used.
	var workflowId string
	err := tx.QueryRowx(`
		SELECT value FROM integration_config_values
		WHERE integration_id = $1::uuid AND name = 'workflow_id'
		LIMIT 1
	`, integrationId).Scan(&workflowId)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	if workflowId == "" {
		return nil
	}
	// workflow_id is stored as text — guard against malformed values so a
	// bad row doesn't blow up the cast and block legitimate deletes.
	if _, parseErr := uuid.Parse(workflowId); parseErr != nil {
		slog.Warn("integrations: workflow_webhook has non-uuid workflow_id, treating as no-association",
			"integration_id", integrationId, "workflow_id", workflowId)
		return nil
	}
	var workflowName string
	err = tx.QueryRowx(`
		SELECT name FROM workflows
		WHERE id = $1::uuid AND tenant_id = $2::uuid
		LIMIT 1
	`, workflowId, tenantId).Scan(&workflowName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	return fmt.Errorf("cannot %s workflow_webhook: automation %q (%s) is using it. Remove or update the automation first", action, workflowName, workflowId)
}

func DeleteIntegrationConfig(
	ctx *security.RequestContext,
	integrationType string,
	integrationConfigName string,
	source string,
) error {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		slog.Error("integrations: failed to get database manager", "error", err)
		return err
	}

	tx, err := dbms.Db.Beginx()
	if err != nil {
		slog.Error("integrations: failed to begin transaction", "error", err)
		return err
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			slog.Error("unable to to recover", p)
		} else if err != nil {
			_ = tx.Rollback()
		} else {
			if commitErr := tx.Commit(); commitErr != nil {
				slog.Error("integrations: commit failed", "error", commitErr)
				err = commitErr
			} else {
				InvalidateIntegrationCache()
			}
		}
	}()

	tenantId := ctx.GetSecurityContext().GetTenantId()

	if source == "" {
		source = "user"
	}

	// Find integration id
	var integrationId string
	if source != "" {
		err = tx.QueryRowx(`
			SELECT id
			FROM integrations
			WHERE tenant_id = $1
			  AND lower(type) = lower($2)
			  AND lower(name) = lower($3)
			  AND lower(source) = lower($4)
		`, tenantId, integrationType, integrationConfigName, source).Scan(&integrationId)
	} else {
		err = tx.QueryRowx(`
			SELECT id
			FROM integrations
			WHERE tenant_id = $1
			  AND lower(type) = lower($2)
			  AND lower(name) = lower($3)
		`, tenantId, integrationType, integrationConfigName).Scan(&integrationId)
	}
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("integrations: integration not found for given tenant and config")
		}
		slog.Error("integrations: failed to find integration", "error", err)
		return err
	}

	if err = checkWorkflowWebhookNotInUse(tx, integrationType, integrationId, tenantId, "delete"); err != nil {
		return err
	}

	// Check if this is a proxy integration by querying its config values
	var affectedAccountIDs []string
	isProxy := false
	if IsProxyIntegrationType(integrationType) {
		// Fetch config values to determine if this is a proxy integration
		configRows, qErr := tx.Queryx(`
			SELECT name::text, value::text FROM integration_config_values WHERE integration_id = $1
		`, integrationId)
		if qErr != nil {
			slog.Error("integrations: failed to query config values for proxy check",
				"integration_id", integrationId, "error", qErr)
			return qErr
		}
		var configVals []IntegrationConfigValue
		for configRows.Next() {
			var cv IntegrationConfigValue
			if sErr := configRows.Scan(&cv.Name, &cv.Value); sErr != nil {
				slog.Error("integrations: failed to scan config value", "error", sErr)
				continue
			}
			configVals = append(configVals, cv)
		}
		if cerr := configRows.Close(); cerr != nil {
			slog.Error("integrations: failed to close config rows", "error", cerr)
		}

		isProxy = IsProxyIntegration(integrationType, configVals)
	}

	// Capture affected account IDs before deletion (for proxy config push)
	if isProxy {
		accountRows, qErr := tx.Queryx(`
			SELECT cloud_account_id::text FROM integrations_cloud_accounts WHERE integration_id = $1
		`, integrationId)
		if qErr != nil {
			slog.Error("integrations: failed to query affected accounts before delete",
				"integration_id", integrationId, "error", qErr)
			return qErr
		}
		defer func() {
			if cerr := accountRows.Close(); cerr != nil {
				slog.Error("integrations: failed to close account rows", "error", cerr)
			}
		}()
		for accountRows.Next() {
			var accID string
			if sErr := accountRows.Scan(&accID); sErr != nil {
				slog.Error("integrations: failed to scan account ID", "error", sErr)
				return sErr
			}
			affectedAccountIDs = append(affectedAccountIDs, accID)
		}
		if rowErr := accountRows.Err(); rowErr != nil {
			return rowErr
		}
	}

	// Delete from integrations_cloud_accounts
	_, err = tx.Exec(`
        DELETE FROM integrations_cloud_accounts
        WHERE integration_id = $1
    `, integrationId)
	if err != nil {
		slog.Error("integrations: failed to delete from integrations_cloud_accounts", "error", err)
		return err
	}

	// Delete proxy agent if this is the last vm_agent integration using it
	if integrationType == "vm_agent" {
		var accessKey string
		scanErr := tx.QueryRowx(`
			SELECT value FROM integration_config_values
			WHERE integration_id = $1 AND name = 'access_key'
		`, integrationId).Scan(&accessKey)
		if scanErr == nil && accessKey != "" {
			var refCount int
			_ = tx.QueryRowx(`
				SELECT COUNT(*) FROM integration_config_values
				WHERE name = 'access_key' AND value = $1 AND integration_id != $2
			`, accessKey, integrationId).Scan(&refCount)
			if refCount == 0 {
				if delErr := DeleteProxyAgent(tx, accessKey); delErr != nil {
					slog.Error("integrations: failed to delete proxy agent on integration delete",
						"integration_id", integrationId, "error", delErr)
					return delErr
				}
			}
		}

		// Delete datasources created by this VM agent
		var linkedIntegrationIDs []string
		linkRows, qErr := tx.Queryx(`
			SELECT DISTINCT icv.integration_id
			FROM integration_config_values icv
			WHERE icv.name = 'vm_agent_config_id' AND icv.value = $1
		`, integrationId)
		if qErr != nil {
			slog.Error("integrations: failed to query linked datasources",
				"vm_agent_id", integrationId, "error", qErr)
			return qErr
		}
		for linkRows.Next() {
			var id string
			if sErr := linkRows.Scan(&id); sErr != nil {
				slog.Warn("integrations: failed to scan linked datasource id",
					"vm_agent_id", integrationId, "error", sErr)
				continue
			}
			linkedIntegrationIDs = append(linkedIntegrationIDs, id)
		}
		_ = linkRows.Close()

		for _, intID := range linkedIntegrationIDs {
			for _, accID := range affectedAccountIDs {
				if _, execErr := tx.Exec(`
					DELETE FROM integrations_cloud_accounts
					WHERE integration_id = $1 AND cloud_account_id = $2
				`, intID, accID); execErr != nil {
					slog.Error("integrations: failed to unlink datasource from account",
						"integration_id", intID, "account_id", accID, "error", execErr)
					return execErr
				}
			}

			var remaining int
			if scanErr := tx.QueryRowx(`
				SELECT COUNT(*) FROM integrations_cloud_accounts
				WHERE integration_id = $1
			`, intID).Scan(&remaining); scanErr != nil {
				slog.Error("integrations: failed to count remaining accounts for datasource",
					"integration_id", intID, "error", scanErr)
				return scanErr
			}
			if remaining == 0 {
				if _, execErr := tx.Exec(`DELETE FROM integration_config_values WHERE integration_id = $1`, intID); execErr != nil {
					slog.Error("integrations: failed to delete config values for linked datasource",
						"integration_id", intID, "error", execErr)
					return execErr
				}
				if _, execErr := tx.Exec(`DELETE FROM integrations WHERE id = $1`, intID); execErr != nil {
					slog.Error("integrations: failed to delete linked datasource",
						"integration_id", intID, "error", execErr)
					return execErr
				}
				slog.Info("integrations: deleted vm_agent datasource",
					"integration_id", intID, "vm_agent_id", integrationId)
			}
		}
	}

	// Delete from integration_config_values
	_, err = tx.Exec(`
        DELETE FROM integration_config_values
        WHERE integration_id = $1
    `, integrationId)
	if err != nil {
		slog.Error("integrations: failed to delete from integration_config_values", "error", err)
		return err
	}

	// Delete integration itself
	_, err = tx.Exec(`
        DELETE FROM integrations
        WHERE id = $1
    `, integrationId)
	if err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == "23503" {
			return fmt.Errorf("cannot delete this integration because it has linked ticket(s)")
		}
		slog.Error("integrations: failed to delete integration", "error", err)
		return err
	}

	// Audit
	auditEvent := audit.Audit{
		UserId:         ctx.GetSecurityContext().GetUserId(),
		TenantId:       tenantId,
		AccountId:      "", // not account-specific anymore
		EventTime:      time.Now().UTC(),
		EventCategory:  audit.EventCategoryIntegration,
		EventTarget:    "integration",
		EventType:      audit.EventTypeIntegrationDelete,
		EventState:     nil,
		EventPrevState: integrationConfigName,
		EventActor:     audit.EventActorUiService,
		EventAction:    audit.EventActionDelete,
		EventStatus:    audit.EventStatusSuccess,
		EventAttr:      map[string]any{},
	}
	_ = audit.CreateAudit(ctx, &audit.AuditRequest{Audits: []audit.Audit{auditEvent}})

	// Trigger proxy config push for affected accounts (datasource was removed)
	for _, accID := range affectedAccountIDs {
		TriggerProxyConfigPush(accID)
	}

	return nil
}

func UpdateIntegrationConfigStatus(
	ctx *security.RequestContext,
	integrationType string,
	integrationConfigName string,
	integrationStatus string,
) (err error) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		slog.Error("integrations: failed to get database manager", "error", err)
		return err
	}

	tx, err := dbms.Db.Beginx()
	if err != nil {
		slog.Error("integrations: failed to begin transaction", "error", err)
		return err
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			slog.Error("unable to to recover", p)
			err = fmt.Errorf("recovered from panic: %v", p)
		} else if err != nil {
			_ = tx.Rollback()
		} else {
			if commitErr := tx.Commit(); commitErr != nil {
				slog.Error("integrations: commit failed", "error", commitErr)
				err = commitErr
			} else {
				InvalidateIntegrationCache()
			}
		}
	}()

	tenantId := ctx.GetSecurityContext().GetTenantId()

	// Find integration id
	var integrationId string
	err = tx.QueryRowx(`
        SELECT id
        FROM integrations
        WHERE tenant_id = $1
          AND lower(type) = lower($2)
          AND lower(name) = lower($3)
    `, tenantId, integrationType, integrationConfigName).Scan(&integrationId)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("integrations: integration not found for given tenant and config")
		}
		slog.Error("integrations: failed to find integration", "error", err)
		return err
	}

	if integrationStatus == "disabled" {
		if err = checkWorkflowWebhookNotInUse(tx, integrationType, integrationId, tenantId, "disable"); err != nil {
			return err
		}
	}

	// Enabling: enforce the same one-per-(type, account) invariant the
	// create-path check enforces. Without this gate, enabling a previously-
	// disabled row while another row of the same type is already enabled
	// for an overlapping account leaves both rows active, and the resolver
	// has no deterministic way to pick — see the duplicate-check on create
	// above for the matching invariant.
	//
	// Skip the check for integration types that legitimately allow multiple
	// active configs per account (mirrors the create-path skip-list above).
	// IsProxyIntegrationType is type-only — dual-mode types (postgres / mysql /
	// clickhouse / mssql / oracle / redis / ssh) are skipped in both modes
	// because each row addresses a distinct backend by name at execute time
	// regardless of connection_mode. The create-path uses the same type-only
	// check so the create/enable behaviour is symmetric.
	// `mcp` is intentionally NOT in the explicit skip list: it's already in
	// `dualModeProxyMapping`, so `IsProxyIntegrationType("mcp")` returns true
	// and the trailing predicate skips the check. Keeping the explicit list
	// to types that aren't proxy-eligible (vm_agent, workflow_webhook).
	if integrationStatus == "enabled" &&
		integrationType != "vm_agent" &&
		integrationType != "workflow_webhook" &&
		!IsProxyIntegrationType(integrationType) {
		// Defense-in-depth: refuse the check (and therefore the enable) if
		// the request has no tenant. Without a tenant filter the duplicate
		// SELECT below would return matches across all tenants for the same
		// type, which would be both wrong and a cross-tenant leak.
		if tenantId == "" {
			return errors.New("integrations: tenant ID is required to enable an integration")
		}
		var conflictAccountName, conflictIntegrationName string
		scanErr := tx.QueryRow(`
            SELECT ca.account_name, i.name
            FROM integrations i
            JOIN integrations_cloud_accounts ica ON i.id = ica.integration_id
            JOIN cloud_accounts ca ON ca.id = ica.cloud_account_id
            WHERE i.tenant_id = $1
              AND i.type = $2
              AND i.status = 'enabled'
              AND i.id != $3
              AND ica.cloud_account_id IN (
                  SELECT cloud_account_id
                  FROM integrations_cloud_accounts
                  WHERE integration_id = $3
              )
            LIMIT 1
        `, tenantId, integrationType, integrationId).Scan(&conflictAccountName, &conflictIntegrationName)
		if scanErr == nil {
			return fmt.Errorf(
				"account '%s' already has an enabled '%s' integration ('%s'); only one '%s' integration per account can be enabled — disable it before enabling another",
				conflictAccountName, integrationType, conflictIntegrationName, integrationType,
			)
		} else if !errors.Is(scanErr, sql.ErrNoRows) {
			slog.Error("integrations: failed to check for existing enabled integration", "error", scanErr)
			return fmt.Errorf("failed to check for existing enabled integration: %w", scanErr)
		}
	}

	_, err = tx.Exec(`
        UPDATE integrations set status = $2
        WHERE id = $1
    `, integrationId, integrationStatus)
	if err != nil {
		slog.Error("integrations: failed to update integration status", "error", err)
		return err
	}

	// Audit
	auditEvent := audit.Audit{
		UserId:   ctx.GetSecurityContext().GetUserId(),
		TenantId: tenantId,

		EventTime:      time.Now().UTC(),
		EventCategory:  audit.EventCategoryIntegration,
		EventTarget:    "integration",
		EventType:      audit.EventTypeIntegrationUpdate,
		EventState:     map[string]string{"name": integrationConfigName, "status": integrationStatus},
		EventPrevState: integrationConfigName,
		EventActor:     audit.EventActorUiService,
		EventAction:    audit.EventActionUpdate,
		EventStatus:    audit.EventStatusSuccess,
		EventAttr:      map[string]any{},
	}
	if auditErr := audit.CreateAudit(ctx, &audit.AuditRequest{Audits: []audit.Audit{auditEvent}}); auditErr != nil {
		slog.Error("integrations: failed to create audit event", "error", auditErr)
	}

	return
}

// applySchemaDefaults fills in missing config values from the integration's schema defaults.
// This ensures hidden fields like connection_mode are always present, even when the caller
// (frontend or DB row) omits them. Without this, IsProxyIntegration may mis-route dual-mode
// integrations to the K8s validation path which only checks field presence, not connectivity.
//
// Skips schema-metadata fields (integration_config_name) that are stored as columns on the
// integrations row, not as integration_config_values rows. Injecting an empty string for
// them would land a redundant duplicate of the integration name in the values table.
func applySchemaDefaults(integration Integration, configValues []IntegrationConfigValue) []IntegrationConfigValue {
	schema := integration.ConfigSchema()
	existing := make(map[string]bool, len(configValues))
	for _, cv := range configValues {
		existing[cv.Name] = true
	}
	existing[IntegrationConfigName] = true // skip — it's the integrations.name column
	for name, prop := range schema.Properties {
		if existing[name] || prop.Default == nil {
			continue
		}
		configValues = append(configValues, IntegrationConfigValue{
			Name:  name,
			Value: fmt.Sprintf("%v", prop.Default),
		})
	}
	return configValues
}

// TestIntegrationConnectionByConfig tests connectivity using raw config values
// (before the integration is saved). Used by the "Test Connection" button in the add/edit form.
func TestIntegrationConnectionByConfig(
	ctx *security.RequestContext,
	integrationType string,
	configValues []IntegrationConfigValue,
	accountIds []string,
	source string,
	integrationID string,
) error {
	if integrationType == "" {
		return errors.New("integration_name is required")
	}

	integration, found := GetIntegrationBySource(integrationType, source)
	if !found {
		return fmt.Errorf("integration type '%s' not registered", integrationType)
	}

	// Decrypt encrypted values
	for i := range configValues {
		if configValues[i].IsEncrypted && configValues[i].Value != "" {
			decrypted, decErr := common.Decrypt(configValues[i].Value)
			if decErr != nil {
				return fmt.Errorf("failed to decrypt field %s: %w", configValues[i].Name, decErr)
			}
			configValues[i].Value = decrypted
			configValues[i].IsEncrypted = false
		}
	}

	// Edit-flow augmentation: when the user clicks Test Connection on an edit
	// form, they may not have re-typed the API key — the SecretInput shows
	// "✓ Configured" and the UI sends an empty value (omit-to-keep semantics).
	// Without this augmentation the probe would run against a blank secret and
	// fail auth even when the stored secret was still valid. Mirror the
	// equivalent path in CreateIntegrationConfig that injects stored secrets
	// into the validation payload before ValidateConfig runs.
	//
	// Scoped to LLM integrations because the augmentLLMConfigWithStoredSecrets
	// helper knows the LLM secret-shaped prefixes (llm_provider_api_key,
	// llm_provider_access_key, etc.). For non-LLM types this loop is a no-op.
	if integrationType == "llm" && integrationID != "" {
		configValues = augmentLLMConfigWithStoredSecrets(
			ctx.GetContext(),
			ctx.GetSecurityContext().GetTenantId(),
			integrationID,
			configValues,
		)
		// The augmented entries come back encrypted (decrypted only at read).
		// Decrypt them so the per-target probe (which builds clients from raw
		// values) sees the plaintext credentials.
		for i := range configValues {
			if configValues[i].IsEncrypted && configValues[i].Value != "" {
				decrypted, decErr := common.Decrypt(configValues[i].Value)
				if decErr != nil {
					return fmt.Errorf("failed to decrypt stored secret %s: %w", configValues[i].Name, decErr)
				}
				configValues[i].Value = decrypted
				configValues[i].IsEncrypted = false
			}
		}
	}

	if len(accountIds) == 0 {
		return errors.New("at least one account_id is required for connection test")
	}

	// Apply schema defaults so hidden fields like connection_mode are always present
	configValues = applySchemaDefaults(integration, configValues)

	// For proxy integrations, test via relay proxy agent
	if IsProxyIntegration(integrationType, configValues) {
		dsConfig, buildErr := BuildSingleDatasourceConfig(integrationType, configValues)
		if buildErr != nil {
			return fmt.Errorf("failed to build datasource config: %w", buildErr)
		}
		if testErr := relay.TestProxyDatasourceConfig(accountIds[0], dsConfig); testErr != nil {
			return fmt.Errorf("connection test failed: %w", testErr)
		}
		return nil
	}

	// For K8s mode, use the integration's ValidateConfig.
	// Warn if a dual-mode type fell through to K8s validation — this likely means the proxy
	// path was expected but connection_mode was somehow still missing after applying defaults.
	if IsProxyIntegrationType(integrationType) {
		slog.Warn("integrations: dual-mode type routed to K8s validation instead of proxy test",
			"type", integrationType, "source", source)
	}
	validationErrors := integration.ValidateConfig(ctx.GetSecurityContext(), configValues, accountIds[0])
	if len(validationErrors) > 0 {
		return validationErrors[0]
	}

	// If the integration opts into a real connectivity probe (via the
	// TestableIntegration interface), run it on top of structural validation.
	// Today only LLM uses this — for everything else this is a no-op cast and
	// we keep the prior "validation == success" behaviour.
	if testable, ok := integration.(TestableIntegration); ok {
		if testErr := testable.TestConnection(ctx.GetSecurityContext(), configValues, accountIds[0]); testErr != nil {
			return testErr
		}
	}

	return nil
}

// TestIntegrationConnection tests connectivity for an existing integration by ID.
// It fetches the integration config, decrypts encrypted values, and runs the
// integration's ValidateConfig (for K8s mode) or proxy connectivity test (for vm_agent mode).
func TestIntegrationConnection(ctx *security.RequestContext, integrationID string) error {
	if integrationID == "" {
		return errors.New("integrations: integration_id is required")
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return fmt.Errorf("failed to get database manager: %w", err)
	}

	tenantID := ctx.GetSecurityContext().GetTenantId()

	// Fetch integration record
	var integrationType, integrationSource string
	err = dbms.Db.QueryRow(`
		SELECT type::text, source::text
		FROM integrations
		WHERE id = $1 AND tenant_id = $2
	`, integrationID, tenantID).Scan(&integrationType, &integrationSource)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("integration not found")
		}
		return fmt.Errorf("failed to query integration: %w", err)
	}

	integration, found := GetIntegrationBySource(integrationType, integrationSource)
	if !found {
		return fmt.Errorf("integration type '%s' not registered", integrationType)
	}

	// Fetch config values
	configRows, err := dbms.Db.Queryx(`
		SELECT name::text, value::text, is_encrypted
		FROM integration_config_values
		WHERE integration_id = $1
	`, integrationID)
	if err != nil {
		return fmt.Errorf("failed to query config values: %w", err)
	}
	defer func() {
		if cerr := configRows.Close(); cerr != nil {
			slog.Error("integrations: failed to close config rows", "error", cerr)
		}
	}()

	var configValues []IntegrationConfigValue
	for configRows.Next() {
		var cv IntegrationConfigValue
		if err := configRows.Scan(&cv.Name, &cv.Value, &cv.IsEncrypted); err != nil {
			return fmt.Errorf("failed to scan config value: %w", err)
		}
		configValues = append(configValues, cv)
	}

	// Decrypt encrypted values
	for i := range configValues {
		if configValues[i].IsEncrypted && configValues[i].Value != "" {
			decrypted, decErr := common.Decrypt(configValues[i].Value)
			if decErr != nil {
				return fmt.Errorf("failed to decrypt field %s: %w", configValues[i].Name, decErr)
			}
			configValues[i].Value = decrypted
			configValues[i].IsEncrypted = false
		}
	}

	// Fetch associated account IDs
	accountRows, err := dbms.Db.Queryx(`
		SELECT cloud_account_id::text
		FROM integrations_cloud_accounts
		WHERE integration_id = $1
	`, integrationID)
	if err != nil {
		return fmt.Errorf("failed to query account mappings: %w", err)
	}
	defer func() {
		if cerr := accountRows.Close(); cerr != nil {
			slog.Error("integrations: failed to close account rows", "error", cerr)
		}
	}()

	var accountIDs []string
	for accountRows.Next() {
		var accID string
		if err := accountRows.Scan(&accID); err != nil {
			return fmt.Errorf("failed to scan account ID: %w", err)
		}
		accountIDs = append(accountIDs, accID)
	}

	if len(accountIDs) == 0 {
		return errors.New("no accounts associated with this integration")
	}

	// Apply schema defaults so hidden fields like connection_mode are always present
	configValues = applySchemaDefaults(integration, configValues)

	// For proxy integrations, test via relay proxy agent
	if IsProxyIntegration(integrationType, configValues) {
		dsConfig, buildErr := BuildSingleDatasourceConfig(integrationType, configValues)
		if buildErr != nil {
			return fmt.Errorf("failed to build datasource config: %w", buildErr)
		}
		if testErr := relay.TestProxyDatasourceConfig(accountIDs[0], dsConfig); testErr != nil {
			return fmt.Errorf("connection test failed: %w", testErr)
		}
		return nil
	}

	// For K8s mode, use the integration's ValidateConfig.
	if IsProxyIntegrationType(integrationType) {
		slog.Warn("integrations: dual-mode type routed to K8s validation instead of proxy test",
			"type", integrationType, "integration_id", integrationID)
	}
	validationErrors := integration.ValidateConfig(ctx.GetSecurityContext(), configValues, accountIDs[0])
	if len(validationErrors) > 0 {
		return validationErrors[0]
	}

	if testable, ok := integration.(TestableIntegration); ok {
		if testErr := testable.TestConnection(ctx.GetSecurityContext(), configValues, accountIDs[0]); testErr != nil {
			return testErr
		}
	}

	return nil
}

func IntegrationConfigs(context *security.RequestContext, integrationName string, source string) (IntegrationSchema, error) {
	if integrationName == "" {
		return IntegrationSchema{}, errors.New("integrations: integrationName is required")
	}

	integration, found := GetIntegrationBySource(integrationName, source)
	if !found {
		slog.Error("integrations: not found")
		return IntegrationSchema{}, errors.New("integrations: not found")
	}

	schema := integration.ConfigSchema()
	schema.Category = integration.Category()
	return schema, nil
}

func GetIntegrationByConfigNameValues(
	context *security.RequestContext,
	accountId string,
	configName string,
	configValue string,
) (*IntegrationDto, error) {
	if configName == "" || configValue == "" {
		return nil, errors.New("integrations: configName and configValue are required")
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		context.GetLogger().Error("integrations: failed to get database manager", "error", err)
		return nil, err
	}

	var rows *sqlx.Rows

	if configName == DefaultLogProvider || configName == DefaultTraceProvider || configName == DefaultMetricsProvider {
		column := ""
		switch configName {
		case DefaultLogProvider:
			column = "default_log_provider"
		case DefaultTraceProvider:
			column = "default_traces_provider"
		case DefaultMetricsProvider:
			column = "default_metrics_provider"
		}

		// Use BuildInClause to safely interpolate values into the query string
		// instead of using parameterized queries ($1, $2, $3) which trigger
		// lib/pq unnamed prepared statements that collide under concurrent goroutines.
		rows, err = dbms.Db.Queryx(fmt.Sprintf(`
			SELECT i.id, i.name, i.source, i.type
			FROM integrations i
			JOIN integrations_cloud_accounts ica
				ON i.id = ica.integration_id
			WHERE ica.cloud_account_id = %s
			AND ica.%s = %s
			AND i.tenant_id = %s
			AND i.status != 'disabled'
			LIMIT 1
		`, dbms.BuildInClause(accountId), column, dbms.BuildInClause(configValue), dbms.BuildInClause(context.GetSecurityContext().GetTenantId())))
	} else {
		rows, err = dbms.Db.Queryx(fmt.Sprintf(`
			SELECT i.id, i.name, i.source, i.type
			FROM integrations i
			JOIN integrations_cloud_accounts ica
				ON i.id = ica.integration_id
			JOIN integration_config_values icv
				ON i.id = icv.integration_id
			WHERE ica.cloud_account_id = %s
			AND icv.name = %s
			AND icv.value = %s
			AND i.tenant_id = %s
			AND i.status != 'disabled'
			LIMIT 1
		`, dbms.BuildInClause(accountId), dbms.BuildInClause(configName), dbms.BuildInClause(configValue), dbms.BuildInClause(context.GetSecurityContext().GetTenantId())))
	}

	if err != nil {
		context.GetLogger().Error("integrations: failed to get integration by config values", "error", err)
		return nil, err
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil {
			slog.Error("integrations: failed to close integration by config values result", "error", cerr)
		}
	}()

	if rows.Next() {
		var integration IntegrationDto
		if err := rows.Scan(
			&integration.Id,
			&integration.Name,
			&integration.Source,
			&integration.Type,
		); err != nil {
			return nil, err
		}
		integration.Configs = []IntegrationConfigValue{}
		integration.Tags = map[string]any{}
		integration.Schema = IntegrationSchema{}

		return &integration, nil
	}

	return nil, nil
}

// ListActiveIntegrationsForAccount returns every active (non-disabled)
// integration linked to the given cloud account for the caller's tenant.
// Only identity columns (id, name, source, type) are populated — config
// values are intentionally not loaded since callers of this list path only
// need the type/source pair (e.g. to enumerate available observability
// providers). Mirrors the `status != 'disabled'` active-filter used by the
// other read paths in this file.
func ListActiveIntegrationsForAccount(
	context *security.RequestContext,
	accountId string,
) ([]IntegrationDto, error) {
	integrations := []IntegrationDto{}

	if accountId == "" {
		return integrations, errors.New("integrations: accountId is required")
	}

	tenantId := context.GetSecurityContext().GetTenantId()
	if tenantId == "" {
		return integrations, errors.New("integrations: tenant id is required")
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		context.GetLogger().Error("integrations: failed to get database manager", "error", err)
		return integrations, err
	}

	// String interpolation via BuildInClause (not $1/$2) to avoid lib/pq
	// unnamed prepared-statement collisions under concurrent goroutines —
	// same rationale as ListIntegrationConfigs / GetIntegrationByConfigNameValues.
	rows, err := dbms.Db.Queryx(fmt.Sprintf(`
		SELECT i.id::text, i.name::text, i.source::text, i.type::text
		FROM integrations i
		JOIN integrations_cloud_accounts ica
			ON i.id = ica.integration_id
		WHERE ica.cloud_account_id = %s
		  AND i.tenant_id = %s
		  AND i.status != 'disabled'
	`, dbms.BuildInClause(accountId), dbms.BuildInClause(tenantId)))
	if err != nil {
		context.GetLogger().Error("integrations: failed to list active integrations for account", "error", err)
		return integrations, err
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil {
			context.GetLogger().Error("integrations: failed to close active integrations result", "error", cerr)
		}
	}()

	for rows.Next() {
		var id, name, source, integrationType string
		if err := rows.Scan(&id, &name, &source, &integrationType); err != nil {
			context.GetLogger().Error("integrations: failed to scan active integration row", "error", err)
			return integrations, err
		}
		integrations = append(integrations, IntegrationDto{
			Id:      id,
			Name:    name,
			Source:  source,
			Type:    integrationType,
			Configs: []IntegrationConfigValue{},
			Tags:    map[string]any{},
		})
	}
	if err := rows.Err(); err != nil {
		context.GetLogger().Error("integrations: error iterating active integration rows", "error", err)
		return integrations, err
	}

	return integrations, nil
}

func GetIntegrationByType(
	context *security.RequestContext,
	accountId string,
	integrationType string,
) (*IntegrationDto, error) {
	// Check cache first
	cacheKey := accountId + ":" + integrationType + ":" + context.GetSecurityContext().GetTenantId()
	integrationByTypeCache.RLock()
	if entry, ok := integrationByTypeCache.entries[cacheKey]; ok {
		if time.Now().Before(entry.expiresAt) {
			integrationByTypeCache.RUnlock()
			return entry.value, nil
		}
		// Expired — evict the stale entry
		integrationByTypeCache.RUnlock()
		integrationByTypeCache.Lock()
		if e, exists := integrationByTypeCache.entries[cacheKey]; exists && time.Now().After(e.expiresAt) {
			delete(integrationByTypeCache.entries, cacheKey)
		}
		integrationByTypeCache.Unlock()
	} else {
		integrationByTypeCache.RUnlock()
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, err
	}

	rows, err := dbms.Db.Queryx(`
        SELECT i.id, i.name, i.source, i.type
        FROM integrations i
        JOIN integrations_cloud_accounts ica
            ON i.id = ica.integration_id
        WHERE ica.cloud_account_id = $1
        AND i.type = $2
        AND i.tenant_id = $3
        AND i.status != 'disabled'
        LIMIT 1
    `, accountId, integrationType, context.GetSecurityContext().GetTenantId())

	if err != nil {
		return nil, err
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil {
			slog.Error("integrations: failed to close integration cloud accounts values result", "error", cerr)
		}
	}()

	var result *IntegrationDto
	if rows.Next() {
		var integration IntegrationDto
		if err := rows.Scan(
			&integration.Id,
			&integration.Name,
			&integration.Source,
			&integration.Type,
		); err != nil {
			return nil, err
		}
		integration.Configs = []IntegrationConfigValue{}
		integration.Tags = map[string]any{}
		integration.Schema = IntegrationSchema{}
		result = &integration
	}

	// Cache the result (including nil)
	integrationByTypeCache.Lock()
	integrationByTypeCache.entries[cacheKey] = integrationCacheEntry{
		value:     result,
		expiresAt: time.Now().Add(integrationCacheTTL),
	}
	integrationByTypeCache.Unlock()

	return result, nil
}

// GetIntegrationConfigValueByName returns the value of a single config entry
// for the given integration. Returns an empty string (no error) when the
// entry is not present. Encrypted values are returned as stored (encrypted);
// callers that need plaintext must decrypt themselves.
func GetIntegrationConfigValueByName(
	ctx *security.RequestContext,
	integrationId string,
	name string,
) (string, error) {
	if integrationId == "" || name == "" {
		return "", nil
	}
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return "", err
	}
	var value string
	row := dbms.Db.QueryRowx(`
        SELECT value
        FROM integration_config_values
        WHERE integration_id = $1 AND name = $2
        LIMIT 1
    `, integrationId, name)
	if err := row.Scan(&value); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	return value, nil
}
