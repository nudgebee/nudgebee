package gcloud

import (
	"encoding/json"
	"testing"

	"cloud.google.com/go/compute/apiv1/computepb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func strptr(s string) *string { return &s }

// TestNetworkEndpointGroupMetaKeys pins the exact meta keys this writer emits.
// The knowledge graph decodes these by name in
// knowledge_graph/sources/gcp/topology_from_db.go, and the two live in separate
// Go modules so the compiler cannot catch a rename. This test is the contract:
// change a key here and it fails, rather than the graph silently losing edges.
func TestNetworkEndpointGroupMetaKeys(t *testing.T) {
	svc := &cloudLoadBalancingService{}
	neg := &computepb.NetworkEndpointGroup{
		Name:                strptr("serverless-neg"),
		SelfLink:            strptr("https://www.googleapis.com/compute/v1/projects/p/regions/us-central1/networkEndpointGroups/serverless-neg"),
		Region:              strptr("https://www.googleapis.com/compute/v1/projects/p/regions/us-central1"),
		NetworkEndpointType: strptr("SERVERLESS"),
		CloudRun:            &computepb.NetworkEndpointGroupCloudRun{Service: strptr("checkout")},
	}

	res := svc.networkEndpointGroupToResource("p", neg)

	assert.Equal(t, "network-endpoint-group", res.Type)
	assert.Equal(t, "serverless-neg", res.Name)
	assert.Equal(t, "us-central1", res.Region, "region URL must be reduced to its bare name")
	assert.Equal(t, "p/regions/us-central1/networkEndpointGroups/serverless-neg", res.Id)

	raw, err := json.Marshal(res.Meta)
	require.NoError(t, err)
	var meta map[string]any
	require.NoError(t, json.Unmarshal(raw, &meta))

	assert.Equal(t, "SERVERLESS", meta["network_endpoint_type"])
	cloudRun, ok := meta["cloud_run"].(map[string]any)
	require.True(t, ok, "cloud_run must be a nested object — the graph reads cloud_run.service")
	assert.Equal(t, "checkout", cloudRun["service"])
}

// Zonal NEGs must keep their zone. GKE creates one NEG per zone under a single
// name, so collapsing zone to region (or dropping it) would merge them into one
// row and lose three of the four.
func TestNetworkEndpointGroupZonalKeepsZone(t *testing.T) {
	svc := &cloudLoadBalancingService{}

	ids := map[string]bool{}
	for _, zone := range []string{"us-central1-a", "us-central1-b", "us-central1-c"} {
		neg := &computepb.NetworkEndpointGroup{
			Name:                strptr("k8s1-shared-name"),
			Zone:                strptr("https://www.googleapis.com/compute/v1/projects/p/zones/" + zone),
			NetworkEndpointType: strptr("GCE_VM_IP_PORT"),
		}
		res := svc.networkEndpointGroupToResource("p", neg)

		assert.Equal(t, zone, res.Region, "zonal NEG must record its zone")
		assert.Equal(t, "p/zones/"+zone+"/networkEndpointGroups/k8s1-shared-name", res.Id)
		assert.False(t, ids[res.Id], "same-named NEGs in different zones must not collide on id")
		ids[res.Id] = true
	}
	assert.Len(t, ids, 3)
}

// A global NEG has neither zone nor region.
func TestNetworkEndpointGroupGlobalScope(t *testing.T) {
	svc := &cloudLoadBalancingService{}
	neg := &computepb.NetworkEndpointGroup{
		Name:                strptr("internet-neg"),
		NetworkEndpointType: strptr("INTERNET_FQDN_PORT"),
	}
	res := svc.networkEndpointGroupToResource("p", neg)

	assert.Equal(t, "global", res.Region)
	assert.Equal(t, "p/global/networkEndpointGroups/internet-neg", res.Id)
}
