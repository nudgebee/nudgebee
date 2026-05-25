package sources

import (
	"reflect"
	"sort"
	"testing"

	"nudgebee/services/knowledge_graph/core"
)

// TestExtractENIMetadata pins the primary createNodeFromResource extraction
// path for AWS ENIs. The earlier #30683 fix only touched
// createENINodeFromAWSData, which is a *fallback* path; the primary path was
// silently bypassing PrivateIpAddresses[]. See the empirical failure in the
// 2026-05-19 local run where 28 active ENIs all came back without
// properties["private_ips"]. This test pins the fix.
func TestExtractENIMetadata(t *testing.T) {
	src, err := NewAWSSource(AWSSourceConfig{}, nil)
	if err != nil {
		t.Fatalf("NewAWSSource: %v", err)
	}

	meta := map[string]interface{}{
		"NetworkInterfaceId": "eni-01a89a881959458aa",
		"PrivateIpAddress":   "172.31.4.62",
		"VpcId":              "vpc-00459f012dc59d416",
		"SubnetId":           "subnet-0735a8c27a0638bc7",
		"InterfaceType":      "interface",
		"Status":             "in-use",
		"PrivateIpAddresses": []interface{}{
			map[string]interface{}{"PrivateIpAddress": "172.31.4.62", "Primary": true},
			map[string]interface{}{"PrivateIpAddress": "172.31.10.218", "Primary": false},
			map[string]interface{}{"PrivateIpAddress": "172.31.14.96", "Primary": false},
		},
	}

	props := map[string]interface{}{}
	src.extractENIMetadata(props, meta)

	if got := props["private_ip_address"]; got != "172.31.4.62" {
		t.Errorf("private_ip_address = %v, want 172.31.4.62", got)
	}
	if got := props["network_interface_id"]; got != "eni-01a89a881959458aa" {
		t.Errorf("network_interface_id = %v", got)
	}
	if got := props["vpc_id"]; got != "vpc-00459f012dc59d416" {
		t.Errorf("vpc_id = %v", got)
	}

	got, ok := props["private_ips"].([]string)
	if !ok {
		t.Fatalf("private_ips missing or wrong type: %T", props["private_ips"])
	}
	want := []string{"172.31.4.62", "172.31.10.218", "172.31.14.96"}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("private_ips = %v, want %v", got, want)
	}
}

// TestExtractENIMetadata_NoSecondaries asserts that when PrivateIpAddresses
// is missing or empty, private_ips is not set (no empty array).
func TestExtractENIMetadata_NoSecondaries(t *testing.T) {
	src, err := NewAWSSource(AWSSourceConfig{}, nil)
	if err != nil {
		t.Fatalf("NewAWSSource: %v", err)
	}

	props := map[string]interface{}{}
	src.extractENIMetadata(props, map[string]interface{}{
		"NetworkInterfaceId": "eni-test",
		"PrivateIpAddress":   "10.0.0.1",
	})

	if _, ok := props["private_ips"]; ok {
		t.Errorf("private_ips should be absent when PrivateIpAddresses is missing")
	}
}

// TestCreateENINodeFromAWSData_PrivateIPs pins the *fallback* path. Kept
// for the case where the ENI isn't already in the existing node list
// (eg filtered out earlier) and createENIEdges falls back to building
// fresh from the meta cache. See #30683.
func TestCreateENINodeFromAWSData_PrivateIPs(t *testing.T) {
	src, err := NewAWSSource(AWSSourceConfig{}, nil)
	if err != nil {
		t.Fatalf("NewAWSSource: %v", err)
	}

	eni := &ENINetworkInterface{
		NetworkInterfaceId: "eni-046254e28933278cc",
		PrivateIpAddress:   "172.31.81.38",
		PrivateIpAddresses: []struct {
			PrivateIpAddress string `json:"PrivateIpAddress"`
			Primary          bool   `json:"Primary"`
		}{
			{PrivateIpAddress: "172.31.81.38", Primary: true},
			{PrivateIpAddress: "172.31.91.126"},
			{PrivateIpAddress: "172.31.83.27"},
		},
	}

	node := src.createENINodeFromAWSData(eni, &core.SourceBuildRequest{TenantID: "t", CloudAccountID: "a"})

	got, ok := node.Properties["private_ips"].([]string)
	if !ok {
		t.Fatalf("private_ips missing or wrong type: %T", node.Properties["private_ips"])
	}
	want := []string{"172.31.81.38", "172.31.91.126", "172.31.83.27"}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("private_ips = %v, want %v", got, want)
	}
}

// TestCreateENINodeFromAWSData_NoSecondaries — regression guard for the
// fallback path with empty PrivateIpAddresses.
func TestCreateENINodeFromAWSData_NoSecondaries(t *testing.T) {
	src, err := NewAWSSource(AWSSourceConfig{}, nil)
	if err != nil {
		t.Fatalf("NewAWSSource: %v", err)
	}

	eni := &ENINetworkInterface{
		NetworkInterfaceId: "eni-test",
		PrivateIpAddress:   "10.0.0.1",
	}

	node := src.createENINodeFromAWSData(eni, &core.SourceBuildRequest{TenantID: "t", CloudAccountID: "a"})

	if _, exists := node.Properties["private_ips"]; exists {
		t.Errorf("private_ips should be absent when PrivateIpAddresses is empty")
	}
}
