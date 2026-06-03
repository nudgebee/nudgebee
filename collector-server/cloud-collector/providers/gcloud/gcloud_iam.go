package gcloud

import (
	"errors"
	"fmt"
	"nudgebee/collector/cloud/providers"
	"sync"
	"time"

	iam "google.golang.org/api/iam/v1"
)

// ServiceNameIAM is the canonical service identifier used in cloud_resourses
// rows and matched by gcpServiceAPIMap. Kept short ("IAM") so the gcp KG
// source can match on it directly when classifying SA rows as ServiceIdentity.
const ServiceNameIAM = "IAM"

// IAMServiceAccountType is the cloud_resourses.type value for every emitted
// row. Mirrors AWS's "Role"/"User" type discriminator.
const IAMServiceAccountType = "iam.googleapis.com/ServiceAccount"

// iamAnchorTTL gates the per-project "we've already scanned this cycle" check.
// IAM is global — there's no benefit to scanning per region. We use a
// short-TTL sync.Map to no-op the 2nd+ region call within the same sync
// cycle without hardcoding any single anchor region (a hardcoded
// "us-central1" anchor would silently miss IAM SAs in compliance-restricted
// projects that only scan EU/Asia regions).
const iamAnchorTTL = 1 * time.Minute

// iamService implements the gcloudService interface for GCP IAM. Today it
// only emits ServiceAccount resources — the substrate for the GKE Workload
// Identity chain (K8sServiceAccount → ASSUMES → ServiceIdentity).
type iamService struct {
	// scanned tracks the last time we listed SAs for a given project, keyed
	// by GCP project ID. Cleared automatically by TTL — the entries are
	// stale-safe (re-scanning is correct, just wasted work).
	scanned sync.Map
}

func (s *iamService) GetMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	// IAM has no Cloud Monitoring metrics worth surfacing here; identities
	// don't have time-series associated with them in the usual sense.
	return providers.QueryMetricsResponse{Items: []providers.MetricItem{}}, nil
}

func (s *iamService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
	session, err := getGcloudSessionFromAccount(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("failed to get gcloud session: %w", err)
	}

	// IAM is a global GCP service. The caller iterates regions; we no-op
	// every region after the first per (project, sync-cycle) so we don't
	// emit 30+ identical SA rows per refresh. Dynamic anchoring (whichever
	// region is scanned first) keeps the lister correct on compliance-
	// restricted projects that don't scan us-central1.
	now := time.Now()
	if last, ok := s.scanned.Load(session.ProjectId); ok {
		if t, ok := last.(time.Time); ok && now.Sub(t) < iamAnchorTTL {
			return nil, nil
		}
	}
	s.scanned.Store(session.ProjectId, now)

	svc, err := iam.NewService(ctx.GetContext(), session.Opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create IAM client: %w", err)
	}

	parent := fmt.Sprintf("projects/%s", session.ProjectId)

	var resources []providers.Resource
	err = svc.Projects.ServiceAccounts.List(parent).Pages(ctx.GetContext(), func(resp *iam.ListServiceAccountsResponse) error {
		// Defensive guards: google-api-go-client shouldn't pass nil here under
		// normal conditions, but the API client surface is external and the
		// guards are cheap.
		if resp == nil {
			return nil
		}
		for _, sa := range resp.Accounts {
			if sa == nil {
				continue
			}
			resources = append(resources, serviceAccountToResource(sa, session.ProjectId))
		}
		return nil
	})
	if err != nil {
		RecordGCPPermissionError(ctx, err)
		if isGCPPermissionOrNotFoundError(err) {
			ctx.GetLogger().Warn("skipping IAM ServiceAccounts — API disabled or permission denied", "error", err, "project", session.ProjectId)
			return nil, nil
		}
		return nil, fmt.Errorf("failed to list IAM service accounts: %w", err)
	}

	ctx.GetLogger().Info("retrieved IAM service accounts", "count", len(resources), "projectId", session.ProjectId)
	return resources, nil
}

// serviceAccountToResource flattens a GCP IAM SA into the generic Resource
// shape. The SA's `email` doubles as both Name and Id — it's the canonical
// identifier and matches the value that K8s SAs carry in the
// `iam.gke.io/gcp-service-account` Workload Identity annotation.
func serviceAccountToResource(sa *iam.ServiceAccount, projectId string) providers.Resource {
	meta := map[string]interface{}{
		"email":            sa.Email,
		"display_name":     sa.DisplayName,
		"description":      sa.Description,
		"disabled":         sa.Disabled,
		"unique_id":        sa.UniqueId,
		"oauth2_client_id": sa.Oauth2ClientId,
		"project_id":       projectId,
	}

	status := providers.ResourceStatusActive
	if sa.Disabled {
		status = providers.ResourceStatusInactive
	}

	return providers.Resource{
		Id:          sa.Email,
		Name:        sa.Email,
		Type:        IAMServiceAccountType,
		ServiceName: ServiceNameIAM,
		Status:      status,
		Region:      "global",
		Arn:         sa.Name, // projects/{p}/serviceAccounts/{email} — IAM's self-link
		Meta:        meta,
		CreatedAt:   time.Time{}, // IAM API doesn't expose creation time
	}
}

func (s *iamService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	// No recommendations for identities — IAM hygiene lives in dedicated
	// Recommender API insights, surfaced via the recommender service.
	return []providers.Recommendation{}, nil
}

func (s *iamService) ApplyRecommendation(_ providers.CloudProviderContext, _ providers.Account, recommendation providers.Recommendation) error {
	return fmt.Errorf("iam: ApplyRecommendation not implemented for rule %q", recommendation.RuleName)
}

func (s *iamService) ApplyCommand(_ providers.CloudProviderContext, _ providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	return providers.ApplyCommandResponse{}, errors.ErrUnsupported
}

func (s *iamService) GetLogFilter(_ providers.CloudProviderContext, _ providers.Account, _ string) string {
	return ""
}
