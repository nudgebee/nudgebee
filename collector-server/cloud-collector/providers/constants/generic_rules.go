package constants

// =============================================================================
// GENERIC RECOMMENDATION RULES (Cross-Provider)
// =============================================================================
// These rules represent the same concept across AWS, Azure, and GCP.
// Use these constants when the rule logic is provider-agnostic.
//
// A rule is GENERIC if:
// - Same concept across providers (e.g., idle instances, unused disks)
// - Provider-agnostic outcome (same intent regardless of cloud)
// - Metrics are conceptually equivalent (even if metric names differ)
// - Can be abstracted with parameters (thresholds, metric names, etc.)
// - Business/cost/security logic is the same across providers
//
// Examples: IdleInstance, UnusedVolume, PublicAccess, BackupDisabled
// =============================================================================

// -----------------------------------------------------------------------------
// RIGHTSIZING - Compute Optimization
// -----------------------------------------------------------------------------

const (
	// IdleInstance - Instances with minimal or no activity that can be stopped or terminated
	// AWS: aws_ec2_idle_instance, aws_rds_idle_instance, aws_elasticache_idle_instance
	// Azure: azure_vm_idle_instance
	// GCP: gcp_sql_inactive_instance
	IdleInstance = "idle_instance"

	// UnderutilizedInstance - Instances running below optimal capacity that can be downsized
	// AWS: aws_ec2_underutilized, aws_rds_underutilized, aws_fargate_service_underutilized
	// Azure: azure_vm_underutilized
	UnderutilizedInstance = "underutilized_instance"

	// OverutilizedInstance - Instances running at or near capacity limits that need scaling up
	// AWS: aws_rds_overutilized, aws_fargate_service_overutilized, aws_ecs_fargate_service_overutilized
	OverutilizedInstance = "overutilized_instance"

	// StoppedInstance - Stopped/deallocated instances still incurring costs
	// AWS: aws_ec2_stopped_instance_incurring_storage_cost
	// Azure: azure_app_service_stopped_app, azure_mysql_server_stopped, azure_postgres_server_stopped
	// GCP: gcp_compute_stopped_instance
	StoppedInstance = "stopped_instance"

	// OldInstance - Long-running instances that may benefit from redeployment
	// GCP: gcp_compute_old_instance, gcp_sql_old_instance, gcp_gke_old_cluster
	OldInstance = "old_instance"

	// InstanceGenerationUpgrade - Instances on older hardware generations
	// AWS: aws_ec2_instance_generation_upgrade, aws_ec2_ebs_generation_upgrade
	// Azure: azure_vm_generation_upgrade
	InstanceGenerationUpgrade = "instance_generation_upgrade"

	// AlternateInstances - Alternative instance types with better cost-performance
	// AWS: aws_ec2_alternate_instances, aws_rds_alternate_instances
	AlternateInstances = "alternate_instances"
)

// -----------------------------------------------------------------------------
// RIGHTSIZING - Storage Optimization
// -----------------------------------------------------------------------------

const (
	// UnattachedVolume - Storage volumes not attached to any instance
	// AWS: aws_ec2_orphaned_volume
	// Azure: azure_disk_unattached_volume
	UnattachedVolume = "unattached_volume"

	// StorageLifecycle - Object storage without lifecycle policies
	// AWS: aws_s3_lifecycle_policy_not_enabled
	// Azure: azure_storage_lifecycle_management_disabled
	StorageLifecycle = "storage_lifecycle"
)

// -----------------------------------------------------------------------------
// SECURITY - Encryption
// -----------------------------------------------------------------------------

const (
	// EncryptionAtRest - Data not encrypted at rest
	// AWS: aws_ebs_encryption_not_enabled, aws_rds_storage_not_encrypted
	// Azure: azure_vm_boot_disk_encryption_disabled, azure_vm_data_disk_encryption_disabled
	// GCP: gcp_sql_encryption_at_rest_disabled
	EncryptionAtRest = "encryption_at_rest"

	// EncryptionInTransit - Data not encrypted during transmission
	// AWS: aws_alb_ssl_tls_1_2, aws_rds_ssl_not_enforced
	// Azure: azure_mysql_ssl_not_enforced
	// GCP: gcp_sql_ssl_not_required
	EncryptionInTransit = "encryption_in_transit"

	// EncryptionCMK - Not using customer-managed encryption keys
	// AWS: aws_ebs_encryption_cmk_not_enabled
	// Azure: azure_vm_disk_encryption_cmk_missing
	EncryptionCMK = "encryption_cmk"
)

// -----------------------------------------------------------------------------
// SECURITY - Access Control
// -----------------------------------------------------------------------------

const (
	// PublicAccess - Resources exposed to the public internet
	// AWS: aws_rds_publicly_accessible, aws_s3_public_access_enabled
	// Azure: azure_storage_public_access_enabled
	// GCP: gcp_sql_public_access_enabled, gcp_storage_public_access
	PublicAccess = "public_access"

	// PublicNetworkAccess - Network endpoints accessible from internet
	// Azure: azure_mysql_public_network_access_enabled
	PublicNetworkAccess = "public_network_access"

	// HTTPSOnly - Service not requiring HTTPS connections
	// AWS: aws_s3_https_only_not_enforced
	// Azure: azure_storage_https_only_not_enforced
	HTTPSOnly = "https_only"

	// RBAC - Role-based access control not enabled
	// AWS: aws_eks_rbac_not_enabled
	// GCP: gcp_gke_rbac_not_enabled
	RBAC = "rbac"

	// MFA - Multi-factor authentication not enabled
	// AWS: aws_iam_root_mfa_not_enabled
	MFA = "mfa"
)

// -----------------------------------------------------------------------------
// SECURITY - SSL/TLS Versions
// -----------------------------------------------------------------------------

const (
	// MinTLSVersion - Using outdated TLS versions
	// AWS: aws_alb_min_tls_version, aws_api_gateway_min_tls_version
	// Azure: azure_storage_min_tls_version
	MinTLSVersion = "min_tls_version"
)

// -----------------------------------------------------------------------------
// CONFIGURATION - Backup & Disaster Recovery
// -----------------------------------------------------------------------------

const (
	// BackupDisabled - Automated backups not configured
	// AWS: aws_rds_backup_enabled, aws_dynamodb_point_in_time_recovery
	// Azure: azure_vm_backup_disabled, azure_mysql_backup_disabled
	// GCP: gcp_sql_no_backup
	BackupDisabled = "backup_disabled"

	// BackupRetention - Backup retention period too short
	// AWS: aws_rds_backup_retention
	// Azure: azure_mysql_backup_retention_less_than_7_days
	BackupRetention = "backup_retention"
)

// -----------------------------------------------------------------------------
// CONFIGURATION - Monitoring & Logging
// -----------------------------------------------------------------------------

const (
	// LoggingDisabled - Logging not enabled
	// AWS: aws_vpc_flow_logs_not_enabled, aws_alb_access_logs_not_enabled
	// Azure: azure_storage_logging_disabled
	// GCP: gcp_gke_logging_disabled
	LoggingDisabled = "logging_disabled"

	// MonitoringDisabled - Monitoring/metrics not enabled
	// AWS: aws_rds_enhanced_monitoring_not_enabled
	// Azure: azure_vm_boot_diagnostics_disabled
	MonitoringDisabled = "monitoring_disabled"

	// LogRetention - Log retention period too short
	// AWS: aws_cloudwatch_log_retention
	LogRetention = "log_retention"
)

// -----------------------------------------------------------------------------
// CONFIGURATION - Auto-Scaling & High Availability
// -----------------------------------------------------------------------------

const (
	// AutoScalingDisabled - Auto-scaling not configured
	// AWS: aws_rds_autoscaling_not_enabled, aws_ecs_service_autoscaling_disabled
	// Azure: azure_vmss_autoscale_disabled
	AutoScalingDisabled = "autoscaling_disabled"

	// HealthCheckDisabled - Health checks not configured
	// AWS: aws_alb_health_check_not_configured
	HealthCheckDisabled = "health_check_disabled"

	// AutoUpgradeDisabled - Automatic upgrades not enabled
	// AWS: aws_rds_minor_version_auto_upgrade_not_enabled
	// Azure: azure_vm_automatic_os_upgrade_disabled
	// GCP: gcp_gke_auto_upgrade_disabled
	AutoUpgradeDisabled = "auto_upgrade_disabled"

	// TerminationProtection - Resources can be accidentally deleted
	// AWS: aws_rds_deletion_protection_not_enabled
	// Azure: azure_vm_delete_protection_disabled
	TerminationProtection = "termination_protection"
)

// -----------------------------------------------------------------------------
// CONFIGURATION - Tagging & Organization
// -----------------------------------------------------------------------------

const (
	// TaggingIncomplete - Resources missing required tags
	// AWS: aws_resource_missing_tags
	// Azure: azure_resource_missing_tags
	// GCP: gcp_resource_missing_labels
	TaggingIncomplete = "tagging_incomplete"
)

// -----------------------------------------------------------------------------
// HIGH AVAILABILITY
// -----------------------------------------------------------------------------

const (
	// HADisabled - High availability not configured
	// AWS: aws_rds_multi_az_not_enabled
	// Azure: azure_mysql_ha_not_enabled
	// GCP: gcp_sql_ha_not_enabled
	HADisabled = "ha_disabled"

	// MultiRegionDisabled - Not deployed across multiple regions
	// AWS: aws_s3_cross_region_replication_not_enabled
	MultiRegionDisabled = "multi_region_disabled"

	// RedundancyInsufficient - Insufficient redundancy configuration
	// AWS: aws_elasticache_insufficient_redundancy
	RedundancyInsufficient = "redundancy_insufficient"
)

// -----------------------------------------------------------------------------
// INFRASTRUCTURE UPGRADE
// -----------------------------------------------------------------------------

const (
	// OutdatedVersion - Running outdated version
	// AWS: aws_rds_outdated_version, aws_eks_outdated_version
	// Azure: azure_postgres_outdated_version
	// GCP: gcp_gke_outdated_version
	OutdatedVersion = "outdated_version"

	// DeprecatedRuntime - Using deprecated runtime/platform
	// AWS: aws_lambda_deprecated_runtime
	// Azure: azure_function_deprecated_runtime
	// GCP: gcp_function_deprecated_runtime
	DeprecatedRuntime = "deprecated_runtime"

	// OldPlatformVersion - Platform version needs upgrade
	// AWS: aws_elasticbeanstalk_old_platform_version
	OldPlatformVersion = "old_platform_version"
)

// -----------------------------------------------------------------------------
// CATEGORY CONSTANTS
// -----------------------------------------------------------------------------

const (
	CategoryRightSizing      = "RightSizing"
	CategorySecurity         = "Security"
	CategoryConfiguration    = "Configuration"
	CategoryInfraUpgrade     = "InfraUpgrade"
	CategoryHighAvailability = "HighAvailability"
)

// -----------------------------------------------------------------------------
// SEVERITY CONSTANTS
// -----------------------------------------------------------------------------

const (
	SeverityCritical = "Critical"
	SeverityHigh     = "High"
	SeverityMedium   = "Medium"
	SeverityLow      = "Low"
	SeverityInfo     = "Info"
)

// -----------------------------------------------------------------------------
// ACTION CONSTANTS
// -----------------------------------------------------------------------------

const (
	ActionModify = "Modify"
	ActionDelete = "Delete"
)

// -----------------------------------------------------------------------------
// PROVIDER CONSTANTS
// -----------------------------------------------------------------------------

const (
	ProviderAWS   = "AWS"
	ProviderAzure = "Azure"
	ProviderGCP   = "GCP"
)
