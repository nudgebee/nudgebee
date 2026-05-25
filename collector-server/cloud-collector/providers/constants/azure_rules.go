package constants

// =============================================================================
// AZURE-SPECIFIC RECOMMENDATION RULE NAMES - EXTRACTED AND ORGANIZED BY SERVICE
// =============================================================================

const (
	// =============================================================================
	// Azure Virtual Machine (VM) Recommendations
	// =============================================================================
	AzureVMIdleInstance                     = "vm_idle"
	AzureVMUnderutilized                    = "vm_underutilized"
	AzureVMGenerationUpgrade                = "vm_generation_upgrade"
	AzureVMBackupDisabled                   = "azure_vm_backup_disabled"
	AzureVMAcceleratedNetworkingDisabled    = "azure_vm_accelerated_networking_disabled"
	AzureVMAutomaticOSUpgradeDisabled       = "azure_vm_automatic_os_upgrade_disabled"
	AzureVMAutoShutdownDisabled             = "azure_vm_auto_shutdown_disabled"
	AzureVMBootDiagnosticsDisabled          = "azure_vm_boot_diagnostics_disabled"
	AzureVMBootDiskEncryptionDisabled       = "azure_vm_boot_disk_encryption_disabled"
	AzureVMConfidentialComputingDisabled    = "azure_vm_confidential_computing_disabled"
	AzureVMDataDiskEncryptionDisabled       = "azure_vm_data_disk_encryption_disabled"
	AzureVMDiskEncryptionCMKMissing         = "azure_vm_disk_encryption_cmk_missing"
	AzureVMEndpointProtectionMissing        = "azure_vm_endpoint_protection_missing"
	AzureVMEntraIDAuthenticationDisabled    = "azure_vm_entra_id_authentication_disabled"
	AzureVMGuestLevelDiagnosticsMissing     = "azure_vm_guest_level_diagnostics_missing"
	AzureVMJITAccessDisabled                = "azure_vm_jit_access_disabled"
	AzureVMMonitorAgentMissing              = "azure_vm_monitor_agent_missing"
	AzureVMPremiumSSDOSDiskUsed             = "azure_vm_premium_ssd_os_disk_used"
	AzureVMSSHPasswordAuthenticationEnabled = "azure_vm_ssh_password_authentication_enabled"
	AzureVMSystemAssignedIdentityDisabled   = "azure_vm_system_assigned_identity_disabled"
	AzureVMTrustedLaunchDisabled            = "azure_vm_trusted_launch_disabled"
	AzureVMUnmanagedDisk                    = "azure_vm_unmanaged_disk"

	// =============================================================================
	// Azure Virtual Machine Scale Sets (VMSS) Recommendations
	// =============================================================================
	AzureVMSSAcceleratedNetworkingDisabled     = "azure_vmss_accelerated_networking_disabled"
	AzureVMSSApplicationHealthExtensionMissing = "azure_vmss_application_health_extension_missing"
	AzureVMSSAutomaticInstanceRepairsDisabled  = "azure_vmss_automatic_instance_repairs_disabled"
	AzureVMSSAutomaticOSUpgradeDisabled        = "azure_vmss_automatic_os_upgrade_disabled"
	AzureVMSSBootDiagnosticsDisabled           = "azure_vmss_boot_diagnostics_disabled"
	AzureVMSSEmpty                             = "azure_vmss_empty"
	AzureVMSSInstancePublicIPAssigned          = "azure_vmss_instance_public_ip_assigned"
	AzureVMSSManualUpgradePolicy               = "azure_vmss_manual_upgrade_policy"
	AzureVMSSNotZoneRedundant                  = "azure_vmss_not_zone_redundant"
	AzureVMSSOverprovisionDisabled             = "azure_vmss_overprovision_disabled"
	AzureVMSSSystemAssignedIdentityDisabled    = "azure_vmss_system_assigned_identity_disabled"
	AzureVMSSTrustedLaunchDisabled             = "azure_vmss_trusted_launch_disabled"

	// =============================================================================
	// Azure Kubernetes Service (AKS) Recommendations
	// =============================================================================
	AzureAKSRBACDisabled          = "azure_aks_rbac_disabled"
	AzureAKSEnableRBAC            = "azure_aks_enable_rbac"
	AzureAKSNetworkPolicyDisabled = "k8s_network_policy"
	AzureAKSOldKubernetesVersion  = "azure_aks_old_kubernetes_version"
	AzureAKSAzurePolicyDisabled   = "azure_aks_azure_policy_disabled"

	// =============================================================================
	// Azure App Service Recommendations
	// =============================================================================
	AzureAppServiceHTTPSOnlyDisabled  = "azure_app_service_https_only_disabled"
	AzureAppServiceClientCertDisabled = "azure_app_service_client_cert_disabled"
	AzureAppServicePlanOptimization   = "azure_app_service_plan_optimization"
	AzureAppServiceStoppedApp         = "azure_app_service_stopped_app"

	// =============================================================================
	// Azure Storage Account Recommendations
	// =============================================================================
	AzureStorageCMKDisabled                      = "storage_no_cmek"
	AzureStorageVersioningDisabled               = "storage_versioning_disabled"
	AzureStorageHTTPSOnlyDisabled                = "azure_storage_https_only_disabled"
	AzureStorageMinimumTLSVersion                = "azure_storage_minimum_tls_version"
	AzureStorageMinimumTLSVersionNotSetTo12      = "azure_storage_minimum_tls_version_not_set_to_1_2"
	AzureStorageAnonymousAccessEnabled           = "azure_storage_anonymous_access_enabled"
	AzureStorageBlobPublicAccessEnabled          = "storage_public_access"
	AzureStorageFirewallNotConfigured            = "azure_storage_firewall_not_configured"
	AzureStorageGeoRedundantStorageDisabled      = "azure_storage_geo_redundant_storage_disabled"
	AzureStorageInfrastructureEncryptionDisabled = "azure_storage_infrastructure_encryption_disabled"
	AzureStorageLoggingForDeleteAccessDisabled   = "azure_storage_logging_for_delete_access_disabled"
	AzureStorageLoggingForReadAccessDisabled     = "azure_storage_logging_for_read_access_disabled"
	AzureStorageLoggingForWriteAccessDisabled    = "azure_storage_logging_for_write_access_disabled"
	AzureStoragePublicNetworkAccessEnabled       = "azure_storage_public_network_access_enabled"
	AzureStorageSecureTransferDisabled           = "azure_storage_secure_transfer_disabled"
	AzureStorageSharedKeyAccessEnabled           = "azure_storage_shared_key_access_enabled"
	AzureStorageSoftDeleteDisabled               = "azure_storage_soft_delete_disabled"
	AzureStorageAccessTierOptimization           = "storage_class_optimization"
	AzureStorageAccountPotentiallyIdle           = "azure_storage_account_potentially_idle"
	AzureStoragePerformanceTierUpgrade           = "azure_storage_performance_tier_upgrade"
	AzureStorageRedundancyOptimization           = "azure_storage_redundancy_optimization"
	AzureStorageMissingTags                      = "missing_tags"

	// =============================================================================
	// Azure SQL Database Recommendations
	// =============================================================================
	AzureSQLPublicNetworkAccessEnabled        = "db_public_access"
	AzureSQLTransparentDataEncryptionDisabled = "azure_sql_transparent_data_encryption_disabled"
	AzureSQLAdvancedDataSecurityDisabled      = "azure_sql_advanced_data_security_disabled"
	AzureSQLDatabasePricingModelUpgrade       = "azure_sql_database_pricing_model_upgrade"
	AzureSQLEntraIDAdminNotConfigured         = "azure_sql_entra_id_admin_not_configured"
	AzureSQLGeoRedundantBackupsDisabled       = "azure_sql_geo_redundant_backups_disabled"
	AzureSQLLongTermRetentionNotConfigured    = "azure_sql_long_term_retention_not_configured"
	AzureSQLServerlessOptimization            = "azure_sql_serverless_optimization"
	AzureSQLStorageAutoGrowthDisabled         = "db_storage_autoscaling"

	// =============================================================================
	// Azure Defender Recommendations
	// =============================================================================
	AzureDefenderAutoProvisionDisabled = "azure_defender_auto_provision_disabled"
	AzureDefenderFreeTier              = "azure_defender_free_tier"
	AzureDefenderNoSecurityContacts    = "azure_defender_no_security_contacts"
	AzureDefenderUnhealthyAssessment   = "azure_defender_unhealthy_assessment"

	// =============================================================================
	// Azure Sentinel Recommendations
	// =============================================================================
	AzureSentinelAlertRuleDisabled = "azure_sentinel_alert_rule_disabled"
	AzureSentinelIncidentNoOwner   = "azure_sentinel_incident_no_owner"
	AzureSentinelNoAutomationRules = "azure_sentinel_no_automation_rules"
	AzureSentinelNoDataConnectors  = "azure_sentinel_no_data_connectors"
	AzureSentinelNoThreatIntel     = "azure_sentinel_no_threat_intel"
	AzureSentinelStaleIncident     = "azure_sentinel_stale_incident"

	// =============================================================================
	// Azure Key Vault Recommendations
	// =============================================================================
	AzureKeyVaultPurgeProtectionDisabled = "azure_keyvault_purge_protection_disabled"
	AzureKeyVaultSoftDeleteDisabled      = "azure_keyvault_soft_delete_disabled"

	// =============================================================================
	// Azure Application Gateway Recommendations
	// =============================================================================
	AzureAppGatewayHTTP2Disabled = "azure_appgateway_http2_disabled"
	AzureAppGatewayStopped       = "azure_appgateway_stopped"
	AzureAppGatewayWAFDisabled   = "azure_appgateway_waf_disabled"

	// =============================================================================
	// Azure Container Registry Recommendations
	// =============================================================================
	AzureContainerRegistryAdminUserEnabled           = "azure_container_registry_admin_user_enabled"
	AzureContainerRegistryARMTokenAuthEnabled        = "azure_container_registry_arm_token_auth_enabled"
	AzureContainerRegistryCMKEncryptionDisabled      = "azure_container_registry_cmk_encryption_disabled"
	AzureContainerRegistryNoIPRules                  = "azure_container_registry_no_ip_rules"
	AzureContainerRegistryNoManagedIdentity          = "azure_container_registry_no_managed_identity"
	AzureContainerRegistryNoPrivateEndpoints         = "azure_container_registry_no_private_endpoints"
	AzureContainerRegistryPublicNetworkAccessEnabled = "azure_container_registry_public_network_access_enabled"
	AzureContainerRegistrySoftDeleteDisabled         = "azure_container_registry_soft_delete_disabled"
	AzureContainerRegistryTrustedMSDisabled          = "azure_container_registry_trusted_ms_disabled"
	AzureContainerRegistryZoneRedundancyDisabled     = "azure_container_registry_zone_redundancy_disabled"

	// =============================================================================
	// Azure CosmosDB Recommendations
	// =============================================================================
	AzureCosmosDBAutomaticFailoverDisabled = "azure_cosmosdb_automatic_failover_disabled"
	AzureCosmosDBSingleRegion              = "azure_cosmosdb_single_region"

	// =============================================================================
	// Azure ExpressRoute Recommendations
	// =============================================================================
	AzureExpressRouteEnableGlobalReach = "azure_expressroute_enable_global_reach"
	AzureExpressRouteEnableStandardSKU = "azure_expressroute_enable_standard_sku"

	// =============================================================================
	// Azure Firewall Recommendations
	// =============================================================================
	AzureFirewallEnableDNSProxy    = "azure_firewall_enable_dns_proxy"
	AzureFirewallEnableThreatIntel = "azure_firewall_enable_threat_intel"

	// =============================================================================
	// Azure Front Door Recommendations
	// =============================================================================
	AzureFrontDoorEnableHTTPSRedirect = "azure_frontdoor_enable_https_redirect"
	AzureFrontDoorEnableWAF           = "azure_frontdoor_enable_waf"
	AzureFrontDoorProfileNoEndpoints  = "azure_frontdoor_profile_no_endpoints"

	// =============================================================================
	// Azure MariaDB Recommendations
	// =============================================================================
	AzureMariaDBBackupDisabled = "db_backup_disabled"
	AzureMariaDBServerStopped  = "azure_mariadb_server_stopped"
	AzureMariaDBSSLDisabled    = "azure_mariadb_ssl_disabled"

	// =============================================================================
	// Azure MySQL Recommendations
	// =============================================================================
	AzureMySQLBackupDisabled = "db_backup_disabled"
	AzureMySQLServerStopped  = "azure_mysql_server_stopped"

	// =============================================================================
	// Azure PostgreSQL Recommendations
	// =============================================================================
	AzurePostgresBackupDisabled = "db_backup_disabled"
	AzurePostgresServerStopped  = "azure_postgres_server_stopped"

	// =============================================================================
	// Azure Virtual Network (VNet) Recommendations
	// =============================================================================
	AzureVNetDDOSProtectionDisabled        = "azure_vnet_ddos_protection_disabled"
	AzureVNetVMProtectionDisabled          = "azure_vnet_vm_protection_disabled"
	AzureVNetAddressSpaceOverprovisioned   = "azure_vnet_address_space_overprovisioned"
	AzureVNetCustomDNSNotConfigured        = "azure_vnet_custom_dns_not_configured"
	AzureVNetEmptyNoSubnets                = "azure_vnet_empty_no_subnets"
	AzureVNetGatewayTransitNotOptimized    = "azure_vnet_gateway_transit_not_optimized"
	AzureVNetServiceEndpointsNotConfigured = "azure_vnet_service_endpoints_not_configured"
	AzureVNetSubnetWithoutNSG              = "azure_vnet_subnet_without_nsg"

	// =============================================================================
	// Azure Disk Recommendations
	// =============================================================================
	AzureDiskPremiumSSDV2Upgrade        = "azure_disk_premium_ssd_v2_upgrade"
	AzureDiskPublicNetworkAccessEnabled = "azure_disk_public_network_access_enabled"
	AzureDiskUnattachedCMKMissing       = "azure_disk_unattached_cmk_missing"
	AzureDiskUnattachedUnencrypted      = "azure_disk_unattached_unencrypted"
	AzureDiskUnattachedVolume           = "orphaned_volume"
	AzureManagedDiskSKUUpgrade          = "azure_managed_disk_sku_upgrade"

	// =============================================================================
	// Azure Files Recommendations
	// =============================================================================
	AzureFilesEnableSMBEncryption = "azure_files_enable_smb_encryption"
	AzureFilesLargeQuota          = "azure_files_large_quota"
	AzureFilesSMBNotEnabled       = "azure_files_smb_not_enabled"
	AzureFilesUnusedFileShare     = "azure_files_unused_file_share"

	// =============================================================================
	// Azure Redis Cache Recommendations
	// =============================================================================
	AzureRedisNonSSLPortEnabled   = "azure_redis_non_ssl_port_enabled"
	AzureRedisOldTLSVersion       = "azure_redis_old_tls_version"
	AzureRedisOverprovisionedSKU  = "azure_redis_overprovisioned_sku"
	AzureRedisPublicNetworkAccess = "azure_redis_public_network_access"

	// =============================================================================
	// Azure Load Balancer Recommendations
	// =============================================================================
	AzureLoadBalancerBasicSKU        = "azure_load_balancer_basic_sku"
	AzureLoadBalancerNoHealthProbes  = "azure_load_balancer_no_health_probes"
	AzureLoadBalancerNoOutboundRules = "azure_load_balancer_no_outbound_rules"
	AzureUnusedLoadBalancer          = "unused_load_balancer"

	// =============================================================================
	// Azure Public IP Recommendations
	// =============================================================================
	AzureUnassociatedPublicIP = "unassociated_public_ip"

	// =============================================================================
	// Azure DDoS Protection Recommendations
	// =============================================================================
	AzureDDoSProtectionNoPlan               = "azure_ddos_protection_no_plan"
	AzureDDoSProtectionPlanNoVNets          = "azure_ddos_protection_plan_no_vnets"
	AzureDDoSProtectionPublicIPDisabled     = "azure_ddos_protection_public_ip_disabled"
	AzureDDoSProtectionPublicIPNotProtected = "azure_ddos_protection_public_ip_not_protected"
	AzureDDoSProtectionVNetNotProtected     = "azure_ddos_protection_vnet_not_protected"

	// =============================================================================
	// Azure DNS Recommendations
	// =============================================================================
	AzureDNSAddCAARecord = "azure_dns_add_caa_record"

	// =============================================================================
	// Azure Entra ID (formerly Azure AD) Recommendations
	// =============================================================================
	AzureEntraIDGuestWithPrivilegedRole              = "azure_entra_id_guest_with_privileged_role"
	AzureEntraIDOverlyPermissiveRole                 = "azure_entra_id_overly_permissive_role"
	AzureEntraIDServicePrincipalCertificatesExpired  = "azure_entra_id_service_principal_certificates_expired"
	AzureEntraIDServicePrincipalCertificatesExpiring = "azure_entra_id_service_principal_certificates_expiring_soon"
	AzureEntraIDServicePrincipalCredentialsExpired   = "azure_entra_id_service_principal_credentials_expired"
	AzureEntraIDServicePrincipalCredentialsExpiring  = "azure_entra_id_service_principal_credentials_expiring_soon"

	// =============================================================================
	// Azure EventGrid Recommendations
	// =============================================================================
	AzureEventGridDomainLocalAuthEnabled      = "azure_eventgrid_domain_local_auth_enabled"
	AzureEventGridDomainPublicAccessEnabled   = "azure_eventgrid_domain_public_access_enabled"
	AzureEventGridResourceFailedProvisioning  = "azure_eventgrid_resource_failed_provisioning"
	AzureEventGridTopicNoManagedIdentity      = "azure_eventgrid_topic_no_managed_identity"
	AzureEventGridTopicPublicAccessNoIPFilter = "azure_eventgrid_topic_public_access_no_ip_filter"

	// =============================================================================
	// Azure Function Recommendations
	// =============================================================================
	AzureFunctionAuthenticationDisabled = "azure_function_authentication_disabled"
	AzureFunctionHTTPSOnlyDisabled      = "azure_function_https_only_disabled"
	AzureFunctionOldRuntime             = "azure_function_old_runtime"

	// =============================================================================
	// Azure Logic App Recommendations
	// =============================================================================
	AzureLogicAppNoActions        = "azure_logic_app_no_actions"
	AzureLogicAppNoTriggers       = "azure_logic_app_no_triggers"
	AzureLogicAppOutdatedWorkflow = "azure_logic_app_outdated_workflow"
	AzureLogicAppWorkflowDisabled = "azure_logic_app_workflow_disabled"

	// =============================================================================
	// Azure ML Workspace Recommendations
	// =============================================================================
	AzureMLWorkspaceHBINotEnabled              = "azure_ml_workspace_hbi_not_enabled"
	AzureMLWorkspaceManagedIdentityDisabled    = "azure_ml_workspace_managed_identity_disabled"
	AzureMLWorkspacePublicNetworkAccessEnabled = "azure_ml_workspace_public_network_access_enabled"

	// =============================================================================
	// Azure Monitor Recommendations
	// =============================================================================
	AzureMonitorActionGroupDisabled    = "azure_monitor_action_group_disabled"
	AzureMonitorActionGroupNoReceivers = "azure_monitor_action_group_no_receivers"
	AzureMonitorAlertNoActionGroup     = "azure_monitor_alert_no_action_group"
	AzureMonitorAlertRuleDisabled      = "azure_monitor_alert_rule_disabled"
	AzureMonitorNoActionGroups         = "azure_monitor_no_action_groups"
	AzureMonitorNoAlertRules           = "azure_monitor_no_alert_rules"

	// =============================================================================
	// Azure Metrics Alert Recommendations
	// =============================================================================
	AzureMetricAlertAutoMitigationDisabled = "azure_metric_alert_auto_mitigation_disabled"
	AzureMetricAlertBroadScope             = "azure_metric_alert_broad_scope"
	AzureMetricAlertDisabled               = "azure_metric_alert_disabled"
	AzureMetricAlertInefficientEvaluation  = "azure_metric_alert_inefficient_evaluation"
	AzureMetricAlertMissingTags            = "missing_tags"
	AzureMetricAlertNoActionGroup          = "azure_metric_alert_no_action_group"

	// =============================================================================
	// Azure Scheduled Query Rules Recommendations
	// =============================================================================
	AzureScheduledQueryRuleAutoMitigationDisabled = "azure_scheduled_query_rule_auto_mitigation_disabled"
	AzureScheduledQueryRuleDisabled               = "azure_scheduled_query_rule_disabled"
	AzureScheduledQueryRuleEmptyQuery             = "azure_scheduled_query_rule_empty_query"
	AzureScheduledQueryRuleMissingTags            = "missing_tags"
	AzureScheduledQueryRuleNoActionGroup          = "azure_scheduled_query_rule_no_action_group"
	AzureScheduledQueryRuleNoScopes               = "azure_scheduled_query_rule_no_scopes"

	// =============================================================================
	// Azure Activity Log Alert Recommendations
	// =============================================================================
	AzureActivityLogAlertBroadScope     = "azure_activity_log_alert_broad_scope"
	AzureActivityLogAlertDisabled       = "azure_activity_log_alert_disabled"
	AzureActivityLogAlertEmptyCondition = "azure_activity_log_alert_empty_condition"
	AzureActivityLogAlertMissingTags    = "missing_tags"
	AzureActivityLogAlertNoActionGroup  = "azure_activity_log_alert_no_action_group"
	AzureActivityLogAlertNoConditions   = "azure_activity_log_alert_no_conditions"
	AzureActivityLogAlertNoScopes       = "azure_activity_log_alert_no_scopes"

	// =============================================================================
	// Azure Operational Insights Recommendations
	// =============================================================================
	AzureOperationalInsightsWorkspaceLowRetention = "azure_operationalinsights_workspace_low_retention"

	// =============================================================================
	// Azure Policy Recommendations
	// =============================================================================
	AzurePolicyAssignmentNoDescription    = "azure_policy_assignment_no_description"
	AzurePolicyAssignmentNotEnforced      = "azure_policy_assignment_not_enforced"
	AzurePolicyCustomDefinitionNoMetadata = "azure_policy_custom_definition_no_metadata"
	AzurePolicyDefinitionNoCategory       = "azure_policy_definition_no_category"
	AzurePolicyNoAssignments              = "azure_policy_no_assignments"

	// =============================================================================
	// Azure DevOps Recommendations
	// =============================================================================
	AzureDevOpsNoPipelines             = "azure_devops_no_pipelines"
	AzureDevOpsProjectNoDescription    = "azure_devops_project_no_description"
	AzureDevOpsProjectPublicVisibility = "azure_devops_project_public_visibility"
	AzureDevOpsRepositoryDisabled      = "azure_devops_repository_disabled"
	AzureDevOpsRepositoryLargeSize     = "azure_devops_repository_large_size"

	// =============================================================================
	// Azure Pipeline Recommendations
	// =============================================================================
	AzurePipelineBuildFailed     = "azure_pipeline_build_failed"
	AzurePipelineBuildNoBranch   = "azure_pipeline_build_no_branch"
	AzurePipelineHighFailureRate = "azure_pipeline_high_failure_rate"

	// =============================================================================
	// Azure Container App Recommendations
	// =============================================================================
	AzureContainerAppInsecureIngress     = "azure_container_app_insecure_ingress"
	AzureContainerAppLowMinReplicas      = "azure_container_app_low_min_replicas"
	AzureContainerAppMissingTags         = "missing_tags"
	AzureContainerAppNoManagedIdentity   = "azure_container_app_no_managed_identity"
	AzureContainerAppPublicIngressNoAuth = "azure_container_app_public_ingress_no_auth"
	AzureContainerAppSecretsNotKeyVault  = "azure_container_app_secrets_not_keyvault"

	// =============================================================================
	// Azure Arc Recommendations
	// =============================================================================
	AzureArcMachineDisconnected = "azure_arc_machine_disconnected"
	AzureArcMachineStaleStatus  = "azure_arc_machine_stale_status"
	AzureArcOutdatedAgent       = "azure_arc_outdated_agent"

	// =============================================================================
	// Azure Bot Service Recommendations
	// =============================================================================
	AzureBotServiceManagedIdentityDisabled    = "azure_bot_service_managed_identity_disabled"
	AzureBotServicePublicNetworkAccessEnabled = "azure_bot_service_public_network_access_enabled"

	// =============================================================================
	// Azure Tags
	// =============================================================================
	AzureMissingTags = "missing_tags"
)
