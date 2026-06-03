package gcloud

import (
	"errors"
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"
	"sync"
	"time"

	artifactregistry "google.golang.org/api/artifactregistry/v1"
)

// ServiceNameArtifactRegistry matches the existing gcp_source.go classifier
// entry for `Artifact Registry` → NodeTypeContainerRegistry. Kept as a const
// so registration in gcloudServiceMap and the per-row ServiceName field can't
// drift apart silently.
const ServiceNameArtifactRegistry = "Artifact Registry"

// ArtifactRegistryRepoType is the cloud_resourses.type value the gcp_source
// classifier already maps to NodeTypeContainerRegistry (see gcpResourceTypeMap
// entry "artifact-registry").
const ArtifactRegistryRepoType = "artifact-registry"

// arAnchorTTL gates the per-project "we've already scanned this cycle" check.
// AR is queried with parent="projects/<p>/locations/-" so a single API call
// returns every region's repos. We use a short-TTL sync.Map to no-op the
// 2nd+ region call within the same sync cycle without hardcoding any single
// anchor region (a hardcoded "us-central1" anchor would silently miss AR
// repos in compliance-restricted projects that only scan EU/Asia regions).
const arAnchorTTL = 1 * time.Minute

// artifactRegistryService implements gcloudService for GCP Artifact Registry.
// Each Docker/Maven/etc. repository is emitted as a single ContainerRegistry
// node so that Cloud Run / Workload PULLS_FROM edges can target the right
// repo (a project typically owns multiple repos spread across regions).
type artifactRegistryService struct {
	// scanned tracks the last time we listed repos for a given project,
	// keyed by GCP project ID. Cleared automatically by TTL — re-scanning
	// is correct, just wasted work.
	scanned sync.Map
}

func (s *artifactRegistryService) GetMetrices(_ providers.CloudProviderContext, _ providers.Account, _ providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return providers.QueryMetricsResponse{Items: []providers.MetricItem{}}, nil
}

func (s *artifactRegistryService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
	session, err := getGcloudSessionFromAccount(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("failed to get gcloud session: %w", err)
	}

	// Artifact Registry repositories are addressed by (project, location).
	// Use a single anchor call per refresh and have the API server-side list
	// every location at once via `parent="projects/<p>/locations/-"`. Iterating
	// our own region list would multiply API calls by ~30 with no extra data.
	// Dynamic anchoring (whichever region is scanned first) keeps the lister
	// correct on compliance-restricted projects that don't scan us-central1.
	now := time.Now()
	if last, ok := s.scanned.Load(session.ProjectId); ok {
		if t, ok := last.(time.Time); ok && now.Sub(t) < arAnchorTTL {
			return nil, nil
		}
	}
	s.scanned.Store(session.ProjectId, now)

	svc, err := artifactregistry.NewService(ctx.GetContext(), session.Opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create artifactregistry client: %w", err)
	}

	parent := fmt.Sprintf("projects/%s/locations/-", session.ProjectId)
	var resources []providers.Resource
	err = svc.Projects.Locations.Repositories.List(parent).Pages(ctx.GetContext(), func(resp *artifactregistry.ListRepositoriesResponse) error {
		for _, repo := range resp.Repositories {
			resources = append(resources, repositoryToResource(repo, session.ProjectId))
		}
		return nil
	})
	if err != nil {
		RecordGCPPermissionError(ctx, err)
		if isGCPPermissionOrNotFoundError(err) {
			ctx.GetLogger().Warn("skipping Artifact Registry — API disabled or permission denied", "error", err, "project", session.ProjectId)
			return nil, nil
		}
		return nil, fmt.Errorf("failed to list artifact registry repositories: %w", err)
	}

	ctx.GetLogger().Info("retrieved Artifact Registry repositories", "count", len(resources), "projectId", session.ProjectId)
	return resources, nil
}

// repositoryToResource flattens an Artifact Registry repo into a Resource row.
// The short repo name is used as Name (and the unique key suffix) so the
// cross-account matcher can join on it — that's the same segment Cloud Run
// container image URLs reference (e.g. `<loc>-docker.pkg.dev/<proj>/<repo>/<img>`).
//
// Repo full-name format: projects/{project}/locations/{location}/repositories/{name}
func repositoryToResource(repo *artifactregistry.Repository, projectID string) providers.Resource {
	parts := strings.Split(repo.Name, "/")
	repoName := parts[len(parts)-1]

	location := ""
	for i, p := range parts {
		if p == "locations" && i+1 < len(parts) {
			location = parts[i+1]
			break
		}
	}

	meta := map[string]interface{}{
		"full_name":           repo.Name,
		"format":              repo.Format, // DOCKER, MAVEN, NPM, etc.
		"location":            location,
		"project_id":          projectID,
		"kms_key_name":        repo.KmsKeyName,
		"mode":                repo.Mode,
		"description":         repo.Description,
		"size_bytes":          repo.SizeBytes,
		"create_time":         repo.CreateTime,
		"update_time":         repo.UpdateTime,
		"cleanup_policy_dry":  repo.CleanupPolicyDryRun,
		"satisfies_pzi":       repo.SatisfiesPzi,
		"satisfies_pzs":       repo.SatisfiesPzs,
		"vulnerability_scan":  repo.VulnerabilityScanningConfig,
		"docker_config":       repo.DockerConfig,
		"maven_config":        repo.MavenConfig,
		"virtual_repo_config": repo.VirtualRepositoryConfig,
	}

	labels := make(map[string][]string, len(repo.Labels))
	for k, v := range repo.Labels {
		labels[k] = []string{v}
	}

	return providers.Resource{
		Id:          repo.Name,
		Name:        repoName,
		Type:        ArtifactRegistryRepoType,
		ServiceName: ServiceNameArtifactRegistry,
		Status:      providers.ResourceStatusActive,
		Region:      location,
		Arn:         repo.Name,
		Tags:        labels,
		Meta:        meta,
		CreatedAt:   time.Time{}, // create_time is on the meta if needed
	}
}

func (s *artifactRegistryService) GetRecommendations(_ providers.CloudProviderContext, _ providers.Account, _ providers.ListRecommendationsRequest, _ []providers.Resource) ([]providers.Recommendation, error) {
	return []providers.Recommendation{}, nil
}

func (s *artifactRegistryService) ApplyRecommendation(_ providers.CloudProviderContext, _ providers.Account, recommendation providers.Recommendation) error {
	return fmt.Errorf("artifact registry: ApplyRecommendation not implemented for rule %q", recommendation.RuleName)
}

func (s *artifactRegistryService) ApplyCommand(_ providers.CloudProviderContext, _ providers.Account, _ providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	return providers.ApplyCommandResponse{}, errors.ErrUnsupported
}

func (s *artifactRegistryService) GetLogFilter(_ providers.CloudProviderContext, _ providers.Account, _ string) string {
	return ""
}
