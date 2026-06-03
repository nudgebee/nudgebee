package sources

import (
	"testing"

	"nudgebee/services/knowledge_graph/core"
)

// TestDefaultServiceTypeFilter_AllowsGateways guards against a regression
// where AmazonVPC's whitelist forgets to include natgateway / internet-gateway.
// During the 2026-05-19 local audit we discovered shouldIncludeResource was
// silently dropping the live NAT GW because AmazonVPC only listed
// {elastic-ip, network-interface, security_group, subnet, vpc, vpc-endpoint}
// — even though the type→NodeType mapping was correct. See #30681.
func TestDefaultServiceTypeFilter_AllowsGateways(t *testing.T) {
	src, err := NewAWSSource(AWSSourceConfig{ServiceTypeFilter: DefaultServiceTypeFilter}, nil)
	if err != nil {
		t.Fatalf("NewAWSSource: %v", err)
	}
	for _, tc := range []struct {
		serviceName string
		resType     string
	}{
		{"AmazonVPC", "natgateway"},
		{"AmazonVPC", "internet-gateway"},
		{"AmazonEC2", "natgateway"}, // legacy rows
	} {
		row := &CloudResourceRow{ServiceName: tc.serviceName, Type: tc.resType, Name: "x"}
		if !src.shouldIncludeResource(row) {
			t.Errorf("shouldIncludeResource(%s, %s) = false, want true (NetworkGateway rows must not be filtered)", tc.serviceName, tc.resType)
		}
	}
}

// TestDetermineNodeType_NetworkGateways pins the type → NodeType routing for
// NAT Gateways and Internet Gateways under both the legacy AmazonEC2 namespace
// and the current AmazonVPC namespace. Without the AmazonVPC entries, fresh
// rows written by the collector (which uses ServiceNameVPC = "AmazonVPC")
// were silently routed to NodeTypeCloudResource — the prod NAT GW for the
// nudgebee VPC ended up with 0 active NetworkGateway nodes despite a live
// `nat-07a10ccb3e79c1cdd` in AWS. See #30681.
func TestDetermineNodeType_NetworkGateways(t *testing.T) {
	src, err := NewAWSSource(AWSSourceConfig{}, nil)
	if err != nil {
		t.Fatalf("NewAWSSource: %v", err)
	}

	cases := []struct {
		name        string
		resType     string
		serviceName string
		want        core.NodeType
	}{
		// NAT GW: both namespaces must route to NetworkGateway.
		{"nat_gw_vpc_namespace", "natgateway", "AmazonVPC", core.NodeTypeNetworkGateway},
		{"nat_gw_legacy_ec2_namespace", "natgateway", "AmazonEC2", core.NodeTypeNetworkGateway},
		// IGW: AmazonVPC only (collector writes IGWs under VPC namespace).
		{"igw_vpc_namespace", "internet-gateway", "AmazonVPC", core.NodeTypeNetworkGateway},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := src.determineNodeType(tc.resType, tc.serviceName)
			if got != tc.want {
				t.Errorf("determineNodeType(%q, %q) = %q, want %q", tc.resType, tc.serviceName, got, tc.want)
			}
		})
	}
}
