package query

import (
	"testing"

	"nudgebee/services/account"

	"github.com/stretchr/testify/assert"
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
