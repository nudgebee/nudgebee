package core

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
)

// llmSecretFieldPrefixes is the canonical list of secret-shaped config-key
// prefixes for the LLM integration. Anything starting with one of these is a
// credential and must never round-trip to clients, must be encrypted at rest
// (where the schema declares IsEncrypted=true), and must be preserved on
// omit-to-keep semantics during save.
//
// The prefix shape covers the bare global key, every per-agent override
// (llm_provider_api_key_<agent>, etc. — all share the llm_provider_* prefix),
// AND every per-tier override (llm_tier_api_key_<tier>, etc. — these use the
// distinct llm_tier_* prefix, so they must be listed separately). Matches the
// resolver's read paths in llm-server (see llm/llm-server/agents/core/
// llm_config.go: llmTierApiKeyFormat et al.).
//
// Lives in package core (not the integrations parent package) because it is
// referenced from CreateIntegrationConfig's save path, and core cannot
// import its parent. Keep the pattern in sync with the LIKE clauses in
// query/metadata.go's admin_get_integrations_v2 redaction subquery.
var llmSecretFieldPrefixes = []string{
	"llm_provider_api_key",
	"llm_provider_access_key",
	"llm_provider_secret_key",
	"llm_provider_session_token",
	"llm_oauth_client_secret",
	"llm_tier_api_key",
	"llm_tier_access_key",
	"llm_tier_secret_key",
	"llm_tier_session_token",
	"llm_tier_oauth_client_secret",
	// The integration's configured fallback slot (#36339).
	"llm_fallback_api_key",
	"llm_fallback_access_key",
	"llm_fallback_secret_key",
}

// LLMSecretFieldPrefixes returns a copy of the secret prefixes, so other
// packages can check their own lists (e.g. the list-API redaction) stay in sync.
func LLMSecretFieldPrefixes() []string {
	return append([]string(nil), llmSecretFieldPrefixes...)
}

// IsLLMSecretFieldName reports whether the given integration_config_values
// name is a credential for the LLM integration. Pattern-based so per-agent
// variants (llm_provider_api_key_<agent> etc.) are covered without
// enumerating every agent — the agent list lives in llm-server and is
// fetched dynamically by the UI via ai_list_agents.
func IsLLMSecretFieldName(name string) bool {
	for _, p := range llmSecretFieldPrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// isLLMOverrideKeyForDelete reports whether the given config-value name is a
// tier or per-agent override key that the LLM modal manages and that should
// be DELETE'd from the DB when the modal sends value="" on update. This is
// distinct from omit-to-keep semantics for secret fields — those are handled
// by IsLLMSecretFieldName and never reach the DELETE branch.
//
// Matches:
//   - llm_tier_provider_<tier>, llm_tier_model_<tier>, llm_tier_model_fallbacks_<tier>
//   - llm_provider_<agent>, llm_model_name_<agent>, llm_model_fallbacks_<agent>
//
// Excludes:
//   - llm_provider, llm_model_name, llm_model_fallbacks (global — required,
//     UI validates non-empty before allowing save)
//   - Any llm_provider_api_key, llm_provider_access_key, etc. (secrets — see
//     IsLLMSecretFieldName)
func isLLMOverrideKeyForDelete(name string) bool {
	if IsLLMSecretFieldName(name) {
		return false
	}
	// Tier overrides, and the configured fallback's non-secret keys (#36339).
	if strings.HasPrefix(name, "llm_tier_") || strings.HasPrefix(name, "llm_fallback_") {
		return true
	}
	// Per-agent overrides — distinguished from globals by the trailing
	// _<agent> suffix.
	for _, prefix := range []string{"llm_provider_", "llm_model_name_", "llm_model_fallbacks_"} {
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		suffix := strings.TrimPrefix(name, prefix)
		// Exclude the base global keys (llm_provider, llm_model_name,
		// llm_model_fallbacks). The check above ensures we matched
		// `prefix_X`, so suffix is non-empty.
		if suffix == "" {
			return false
		}
		return true
	}
	return false
}

// augmentLLMConfigWithStoredSecrets extends the incoming config_values slice
// with stored LLM secret rows that aren't in the payload — used at validation
// time so omit-to-keep edits don't fail RequiredWhen checks.
//
// Scenario: the UI knows an integration has llm_provider_api_key configured
// but doesn't have the value (backend redacts via admin_get_integrations_v2).
// On edit the user leaves the field blank → the UI omits it. The validator
// would then see cfg["llm_provider_api_key"] == "" and reject with
// "llm_provider_api_key is required when llm_provider is googleai".
//
// Fix: for an UPDATE on an LLM integration, look up the stored row for any
// secret-shaped name that's missing from the payload and synthesize it back
// into the validation-only slice. The actual save loop in
// CreateIntegrationConfig still skips upsert for these (preserving the
// stored ciphertext intact) — see the IsLLMSecretFieldName guard there.
//
// The augmented entries are passed to ValidateConfig as-is (ciphertext when
// is_encrypted=true on the row). LLM.ValidateConfig only checks non-emptiness
// of these fields, so ciphertext satisfies the contract without needing
// decryption.
//
// Returns the original slice unchanged when tenantId or integrationId is
// empty, or on any DB error — validation falls through with the un-augmented
// payload, which is the pre-fix behavior. A noisy DB-down here shouldn't
// block legitimate edits.
//
// Tenant scoping: the SELECT joins the integrations table with a tenant_id
// filter so a caller can't smuggle in an integrationId from a different
// tenant and pull that tenant's secret rows into the validation set. The
// upstream CreateIntegrationConfig caller already validates ownership of the
// row when isUpdate=true, but explicit scoping here is defense-in-depth.
func augmentLLMConfigWithStoredSecrets(ctx context.Context, tenantId, integrationId string, incoming []IntegrationConfigValue) []IntegrationConfigValue {
	if tenantId == "" || integrationId == "" {
		return incoming
	}
	// Build the "already present" set so we don't override values the caller
	// explicitly typed. Empty-value secret entries (omit-to-keep signals from
	// the UI) deliberately do NOT count as present — without this filter, a
	// caller sending {name: llm_provider_api_key, value: ""} would shadow the
	// stored row and the augmentation wouldn't fire, causing RequiredWhen
	// validators to spuriously reject the save. Non-secret entries with
	// empty value still count as "present" because for them empty means
	// "DELETE this row" (see isLLMOverrideKeyForDelete), and we must not
	// resurrect them from the stored row.
	incomingNames := make(map[string]struct{}, len(incoming))
	for _, v := range incoming {
		if v.Value == "" && IsLLMSecretFieldName(v.Name) {
			continue
		}
		incomingNames[v.Name] = struct{}{}
	}
	stored, err := loadStoredConfigValues(ctx, tenantId, integrationId)
	if err != nil {
		return incoming
	}

	augmented := make([]IntegrationConfigValue, 0, len(incoming))
	augmented = append(augmented, incoming...)
	for _, row := range stored {
		if !IsLLMSecretFieldName(row.Name) {
			continue
		}
		if _, present := incomingNames[row.Name]; present {
			continue
		}
		if row.Value == "" {
			continue
		}
		augmented = append(augmented, IntegrationConfigValue{
			Name:        row.Name,
			Value:       storedValuePlaintext(row.Value),
			IsEncrypted: false,
		})
	}
	return augmented
}

// loadStoredConfigValues returns an integration's stored config rows as they sit in
// the database (values still encrypted where they were saved encrypted). The join on
// integrations.tenant_id means an integration id from another tenant yields no rows.
func loadStoredConfigValues(ctx context.Context, tenantId, integrationId string) ([]IntegrationConfigValue, error) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, err
	}
	// QueryxContext (not Queryx) so the SELECT is cancelled if the request
	// is cancelled or times out mid-validation — saves us from holding a
	// row-lock or a network round-trip on behalf of a client that's already
	// gone.
	rows, err := dbms.Db.QueryxContext(
		ctx,
		// COALESCE: both columns are nullable, and one NULL must not fail the scan
		// for the whole integration.
		`SELECT icv.name::text, COALESCE(icv.value, '')::text, COALESCE(icv.is_encrypted, false)
		   FROM integration_config_values icv
		   JOIN integrations i ON i.id = icv.integration_id
		  WHERE icv.integration_id = $1
		    AND i.tenant_id = $2`,
		integrationId,
		tenantId,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var stored []IntegrationConfigValue
	for rows.Next() {
		var row IntegrationConfigValue
		if err := rows.Scan(&row.Name, &row.Value, &row.IsEncrypted); err != nil {
			return nil, err
		}
		stored = append(stored, row)
	}
	// Surface iteration errors — a partial result shouldn't silently slip
	// through.
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return stored, nil
}

// storedValuePlaintext decrypts a stored secret value, trying regardless of the
// row's is_encrypted flag. The write path's ON CONFLICT DO UPDATE historically did
// not refresh is_encrypted (see integration_config.go upsert), so legacy rows can
// carry ciphertext under is_encrypted=false. common.Decrypt is hex-decode +
// AES-GCM, so a plaintext credential (e.g. "AIza…", "sk-…") errors out cleanly
// rather than producing garbage — making this safe to attempt unconditionally.
func storedValuePlaintext(value string) string {
	if dec, err := common.Decrypt(value); err == nil {
		return dec
	}
	return value
}

// ResolveKeptSecrets replaces each value the caller flagged is_encrypted with the
// secret stored for that field on integrationId, decrypted. It is the only way
// request-supplied config values may carry is_encrypted into the server.
//
// The integration form shows saved secrets as a mask, and when the user leaves one
// untouched it sends the stored ciphertext back with is_encrypted=true, meaning
// "keep what is saved". The server used to decrypt that ciphertext, which made
// every endpoint taking config values a decryption oracle: one global key and no
// binding to tenant or integration, so any ciphertext produced under that key could
// be sent in and read back — echoed in the response, quoted in a validation error,
// or sent to a url the caller controls (#39639). Here the client's ciphertext is
// only a marker and is never decrypted; the secret comes from the caller's own
// tenant's stored row, looked up by id.
//
// No returned value has IsEncrypted set, so downstream code never sees a
// request-sourced ciphertext. Values that are not flagged pass through unchanged,
// and a request that flags nothing costs no query.
func ResolveKeptSecrets(ctx *security.RequestContext, integrationId string, values []IntegrationConfigValue) ([]IntegrationConfigValue, error) {
	kept := false
	for _, v := range values {
		if v.IsEncrypted {
			kept = true
			break
		}
	}
	if !kept {
		return values, nil
	}

	tenantId := ""
	if ctx != nil && ctx.GetSecurityContext() != nil {
		tenantId = ctx.GetSecurityContext().GetTenantId()
	}
	if integrationId == "" || tenantId == "" {
		return nil, errors.New("integrations: is_encrypted is only accepted when editing a saved integration")
	}
	stored, err := loadStoredConfigValues(ctx.GetContext(), tenantId, integrationId)
	if err != nil {
		return nil, fmt.Errorf("integrations: failed to load stored config values: %w", err)
	}
	storedByName := make(map[string]string, len(stored))
	for _, row := range stored {
		storedByName[row.Name] = row.Value
	}

	resolved := make([]IntegrationConfigValue, len(values))
	for i, v := range values {
		if !v.IsEncrypted {
			resolved[i] = v
			continue
		}
		storedValue := storedByName[v.Name]
		if storedValue == "" {
			return nil, fmt.Errorf("integrations: field %s has no saved value to keep; enter it again", v.Name)
		}
		resolved[i] = IntegrationConfigValue{Name: v.Name, Value: storedValuePlaintext(storedValue)}
	}
	return resolved, nil
}

// isSecretConfigField reports whether a field's value must be encrypted at rest and
// never returned to a client: the schema's IsEncrypted flag, plus the LLM
// integration's dynamic per-agent / per-tier secret keys, which AllowsDynamicKey lets
// through without a static schema entry.
func isSecretConfigField(integrationType string, schema IntegrationSchema, name string) bool {
	if schema.Properties[name].IsEncrypted {
		return true
	}
	return integrationType == "llm" && IsLLMSecretFieldName(name)
}

// redactSecretConfigValues returns a copy of values with every secret field's value
// blanked, for responses. Entries are kept, not dropped: the integration forms read
// `configs.length === 0` as a failed save. Non-secret values the forms do read — the
// generated webhook token, vm_agent access_key / access_secret — pass through.
func redactSecretConfigValues(integrationType string, schema IntegrationSchema, values []IntegrationConfigValue) []IntegrationConfigValue {
	redacted := make([]IntegrationConfigValue, len(values))
	for i, v := range values {
		redacted[i] = v
		if v.Value != "" && isSecretConfigField(integrationType, schema, v.Name) {
			redacted[i] = IntegrationConfigValue{Name: v.Name}
		}
	}
	return redacted
}
