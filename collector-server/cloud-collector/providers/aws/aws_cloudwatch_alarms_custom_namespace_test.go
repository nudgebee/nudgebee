package aws

import (
	"context"
	"testing"
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
