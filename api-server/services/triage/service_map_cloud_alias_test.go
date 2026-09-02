package triage

import (
	"encoding/json"
	"testing"

	"nudgebee/services/internal/database/models"
)

// realCloudServiceMapEvidence is the verbatim cloud_service_map evidence stored
// for event c52bc8b8-f7ba-4900-a487-63ac3e927d20 (CloudWatch alarm
// nb-demo-alb-target-5xx), trimmed to the nodes that matter here.
const realCloudServiceMapEvidence = `{"data":[
 {"Id":{"name":"arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/app/nb-demo-alb/3c4737067cb64e8e","kind":"elb","namespace":"us-east-1"},
  "Status":"active","Upstreams":[],
  "Downstreams":[{"Id":{"kind":"ec2","name":"i-0f568ef22d52139bb","namespace":"us-east-1"}}]},
 {"Id":{"name":"i-0f568ef22d52139bb","kind":"ec2","namespace":"us-east-1"},"Status":"active","Upstreams":[],"Downstreams":[]}
]}`

func graphFromRealEvidence(t *testing.T) *DependencyGraph {
	t.Helper()
	var payload struct {
		Data []ServiceNode `json:"data"`
	}
	if err := json.Unmarshal([]byte(realCloudServiceMapEvidence), &payload); err != nil {
		t.Fatalf("unmarshal evidence: %v", err)
	}
	if len(payload.Data) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(payload.Data))
	}
	return buildDependencyGraph(payload.Data)
}

// TestResolveKeyMatchesEventARNAgainstNodeARN is the regression for the defect
// that produced zero topology correlations on every AWS event: the event's
// service_key and the graph node's name are different ARN spellings of the same
// load balancer, so getDependencyDistance never resolved either end.
func TestResolveKeyMatchesEventARNAgainstNodeARN(t *testing.T) {
	graph := graphFromRealEvidence(t)

	const (
		// Exactly what events.service_key held for the alarm.
		eventKey = "arn:aws:elb:us-east-1:123456789012:loadbalancer:app/nb-demo-alb/3c4737067cb64e8e"
		// Exactly what the graph node is keyed as.
		nodeKey = "us-east-1:elb:arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/app/nb-demo-alb/3c4737067cb64e8e"
	)

	if got := graph.resolveKey(eventKey); got != nodeKey {
		t.Errorf("resolveKey(event ARN) = %q, want the load balancer node %q", got, nodeKey)
	}
}

// TestDependencyDistanceFromCloudEvent proves the fix reaches what actually
// matters: an alarm on the ALB and an alarm on the EC2 instance behind it are
// now one hop apart instead of unresolvable.
func TestDependencyDistanceFromCloudEvent(t *testing.T) {
	graph := graphFromRealEvidence(t)

	albEventKey := "arn:aws:elb:us-east-1:123456789012:loadbalancer:app/nb-demo-alb/3c4737067cb64e8e"
	ec2EventKey := "arn:aws:ec2:us-east-1:123456789012:ec2-instance:i-0f568ef22d52139bb"

	if got := graph.getDependencyDistance(albEventKey, ec2EventKey); got != 1 {
		t.Errorf("distance(ALB alarm, EC2 alarm) = %d, want 1", got)
	}
	if !graph.isUpstream(albEventKey, ec2EventKey) {
		t.Errorf("ALB should resolve as upstream of the instance it routes to")
	}
}

func TestCloudResourceIDFromARN(t *testing.T) {
	tests := []struct {
		arn  string
		want string
	}{
		{"arn:aws:elasticloadbalancing:us-east-1:1234:loadbalancer/app/lb/9f", "app/lb/9f"},
		{"arn:aws:elb:us-east-1:1234:loadbalancer:app/lb/9f", "app/lb/9f"},
		{"arn:aws:ec2:us-east-1:1234:instance/i-0abc", "i-0abc"},
		{"arn:aws:ec2:us-east-1:1234:ec2-instance:i-0abc", "i-0abc"},
		{"arn:aws:rds:us-east-1:1234:db:nb-demo-db", "nb-demo-db"},
		{"arn:aws:s3:::my-bucket", "my-bucket"},
		{"demo:Workload:flagd", ""},
		{"", ""},
		{"arn:aws:ec2", ""},
	}
	for _, tt := range tests {
		if got := cloudResourceIDFromARN(tt.arn); got != tt.want {
			t.Errorf("cloudResourceIDFromARN(%q) = %q, want %q", tt.arn, got, tt.want)
		}
	}
}

// TestK8sNodesAreNotAliasedUnqualified guards the blast radius of the change:
// a workload name is unique only inside its namespace, so registering it bare
// would let "flagd" in one namespace answer for "flagd" in another.
func TestK8sNodesAreNotAliasedUnqualified(t *testing.T) {
	nodes := []ServiceNode{
		makeNode("demo", "Workload", "flagd"),
		makeNode("other", "Workload", "flagd"),
	}
	graph := buildDependencyGraph(nodes)

	if canonical, ok := graph.nodeAliases["flagd"]; ok {
		t.Errorf("bare workload name registered as an alias for %q", canonical)
	}
	if got := graph.resolveKey("demo:Deployment:flagd"); got != "demo:Workload:flagd" {
		t.Errorf("existing K8s alias behaviour changed: got %q", got)
	}
}

// TestCloudAliasDoesNotShadowCanonicalNode ensures an alias can never displace a
// real node of the same key.
func TestCloudAliasDoesNotShadowCanonicalNode(t *testing.T) {
	nodes := []ServiceNode{makeNode("us-east-1", "ec2", "i-0abc")}
	graph := buildDependencyGraph(nodes)

	key := formatNodeKey("us-east-1", "ec2", "i-0abc")
	if got := graph.resolveKey(key); got != key {
		t.Errorf("canonical key %q resolved away to %q", key, got)
	}
	if got := graph.resolveKey("arn:aws:ec2:us-east-1:1234:instance/i-0abc"); got != key {
		t.Errorf("ARN form resolved to %q, want %q", got, key)
	}
}

func makeNode(namespace, kind, name string) ServiceNode {
	var n ServiceNode
	n.ID.Namespace = namespace
	n.ID.Kind = kind
	n.ID.Name = name
	return n
}

func TestEventWithoutServiceKeyStillUsesK8sFallback(t *testing.T) {
	namespace := "demo"
	owner := "flagd"
	kind := "Deployment"
	event := &models.Event{
		SubjectNamespace: &namespace,
		SubjectOwner:     &owner,
		SubjectOwnerKind: &kind,
	}
	if got := getServiceKeyFromEvent(event); got != "demo:Deployment:flagd" {
		t.Errorf("getServiceKeyFromEvent = %q, want demo:Deployment:flagd", got)
	}
}
