package query

import (
	"testing"

	"nudgebee/services/account"
	"nudgebee/services/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The collector's clickhouse exporter writes the instrumentation scope to the ScopeName
// column, not into SpanAttributes — deriving trace_source from
// SpanAttributes['otel.scope.name'] alone labelled every eBPF span 'otel', so the
// trace_source='ebpf' filter matched nothing.
func TestTraceSourceExprUsesScopeNameForAgentTable(t *testing.T) {
	expr := traceSourceExpr(account.AgentTraceTableConfigKey)

	assert.Contains(t, expr, "ScopeName = 'nudgebee-node-agent'")
	assert.Contains(t, expr, "SpanAttributes['otel.scope.name'] = 'nudgebee-node-agent'", "pre-ScopeName exporter rows must still resolve")
	assert.Contains(t, expr, "'ebpf'")
}

// Third-party ClickHouse stores (Last9's otel.traces) are read with the same base query but
// are not guaranteed to expose ScopeName; referencing it there would fail every trace query.
func TestTraceSourceExprKeepsAttributeFormForForeignTable(t *testing.T) {
	expr := traceSourceExpr("otel.traces")

	assert.NotContains(t, expr, "ScopeName")
	assert.Contains(t, expr, "SpanAttributes['otel.scope.name'] = 'nudgebee-node-agent'")
}

// node-agent emits the destination zone under a misspelled key ("availablity"), and the agent
// builds its materialized column from that spelling. #37036 corrected the key on the read side
// only, so the fallback query read a key no span carries and the zone came back empty (#40498).
func TestDestinationZoneExprReadsTheKeyNodeAgentEmits(t *testing.T) {
	assert.Contains(t, DestinationZoneExpr, "WHEN mapContains(SpanAttributes, 'destination.cloud.availablity_zone') THEN SpanAttributes['destination.cloud.availablity_zone']")
	assert.Contains(t, DestinationZoneExpr, "ELSE SpanAttributes['destination.cloud.availability_zone']", "an agent that fixes the key must still resolve")
}

// With no agent on record the grouping query takes the non-materialized path, which has to
// derive destination_workload_zone from the span itself.
func TestTracesGroupingsFallbackQueryDerivesDestinationZone(t *testing.T) {
	def, _, err := table_metadata["traces_groupings_v2"].DefGenerator(security.NewRequestContextForSuperAdmin(nil, nil, nil), "no-such-account", QueryRequest{})

	require.NoError(t, err)
	assert.Contains(t, def, DestinationZoneExpr+" AS destination_workload_zone")
}
