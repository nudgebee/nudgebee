package common

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// installManualReader wires the global OTel meter provider to a manual reader
// so tests can flush the SDK and inspect counter values. Returns the reader and
// a cleanup that restores the previous provider.
func installManualReader(t *testing.T) *sdkmetric.ManualReader {
	t.Helper()
	prev := otel.GetMeterProvider()
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	otel.SetMeterProvider(mp)
	t.Cleanup(func() {
		_ = mp.Shutdown(context.Background())
		otel.SetMeterProvider(prev)
	})
	InitMetrics()
	return reader
}

const (
	testTenantA   = "tenant-A"
	testTenantB   = "tenant-B"
	testAWSAcctA  = "111111111111"
	testAWSAcctB  = "222222222222"
	endpointColl  = "nb_services_kg_endpoint_collision"
	route53Unmatc = "nb_services_kg_route53_unmatched"
)

// counterSum reads the manual reader and returns the sum of data points for
// the named counter whose attributes match every (key, value) pair in want.
func counterSum(t *testing.T, reader *sdkmetric.ManualReader, metricName string, want map[string]string) int64 {
	t.Helper()
	rm := metricdata.ResourceMetrics{}
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("manual reader collect: %v", err)
	}
	for _, sm := range rm.ScopeMetrics {
		if total, found := sumForMetric(t, sm.Metrics, metricName, want); found {
			return total
		}
	}
	return 0
}

func sumForMetric(t *testing.T, metrics []metricdata.Metrics, metricName string, want map[string]string) (int64, bool) {
	t.Helper()
	for _, m := range metrics {
		if m.Name != metricName {
			continue
		}
		sum, ok := m.Data.(metricdata.Sum[int64])
		if !ok {
			t.Fatalf("metric %s is not Sum[int64]", metricName)
		}
		var total int64
		for _, dp := range sum.DataPoints {
			if attrsContain(dp.Attributes, want) {
				total += dp.Value
			}
		}
		return total, true
	}
	return 0, false
}

func attrsContain(have attribute.Set, want map[string]string) bool {
	for k, v := range want {
		got, ok := have.Value(attribute.Key(k))
		if !ok || got.AsString() != v {
			return false
		}
	}
	return true
}

func TestMetricsKGEndpointCollisionTicks(t *testing.T) {
	reader := installManualReader(t)

	MetricsKGEndpointCollision(context.Background(), testTenantA, "LoadBalancer")
	MetricsKGEndpointCollision(context.Background(), testTenantA, "LoadBalancer")
	MetricsKGEndpointCollision(context.Background(), testTenantA, "Database")

	gotLB := counterSum(t, reader, endpointColl,
		map[string]string{MetricKeyTenantID: testTenantA, MetricKeyNodeType: "LoadBalancer"})
	if gotLB != 2 {
		t.Errorf("LoadBalancer collisions: got %d, want 2", gotLB)
	}

	gotDB := counterSum(t, reader, endpointColl,
		map[string]string{MetricKeyTenantID: testTenantA, MetricKeyNodeType: "Database"})
	if gotDB != 1 {
		t.Errorf("Database collisions: got %d, want 1", gotDB)
	}
}

func TestMetricsKGRoute53UnmatchedTicks(t *testing.T) {
	reader := installManualReader(t)

	MetricsKGRoute53Unmatched(context.Background(), testTenantA, testAWSAcctA)
	MetricsKGRoute53Unmatched(context.Background(), testTenantA, testAWSAcctA)
	MetricsKGRoute53Unmatched(context.Background(), testTenantB, testAWSAcctB)

	got := counterSum(t, reader, route53Unmatc,
		map[string]string{MetricKeyTenantID: testTenantA, MetricKeyAccountID: testAWSAcctA})
	if got != 2 {
		t.Errorf("tenant-A unmatched: got %d, want 2", got)
	}

	got = counterSum(t, reader, route53Unmatc,
		map[string]string{MetricKeyTenantID: testTenantB, MetricKeyAccountID: testAWSAcctB})
	if got != 1 {
		t.Errorf("tenant-B unmatched: got %d, want 1", got)
	}
}
