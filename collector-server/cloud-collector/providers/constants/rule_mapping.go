package constants

import "sync"

// =============================================================================
// RULE MAPPING - Generic to Provider-Specific
// =============================================================================
// Maps generic cross-provider rules to their provider-specific implementations.
// Provides efficient O(1) lookups using cached maps with sync.Once initialization.
// =============================================================================

// RuleMapping defines the relationship between a generic rule and its provider implementation
type RuleMapping struct {
	GenericRuleName      string // Generic rule name (e.g., IdleInstance)
	Provider             string // Provider: AWS, Azure, or GCP
	ProviderSpecificRule string // Provider-specific constant (e.g., AwsEC2IdleInstance)
	Category             string // RightSizing, Security, Configuration, etc.
}

// Cache variables for O(1) lookups
var (
	allMappings          []RuleMapping
	genericToProviderMap map[string][]RuleMapping     // Generic -> []Provider mappings
	providerToGenericMap map[string]map[string]string // Provider -> ProviderRule -> Generic
	providerMappings     map[string][]RuleMapping     // Provider -> All its mappings
	categoryMappings     map[string][]RuleMapping     // Category -> All its mappings
	once                 sync.Once
)

// initializeMappings sets up the cached maps (called once via sync.Once)
func initializeMappings() {
	allMappings = []RuleMapping{
		// RIGHTSIZING - Compute
		{IdleInstance, ProviderAWS, AwsEC2IdleInstance, CategoryRightSizing},
		{IdleInstance, ProviderAWS, AwsRDSIdleInstance, CategoryRightSizing},
		{IdleInstance, ProviderAWS, AwsElastiCacheIdleInstance, CategoryRightSizing},
		{IdleInstance, ProviderAzure, AzureVMIdleInstance, CategoryRightSizing},

		{UnderutilizedInstance, ProviderAWS, AwsEC2Underutilized, CategoryRightSizing},
		{UnderutilizedInstance, ProviderAWS, AwsRDSUnderutilized, CategoryRightSizing},
		{UnderutilizedInstance, ProviderAWS, AwsFargateServiceUnderutilized, CategoryRightSizing},
		{UnderutilizedInstance, ProviderAzure, AzureVMUnderutilized, CategoryRightSizing},

		{OverutilizedInstance, ProviderAWS, AwsRDSOverutilized, CategoryRightSizing},
		{OverutilizedInstance, ProviderAWS, AwsFargateServiceOverutilized, CategoryRightSizing},
		{OverutilizedInstance, ProviderAWS, AwsECSFargateServiceOverutilized, CategoryRightSizing},

		{StoppedInstance, ProviderAWS, AwsEC2StoppedInstanceIncurringStorageCost, CategoryRightSizing},
		{StoppedInstance, ProviderAzure, AzureAppServiceStoppedApp, CategoryRightSizing},

		{InstanceGenerationUpgrade, ProviderAWS, AwsEC2InstanceGenerationUpgrade, CategoryInfraUpgrade},
		{InstanceGenerationUpgrade, ProviderAWS, AwsEC2EBSGenerationUpgrade, CategoryInfraUpgrade},
		{InstanceGenerationUpgrade, ProviderAWS, AwsRDSInstanceGeneration, CategoryInfraUpgrade},
		{InstanceGenerationUpgrade, ProviderAWS, AwsElastiCacheInstanceGeneration, CategoryInfraUpgrade},
		{InstanceGenerationUpgrade, ProviderAzure, AzureVMGenerationUpgrade, CategoryInfraUpgrade},

		{AlternateInstances, ProviderAWS, AwsEC2AlternateInstances, CategoryRightSizing},
		{AlternateInstances, ProviderAWS, AwsRDSAlternateInstances, CategoryRightSizing},

		// RIGHTSIZING - Storage
		{UnattachedVolume, ProviderAWS, AwsEC2OrphanedVolume, CategoryRightSizing},
		{UnattachedVolume, ProviderAzure, AzureDiskUnattachedVolume, CategoryRightSizing},

		// SECURITY - Encryption
		{EncryptionAtRest, ProviderAWS, AwsEC2EBSEncrypt, CategorySecurity},
		{EncryptionAtRest, ProviderAWS, AwsRDSStorageEncrypted, CategorySecurity},
		{EncryptionAtRest, ProviderAzure, AzureVMBootDiskEncryptionDisabled, CategorySecurity},
		{EncryptionAtRest, ProviderAzure, AzureVMDataDiskEncryptionDisabled, CategorySecurity},

		{EncryptionCMK, ProviderAzure, AzureVMDiskEncryptionCMKMissing, CategorySecurity},

		// SECURITY - Access Control
		{PublicAccess, ProviderAWS, AwsRDSPublicAccess, CategorySecurity},
		{PublicAccess, ProviderAWS, AwsS3PublicAccessACL, CategorySecurity},
		{PublicAccess, ProviderAWS, AwsS3PublicAccessPolicy, CategorySecurity},

		// CONFIGURATION - Backup
		{BackupDisabled, ProviderAWS, AwsRDSBackupEnabled, CategoryConfiguration},
		{BackupDisabled, ProviderAWS, AwsDynamoDBPITREnabled, CategoryConfiguration},
		{BackupDisabled, ProviderAzure, AzureVMBackupDisabled, CategoryConfiguration},

		// CONFIGURATION - Monitoring
		{MonitoringDisabled, ProviderAzure, AzureVMBootDiagnosticsDisabled, CategoryConfiguration},

		// CONFIGURATION - Auto Upgrade
		{AutoUpgradeDisabled, ProviderAWS, AwsRDSAutoMinorUpgrade, CategoryConfiguration},
		{AutoUpgradeDisabled, ProviderAzure, AzureVMAutomaticOSUpgradeDisabled, CategoryConfiguration},

		// INFRASTRUCTURE UPGRADE
		{DeprecatedRuntime, ProviderAWS, AwsLambdaDeprecatedRuntime, CategoryInfraUpgrade},
	}

	// Build cached maps
	genericToProviderMap = make(map[string][]RuleMapping)
	providerToGenericMap = make(map[string]map[string]string)
	providerMappings = make(map[string][]RuleMapping)
	categoryMappings = make(map[string][]RuleMapping)

	for _, mapping := range allMappings {
		genericToProviderMap[mapping.GenericRuleName] = append(
			genericToProviderMap[mapping.GenericRuleName], mapping)

		if providerToGenericMap[mapping.Provider] == nil {
			providerToGenericMap[mapping.Provider] = make(map[string]string)
		}
		providerToGenericMap[mapping.Provider][mapping.ProviderSpecificRule] = mapping.GenericRuleName

		providerMappings[mapping.Provider] = append(providerMappings[mapping.Provider], mapping)
		categoryMappings[mapping.Category] = append(categoryMappings[mapping.Category], mapping)
	}
}

// GetAllRuleMappings returns all rule mappings
func GetAllRuleMappings() []RuleMapping {
	once.Do(initializeMappings)
	return allMappings
}

// GetRuleMappingsByGenericRule returns all provider implementations for a generic rule
func GetRuleMappingsByGenericRule(genericRuleName string) []RuleMapping {
	once.Do(initializeMappings)
	return genericToProviderMap[genericRuleName]
}

// GetProviderRulesForGeneric returns provider-specific rule names for a generic rule
func GetProviderRulesForGeneric(genericRuleName string) []string {
	once.Do(initializeMappings)
	mappings := genericToProviderMap[genericRuleName]
	rules := make([]string, len(mappings))
	for i, m := range mappings {
		rules[i] = m.ProviderSpecificRule
	}
	return rules
}

// GetGenericRuleName returns the generic rule name for a provider-specific rule
func GetGenericRuleName(providerRuleName, provider string) string {
	once.Do(initializeMappings)
	if providerMap, ok := providerToGenericMap[provider]; ok {
		return providerMap[providerRuleName]
	}
	return ""
}

// GetProviderRuleName returns the provider-specific rule name for a generic rule
func GetProviderRuleName(genericRuleName, provider string) string {
	once.Do(initializeMappings)
	mappings := genericToProviderMap[genericRuleName]
	for _, m := range mappings {
		if m.Provider == provider {
			return m.ProviderSpecificRule
		}
	}
	return ""
}

// GetRuleMappingsByProvider returns all rules for a specific provider
func GetRuleMappingsByProvider(provider string) []RuleMapping {
	once.Do(initializeMappings)
	return providerMappings[provider]
}

// GetRuleMappingsByCategory returns all rules in a category
func GetRuleMappingsByCategory(category string) []RuleMapping {
	once.Do(initializeMappings)
	return categoryMappings[category]
}

// IsGenericRuleName checks if a rule name is a generic rule
func IsGenericRuleName(ruleName string) bool {
	once.Do(initializeMappings)
	_, exists := genericToProviderMap[ruleName]
	return exists
}

// IsProviderSpecificRule checks if a rule name is provider-specific
func IsProviderSpecificRule(ruleName, provider string) bool {
	once.Do(initializeMappings)
	if providerMap, ok := providerToGenericMap[provider]; ok {
		_, exists := providerMap[ruleName]
		return exists
	}
	return false
}
