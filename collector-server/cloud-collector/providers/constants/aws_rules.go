package constants

// =============================================================================
// AWS-SPECIFIC RECOMMENDATION RULES
// =============================================================================
// These rules use AWS-exclusive features and have no direct equivalent in
// other cloud providers.
//
// A rule is AWS-SPECIFIC if:
// - Uses AWS-exclusive features (Spot Instances, Savings Plans, Lambda Reserved Concurrency)
// - AWS-specific services (IAM Access Analyzer, GuardDuty, Inspector, Config)
// - No equivalent concept in Azure/GCP (ECS/Fargate, Step Functions, CloudTrail)
// - Relies on AWS-specific APIs or metadata (EC2 IMDS, RDS Performance Insights)
// - AWS billing/pricing models (Reserved Instances, Savings Plans)
//
// Examples: AwsEC2SpotInterruptionRisk, AwsLambdaReservedConcurrency,
//           AwsGuardDutyNotEnabled, AwsECSContainerInsightsDisabled
// =============================================================================

// -----------------------------------------------------------------------------
// AWS EC2 - Elastic Compute Cloud
// -----------------------------------------------------------------------------
const (
	AwsEC2IdleInstance                          = "vm_idle"
	AwsEC2Underutilized                         = "vm_underutilized"
	AwsEC2InstanceGenerationUpgrade             = "vm_generation_upgrade"
	AwsEC2AlternateInstances                    = "aws_ec2_alternate_instances"
	AwsEC2DetailedMonitoringDisabled            = "aws_ec2_detailed_monitoring_disabled"
	AwsEC2EBSEncrypt                            = "aws_ec2_ebs_encrypt"
	AwsEC2EBSGenerationUpgrade                  = "aws_ec2_ebs_generation_upgrade"
	AwsEC2OrphanedVolume                        = "orphaned_volume"
	AwsEC2StoppedInstanceIncurringStorageCost   = "vm_stopped"
	AwsEC2InstanceIMDSTokenOptional             = "aws_ec2_instance_imds_token_optional" // AWS-Exclusive: IMDSv2
	AwsEC2InstancePublicIP                      = "aws_ec2_instance_public_ip"
	AwsEC2InstancePublicSubnet                  = "aws_ec2_instance_public_subnet"
	AwsEC2InstanceTerminatesOnOSShutdown        = "aws_ec2_instance_terminates_on_os_shutdown"
	AwsEC2InstanceTerminationProtectionDisabled = "aws_ec2_instance_termination_protection_disabled"
)

// -----------------------------------------------------------------------------
// AWS RDS - Relational Database Service
// -----------------------------------------------------------------------------
const (
	AwsRDSIdleInstance                  = "aws_rds_idle_instance"
	AwsRDSUnderutilized                 = "aws_rds_underutilized"
	AwsRDSOverutilized                  = "aws_rds_overutilized"
	AwsRDSFreeStorageSpace              = "aws_rds_free_storage_space"
	AwsRDSBackupEnabled                 = "db_backup_disabled"
	AwsRDSBackupServiceEnabled          = "aws_rds_backupservice_enabled"
	AwsRDSAlternateInstances            = "aws_rds_alternate_instances"
	AwsRDSAutoMinorUpgrade              = "aws_rds_auto_minor_upgrade"
	AwsRDSCopyTagsToSnapshots           = "aws_rds_copy_tags_to_snapshots"
	AwsRDSDeleteProtection              = "aws_rds_delete_protection"
	AwsRDSInstanceGeneration            = "aws_rds_instance_generation"
	AwsRDSInstancePublicSubnet          = "aws_rds_instance_public_subnet"
	AwsRDSInstanceReserved              = "aws_rds_instance_reserved"    // AWS-Exclusive: Reserved Instances
	AwsRDSPerformanceInsights           = "aws_rds_performance_insights" // AWS-Exclusive: Performance Insights
	AwsRDSPublicAccess                  = "db_public_access"
	AwsRDSReservedInstanceConfigured    = "aws_rds_reservedinstance_configured" // AWS-Exclusive: Reserved Instances
	AwsRDSSnapshotEncryption            = "aws_rds_snapshot_encryption"
	AwsRDSStorageAutoscaling            = "db_storage_autoscaling"
	AwsRDSStorageEncrypted              = "aws_rds_storage_encrypted"
	AwsRDSGp2Storage                    = "aws_rds_gp2_storage"
	AwsRDSGravitonMigration             = "aws_rds_graviton_migration"
	AwsRDSExtendedSupport               = "aws_rds_extended_support"
	AwsRDSAuroraServerlessMigration     = "aws_rds_aurora_serverless_migration"      // AWS-Exclusive: Aurora Serverless v2
	AwsRDSAuroraServerlessScalingConfig = "aws_rds_aurora_serverless_scaling_config" // AWS-Exclusive: Aurora Serverless v2 scaling config
)

// -----------------------------------------------------------------------------
// AWS Cost Explorer - Savings Plans and Reserved Instances
// -----------------------------------------------------------------------------
const (
	AwsNativeCESavingsPlan       = "aws_native_ce_savings_plan_recommendation"
	AwsNativeCERI                = "aws_native_ce_ri_recommendation"
	AwsNativeDatabaseSavingsPlan = "aws_native_database_savings_plan" // AWS-Exclusive: Database Savings Plans
)

// -----------------------------------------------------------------------------
// AWS S3 - Simple Storage Service
// -----------------------------------------------------------------------------
const (
	AwsS3Lifecycle          = "storage_no_lifecycle"
	AwsS3PublicAccessACL    = "storage_public_access"
	AwsS3PublicAccessPolicy = "aws_s3_public_access_policy"
	AwsS3Versioning         = "storage_versioning_disabled"
)

// -----------------------------------------------------------------------------
// AWS Lambda - Serverless Functions
// -----------------------------------------------------------------------------
// All Lambda rules are AWS-exclusive (no direct equivalent in Azure Functions/GCP Functions)
const (
	AwsLambdaDeprecatedRuntime             = "aws_lambda_deprecated_runtime"
	AwsLambdaTracing                       = "aws_lambda_tracing" // AWS-Exclusive: X-Ray tracing
	AwsLambdaDeadLetterQueue               = "aws_lambda_dead_letter_queue"
	AwsLambdaEnvironmentVariableEncryption = "aws_lambda_environment_variable_encryption"
	AwsLambdaFunctionURL                   = "aws_lambda_function_url"            // AWS-Exclusive: Lambda Function URLs
	AwsLambdaProvisionedConcurrency        = "aws_lambda_provisioned_concurrency" // AWS-Exclusive: Provisioned Concurrency
	AwsLambdaReservedConcurrency           = "aws_lambda_reserved_concurrency"    // AWS-Exclusive: Reserved Concurrency
)

// -----------------------------------------------------------------------------
// AWS ECS/Fargate - Container Orchestration
// -----------------------------------------------------------------------------
// All ECS/Fargate rules are AWS-exclusive (no direct equivalent in Azure Container Instances/GCP Cloud Run)
const (
	AwsECSAvoidDefaultCluster                             = "aws_ecs_avoid_default_cluster"
	AwsECSClusterFargateFIPSDisabled                      = "aws_ecs_cluster_fargate_fips_disabled"
	AwsECSContainerInsightsDisabled                       = "aws_ecs_container_insights_disabled" // AWS-Exclusive: Container Insights
	AwsECSServiceAutoscalingDisabled                      = "aws_ecs_service_autoscaling_disabled"
	AwsECSServiceConnectDisabled                          = "aws_ecs_service_connect_disabled" // AWS-Exclusive: ECS Service Connect
	AwsECSServiceExecLoggingDisabled                      = "aws_ecs_service_exec_logging_disabled"
	AwsECSServiceHealthCheckGracePeriodZero               = "aws_ecs_service_health_check_grace_period_zero"
	AwsECSServiceMaxPercentNonStandard                    = "aws_ecs_service_max_percent_non_standard"
	AwsECSServiceMinHealthyPercentLow                     = "aws_ecs_service_min_healthy_percent_low"
	AwsECSServiceMinHealthyPercentTooLowForSingleTask     = "aws_ecs_service_min_healthy_percent_too_low_for_single_task"
	AwsECSServiceReviewAutoscaling                        = "aws_ecs_service_review_autoscaling"
	AwsECSTaskDefinitionHealthCheckNotConfigured          = "aws_ecs_task_definition_health_check_not_configured"
	AwsECSTaskDefinitionImageLatestTag                    = "aws_ecs_task_definition_image_latest_tag"
	AwsECSTaskDefinitionLoggingNotConfigured              = "aws_ecs_task_definition_logging_not_configured"
	AwsECSTaskDefinitionMissingTaskRole                   = "aws_ecs_task_definition_missing_task_role"
	AwsECSTaskDefinitionPrivilegedContainer               = "aws_ecs_task_definition_privileged_container"
	AwsECSTaskDefinitionReadonlyRootFsDisabled            = "aws_ecs_task_definition_readonly_root_fs_disabled"
	AwsECSTaskDefinitionSecretsNotUsed                    = "aws_ecs_task_definition_secrets_not_used"
	AwsECSFargateServiceOverutilized                      = "aws_ecs_fargate_service_overutilized"
	AwsFargateLatestPlatformVersion                       = "aws_fargate_latest_platform_version" // AWS-Exclusive: Fargate platform versions
	AwsFargateServiceOverutilized                         = "aws_fargate_service_overutilized"
	AwsFargateServiceUnderutilized                        = "aws_fargate_service_underutilized"
	AwsFargateTaskDefinitionCPUUndefined                  = "aws_fargate_task_definition_cpu_undefined"
	AwsFargateTaskDefinitionMemoryUndefined               = "aws_fargate_task_definition_memory_undefined"
	AwsFargateServiceExecEnabled                          = "aws_fargate_service_exec_enabled"
	AwsFargateServiceHealthCheckGracePeriodZero           = "aws_fargate_service_health_check_grace_period_zero"
	AwsFargateServiceMaxPercentNonStandard                = "aws_fargate_service_max_percent_non_standard"
	AwsFargateServiceMinHealthyPercentLow                 = "aws_fargate_service_min_healthy_percent_low"
	AwsFargateServiceMinHealthyPercentTooLowForSingleTask = "aws_fargate_service_min_healthy_percent_too_low_for_single_task"
	AwsFargateTaskDefinitionHealthCheckNotConfigured      = "aws_fargate_task_definition_health_check_not_configured"
	AwsFargateTaskDefinitionImageLatestTag                = "aws_fargate_task_definition_image_latest_tag"
	AwsFargateTaskDefinitionLoggingNotConfigured          = "aws_fargate_task_definition_logging_not_configured"
	AwsFargateTaskDefinitionMissingTaskRole               = "aws_fargate_task_definition_missing_task_role"
	AwsFargateTaskDefinitionPrivilegedContainer           = "aws_fargate_task_definition_privileged_container"
	AwsFargateTaskDefinitionReadonlyRootFsDisabled        = "aws_fargate_task_definition_readonly_root_fs_disabled"
	AwsFargateTaskDefinitionSecretsNotUsed                = "aws_fargate_task_definition_secrets_not_used"
)

// -----------------------------------------------------------------------------
// AWS CloudTrail - Audit and Compliance Logging
// -----------------------------------------------------------------------------
// AWS-exclusive service (no direct equivalent in Azure Activity Log/GCP Cloud Audit Logs)
const (
	AwsCloudTrailLoggingEnabled        = "aws_cloudtrail_logging_enabled"
	AwsCloudTrailMultiRegion           = "aws_cloudtrail_multi_region"
	AwsCloudTrailNoMultiRegionTrail    = "aws_cloudtrail_no_multi_region_trail"
	AwsCloudTrailEncryptionCMK         = "aws_cloudtrail_encryption_cmk"
	AwsCloudTrailLogValidation         = "aws_cloudtrail_log_validation" // AWS-Exclusive: Log file validation
	AwsCloudTrailCloudWatchIntegration = "aws_cloudtrail_cloudwatch_integration"
	AwsCloudTrailEDSEncryptionCMK      = "aws_cloudtrail_eds_encryption_cmk"
	AwsCloudTrailEDSRetention          = "aws_cloudtrail_eds_retention"
)

// -----------------------------------------------------------------------------
// AWS CloudFront - Content Delivery Network
// -----------------------------------------------------------------------------
const (
	AwsCloudFrontAccessLogging       = "aws_cloudfront_access_logging"
	AwsCloudFrontGeoRestriction      = "aws_cloudfront_geo_restriction"
	AwsCloudFrontOriginAccessControl = "aws_cloudfront_origin_access_control" // AWS-Exclusive: OAC
	AwsCloudFrontViewerProtocolHTTPS = "aws_cloudfront_viewer_protocol_https"
	AwsCloudFrontWAFIntegration      = "aws_cloudfront_waf_integration"
)

// -----------------------------------------------------------------------------
// AWS IAM - Identity and Access Management
// -----------------------------------------------------------------------------
const (
	AwsIAMMFANotEnabled = "iam_mfa_not_enabled" // AWS-Exclusive: IAM root MFA
)

// -----------------------------------------------------------------------------
// AWS ElastiCache - Managed Redis/Memcached
// -----------------------------------------------------------------------------
const (
	AwsElastiCacheIdleInstance        = "aws_elasticache_idle_instance"
	AwsElastiCacheOversized           = "aws_elasticache_oversized"
	AwsElastiCacheUndersized          = "aws_elasticache_undersized"
	AwsElastiCacheLowHitRate          = "aws_elasticache_low_hit_rate"
	AwsElastiCacheAutoMinorUpgrade    = "aws_elasticache_auto_minor_upgrade"
	AwsElastiCacheEncryptionAtRest    = "aws_elasticache_encryption_at_rest"
	AwsElastiCacheEncryptionInTransit = "aws_elasticache_encryption_in_transit"
	AwsElastiCacheEngineVersion       = "aws_elasticache_engine_version"
	AwsElastiCacheInstanceGeneration  = "aws_elasticache_instance_generation"
	AwsElastiCacheGravitonMigration   = "aws_elasticache_graviton_migration"
)

// -----------------------------------------------------------------------------
// AWS DynamoDB - NoSQL Database
// -----------------------------------------------------------------------------
// AWS-exclusive service (no direct equivalent in Azure Cosmos DB/GCP Firestore)
const (
	AwsDynamoDBPITREnabled  = "aws_dynamodb_pitr_enabled"  // AWS-Exclusive: Point-in-Time Recovery
	AwsDynamoDBSSECMK       = "aws_dynamodb_sse_cmk"       // AWS-Exclusive: Server-Side Encryption with CMK
	AwsDynamoDBCapacityMode = "aws_dynamodb_capacity_mode" // AWS-Exclusive: Provisioned vs On-Demand capacity mode optimization
)

// -----------------------------------------------------------------------------
// AWS Config - Configuration Management and Compliance
// -----------------------------------------------------------------------------
// AWS-exclusive service (no direct equivalent in Azure Policy/GCP Config Connector)
const (
	AwsConfigNotEnabled                  = "aws_config_not_enabled"
	AwsConfigNoDeliveryChannel           = "aws_config_no_delivery_channel"
	AwsConfigRecorderNotRecording        = "aws_config_recorder_not_recording"
	AwsConfigNotRecordingAllResources    = "aws_config_not_recording_all_resources"
	AwsConfigRuleNonCompliant            = "aws_config_rule_non_compliant"
	AwsConfigRuleEvaluationError         = "aws_config_rule_evaluation_error"
	AwsConfigConformancePackNonCompliant = "aws_config_conformance_pack_non_compliant"
)

// -----------------------------------------------------------------------------
// AWS Inspector - Vulnerability Assessment
// -----------------------------------------------------------------------------
// AWS-exclusive service (no direct equivalent in Azure Defender/GCP Security Command Center)
const (
	AwsInspectorNotEnabled       = "aws_inspector_not_enabled"
	AwsInspectorEC2NotEnabled    = "aws_inspector_ec2_not_enabled"
	AwsInspectorECRNotEnabled    = "aws_inspector_ecr_not_enabled"
	AwsInspectorLambdaNotEnabled = "aws_inspector_lambda_not_enabled"
	AwsInspectorNoCoverage       = "aws_inspector_no_coverage"
	AwsInspectorCriticalFinding  = "aws_inspector_critical_finding"
	AwsInspectorOldFinding       = "aws_inspector_old_finding"
)

// -----------------------------------------------------------------------------
// AWS GuardDuty - Threat Detection
// -----------------------------------------------------------------------------
// AWS-exclusive service (no direct equivalent in Azure Defender/GCP Security Command Center)
const (
	AwsGuardDutyNotEnabled          = "aws_guardduty_not_enabled"
	AwsGuardDutyDetectorActive      = "aws_guardduty_detector_active"
	AwsGuardDutyK8sAuditLogsEnabled = "aws_guardduty_k8s_audit_logs_enabled"
	AwsGuardDutyS3LogsEnabled       = "aws_guardduty_s3_logs_enabled"
)

// -----------------------------------------------------------------------------
// AWS WAF - Web Application Firewall
// -----------------------------------------------------------------------------
const (
	AwsWAFLoggingDisabled     = "aws_waf_logging_disabled"
	AwsWAFEmptyIPSet          = "aws_waf_empty_ipset"
	AwsWAFNoManagedRules      = "aws_waf_no_managed_rules" // AWS-Exclusive: Managed rule groups
	AwsWAFNoRateLimiting      = "aws_waf_no_rate_limiting"
	AwsWAFNoRules             = "aws_waf_no_rules"
	AwsWAFWebACLNotAssociated = "aws_waf_webacl_not_associated"
)

// -----------------------------------------------------------------------------
// AWS KMS - Key Management Service
// -----------------------------------------------------------------------------
const (
	AwsKMSKeyRotationEnabled = "aws_kms_key_rotation_enabled" // AWS-Exclusive: Automatic key rotation
)

// -----------------------------------------------------------------------------
// AWS Secrets Manager
// -----------------------------------------------------------------------------
const (
	AwsSecretsManagerEncryptionCMK   = "aws_secretsmanager_encryption_cmk"
	AwsSecretsManagerRotationEnabled = "aws_secretsmanager_rotation_enabled" // AWS-Exclusive: Automatic rotation
	AwsSecretsManagerUnusedSecret    = "aws_secretsmanager_unused_secret"
)

// -----------------------------------------------------------------------------
// AWS SNS - Simple Notification Service
// -----------------------------------------------------------------------------
const (
	AwsSNSDeliveryStatusLogging = "aws_sns_delivery_status_logging"
	AwsSNSSSEEnabledCMK         = "aws_sns_sse_enabled_cmk"
	AwsSNSTopicNoPublicAccess   = "aws_sns_topic_no_public_access"
)

// -----------------------------------------------------------------------------
// AWS SQS - Simple Queue Service
// -----------------------------------------------------------------------------
const (
	AwsSQSDLQConfigured = "aws_sqs_dlq_configured" // AWS-Exclusive: Dead Letter Queue
	AwsSQSSSEEnabled    = "aws_sqs_sse_enabled"
)

// -----------------------------------------------------------------------------
// AWS Direct Connect - Dedicated Network Connection
// -----------------------------------------------------------------------------
// AWS-exclusive service (no direct equivalent in Azure ExpressRoute/GCP Interconnect)
const (
	AwsDirectConnectConnectionDown      = "aws_directconnect_connection_down"
	AwsDirectConnectBGPPeerDown         = "aws_directconnect_bgp_peer_down"
	AwsDirectConnectLAGDown             = "aws_directconnect_lag_down"
	AwsDirectConnectLAGBelowMinimum     = "aws_directconnect_lag_below_minimum"
	AwsDirectConnectNoRedundancy        = "aws_directconnect_no_redundancy"
	AwsDirectConnectNoVirtualInterfaces = "aws_directconnect_no_virtual_interfaces"
	AwsDirectConnectVIFDown             = "aws_directconnect_vif_down"
)

// -----------------------------------------------------------------------------
// AWS Route53 - DNS Service
// -----------------------------------------------------------------------------
const (
	AwsRoute53DNSSECDisabled       = "aws_route53_dnssec_disabled"
	AwsRoute53EmptyHostedZone      = "aws_route53_empty_hosted_zone"
	AwsRoute53HealthCheckNoAlarm   = "aws_route53_health_check_no_alarm"
	AwsRoute53QueryLoggingDisabled = "aws_route53_query_logging_disabled"
	AwsRoute53UnhealthyHealthCheck = "aws_route53_unhealthy_health_check"
)

// -----------------------------------------------------------------------------
// AWS Redshift - Data Warehouse
// -----------------------------------------------------------------------------
const (
	AwsRedshiftAuditLogging       = "aws_redshift_audit_logging"
	AwsRedshiftEncryptionAtRest   = "aws_redshift_encryption_at_rest"
	AwsRedshiftEnhancedVPCRouting = "aws_redshift_enhanced_vpc_routing" // AWS-Exclusive: Enhanced VPC routing
	AwsRedshiftPublicAccess       = "aws_redshift_public_access"
	AwsRedshiftSnapshotRetention  = "aws_redshift_snapshot_retention"
	AwsRedshiftIdleCluster        = "aws_redshift_idle_cluster"
	AwsRedshiftUnderutilized      = "aws_redshift_underutilized"
	AwsRedshiftNodeGeneration     = "aws_redshift_node_generation"
	AwsRedshiftClusterVersion     = "aws_redshift_cluster_version"
	AwsRedshiftRequireSSL         = "aws_redshift_require_ssl"
)

// -----------------------------------------------------------------------------
// AWS Elasticsearch/OpenSearch Service
// -----------------------------------------------------------------------------
const (
	AwsESAuditLogsEnabled     = "aws_es_audit_logs_enabled"
	AwsESDedicatedMaster      = "aws_es_dedicated_master" // AWS-Exclusive: Dedicated master nodes
	AwsESEncryptionAtRest     = "aws_es_encryption_at_rest"
	AwsESNodeToNodeEncryption = "aws_es_node_to_node_encryption"
	AwsESSlowLogsEnabled      = "aws_es_slow_logs_enabled"
)

// -----------------------------------------------------------------------------
// AWS EFS - Elastic File System
// -----------------------------------------------------------------------------
const (
	AwsEFSEncryptionAtRest = "aws_efs_encryption_at_rest"
	AwsEFSLifecyclePolicy  = "aws_efs_lifecycle_policy" // AWS-Exclusive: Lifecycle management
)

// -----------------------------------------------------------------------------
// AWS EKS - Elastic Kubernetes Service
// -----------------------------------------------------------------------------
const (
	AwsEKSLogging          = "k8s_logging_disabled"
	AwsEKSPublicAccess     = "aws_eks_public_access"
	AwsEKSSecretEncryption = "aws_eks_secret_encryption" // AWS-Exclusive: Secrets encryption with KMS
)

// -----------------------------------------------------------------------------
// AWS Elastic Beanstalk - Application Platform
// -----------------------------------------------------------------------------
// AWS-exclusive service (no direct equivalent in Azure App Service/GCP App Engine)
const (
	AwsElasticBeanstalkEnhancedHealthDisabled = "aws_elasticbeanstalk_enhanced_health_disabled" // AWS-Exclusive: Enhanced health reporting
	AwsElasticBeanstalkOutdatedPlatform       = "aws_elasticbeanstalk_outdated_platform"
	AwsElasticBeanstalkSingleInstance         = "aws_elasticbeanstalk_single_instance"
	AwsElasticBeanstalkUnhealthy              = "aws_elasticbeanstalk_unhealthy"
)

// -----------------------------------------------------------------------------
// AWS ELB - Elastic Load Balancer (Classic)
// -----------------------------------------------------------------------------
const (
	AwsELBAccessLogs         = "aws_elb_access_logs"
	AwsELBConnectionDraining = "aws_elb_connection_draining" // AWS-Exclusive: Connection draining
	AwsELBCrossZoneBalancing = "aws_elb_cross_zone_balancing"
	AwsELBNoListeners        = "aws_elb_no_listeners"
	AwsELBUnused             = "unused_load_balancer"
)

// -----------------------------------------------------------------------------
// AWS MSK - Managed Streaming for Apache Kafka
// -----------------------------------------------------------------------------
// AWS-exclusive service (no direct equivalent in Azure Event Hubs/GCP Pub/Sub)
const (
	AwsMSKCloudWatchLogs       = "aws_msk_cloudwatch_logs"
	AwsMSKEncryptionAtRest     = "aws_msk_encryption_at_rest"
	AwsMSKEncryptionInTransit  = "aws_msk_encryption_in_transit"
	AwsMSKEnhancedMonitoring   = "aws_msk_enhanced_monitoring"
	AwsMSKPublicAccessDisabled = "aws_msk_public_access_disabled"
)

// -----------------------------------------------------------------------------
// AWS SageMaker - Machine Learning Platform
// -----------------------------------------------------------------------------
// AWS-exclusive service (no direct equivalent in Azure ML/GCP Vertex AI)
const (
	AwsSageMakerEndpointDataCapture        = "aws_sagemaker_endpoint_data_capture"
	AwsSageMakerNotebookEncryptionCMK      = "aws_sagemaker_notebook_encryption_cmk"
	AwsSageMakerNotebookNoDirectInternet   = "aws_sagemaker_notebook_no_direct_internet"
	AwsSageMakerNotebookRootAccessDisabled = "aws_sagemaker_notebook_root_access_disabled"
)

// -----------------------------------------------------------------------------
// AWS Backup - Centralized Backup Service
// -----------------------------------------------------------------------------
// AWS-exclusive service (no direct equivalent in Azure Backup/GCP Backup and DR)
const (
	AwsBackupPlanHasRules            = "aws_backup_plan_has_rules"
	AwsBackupPlanRuleLifecycle       = "aws_backup_plan_rule_lifecycle"
	AwsBackupVaultAccessPolicyExists = "aws_backup_vault_access_policy_exists"
	AwsBackupVaultEncryptionCMK      = "aws_backup_vault_encryption_cmk"
	AwsBackupVaultLockEnabled        = "aws_backup_vault_lock_enabled" // AWS-Exclusive: Vault Lock
)

// -----------------------------------------------------------------------------
// AWS Bedrock - Foundation Models
// -----------------------------------------------------------------------------
// AWS-exclusive service (no direct equivalent in Azure OpenAI/GCP Vertex AI)
const (
	AwsBedrockCustomModelOutputEncryptionCMK = "aws_bedrock_custom_model_output_encryption_cmk"
	AwsBedrockInvocationLoggingEnabled       = "aws_bedrock_invocation_logging_enabled"
	AwsBedrockInvocationLoggingCheckFailed   = "aws_bedrock_invocation_logging_check_failed"
)

// -----------------------------------------------------------------------------
// AWS CloudFormation - Infrastructure as Code
// -----------------------------------------------------------------------------
const (
	AwsCFNDriftDetectionCheck   = "aws_cfn_drift_detection_check" // AWS-Exclusive: Drift detection
	AwsCFNStackDrifted          = "aws_cfn_stack_drifted"
	AwsCFNStackPolicy           = "aws_cfn_stack_policy"
	AwsCFNTerminationProtection = "aws_cfn_termination_protection"
)

// -----------------------------------------------------------------------------
// AWS CloudWatch - Monitoring and Logging
// -----------------------------------------------------------------------------
const (
	AwsCloudWatchLogGroupEncryptionCMK = "aws_cloudwatch_log_group_encryption_cmk"
	AwsCloudWatchLogGroupRetention     = "aws_cloudwatch_log_group_retention"
)

// -----------------------------------------------------------------------------
// AWS CodeArtifact - Artifact Repository
// -----------------------------------------------------------------------------
// AWS-exclusive service (no direct equivalent in Azure Artifacts/GCP Artifact Registry)
const (
	AwsCodeArtifactRepositoryPolicy = "aws_codeartifact_repository_policy"
)

// -----------------------------------------------------------------------------
// AWS ECR - Elastic Container Registry
// -----------------------------------------------------------------------------
const (
	AwsECRPushScanEnabled    = "aws_ecr_pushscan_enabled" // AWS-Exclusive: Image scanning on push
	AwsECRTagImmutable       = "aws_ecr_tag_immutable"
	AwsECRPublicTagImmutable = "aws_ecr_public_tag_immutable"
)

// -----------------------------------------------------------------------------
// AWS SSM - Systems Manager
// -----------------------------------------------------------------------------
// AWS-exclusive service (no direct equivalent in Azure Automation/GCP OS Config)
const (
	AwsSSMInstanceOffline           = "aws_ssm_instance_offline"
	AwsSSMMaintenanceWindowDisabled = "aws_ssm_maintenance_window_disabled"
	AwsSSMMissingPatches            = "aws_ssm_missing_patches"
	AwsSSMNoAssociations            = "aws_ssm_no_associations"
	AwsSSMOutdatedAgent             = "aws_ssm_outdated_agent"
	AwsSSMParameterNotEncrypted     = "aws_ssm_parameter_not_encrypted"
)

// -----------------------------------------------------------------------------
// AWS Step Functions - Workflow Orchestration
// -----------------------------------------------------------------------------
// AWS-exclusive service (no direct equivalent in Azure Logic Apps/GCP Workflows)
const (
	AwsStepFunctionsExpressNotUtilized   = "aws_stepfunctions_express_not_utilized" // AWS-Exclusive: Express workflows
	AwsStepFunctionsHighFailureRate      = "aws_stepfunctions_high_failure_rate"
	AwsStepFunctionsLargeDefinition      = "aws_stepfunctions_large_definition"
	AwsStepFunctionsLoggingDisabled      = "aws_stepfunctions_logging_disabled"
	AwsStepFunctionsLoggingNotConfigured = "aws_stepfunctions_logging_not_configured"
	AwsStepFunctionsXRayTracingDisabled  = "aws_stepfunctions_xray_tracing_disabled"
)

// -----------------------------------------------------------------------------
// AWS VPC - Virtual Private Cloud
// -----------------------------------------------------------------------------
const (
	AwsVPCUnallocatedElasticIP = "unassociated_public_ip"
)

// -----------------------------------------------------------------------------
// AWS X-Ray - Distributed Tracing
// -----------------------------------------------------------------------------
// AWS-exclusive service (no direct equivalent in Azure Application Insights/GCP Cloud Trace)
const (
	AwsXRayEncryptionCMK      = "aws_xray_encryption_cmk"
	AwsXRaySamplingRulesExist = "aws_xray_sampling_rules_exist"
)

// -----------------------------------------------------------------------------
// AWS SES - Simple Email Service
// -----------------------------------------------------------------------------
const (
	AwsSESConfigSetEventDestinations = "aws_ses_configset_event_destinations"
	AwsSESIdentityDKIM               = "aws_ses_identity_dkim"
	AwsSESIdentityMailFromDomain     = "aws_ses_identity_mail_from_domain"
	AwsSESIdentityNotifications      = "aws_ses_identity_notifications"
)

// -----------------------------------------------------------------------------
// AWS Resource Tagging
// -----------------------------------------------------------------------------
const (
	AwsTags = "missing_tags"
)
