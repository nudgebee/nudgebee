package aws

import (
	"encoding/json"
	"testing"

	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
)

const (
	manualTestTenant  = "11111111-1111-1111-1111-111111111111"
	manualTestAccount = "22222222-2222-2222-2222-222222222222"
	manualTestVPCID   = "vpc-0abc123"
)

// collectorRow is the inventory row the collector writes for the resource a
// manual declaration names. Metadata names the VPC in every spelling the AWS
// extractors read (top level, and RDS's DBSubnetGroup).
func collectorRow(e sources.ManualNodeEntry, withVPC bool) sources.CloudResourceRow {
	meta := map[string]interface{}{}
	if withVPC {
		meta["VpcId"] = manualTestVPCID
		meta["VPCId"] = manualTestVPCID
		meta["DBSubnetGroup"] = map[string]interface{}{"VpcId": manualTestVPCID}
	}
	rawMeta, _ := json.Marshal(meta)
	return sources.CloudResourceRow{
		ID:            "33333333-3333-3333-3333-333333333333",
		ResourceID:    "orders-resource",
		Name:          "orders",
		Type:          e.RowType,
		ServiceName:   e.ServiceName,
		Account:       manualTestAccount,
		Tenant:        manualTestTenant,
		CloudProvider: "AWS",
		Region:        "us-east-1",
		IsActive:      true,
		AccountNumber: "123456789012",
		Meta:          rawMeta,
		Tags:          []byte("{}"),
	}
}

func vpcRow() sources.CloudResourceRow {
	return sources.CloudResourceRow{
		ID:            "44444444-4444-4444-4444-444444444444",
		ResourceID:    manualTestVPCID,
		Name:          "prod-vpc",
		Type:          "vpc",
		ServiceName:   "AmazonVPC",
		Account:       manualTestAccount,
		Tenant:        manualTestTenant,
		CloudProvider: "AWS",
		Region:        "us-east-1",
		IsActive:      true,
		Meta:          []byte("{}"),
		Tags:          []byte("{}"),
	}
}

// TestManualNode_AWSKeyMatchesCollector is the guarantee the feature rests on:
// a resource declared by hand gets the node id the collector later writes for
// it, so the two merge instead of duplicating. It runs every declarable type
// through the full collector conversion (including the VPC name pass) and
// through the manual builder, and requires identical unique keys and ids.
func TestManualNode_AWSKeyMatchesCollector(t *testing.T) {
	defer disableCloudCLIForTest()()
	b, ok := core.ManualNodeBuilderFor("AWS")
	if !ok {
		t.Fatal("no AWS manual node builder registered")
	}
	declarable := b.Kinds()
	if len(declarable) < 10 {
		t.Fatalf("expected the common AWS types to be declarable, got %d", len(declarable))
	}

	entries := map[string]sources.ManualNodeEntry{}
	for _, e := range awsManualCandidates() {
		if _, seen := entries[e.SpecificType]; !seen {
			entries[e.SpecificType] = e
		}
	}

	src, err := NewAWSSource(AWSSourceConfig{}, nil)
	if err != nil {
		t.Fatalf("NewAWSSource: %v", err)
	}
	h := NewAWSSourceTestHelper(src)
	req := &core.SourceBuildRequest{TenantID: manualTestTenant, CloudAccountID: manualTestAccount}

	for _, kind := range declarable {
		kind := kind
		t.Run(kind.SpecificType, func(t *testing.T) {
			e := entries[kind.SpecificType]
			withVPC := e.Network != ""

			rows := []sources.CloudResourceRow{collectorRow(e, withVPC)}
			if withVPC {
				rows = append(rows, vpcRow())
			}
			nodes, _ := h.ConvertResourcesToGraph(allTypesReqCtx(), rows, req)

			var collected, vpc *core.DbNode
			for _, n := range nodes {
				switch {
				case n.SpecificType == kind.SpecificType && n.Properties["name"] == "orders":
					collected = n
				case n.NodeType == core.NodeTypeVPC:
					vpc = n
				}
			}
			if collected == nil {
				t.Fatalf("collector built no %s node", kind.SpecificType)
			}

			in := core.ManualNodeInput{
				TenantID: manualTestTenant, CloudAccountID: manualTestAccount, AccountNumber: "123456789012",
				CloudProvider: "AWS", SpecificType: kind.SpecificType, Name: "orders", Region: "us-east-1",
			}
			if withVPC {
				if vpc == nil {
					t.Fatal("collector built no VPC node")
				}
				// What the RPC layer reads off the picked VPC node.
				name, _ := vpc.Properties["name"].(string)
				in.Network = &core.ManualNodeNetwork{ResourceID: manualTestVPCID, Name: name}
			}
			manual, err := b.Build(in)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}

			if manual.UniqueKey != collected.UniqueKey {
				t.Errorf("unique key drifted from the collector's\n manual:    %s\n collector: %s", manual.UniqueKey, collected.UniqueKey)
			}
			if manual.ID != collected.ID {
				t.Errorf("node id drifted from the collector's: manual %s, collector %s", manual.ID, collected.ID)
			}
			if manual.NodeType != collected.NodeType || manual.SpecificType != collected.SpecificType {
				t.Errorf("type drifted: manual %s/%s, collector %s/%s",
					manual.NodeType, manual.SpecificType, collected.NodeType, collected.SpecificType)
			}
		})
	}
}

// TestManualNode_AWSNetworkRules covers the validation that keeps a declared
// key on the collector's recipe.
func TestManualNode_AWSNetworkRules(t *testing.T) {
	b, _ := core.ManualNodeBuilderFor("AWS")
	base := core.ManualNodeInput{TenantID: manualTestTenant, CloudAccountID: manualTestAccount, CloudProvider: "AWS", Name: "orders", Region: "us-east-1"}
	vpc := &core.ManualNodeNetwork{ResourceID: manualTestVPCID, Name: "prod-vpc"}

	cases := []struct {
		name         string
		specificType string
		network      *core.ManualNodeNetwork
		region       string
		wantErr      bool
	}{
		{"EC2 needs its VPC", "EC2Instance", nil, "us-east-1", true},
		{"EC2 with VPC", "EC2Instance", vpc, "us-east-1", false},
		{"RDS outside a VPC", "RDSInstance", nil, "us-east-1", false},
		{"SQS has no VPC", "SQSQueue", vpc, "us-east-1", true},
		{"region is required", "SQSQueue", nil, "", true},
		{"undeclarable type", "EC2SecurityGroup", nil, "us-east-1", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := base
			in.SpecificType, in.Network, in.Region = tc.specificType, tc.network, tc.region
			_, err := b.Build(in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("Build error = %v, want error %v", err, tc.wantErr)
			}
		})
	}
}
