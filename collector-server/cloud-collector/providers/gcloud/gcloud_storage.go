package gcloud

import (
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	"cloud.google.com/go/storage"
	"google.golang.org/api/iterator"
)

const (
	ServiceNameStorage = "Cloud Storage"
)

type cloudStorageService struct{}

func (s *cloudStorageService) GetMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	// Query Cloud Monitoring metrics for Cloud Storage
	// Common metrics: api/request_count, network/received_bytes_count, network/sent_bytes_count,
	// storage/total_bytes, storage/object_count
	return getGcloudMonitoringMetrics(ctx, account, filter)
}

func (s *cloudStorageService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
	session, err := getGcloudSessionFromAccount(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("failed to get gcloud session: %w", err)
	}

	client, err := storage.NewClient(ctx.GetContext(), session.Opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create storage client: %w", err)
	}

	defer func() {
		if cerr := client.Close(); cerr != nil {
			ctx.GetLogger().Error("failed to close google cloud storage client", "error", cerr)
		}
	}()

	var resources []providers.Resource

	// List all buckets in the project
	// Cloud Storage buckets are global resources with location constraints
	// We fetch all buckets once and return them with their actual location
	it := client.Buckets(ctx.GetContext(), session.ProjectId)
	for {
		bucketAttrs, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to list storage buckets: %w", err)
		}

		// No region filtering - return all buckets with their actual location
		// The bucketToResource function will set the correct region based on bucket.Location
		resource := s.bucketToResource(bucketAttrs, session.ProjectId)
		resources = append(resources, resource)
	}

	ctx.GetLogger().Info("fetched cloud storage buckets", "count", len(resources), "region", region)
	return resources, nil
}

func (s *cloudStorageService) bucketToResource(bucket *storage.BucketAttrs, projectId string) providers.Resource {
	// Use bucket name as resource ID (matches GCP Monitoring bucket_name label)
	resourceId := bucket.Name

	// Store full path in metadata for reference
	selfLink := fmt.Sprintf("projects/%s/buckets/%s", projectId, bucket.Name)

	// Extract labels
	tags := make(map[string][]string)
	if bucket.Labels != nil {
		for key, value := range bucket.Labels {
			tags[key] = []string{value}
		}
	}

	// Cloud Storage buckets are always active if they exist
	status := providers.ResourceStatusActive

	// Extract creation timestamp
	createdAt := bucket.Created

	// Convert bucket to map for Meta field
	meta := structToMap(bucket)
	meta["selfLink"] = selfLink

	// Normalize location to region format
	region := normalizeGCPLocation(bucket.Location)

	return providers.Resource{
		Id:          resourceId, // Just bucket name (matches GCP Monitoring bucket_name)
		Name:        bucket.Name,
		Type:        "storage.googleapis.com/Bucket",
		Arn:         selfLink, // Full path for ARN
		ServiceName: ServiceNameStorage,
		Status:      status,
		Region:      region,
		Tags:        tags,
		Meta:        meta,
		CreatedAt:   createdAt,
	}
}

func normalizeGCPLocation(location string) string {
	// Cloud Storage locations can be:
	// - Regional: us-central1, europe-west1
	// - Multi-regional: us, eu, asia
	// - Dual-region: nam4, eur4
	// Return as-is, converted to lowercase for consistency
	return strings.ToLower(location)
}

func (s *cloudStorageService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	recommendations := []providers.Recommendation{}

	for _, resource := range existingResources {
		if resource.ServiceName != ServiceNameStorage {
			continue
		}

		// Recommendation 1: Check for buckets without labels
		if len(resource.Tags) == 0 {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName: providers.RecommendationCategoryConfiguration,
				RuleName:     "gcp_storage_no_labels",
				Severity:     providers.RecommendationSeverityLow,
				Savings:      0,
				Data: map[string]any{
					"bucket_id":   resource.Id,
					"bucket_name": resource.Name,
					"region":      resource.Region,
				},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// Recommendation 2: Check for public access
		if meta, ok := resource.Meta["PredefinedACL"].(string); ok {
			if strings.Contains(meta, "public") || strings.Contains(meta, "allUsers") {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName: providers.RecommendationCategorySecurity,
					RuleName:     "gcp_storage_public_access",
					Severity:     providers.RecommendationSeverityHigh,
					Savings:      0,
					Data: map[string]any{
						"bucket_id":   resource.Id,
						"bucket_name": resource.Name,
						"region":      resource.Region,
						"acl":         meta,
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}
		}

		// Recommendation 3: Check versioning
		if meta, ok := resource.Meta["Versioning"].(map[string]interface{}); ok {
			if enabled, ok := meta["Enabled"].(bool); !ok || !enabled {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName: providers.RecommendationCategoryConfiguration,
					RuleName:     "gcp_storage_no_versioning",
					Severity:     providers.RecommendationSeverityMedium,
					Savings:      0,
					Data: map[string]any{
						"bucket_id":   resource.Id,
						"bucket_name": resource.Name,
						"region":      resource.Region,
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}
		}

		// Recommendation 4: Check lifecycle management
		if meta, ok := resource.Meta["Lifecycle"].(map[string]interface{}); ok {
			if rules, ok := meta["Rules"].([]interface{}); !ok || len(rules) == 0 {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName: providers.RecommendationCategoryConfiguration,
					RuleName:     "gcp_storage_no_lifecycle",
					Severity:     providers.RecommendationSeverityMedium,
					Savings:      0,
					Data: map[string]any{
						"bucket_id":   resource.Id,
						"bucket_name": resource.Name,
						"region":      resource.Region,
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}
		}

		// Recommendation 5: Check storage class
		if storageClass, ok := resource.Meta["StorageClass"].(string); ok {
			if storageClass == "STANDARD" {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName: providers.RecommendationCategoryRightSizing,
					RuleName:     "gcp_storage_class_optimization",
					Severity:     providers.RecommendationSeverityMedium,
					Savings:      0,
					Data: map[string]any{
						"bucket_id":             resource.Id,
						"bucket_name":           resource.Name,
						"region":                resource.Region,
						"current_storage_class": storageClass,
						"recommended_classes":   []string{"NEARLINE", "COLDLINE", "ARCHIVE"},
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}
		}

		// Recommendation 6: Check encryption
		if meta, ok := resource.Meta["Encryption"].(map[string]interface{}); ok {
			if defaultKMSKeyName, ok := meta["DefaultKMSKeyName"].(string); !ok || defaultKMSKeyName == "" {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName: providers.RecommendationCategorySecurity,
					RuleName:     "gcp_storage_no_cmek",
					Severity:     providers.RecommendationSeverityLow,
					Savings:      0,
					Data: map[string]any{
						"bucket_id":   resource.Id,
						"bucket_name": resource.Name,
						"region":      resource.Region,
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}
		}

		// Recommendation 7: Check uniform bucket-level access
		if meta, ok := resource.Meta["UniformBucketLevelAccess"].(map[string]interface{}); ok {
			if enabled, ok := meta["Enabled"].(bool); !ok || !enabled {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName: providers.RecommendationCategorySecurity,
					RuleName:     "gcp_storage_no_ubla",
					Severity:     providers.RecommendationSeverityMedium,
					Savings:      0,
					Data: map[string]any{
						"bucket_id":   resource.Id,
						"bucket_name": resource.Name,
						"region":      resource.Region,
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}
		}

		// Recommendation 8: Check for old buckets
		if time.Since(resource.CreatedAt) > 180*24*time.Hour {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName: providers.RecommendationCategoryInfraUpgrade,
				RuleName:     "gcp_storage_old_bucket",
				Severity:     providers.RecommendationSeverityLow,
				Savings:      0,
				Data: map[string]any{
					"bucket_id":   resource.Id,
					"bucket_name": resource.Name,
					"region":      resource.Region,
					"age_days":    int(time.Since(resource.CreatedAt).Hours() / 24),
				},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// Recommendation 9: Check logging
		if meta, ok := resource.Meta["Logging"].(map[string]interface{}); !ok || meta == nil {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName: providers.RecommendationCategorySecurity,
				RuleName:     "gcp_storage_no_logging",
				Severity:     providers.RecommendationSeverityLow,
				Savings:      0,
				Data: map[string]any{
					"bucket_id":   resource.Id,
					"bucket_name": resource.Name,
					"region":      resource.Region,
				},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}
	}

	return recommendations, nil
}

func (s *cloudStorageService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	// Check if this is an alarm/alert policy recommendation
	if _, ok := recommendation.Data["alarm_config"]; ok {
		ctx.GetLogger().Info("gcp: applying alarm recommendation for gcloud_storage",
			"rule_name", recommendation.RuleName,
			"resource_id", recommendation.ResourceId)
		return CreateGCPAlertPolicyFromRecommendation(ctx, account, recommendation)
	}

	session, err := getGcloudSessionFromAccount(ctx, account)
	if err != nil {
		return fmt.Errorf("failed to get gcloud session: %w", err)
	}

	client, err := storage.NewClient(ctx.GetContext(), session.Opts...)
	if err != nil {
		return fmt.Errorf("failed to create storage client: %w", err)
	}
	defer func() {
		if cerr := client.Close(); cerr != nil {
			ctx.GetLogger().Error("failed to close google cloud storage client", "error", cerr)
		}
	}()
	bucketName, ok := recommendation.Data["bucket_name"].(string)
	if !ok || bucketName == "" {
		return fmt.Errorf("bucket_name not found in recommendation data")
	}

	bucket := client.Bucket(bucketName)

	switch recommendation.RuleName {
	case "gcp_storage_no_labels":
		return fmt.Errorf("automatic label addition not yet implemented - please add labels manually via GCP console or gsutil")

	case "gcp_storage_public_access":
		return fmt.Errorf("automatic access control modification requires careful review - please update bucket ACLs manually via GCP console")

	case "gcp_storage_no_versioning":
		// Enable versioning
		attrs := storage.BucketAttrsToUpdate{
			VersioningEnabled: true,
		}
		_, err := bucket.Update(ctx.GetContext(), attrs)
		if err != nil {
			return fmt.Errorf("failed to enable versioning: %w", err)
		}

		ctx.GetLogger().Info("successfully enabled versioning", "bucket", bucketName)
		return nil

	case "gcp_storage_no_lifecycle":
		return fmt.Errorf("automatic lifecycle configuration requires custom rules - please configure lifecycle manually via GCP console")

	case "gcp_storage_class_optimization":
		return fmt.Errorf("storage class optimization requires analyzing access patterns - please review and update manually")

	case "gcp_storage_no_cmek":
		return fmt.Errorf("CMEK configuration requires KMS key setup - please configure CMEK manually via GCP console")

	case "gcp_storage_no_ubla":
		// Enable uniform bucket-level access
		attrs := storage.BucketAttrsToUpdate{
			UniformBucketLevelAccess: &storage.UniformBucketLevelAccess{
				Enabled: true,
			},
		}
		_, err := bucket.Update(ctx.GetContext(), attrs)
		if err != nil {
			return fmt.Errorf("failed to enable uniform bucket-level access: %w", err)
		}

		ctx.GetLogger().Info("successfully enabled uniform bucket-level access", "bucket", bucketName)
		return nil

	case "gcp_storage_old_bucket":
		return fmt.Errorf("bucket deletion requires manual review - please review bucket contents and delete manually if not needed")

	case "gcp_storage_no_logging":
		return fmt.Errorf("automatic logging configuration requires destination bucket - please configure logging manually via GCP console")

	default:
		return fmt.Errorf("unknown recommendation rule: %s", recommendation.RuleName)
	}
}

func (s *cloudStorageService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	session, err := getGcloudSessionFromAccount(ctx, account)
	if err != nil {
		return providers.ApplyCommandResponse{}, fmt.Errorf("failed to get gcloud session: %w", err)
	}

	client, err := storage.NewClient(ctx.GetContext(), session.Opts...)
	if err != nil {
		return providers.ApplyCommandResponse{}, fmt.Errorf("failed to create storage client: %w", err)
	}
	defer func() {
		if cerr := client.Close(); cerr != nil {
			ctx.GetLogger().Error("failed to close google cloud storage client", "error", cerr)
		}
	}()
	switch command.Command {
	case "create":
		bucketName, ok := command.Args["bucket_name"].(string)
		if !ok || bucketName == "" {
			return providers.ApplyCommandResponse{}, fmt.Errorf("bucket_name arg required")
		}

		location, ok := command.Args["location"].(string)
		if !ok || location == "" {
			location = "US" // Default to US multi-region
		}

		bucket := client.Bucket(bucketName)
		attrs := &storage.BucketAttrs{
			Location: location,
		}

		err := bucket.Create(ctx.GetContext(), session.ProjectId, attrs)
		if err != nil {
			return providers.ApplyCommandResponse{}, fmt.Errorf("failed to create bucket: %w", err)
		}

		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("Successfully created bucket %s in location %s", bucketName, location),
		}, nil

	case "delete":
		bucketName, ok := command.Args["bucket_name"].(string)
		if !ok || bucketName == "" {
			return providers.ApplyCommandResponse{}, fmt.Errorf("bucket_name arg required")
		}

		bucket := client.Bucket(bucketName)
		err := bucket.Delete(ctx.GetContext())
		if err != nil {
			return providers.ApplyCommandResponse{}, fmt.Errorf("failed to delete bucket: %w", err)
		}

		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("Successfully deleted bucket %s", bucketName),
		}, nil

	case "enable_versioning":
		bucketName, ok := command.Args["bucket_name"].(string)
		if !ok || bucketName == "" {
			return providers.ApplyCommandResponse{}, fmt.Errorf("bucket_name arg required")
		}

		bucket := client.Bucket(bucketName)
		attrs := storage.BucketAttrsToUpdate{
			VersioningEnabled: true,
		}
		_, err := bucket.Update(ctx.GetContext(), attrs)
		if err != nil {
			return providers.ApplyCommandResponse{}, fmt.Errorf("failed to enable versioning: %w", err)
		}

		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("Successfully enabled versioning for bucket %s", bucketName),
		}, nil

	case "disable_versioning":
		bucketName, ok := command.Args["bucket_name"].(string)
		if !ok || bucketName == "" {
			return providers.ApplyCommandResponse{}, fmt.Errorf("bucket_name arg required")
		}

		bucket := client.Bucket(bucketName)
		versioningDisabled := false
		attrs := storage.BucketAttrsToUpdate{
			VersioningEnabled: versioningDisabled,
		}
		_, err := bucket.Update(ctx.GetContext(), attrs)
		if err != nil {
			return providers.ApplyCommandResponse{}, fmt.Errorf("failed to disable versioning: %w", err)
		}

		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("Successfully disabled versioning for bucket %s", bucketName),
		}, nil

	default:
		return providers.ApplyCommandResponse{}, fmt.Errorf("unsupported command: %s", command.Command)
	}
}

func (s *cloudStorageService) GetLogFilter(_ providers.CloudProviderContext, _ providers.Account, _ string) string {
	return ""
}
