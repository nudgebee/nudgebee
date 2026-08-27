package observability

// ProviderRef for every log source, kept together so this table can be read against the
// getLogSource switch it mirrors.
//
// Each source is dispatched for exactly one (provider, source) pair and reports that
// pair back. The mapping has to live somewhere the label-mapping resolver can reach it:
// the integration-level tier is stored on a specific integrations row, and one account
// can carry several log integrations at once, so "which integration" is not recoverable
// from the source type alone at the call site — every source is a stateless empty struct.
//
// These constants duplicate what the switch already encodes, which is why
// TestProviderRef_MatchesGetLogSource asserts the two agree for every pair, and
// TestProviderRef_IsOneToOne asserts no type claims two.

func (s *LokiSource) ProviderRef() providerRef {
	return providerRef{Provider: "loki", Source: "agent"}
}

func (s *SignozSource) ProviderRef() providerRef {
	return providerRef{Provider: "signoz", Source: "agent"}
}

func (s *SignozSaasSource) ProviderRef() providerRef {
	return providerRef{Provider: "signoz", Source: "user"}
}

func (s *DatadogSource) ProviderRef() providerRef {
	return providerRef{Provider: "datadog", Source: "user"}
}

func (s *ObserveSource) ProviderRef() providerRef {
	return providerRef{Provider: "observe", Source: "user"}
}

func (s *LogglySource) ProviderRef() providerRef {
	return providerRef{Provider: "loggly", Source: "user"}
}

func (s *AzureAppInsightsSource) ProviderRef() providerRef {
	return providerRef{Provider: "azure_app_insights", Source: "user"}
}

func (s *cloudLogs) ProviderRef() providerRef {
	return providerRef{Provider: "aws_cloudwatch", Source: "user"}
}

func (e *ElasticSource) ProviderRef() providerRef {
	return providerRef{Provider: "ES", Source: "agent"}
}

func (e *ElasticSaasSource) ProviderRef() providerRef {
	return providerRef{Provider: "ES", Source: "user"}
}

func (s *NewRelicLogSource) ProviderRef() providerRef {
	return providerRef{Provider: "newrelic", Source: "user"}
}

func (s *SplunkLogSource) ProviderRef() providerRef {
	return providerRef{Provider: "splunk_observability_platform", Source: "user"}
}

func (s *DynatraceLogSource) ProviderRef() providerRef {
	return providerRef{Provider: "dynatrace", Source: "user"}
}

func (s *SolarWindsLogSource) ProviderRef() providerRef {
	return providerRef{Provider: "solarwinds", Source: "user"}
}

func (p *PinotSource) ProviderRef() providerRef {
	return providerRef{Provider: "pinot", Source: "agent"}
}

func (p *PinotSaasSource) ProviderRef() providerRef {
	return providerRef{Provider: "pinot", Source: "user"}
}

func (h *HiveSaasSource) ProviderRef() providerRef {
	return providerRef{Provider: "hive", Source: "user"}
}

func (s *OpenObserveLogSource) ProviderRef() providerRef {
	return providerRef{Provider: "openobserve", Source: "user"}
}

// HiveSource is implemented but deliberately absent from getLogSource — the matching
// hive_query / hive_schema agent actions do not exist yet (see the switch's comment). It
// still has to satisfy LogSource, and names the pair it will be dispatched for.
func (h *HiveSource) ProviderRef() providerRef {
	return providerRef{Provider: "hive", Source: "agent"}
}
