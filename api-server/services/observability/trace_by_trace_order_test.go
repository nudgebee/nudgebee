package observability

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"nudgebee/services/query"
)

// TestByTraceNaturalOrderOnlyMatchesTheReader is the forcing function for the
// by_trace_natural_order_only capability (#41495).
//
// Declaring the constraint next to the code that enforces it is not enough: the
// declaration is a constant and the enforcement is a branch, and nothing but this test
// connects them. The drift that matters is silent in the worst direction — teach the
// reader a second pass that honours a duration order and leave the declaration alone, and
// the UI goes on hiding a sort that now works, raising no error and no 400 for anyone to
// notice. So drive the REAL builder and compare it with what the capability claims.
func TestByTraceNaturalOrderOnlyMatchesTheReader(t *testing.T) {
	src := &SplunkEnterpriseTraceSource{}
	declared := byTraceNaturalOrderOnlyReason(src) != ""
	require.True(t, declared, "Splunk Enterprise must declare the constraint its By Traces builder enforces")

	t.Run("a declared constraint means the builder really refuses", func(t *testing.T) {
		for _, ob := range []query.QueryOrderBy{
			{Column: "duration_ns", Order: query.Desc},
			{Column: "duration_ns", Order: query.Asc},
			{Column: "timestamp", Order: query.Asc},
		} {
			_, err := buildSplunkEnterpriseTraceByTraceSPL(traceReq(t, "", 10, 0, ob), "otel_traces")
			require.Error(t, err, "order %v is declared unsupported but the builder accepted it", ob)
			// The client is told one sentence and a caller that asked anyway reads the
			// same one; an error that no longer carries it means the two have parted.
			assert.Contains(t, err.Error(), byTraceNaturalOrderOnlyReason(src), "order %v", ob)
		}
	})

	t.Run("the natural order is still served", func(t *testing.T) {
		// The other half of the claim: "newest first only" must mean newest first WORKS,
		// or the capability is hiding a broken view rather than an unsupported order.
		_, err := buildSplunkEnterpriseTraceByTraceSPL(traceReq(t, "", 10, 0), "otel_traces")
		require.NoError(t, err)
		_, err = buildSplunkEnterpriseTraceByTraceSPL(
			traceReq(t, "", 10, 0, query.QueryOrderBy{Column: "timestamp", Order: query.Desc}), "otel_traces")
		require.NoError(t, err)
	})

	t.Run("no other reader declares it", func(t *testing.T) {
		// The reverse direction. Every other reader reaches By Traces through
		// queryRootSpansViaSpans, which ranks no traces at all and so refuses no order.
		// A new reader appearing here is either genuinely trace-ranked, and
		// belongs in the list below with its own builder test, or it has copied the
		// interface by mistake.
		for _, p := range traceProviders {
			if p == "splunk_enterprise" {
				continue
			}
			for _, integrationSource := range []string{"user", "agent"} {
				source, err := getTraceSource(p, integrationSource)
				if err != nil {
					continue
				}
				assert.Empty(t, byTraceNaturalOrderOnlyReason(source),
					"%s/%s declares the By Traces order constraint; add a builder test like the one above", p, integrationSource)
			}
		}
	})
}
