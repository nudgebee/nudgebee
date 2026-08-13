package gcp

import (
	"encoding/json"
	"fmt"
	"nudgebee/services/cloud"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
	"nudgebee/services/security"
	"strings"
)

// fetchCDNBackendsFromGCP fetches backend services with Cloud CDN enabled via gcloud CLI.
// Cloud CDN is configured on backend services (not as a separate resource), so we filter for
// enableCDN=true to identify CDN-fronted backends.
// fetchServerlessNEGsFromGCP lists serverless Network Endpoint Groups, which name
// the Cloud Run / App Engine service that an LB backend service ultimately fronts.
func (s *GCPSource) fetchServerlessNEGsFromGCP(reqCtx *security.RequestContext, accountID string) ([]GCPServerlessNEG, error) {
	cmd := "gcloud compute network-endpoint-groups list --format=json"
	s.logger.Info("fetching GCP serverless NEGs via CLI", "account_id", accountID)

	resp, err := cloud.ExecuteCliWithRetry(reqCtx, cloud.CloudExecuteCliCommandRequest{
		AccountID: accountID,
		Command:   cmd,
	}, 3)
	if err != nil {
		return nil, fmt.Errorf("failed to execute gcloud CLI: %w", err)
	}
	output, err := parseGCloudCLIResponse(resp)
	if err != nil {
		return nil, err
	}
	var negs []GCPServerlessNEG
	if err := json.Unmarshal([]byte(output), &negs); err != nil {
		return nil, fmt.Errorf("failed to parse network-endpoint-groups response: %w", err)
	}
	// Keep only serverless NEGs that resolve to a Cloud Run / App Engine service.
	serverless := negs[:0]
	for _, n := range negs {
		if n.CloudRun != nil || n.AppEngine != nil {
			serverless = append(serverless, n)
		}
	}
	return serverless, nil
}

// cloudRunServiceSchema is the concrete per-specific_type property schema for the
// CloudRunService specific_type.
// Names are the node.Properties keys written by createNodeFromResource +
// extractGCPCloudRunURL. dns_name (the per-service run.app host) is Indexed for
// endpoint filtering; registry_name is declared but NOT Indexed. State maps to the
// universal `status` key (see core/ontology_data_gcp.go).
var cloudRunServiceSchema = core.SpecificTypeSchema{
	SpecificType: "CloudRunService",
	NodeType:     core.NodeTypeServerlessFunction,
	Properties: []core.PropertyDef{
		{Name: "dns_name", Indexed: true},
		{Name: "registry_name"},
		// Additional provider fields (not yet emitted by our extractor):
		{Name: "description"},
		{Name: "uri"},
		{Name: "latest_ready_revision"},
		{Name: "service_account_email"},
		{Name: "project_id"},
		{Name: "ingress"},
	},
}

func init() { core.RegisterSpecificTypeSchema(cloudRunServiceSchema) }

// createCloudRunEdges creates PULLS_FROM edges from Cloud Run services to Artifact Registry
func (s *GCPSource) createCloudRunEdges(nodes []*core.DbNode, lookup *sources.NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	crNodes, ok := lookup.ByNodeType[core.NodeTypeServerlessFunction]
	if !ok {
		return edges
	}

	for _, node := range crNodes {
		registryName, _ := node.Properties["registry_name"].(string)
		if registryName == "" {
			continue
		}

		if regNode := findNodeByNameAndType(lookup, core.NodeTypeContainerRegistry, registryName); regNode != nil {
			edges = append(edges, s.createEdge(node, regNode, core.RelationshipPullsFrom,
				map[string]interface{}{"connection_type": "container_image"}, req))
			s.logger.Debug("created Cloud Run → Artifact Registry edge",
				"service", getNodeName(node), "registry", registryName)
		}
	}

	s.logger.Info("created GCP Cloud Run edges", "edge_count", len(edges))
	return edges
}

// extractArtifactRegistryName extracts the repository name from a GCP container
// image URL. Artifact Registry's API addresses a repo by
//
//	projects/{project}/locations/{location}/repositories/{repo}
//
// and the cloud-collector emits one cloud_resourses row per repo with
// Name = repo. Cloud Run pulls FROM a specific repo, not a project (a project
// commonly hosts several repos in different locations), so the cross-account
// match key has to be the repo name — not the project as the previous
// implementation assumed. Issue #31101 gap #6.
//
// Image URL format: {location}-docker.pkg.dev/{project}/{repo}/{image}@{digest}
// After ".pkg.dev/": {project}/{repo}/{image}@{digest}
// SplitN(rest, "/", 3): ["{project}", "{repo}", "{image}@{digest}"]
//
// e.g. "asia-south1-docker.pkg.dev/my-project/cloud-run-source-deploy/image@sha256:..."
//
//	→ "cloud-run-source-deploy"
func extractArtifactRegistryName(containerImage string) string {
	if containerImage == "" {
		return ""
	}
	idx := strings.Index(containerImage, ".pkg.dev/")
	if idx < 0 {
		return ""
	}
	rest := containerImage[idx+9:] // skip ".pkg.dev/"
	parts := strings.SplitN(rest, "/", 3)
	if len(parts) >= 2 && parts[1] != "" {
		return parts[1] // repo name — matches artifact-registry node name in DB
	}
	return ""
}
