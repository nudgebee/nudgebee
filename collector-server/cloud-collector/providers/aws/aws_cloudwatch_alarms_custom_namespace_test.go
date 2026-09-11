package aws

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
)

// A CloudWatch alarm on a custom namespace used to resolve to no cloud resource
// at all: ResourceType "alarm", ServiceName "CloudWatch". Both correlation and
// event analysis key off that resolution, so such an event was dropped by both,
// silently — it is accepted, stored, displayed, and never linked or analysed.
//
// Measured on one live account over a day: 22 events with subject_type "alarm"
// produced 0 correlations and 0 analyses, while 35 compute-instance events from
// the same instances produced 24 correlations and completed analyses.
//
// The namespace is unrecognised; the resource is not. An alarm dimensioned by
// InstanceId is about that EC2 instance whatever namespace it was published
// under, and application health is rarely a native AWS metric — so this is the
// ordinary case for anyone alarming on their own telemetry, not an edge case.
func TestEnrichCloudWatchAlarm_CustomNamespaceResolvesByDimension(t *testing.T) {
	got := EnrichCloudWatchAlarm(context.Background(), CloudWatchAlarmInfo{
		AlarmName:       "nudgebee-scenario-services-payment-down",
		MetricName:      "ServiceHealthy",
		MetricNamespace: "NudgebeeScenarioLab", // not an AWS namespace
		Dimensions: []AlarmDimension{
			{Name: "InstanceId", Value: "i-0b079820a95b1517a"},
		},
	}, false, nil, nil)

	if got.ResourceType != "compute-instance" {
		t.Errorf("ResourceType = %q, want compute-instance — as an alarm it is "+
			"excluded from correlation and analysis", got.ResourceType)
	}
	if got.ResourceId != "i-0b079820a95b1517a" {
		t.Errorf("ResourceId = %q, want the instance id", got.ResourceId)
	}
	if got.ResourceServiceName != ServiceNameEc2 {
		t.Errorf("ResourceServiceName = %q, want %q", got.ResourceServiceName, ServiceNameEc2)
	}
}

// A custom namespace whose dimensions identify nothing keeps the generic
// fallback. Guessing would be worse than admitting we cannot tell.
func TestEnrichCloudWatchAlarm_CustomNamespaceWithoutResourceDimension(t *testing.T) {
	got := EnrichCloudWatchAlarm(context.Background(), CloudWatchAlarmInfo{
		AlarmName:       "queue-depth-high",
		MetricName:      "Depth",
		MetricNamespace: "MyCompany/Queues",
		Dimensions: []AlarmDimension{
			{Name: "QueueLabel", Value: "orders"},
		},
	}, false, nil, nil)

	if got.ResourceType != "alarm" {
		t.Errorf("ResourceType = %q, want the generic alarm fallback", got.ResourceType)
	}
}

// A recognised namespace must be unaffected by the new inference path.
func TestEnrichCloudWatchAlarm_KnownNamespaceUnchanged(t *testing.T) {
	got := EnrichCloudWatchAlarm(context.Background(), CloudWatchAlarmInfo{
		AlarmName:       "cpu-high",
		MetricName:      "CPUUtilization",
		MetricNamespace: "AWS/EC2",
		Dimensions: []AlarmDimension{
			{Name: "InstanceId", Value: "i-0dcee3621b8456783"},
		},
	}, false, nil, nil)

	if got.ResourceType != "compute-instance" {
		t.Errorf("ResourceType = %q, want compute-instance", got.ResourceType)
	}
	if got.ResourceId != "i-0dcee3621b8456783" {
		t.Errorf("ResourceId = %q, want the instance id", got.ResourceId)
	}
}

// The reverse index must not invent a mapping for a dimension name that more
// than one namespace claims — picking arbitrarily would mistype the resource.
func TestResourceDimensionIndex_ExcludesAmbiguousDimensions(t *testing.T) {
	// Sharing a dimension is fine; disagreeing about what it identifies is not.
	// AWS/EC2 and CWAgent both key on InstanceId and both mean compute-instance,
	// so InstanceId must stay indexed - excluding it would drop the case this
	// whole path exists for.
	type kind struct{ service, resource string }
	kinds := map[string]map[kind]bool{}
	for _, ns := range cloudwatchNamespaceServiceMap {
		if ns.ResourceDimensionName == "" {
			continue
		}
		if kinds[ns.ResourceDimensionName] == nil {
			kinds[ns.ResourceDimensionName] = map[kind]bool{}
		}
		kinds[ns.ResourceDimensionName][kind{ns.ServiceName, ns.ResourceType}] = true
	}
	for dim, ks := range kinds {
		_, indexed := resourceDimensionIndex[dim]
		if len(ks) > 1 && indexed {
			t.Errorf("dimension %q maps to %d different resource kinds but is still indexed", dim, len(ks))
		}
		if len(ks) == 1 && !indexed {
			t.Errorf("dimension %q identifies one resource kind but is missing from the index", dim)
		}
	}
	if _, ok := resourceDimensionIndex["InstanceId"]; !ok {
		t.Error("InstanceId must be indexed - it is shared but not ambiguous")
	}
}

// Dimension order must not change the answer. Three structurally identical
// alarms carrying Service and InstanceId resolved two to the instance and one
// to the service name, purely because CloudWatch listed the dimensions in a
// different order. The one that lost pointed at no cloud resource, so it was
// dropped from correlation while its siblings were kept - and the alarm that
// lost was the parent of the cascade being diagnosed.
func TestResolveResourceFromDimensions_OrderIndependent(t *testing.T) {
	ns := CloudwatchNamespace{
		Name:                  "NudgebeeScenarioLab",
		ResourceDimensionName: "Resource", // matches neither dimension
		ServiceName:           "CloudWatch",
		ResourceType:          "alarm",
	}
	instanceFirst := []AlarmDimension{
		{Name: "InstanceId", Value: "i-0dcee3621b8456783"},
		{Name: "Service", Value: "order"},
	}
	serviceFirst := []AlarmDimension{
		{Name: "Service", Value: "order"},
		{Name: "InstanceId", Value: "i-0dcee3621b8456783"},
	}

	gotA, typeA := resolveResourceFromDimensions(instanceFirst, ns, "order-down")
	gotB, typeB := resolveResourceFromDimensions(serviceFirst, ns, "order-down")

	if gotA != gotB || typeA != typeB {
		t.Fatalf("dimension order changed the result: %s/%s vs %s/%s", gotA, typeA, gotB, typeB)
	}
	if gotA != "i-0dcee3621b8456783" {
		t.Errorf("resolved %q, want the instance id - the only dimension that names a resource", gotA)
	}
}

// With no resource-identifying dimension present, the existing behaviour stands.
func TestResolveResourceFromDimensions_FallsBackWhenNoResourceDimension(t *testing.T) {
	ns := CloudwatchNamespace{ResourceDimensionName: "Resource", ResourceType: "alarm"}
	got, gotType := resolveResourceFromDimensions([]AlarmDimension{
		{Name: "QueueLabel", Value: "orders"},
	}, ns, "queue-depth")
	if got != "orders" || gotType != "QueueLabel" {
		t.Errorf("got %q/%q, want orders/QueueLabel", got, gotType)
	}
}

// dim builds a CloudWatch metric dimension for the sibling-lookup tests.
func dim(name, value string) types.Dimension {
	return types.Dimension{Name: aws.String(name), Value: aws.String(value)}
}

// TestPickSiblingResource covers the rule that decides whether a custom-namespace
// alarm can borrow its resource from a sibling metric series. The alarm in every
// case is dimensioned "Service=nginx", which names no resource on its own.
func TestPickSiblingResource(t *testing.T) {
	alarmDims := []AlarmDimension{{Name: "Service", Value: "nginx"}}

	tests := []struct {
		name    string
		metrics []types.Metric
		wantOK  bool
		wantDim string
		wantVal string
	}{
		{
			name: "sibling series adds InstanceId",
			metrics: []types.Metric{
				{Dimensions: []types.Dimension{dim("Service", "nginx")}},
				{Dimensions: []types.Dimension{dim("Service", "nginx"), dim("InstanceId", "i-0422a52658b71572a")}},
			},
			wantOK: true, wantDim: "InstanceId", wantVal: "i-0422a52658b71572a",
		},
		{
			// The PixelPulse/AppHealth shape, measured 2026-09-11: every series
			// in the namespace is Service-only, so there is nothing to borrow.
			name: "no sibling carries a resource dimension",
			metrics: []types.Metric{
				{Dimensions: []types.Dimension{dim("Service", "nginx")}},
				{Dimensions: []types.Dimension{dim("Service", "order-service")}},
			},
			wantOK: false,
		},
		{
			// A load-balanced tier: refusing is the point. Picking either would
			// blame a machine that may be perfectly healthy.
			name: "two instances behind one service is ambiguous",
			metrics: []types.Metric{
				{Dimensions: []types.Dimension{dim("Service", "nginx"), dim("InstanceId", "i-aaa")}},
				{Dimensions: []types.Dimension{dim("Service", "nginx"), dim("InstanceId", "i-bbb")}},
			},
			wantOK: false,
		},
		{
			name: "series for a different service is ignored",
			metrics: []types.Metric{
				{Dimensions: []types.Dimension{dim("Service", "order-service"), dim("InstanceId", "i-other")}},
				{Dimensions: []types.Dimension{dim("Service", "nginx"), dim("InstanceId", "i-mine")}},
			},
			wantOK: true, wantDim: "InstanceId", wantVal: "i-mine",
		},
		{
			name: "extra dimension that names no resource is not enough",
			metrics: []types.Metric{
				{Dimensions: []types.Dimension{dim("Service", "nginx"), dim("Tier", "web")}},
			},
			wantOK: false,
		},
		{
			name:    "no metrics returned",
			metrics: nil,
			wantOK:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := pickSiblingResource(tt.metrics, alarmDims)
			if ok != tt.wantOK {
				t.Fatalf("resolved = %v, want %v (got %+v)", ok, tt.wantOK, got)
			}
			if !tt.wantOK {
				return
			}
			if got.dimensionName != tt.wantDim || got.value != tt.wantVal {
				t.Errorf("got %s=%s, want %s=%s", got.dimensionName, got.value, tt.wantDim, tt.wantVal)
			}
			if got.resourceType != "compute-instance" {
				t.Errorf("resourceType = %q, want compute-instance (from resourceDimensionIndex)", got.resourceType)
			}
		})
	}
}

// TestSiblingCacheKeyIsolatesAccountAndRegion — the cache behind the sibling
// lookup is a package-level global shared by every account and region this
// collector polls, and what it stores is "this alarm is about instance i-xxx".
// A custom namespace is a name the customer picked, so two tenants publishing
// ServiceHealth for Service=nginx under the same namespace is an ordinary
// collision. Keyed without the account, the second tenant would be handed the
// first tenant's instance.
func TestSiblingCacheKeyIsolatesAccountAndRegion(t *testing.T) {
	base := CloudWatchAlarmInfo{
		MetricNamespace: "PixelPulse/AppHealth",
		MetricName:      "ServiceHealth",
		Region:          "us-west-2",
		AccountNumber:   "111111111111",
		Dimensions:      []AlarmDimension{{Name: "Service", Value: "nginx"}},
	}

	otherAccount := base
	otherAccount.AccountNumber = "222222222222"
	otherRegion := base
	otherRegion.Region = "us-east-1"
	otherService := base
	otherService.Dimensions = []AlarmDimension{{Name: "Service", Value: "order-service"}}

	for _, tt := range []struct {
		name  string
		other CloudWatchAlarmInfo
	}{
		{"different account", otherAccount},
		{"different region", otherRegion},
		{"different dimension value", otherService},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if newSiblingCacheKey(base) == newSiblingCacheKey(tt.other) {
				t.Error("keys collided; one tenant's resolution would be served to another")
			}
		})
	}

	// Same inputs must still hit, and dimension order must not matter — the
	// whole point of the cache is that a repeatedly-firing alarm pays once.
	reordered := base
	reordered.Dimensions = []AlarmDimension{
		{Name: "Zone", Value: "a"}, {Name: "Service", Value: "nginx"},
	}
	withZone := base
	withZone.Dimensions = []AlarmDimension{
		{Name: "Service", Value: "nginx"}, {Name: "Zone", Value: "a"},
	}
	if newSiblingCacheKey(reordered) != newSiblingCacheKey(withZone) {
		t.Error("dimension order changed the key; the same alarm would miss its own cache entry")
	}
}

// TestSiblingCacheEviction — expiry on its own does not bound the map. An alarm
// that stops firing is never looked up again, so without a sweep its entry would
// sit in a long-running collector for the life of the process.
func TestSiblingCacheEviction(t *testing.T) {
	reset := func() {
		siblingResourceCacheMu.Lock()
		siblingResourceCache = map[siblingCacheKey]siblingCacheEntry{}
		siblingResourceCacheMu.Unlock()
	}
	key := func(n int) siblingCacheKey {
		return siblingCacheKey{accountNumber: "1", region: "us-west-2", namespace: "NS", metricName: fmt.Sprintf("m%d", n)}
	}
	size := func() int {
		siblingResourceCacheMu.RLock()
		defer siblingResourceCacheMu.RUnlock()
		return len(siblingResourceCache)
	}

	t.Run("a read drops the entry it finds expired", func(t *testing.T) {
		reset()
		siblingResourceCacheMu.Lock()
		siblingResourceCache[key(0)] = siblingCacheEntry{expiresAt: time.Now().Add(-time.Minute)}
		siblingResourceCacheMu.Unlock()

		if _, hit := getCachedSiblingResource(key(0)); hit {
			t.Fatal("expired entry reported as a hit")
		}
		if size() != 0 {
			t.Errorf("expired entry survived the read: size %d", size())
		}
	})

	t.Run("a write sweeps expired entries once the map is large", func(t *testing.T) {
		reset()
		siblingResourceCacheMu.Lock()
		stale := time.Now().Add(-time.Minute)
		for i := 0; i < siblingCacheSweepAt; i++ {
			siblingResourceCache[key(i)] = siblingCacheEntry{expiresAt: stale}
		}
		siblingResourceCacheMu.Unlock()

		setCachedSiblingResource(key(siblingCacheSweepAt), nil)
		if got := size(); got != 1 {
			t.Errorf("size %d after sweep, want 1 (the entry just written)", got)
		}
	})

	t.Run("a live entry is not swept", func(t *testing.T) {
		reset()
		setCachedSiblingResource(key(0), &siblingResource{dimensionName: "InstanceId", value: "i-live"})
		got, hit := getCachedSiblingResource(key(0))
		if !hit || got == nil || got.value != "i-live" {
			t.Errorf("live entry lost: hit=%v got=%+v", hit, got)
		}
	})

	t.Run("a negative result is cached and distinguishable from a miss", func(t *testing.T) {
		reset()
		setCachedSiblingResource(key(0), nil)
		got, hit := getCachedSiblingResource(key(0))
		if !hit {
			t.Error("negative result not cached; every firing would re-hit AWS")
		}
		if got != nil {
			t.Errorf("negative result came back as %+v", got)
		}
	})
	reset()
}
