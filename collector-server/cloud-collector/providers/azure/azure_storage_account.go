package azure

import (
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage"
)

type storageAccountService struct {
}

func (s *storageAccountService) Name() string {
	return "Microsoft.Storage/storageAccounts"
}

// Scope returns the service scope - this is a regional service
func (s *storageAccountService) Scope() ServiceScope {
	return ServiceScopeRegional
}

func (s *storageAccountService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
	cred, session, err := getAzureCredsForAccount(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("failed to create azure credential: %w", err)
	}

	var allResources []providers.Resource
	var subscriptionIDs = strings.Split(session.SubscriptionID, ",")
	for _, subID := range subscriptionIDs {
		if strings.TrimSpace(subID) == "" {
			continue
		}
		client, err := armstorage.NewAccountsClient(subID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to create storage account client: %w", err)
		}

		pager := client.NewListPager(nil)
		for pager.More() {
			page, err := pager.NextPage(ctx.GetContext())
			if err != nil {
				return nil, fmt.Errorf("failed to get next page: %w", err)
			}
			for _, sa := range page.Value {
				status := providers.ResourceStatusUnknown
				if sa.Properties != nil && sa.Properties.ProvisioningState != nil {
					if val, ok := nbStatusFromAzureProvisioningState[string(*sa.Properties.ProvisioningState)]; ok {
						status = val
					}
				}

				createdAt := time.Time{}
				if sa.Properties != nil && sa.Properties.CreationTime != nil {
					createdAt = *sa.Properties.CreationTime
				}

				allResources = append(allResources, providers.Resource{
					Id:          *sa.ID,
					Name:        *sa.Name,
					Type:        *sa.Type,
					Region:      *sa.Location,
					Tags:        toAzureTags(sa.Tags),
					Meta:        structToMap(sa),
					Status:      status,
					CreatedAt:   createdAt,
					Arn:         *sa.ID,
					ServiceName: s.Name(),
				})
			}
		}
	}
	return allResources, nil
}

func (s *storageAccountService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	// Check if this is an alarm/metric alert recommendation
	if _, ok := recommendation.Data["alarm_config"]; ok {
		ctx.GetLogger().Info("azure: applying alarm recommendation for storage_account",
			"rule_name", recommendation.RuleName,
			"resource_id", recommendation.ResourceId)
		return CreateAzureMetricAlertFromRecommendation(ctx, account, recommendation)
	}

	_, err := s.ApplyCommand(ctx, account, providers.ApplyCommandRequest{
		ResourceId: recommendation.ResourceId,
		Command:    recommendation.RuleName,
	})
	return err
}

func (s *storageAccountService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	logger := ctx.GetLogger()
	cred, session, err := getAzureCredsForAccount(ctx, account)
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to create azure credential: %v", err),
		}, err
	}

	// Extract subscription ID, resource group and storage account name from resource ID
	parts := strings.Split(command.ResourceId, "/")
	var subscriptionID, resourceGroup, storageAccountName string
	for i, part := range parts {
		if part == "subscriptions" && i+1 < len(parts) {
			subscriptionID = parts[i+1]
		}
		if part == "resourceGroups" && i+1 < len(parts) {
			resourceGroup = parts[i+1]
		}
		if part == "storageAccounts" && i+1 < len(parts) {
			storageAccountName = parts[i+1]
		}
	}

	if subscriptionID == "" {
		subscriptionID = session.SubscriptionID
	}
	if resourceGroup == "" || storageAccountName == "" {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to extract resource group or storage account name from resource ID: %s", command.ResourceId),
		}, fmt.Errorf("invalid resource ID")
	}

	client, err := armstorage.NewAccountsClient(subscriptionID, cred, getAzureAuditOpts(ctx))
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to create storage accounts client: %v", err),
		}, err
	}

	// Get the current storage account configuration
	accountResp, err := client.GetProperties(ctx.GetContext(), resourceGroup, storageAccountName, nil)
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to get storage account: %v", err),
		}, err
	}

	storageAccount := accountResp.Account
	if storageAccount.Properties == nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: "storage account properties are nil",
		}, fmt.Errorf("storage account properties are nil")
	}

	updateParams := armstorage.AccountUpdateParameters{
		Properties: &armstorage.AccountPropertiesUpdateParameters{},
	}

	// Apply the command based on the rule name
	switch command.Command {
	case "azure_storage_https_only_disabled":
		logger.Info("applying command: enabling HTTPS-only traffic", "storageAccount", storageAccountName)
		updateParams.Properties.EnableHTTPSTrafficOnly = to.Ptr(true)

	case "azure_storage_minimum_tls_version":
		logger.Info("applying command: setting minimum TLS version to 1.2", "storageAccount", storageAccountName)
		updateParams.Properties.MinimumTLSVersion = to.Ptr(armstorage.MinimumTLSVersionTLS12)

	case "disable_blob_public_access":
		logger.Info("applying command: disabling blob public access", "storageAccount", storageAccountName)
		updateParams.Properties.AllowBlobPublicAccess = to.Ptr(false)

	case "disable_shared_key_access":
		logger.Info("applying command: disabling shared key access", "storageAccount", storageAccountName)
		updateParams.Properties.AllowSharedKeyAccess = to.Ptr(false)

	default:
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("unknown or unsupported command: %s", command.Command),
		}, fmt.Errorf("unknown command: %s", command.Command)
	}

	// Update the storage account
	_, err = client.Update(ctx.GetContext(), resourceGroup, storageAccountName, updateParams, nil)
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to update storage account: %v", err),
		}, err
	}

	logger.Info("successfully applied command", "command", command.Command, "storageAccount", storageAccountName)
	return providers.ApplyCommandResponse{
		Success: true,
		Message: fmt.Sprintf("successfully executed command '%s' on storage account '%s'", command.Command, storageAccountName),
	}, nil
}

func (s *storageAccountService) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAzureMonitorMetrics(ctx, account, filter)
}

func (s *storageAccountService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	var allRecommendations []providers.Recommendation
	pricingCache := GetPricingCache()

	for _, resource := range existingResources {
		meta := resource.Meta
		if len(meta) == 0 {
			continue
		}

		// Check for missing tags
		if len(resource.Tags) == 0 {
			allRecommendations = append(allRecommendations, providers.Recommendation{
				CategoryName: providers.RecommendationCategoryConfiguration,
				RuleName:     "azure_storage_missing_tags",
				Severity:     providers.RecommendationSeverityLow,
				Savings:      0,
				Data: map[string]any{
					"resource_id":     resource.Id,
					"resource_name":   resource.Name,
					"resource_type":   resource.Type,
					"resource_region": resource.Region,
					"service_name":    resource.ServiceName,
					"reason":          "Storage account has no tags applied. Tags are essential for cost allocation, resource organization, compliance tracking, automated lifecycle policies, and storage account management at scale.",
					"benefits": []string{
						"Cost tracking and chargeback by department/project",
						"Resource organization and filtering",
						"Compliance and governance tracking",
						"Automated lifecycle management policies",
						"Ownership and responsibility tracking",
						"Environment identification (dev/test/prod)",
					},
					"recommended_tags": []string{"environment", "owner", "cost-center", "application", "data-classification", "retention-period"},
				},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		if props, ok := meta["properties"].(map[string]interface{}); ok {
			// Check HTTPS only
			if supportsHttpsOnly, ok := props["supportsHttpsTrafficOnly"].(bool); !ok || !supportsHttpsOnly {
				allRecommendations = append(allRecommendations, providers.Recommendation{
					CategoryName: providers.RecommendationCategorySecurity,
					RuleName:     "azure_storage_https_only_disabled",
					Severity:     providers.RecommendationSeverityHigh,
					Savings:      0,
					Data: map[string]any{
						"resource_id":                resource.Id,
						"resource_name":              resource.Name,
						"resource_type":              resource.Type,
						"resource_region":            resource.Region,
						"service_name":               resource.ServiceName,
						"current_traffic_policy":     "HTTP and HTTPS allowed",
						"recommended_traffic_policy": "HTTPS only",
						"reason":                     "Storage account allows both HTTP and HTTPS traffic. HTTP transmits data unencrypted, exposing sensitive information to interception and man-in-the-middle attacks. Enforcing HTTPS-only ensures all data in transit is encrypted with TLS.",
						"benefits": []string{
							"Protects data in transit with TLS encryption",
							"Prevents man-in-the-middle attacks",
							"Compliance with security standards (PCI DSS, HIPAA, SOC 2)",
							"Protects access keys and SAS tokens from interception",
							"No performance impact",
							"Industry best practice for cloud storage",
						},
						"security_risks":    []string{"data interception", "man-in-the-middle attacks", "credential theft", "data tampering"},
						"compliance_impact": "Required for PCI DSS, HIPAA, SOC 2, and ISO 27001 compliance",
						"cost_impact":       "No additional cost",
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}

			// Check minimum TLS version
			currentTLSVersion := ""
			if minimumTLSVersion, ok := props["minimumTlsVersion"].(string); ok {
				currentTLSVersion = minimumTLSVersion
			}
			if currentTLSVersion != "TLS1_2" && currentTLSVersion != "TLS1_3" {
				if currentTLSVersion == "" {
					currentTLSVersion = "TLS1_0 (default)"
				}
				allRecommendations = append(allRecommendations, providers.Recommendation{
					CategoryName: providers.RecommendationCategorySecurity,
					RuleName:     "azure_storage_minimum_tls_version",
					Severity:     providers.RecommendationSeverityMedium,
					Savings:      0,
					Data: map[string]any{
						"resource_id":             resource.Id,
						"resource_name":           resource.Name,
						"resource_type":           resource.Type,
						"resource_region":         resource.Region,
						"service_name":            resource.ServiceName,
						"current_tls_version":     currentTLSVersion,
						"recommended_tls_version": "TLS1_2 or TLS1_3",
						"reason":                  "Storage account is using an outdated TLS version. TLS 1.0 and 1.1 have known security vulnerabilities and are deprecated by PCI DSS and major browsers. TLS 1.2 or higher provides strong encryption and is the minimum required for modern security standards.",
						"benefits": []string{
							"Protection against known TLS 1.0/1.1 vulnerabilities (BEAST, POODLE)",
							"Stronger cipher suites (AES-GCM, ChaCha20)",
							"Compliance with PCI DSS 3.2+ requirements",
							"Support for modern encryption algorithms",
							"Better protection against protocol downgrade attacks",
							"Industry standard for secure communications",
						},
						"security_risks":    []string{"BEAST attack", "POODLE attack", "protocol downgrade attacks", "weak cipher exploitation"},
						"compliance_impact": "TLS 1.2+ required for PCI DSS 3.2, HIPAA, and SOC 2 compliance",
						"deprecation_note":  "TLS 1.0 and 1.1 deprecated by Microsoft, browsers, and PCI Council",
						"cost_impact":       "No additional cost",
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}

			// Check for blob public access
			if allowBlobPublicAccess, ok := props["allowBlobPublicAccess"].(bool); ok && allowBlobPublicAccess {
				allRecommendations = append(allRecommendations, providers.Recommendation{
					CategoryName: providers.RecommendationCategorySecurity,
					RuleName:     "azure_storage_blob_public_access_enabled",
					Severity:     providers.RecommendationSeverityHigh,
					Savings:      0,
					Data: map[string]any{
						"resource_id":        resource.Id,
						"resource_name":      resource.Name,
						"resource_type":      resource.Type,
						"resource_region":    resource.Region,
						"service_name":       resource.ServiceName,
						"current_access":     "Public access enabled",
						"recommended_access": "Public access disabled",
						"reason":             "Storage account allows anonymous public access to blob containers. Public access exposes data to unauthorized access and increases risk of data breaches. Unless explicitly required for public content (CDN, public downloads), disable public access and use SAS tokens or Azure AD authentication.",
						"benefits": []string{
							"Prevents unauthorized data access",
							"Protects against accidental data exposure",
							"Enforces authentication for all access",
							"Compliance with data protection regulations",
							"Reduces attack surface for data breaches",
							"Azure Security Center recommendation",
						},
						"security_risks":     []string{"unauthorized data access", "data breach", "accidental exposure", "data scraping"},
						"compliance_impact":  "Required for GDPR, HIPAA, and enterprise security policies",
						"alternative_access": "Use SAS tokens, Azure AD authentication, or Private Endpoints for controlled access",
						"cost_impact":        "No additional cost",
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}

			// Check for shared key access
			if allowSharedKeyAccess, ok := props["allowSharedKeyAccess"].(bool); ok && allowSharedKeyAccess {
				allRecommendations = append(allRecommendations, providers.Recommendation{
					CategoryName: providers.RecommendationCategorySecurity,
					RuleName:     "azure_storage_shared_key_access_enabled",
					Severity:     providers.RecommendationSeverityMedium,
					Savings:      0,
					Data: map[string]any{
						"resource_id":        resource.Id,
						"resource_name":      resource.Name,
						"resource_type":      resource.Type,
						"resource_region":    resource.Region,
						"service_name":       resource.ServiceName,
						"current_access":     "Shared Key access enabled",
						"recommended_access": "Azure AD authentication only",
						"reason":             "Storage account allows Shared Key (storage account key) authentication. Shared keys are long-lived credentials that, if compromised, provide full account access. Azure AD authentication provides better security through managed identities, conditional access policies, and granular RBAC permissions.",
						"benefits": []string{
							"Eliminates long-lived credentials",
							"Granular RBAC permissions",
							"Conditional access policies (MFA, location-based)",
							"Automatic credential rotation via managed identities",
							"Comprehensive audit logs in Azure AD",
							"Principle of least privilege enforcement",
							"Integration with Azure Policy",
						},
						"security_risks":    []string{"credential theft", "excessive permissions", "no MFA enforcement", "difficult credential rotation"},
						"compliance_impact": "Azure AD auth recommended for Zero Trust security and enterprise compliance",
						"migration_note":    "Ensure applications use managed identities or service principals before disabling Shared Key access",
						"cost_impact":       "No additional cost",
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}

			// Check for infrastructure encryption (double encryption)
			if requireInfrastructureEncryption, ok := props["encryption"].(map[string]interface{}); ok {
				if infraEncrypt, ok := requireInfrastructureEncryption["requireInfrastructureEncryption"].(bool); !ok || !infraEncrypt {
					allRecommendations = append(allRecommendations, providers.Recommendation{
						CategoryName: providers.RecommendationCategorySecurity,
						RuleName:     "azure_storage_infrastructure_encryption_disabled",
						Severity:     providers.RecommendationSeverityLow,
						Savings:      0,
						Data: map[string]any{
							"resource_id":            resource.Id,
							"resource_name":          resource.Name,
							"resource_type":          resource.Type,
							"resource_region":        resource.Region,
							"service_name":           resource.ServiceName,
							"current_encryption":     "Single layer (service-side encryption)",
							"recommended_encryption": "Double encryption (infrastructure + service)",
							"reason":                 "Storage account does not have infrastructure encryption (double encryption) enabled. Infrastructure encryption adds a second layer of AES-256 encryption at the infrastructure level, providing defense-in-depth for highly sensitive data. Recommended for regulated industries (healthcare, finance, government).",
							"benefits": []string{
								"Two independent layers of AES-256 encryption",
								"Defense-in-depth security strategy",
								"Protection against cryptographic vulnerabilities",
								"Required for high-security compliance (FedRAMP, ITAR)",
								"No performance impact",
								"Transparent to applications",
							},
							"encryption_layers": []string{"Service-side encryption (256-bit AES)", "Infrastructure encryption (256-bit AES)"},
							"compliance_impact": "Required for FedRAMP High, ITAR, and high-security government workloads",
							"use_cases":         []string{"healthcare PHI data", "financial records", "government classified data", "intellectual property"},
							"cost_impact":       "No additional cost",
							"note":              "Can only be enabled at storage account creation time",
						},
						Action:              providers.RecommendationActionModify,
						ResourceServiceName: resource.ServiceName,
						ResourceId:          resource.Id,
						ResourceType:        resource.Type,
						ResourceRegion:      resource.Region,
					})
				}
			}

			// Check for network rules (firewall)
			if networkAcls, ok := props["networkAcls"].(map[string]interface{}); ok {
				if defaultAction, ok := networkAcls["defaultAction"].(string); ok && defaultAction == "Allow" {
					allRecommendations = append(allRecommendations, providers.Recommendation{
						CategoryName: providers.RecommendationCategorySecurity,
						RuleName:     "azure_storage_firewall_not_configured",
						Severity:     providers.RecommendationSeverityMedium,
						Savings:      0,
						Data: map[string]any{
							"resource_id":          resource.Id,
							"resource_name":        resource.Name,
							"resource_type":        resource.Type,
							"resource_region":      resource.Region,
							"service_name":         resource.ServiceName,
							"current_firewall":     "Allow all networks",
							"recommended_firewall": "Deny by default with allow rules",
							"reason":               "Storage account firewall allows access from all networks. This exposes the storage account to the public internet, increasing the attack surface. Configure firewall rules to allow access only from trusted networks, VNets, or specific IP ranges.",
							"benefits": []string{
								"Restricts access to trusted networks only",
								"Reduces attack surface significantly",
								"Prevents unauthorized access attempts",
								"Supports VNet integration and Private Endpoints",
								"Compliance with network segmentation requirements",
								"Azure Security Center recommendation",
							},
							"security_risks":    []string{"unauthorized access from internet", "data exfiltration", "brute force attacks", "DDoS exposure"},
							"compliance_impact": "Required for PCI DSS, HIPAA, and network isolation policies",
							"firewall_options":  []string{"VNet service endpoints", "Private Endpoints", "IP-based firewall rules", "Trusted Azure services"},
							"cost_impact":       "Private Endpoints have minimal cost (~$7/month per endpoint)",
						},
						Action:              providers.RecommendationActionModify,
						ResourceServiceName: resource.ServiceName,
						ResourceId:          resource.Id,
						ResourceType:        resource.Type,
						ResourceRegion:      resource.Region,
					})
				}
			}

			// Check for storage tier optimization opportunities
			if sku, ok := meta["sku"].(map[string]interface{}); ok {
				if skuName, ok := sku["name"].(string); ok {
					// Check for redundancy optimization
					redundancyOptimization := getStorageRedundancyOptimization(ctx, pricingCache, skuName, resource.Region)
					if redundancyOptimization != nil {
						allRecommendations = append(allRecommendations, providers.Recommendation{
							CategoryName: providers.RecommendationCategoryRightSizing,
							RuleName:     "azure_storage_redundancy_optimization",
							Severity:     providers.RecommendationSeverityMedium,
							Savings:      redundancyOptimization["estimated_savings_monthly"].(float64),
							Data: map[string]any{
								"resource_id":            resource.Id,
								"resource_name":          resource.Name,
								"resource_type":          resource.Type,
								"resource_region":        resource.Region,
								"service_name":           resource.ServiceName,
								"current_redundancy":     redundancyOptimization["current_redundancy"],
								"recommended_redundancy": redundancyOptimization["recommended_redundancy"],
								"reason":                 redundancyOptimization["reason"],
								"benefits":               redundancyOptimization["benefits"],
								"estimated_savings_type": "monthly",
								"savings_source":         "Azure Storage pricing - redundancy tier differences",
								"data_durability_note":   redundancyOptimization["data_durability_note"],
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

			// Check for blob access tier optimization (Hot → Cool/Archive)
			// This would typically require analyzing blob access patterns
			// For now, we'll check if the account is using Hot tier and suggest Cool tier evaluation
			if props["accessTier"] != nil {
				if accessTier, ok := props["accessTier"].(string); ok && accessTier == "Hot" {
					// Calculate savings using dynamic pricing
					hotPrice, err1 := pricingCache.GetStoragePrice(ctx, "hot", "LRS", resource.Region)
					coolPrice, err2 := pricingCache.GetStoragePrice(ctx, "cool", "LRS", resource.Region)

					estimatedSavings := 50.0 // Fallback value
					if err1 == nil && err2 == nil && hotPrice > coolPrice {
						// Assume 100GB for estimation
						estimatedSavings = (hotPrice - coolPrice) * 100.0
					}

					allRecommendations = append(allRecommendations, providers.Recommendation{
						CategoryName: providers.RecommendationCategoryRightSizing,
						RuleName:     "azure_storage_access_tier_optimization",
						Severity:     providers.RecommendationSeverityLow,
						Savings:      estimatedSavings,
						Data: map[string]any{
							"resource_id":            resource.Id,
							"resource_name":          resource.Name,
							"resource_type":          resource.Type,
							"resource_region":        resource.Region,
							"service_name":           resource.ServiceName,
							"current_access_tier":    "Hot",
							"recommended_evaluation": "Cool or Archive",
							"reason":                 "Storage account is using Hot access tier. If data is infrequently accessed (less than once per 30 days), consider moving to Cool tier for 50% storage cost savings, or Archive tier for 90% savings on rarely accessed data.",
							"benefits": []string{
								"Up to 50% storage cost savings (Cool tier)",
								"Up to 90% storage cost savings (Archive tier)",
								"Same data durability guarantees",
								"Lifecycle management policies for automatic tiering",
								"No data loss or quality degradation",
							},
							"estimated_savings_type": "monthly",
							"savings_source":         "Azure Storage access tier pricing differences",
							"savings_note":           "Actual savings depend on data access patterns. Cool tier suitable for data accessed <once/month, Archive for rarely accessed data.",
							"consideration":          "Cool tier has higher access costs; ensure access patterns justify the tier change",
						},
						Action:              providers.RecommendationActionModify,
						ResourceServiceName: resource.ServiceName,
						ResourceId:          resource.Id,
						ResourceType:        resource.Type,
						ResourceRegion:      resource.Region,
					})
				}
			}

			// Check for RightSizing opportunities based on storage account kind and replication
			if sku, ok := meta["sku"].(map[string]interface{}); ok {
				if skuName, ok := sku["name"].(string); ok {
					// Check storage account kind
					var accountKind string
					if kind, ok := meta["kind"].(string); ok {
						accountKind = kind
					}

					rightSizingRecommendation := getStorageAccountRightSizing(ctx, pricingCache, skuName, accountKind, props, resource.Region)
					if rightSizingRecommendation != nil {
						allRecommendations = append(allRecommendations, providers.Recommendation{
							CategoryName: providers.RecommendationCategoryRightSizing,
							RuleName:     rightSizingRecommendation["rule_name"].(string),
							Severity:     providers.RecommendationSeverityMedium,
							Savings:      rightSizingRecommendation["estimated_savings_monthly"].(float64),
							Data: map[string]any{
								"resource_id":               resource.Id,
								"resource_name":             resource.Name,
								"resource_type":             resource.Type,
								"resource_region":           resource.Region,
								"service_name":              resource.ServiceName,
								"current_configuration":     rightSizingRecommendation["current_configuration"],
								"recommended_configuration": rightSizingRecommendation["recommended_configuration"],
								"reason":                    rightSizingRecommendation["reason"],
								"benefits":                  rightSizingRecommendation["benefits"],
								"estimated_savings_type":    "monthly",
								"savings_source":            rightSizingRecommendation["savings_source"],
								"workload_suitability":      rightSizingRecommendation["workload_suitability"],
								"performance_impact":        rightSizingRecommendation["performance_impact"],
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

			// Check for unused or idle storage accounts
			if provisioningState, ok := props["provisioningState"].(string); ok {
				if provisioningState == "Succeeded" {
					// Check if storage account has been created but potentially not used
					// This is indicated by primary endpoints but low usage metrics
					idleCheckRecommendation := checkStorageAccountIdle(props, resource.CreatedAt)
					if idleCheckRecommendation != nil {
						allRecommendations = append(allRecommendations, providers.Recommendation{
							CategoryName: providers.RecommendationCategoryRightSizing,
							RuleName:     "azure_storage_account_potentially_idle",
							Severity:     providers.RecommendationSeverityMedium,
							Savings:      idleCheckRecommendation["estimated_savings_monthly"].(float64),
							Data: map[string]any{
								"resource_id":            resource.Id,
								"resource_name":          resource.Name,
								"resource_type":          resource.Type,
								"resource_region":        resource.Region,
								"service_name":           resource.ServiceName,
								"account_age_days":       idleCheckRecommendation["account_age_days"],
								"reason":                 idleCheckRecommendation["reason"],
								"benefits":               idleCheckRecommendation["benefits"],
								"estimated_savings_type": "monthly",
								"savings_source":         idleCheckRecommendation["savings_source"],
								"action_recommendation":  idleCheckRecommendation["action_recommendation"],
								"verification_steps":     idleCheckRecommendation["verification_steps"],
							},
							Action:              providers.RecommendationActionDelete,
							ResourceServiceName: resource.ServiceName,
							ResourceId:          resource.Id,
							ResourceType:        resource.Type,
							ResourceRegion:      resource.Region,
						})
					}
				}
			}
		}
	}
	return allRecommendations, nil
}

// getStorageRedundancyOptimization returns optimization suggestions for storage redundancy
func getStorageRedundancyOptimization(ctx providers.CloudProviderContext, pricingCache *PricingCache, skuName string, region string) map[string]interface{} {
	// Default storage size for estimation (100GB)
	const estimatedStorageGB = 100.0

	// GRS → LRS optimization for non-critical data
	// GRS (Geo-Redundant) costs ~2x more than LRS (Locally Redundant)
	if strings.Contains(skuName, "GRS") || strings.Contains(skuName, "RAGRS") {
		recommendedSKU := strings.Replace(skuName, "GRS", "LRS", 1)
		recommendedSKU = strings.Replace(recommendedSKU, "RAGRS", "LRS", 1)

		grsPrice, err1 := pricingCache.GetStoragePrice(ctx, "hot", "GRS", region)
		lrsPrice, err2 := pricingCache.GetStoragePrice(ctx, "hot", "LRS", region)

		estimatedSavings := 100.0 // Fallback value
		if err1 == nil && err2 == nil && grsPrice > lrsPrice {
			estimatedSavings = (grsPrice - lrsPrice) * estimatedStorageGB
		} else if err1 != nil || err2 != nil {
			ctx.GetLogger().Warn("Could not fetch dynamic pricing for storage redundancy optimization; using fallback.", "region", region, "grsErr", err1, "lrsErr", err2)
		}

		return map[string]interface{}{
			"current_sku":               skuName,
			"recommended_sku":           recommendedSKU,
			"current_redundancy":        "GRS/RA-GRS",
			"recommended_redundancy":    "LRS",
			"estimated_savings_monthly": estimatedSavings,
			"reason":                    "Storage account is using Geo-Redundant Storage (GRS). If your data doesn't require geographic redundancy (e.g., dev/test environments, non-critical data, data with other backup mechanisms), switching to Locally Redundant Storage (LRS) can save ~50% on storage costs.",
			"benefits": []string{
				"~50% cost reduction on storage",
				"Still maintains 11 nines durability within region",
				"3 synchronous copies within datacenter",
				"Suitable for non-critical data, dev/test, or data with external backups",
			},
			"data_durability_note": "LRS provides 99.999999999% (11 nines) durability within a single region. Consider GRS only if geographic disaster recovery is required.",
		}
	}

	// ZRS → LRS for single-region workloads
	if strings.Contains(skuName, "ZRS") {
		recommendedSKU := strings.Replace(skuName, "ZRS", "LRS", 1)

		// Calculate actual savings using dynamic pricing
		zrsPrice, err1 := pricingCache.GetStoragePrice(ctx, "hot", "ZRS", region)
		lrsPrice, err2 := pricingCache.GetStoragePrice(ctx, "hot", "LRS", region)

		estimatedSavings := 40.0 // Fallback value
		if err1 == nil && err2 == nil && zrsPrice > lrsPrice {
			estimatedSavings = (zrsPrice - lrsPrice) * estimatedStorageGB
		}

		return map[string]interface{}{
			"current_sku":               skuName,
			"recommended_sku":           recommendedSKU,
			"current_redundancy":        "ZRS",
			"recommended_redundancy":    "LRS",
			"estimated_savings_monthly": estimatedSavings,
			"reason":                    "Storage account uses Zone-Redundant Storage (ZRS). If availability zones redundancy isn't required for your workload, LRS can provide cost savings while maintaining high durability.",
			"benefits": []string{
				"~25-30% cost reduction",
				"Still maintains 11 nines durability",
				"Suitable for single-zone workloads",
				"Same performance characteristics",
			},
			"data_durability_note": "Both LRS and ZRS provide 11 nines durability. ZRS protects against datacenter failures; LRS is sufficient if zone redundancy isn't critical.",
		}
	}

	return nil
}

// getStorageAccountRightSizing analyzes storage account configuration and suggests rightsizing opportunities
func getStorageAccountRightSizing(ctx providers.CloudProviderContext, pricingCache *PricingCache, skuName string, accountKind string, props map[string]interface{}, region string) map[string]interface{} {
	skuUpper := strings.ToUpper(skuName)
	kindLower := strings.ToLower(accountKind)

	// Check for over-provisioned Storage V2 that could be BlobStorage
	if kindLower == "storagev2" {
		// Check if only blob services are enabled (no file/queue/table usage indicators)
		hasOnlyBlobServices := true

		// If file share, queue, or table services are enabled, it needs StorageV2
		if enabledServices, ok := props["enabledServices"].([]interface{}); ok {
			for _, service := range enabledServices {
				if svc, ok := service.(string); ok {
					svcLower := strings.ToLower(svc)
					if svcLower == "file" || svcLower == "queue" || svcLower == "table" {
						hasOnlyBlobServices = false
						break
					}
				}
			}
		}

		if hasOnlyBlobServices {
			// StorageV2 with only blob usage could be downgraded to BlobStorage
			// Calculate savings (approximately 10-15% on operations)
			storagePrice, err := pricingCache.GetStoragePrice(ctx, "hot", "LRS", region)
			if err != nil {
				ctx.GetLogger().Error("Error fetching storage price for rightsizing", "error", err)
				return nil
			}
			estimatedSavings := 15.0 // Fallback
			if storagePrice > 0 {
				// Estimate 10% savings on 100GB plus operation cost savings
				estimatedSavings = storagePrice * 100.0 * 0.10
			}

			return map[string]interface{}{
				"rule_name":                 "azure_storage_account_kind_optimization",
				"current_configuration":     "StorageV2 (General Purpose v2)",
				"recommended_configuration": "BlobStorage",
				"estimated_savings_monthly": estimatedSavings,
				"reason":                    "Storage account is configured as StorageV2 (General Purpose v2) but appears to only use Blob storage. Downgrading to BlobStorage account kind reduces costs by eliminating unnecessary features (file shares, queues, tables) while maintaining full blob functionality including access tiers.",
				"benefits": []string{
					"~10-15% cost reduction on storage operations",
					"Maintains all blob storage features",
					"Access tier support (Hot/Cool/Archive)",
					"Same redundancy options (LRS/ZRS/GRS)",
					"Simplified service configuration",
					"Lower transaction costs",
				},
				"savings_source":       "Reduced per-operation costs and eliminated unused service features",
				"workload_suitability": "Ideal for blob-only workloads (backups, media files, data lakes, static websites)",
				"performance_impact":   "No performance impact - same blob storage performance",
			}
		}
	}

	// Check for premium accounts that could use standard
	if strings.Contains(skuUpper, "PREMIUM") {
		// Premium accounts cost 3-5x more - calculate actual savings
		premiumPrice, err := pricingCache.GetStoragePrice(ctx, "premium", "LRS", region)
		if err != nil {
			return nil
		}
		standardPrice, err1 := pricingCache.GetStoragePrice(ctx, "hot", "LRS", region)
		if err1 != nil {
			return nil
		}

		estimatedSavings := 200.0 // Fallback
		if premiumPrice > 0 && standardPrice > 0 {
			// Assume 100GB for estimation
			estimatedSavings = (premiumPrice - standardPrice) * 100.0
		}

		return map[string]interface{}{
			"rule_name":                 "azure_storage_account_premium_to_standard",
			"current_configuration":     "Premium tier (SSD-backed)",
			"recommended_configuration": "Standard tier",
			"estimated_savings_monthly": estimatedSavings,
			"reason":                    "Storage account uses Premium tier (SSD-backed storage). Unless you require <10ms latency, >20,000 IOPS, or premium file shares, Standard tier provides 99.9% SLA at significantly lower cost. Premium tier costs 3-5x more than Standard.",
			"downgrade_justification": map[string]interface{}{
				"when_to_downgrade": []string{
					"Current P95 IOPS usage is consistently below 500 (<2.5% of Premium capacity)",
					"Application latency requirements are >20ms (not latency-critical)",
					"No performance SLA requirements below 50ms",
					"Workload is primarily sequential I/O (backups, media streaming, logs)",
					"No throttling observed even during peak hours on test workloads",
					"Cost optimization is a higher priority than sub-10ms latency",
				},
				"do_not_downgrade_if": []string{
					"Current IOPS consistently exceeds 500 (will be throttled on Standard)",
					"Application requires sub-10ms latency SLA",
					"Running production databases (SQL Server, PostgreSQL, MongoDB)",
					"VDI or virtual desktop workloads",
					"Real-time transaction processing or analytics",
					"Experiencing performance issues or throttling",
				},
			},
			"performance_evidence_required": map[string]interface{}{
				"metrics_to_check": []string{
					"Peak IOPS from Azure Monitor (past 30 days) - must be <400 consistently",
					"P95 and P99 latency measurements - must be acceptable at 20-40ms range",
					"Current throttling events (should be zero)",
					"Transaction patterns (sequential vs random I/O)",
					"Application performance SLA requirements",
					"Business criticality of the workload",
				},
				"how_to_collect_evidence": "Use Azure Monitor > Storage Account > Metrics. Review 'Transactions' (IOPS), 'Success E2E Latency', and 'Server Latency' over 30 days. Check application logs for performance issues. Verify no SuccessWithThrottling responses.",
			},
			"performance_comparison": map[string]interface{}{
				"premium_tier": map[string]string{
					"latency":       "<10ms (consistent, sub-millisecond for cached)",
					"iops_per_disk": "Up to 20,000 IOPS (P50 disk)",
					"throughput":    "Up to 900 MB/s per disk (P50)",
					"sla":           "99.9% availability",
					"storage_type":  "SSD-backed (NVMe)",
					"best_for":      "Databases (SQL, NoSQL), VDI, transaction processing, real-time analytics",
					"cost_per_gb":   fmt.Sprintf("~$%.4f/month", premiumPrice),
				},
				"standard_tier": map[string]string{
					"latency":       "~20-40ms (average)",
					"iops_per_disk": "Up to 500 IOPS",
					"throughput":    "Up to 60 MB/s per disk",
					"sla":           "99.9% availability",
					"storage_type":  "HDD-backed",
					"best_for":      "Infrequent access, backups, dev/test, media storage",
					"cost_per_gb":   fmt.Sprintf("~$%.4f/month", standardPrice),
				},
				"impact_of_downgrade": "IOPS reduced to 500 (2.5% of Premium), latency increases 4-8x (to 20-40ms), throughput reduced by 93%",
			},
			"recommendation_strength": "Conditional - requires evidence that Premium performance is not needed",
			"action_before_downgrade": []string{
				"1. Collect 30 days of Azure Monitor metrics (IOPS, latency, throughput)",
				"2. Verify P95 IOPS is consistently <400 (with 20% safety margin from 500 limit)",
				"3. Confirm application can tolerate 20-40ms latency",
				"4. Check no performance throttling events in logs",
				"5. Test with pilot: migrate non-critical workload first and monitor for 1 week",
				"6. Document rollback plan in case of performance degradation",
			},
			"benefits": []string{
				"60-75% cost reduction for storage",
				"Maintains 99.9% availability SLA",
				"Sufficient for most non-latency-critical workloads",
				"Same redundancy options (LRS/ZRS/GRS)",
				"Hot/Cool/Archive tier support for further optimization",
				"Supports workloads with <500 IOPS requirements",
			},
			"savings_source":       fmt.Sprintf("Premium tier $%.4f/GB vs Standard tier $%.4f/GB (%.0f%% reduction)", premiumPrice, standardPrice, ((premiumPrice-standardPrice)/premiumPrice)*100),
			"workload_suitability": "Standard suitable for: backups, media storage, data lakes, web content, general file storage, dev/test environments, log storage",
			"performance_impact":   fmt.Sprintf("Lower IOPS (500 vs 20,000+), higher latency (~20-40ms vs <10ms), reduced throughput (60 MB/s vs 900 MB/s). Cost savings: $%.2f/month for 100GB.", estimatedSavings),
		}
	}

	// Check for BlockBlobStorage premium accounts (very expensive)
	if kindLower == "blockblobstorage" {
		// Calculate savings from switching to StorageV2
		premiumPrice, _ := pricingCache.GetStoragePrice(ctx, "premium", "LRS", region)
		standardPrice, _ := pricingCache.GetStoragePrice(ctx, "hot", "LRS", region)

		estimatedSavings := 150.0 // Fallback
		if premiumPrice > 0 && standardPrice > 0 {
			// Assume 100GB for estimation
			estimatedSavings = (premiumPrice - standardPrice) * 100.0
		}

		return map[string]interface{}{
			"rule_name":                 "azure_storage_block_blob_to_standard",
			"current_configuration":     "BlockBlobStorage (Premium)",
			"recommended_configuration": "StorageV2 with Premium blob tier",
			"estimated_savings_monthly": estimatedSavings,
			"reason":                    "Storage account uses BlockBlobStorage (premium block blobs only). This is the most expensive storage option designed for extremely high-throughput workloads. For most scenarios, StorageV2 with selective premium blob tier provides better cost efficiency.",
			"benefits": []string{
				"50-70% cost reduction",
				"Flexibility to use Hot/Cool/Archive tiers",
				"Can selectively use premium tier for specific containers",
				"Access to file/queue/table services if needed",
				"Lower minimum capacity requirements",
			},
			"savings_source":       "BlockBlobStorage premium (~$0.15/GB) vs StorageV2 tiered pricing",
			"workload_suitability": "Premium block blobs only needed for: high-frequency small object workloads, real-time analytics, AI/ML training data",
			"performance_impact":   "Reduced throughput (190MB/s vs 1,250MB/s). Suitable for 95% of workloads.",
		}
	}

	// Check for FileStorage premium accounts
	if kindLower == "filestorage" {
		// Calculate savings from switching to standard file shares
		// Premium file shares are about 20x more expensive
		premiumPrice, err := pricingCache.GetStoragePrice(ctx, "premium", "LRS", region)
		if err != nil {
			ctx.GetLogger().Error("Error fetching premium storage price for file share rightsizing", "error", err)
			return nil
		}
		standardPrice, err1 := pricingCache.GetStoragePrice(ctx, "hot", "LRS", region)
		if err1 != nil {
			ctx.GetLogger().Error("Error fetching standard storage price for file share rightsizing", "error", err1)
			return nil
		}

		estimatedSavings := 120.0 // Fallback
		if premiumPrice > 0 && standardPrice > 0 {
			// Premium file shares are significantly more expensive
			// Assume 100GB for estimation with premium file multiplier
			estimatedSavings = (premiumPrice*3 - standardPrice) * 100.0 // File shares have additional premium cost
		}

		return map[string]interface{}{
			"rule_name":                 "azure_storage_file_premium_to_standard",
			"current_configuration":     "FileStorage (Premium file shares)",
			"recommended_configuration": "StorageV2 with standard file shares",
			"estimated_savings_monthly": estimatedSavings,
			"reason":                    "Storage account uses FileStorage (premium file shares only). Premium file shares cost 20x more than standard and are designed for high-IOPS workloads (databases, HPC, IOPS-intensive apps). For general file sharing, standard file shares provide adequate performance at much lower cost.",
			"benefits": []string{
				"90-95% cost reduction for file storage",
				"Maintains Azure Files capabilities",
				"SMB and NFS protocol support",
				"Up to 100TB capacity per share",
				"Suitable for general file sharing workloads",
			},
			"savings_source":       "Premium file shares (~$0.20/GB) vs Standard file shares (~$0.06/GB)",
			"workload_suitability": "Standard file shares suitable for: team file sharing, home directories, application file storage, CMS content",
			"performance_impact":   "Lower IOPS (1,000 baseline vs 100,000 IOPS). Premium only needed for: SQL databases on file shares, SAP HANA, high-frequency trading.",
		}
	}

	return nil
}

// checkStorageAccountIdle checks if a storage account appears to be idle or unused
func checkStorageAccountIdle(props map[string]interface{}, createdAt time.Time) map[string]interface{} {
	// Calculate account age
	accountAgeDays := int(time.Since(createdAt).Hours() / 24)

	// Only flag accounts older than 30 days
	if accountAgeDays < 30 {
		return nil
	}

	// Check for indicators of idle state
	// Note: In a production implementation, you MUST query Azure Monitor metrics
	// for actual usage data (Transactions, Ingress, Egress over last 30 days)
	// This is a conservative heuristic-based approach to minimize false positives

	// We use multiple conservative indicators to reduce false positives
	idleIndicators := 0
	requiredIndicators := 2 // Require at least 2 indicators to flag as idle

	// Indicator 1: Very old account (>180 days) with default settings
	// New accounts are often created and not immediately used
	if accountAgeDays > 180 {
		// Check if still has default public network access (Allow all)
		if networkAcls, ok := props["networkAcls"].(map[string]interface{}); ok {
			if defaultAction, ok := networkAcls["defaultAction"].(string); ok {
				if defaultAction == "Allow" {
					idleIndicators++
					// Note: Active accounts are typically secured within first 6 months
				}
			}
		}
	}

	// Indicator 2: No primary endpoints configured (account never fully provisioned)
	// This is a strong signal that the account was created but never used
	if primaryEndpoints, ok := props["primaryEndpoints"].(map[string]interface{}); ok {
		// Check if endpoints exist but are not configured (empty or null)
		if len(primaryEndpoints) == 0 {
			idleIndicators++
		}
	}

	// Indicator 3: Creation time equals last modified time (never updated after creation)
	// This suggests no configuration changes after initial provisioning
	if creationTime, ok := props["creationTime"].(string); ok {
		if lastModified, ok := props["lastModifiedTime"].(string); ok {
			if creationTime == lastModified && accountAgeDays > 90 {
				idleIndicators++
				// Note: Active accounts typically have configuration changes
			}
		}
	}

	// Only flag as idle if we have enough conservative indicators
	// This approach minimizes false positives while catching truly abandoned accounts
	if idleIndicators < requiredIndicators {
		return nil
	}

	// Estimate savings based on typical storage account costs.
	// These are rough estimates and should be documented.
	// Minimum cost for a standard LRS account with minimal data.
	const baseMonthlyCost = 25.0
	// Additional cost for GRS/RA-GRS redundancy over LRS.
	const grsAdditionalCost = 50.0
	// Additional cost for ZRS redundancy over LRS.
	const zrsAdditionalCost = 20.0

	estimatedMonthlyCost := baseMonthlyCost

	// Add redundancy costs
	if props["sku"] != nil {
		if skuMap, ok := props["sku"].(map[string]interface{}); ok {
			if skuName, ok := skuMap["name"].(string); ok {
				if strings.Contains(skuName, "GRS") || strings.Contains(skuName, "RAGRS") {
					estimatedMonthlyCost += grsAdditionalCost
				} else if strings.Contains(skuName, "ZRS") {
					estimatedMonthlyCost += zrsAdditionalCost
				}
			}
		}
	}

	return map[string]interface{}{
		"account_age_days":          accountAgeDays,
		"estimated_savings_monthly": estimatedMonthlyCost,
		"idle_indicators_found":     idleIndicators,
		"reason":                    fmt.Sprintf("Storage account has been provisioned for %d days and shows %d indicators of potentially low/no usage (e.g., default network config, no configuration updates, empty endpoints). IMPORTANT: This is a conservative heuristic-based detection. You MUST verify actual usage via Azure Monitor metrics before taking any action. Idle storage accounts incur ongoing costs for the account itself, redundancy, and any stored data.", accountAgeDays, idleIndicators),
		"benefits": []string{
			"Eliminate unnecessary storage account costs",
			"Reduce management overhead",
			"Simplify resource inventory",
			"Improve security posture (fewer attack surfaces)",
			"Better cost visibility and allocation",
		},
		"savings_source":        "Storage account base cost + redundancy + any stored data",
		"action_recommendation": "CRITICAL: Verify actual usage via Azure Monitor metrics BEFORE deletion. This recommendation is heuristic-based and may have false positives.",
		"verification_steps": []string{
			"REQUIRED: Check Azure Monitor metrics for Transactions, Ingress, Egress (last 30-90 days)",
			"Query blob containers and file shares for actual content and size",
			"Review Azure Storage Analytics logs for access patterns",
			"Check application dependencies and connection strings",
			"Verify with resource owners before deletion",
			"Use soft-delete and ensure backups before permanent removal",
		},
		"false_positive_risk": "MODERATE - This heuristic-based check cannot access actual usage metrics. Always verify with Azure Monitor before deletion.",
	}
}

func (s *storageAccountService) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, resource providers.Resource) (providers.ServiceMapApplication, error) {
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resource.Name,
			Kind:      "storageaccount",
			Namespace: resource.Region,
		},
		Upstreams:   []providers.UpstreamLink{},
		Downstreams: []providers.DownstreamLink{},
		Status:      "Unknown",
	}
	return app, nil
}

func (s *storageAccountService) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region string, resourceId string) (string, error) {
	// Azure Storage Account logs are typically found in Log Analytics workspace
	// Format: /subscriptions/{subscription}/resourceGroups/{rg}/providers/Microsoft.Storage/storageAccounts/{name}
	return resourceId, nil
}
