package core

import (
	"encoding/json"
	"fmt"
	"strings"
)

// LogLabelMappingsConfigName is the integration_config_values entry holding the
// per-account canonical -> provider log field mapping an operator fills in on the
// log integration's Advanced Settings form.
//
// It is auto-allowed on every log / observability-platform integration (see
// CreateIntegrationConfig) rather than declared per ConfigSchema, so a new log
// provider gets the capability without touching its integration definition.
const LogLabelMappingsConfigName = "log_label_mappings"

// AccountLogLabelMappings is one per-account entry in LogLabelMappingsConfigName.
// Stored as a JSON array so one integration serving several cloud accounts can map
// each of them separately — the same shape default_filters uses.
//
// The `accountId` spelling (not `account_id`) matches the default_filters blob the
// same form already writes; index_account_mapping uses the snake_case spelling.
// Both are load-bearing wire formats — do not "normalise" either one.
type AccountLogLabelMappings struct {
	AccountId string            `json:"accountId"`
	Mappings  map[string]string `json:"mappings"`
}

// validateLogLabelMappings shape-checks the LogLabelMappingsConfigName value, if
// present. Deliberately lenient, mirroring the index_account_mapping contract
// (integrations/elasticsearch.go): only the array shape and a non-empty accountId
// are enforced. An unknown account id or a blank canonical/provider name simply
// contributes nothing when the mapping is resolved, so rejecting them here would
// turn a harmless typo into a failed save.
func validateLogLabelMappings(values []IntegrationConfigValue) error {
	raw := ""
	for _, v := range values {
		if strings.TrimSpace(v.Name) == LogLabelMappingsConfigName {
			raw = strings.TrimSpace(v.Value)
			break
		}
	}
	if raw == "" {
		return nil
	}

	var rows []AccountLogLabelMappings
	if err := json.Unmarshal([]byte(raw), &rows); err != nil {
		return fmt.Errorf("%s must be a JSON array: %w", LogLabelMappingsConfigName, err)
	}
	for i, r := range rows {
		if strings.TrimSpace(r.AccountId) == "" {
			return fmt.Errorf("%s[%d] is missing accountId", LogLabelMappingsConfigName, i)
		}
	}
	return nil
}

// injectSharedLogConfigProperties auto-allows the two per-account JSON blobs every
// log / observability-platform integration accepts, without each ConfigSchema having
// to declare them: default_filters (always-apply log filters) and
// log_label_mappings (canonical -> provider field overrides). Both are consumed
// centrally in the observability package, so the provider definition has nothing to
// say about them.
//
// This is load-bearing rather than a convenience. CreateIntegrationConfig rejects any
// config value whose name is absent from Properties, so a save carrying one of these
// keys fails with "not found in schema" until it is injected here — which is also why
// a new log provider gets both capabilities for free.
//
// Returns the schema with a CLONED Properties map: ConfigSchema() implementations
// commonly return a shared/static map, and mutating it in place would leak the
// injection into every other integration built from it.
func injectSharedLogConfigProperties(schema IntegrationSchema, category IntegrationCategory) IntegrationSchema {
	if category != IntegrationCategoryLog && category != IntegrationCategoryObservabilityPlatform {
		return schema
	}

	shared := map[string]IntegrationSchemaProperty{
		"default_filters": {
			Type:        ToolSchemaTypeString,
			Description: "JSON array of per-account always-apply log filters",
			Default:     "",
			Hidden:      true,
		},
		LogLabelMappingsConfigName: {
			Type:        ToolSchemaTypeString,
			Description: "JSON array of per-account canonical to provider log field mappings",
			Default:     "",
			Hidden:      true,
		},
	}

	cloned := make(map[string]IntegrationSchemaProperty, len(schema.Properties)+len(shared))
	for k, v := range schema.Properties {
		cloned[k] = v
	}
	for name, prop := range shared {
		// An integration that declares one of these itself keeps its own definition.
		if _, exists := cloned[name]; !exists {
			cloned[name] = prop
		}
	}
	schema.Properties = cloned
	return schema
}
