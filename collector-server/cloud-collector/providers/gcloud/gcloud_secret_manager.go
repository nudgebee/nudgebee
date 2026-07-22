package gcloud

import (
	"fmt"
	"strings"
	"time"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	"google.golang.org/api/iterator"

	"nudgebee/collector/cloud/providers"
)

const ServiceNameSecretManager = "Secret Manager"

// secretManagerService lists Secret Manager secrets. Secrets are global (their
// replication policy governs where the payload lives), so region is ignored — this
// mirrors the Cloud Storage / Pub/Sub global-listing pattern.
type secretManagerService struct{}

func (s *secretManagerService) GetMetrices(_ providers.CloudProviderContext, _ providers.Account, _ providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return providers.QueryMetricsResponse{}, nil
}

func (s *secretManagerService) GetResources(ctx providers.CloudProviderContext, account providers.Account, _ string) ([]providers.Resource, error) {
	session, err := getGcloudSessionFromAccount(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("failed to get gcloud session: %w", err)
	}

	client, err := secretmanager.NewClient(ctx.GetContext(), session.Opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create secret manager client: %w", err)
	}
	defer func() {
		if cerr := client.Close(); cerr != nil {
			ctx.GetLogger().Error("failed to close secret manager client", "error", cerr)
		}
	}()

	resources := []providers.Resource{}
	it := client.ListSecrets(ctx.GetContext(), &secretmanagerpb.ListSecretsRequest{
		Parent: fmt.Sprintf("projects/%s", session.ProjectId),
	})
	for {
		secret, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			if isGCPPermissionOrNotFoundError(err) {
				ctx.GetLogger().Warn("skipping Secret Manager secrets — API disabled or permission denied", "error", err)
				return resources, nil
			}
			return resources, fmt.Errorf("failed to list secrets: %w", err)
		}
		resources = append(resources, secretToResource(secret, session.ProjectId))
	}

	ctx.GetLogger().Info("fetched Secret Manager secrets", "count", len(resources))
	return resources, nil
}

func secretToResource(secret *secretmanagerpb.Secret, projectId string) providers.Resource {
	// secret.Name is "projects/{projectNumber}/secrets/{secretId}"
	parts := strings.Split(secret.GetName(), "/")
	secretID := parts[len(parts)-1]

	tags := make(map[string][]string)
	for key, value := range secret.GetLabels() {
		tags[key] = []string{value}
	}

	meta := map[string]interface{}{
		"selfLink": secret.GetName(),
	}
	if secret.GetExpireTime() != nil {
		meta["expire_time"] = secret.GetExpireTime().AsTime().String()
	}
	if r := secret.GetReplication(); r != nil {
		if r.GetAutomatic() != nil {
			meta["replication"] = "automatic"
		} else if r.GetUserManaged() != nil {
			meta["replication"] = "user-managed"
		}
	}

	var createdAt time.Time
	if secret.GetCreateTime() != nil {
		createdAt = secret.GetCreateTime().AsTime()
	}

	return providers.Resource{
		Id:          secretID,
		Name:        secretID,
		Type:        "secret-manager",
		Arn:         secret.GetName(),
		ServiceName: ServiceNameSecretManager,
		Status:      providers.ResourceStatusActive,
		Region:      "global",
		Tags:        tags,
		Meta:        meta,
		CreatedAt:   createdAt,
	}
}

func (s *secretManagerService) GetRecommendations(_ providers.CloudProviderContext, _ providers.Account, _ providers.ListRecommendationsRequest, _ []providers.Resource) ([]providers.Recommendation, error) {
	return nil, nil
}

func (s *secretManagerService) ApplyRecommendation(_ providers.CloudProviderContext, _ providers.Account, _ providers.Recommendation) error {
	return fmt.Errorf("apply recommendation not supported for Secret Manager")
}

func (s *secretManagerService) ApplyCommand(_ providers.CloudProviderContext, _ providers.Account, _ providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	return providers.ApplyCommandResponse{}, fmt.Errorf("apply command not supported for Secret Manager")
}

func (s *secretManagerService) GetLogFilter(_ providers.CloudProviderContext, _ providers.Account, _ string) string {
	return ""
}
