package test

// End-to-end KG test module — Tier A (JSON in-memory build scenarios).
//
// Each scenario loads JSON fixtures, drives the REAL source converters +
// merge pipeline (mirroring BuildGraphs phase 3), and diffs the result against
// a committed golden. No database is required — see e2e_diff_test.go for the
// golden engine and the -update flag.

import (
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"nudgebee/services/config"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
)

// Identifiers shared by the Tier-A scenarios. Kept consistent with the existing
// testdata fixtures so reused inputs (k8s_nodes.json etc.) stay coherent.
const (
	e2eTenantID     = "test-tenant-1"
	e2eK8sAccount   = "test-account-k8s-1"
	e2eAWSAccount   = "test-account-aws-1"
	e2eGCPAccount   = "test-account-gcp-1"
	e2eAzureAccount = "test-account-azure-1"
)

func e2eGoldenPath(scenario string) string {
	return filepath.Join("testdata", "e2e", scenario, "golden.json")
}

func e2eDiscardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// buildK8sGraph runs the real k8s converters (nodes + workloads) and returns the
// combined pre-merge nodes/edges — the same wiring BuildGraphs' k8s source uses.
func buildK8sGraph(t *testing.T, workloads []sources.K8sWorkloadRow, k8sNodes []sources.K8sNodeRow, req *core.SourceBuildRequest) ([]*core.DbNode, []*core.DbEdge) {
	t.Helper()
	k8sSource, err := sources.NewK8sSource(sources.K8sSourceConfig{TenantID: req.TenantID, CloudAccountID: req.CloudAccountID}, nil)
	if err != nil {
		t.Fatalf("NewK8sSource: %v", err)
	}
	h := sources.NewK8sSourceTestHelper(k8sSource)

	nodeNodes, nodeEdges := h.ConvertK8sNodesToGraph(k8sNodes, req)
	nodeMap := make(map[string]*core.DbNode, len(nodeNodes))
	for _, n := range nodeNodes {
		if name, ok := core.GetNodePropertyString(n, "name"); ok {
			nodeMap[name] = n
		}
	}
	wlNodes, wlEdges, clusterNodes, nsNodes, _ := h.ConvertWorkloadsToGraph(workloads, &nodeMap, req)

	nodes := make([]*core.DbNode, 0, len(nodeNodes)+len(wlNodes))
	nodes = append(nodes, nodeNodes...)
	nodes = append(nodes, wlNodes...)
	nodes = appendMissingNodes(nodes, clusterNodes)
	nodes = appendMissingNodes(nodes, nsNodes)

	edges := make([]*core.DbEdge, 0, len(nodeEdges)+len(wlEdges))
	edges = append(edges, nodeEdges...)
	edges = append(edges, wlEdges...)
	return nodes, edges
}

// buildAWSGraph runs the real AWS converter over the given rows.
func buildAWSGraph(t *testing.T, resources []sources.CloudResourceRow, req *core.SourceBuildRequest) ([]*core.DbNode, []*core.DbEdge) {
	t.Helper()
	defer disableCloudCLI()()
	awsSource, err := sources.NewAWSSource(sources.AWSSourceConfig{}, nil)
	if err != nil {
		t.Fatalf("NewAWSSource: %v", err)
	}
	h := sources.NewAWSSourceTestHelper(awsSource)
	// Use the cache-populating variant so cache-backed edge builders (IAM roles,
	// S3 notifications, route-table, elastic-ip, ENI) can fire — BuildGraph
	// populates this cache in production; the bare converter does not.
	return h.ConvertResourcesToGraphWithCache(createTestRequestContext(req.TenantID), resources, req)
}

// buildAzureGraph runs the real Azure converter over the given rows.
func buildAzureGraph(t *testing.T, resources []sources.CloudResourceRow, req *core.SourceBuildRequest) ([]*core.DbNode, []*core.DbEdge) {
	t.Helper()
	defer disableCloudCLI()()
	azureSource, err := sources.NewAzureSource(sources.AzureSourceConfig{}, nil)
	if err != nil {
		t.Fatalf("NewAzureSource: %v", err)
	}
	h := sources.NewAzureSourceTestHelper(azureSource)
	return h.ConvertResourcesToGraph(createTestRequestContext(req.TenantID), resources, req)
}

// loadCloudResourceRows loads a hand-authored cloud-resource fixture (AWS/GCP/
// Azure share the CloudResourceRow shape) from an arbitrary path under testdata.
func loadCloudResourceRows(t *testing.T, path string) []sources.CloudResourceRow {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	var jr []CloudResourceJSON
	if err := json.Unmarshal(data, &jr); err != nil {
		t.Fatalf("unmarshal fixture %s: %v", path, err)
	}
	rows := make([]sources.CloudResourceRow, len(jr))
	for i, r := range jr {
		tags, _ := json.Marshal(r.Tags)
		meta, _ := json.Marshal(r.Meta)
		rows[i] = sources.CloudResourceRow{
			ID:                 r.ID,
			ResourceID:         r.ResourceID,
			Name:               r.Name,
			Type:               r.Type,
			Status:             r.Status,
			Account:            r.Account,
			Tenant:             r.Tenant,
			CloudProvider:      r.CloudProvider,
			Region:             r.Region,
			ARN:                r.ARN,
			Tags:               tags,
			Meta:               meta,
			ServiceName:        r.ServiceName,
			IsActive:           r.IsActive,
			ExternalResourceID: r.ExternalResourceID,
			AccountNumber:      r.AccountNumber,
		}
	}
	return rows
}

// loadK8sNodeRows loads a hand-authored K8s node fixture from an arbitrary path,
// mirroring integration_test.go's loadK8sNodes conversion (K8sNodeJSON → row).
func loadK8sNodeRows(t *testing.T, path string) []sources.K8sNodeRow {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read k8s node fixture %s: %v", path, err)
	}
	var jn []K8sNodeJSON
	if err := json.Unmarshal(data, &jn); err != nil {
		t.Fatalf("unmarshal k8s node fixture %s: %v", path, err)
	}
	rows := make([]sources.K8sNodeRow, len(jn))
	for i, n := range jn {
		meta, _ := json.Marshal(n.Meta)
		rows[i] = sources.K8sNodeRow{
			TenantID:          n.TenantID,
			CloudAccountID:    n.CloudAccountID,
			Name:              n.Name,
			IsActive:          n.IsActive,
			Conditions:        n.Conditions,
			NodeType:          n.NodeType,
			NodeFlavor:        n.NodeFlavor,
			NodeRegion:        n.NodeRegion,
			NodeZone:          n.NodeZone,
			MemoryCapacity:    n.MemoryCapacity,
			CPUCapacity:       n.CPUCapacity,
			MemoryAllocatable: n.MemoryAllocatable,
			CPUAllocatable:    n.CPUAllocatable,
			Meta:              meta,
			ClusterName:       n.ClusterName,
		}
	}
	return rows
}

// runK8sEC2Enricher runs the k8s_ec2 cross-source enricher (K8s Node → AWS
// ComputeInstance RUNS_ON via providerID). It is in-memory only. The aws_lb_k8s
// enricher is intentionally not run here — it depends on relay account-mapping
// lookups that need external state.
func runK8sEC2Enricher(t *testing.T, nodes []*core.DbNode, edges []*core.DbEdge, tenantID string) ([]*core.DbNode, []*core.DbEdge) {
	t.Helper()
	en := sources.NewK8sEC2Enricher(e2eDiscardLogger())
	n, e, err := en.EnrichCrossSources(createTestRequestContext(tenantID), nodes, edges, tenantID)
	if err != nil {
		t.Fatalf("k8s_ec2 enricher: %v", err)
	}
	return n, e
}

// runAWSLBK8sEnricher runs the aws_lb_k8s cross-source enricher (AWS LoadBalancer
// → K8s Service/Workload via K8s tags, and EKS ManagedCluster → K8s Cluster).
// CLI is disabled so its Strategy-3/4 (target-group / Prometheus) paths no-op;
// the offline Strategy-1 (tags) and ManagedCluster links still fire. The DB
// account-mapping lookup is optional — on failure the enricher falls back to all
// K8s accounts.
func runAWSLBK8sEnricher(t *testing.T, nodes []*core.DbNode, edges []*core.DbEdge, tenantID string) ([]*core.DbNode, []*core.DbEdge) {
	t.Helper()
	defer disableCloudCLI()()
	en := sources.NewLoadBalancerK8sEnricher(e2eDiscardLogger())
	n, e, err := en.EnrichCrossSources(createTestRequestContext(tenantID), nodes, edges, tenantID)
	if err != nil {
		t.Fatalf("aws_lb_k8s enricher: %v", err)
	}
	return n, e
}

// buildGCPGraph runs the real GCP converter over the given rows.
func buildGCPGraph(t *testing.T, resources []sources.CloudResourceRow, req *core.SourceBuildRequest) ([]*core.DbNode, []*core.DbEdge) {
	t.Helper()
	defer disableCloudCLI()()
	gcpSource, err := sources.NewGCPSource(sources.GCPSourceConfig{}, nil)
	if err != nil {
		t.Fatalf("NewGCPSource: %v", err)
	}
	h := sources.NewGCPSourceTestHelper(gcpSource)
	return h.ConvertResourcesToGraph(createTestRequestContext(req.TenantID), resources, req)
}

// disableCloudCLI forces cloud.ExecuteCli to early-return an error (before it
// touches the security context or the network), so the AWS/GCP/Azure converters
// run purely in-memory and produce a deterministic golden. The converters
// already treat a CLI error as "enrichment unavailable" and continue. Returns a
// restore func; call as `defer disableCloudCLI()()`.
func disableCloudCLI() func() {
	prev := config.Config.CloudCollectorServerUrl
	config.Config.CloudCollectorServerUrl = ""
	return func() { config.Config.CloudCollectorServerUrl = prev }
}

// appendMissingNodes appends map values not already present (by ID).
func appendMissingNodes(nodes []*core.DbNode, m map[string]*core.DbNode) []*core.DbNode {
	seen := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		seen[n.ID] = true
	}
	for _, n := range m {
		if !seen[n.ID] {
			nodes = append(nodes, n)
			seen[n.ID] = true
		}
	}
	return nodes
}

// mergeOpts configures optional phases of the merge pipeline.
type mergeOpts struct {
	crossAccount bool          // apply default cross-account relationship rules
	svc          *core.Service // required when crossAccount is true
}

// mergeGraph applies BuildGraphs' phase-3 merge to a combined graph: dedup nodes
// (with ID remap), priority-dedup edges, optional cross-account rules, then the
// collapse/GCP-resolve passes and a final priority dedup. It is DB-free unless
// crossAccount is requested with a Service (GetDefaultRelationships is file-based
// and BuildCrossAccountRelationships is in-memory, so even that stays DB-free).
func mergeGraph(nodes []*core.DbNode, edges []*core.DbEdge, opts mergeOpts) ([]*core.DbNode, []*core.DbEdge) {
	logger := e2eDiscardLogger()

	nodes, idMap := core.DeduplicateNodesWithIDMapping(nodes)
	for _, e := range edges {
		if nid, ok := idMap[e.SourceNodeID]; ok {
			e.SourceNodeID = nid
		}
		if nid, ok := idMap[e.DestinationNodeID]; ok {
			e.DestinationNodeID = nid
		}
	}
	edges = core.DeduplicateEdgesWithPriority(edges)

	if opts.crossAccount && opts.svc != nil {
		g := &core.Graph{Nodes: nodes, Edges: edges}
		rules := opts.svc.GetDefaultRelationships()
		if caEdges, err := opts.svc.BuildCrossAccountRelationships(g, rules); err == nil && len(caEdges) > 0 {
			edges = append(edges, caEdges...)
			edges = core.DeduplicateEdges(edges)
		}
	}

	nodes, edges, _ = core.CollapseEnrichedExternalServices(nodes, edges, logger)
	nodes, edges, _ = core.ResolveGCPManagedServiceCalls(nodes, edges, logger)
	edges = core.DeduplicateEdgesWithPriority(edges)
	return nodes, edges
}

// ============================================================================
// Scenario: basic_k8s — the k8s converter baseline (workloads, nodes, pods).
// ============================================================================

func TestE2E_TierA_BasicK8s(t *testing.T) {
	req := &core.SourceBuildRequest{TenantID: e2eTenantID, CloudAccountID: e2eK8sAccount}
	workloads := loadK8sWorkloads(t)
	k8sNodes := loadK8sNodes(t)

	nodes, edges := buildK8sGraph(t, workloads, k8sNodes, req)
	nodes, edges = mergeGraph(nodes, edges, mergeOpts{})

	assertGoldenGraph(t, e2eGoldenPath("basic_k8s"), nodes, edges)
}

// ============================================================================
// Scenario: k8s workload-kind routing — exhaustively asserts every k8s workload
// kind the table converter (createNodeFromWorkload) can receive routes to the
// correct NodeType: pod→Pod, service→K8sService, everything else→Workload.
// (Typed ConfigMap/Secret/Ingress/ServiceAccount nodes come from the relay-fetch
// path, not the k8s_workloads table, so they are out of this module's scope.)
// ============================================================================

func TestE2E_TierA_K8sWorkloadKindRouting(t *testing.T) {
	expected := map[string]core.NodeType{
		"Deployment":            core.NodeTypeWorkload,
		"StatefulSet":           core.NodeTypeWorkload,
		"DaemonSet":             core.NodeTypeWorkload,
		"ReplicaSet":            core.NodeTypeWorkload,
		"Job":                   core.NodeTypeWorkload,
		"CronJob":               core.NodeTypeWorkload,
		"ConfigMap":             core.NodeTypeWorkload,
		"Secret":                core.NodeTypeWorkload,
		"Ingress":               core.NodeTypeWorkload,
		"PersistentVolumeClaim": core.NodeTypeWorkload,
		"Service":               core.NodeTypeK8sService,
		"Pod":                   core.NodeTypePod,
	}

	req := &core.SourceBuildRequest{TenantID: e2eTenantID, CloudAccountID: e2eK8sAccount}
	workloads := make([]sources.K8sWorkloadRow, 0, len(expected))
	for kind := range expected {
		workloads = append(workloads, sources.K8sWorkloadRow{
			ID:          kind,
			Kind:        kind,
			Namespace:   "default",
			Name:        kind, // name == kind so we can look the node back up
			ClusterName: "kind-cluster",
			IsActive:    true,
			Meta:        json.RawMessage("{}"),
			Labels:      json.RawMessage("{}"),
		})
	}

	nodes, _ := buildK8sGraph(t, workloads, nil, req)

	got := make(map[string]core.NodeType, len(nodes))
	for _, n := range nodes {
		if name, ok := core.GetNodePropertyString(n, "name"); ok {
			got[name] = n.NodeType
		}
	}
	for kind, want := range expected {
		if got[kind] != want {
			t.Errorf("workload kind %q routed to %q, want %q", kind, got[kind], want)
		}
	}
}

// ============================================================================
// Scenario: aws_resources — the AWS converter over a representative resource set
// (RDS, DynamoDB, Lambda, SQS/SNS, S3, ElastiCache, ALB, CloudFront, Route53,
// ECR, VPC) and their intra-account infra edges.
// ============================================================================

func TestE2E_TierA_AWSResources(t *testing.T) {
	req := &core.SourceBuildRequest{TenantID: e2eTenantID, CloudAccountID: e2eAWSAccount}
	resources := loadAWSResources(t)

	nodes, edges := buildAWSGraph(t, resources, req)
	nodes, edges = mergeGraph(nodes, edges, mergeOpts{})

	assertGoldenGraph(t, e2eGoldenPath("aws_resources"), nodes, edges)
}

// ============================================================================
// Scenario: gcp_plus_k8s — GCP↔k8s CROSS-ACCOUNT via the default-relationship
// engine. The Workload-Identity rule (k8s_serviceaccount_to_gcp_iam_sa_wi) links
// a K8s ServiceAccount (property gcp_service_account_email) to the GCP
// ServiceIdentity (property email) with ASSUMES, across accounts.
// ============================================================================

func TestE2E_TierA_GCPCrossAccountK8s(t *testing.T) {
	email := "sa@proj-1.iam.gserviceaccount.com"

	gcpReq := &core.SourceBuildRequest{TenantID: e2eTenantID, CloudAccountID: e2eGCPAccount}
	gcpRes := []sources.CloudResourceRow{{
		ID: "gsa-1", ResourceID: email, Name: email, Type: "iam.googleapis.com/ServiceAccount", ServiceName: "IAM",
		Account: "proj-1", Region: "global", CloudProvider: "GCP", IsActive: true, ExternalResourceID: "gsa-1",
		Meta: json.RawMessage(`{"email":"` + email + `"}`),
	}}
	gcpNodes, gcpEdges := buildGCPGraph(t, gcpRes, gcpReq)

	// Synthetic K8s ServiceAccount with the workload-identity annotation resolved
	// into gcp_service_account_email (K8sServiceAccount nodes come from the relay
	// path, not the source tables, so we construct one directly).
	ksa := core.NewNode(core.NodeTypeK8sServiceAccount,
		"k8s:"+e2eK8sAccount+"::K8sServiceAccount:default:my-ksa",
		map[string]interface{}{"name": "my-ksa", "namespace": "default", "gcp_service_account_email": email},
		e2eTenantID, e2eK8sAccount, "k8s")

	nodes := append(append([]*core.DbNode{}, gcpNodes...), ksa)
	edges := append([]*core.DbEdge{}, gcpEdges...)

	svc := core.NewService(createTestRequestContext(e2eTenantID), e2eDiscardLogger(), nil)
	nodes, edges = mergeGraph(nodes, edges, mergeOpts{crossAccount: true, svc: svc})

	assertGoldenGraph(t, e2eGoldenPath("gcp_plus_k8s"), nodes, edges)
	if !crossAccountEdgePresent(edges, nodes, core.RelationshipAssumes, core.NodeTypeK8sServiceAccount, core.NodeTypeServiceIdentity) {
		t.Error("gcp_plus_k8s: expected cross-account ASSUMES (K8s ServiceAccount → GCP ServiceIdentity) not produced")
	}
}

// ============================================================================
// Scenario: azure_plus_k8s — Azure↔k8s CROSS-ACCOUNT via the default-relationship
// engine. The k8s_cluster_to_aks_cluster_by_name rule links a K8s Cluster to the
// Azure AKS ManagedCluster of the same name with RUNS_ON, across accounts.
// ============================================================================

func TestE2E_TierA_AzureCrossAccountK8s(t *testing.T) {
	aksID := "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.ContainerService/managedClusters/my-aks"

	azReq := &core.SourceBuildRequest{TenantID: e2eTenantID, CloudAccountID: e2eAzureAccount}
	azRes := []sources.CloudResourceRow{{
		ID: "aks-1", ResourceID: aksID, Name: "my-aks", Type: "managedclusters", ServiceName: "microsoft.containerservice/managedclusters",
		Account: "sub1", Region: "eastus", CloudProvider: "Azure", IsActive: true, ExternalResourceID: "aks-1", ARN: aksID, Meta: json.RawMessage("{}"),
	}}
	azNodes, azEdges := buildAzureGraph(t, azRes, azReq)

	// K8s Cluster named "my-aks" (derived from a k8s node's cluster_name).
	k8sReq := &core.SourceBuildRequest{TenantID: e2eTenantID, CloudAccountID: e2eK8sAccount}
	k8sNodes := []sources.K8sNodeRow{{TenantID: e2eTenantID, CloudAccountID: e2eK8sAccount, Name: "node1", IsActive: true, ClusterName: "my-aks", Meta: json.RawMessage("{}")}}
	kNodes, kEdges := buildK8sGraph(t, nil, k8sNodes, k8sReq)

	nodes := append(append([]*core.DbNode{}, azNodes...), kNodes...)
	edges := append(append([]*core.DbEdge{}, azEdges...), kEdges...)

	svc := core.NewService(createTestRequestContext(e2eTenantID), e2eDiscardLogger(), nil)
	nodes, edges = mergeGraph(nodes, edges, mergeOpts{crossAccount: true, svc: svc})

	assertGoldenGraph(t, e2eGoldenPath("azure_plus_k8s"), nodes, edges)
	if !crossAccountEdgePresent(edges, nodes, core.RelationshipRunsOn, core.NodeTypeCluster, core.NodeTypeManagedCluster) {
		t.Error("azure_plus_k8s: expected cross-account RUNS_ON (K8s Cluster → Azure AKS) not produced")
	}
}

// crossAccountEdgePresent reports whether an edge of relType exists whose source
// node is srcType and destination node is dstType, with the two endpoints in
// different cloud accounts (i.e. a genuine cross-account edge).
func crossAccountEdgePresent(edges []*core.DbEdge, nodes []*core.DbNode, relType core.RelationshipType, srcType, dstType core.NodeType) bool {
	byID := make(map[string]*core.DbNode, len(nodes))
	for _, n := range nodes {
		byID[n.ID] = n
	}
	for _, e := range edges {
		if e.RelationshipType != relType {
			continue
		}
		s, d := byID[e.SourceNodeID], byID[e.DestinationNodeID]
		if s == nil || d == nil {
			continue
		}
		if s.NodeType == srcType && d.NodeType == dstType && s.CloudAccountID != d.CloudAccountID {
			return true
		}
	}
	return false
}

// ============================================================================
// Scenario: azure_all_edges — a meta-rich Azure fixture (full Azure resource-id
// paths + nested meta.properties.*) engineered to trigger every offline-reachable
// Azure edge type: BELONGS_TO, HOSTED_ON, PROTECTS, ASSOCIATED_WITH, ROUTES_TO,
// MANAGES, RUNS_AS, HAS_ACCESS_TO, CALLS. Only DNSZone→VNet ASSOCIATED_WITH is
// CLI-only (live Resource Graph query) and out of scope.
// ============================================================================

func TestE2E_TierA_AzureAllEdges(t *testing.T) {
	req := &core.SourceBuildRequest{TenantID: e2eTenantID, CloudAccountID: e2eAzureAccount}
	resources := loadCloudResourceRows(t, filepath.Join("testdata", "e2e", "azure_all_edges", "input", "azure.json"))

	nodes, edges := buildAzureGraph(t, resources, req)
	nodes, edges = mergeGraph(nodes, edges, mergeOpts{})

	assertGoldenGraph(t, e2eGoldenPath("azure_all_edges"), nodes, edges)

	// Azure edge-type coverage gate (offline-reachable meta-driven edges).
	wantEdgeTypes := []core.RelationshipType{
		core.RelationshipBelongsTo,
		core.RelationshipHostedOn,
		core.RelationshipProtects,
		core.RelationshipAssociatedWith,
		core.RelationshipRoutesTo,
		core.RelationshipManages,
		core.RelationshipRunsAs,
		core.RelationshipHasAccessTo,
		core.RelationshipCalls,
	}
	present := make(map[core.RelationshipType]bool, len(edges))
	for _, e := range edges {
		present[e.RelationshipType] = true
	}
	for _, rt := range wantEdgeTypes {
		if !present[rt] {
			t.Errorf("azure_all_edges: expected edge type %q not produced", rt)
		}
	}
}

// ============================================================================
// Scenario: multi_account — AWS + K8s in separate accounts under one tenant,
// with the default cross-account relationship rules applied
// (BuildCrossAccountRelationships over default_relationships.json).
// ============================================================================

func TestE2E_TierA_MultiAccount(t *testing.T) {
	awsReq := &core.SourceBuildRequest{TenantID: e2eTenantID, CloudAccountID: e2eAWSAccount}
	k8sReq := &core.SourceBuildRequest{TenantID: e2eTenantID, CloudAccountID: e2eK8sAccount}

	awsNodes, awsEdges := buildAWSGraph(t, loadAWSResources(t), awsReq)
	k8sNodes, k8sEdges := buildK8sGraph(t, loadK8sWorkloads(t), loadK8sNodes(t), k8sReq)

	nodes := append(append([]*core.DbNode{}, awsNodes...), k8sNodes...)
	edges := append(append([]*core.DbEdge{}, awsEdges...), k8sEdges...)

	// Service is only used for GetDefaultRelationships (file-based) and
	// BuildCrossAccountRelationships (in-memory) — no DB access with a nil manager.
	svc := core.NewService(createTestRequestContext(e2eTenantID), e2eDiscardLogger(), nil)
	nodes, edges = mergeGraph(nodes, edges, mergeOpts{crossAccount: true, svc: svc})

	assertGoldenGraph(t, e2eGoldenPath("multi_account"), nodes, edges)
}

// ============================================================================
// Scenario: aws_all_edges — a meta-rich AWS fixture engineered to trigger as many
// distinct edge builders as possible offline (HOSTED_ON, IS_ENCRYPTED_BY,
// ROUTES_TO, BELONGS_TO, RUNS_IN, RUNS_ON, STORES_IN, PUBLISHES_TO, EMITS_LOGS_TO,
// RUNS_AS, ASSUMES, ROUTES_THROUGH, ASSOCIATED_WITH, PROTECTS). PROTECTS fires
// from the private-endpoint's own meta (Groups[]) — no live fetch needed once the
// endpoint carries VpcEndpointType="Interface". Only MANAGES is truly CLI-only
// (CloudFormation stack expansion), covered by the aws_manages fake-collector scenario.
// ============================================================================

func TestE2E_TierA_AWSAllEdges(t *testing.T) {
	req := &core.SourceBuildRequest{TenantID: e2eTenantID, CloudAccountID: e2eAWSAccount}
	resources := loadCloudResourceRows(t, filepath.Join("testdata", "e2e", "aws_all_edges", "input", "aws.json"))

	nodes, edges := buildAWSGraph(t, resources, req)
	nodes, edges = mergeGraph(nodes, edges, mergeOpts{})

	assertGoldenGraph(t, e2eGoldenPath("aws_all_edges"), nodes, edges)

	// Explicit AWS edge-type coverage gate: 14 of the 15 AWS relationship types,
	// all reachable offline (meta-driven, via the row cache — no live CLI). If a
	// converter change stops emitting one, this fails independently of the golden.
	// The only type NOT covered here requires the live cloud-collector fetch:
	//   - MANAGES:  CloudFormation stack resources (only true cloud.ExecuteCli path),
	//     covered separately by the aws_manages fake-collector scenario.
	wantEdgeTypes := []core.RelationshipType{
		core.RelationshipHostedOn,
		core.RelationshipIsEncryptedBy,
		core.RelationshipRoutesTo,
		core.RelationshipEmitsLogsTo,
		core.RelationshipRunsOn,
		core.RelationshipRunsIn,
		core.RelationshipBelongsTo,
		core.RelationshipStoresIn,
		core.RelationshipRunsAs,
		core.RelationshipAssumes,
		core.RelationshipPublishesTo,
		core.RelationshipRoutesThrough,
		core.RelationshipAssociatedWith,
		core.RelationshipProtects,
	}
	present := make(map[core.RelationshipType]bool, len(edges))
	for _, e := range edges {
		present[e.RelationshipType] = true
	}
	for _, rt := range wantEdgeTypes {
		if !present[rt] {
			t.Errorf("aws_all_edges: expected edge type %q not produced", rt)
		}
	}
}

// ============================================================================
// Scenario: aws_lb_k8s — the AWS LoadBalancer → K8s cross-source enricher. An
// ELBv2 ALB tagged kubernetes.io/service-name="default/my-svc" links to the K8s
// Service (ROUTES_TO), and an EKS ManagedCluster links to the K8s Cluster of the
// same name (MANAGES). Both fire offline (Strategy-1 tags + name match); the
// enricher's CLI/Prometheus strategies are disabled.
// ============================================================================

func TestE2E_TierA_AWSLBToK8s(t *testing.T) {
	awsReq := &core.SourceBuildRequest{TenantID: e2eTenantID, CloudAccountID: e2eAWSAccount}
	k8sReq := &core.SourceBuildRequest{TenantID: e2eTenantID, CloudAccountID: e2eK8sAccount}

	awsNodes, awsEdges := buildAWSGraph(t, loadCloudResourceRows(t, filepath.Join("testdata", "e2e", "aws_lb_k8s", "input", "aws.json")), awsReq)

	// K8s side: a Service named my-svc/default and a Cluster my-eks (from the node).
	k8sWorkloads := []sources.K8sWorkloadRow{{
		ID: "my-svc", Kind: "Service", Namespace: "default", Name: "my-svc",
		ClusterName: "my-eks", IsActive: true, Meta: json.RawMessage("{}"), Labels: json.RawMessage("{}"),
	}}
	k8sNodes := loadK8sNodeRows(t, filepath.Join("testdata", "e2e", "aws_lb_k8s", "input", "k8s_nodes.json"))
	kNodes, kEdges := buildK8sGraph(t, k8sWorkloads, k8sNodes, k8sReq)

	nodes := append(append([]*core.DbNode{}, awsNodes...), kNodes...)
	edges := append(append([]*core.DbEdge{}, awsEdges...), kEdges...)

	nodes, edges = runAWSLBK8sEnricher(t, nodes, edges, e2eTenantID)
	nodes, edges = mergeGraph(nodes, edges, mergeOpts{})

	assertGoldenGraph(t, e2eGoldenPath("aws_lb_k8s"), nodes, edges)

	// Assert the two cross-source edge types from this enricher are present.
	var routesToK8s, managesK8s bool
	for _, e := range edges {
		if e.Source == "aws_lb_k8s_enricher" {
			switch e.RelationshipType {
			case core.RelationshipRoutesTo:
				routesToK8s = true
			case core.RelationshipManages:
				managesK8s = true
			}
		}
	}
	if !routesToK8s {
		t.Error("aws_lb_k8s: expected LoadBalancer→K8sService ROUTES_TO edge not produced")
	}
	if !managesK8s {
		t.Error("aws_lb_k8s: expected EKS→K8sCluster MANAGES edge not produced")
	}
}

// ============================================================================
// Scenario: gcp_resources — the GCP converter over a representative resource set
// (Compute Engine, Cloud SQL, GKE, BigQuery, GCS, Pub/Sub, LB, VPC, etc.),
// including the Phase-6 GCP managed-service resolution pass.
// ============================================================================

func TestE2E_TierA_GCPResources(t *testing.T) {
	req := &core.SourceBuildRequest{TenantID: e2eTenantID, CloudAccountID: e2eGCPAccount}
	resources := loadGCPResources(t)

	nodes, edges := buildGCPGraph(t, resources, req)
	nodes, edges = mergeGraph(nodes, edges, mergeOpts{})

	assertGoldenGraph(t, e2eGoldenPath("gcp_resources"), nodes, edges)
}

// ============================================================================
// Scenario: k8s_plus_aws — cross-source enrichment. A K8s Node carrying an EC2
// providerID and a matching AWS ComputeInstance (in a different account) must
// produce a K8s Node → EC2 RUNS_ON edge via the k8s_ec2 enricher.
// ============================================================================

func TestE2E_TierA_K8sPlusAWS(t *testing.T) {
	inputDir := filepath.Join("testdata", "e2e", "k8s_plus_aws", "input")

	awsReq := &core.SourceBuildRequest{TenantID: e2eTenantID, CloudAccountID: e2eAWSAccount}
	awsNodes, awsEdges := buildAWSGraph(t, loadCloudResourceRows(t, filepath.Join(inputDir, "aws.json")), awsReq)

	k8sReq := &core.SourceBuildRequest{TenantID: e2eTenantID, CloudAccountID: e2eK8sAccount}
	k8sNodes, k8sEdges := buildK8sGraph(t, nil, loadK8sNodeRows(t, filepath.Join(inputDir, "k8s_nodes.json")), k8sReq)

	nodes := append(append([]*core.DbNode{}, awsNodes...), k8sNodes...)
	edges := append(append([]*core.DbEdge{}, awsEdges...), k8sEdges...)

	// Phase 2.1 cross-source enrichment, then the phase-3 merge.
	nodes, edges = runK8sEC2Enricher(t, nodes, edges, e2eTenantID)
	nodes, edges = mergeGraph(nodes, edges, mergeOpts{})

	assertGoldenGraph(t, e2eGoldenPath("k8s_plus_aws"), nodes, edges)
}

// ============================================================================
// Scenario: azure_resources — the Azure converter over a hand-authored resource
// set covering the primary node types (VM, VNet, Subnet, NSG, Storage, SQL,
// AKS, Redis, KeyVault, LoadBalancer) plus a Subnet→VNet BELONGS_TO edge.
// ============================================================================

func TestE2E_TierA_AzureResources(t *testing.T) {
	req := &core.SourceBuildRequest{TenantID: e2eTenantID, CloudAccountID: e2eAzureAccount}
	resources := loadCloudResourceRows(t, filepath.Join("testdata", "e2e", "azure_resources", "input", "azure_resources.json"))

	nodes, edges := buildAzureGraph(t, resources, req)
	nodes, edges = mergeGraph(nodes, edges, mergeOpts{})

	assertGoldenGraph(t, e2eGoldenPath("azure_resources"), nodes, edges)
}

// ============================================================================
// Scenario: gcp_all_edges — a meta-rich GCP fixture engineered to trigger every
// offline-reachable GCP edge builder (HOSTED_ON, BELONGS_TO, PROTECTS, RUNS_AS,
// USES_SECRET, CALLS, PULLS_FROM, HAS_ACCESS_TO, PUBLISHES_TO, SUBSCRIBES_TO).
// ROUTES_TO/ASSOCIATED_WITH from the GCP load-balancer chain are cliData-gated
// (live gcloud) and out of scope for the offline suite.
// ============================================================================

func TestE2E_TierA_GCPAllEdges(t *testing.T) {
	req := &core.SourceBuildRequest{TenantID: e2eTenantID, CloudAccountID: e2eGCPAccount}
	resources := loadCloudResourceRows(t, filepath.Join("testdata", "e2e", "gcp_all_edges", "input", "gcp.json"))

	nodes, edges := buildGCPGraph(t, resources, req)
	nodes, edges = mergeGraph(nodes, edges, mergeOpts{})

	assertGoldenGraph(t, e2eGoldenPath("gcp_all_edges"), nodes, edges)

	// GCP offline edge-type coverage gate (meta-driven, no gcloud CLI). The three
	// cliData-gated edges — PROTECTS (firewall), ROUTES_TO + ASSOCIATED_WITH
	// (load-balancer chain) — are covered in the mocked-infra tier
	// (TestE2E_MockedInfra_GCPFirewallProtects / GCPLoadBalancerChain), bringing
	// GCP to 12/12 relationship types overall.
	wantEdgeTypes := []core.RelationshipType{
		core.RelationshipHostedOn,
		core.RelationshipBelongsTo,
		core.RelationshipRunsAs,
		core.RelationshipUsesSecret,
		core.RelationshipPullsFrom,
		core.RelationshipCalls,
		core.RelationshipHasAccessTo,
		core.RelationshipPublishesTo,
		core.RelationshipSubscribesTo,
	}
	present := make(map[core.RelationshipType]bool, len(edges))
	for _, e := range edges {
		present[e.RelationshipType] = true
	}
	for _, rt := range wantEdgeTypes {
		if !present[rt] {
			t.Errorf("gcp_all_edges: expected edge type %q not produced", rt)
		}
	}
}

// ============================================================================
// Scenario: flow_edges — k8s topology plus injected eBPF CALLS edges, exercising
// the priority-aware edge dedup and the behavioural-edge (CALLS) path.
// ============================================================================

func TestE2E_TierA_FlowEdges(t *testing.T) {
	req := &core.SourceBuildRequest{TenantID: e2eTenantID, CloudAccountID: e2eK8sAccount}
	workloads := loadK8sWorkloads(t)
	k8sNodes := loadK8sNodes(t)

	nodes, edges := buildK8sGraph(t, workloads, k8sNodes, req)

	// Inject eBPF-derived CALLS edges between existing k8s nodes, mirroring a
	// flow source's contribution before the merge/dedup pass.
	ebpf := loadEBPFServiceMap(t)
	flowEdges := createFlowEdgesFromEBPF(t, nodes, ebpf, e2eTenantID, e2eK8sAccount)
	edges = append(edges, flowEdges...)

	nodes, edges = mergeGraph(nodes, edges, mergeOpts{})

	assertGoldenGraph(t, e2eGoldenPath("flow_edges"), nodes, edges)
}
