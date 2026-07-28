package test

// End-to-end KG test module — mocked-infra tier.
//
// A few AWS edge builders are reachable only via a live cloud-collector fetch
// (cloud.ExecuteCli). This tier stands up a fake collector — an in-process
// httptest server answering /v1/cloud/execute_cli with canned CLI JSON — so
// those edges can be exercised deterministically, with no real collector, DB,
// or network. It runs everywhere (no RequireMetastore gate).
//
// Currently covers MANAGES (CloudFormation stack → managed resources). The same
// harness can be extended to other ExecuteCli-only edges by returning more
// canned command responses.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"nudgebee/services/config"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
	"nudgebee/services/security"
)

// fakeCollector returns an httptest server that answers the cloud-collector
// execute_cli endpoint. handler maps an AWS CLI command string to the CLI's
// JSON output; the output is wrapped in the {"data": "<json>"} envelope the
// converter expects. Unmatched commands return an empty object so their parsers
// degrade gracefully (as they do in production when a fetch yields nothing).
func fakeCollector(t *testing.T, handler func(command string) (string, bool)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/v1/cloud/execute_cli") {
			http.Error(w, "unexpected path", http.StatusNotFound)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var req struct {
			AccountID string `json:"account_id"`
			Command   string `json:"command"`
		}
		_ = json.Unmarshal(body, &req)

		resp := map[string]any{}
		if out, ok := handler(req.Command); ok {
			resp["data"] = out
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

// mockedInfraReqCtx builds a DB-free, non-nil security context (audit-relay
// flavor) wrapped in a RequestContext — required because ExecuteCli reads
// ctx.GetSecurityContext().GetTenantId(). The normal tenant-admin context does a
// DB lookup and returns nil offline.
func mockedInfraReqCtx() *security.RequestContext {
	sc := security.NewSecurityContextForAuditRelay(e2eTenantID, "")
	return security.NewRequestContext(context.Background(), sc, e2eDiscardLogger(), nil, nil)
}

// TestE2E_MockedInfra_AWSManages exercises the CloudFormation MANAGES edge:
// createCloudFormationEdges calls `aws cloudformation list-stack-resources` via
// the collector; the fake returns a stack containing an S3 bucket, and the
// converter links the InfraStack → Storage node with MANAGES.
func TestE2E_MockedInfra_AWSManages(t *testing.T) {
	srv := fakeCollector(t, func(command string) (string, bool) {
		if strings.Contains(command, "cloudformation list-stack-resources") {
			out, _ := json.Marshal(map[string]any{
				"StackResourceSummaries": []map[string]any{{
					"LogicalResourceId":  "AppBucket",
					"PhysicalResourceId": "app-bucket",
					"ResourceType":       "AWS::S3::Bucket",
					"ResourceStatus":     "CREATE_COMPLETE",
				}},
			})
			return string(out), true
		}
		return "", false
	})
	defer srv.Close()

	prev := config.Config.CloudCollectorServerUrl
	config.Config.CloudCollectorServerUrl = srv.URL
	defer func() { config.Config.CloudCollectorServerUrl = prev }()

	req := &core.SourceBuildRequest{TenantID: e2eTenantID, CloudAccountID: e2eAWSAccount}
	resources := loadCloudResourceRows(t, filepath.Join("testdata", "e2e", "aws_manages", "input", "aws.json"))

	awsSource, err := sources.NewAWSSource(sources.AWSSourceConfig{}, nil)
	if err != nil {
		t.Fatalf("NewAWSSource: %v", err)
	}
	h := sources.NewAWSSourceTestHelper(awsSource)
	nodes, edges := h.ConvertResourcesToGraphWithCache(mockedInfraReqCtx(), resources, req)
	nodes, edges = mergeGraph(nodes, edges, mergeOpts{})

	assertGoldenGraph(t, e2eGoldenPath("aws_manages"), nodes, edges)

	var manages bool
	for _, e := range edges {
		if e.RelationshipType == core.RelationshipManages {
			manages = true
		}
	}
	if !manages {
		t.Error("aws_manages: expected CloudFormation InfraStack→Storage MANAGES edge not produced")
	}
}

// TestE2E_MockedInfra_GCPFirewallProtects exercises the GCP firewall PROTECTS
// edge, which is cliData-gated: the firewall node's vpc_id is enriched from a
// live `gcloud compute firewall-rules list` fetch (createFirewallRuleEdges then
// links firewall → VPC). The fake collector returns the firewall rule with its
// network self-link, enabling the edge offline.
func TestE2E_MockedInfra_GCPFirewallProtects(t *testing.T) {
	srv := fakeCollector(t, func(command string) (string, bool) {
		if strings.Contains(command, "firewall-rules list") {
			out, _ := json.Marshal([]map[string]any{{
				"name":      "my-fw",
				"network":   "projects/proj-1/global/networks/my-vpc",
				"direction": "INGRESS",
			}})
			return string(out), true
		}
		return "", false
	})
	defer srv.Close()

	prev := config.Config.CloudCollectorServerUrl
	config.Config.CloudCollectorServerUrl = srv.URL
	defer func() { config.Config.CloudCollectorServerUrl = prev }()

	req := &core.SourceBuildRequest{TenantID: e2eTenantID, CloudAccountID: e2eGCPAccount}
	resources := []sources.CloudResourceRow{
		{ID: "vpc-1", ResourceID: "my-vpc", Name: "my-vpc", Type: "vpc-network", ServiceName: "Networking", Account: "proj-1", Region: "global", CloudProvider: "GCP", IsActive: true, ExternalResourceID: "vpc-1", Meta: json.RawMessage("{}")},
		{ID: "fw-1", ResourceID: "my-fw", Name: "my-fw", Type: "firewall-rule", ServiceName: "Networking", Account: "proj-1", Region: "global", CloudProvider: "GCP", IsActive: true, ExternalResourceID: "fw-1", Meta: json.RawMessage(`{"network":"projects/proj-1/global/networks/my-vpc","direction":"INGRESS"}`)},
	}

	gcpSource, err := sources.NewGCPSource(sources.GCPSourceConfig{}, nil)
	if err != nil {
		t.Fatalf("NewGCPSource: %v", err)
	}
	h := sources.NewGCPSourceTestHelper(gcpSource)
	nodes, edges := h.ConvertResourcesToGraph(mockedInfraReqCtx(), resources, req)
	nodes, edges = mergeGraph(nodes, edges, mergeOpts{})

	assertGoldenGraph(t, e2eGoldenPath("gcp_firewall_protects"), nodes, edges)

	var protects bool
	for _, e := range edges {
		if e.RelationshipType == core.RelationshipProtects {
			protects = true
		}
	}
	if !protects {
		t.Error("gcp_firewall_protects: expected firewall→VPC PROTECTS edge not produced")
	}
}

// TestE2E_MockedInfra_GCPLoadBalancerChain exercises the two GCP load-balancer
// chain edges that are cliData-gated: URLMap → BackendService (ROUTES_TO) and
// BackendService → HealthCheck (ASSOCIATED_WITH). The LB-chain nodes themselves
// are created from the gcloud CLI data (ensureGCP*Nodes), so the fake collector
// supplies url-maps / backend-services / health-checks and the converter wires
// the chain via createLoadBalancerChainEdges.
func TestE2E_MockedInfra_GCPLoadBalancerChain(t *testing.T) {
	const base = "https://www.googleapis.com/compute/v1/projects/proj-1/global"
	srv := fakeCollector(t, func(command string) (string, bool) {
		switch {
		case strings.Contains(command, "url-maps list"):
			out, _ := json.Marshal([]map[string]any{{
				"name":           "my-urlmap",
				"defaultService": base + "/backendServices/my-backend",
			}})
			return string(out), true
		case strings.Contains(command, "backend-services list"):
			out, _ := json.Marshal([]map[string]any{{
				"name":                "my-backend",
				"loadBalancingScheme": "EXTERNAL_MANAGED",
				"healthChecks":        []string{base + "/healthChecks/my-hc"},
			}})
			return string(out), true
		case strings.Contains(command, "health-checks list"):
			out, _ := json.Marshal([]map[string]any{{"name": "my-hc", "type": "HTTP"}})
			return string(out), true
		}
		return "", false
	})
	defer srv.Close()

	prev := config.Config.CloudCollectorServerUrl
	config.Config.CloudCollectorServerUrl = srv.URL
	defer func() { config.Config.CloudCollectorServerUrl = prev }()

	req := &core.SourceBuildRequest{TenantID: e2eTenantID, CloudAccountID: e2eGCPAccount}
	// The URLMap/BackendPool/HealthCheck nodes are synthesized from the CLI data,
	// so only a placeholder resource row is needed to run the converter.
	resources := []sources.CloudResourceRow{
		{ID: "vpc-1", ResourceID: "my-vpc", Name: "my-vpc", Type: "vpc-network", ServiceName: "Networking", Account: "proj-1", Region: "global", CloudProvider: "GCP", IsActive: true, ExternalResourceID: "vpc-1", Meta: json.RawMessage("{}")},
	}

	gcpSource, err := sources.NewGCPSource(sources.GCPSourceConfig{}, nil)
	if err != nil {
		t.Fatalf("NewGCPSource: %v", err)
	}
	h := sources.NewGCPSourceTestHelper(gcpSource)
	nodes, edges := h.ConvertResourcesToGraph(mockedInfraReqCtx(), resources, req)
	nodes, edges = mergeGraph(nodes, edges, mergeOpts{})

	assertGoldenGraph(t, e2eGoldenPath("gcp_lb_chain"), nodes, edges)

	var routesTo, associatedWith bool
	for _, e := range edges {
		switch e.RelationshipType {
		case core.RelationshipRoutesTo:
			routesTo = true
		case core.RelationshipAssociatedWith:
			associatedWith = true
		}
	}
	if !routesTo {
		t.Error("gcp_lb_chain: expected URLMap→BackendService ROUTES_TO edge not produced")
	}
	if !associatedWith {
		t.Error("gcp_lb_chain: expected BackendService→HealthCheck ASSOCIATED_WITH edge not produced")
	}
}

// TestE2E_MockedInfra_AWSLBPodTarget is a documented scaffold (skipped) for the
// aws_lb_pod_target cross-source enricher (AWS LoadBalancer → Pod ROUTES_TO).
//
// Unlike aws_lb_k8s (covered offline via K8s tags), this enricher is the one
// irreducibly live-integration path in the AWS surface. It needs THREE
// dependencies, only two of which are mockable in-process:
//
//  1. Fake collector (this file's fakeCollector) — for `aws elbv2
//     describe-target-groups` / `describe-target-health` / `ec2
//     describe-instances`.
//  2. Fake relay/Prometheus — an httptest server at config.RelayServerEndpoint
//     answering POST /request (action "prometheus_queries_enricher") with canned
//     kube_pod_info (+ kube_replicaset_owner) results. Same pattern as
//     fakeCollector; relay.Execute posts {ActionName, ...} and parses the reply
//     into the metric result array.
//  3. A REAL metastore row: GetK8sCloudAccountMapping (core/helpers.go) runs a
//     raw SELECT against cloud_accounts with NO fallback — if it returns no
//     AWS→K8s mapping the enricher skips every LoadBalancer. This cannot be
//     mocked without a DB or a production injection seam.
//
// Because of (3) this can only run against a live metastore; it is therefore
// gated + skipped by default. To complete it: seed a cloud_accounts row mapping
// the AWS account to the K8s account, stand up the fake collector + fake relay,
// build the graph with mockedInfraReqCtx(), run the aws_lb_pod_target enricher,
// and assert a LoadBalancer→Pod ROUTES_TO edge.
func TestE2E_MockedInfra_AWSLBPodTarget(t *testing.T) {
	t.Skip("aws_lb_pod_target: live-only — needs fake collector + fake relay + a seeded cloud_accounts mapping (no offline fallback); see doc comment")
}
