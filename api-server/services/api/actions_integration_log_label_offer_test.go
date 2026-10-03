package api

import (
	"testing"

	"nudgebee/services/integrations/core"

	"github.com/stretchr/testify/assert"
)

func logLabelOfferSchema() core.IntegrationSchema {
	return core.IntegrationSchema{Properties: map[string]core.IntegrationSchemaProperty{
		"url":                           {},
		core.LogLabelMappingsConfigName: {},
	}}
}

// The OSS-only log providers must keep the Log Label Mapping editor; a trace-only
// provider must lose it without mutating the shared schema map.
func TestWithLogLabelMappingOffer(t *testing.T) {
	for _, provider := range []string{"cubeapm", "splunk_enterprise", "loki"} {
		got := withLogLabelMappingOffer(logLabelOfferSchema(), provider, "")
		assert.Contains(t, got.Properties, core.LogLabelMappingsConfigName, provider)
	}

	in := logLabelOfferSchema()
	got := withLogLabelMappingOffer(in, "jaeger", "user")
	assert.NotContains(t, got.Properties, core.LogLabelMappingsConfigName)
	assert.Contains(t, got.Properties, "url")
	assert.Contains(t, in.Properties, core.LogLabelMappingsConfigName, "input schema must not be mutated")
}
