package constants

// =============================================================================
// GCP-SPECIFIC RECOMMENDATION RULE NAMES (EXTRACTED)
// =============================================================================
// Organized by GCP service type for better maintainability

const (
	// =============================================================================
	// GCP Compute Engine Recommendations
	// =============================================================================
	GCPComputeIdleInstance      = "vm_idle"
	GCPComputeUnderutilized     = "vm_underutilized"
	GCPComputeGenerationUpgrade = "vm_generation_upgrade"
	GCPComputeOldInstance       = "gcp_compute_old_instance"
	GCPComputeStoppedInstance   = "vm_stopped"
	GCPComputeNoLabels          = "missing_tags"

	// =============================================================================
	// GCP Cloud Storage Recommendations
	// =============================================================================
	GCPStorageNoCMEK            = "storage_no_cmek"
	GCPStorageNoLifecycle       = "storage_no_lifecycle"
	GCPStorageNoVersioning      = "storage_versioning_disabled"
	GCPStoragePublicAccess      = "storage_public_access"
	GCPStorageNoLogging         = "gcp_storage_no_logging"
	GCPStorageNoUBLA            = "gcp_storage_no_ubla"
	GCPStorageNoLabels          = "missing_tags"
	GCPStorageOldBucket         = "gcp_storage_old_bucket"
	GCPStorageClassOptimization = "storage_class_optimization"

	// =============================================================================
	// GCP BigQuery Recommendations
	// =============================================================================
	GCPBigQueryDatasetNoCMEK                  = "gcp_bigquery_dataset_no_cmek"
	GCPBigQueryDatasetNoDefaultExpiration     = "gcp_bigquery_dataset_no_default_expiration"
	GCPBigQueryDatasetNoLabels                = "missing_tags"
	GCPBigQueryTableNoClustering              = "gcp_bigquery_table_no_clustering"
	GCPBigQueryTableNoExpiration              = "gcp_bigquery_table_no_expiration"
	GCPBigQueryTableNoLabels                  = "missing_tags"
	GCPBigQueryTableNoPartitioning            = "gcp_bigquery_table_no_partitioning"
	GCPBigQueryTableUnused                    = "gcp_bigquery_table_unused"
	GCPBigQuerySlotsUtilizationAlarmMissing   = "gcp_bigquery_slots_utilization_alarm_missing"
	GCPBigQueryQueryExecutionTimeAlarmMissing = "gcp_bigquery_query_execution_time_alarm_missing"
	GCPBigQueryHighTotalStorageAlarmMissing   = "gcp_bigquery_high_total_storage_alarm_missing"

	// =============================================================================
	// GCP Cloud SQL Recommendations
	// =============================================================================
	GCPSQLNoHighAvailability = "gcp_sql_no_ha"
	GCPSQLNoBackup           = "db_backup_disabled"
	GCPSQLNoSSL              = "gcp_sql_no_ssl"
	GCPSQLNoLabels           = "missing_tags"
	GCPSQLInactiveInstance   = "gcp_sql_inactive_instance"
	GCPSQLOldInstance        = "gcp_sql_old_instance"

	// =============================================================================
	// GCP GKE (Google Kubernetes Engine) Recommendations
	// =============================================================================
	GCPGKENoAutoscaling         = "gcp_gke_no_autoscaling"
	GCPGKELoggingDisabled       = "k8s_logging_disabled"
	GCPGKEMonitoringDisabled    = "gcp_gke_monitoring_disabled"
	GCPGKENoWorkloadIdentity    = "gcp_gke_no_workload_identity"
	GCPGKENoLabels              = "missing_tags"
	GCPGKEOldCluster            = "gcp_gke_old_cluster"
	GCPGKEInactiveCluster       = "gcp_gke_inactive_cluster"
	GCPGKENoBinaryAuthorization = "gcp_gke_no_binary_authorization"
	GCPGKENoMaintenanceWindow   = "gcp_gke_no_maintenance_window"
	GCPGKENoNetworkPolicy       = "k8s_network_policy"

	// =============================================================================
	// GCP Cloud Functions Recommendations
	// =============================================================================
	GCPFunctionHighMemory   = "gcp_function_high_memory"
	GCPFunctionPublicAccess = "gcp_function_public_access"
	GCPFunctionNotActive    = "gcp_function_not_active"
	GCPFunctionNoLabels     = "missing_tags"

	// =============================================================================
	// GCP Cloud Run Recommendations
	// =============================================================================
	GCPRunHighMemory    = "gcp_run_high_memory"
	GCPRunPublicIngress = "gcp_run_public_ingress"
	GCPRunNoLabels      = "missing_tags"
	GCPRunNotReady      = "gcp_run_not_ready"
	GCPRunAlwaysOn      = "gcp_run_always_on"

	// =============================================================================
	// GCP Pub/Sub Recommendations
	// =============================================================================
	GCPPubSubNoDeadLetter  = "gcp_pubsub_no_dead_letter"
	GCPPubSubLongRetention = "gcp_pubsub_long_retention"
	GCPPubSubRetainAcked   = "gcp_pubsub_retain_acked"
)
