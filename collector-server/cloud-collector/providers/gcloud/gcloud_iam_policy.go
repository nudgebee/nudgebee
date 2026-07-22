package gcloud

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	cloudresourcemanager "google.golang.org/api/cloudresourcemanager/v1"

	"nudgebee/collector/cloud/providers"
)

// ServiceNameIAMPolicy is the canonical service identifier for project IAM
// policy *binding* rows (distinct from ServiceNameIAM, which emits the service
// accounts themselves). The GCP KG source folds these rows into HAS_ACCESS_TO /
// PUBLISHES_TO / SUBSCRIBES_TO edges from a ServiceIdentity to the data resource
// a role grants it access to.
const ServiceNameIAMPolicy = "IAM Policy"

// IAMBindingType is the cloud_resourses.type value for every emitted binding row.
const IAMBindingType = "cloudresourcemanager.googleapis.com/IamBinding"

// iamPolicyAnchorTTL no-ops the 2nd+ per-region GetResources call within one
// sync cycle. Project IAM policy is global (one policy per project), so we scan
// it once per (project, cycle) using dynamic anchoring — same rationale as
// iamAnchorTTL in gcloud_iam.go.
const iamPolicyAnchorTTL = 1 * time.Minute

// iamPolicyService lists project-level IAM policy bindings, emitting one row per
// (role, serviceAccount-member) pair. Non-serviceAccount members (users, groups,
// domains) are skipped: only service accounts map to KG ServiceIdentity nodes,
// and app→data-tier access always flows through a service account.
type iamPolicyService struct {
	scanned sync.Map // projectID -> time.Time of last scan
}

func (s *iamPolicyService) GetMetrices(_ providers.CloudProviderContext, _ providers.Account, _ providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return providers.QueryMetricsResponse{Items: []providers.MetricItem{}}, nil
}

func (s *iamPolicyService) GetResources(ctx providers.CloudProviderContext, account providers.Account, _ string) ([]providers.Resource, error) {
	session, err := getGcloudSessionFromAccount(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("failed to get gcloud session: %w", err)
	}

	now := time.Now()
	if last, ok := s.scanned.Load(session.ProjectId); ok {
		if t, ok := last.(time.Time); ok && now.Sub(t) < iamPolicyAnchorTTL {
			return nil, nil
		}
	}
	s.scanned.Store(session.ProjectId, now)

	crm, err := cloudresourcemanager.NewService(ctx.GetContext(), session.Opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create cloudresourcemanager client: %w", err)
	}

	policy, err := crm.Projects.GetIamPolicy(session.ProjectId, &cloudresourcemanager.GetIamPolicyRequest{}).
		Context(ctx.GetContext()).Do()
	if err != nil {
		RecordGCPPermissionError(ctx, err)
		if isGCPPermissionOrNotFoundError(err) {
			ctx.GetLogger().Warn("skipping IAM policy — API disabled or permission denied", "error", err, "project", session.ProjectId)
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get project IAM policy: %w", err)
	}

	resources := iamPolicyBindingsToResources(policy, session.ProjectId)
	ctx.GetLogger().Info("retrieved IAM policy bindings", "count", len(resources), "projectId", session.ProjectId)
	return resources, nil
}

// iamPolicyBindingsToResources flattens a project policy into one row per
// (role, serviceAccount) pair, de-duplicating repeated grants.
func iamPolicyBindingsToResources(policy *cloudresourcemanager.Policy, projectID string) []providers.Resource {
	if policy == nil {
		return nil
	}
	seen := make(map[string]struct{})
	var resources []providers.Resource
	for _, b := range policy.Bindings {
		if b == nil {
			continue
		}
		for _, member := range b.Members {
			email, ok := serviceAccountMemberEmail(member)
			if !ok {
				continue
			}
			key := b.Role + "|" + email
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			resources = append(resources, iamBindingToResource(b.Role, email, projectID))
		}
	}
	return resources
}

// serviceAccountMemberEmail extracts the SA email from an IAM policy member
// string, returning ok=false for non-serviceAccount members. Handles the
// "deleted:serviceAccount:email?uid=..." tombstone form defensively by ignoring it.
func serviceAccountMemberEmail(member string) (string, bool) {
	const prefix = "serviceAccount:"
	if !strings.HasPrefix(member, prefix) {
		return "", false
	}
	email := strings.TrimPrefix(member, prefix)
	if email == "" || strings.Contains(email, "?") {
		return "", false
	}
	return email, true
}

func iamBindingToResource(role, email, projectID string) providers.Resource {
	meta := map[string]interface{}{
		"role":        role,
		"member":      email,
		"member_type": "serviceAccount",
		"project_id":  projectID,
	}
	return providers.Resource{
		Id:          role + "|" + email,
		Name:        email,
		Type:        IAMBindingType,
		ServiceName: ServiceNameIAMPolicy,
		Status:      providers.ResourceStatusActive,
		Region:      "global",
		Arn:         fmt.Sprintf("projects/%s", projectID),
		Meta:        meta,
		CreatedAt:   time.Time{},
	}
}

func (s *iamPolicyService) GetRecommendations(_ providers.CloudProviderContext, _ providers.Account, _ providers.ListRecommendationsRequest, _ []providers.Resource) ([]providers.Recommendation, error) {
	return []providers.Recommendation{}, nil
}

func (s *iamPolicyService) ApplyRecommendation(_ providers.CloudProviderContext, _ providers.Account, recommendation providers.Recommendation) error {
	return fmt.Errorf("iam policy: ApplyRecommendation not implemented for rule %q", recommendation.RuleName)
}

func (s *iamPolicyService) ApplyCommand(_ providers.CloudProviderContext, _ providers.Account, _ providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	return providers.ApplyCommandResponse{}, errors.ErrUnsupported
}

func (s *iamPolicyService) GetLogFilter(_ providers.CloudProviderContext, _ providers.Account, _ string) string {
	return ""
}
