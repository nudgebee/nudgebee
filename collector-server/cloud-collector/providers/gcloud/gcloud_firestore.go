package gcloud

import (
	"fmt"
	"strings"

	firestore "cloud.google.com/go/firestore/apiv1/admin"
	"cloud.google.com/go/firestore/apiv1/admin/adminpb"

	"nudgebee/collector/cloud/providers"
)

const ServiceNameFirestore = "Firestore"

// firestoreService lists Firestore / Datastore-mode databases. Databases are
// project-scoped (with a location), so region is ignored (mirrors the global
// listing pattern). This is the operational database + CDC source for GCP apps.
type firestoreService struct{}

func (s *firestoreService) GetMetrices(_ providers.CloudProviderContext, _ providers.Account, _ providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return providers.QueryMetricsResponse{}, nil
}

func (s *firestoreService) GetResources(ctx providers.CloudProviderContext, account providers.Account, _ string) ([]providers.Resource, error) {
	session, err := getGcloudSessionFromAccount(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("failed to get gcloud session: %w", err)
	}

	client, err := firestore.NewFirestoreAdminClient(ctx.GetContext(), session.Opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create firestore admin client: %w", err)
	}
	defer func() {
		if cerr := client.Close(); cerr != nil {
			ctx.GetLogger().Error("failed to close firestore admin client", "error", cerr)
		}
	}()

	resp, err := client.ListDatabases(ctx.GetContext(), &adminpb.ListDatabasesRequest{
		Parent: fmt.Sprintf("projects/%s", session.ProjectId),
	})
	if err != nil {
		if isGCPPermissionOrNotFoundError(err) {
			ctx.GetLogger().Warn("skipping Firestore databases — API disabled or permission denied", "error", err)
			return nil, nil
		}
		return nil, fmt.Errorf("failed to list firestore databases: %w", err)
	}

	resources := make([]providers.Resource, 0, len(resp.GetDatabases()))
	for _, db := range resp.GetDatabases() {
		resources = append(resources, firestoreDatabaseToResource(db))
	}
	ctx.GetLogger().Info("fetched Firestore databases", "count", len(resources))
	return resources, nil
}

func firestoreDatabaseToResource(db *adminpb.Database) providers.Resource {
	// db.Name is "projects/{project}/databases/{database}" (default DB id is "(default)").
	parts := strings.Split(db.GetName(), "/")
	dbID := parts[len(parts)-1]

	meta := map[string]interface{}{
		"selfLink":      db.GetName(),
		"database_type": db.GetType().String(), // FIRESTORE_NATIVE or DATASTORE_MODE
		"location_id":   db.GetLocationId(),
	}

	return providers.Resource{
		Id:          dbID,
		Name:        dbID,
		Type:        "firestore",
		Arn:         db.GetName(),
		ServiceName: ServiceNameFirestore,
		Status:      providers.ResourceStatusActive,
		Region:      db.GetLocationId(),
		Tags:        map[string][]string{},
		Meta:        meta,
	}
}

func (s *firestoreService) GetRecommendations(_ providers.CloudProviderContext, _ providers.Account, _ providers.ListRecommendationsRequest, _ []providers.Resource) ([]providers.Recommendation, error) {
	return nil, nil
}

func (s *firestoreService) ApplyRecommendation(_ providers.CloudProviderContext, _ providers.Account, _ providers.Recommendation) error {
	return fmt.Errorf("apply recommendation not supported for Firestore")
}

func (s *firestoreService) ApplyCommand(_ providers.CloudProviderContext, _ providers.Account, _ providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	return providers.ApplyCommandResponse{}, fmt.Errorf("apply command not supported for Firestore")
}

func (s *firestoreService) GetLogFilter(_ providers.CloudProviderContext, _ providers.Account, _ string) string {
	return ""
}
