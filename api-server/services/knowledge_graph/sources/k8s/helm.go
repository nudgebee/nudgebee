package k8s

import (
	"fmt"
	annotationkeys "nudgebee/services/internal/annotations"
	"nudgebee/services/knowledge_graph/core"
	"strings"
)

// Concrete-property schemas for the supply-chain / GitOps nodes. Names
// are the node.Properties keys written by createHelmChartNode /
// createRepositoryNode.
var helmChartSchema = core.SpecificTypeSchema{
	SpecificType: "HelmChart",
	NodeType:     core.NodeTypeHelmChart,
	Properties: []core.PropertyDef{
		{Name: "version", Indexed: true},
		{Name: "release_name"},
	},
}

var gitRepositorySchema = core.SpecificTypeSchema{
	SpecificType: "GitRepository",
	NodeType:     core.NodeTypeRepository,
	Properties: []core.PropertyDef{
		{Name: "url", Indexed: true},
	},
}

// HelmRelease is registered in the ontology but has NO source emitter
// yet. Its schema is declared here for registry parity so the k8s coverage test
// (every k8sOntology key → a schema) passes; it carries no resource-specific
// properties beyond the universal base keys. (ContainerImage now HAS an emitter —
// its schema + synthesizer live in container_image.go.)
var helmReleaseSchema = core.SpecificTypeSchema{
	SpecificType: "HelmRelease",
	NodeType:     core.NodeTypeHelmRelease,
	Properties:   []core.PropertyDef{},
}

func init() {
	core.RegisterSpecificTypeSchema(helmChartSchema)
	core.RegisterSpecificTypeSchema(gitRepositorySchema)
	core.RegisterSpecificTypeSchema(helmReleaseSchema)
}

// createRepositoryConnection checks for git repository annotations and creates a repository node connection
func (s *K8sSource) createRepositoryConnection(
	workload *K8sWorkloadRow,
	workloadNode *core.DbNode,
	metaMap map[string]interface{},
	repositoryNodes *map[string]*core.DbNode,
	nodes *[]*core.DbNode,
	edges *[]*core.DbEdge,
	req *core.SourceBuildRequest,
) {
	// Extract annotations from metadata
	var annotations map[string]interface{}

	// Try to get annotations from config.annotations first
	if config, ok := metaMap["config"].(map[string]interface{}); ok {
		if ann, ok := config["annotations"].(map[string]interface{}); ok {
			annotations = ann
		}
	}

	// If not found, try metadata.annotations
	if annotations == nil {
		if metadata, ok := metaMap["metadata"].(map[string]interface{}); ok {
			if ann, ok := metadata["annotations"].(map[string]interface{}); ok {
				annotations = ann
			}
		}
	}

	if annotations == nil {
		return
	}

	// Check for required git annotations
	gitHash, hasHash := annotations[annotationkeys.CIGitHash].(string)
	gitRepo, hasRepo := annotations[annotationkeys.CIGitRepo].(string)
	helmValuesPath, hasPath := annotations[annotationkeys.CIHelmValuesPath].(string)

	// All three annotations must be present
	if !hasHash || !hasRepo || !hasPath || gitHash == "" || gitRepo == "" || helmValuesPath == "" {
		return
	}

	s.logger.Info("found git repository annotations for workload",
		"workload", workload.Name,
		"namespace", workload.Namespace,
		"git_repo", gitRepo,
		"git_hash", gitHash,
		"helm_values_path", helmValuesPath)

	// Create or get repository node
	var repoNode *core.DbNode
	if existingRepo, exists := (*repositoryNodes)[gitRepo]; exists {
		repoNode = existingRepo
	} else {
		repoNode = s.createRepositoryNode(gitRepo, req)
		(*repositoryNodes)[gitRepo] = repoNode
		*nodes = append(*nodes, repoNode)
	}

	// Create edge from workload to repository
	edge := core.NewEdge(
		workloadNode.ID,
		repoNode.ID,
		core.RelationshipBuiltFrom,
		map[string]interface{}{
			"git_hash":         gitHash,
			"helm_values_path": helmValuesPath,
			"connection_type":  "repository",
		},
		req.TenantID,
		req.CloudAccountID,
		"k8s",
	)
	*edges = append(*edges, edge)

	s.logger.Info("created repository connection",
		"workload", workload.Name,
		"repository", gitRepo)
}

// createRepositoryNode creates a Repository node
func (s *K8sSource) createRepositoryNode(repoURL string, req *core.SourceBuildRequest) *core.DbNode {
	properties := map[string]interface{}{
		"url":           repoURL,
		"name":          s.extractRepoName(repoURL),
		"specific_type": "GitRepository",
	}

	// Build unique key using GenerateUniqueKey
	tempNode := &core.DbNode{
		NodeType:       core.NodeTypeRepository,
		Properties:     properties,
		CloudAccountID: req.CloudAccountID,
	}
	uniqueKey := s.GenerateUniqueKey(tempNode)

	return core.NewNode(core.NodeTypeRepository, uniqueKey, properties, req.TenantID, req.CloudAccountID, "k8s")
}

// extractRepoName extracts the repository name from a git URL
// Examples:
//   - https://github.com/org/repo.git -> repo
//   - git@github.com:org/repo.git -> repo
//   - https://github.com/org/repo -> repo
func (s *K8sSource) extractRepoName(repoURL string) string {
	// Remove .git suffix if present
	repoURL = strings.TrimSuffix(repoURL, ".git")

	// Split by / to get the last part
	parts := strings.Split(repoURL, "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}

	return repoURL
}

// createHelmConnection checks for Helm labels and creates Helm chart nodes if workload is Helm-managed
func (s *K8sSource) createHelmConnection(
	workload *K8sWorkloadRow,
	workloadNode *core.DbNode,
	metaMap map[string]interface{},
	helmChartNodes *map[string]*core.DbNode,
	repositoryNodes *map[string]*core.DbNode,
	nodes *[]*core.DbNode,
	edges *[]*core.DbEdge,
	req *core.SourceBuildRequest,
) {
	// Extract labels from metadata
	var labels map[string]interface{}

	// Try to get labels from config.labels first
	if config, ok := metaMap["config"].(map[string]interface{}); ok {
		if lbl, ok := config["labels"].(map[string]interface{}); ok {
			labels = lbl
		}
	}

	// If not found, try metadata.labels
	if labels == nil {
		if metadata, ok := metaMap["metadata"].(map[string]interface{}); ok {
			if lbl, ok := metadata["labels"].(map[string]interface{}); ok {
				labels = lbl
			}
		}
	}

	if labels == nil {
		return
	}

	// Check if workload is managed by Helm
	managedBy, hasManagedBy := labels["app.kubernetes.io/managed-by"].(string)
	if !hasManagedBy || managedBy != "Helm" {
		return
	}

	// Extract Helm metadata from labels
	releaseName, _ := labels["app.kubernetes.io/instance"].(string)
	chartName, hasChartName := labels["helm.sh/chart"].(string)

	// We need at least the chart name to create Helm nodes
	if !hasChartName || chartName == "" {
		return
	}

	s.logger.Info("found Helm-managed workload",
		"workload", workload.Name,
		"namespace", workload.Namespace,
		"release", releaseName,
		"chart", chartName)

	// Extract chart name and version from the chart label (format: chart-name-version)
	parsedChartName, chartVersion := s.parseHelmChartLabel(chartName)

	// Create or get HelmChart node and link workload directly to chart
	chartKey := fmt.Sprintf("%s/%s", parsedChartName, chartVersion)
	var helmChartNode *core.DbNode
	if existingChart, exists := (*helmChartNodes)[chartKey]; exists {
		helmChartNode = existingChart
	} else {
		helmChartNode = s.createHelmChartNode(parsedChartName, chartVersion, releaseName, req)
		(*helmChartNodes)[chartKey] = helmChartNode
		*nodes = append(*nodes, helmChartNode)

		s.logger.Info("created Helm chart node",
			"chart", parsedChartName,
			"version", chartVersion)
	}

	// Create edge from workload to Helm chart (workload is configured by chart)
	edge := core.NewEdge(
		workloadNode.ID,
		helmChartNode.ID,
		core.RelationshipIsConfiguredBy,
		map[string]interface{}{
			"connection_type": "helm_chart",
			"chart_name":      parsedChartName,
			"chart_version":   chartVersion,
			"release_name":    releaseName,
		},
		req.TenantID,
		req.CloudAccountID,
		"k8s",
	)
	*edges = append(*edges, edge)

	// Check for git repository annotations and connect Helm chart to repository
	s.connectHelmChartToRepository(workload, helmChartNode, metaMap, repositoryNodes, nodes, edges, req)
}

// connectHelmChartToRepository checks for git repository annotations and creates a connection
// between HelmChart and Repository nodes when helm.values.filePath is present
func (s *K8sSource) connectHelmChartToRepository(
	workload *K8sWorkloadRow,
	helmChartNode *core.DbNode,
	metaMap map[string]interface{},
	repositoryNodes *map[string]*core.DbNode,
	nodes *[]*core.DbNode,
	edges *[]*core.DbEdge,
	req *core.SourceBuildRequest,
) {
	// Extract annotations from metadata
	var annotations map[string]interface{}

	// Try to get annotations from config.annotations first
	if config, ok := metaMap["config"].(map[string]interface{}); ok {
		if ann, ok := config["annotations"].(map[string]interface{}); ok {
			annotations = ann
		}
	}

	// If not found, try metadata.annotations
	if annotations == nil {
		if metadata, ok := metaMap["metadata"].(map[string]interface{}); ok {
			if ann, ok := metadata["annotations"].(map[string]interface{}); ok {
				annotations = ann
			}
		}
	}

	if annotations == nil {
		return
	}

	// Check for required git annotations
	gitHash, hasHash := annotations[annotationkeys.CIGitHash].(string)
	gitRepo, hasRepo := annotations[annotationkeys.CIGitRepo].(string)
	helmValuesPath, hasPath := annotations[annotationkeys.CIHelmValuesPath].(string)

	// All three annotations must be present to create the connection
	if !hasHash || !hasRepo || !hasPath || gitHash == "" || gitRepo == "" || helmValuesPath == "" {
		return
	}

	s.logger.Info("found Helm values repository connection",
		"workload", workload.Name,
		"namespace", workload.Namespace,
		"git_repo", gitRepo,
		"git_hash", gitHash,
		"helm_values_path", helmValuesPath)

	// Create or get repository node
	var repoNode *core.DbNode
	if existingRepo, exists := (*repositoryNodes)[gitRepo]; exists {
		repoNode = existingRepo
	} else {
		repoNode = s.createRepositoryNode(gitRepo, req)
		(*repositoryNodes)[gitRepo] = repoNode
		*nodes = append(*nodes, repoNode)

		s.logger.Info("created repository node for Helm chart",
			"repository", gitRepo)
	}

	// Create edge from Helm chart to repository
	edge := core.NewEdge(
		helmChartNode.ID,
		repoNode.ID,
		core.RelationshipIsConfiguredBy,
		map[string]interface{}{
			"connection_type":  "helm_values",
			"git_hash":         gitHash,
			"helm_values_path": helmValuesPath,
			"values_file_path": helmValuesPath,
		},
		req.TenantID,
		req.CloudAccountID,
		"k8s",
	)
	*edges = append(*edges, edge)

	s.logger.Info("created Helm chart to repository connection",
		"chart", helmChartNode.Properties["name"],
		"repository", gitRepo,
		"values_path", helmValuesPath)
}

// parseHelmChartLabel parses Helm chart label (format: "chart-name-version") into name and version
// Examples:
//   - "nginx-ingress-4.0.13" -> ("nginx-ingress", "4.0.13")
//   - "prometheus-15.0.0" -> ("prometheus", "15.0.0")
//   - "my-app-1.2.3" -> ("my-app", "1.2.3")
func (s *K8sSource) parseHelmChartLabel(chartLabel string) (name string, version string) {
	// Find the last hyphen followed by version pattern (digits and dots)
	parts := strings.Split(chartLabel, "-")
	if len(parts) < 2 {
		return chartLabel, ""
	}

	// Try to identify where the version starts (last part that looks like a version)
	for i := len(parts) - 1; i >= 1; i-- {
		possibleVersion := parts[i]
		// Check if this looks like a version (starts with digit)
		if len(possibleVersion) > 0 && possibleVersion[0] >= '0' && possibleVersion[0] <= '9' {
			// This is likely the version
			version = strings.Join(parts[i:], "-")
			name = strings.Join(parts[:i], "-")
			return name, version
		}
	}

	// If no version pattern found, return the whole string as name
	return chartLabel, ""
}

// createHelmChartNode creates a HelmChart node
func (s *K8sSource) createHelmChartNode(chartName, chartVersion, releaseName string, req *core.SourceBuildRequest) *core.DbNode {
	properties := map[string]interface{}{
		"name": chartName,
	}

	if chartVersion != "" {
		properties["version"] = chartVersion
	}

	// Optionally include release name for reference
	if releaseName != "" {
		properties["release_name"] = releaseName
	}

	properties["specific_type"] = "HelmChart"

	// Build unique key using GenerateUniqueKey
	tempNode := &core.DbNode{
		NodeType:       core.NodeTypeHelmChart,
		Properties:     properties,
		CloudAccountID: req.CloudAccountID,
	}
	uniqueKey := s.GenerateUniqueKey(tempNode)

	return core.NewNode(core.NodeTypeHelmChart, uniqueKey, properties, req.TenantID, req.CloudAccountID, "k8s")
}
