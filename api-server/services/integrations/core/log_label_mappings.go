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

// DefaultTraceFiltersConfigName is the per-account always-apply TRACE filter blob.
// Deliberately separate from the logs-side "default_filters": one integration record
// commonly serves both logs and traces (datadog, dynatrace, chronosphere, ES), so a
// shared key would apply log filters to trace queries. Read by the observability
// package (trace_default_filters.go).
const DefaultTraceFiltersConfigName = "default_trace_filters"

// TraceLabelMappingsConfigName is the TRACE counterpart of LogLabelMappingsConfigName:
// the per-account canonical -> provider trace field mapping an operator fills in on the
// integration's Advanced Settings form. Read by the observability package
// (trace_label_mappings.go).
//
// Deliberately separate from log_label_mappings for the same reason
// default_trace_filters is separate from default_filters: signoz, datadog, dynatrace,
// chronosphere and ES are each ONE integration record serving both logs and traces, so
// a shared key would apply an operator's log field mapping to their trace queries. The
// two vocabularies differ as well — a log mapping names log columns, a trace mapping
// names span/resource attributes on the same backend.
const TraceLabelMappingsConfigName = "trace_label_mappings"

// AccountLogLabelMappings is one per-account entry in LogLabelMappingsConfigName, and
// in TraceLabelMappingsConfigName — the two blobs are the same shape, so they share
// this type rather than carrying a near-duplicate that could drift.
//
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

// validateLabelMappings shape-checks the named label-mapping value, if present.
// Takes the config name so the log and trace blobs — identical in shape — share one
// implementation and cannot drift into two different contracts.
//
// Deliberately lenient, mirroring the index_account_mapping contract
// (integrations/elasticsearch.go): only the array shape and a non-empty accountId
// are enforced. An unknown account id or a blank canonical/provider name simply
// contributes nothing when the mapping is resolved, so rejecting them here would
// turn a harmless typo into a failed save.
func validateLabelMappings(values []IntegrationConfigValue, name string) error {
	raw := ""
	for _, v := range values {
		if strings.TrimSpace(v.Name) == name {
			raw = strings.TrimSpace(v.Value)
			break
		}
	}
	if raw == "" {
		return nil
	}

	var rows []AccountLogLabelMappings
	if err := json.Unmarshal([]byte(raw), &rows); err != nil {
		return fmt.Errorf("%s must be a JSON array: %w", name, err)
	}
	for i, r := range rows {
		if strings.TrimSpace(r.AccountId) == "" {
			return fmt.Errorf("%s[%d] is missing accountId", name, i)
		}
	}
	return nil
}

// injectSharedLogConfigProperties auto-allows the per-account JSON blobs every
// log / observability-platform integration accepts, without each ConfigSchema having
// to declare them: default_filters (always-apply log filters), default_trace_filters
// (the trace counterpart), log_label_mappings (canonical -> provider field overrides)
// and trace_label_mappings (the trace counterpart of those). All are consumed
// centrally in the observability package, so the provider definition has nothing to
// say about them.
//
// The two categories cover every trace provider too — getTraceSource serves only
// integrations that are IntegrationCategoryLog (otel_clickhouse, ES) or
// IntegrationCategoryObservabilityPlatform (datadog, dynatrace, jaeger,
// chronosphere, newrelic, splunk, solarwinds, openobserve, azure_app_insights) —
// so neither trace key needs a third category here.
//
// Note the categories are BROADER than the set of trace providers: loki, pinot and
// hive are IntegrationCategoryLog and get the trace keys injected too. That is
// harmless server-side (nothing ever writes them) but it is why the frontend gates
// its trace sections on an explicit provider set rather than on the presence of these
// properties — see TRACE_INTEGRATIONS in IntegrationDynamicFormModal.jsx.
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
		DefaultTraceFiltersConfigName: {
			Type:        ToolSchemaTypeString,
			Description: "JSON array of per-account always-apply trace filters",
			Default:     "",
			Hidden:      true,
		},
		LogLabelMappingsConfigName: {
			Type:        ToolSchemaTypeString,
			Description: "JSON array of per-account canonical to provider log field mappings",
			Default:     "",
			Hidden:      true,
		},
		TraceLabelMappingsConfigName: {
			Type:        ToolSchemaTypeString,
			Description: "JSON array of per-account canonical to provider trace field mappings",
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
