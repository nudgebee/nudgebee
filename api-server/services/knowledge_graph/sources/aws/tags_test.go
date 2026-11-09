package aws

import (
	"encoding/json"
	"log/slog"
	"testing"

	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
)

func TestIsAWSUniqueIDTagKey(t *testing.T) {
	tests := []struct {
		name string
		key  string
		want bool
	}{
		// The AWS console tags an IAM user with its own access key IDs.
		{name: "access key id", key: "AKIAIOSFODNN7EXAMPLE", want: true},
		{name: "temporary access key id", key: "ASIAIOSFODNN7EXAMPLE", want: true},
		{name: "role id", key: "AROAEXAMPLEEXAMPLE12", want: true},
		// Tag names an engineer would actually set.
		{name: "ordinary name tag", key: "Name", want: false},
		{name: "ordinary environment tag", key: "environment", want: false},
		{name: "unknown prefix of the right shape", key: "BKIAIOSFODNN7EXAMPLE", want: false},
		{name: "too short", key: "AKIAIOSFODNN7EXAMPL", want: false},
		{name: "too long", key: "AKIAIOSFODNN7EXAMPLE1", want: false},
		{name: "lowercase is not an identifier", key: "akiaiosfodnn7example", want: false},
		{name: "right length but punctuated", key: "AKIA-EXAMPLE-TAG-KEY", want: false},
		{name: "empty", key: "", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isAWSUniqueIDTagKey(tt.key); got != tt.want {
				t.Errorf("isAWSUniqueIDTagKey(%q) = %v, want %v", tt.key, got, tt.want)
			}
		})
	}
}

func TestDropAWSUniqueIDTagKeys(t *testing.T) {
	tags := map[string]interface{}{
		"AKIAIOSFODNN7EXAMPLE": []interface{}{"[ci key]"},
		"ASIAIOSFODNN7EXAMPLE": []interface{}{"[temp key]"},
		"Name":                 []interface{}{"deploy-bot"},
		"owner":                []interface{}{"platform"},
	}
	dropAWSUniqueIDTagKeys(tags)

	if len(tags) != 2 {
		t.Fatalf("expected 2 tags left, got %d: %v", len(tags), tags)
	}
	for _, want := range []string{"Name", "owner"} {
		if _, ok := tags[want]; !ok {
			t.Errorf("ordinary tag %q was dropped", want)
		}
	}
}

// assertNoKeyIDLabels checks the literal fixture keys rather than reusing
// isAWSUniqueIDTagKey — an assertion built on the predicate under test passes
// even when that predicate is broken.
func assertNoKeyIDLabels(t *testing.T, labels map[string]string, wantKept, wantDropped []string) {
	t.Helper()
	for _, key := range wantKept {
		if _, ok := labels[key]; !ok {
			t.Errorf("ordinary tag %q was dropped from labels: %v", key, labels)
		}
	}
	for _, key := range wantDropped {
		if _, ok := labels[key]; ok {
			t.Errorf("AWS identifier %q was published as a label name", key)
		}
	}
	if len(labels) != len(wantKept) {
		t.Errorf("expected exactly %d labels %v, got %v", len(wantKept), wantKept, labels)
	}
}

// TestIAMUserNodeDropsAccessKeyIDLabels is the regression gate: an IAM
// user's access key IDs arrive as tags keyed by the access key ID, and must not
// reach node.Labels — the column kg_get_filter_options reads label_keys from,
// and the one the Node Details Labels table renders.
func TestIAMUserNodeDropsAccessKeyIDLabels(t *testing.T) {
	s := &AWSSource{logger: slog.Default()}
	req := &core.SourceBuildRequest{TenantID: "t1", CloudAccountID: "a1"}

	node := s.createServiceIdentityFromIAMUser(&sources.CloudResourceRow{
		ID:            "11111111-1111-1111-1111-111111111111",
		ResourceID:    "deploy-bot",
		Name:          "deploy-bot",
		Type:          "User",
		ServiceName:   "AWSIAM",
		CloudProvider: "AWS",
		Region:        "global",
		ARN:           "arn:aws:iam::123456789012:user/deploy-bot",
		AccountNumber: "123456789012",
		IsActive:      true,
		Tags: []byte(`{"AKIAIOSFODNN7EXAMPLE":["[ci key]"],` +
			`"ASIAIOSFODNN7EXAMPLE":["[temp key]"],"Name":["deploy-bot"]}`),
		Meta: []byte("{}"),
	}, req)

	if node == nil {
		t.Fatal("createServiceIdentityFromIAMUser returned nil")
	}
	assertNoKeyIDLabels(t, node.Labels,
		[]string{"Name"},
		[]string{"AKIAIOSFODNN7EXAMPLE", "ASIAIOSFODNN7EXAMPLE"})
}

// TestIAMRoleNodeDropsAccessKeyIDLabels covers the role path, which builds its
// labels from the IAM meta cache rather than from the tags column.
func TestIAMRoleNodeDropsAccessKeyIDLabels(t *testing.T) {
	s := &AWSSource{logger: slog.Default()}
	req := &core.SourceBuildRequest{TenantID: "t1", CloudAccountID: "a1"}

	// iamRole.Tags is an anonymous struct slice, so build the role the way
	// fetchIAMRolesFromAWS does — from the JSON shape AWS returns.
	var role iamRole
	if err := json.Unmarshal([]byte(`{
		"RoleName": "deploy-role",
		"RoleId": "AROAEXAMPLEEXAMPLE12",
		"Arn": "arn:aws:iam::123456789012:role/deploy-role",
		"Tags": [
			{"Key": "AKIAIOSFODNN7EXAMPLE", "Value": "[ci key]"},
			{"Key": "team", "Value": "platform"}
		]
	}`), &role); err != nil {
		t.Fatalf("failed to build role fixture: %v", err)
	}

	nodes := s.buildServiceIdentityNodes([]iamRole{role}, req)

	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	labels, ok := nodes[0].Properties["labels"].(map[string]string)
	if !ok {
		t.Fatalf("labels property is %T, want map[string]string", nodes[0].Properties["labels"])
	}
	if labels["team"] != "platform" {
		t.Errorf("ordinary tag team is missing from labels: %v", labels)
	}
	assertNoKeyIDLabels(t, labels, []string{"team"}, []string{"AKIAIOSFODNN7EXAMPLE"})
}

// TestOrdinaryResourceDropsAccessKeyIDLabels covers the shared path every
// non-IAM AWS resource takes, so a key-shaped tag anywhere in the inventory is
// filtered rather than only the IAM users AWS tags today.
func TestOrdinaryResourceDropsAccessKeyIDLabels(t *testing.T) {
	s := &AWSSource{logger: slog.Default()}
	req := &core.SourceBuildRequest{TenantID: "t1", CloudAccountID: "a1"}

	node := s.createNodeFromResource(&sources.CloudResourceRow{
		ID:            "22222222-2222-2222-2222-222222222222",
		ResourceID:    "vpc-123",
		Name:          "prod-vpc",
		Type:          "vpc",
		ServiceName:   "AmazonVPC",
		CloudProvider: "AWS",
		Region:        "us-east-1",
		AccountNumber: "123456789012",
		IsActive:      true,
		Tags:          []byte(`{"AKIAIOSFODNN7EXAMPLE":["[ci key]"],"Name":["prod-vpc"]}`),
		Meta:          []byte("{}"),
	}, req)

	if node == nil {
		t.Fatal("createNodeFromResource returned nil")
	}
	assertNoKeyIDLabels(t, node.Labels, []string{"Name"}, []string{"AKIAIOSFODNN7EXAMPLE"})
}

// TestRouteTableNodeDropsAccessKeyIDLabels covers the fourth tags-to-labels
// site: route tables never reach the classify path, so ensureRouteTableNodes
// builds their labels itself. No route table carries an access key ID today —
// this keeps the filter applied at every site that publishes a label name.
func TestRouteTableNodeDropsAccessKeyIDLabels(t *testing.T) {
	src, err := NewAWSSource(AWSSourceConfig{}, nil)
	if err != nil {
		t.Fatalf("NewAWSSource: %v", err)
	}

	meta, err := json.Marshal(map[string]interface{}{
		"RouteTableId": "rtb-1",
		"VpcId":        "vpc-1",
		"OwnerId":      "123456789012",
		"Tags": []map[string]string{
			{"Key": "AKIAIOSFODNN7EXAMPLE", "Value": "[ci key]"},
			{"Key": "Name", "Value": "public-rtb"},
		},
	})
	if err != nil {
		t.Fatalf("marshal meta: %v", err)
	}

	src.metaByType = map[string][]sources.CloudResourceRow{
		"route-table": {{
			ResourceID: "rtb-1",
			Type:       "route-table",
			Region:     "ap-south-1",
			Meta:       meta,
		}},
	}

	req := &core.SourceBuildRequest{TenantID: "t1", CloudAccountID: "a1"}
	nodes, _ := src.ensureRouteTableNodes(nil, nil, nil, sources.NewNodeLookup(nil), req)
	if len(nodes) != 1 {
		t.Fatalf("expected 1 route table node, got %d", len(nodes))
	}

	assertNoKeyIDLabels(t, nodes[0].Labels, []string{"Name"}, []string{"AKIAIOSFODNN7EXAMPLE"})
}
