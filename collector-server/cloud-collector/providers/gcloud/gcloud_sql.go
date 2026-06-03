package gcloud

import (
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	"cloud.google.com/go/cloudsqlconn"
	sqladmin "google.golang.org/api/sqladmin/v1"
)

const (
	ServiceNameSQL = "Cloud SQL"
)

type cloudSQLService struct{}

func (s *cloudSQLService) GetMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	// Query Cloud Monitoring metrics for Cloud SQL
	// Common metrics: cpu/utilization, memory/utilization, disk/bytes_used,
	// network/connections, network/received_bytes_count, network/sent_bytes_count
	return getGcloudMonitoringMetrics(ctx, account, filter)
}

func (s *cloudSQLService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
	session, err := getGcloudSessionFromAccount(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("failed to get gcloud session: %w", err)
	}

	service, err := sqladmin.NewService(ctx.GetContext(), session.Opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create Cloud SQL admin service: %w", err)
	}

	var resources []providers.Resource

	// List all database instances in the project
	req := service.Instances.List(session.ProjectId)
	resp, err := req.Do()
	if err != nil {
		return nil, fmt.Errorf("failed to list Cloud SQL instances: %w", err)
	}

	for _, instance := range resp.Items {
		// Filter by region if specified
		if region != "" && region != "global" {
			// Cloud SQL instance region format: us-central1, europe-west1, etc.
			if !strings.HasPrefix(instance.Region, region) {
				continue
			}
		}

		resource := s.instanceToResource(instance, session.ProjectId)
		resources = append(resources, resource)
	}

	return resources, nil
}

func (s *cloudSQLService) instanceToResource(instance *sqladmin.DatabaseInstance, projectId string) providers.Resource {
	// Use GCP Monitoring API format: "project:instance-name"
	// This matches the database_id label used by GCP Monitoring API
	resourceId := fmt.Sprintf("%s:%s", projectId, instance.Name)

	// Store selfLink in metadata for reference
	selfLink := fmt.Sprintf("projects/%s/instances/%s", projectId, instance.Name)
	if instance.SelfLink != "" {
		selfLink = instance.SelfLink
	}

	// Extract tags/labels
	tags := make(map[string][]string)
	if instance.Settings != nil && instance.Settings.UserLabels != nil {
		for key, value := range instance.Settings.UserLabels {
			tags[key] = []string{value}
		}
	}

	// Determine status
	status := gcpSQLStatusToNbStatus(instance.State)

	// Extract creation timestamp
	createdAt := time.Now()
	if instance.CreateTime != "" {
		if parsed, err := time.Parse(time.RFC3339, instance.CreateTime); err == nil {
			createdAt = parsed
		}
	}

	// Convert instance to map for Meta field
	meta := structToMap(instance)
	meta["selfLink"] = selfLink

	resourceType := "sqladmin.googleapis.com/Instance"

	return providers.Resource{
		Id:          resourceId, // "project:instance-name" (matches GCP Monitoring database_id)
		Name:        instance.Name,
		Type:        resourceType,
		Arn:         selfLink, // Full selfLink for ARN
		ServiceName: ServiceNameSQL,
		Status:      status,
		Region:      instance.Region,
		Tags:        tags,
		Meta:        meta,
		CreatedAt:   createdAt,
	}
}

func gcpSQLStatusToNbStatus(state string) providers.ResourceStatus {
	switch state {
	case "RUNNABLE":
		return providers.ResourceStatusActive
	case "SUSPENDED", "STOPPED":
		return providers.ResourceStatusInactive
	case "PENDING_CREATE", "MAINTENANCE", "UNKNOWN_STATE":
		return providers.ResourceStatusActive
	case "FAILED":
		return providers.ResourceStatusInactive
	default:
		return providers.ResourceStatusUnknown
	}
}

func (s *cloudSQLService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	recommendations := []providers.Recommendation{}

	// Load GCP alarm templates for Cloud SQL
	sqlAlarmTemplates, err := LoadGCPAlarmTemplates("Cloud SQL")
	if err != nil {
		ctx.GetLogger().Warn("Failed to load GCP Cloud SQL alarm templates", "error", err)
		sqlAlarmTemplates = []providers.AlarmTemplate{} // Continue with other recommendations
	}

	for _, resource := range existingResources {
		if resource.ServiceName != ServiceNameSQL {
			continue
		}

		// Check for missing Cloud Monitoring alert policies
		for _, template := range sqlAlarmTemplates {
			// Check if alarm is missing
			resourceFilter := fmt.Sprintf("resource.type=\"cloudsql_database\" AND resource.labels.database_id=\"%s:%s\"",
				account.AccountNumber, resource.Name)
			isMissing, err := IsAlarmMissing(resource, template, resourceFilter)
			if err != nil {
				ctx.GetLogger().Warn("Failed to check if alarm is missing", "error", err, "template", template.Name)
				continue
			}

			if !isMissing {
				// Alarm already exists, skip
				continue
			}

			// Calculate threshold using helper function that considers instance-specific rules
			threshold := calculateGCPSQLThreshold(resource, template)

			// Build alarm configuration for the recommendation data
			alarmConfig := buildGCPAlarmConfig(resource, template, threshold, []providers.AlarmDimension{
				{Name: "database_id", Value: fmt.Sprintf("%s:%s", account.AccountNumber, resource.Name)},
			})

			// Create recommendation
			recommendation := providers.Recommendation{
				CategoryName: providers.RecommendationCategoryConfiguration,
				RuleName:     template.Name,
				Severity:     providers.RecommendationSeverityFromString(template.Severity),
				Savings:      0,
				Data: map[string]any{
					"instance_id":     resource.Id,
					"instance_name":   resource.Name,
					"instance_region": resource.Region,
					"database_type":   resource.Meta["databaseVersion"],
					"metric_name":     template.Configuration.MetricName,
					"threshold":       threshold,
					"alarm_config":    alarmConfig,
					"alarm_type":      template.AlarmType,
					"reason":          template.Description,
					"project_id":      account.AccountNumber,
				},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			}
			recommendations = append(recommendations, recommendation)
		}

		// Recommendation 1: Check for instances without labels
		if len(resource.Tags) == 0 {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName: providers.RecommendationCategoryConfiguration,
				RuleName:     "gcp_sql_no_labels",
				Severity:     providers.RecommendationSeverityLow,
				Savings:      0,
				Data: map[string]any{
					"instance_id":   resource.Id,
					"instance_name": resource.Name,
					"region":        resource.Region,
				},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// Recommendation 2: Check for stopped/suspended instances
		if resource.Status == providers.ResourceStatusInactive {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName: providers.RecommendationCategoryRightSizing,
				RuleName:     "gcp_sql_inactive_instance",
				Severity:     providers.RecommendationSeverityHigh,
				Savings:      0,
				Data: map[string]any{
					"instance_id":   resource.Id,
					"instance_name": resource.Name,
					"region":        resource.Region,
					"status":        resource.Status,
				},
				Action:              providers.RecommendationActionDelete,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// Recommendation 3: Check backup configuration
		if meta, ok := resource.Meta["backupConfiguration"].(map[string]interface{}); ok {
			if enabled, ok := meta["enabled"].(bool); ok && !enabled {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName: providers.RecommendationCategoryConfiguration,
					RuleName:     "gcp_sql_no_backup",
					Severity:     providers.RecommendationSeverityHigh,
					Savings:      0,
					Data: map[string]any{
						"instance_id":   resource.Id,
						"instance_name": resource.Name,
						"region":        resource.Region,
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}
		}

		// Recommendation 4: Check SSL configuration
		// Note: requireSsl is deprecated in favor of sslMode
		// Check sslMode first (preferred), fallback to requireSsl for older instances
		if settings, ok := resource.Meta["settings"].(map[string]interface{}); ok {
			if ipConfig, ok := settings["ipConfiguration"].(map[string]interface{}); ok {
				sslNotConfigured := false

				// Check sslMode first (modern approach)
				if sslMode, ok := ipConfig["sslMode"].(string); ok {
					// sslMode values: ALLOW_UNENCRYPTED_AND_ENCRYPTED, ENCRYPTED_ONLY, TRUSTED_CLIENT_CERTIFICATE_REQUIRED
					if sslMode == "ALLOW_UNENCRYPTED_AND_ENCRYPTED" || sslMode == "" {
						sslNotConfigured = true
					}
				} else {
					// Fallback to deprecated requireSsl field for older instances
					if requireSsl, ok := ipConfig["requireSsl"].(bool); ok && !requireSsl {
						sslNotConfigured = true
					}
				}

				if sslNotConfigured {
					recommendations = append(recommendations, providers.Recommendation{
						CategoryName: providers.RecommendationCategorySecurity,
						RuleName:     "gcp_sql_no_ssl",
						Severity:     providers.RecommendationSeverityHigh,
						Savings:      0,
						Data: map[string]any{
							"instance_id":   resource.Id,
							"instance_name": resource.Name,
							"region":        resource.Region,
						},
						Action:              providers.RecommendationActionModify,
						ResourceServiceName: resource.ServiceName,
						ResourceId:          resource.Id,
						ResourceType:        resource.Type,
						ResourceRegion:      resource.Region,
					})
				}
			}
		}

		// Recommendation 5: Check for outdated database engine versions
		// Replaced age-based check (>90 days) with engine version check
		if databaseVersion, ok := resource.Meta["databaseVersion"].(string); ok {
			// Map of outdated major versions that should be upgraded
			outdatedVersions := map[string]bool{
				"MYSQL_5_6":      true,
				"MYSQL_5_7":      true,
				"POSTGRES_9_6":   true,
				"POSTGRES_10":    true,
				"POSTGRES_11":    true,
				"SQLSERVER_2017": true,
			}

			if outdatedVersions[databaseVersion] {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName: providers.RecommendationCategoryInfraUpgrade,
					RuleName:     "gcp_sql_outdated_version",
					Severity:     providers.RecommendationSeverityMedium,
					Savings:      0,
					Data: map[string]any{
						"instance_id":      resource.Id,
						"instance_name":    resource.Name,
						"region":           resource.Region,
						"database_version": databaseVersion,
						"reason":           "Database engine version is outdated and should be upgraded",
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}
		}

		// Recommendation 6: Check high availability configuration
		if settings, ok := resource.Meta["settings"].(map[string]interface{}); ok {
			if availabilityType, ok := settings["availabilityType"].(string); ok && availabilityType == "ZONAL" {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName: providers.RecommendationCategoryConfiguration,
					RuleName:     "gcp_sql_no_ha",
					Severity:     providers.RecommendationSeverityMedium,
					Savings:      0,
					Data: map[string]any{
						"instance_id":        resource.Id,
						"instance_name":      resource.Name,
						"region":             resource.Region,
						"availability_type":  availabilityType,
						"recommended_config": "REGIONAL",
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}
		}
	}

	return recommendations, nil
}

// calculateGCPSQLThreshold calculates the alarm threshold based on instance-specific rules
// Uses ByInstanceClass for tier-based thresholds (db-n1-standard-1, etc.)
// Note: Database type specific thresholds (postgresql, mysql, sqlserver) would require
// adding ByDatabaseType field to ThresholdRules struct, currently using default for all types
func calculateGCPSQLThreshold(resource providers.Resource, template providers.AlarmTemplate) float64 {
	// Start with default threshold
	defaultThreshold := template.ThresholdRules.Default
	if defaultThreshold == 0 {
		defaultThreshold = 0.80
	}

	// Check for tier-specific thresholds using ByInstanceClass
	// The YAML uses by_tier (db-f1-micro, db-n1-standard-1, etc.)
	// which maps to ByInstanceClass in the struct
	if template.ThresholdRules.ByInstanceClass != nil {
		if tier, ok := resource.Meta["tier"].(string); ok && tier != "" {
			if tierThreshold, ok := template.ThresholdRules.ByInstanceClass[tier]; ok {
				return tierThreshold
			}
		}
	}

	return defaultThreshold
}

func (s *cloudSQLService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	// Check if this is an alarm/alert policy recommendation
	if _, ok := recommendation.Data["alarm_config"]; ok {
		ctx.GetLogger().Info("gcp: applying alarm recommendation for gcloud_sql",
			"rule_name", recommendation.RuleName,
			"resource_id", recommendation.ResourceId)
		return CreateGCPAlertPolicyFromRecommendation(ctx, account, recommendation)
	}

	session, err := getGcloudSessionFromAccount(ctx, account)
	if err != nil {
		return fmt.Errorf("failed to get gcloud session: %w", err)
	}

	service, err := sqladmin.NewService(ctx.GetContext(), session.Opts...)
	if err != nil {
		return fmt.Errorf("failed to create Cloud SQL admin service: %w", err)
	}

	instanceName, ok := recommendation.Data["instance_name"].(string)
	if !ok || instanceName == "" {
		return fmt.Errorf("instance_name not found in recommendation data")
	}

	switch recommendation.RuleName {
	case "gcp_sql_no_labels":
		return fmt.Errorf("automatic label addition not yet implemented - please add labels manually via GCP console or gcloud CLI")

	case "gcp_sql_inactive_instance":
		// Delete the inactive instance
		op, err := service.Instances.Delete(session.ProjectId, instanceName).Context(ctx.GetContext()).Do()
		if err != nil {
			return fmt.Errorf("failed to delete SQL instance: %w", err)
		}

		ctx.GetLogger().Info("successfully initiated SQL instance deletion", "instance", instanceName, "operation", op.Name)
		return nil

	case "gcp_sql_no_backup":
		// Enable automated backups
		// This requires updating the instance settings
		return fmt.Errorf("automatic backup configuration not yet implemented - please enable backups manually via GCP console")

	case "gcp_sql_no_ssl":
		// Enable SSL requirement
		return fmt.Errorf("automatic SSL configuration not yet implemented - please enable SSL manually via GCP console")

	case "gcp_sql_old_instance":
		return fmt.Errorf("rightsizing requires manual review and cannot be automatically applied")

	case "gcp_sql_no_ha":
		// Enable high availability
		return fmt.Errorf("automatic HA configuration not yet implemented - please enable HA manually via GCP console")

	default:
		return fmt.Errorf("unknown recommendation rule: %s", recommendation.RuleName)
	}
}

// resolveSQLInstanceName picks the Cloud SQL instance name from explicit Args
// first, then falls back to command.ResourceId. GetResources stores resourse_id
// as "<project>:<instance_name>" (see GetResources above), so we strip the
// project prefix to recover the bare instance name expected by the sqladmin
// SDK's positional arg.
func resolveSQLInstanceName(command providers.ApplyCommandRequest) string {
	if n, ok := command.Args["instance_name"].(string); ok && n != "" {
		return n
	}
	rid := command.ResourceId
	if rid == "" {
		return ""
	}
	if idx := strings.LastIndex(rid, ":"); idx >= 0 {
		return rid[idx+1:]
	}
	return rid
}

func (s *cloudSQLService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	session, err := getGcloudSessionFromAccount(ctx, account)
	if err != nil {
		return providers.ApplyCommandResponse{}, fmt.Errorf("failed to get gcloud session: %w", err)
	}

	service, err := sqladmin.NewService(ctx.GetContext(), session.Opts...)
	if err != nil {
		return providers.ApplyCommandResponse{}, fmt.Errorf("failed to create Cloud SQL admin service: %w", err)
	}

	instanceName := resolveSQLInstanceName(command)
	if instanceName == "" {
		resultMessage := "instance_name arg required"
		if auditErr := logResourceActionAudit(ctx, command, account, "FAILURE", resultMessage); auditErr != nil {
			ctx.GetLogger().Warn("failed to log audit record", "error", auditErr)
		}
		return providers.ApplyCommandResponse{}, fmt.Errorf("%s", resultMessage)
	}

	switch command.Command {
	case "start":
		// Get the instance to check its current activation policy
		instance, err := service.Instances.Get(session.ProjectId, instanceName).Context(ctx.GetContext()).Do()
		if err != nil {
			resultMessage := fmt.Sprintf("failed to get SQL instance: %v", err)
			if auditErr := logResourceActionAudit(ctx, command, account, "FAILURE", resultMessage); auditErr != nil {
				ctx.GetLogger().Warn("failed to log audit record", "error", auditErr)
			}
			return providers.ApplyCommandResponse{}, fmt.Errorf("%s", resultMessage)
		}

		// Set activation policy to ALWAYS
		if instance.Settings == nil {
			instance.Settings = &sqladmin.Settings{}
		}
		instance.Settings.ActivationPolicy = "ALWAYS"

		op, err := service.Instances.Patch(session.ProjectId, instanceName, instance).Context(ctx.GetContext()).Do()
		if err != nil {
			resultMessage := fmt.Sprintf("failed to activate SQL instance: %v", err)
			if auditErr := logResourceActionAudit(ctx, command, account, "FAILURE", resultMessage); auditErr != nil {
				ctx.GetLogger().Warn("failed to log audit record", "error", auditErr)
			}
			return providers.ApplyCommandResponse{}, fmt.Errorf("%s", resultMessage)
		}

		resultMessage := fmt.Sprintf("Successfully initiated activation of SQL instance %s (operation: %s)", instanceName, op.Name)
		if auditErr := logResourceActionAudit(ctx, command, account, "SUCCESS", resultMessage); auditErr != nil {
			ctx.GetLogger().Warn("failed to log audit record", "error", auditErr)
		}
		return providers.ApplyCommandResponse{
			Success: true,
			Message: resultMessage,
		}, nil

	case "stop":
		// Set activation policy to NEVER to stop billing
		instance, err := service.Instances.Get(session.ProjectId, instanceName).Context(ctx.GetContext()).Do()
		if err != nil {
			resultMessage := fmt.Sprintf("failed to get SQL instance: %v", err)
			if auditErr := logResourceActionAudit(ctx, command, account, "FAILURE", resultMessage); auditErr != nil {
				ctx.GetLogger().Warn("failed to log audit record", "error", auditErr)
			}
			return providers.ApplyCommandResponse{}, fmt.Errorf("%s", resultMessage)
		}

		if instance.Settings == nil {
			instance.Settings = &sqladmin.Settings{}
		}
		instance.Settings.ActivationPolicy = "NEVER"

		op, err := service.Instances.Patch(session.ProjectId, instanceName, instance).Context(ctx.GetContext()).Do()
		if err != nil {
			resultMessage := fmt.Sprintf("failed to stop SQL instance: %v", err)
			if auditErr := logResourceActionAudit(ctx, command, account, "FAILURE", resultMessage); auditErr != nil {
				ctx.GetLogger().Warn("failed to log audit record", "error", auditErr)
			}
			return providers.ApplyCommandResponse{}, fmt.Errorf("%s", resultMessage)
		}

		resultMessage := fmt.Sprintf("Successfully initiated stopping of SQL instance %s (operation: %s)", instanceName, op.Name)
		if auditErr := logResourceActionAudit(ctx, command, account, "SUCCESS", resultMessage); auditErr != nil {
			ctx.GetLogger().Warn("failed to log audit record", "error", auditErr)
		}
		return providers.ApplyCommandResponse{
			Success: true,
			Message: resultMessage,
		}, nil

	case "restart":
		op, err := service.Instances.Restart(session.ProjectId, instanceName).Context(ctx.GetContext()).Do()
		if err != nil {
			resultMessage := fmt.Sprintf("failed to restart SQL instance: %v", err)
			if auditErr := logResourceActionAudit(ctx, command, account, "FAILURE", resultMessage); auditErr != nil {
				ctx.GetLogger().Warn("failed to log audit record", "error", auditErr)
			}
			return providers.ApplyCommandResponse{}, fmt.Errorf("%s", resultMessage)
		}

		resultMessage := fmt.Sprintf("Successfully initiated restart of SQL instance %s (operation: %s)", instanceName, op.Name)
		if auditErr := logResourceActionAudit(ctx, command, account, "SUCCESS", resultMessage); auditErr != nil {
			ctx.GetLogger().Warn("failed to log audit record", "error", auditErr)
		}
		return providers.ApplyCommandResponse{
			Success: true,
			Message: resultMessage,
		}, nil

	case "delete":
		op, err := service.Instances.Delete(session.ProjectId, instanceName).Context(ctx.GetContext()).Do()
		if err != nil {
			resultMessage := fmt.Sprintf("failed to delete SQL instance: %v", err)
			if auditErr := logResourceActionAudit(ctx, command, account, "FAILURE", resultMessage); auditErr != nil {
				ctx.GetLogger().Warn("failed to log audit record", "error", auditErr)
			}
			return providers.ApplyCommandResponse{}, fmt.Errorf("%s", resultMessage)
		}

		resultMessage := fmt.Sprintf("Successfully initiated deletion of SQL instance %s (operation: %s)", instanceName, op.Name)
		if auditErr := logResourceActionAudit(ctx, command, account, "SUCCESS", resultMessage); auditErr != nil {
			ctx.GetLogger().Warn("failed to log audit record", "error", auditErr)
		}
		return providers.ApplyCommandResponse{
			Success: true,
			Message: resultMessage,
		}, nil

	default:
		return providers.ApplyCommandResponse{}, fmt.Errorf("unsupported command: %s", command.Command)
	}
}

func (s *cloudSQLService) GetLogFilter(ctx providers.CloudProviderContext, account providers.Account, resourceId string) string {
	if resourceId == "" {
		return `resource.type="cloudsql_database"`
	}
	// GCP Cloud SQL database_id label format is "project_id:instance_name"
	session, err := getGcloudSessionFromAccount(ctx, account)
	if err != nil {
		ctx.GetLogger().Warn("failed to get gcloud session for log filter, falling back to broad filter", "error", err)
		return `resource.type="cloudsql_database"`
	}
	databaseId := fmt.Sprintf("%s:%s", session.ProjectId, resourceId)
	return fmt.Sprintf(`resource.type="cloudsql_database" resource.labels.database_id="%s"`, databaseId)
}

// Ensure the cloudsqlconn package is available for potential connection functionality
var _ = cloudsqlconn.NewDialer
