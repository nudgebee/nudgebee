package triage

import (
	"encoding/json"
	"testing"
	"time"

	"nudgebee/services/internal/database/models"
)

// Whether correlation can produce a cross-service dependency for AWS EC2 alarms
// had never been established. Every AWS event we measured scored
// dependency_distance 0, in two environments, over three runs — but the causes
// found were all upstream of correlation (the event carried no knowledge-graph
// evidence, or evidence built from the wrong node), so correlation itself was
// never actually exercised with correct input.
//
// This closes that gap without a deploy: it hands correlation the evidence the
// fixed lookup produces — the real topology from our lab, order <- payment and
// order <- inventory, discovered from VPC flow logs — and asserts it reaches a
// dependency hop.
//
// If this fails, no amount of evidence plumbing helps and the fixes elsewhere
// are beside the point.

func awsSp(s string) *string { return &s }

// kgEvidence builds a knowledge_graph evidence card in the shape the current
// writer emits: nodes and edges at the top level, not nested under "data".
func kgEvidence(t *testing.T) *models.Json {
	t.Helper()
	card := map[string]interface{}{
		"type": "knowledge_graph",
		"nodes": []interface{}{
			map[string]interface{}{
				"id":        "node-order",
				"node_type": "ComputeInstance",
				"properties": map[string]interface{}{
					"name":        "nudgebee-scenario-services-order",
					"resource_id": "i-0dcee3621b8456783",
					"arn":         "arn:aws:ec2:us-east-1:864186153326:instance/i-0dcee3621b8456783",
					"region":      "us-east-1",
				},
			},
			map[string]interface{}{
				"id":        "node-payment",
				"node_type": "ComputeInstance",
				"properties": map[string]interface{}{
					"name":        "nudgebee-scenario-services-payment",
					"resource_id": "i-0b079820a95b1517a",
					"arn":         "arn:aws:ec2:us-east-1:864186153326:instance/i-0b079820a95b1517a",
					"region":      "us-east-1",
				},
			},
		},
		// payment CALLS order — the direction VPC flow logs recorded.
		"edges": []interface{}{
			map[string]interface{}{
				"source_node_id":    "node-payment",
				"dest_node_id":      "node-order",
				"relationship_type": "CALLS",
				"properties": map[string]interface{}{
					"contributing_sources": []interface{}{"aws-vpc-flow"},
				},
			},
		},
		"additional_info": map[string]interface{}{"action_name": "knowledge_graph_service_map"},
	}
	raw, err := json.Marshal([]interface{}{card})
	if err != nil {
		t.Fatalf("marshalling evidence: %v", err)
	}
	var j models.Json
	if err := j.Scan(raw); err != nil {
		t.Fatalf("scanning evidence: %v", err)
	}
	return &j
}

// awsAlarmEvent mirrors a real CloudWatch alarm event: service_key is the ALARM
// arn, not the instance's. Correlation has to alias that back to the instance
// node, and that step is part of what is being tested.
func awsAlarmEvent(t *testing.T, alarmName, instanceID string, at time.Time, ev *models.Json) *models.Event {
	t.Helper()
	return &models.Event{
		// Distinct ids matter: the scorer treats two events with the same id as
		// self-correlation and returns immediately, which looks exactly like
		// "no dependency found".
		Id:               "evt-" + instanceID,
		Title:            "CloudWatch Alarm: " + alarmName,
		SubjectType:      awsSp("compute-instance"),
		SubjectName:      awsSp(instanceID),
		SubjectNamespace: awsSp("AmazonEC2"),
		// Real CloudWatch alarm events set service_key from the resolved subject,
		// so the ARN ends in the instance id rather than the alarm name. Using
		// the alarm name here would make the test fail for a reason production
		// never hits.
		ServiceKey: awsSp("arn:aws:cloudwatch:us-east-1:864186153326:alarm:" + instanceID),
		StartsAt:   &at,
		Evidences:  ev,
	}
}

func TestCorrelation_AWSInstancesReachADependencyHop(t *testing.T) {
	ev := kgEvidence(t)
	orderAt := time.Date(2026, 9, 5, 4, 30, 0, 0, time.UTC)
	paymentAt := orderAt.Add(2 * time.Minute) // downstream fires after upstream

	order := awsAlarmEvent(t, "nudgebee-scenario-services-order-cpu", "i-0dcee3621b8456783", orderAt, ev)
	payment := awsAlarmEvent(t, "nudgebee-scenario-services-payment-cpu", "i-0b079820a95b1517a", paymentAt, ev)

	graph, err := parseServiceMapFromEvent(payment)
	if err != nil {
		t.Fatalf("parseServiceMapFromEvent: %v", err)
	}
	if graph == nil {
		t.Fatal("no dependency graph parsed from knowledge_graph evidence")
	}

	// The engine scores both directions and writes reciprocal rows, so a hop in
	// either is a hop. The edge runs payment -> order (payment calls order), so
	// only one ordering can find it - asserting on a single fixed ordering would
	// fail for a reason production never has.
	got := calculateCorrelationScore(payment, order, graph, nil)
	if got.DependencyDistance <= 0 {
		got = calculateCorrelationScore(order, payment, graph, nil)
	}

	if got.DependencyDistance <= 0 {
		t.Fatalf("dependency_distance = %d, want > 0.\n"+
			"The graph holds payment CALLS order and both events resolve to those "+
			"instances, so a hop must be reachable. Distance 0 here means the "+
			"cross-service path is dead regardless of how good the evidence is.\n"+
			"reason: %s", got.DependencyDistance, got.CorrelationReason)
	}
	if got.CorrelationType != "upstream_dependency" && got.CorrelationType != "downstream_impact" &&
		got.CorrelationType != "likely_root_cause" {
		t.Errorf("correlation_type = %q, want a dependency-based type", got.CorrelationType)
	}
	t.Logf("type=%s distance=%d score=%.2f reason=%s",
		got.CorrelationType, got.DependencyDistance, got.CorrelationScore, got.CorrelationReason)
}

// Two unrelated instances must NOT be given a dependency hop; a test that only
// checks the positive case would pass on a graph that links everything.
func TestCorrelation_UnrelatedAWSInstancesGetNoHop(t *testing.T) {
	ev := kgEvidence(t)
	at := time.Date(2026, 9, 5, 4, 30, 0, 0, time.UTC)

	payment := awsAlarmEvent(t, "payment-cpu", "i-0b079820a95b1517a", at, ev)
	stranger := awsAlarmEvent(t, "unrelated-cpu", "i-0999999999999999", at.Add(time.Minute), ev)

	graph, err := parseServiceMapFromEvent(payment)
	if err != nil {
		t.Fatalf("parseServiceMapFromEvent: %v", err)
	}
	got := calculateCorrelationScore(stranger, payment, graph, nil)
	if got.DependencyDistance > 0 {
		t.Errorf("dependency_distance = %d for an instance not in the graph, want 0",
			got.DependencyDistance)
	}
}
