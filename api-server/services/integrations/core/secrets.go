package core

import (
	"context"
	"strings"

	"nudgebee/services/common"
	"nudgebee/services/internal/database"
)

// llmSecretFieldPrefixes is the canonical list of secret-shaped config-key
// prefixes for the LLM integration. Anything starting with one of these is a
// credential and must never round-trip to clients, must be encrypted at rest
// (where the schema declares IsEncrypted=true), and must be preserved on
// omit-to-keep semantics during save.
//
// The prefix shape covers BOTH the bare global key AND every per-tier /
// per-agent override variant — e.g. llm_provider_api_key,
// llm_provider_api_key_summary_agent, llm_provider_api_key_<agent>,
// llm_provider_access_key_<agent>, etc. Matches the resolver's read paths
// in llm-server (see llm/llm-server/agents/core/llm_config.go).
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
	// Tier overrides.
	if strings.HasPrefix(name, "llm_tier_") {
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
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
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
	// QueryxContext (not Queryx) so the SELECT is cancelled if the request
	// is cancelled or times out mid-validation — saves us from holding a
	// row-lock or a network round-trip on behalf of a client that's already
	// gone.
	rows, err := dbms.Db.QueryxContext(
		ctx,
		`SELECT icv.name::text, icv.value::text, icv.is_encrypted
		   FROM integration_config_values icv
		   JOIN integrations i ON i.id = icv.integration_id
		  WHERE icv.integration_id = $1
		    AND i.tenant_id = $2`,
		integrationId,
		tenantId,
	)
	if err != nil {
		return incoming
	}
	defer func() { _ = rows.Close() }()

	augmented := make([]IntegrationConfigValue, 0, len(incoming))
	augmented = append(augmented, incoming...)
	for rows.Next() {
		var name, value string
		var isEncrypted bool
		if err := rows.Scan(&name, &value, &isEncrypted); err != nil {
			continue
		}
		if !IsLLMSecretFieldName(name) {
			continue
		}
		if _, present := incomingNames[name]; present {
			continue
		}
		if value == "" {
			continue
		}
		// Try-decrypt regardless of the is_encrypted flag. The write path's
		// ON CONFLICT DO UPDATE historically did not refresh is_encrypted
		// (see integration_config.go upsert), so legacy rows can carry
		// ciphertext under is_encrypted=false. common.Decrypt is hex-decode +
		// AES-GCM, so a plaintext credential (e.g. "AIza…", "sk-…") errors
		// out cleanly rather than producing garbage — making this safe to
		// attempt unconditionally for secret-shaped fields.
		if dec, derr := common.Decrypt(value); derr == nil {
			value = dec
		}
		augmented = append(augmented, IntegrationConfigValue{
			Name:        name,
			Value:       value,
			IsEncrypted: false,
		})
	}
	// Surface iteration errors — a partial result shouldn't silently slip
	// through. On error, fall back to the un-augmented payload (same policy
	// as the DB-down branches above).
	if err := rows.Err(); err != nil {
		return incoming
	}
	return augmented
}
