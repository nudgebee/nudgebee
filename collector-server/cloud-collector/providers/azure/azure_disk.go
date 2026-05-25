package azure

import (
	"errors"
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute"
)

type diskService struct {
}

func (s *diskService) Name() string {
	return "Microsoft.Compute/disks"
}

// Scope returns the service scope - this is a regional service
func (s *diskService) Scope() ServiceScope {
	return ServiceScopeRegional
}

func (s *diskService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
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
		disksClient, err := armcompute.NewDisksClient(subID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to create disks client: %w", err)
		}

		pager := disksClient.NewListPager(nil)

		for pager.More() {
			page, err := pager.NextPage(ctx.GetContext())
			if err != nil {
				return nil, fmt.Errorf("failed to get next page: %w", err)
			}
			for _, disk := range page.Value {
				status := providers.ResourceStatusUnknown
				if disk.Properties != nil && disk.Properties.DiskState != nil {
					switch *disk.Properties.DiskState {
					case armcompute.DiskStateActiveSAS, armcompute.DiskStateActiveUpload, armcompute.DiskStateAttached, armcompute.DiskStateReadyToUpload:
						status = providers.ResourceStatusActive
					case armcompute.DiskStateUnattached:
						status = providers.ResourceStatusInactive
					case armcompute.DiskStateReserved:
						status = providers.ResourceStatusInactive
					default:
						status = providers.ResourceStatusUnknown
					}
				}

				createdAt := time.Time{}

				if disk.Properties != nil && disk.Properties.TimeCreated != nil {
					createdAt = *disk.Properties.TimeCreated
				}

				allResources = append(allResources, providers.Resource{
					Id:          *disk.ID,
					Name:        *disk.Name,
					Type:        *disk.Type,
					Region:      normalizeAzureRegion(*disk.Location),
					Tags:        toAzureTags(disk.Tags),
					Meta:        structToMap(disk),
					Status:      status,
					CreatedAt:   createdAt,
					Arn:         *disk.ID,
					ServiceName: s.Name(),
				})
			}
		}
	}

	return allResources, nil
}

func (s *diskService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	var allRecommendations []providers.Recommendation
	for _, resource := range existingResources {
		resourceRecommendations := make(map[string]providers.Recommendation)

		// Check for Tags
		if len(resource.Tags) == 0 {
			resourceRecommendations["azure_missing_tags"] = providers.Recommendation{
				CategoryName: providers.RecommendationCategoryConfiguration,
				RuleName:     "azure_missing_tags",
				Severity:     providers.RecommendationSeverityLow,
				Savings:      0,
				Data: map[string]any{
					"resource_id":     resource.Id,
					"resource_name":   resource.Name,
					"resource_type":   resource.Type,
					"resource_region": resource.Region,
					"service_name":    resource.ServiceName,
					"reason":          "Managed Disk has no tags applied. Tags are essential for cost allocation, resource organization, compliance tracking, and disk lifecycle management.",
					"benefits": []string{
						"Better cost tracking and allocation",
						"Easier resource organization and filtering",
						"Compliance and governance enforcement",
						"Automated policy application",
						"Disk lifecycle management (backup, retention policies)",
						"Ownership and purpose identification",
					},
					"recommended_tags": []string{"environment", "owner", "cost-center", "application", "workload-type", "backup-policy"},
				},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			}
		}

		properties, _ := resource.Meta["properties"].(map[string]interface{})

		// Rule: Azure Disk Encryption for Unattached Disk Volumes
		// Check if disk is unattached and not encrypted
		if diskState, ok := properties["diskState"].(string); ok && diskState == string(armcompute.DiskStateUnattached) {
			encrypted := false
			if encryption, ok := properties["encryption"].(map[string]interface{}); ok {
				if _, ok := encryption["type"].(string); ok {
					encrypted = true
				}
			}
			if !encrypted {
				var diskSizeGB float64
				if size, ok := properties["diskSizeGB"].(float64); ok {
					diskSizeGB = size
				}
				resourceRecommendations["azure_disk_unattached_unencrypted"] = providers.Recommendation{
					CategoryName: providers.RecommendationCategorySecurity,
					RuleName:     "azure_disk_unattached_unencrypted",
					Severity:     providers.RecommendationSeverityHigh,
					Savings:      0,
					Data: map[string]any{
						"resource_id":            resource.Id,
						"resource_name":          resource.Name,
						"resource_type":          resource.Type,
						"resource_region":        resource.Region,
						"service_name":           resource.ServiceName,
						"disk_state":             diskState,
						"disk_size_gb":           diskSizeGB,
						"current_encryption":     "None or Platform Managed Keys (PMK)",
						"recommended_encryption": "Server-Side Encryption with Platform or Customer Managed Keys",
						"reason":                 "Unattached disk is not encrypted. All managed disks should have encryption enabled to protect data at rest, even when not actively attached to VMs. This prevents unauthorized access if disks are exported or accessed outside normal VM operations.",
						"benefits": []string{
							"Protection of data at rest",
							"Compliance with data protection regulations (GDPR, HIPAA, PCI DSS)",
							"AES-256 encryption",
							"No performance impact with server-side encryption",
							"Protection against disk export/theft scenarios",
							"Required for regulatory compliance",
							"Automatic encryption of snapshots and images",
						},
						"encryption_options": []string{"Server-Side Encryption with PMK (default, no additional cost)", "Server-Side Encryption with CMK (customer-managed keys via Key Vault)"},
						"compliance_impact":  "Required for GDPR, HIPAA, PCI DSS, and SOC 2 compliance",
						"cost_impact":        "No additional cost for platform-managed key encryption",
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				}
			}
		}

		// Rule: Remove Unattached Virtual Machine Disk Volumes
		// Check if disk is unattached
		if diskState, ok := properties["diskState"].(string); ok && diskState == string(armcompute.DiskStateUnattached) {
			var diskSizeGB float64
			var diskSKU string
			if size, ok := properties["diskSizeGB"].(float64); ok {
				diskSizeGB = size
			}
			if sku, ok := resource.Meta["sku"].(map[string]interface{}); ok {
				if name, ok := sku["name"].(string); ok {
					diskSKU = name
				}
			}
			monthlyCost := getDiskMonthlyCost(diskSKU, diskSizeGB)
			resourceRecommendations["azure_disk_unattached_volume"] = providers.Recommendation{
				CategoryName: providers.RecommendationCategoryRightSizing,
				RuleName:     "azure_disk_unattached_volume",
				Severity:     providers.RecommendationSeverityMedium,
				Savings:      monthlyCost,
				Data: map[string]any{
					"resource_id":     resource.Id,
					"resource_name":   resource.Name,
					"resource_type":   resource.Type,
					"resource_region": resource.Region,
					"service_name":    resource.ServiceName,
					"disk_state":      diskState,
					"disk_size_gb":    diskSizeGB,
					"disk_sku":        diskSKU,
					"reason":          "Disk is unattached and not being used by any VM. Unattached disks incur storage costs without providing value. Consider taking a snapshot for backup, then delete the disk to eliminate ongoing costs.",
					"benefits": []string{
						"Eliminate storage costs for unused disks",
						"Simplified resource inventory",
						"Reduced management overhead",
						"Cleaner backup and disaster recovery planning",
						"Better cost visibility",
					},
					"estimated_savings_type": "monthly",
					"savings_source":         "Azure Managed Disk pricing",
					"recommended_action":     "Create snapshot for backup, then delete disk",
					"cost_note":              "Snapshots cost ~$0.05/GB/month vs. disk full SKU pricing",
				},
				Action:              providers.RecommendationActionDelete,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			}
		}

		// Rule: Server Side Encryption for Unattached Disk using CMK
		// Check if disk is unattached and not encrypted with CMK
		if diskState, ok := properties["diskState"].(string); ok && diskState == string(armcompute.DiskStateUnattached) {
			cmkEncrypted := false
			if encryption, ok := properties["encryption"].(map[string]interface{}); ok {
				if typeVal, ok := encryption["type"].(string); ok && typeVal == string(armcompute.EncryptionTypeEncryptionAtRestWithCustomerKey) {
					cmkEncrypted = true
				}
			}
			if !cmkEncrypted {
				var diskSizeGB float64
				if size, ok := properties["diskSizeGB"].(float64); ok {
					diskSizeGB = size
				}
				resourceRecommendations["azure_disk_unattached_cmk_missing"] = providers.Recommendation{
					CategoryName: providers.RecommendationCategorySecurity,
					RuleName:     "azure_disk_unattached_cmk_missing",
					Severity:     providers.RecommendationSeverityHigh,
					Savings:      0,
					Data: map[string]any{
						"resource_id":            resource.Id,
						"resource_name":          resource.Name,
						"resource_type":          resource.Type,
						"resource_region":        resource.Region,
						"service_name":           resource.ServiceName,
						"disk_state":             diskState,
						"disk_size_gb":           diskSizeGB,
						"current_encryption":     "Platform Managed Keys (PMK)",
						"recommended_encryption": "Customer Managed Keys (CMK) via Disk Encryption Set",
						"reason":                 "Disk is using platform-managed encryption keys instead of customer-managed keys. Customer-managed keys provide enhanced security control, compliance capabilities, and the ability to revoke access to encrypted data at any time via Azure Key Vault.",
						"benefits": []string{
							"Full control over encryption key lifecycle",
							"Ability to revoke access to data instantly",
							"Key rotation on your schedule",
							"Compliance with regulatory requirements (HIPAA, PCI DSS, GDPR)",
							"Integration with Azure Key Vault",
							"Audit trail for key access via Key Vault logs",
							"Support for multi-region key replication",
						},
						"key_vault_required": true,
						"compliance_impact":  "Required for HIPAA, PCI DSS Level 1, GDPR, and government compliance",
						"encryption_type":    "AES-256",
						"cost_impact":        "Key Vault costs (~$0.03/10,000 operations) + Key storage (~$1/key/month)",
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				}
			}
		}

		// Rule: Disable Public Network Access to Virtual Machine Disks
		// Check if disk has public network access enabled (networkAccessPolicy is AllowAll)
		if networkAccessPolicy, ok := properties["networkAccessPolicy"].(string); ok && networkAccessPolicy == string(armcompute.NetworkAccessPolicyAllowAll) {
			resourceRecommendations["azure_disk_public_network_access_enabled"] = providers.Recommendation{
				CategoryName: providers.RecommendationCategorySecurity,
				RuleName:     "azure_disk_public_network_access_enabled",
				Severity:     providers.RecommendationSeverityCritical,
				Savings:      0,
				Data: map[string]any{
					"resource_id":                resource.Id,
					"resource_name":              resource.Name,
					"resource_type":              resource.Type,
					"resource_region":            resource.Region,
					"service_name":               resource.ServiceName,
					"current_network_access":     "AllowAll (Public access enabled)",
					"recommended_network_access": "DenyAll or AllowPrivate (Private endpoints only)",
					"reason":                     "Disk has public network access enabled. Public access allows disk export and direct access from the internet, creating a significant security risk. Disabling public access and using private endpoints ensures disk data can only be accessed from within your Azure Virtual Networks.",
					"benefits": []string{
						"Eliminates public internet exposure for disk data",
						"Prevents unauthorized disk export operations",
						"Protection against data exfiltration",
						"Compliance with Zero Trust security model",
						"Private endpoint support for secure disk access",
						"Network-level access control via VNet/subnet",
						"Required for high-security environments",
					},
					"security_risks": []string{
						"Unauthorized disk export from internet",
						"Data exfiltration via public access",
						"Exposure to internet-based attacks",
						"Compliance violations (PCI DSS, HIPAA)",
					},
					"recommended_configuration": "Set networkAccessPolicy to DenyAll or AllowPrivate with Private Endpoint",
					"compliance_impact":         "Critical for PCI DSS, HIPAA, SOC 2, and Zero Trust compliance",
					"cost_impact":               "Private Endpoint: ~$7-10/month per endpoint",
				},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			}
		}

		// Check for disk SKU upgrades and optimization opportunities
		if properties != nil {
			if diskSKU, ok := resource.Meta["sku"].(map[string]interface{}); ok {
				if skuName, ok := diskSKU["name"].(string); ok {
					var diskSizeGB float64
					if size, ok := properties["diskSizeGB"].(float64); ok {
						diskSizeGB = size
					}

					// Check for Standard HDD → Standard SSD or Premium SSD upgrade
					if skuName == "Standard_LRS" || skuName == "StandardSSD_LRS" {
						upgradeSuggestion := getDiskSKUUpgradeSuggestion(skuName, diskSizeGB)
						if upgradeSuggestion != nil {
							resourceRecommendations["azure_managed_disk_sku_upgrade"] = providers.Recommendation{
								CategoryName: providers.RecommendationCategoryInfraUpgrade,
								RuleName:     "azure_managed_disk_sku_upgrade",
								Severity:     providers.RecommendationSeverityMedium,
								Savings:      upgradeSuggestion["estimated_savings_monthly"].(float64),
								Data: map[string]any{
									"resource_id":                 resource.Id,
									"resource_name":               resource.Name,
									"resource_type":               resource.Type,
									"resource_region":             resource.Region,
									"service_name":                resource.ServiceName,
									"current_sku":                 skuName,
									"current_tier":                upgradeSuggestion["current_tier"],
									"disk_size_gb":                diskSizeGB,
									"recommended_sku":             upgradeSuggestion["recommended_sku"],
									"recommended_tier":            upgradeSuggestion["recommended_tier"],
									"reason":                      upgradeSuggestion["reason"],
									"benefits":                    upgradeSuggestion["benefits"],
									"estimated_savings_type":      "monthly",
									"savings_source":              "Azure pricing documentation and I/O performance benchmarks",
									"iops_improvement":            upgradeSuggestion["iops_improvement"],
									"throughput_improvement_mbps": upgradeSuggestion["throughput_improvement_mbps"],
									"cost_impact_note":            upgradeSuggestion["cost_impact_note"],
								},
								Action:              providers.RecommendationActionModify,
								ResourceServiceName: resource.ServiceName,
								ResourceId:          resource.Id,
								ResourceType:        resource.Type,
								ResourceRegion:      resource.Region,
							}
						}
					}

					// Check for Premium SSD → Premium SSD v2 upgrade (newer generation)
					if skuName == "Premium_LRS" && diskSizeGB >= 32 {
						resourceRecommendations["azure_disk_premium_ssd_v2_upgrade"] = providers.Recommendation{
							CategoryName: providers.RecommendationCategoryInfraUpgrade,
							RuleName:     "azure_disk_premium_ssd_v2_upgrade",
							Severity:     providers.RecommendationSeverityLow,
							Savings:      15.0, // Variable based on IOPS/throughput provisioning
							Data: map[string]any{
								"resource_id":      resource.Id,
								"resource_name":    resource.Name,
								"resource_type":    resource.Type,
								"resource_region":  resource.Region,
								"service_name":     resource.ServiceName,
								"current_sku":      skuName,
								"current_tier":     "Premium SSD",
								"disk_size_gb":     diskSizeGB,
								"recommended_sku":  "PremiumV2_LRS",
								"recommended_tier": "Premium SSD v2",
								"reason":           "Premium SSD can be upgraded to Premium SSD v2 for better performance flexibility and potential cost savings through granular IOPS/throughput provisioning.",
								"benefits": []string{
									"Granular IOPS provisioning (up to 80,000 IOPS)",
									"Granular throughput control (up to 1,200 MBps)",
									"Sub-millisecond latency",
									"No downtime for performance scaling",
									"Pay only for provisioned performance",
									"Better price-performance for high-IOPS workloads",
								},
								"estimated_savings_type": "monthly",
								"savings_source":         "Azure Premium SSD v2 pricing - savings vary based on actual IOPS/throughput needs",
								"savings_note":           "Actual savings depend on IOPS and throughput requirements. Can save 10-30% if you don't need maximum performance.",
							},
							Action:              providers.RecommendationActionModify,
							ResourceServiceName: resource.ServiceName,
							ResourceId:          resource.Id,
							ResourceType:        resource.Type,
							ResourceRegion:      resource.Region,
						}
					}
				}
			}
		}

		for _, rec := range resourceRecommendations {
			allRecommendations = append(allRecommendations, rec)
		}
	}
	return allRecommendations, nil
}

// getDiskSKUUpgradeSuggestion returns upgrade suggestions for Azure Managed Disk SKUs
// Focuses on Standard HDD → Standard SSD and Premium SSD optimizations
func getDiskSKUUpgradeSuggestion(currentSKU string, diskSizeGB float64) map[string]interface{} {
	// Standard HDD → Standard SSD (better performance, minimal cost increase)
	if currentSKU == "Standard_LRS" {
		// Calculate approximate pricing impact
		// Standard HDD: ~$0.04/GB, Standard SSD: ~$0.075/GB, Premium SSD: ~$0.12/GB
		costIncreaseSSD := diskSizeGB * (0.075 - 0.04)
		costIncreasePremium := diskSizeGB * (0.12 - 0.04)

		// For smaller disks (<256GB), Standard SSD is cost-effective
		// For larger disks or high-IOPS needs, Premium SSD might be better
		if diskSizeGB <= 256 {
			return map[string]interface{}{
				"current_tier":                "Standard HDD",
				"recommended_sku":             "StandardSSD_LRS",
				"recommended_tier":            "Standard SSD",
				"estimated_savings_monthly":   0.0, // This is actually a cost increase, but worth it for performance
				"cost_impact_note":            fmt.Sprintf("Cost increases by ~$%.2f/month, but provides 10x better IOPS", costIncreaseSSD),
				"iops_improvement":            "500 → 6,000 IOPS",
				"throughput_improvement_mbps": "60 → 750 MBps",
				"reason":                      "Standard HDD provides only 500 IOPS. Upgrading to Standard SSD provides 10-12x better IOPS and throughput for a modest cost increase, significantly improving application performance.",
				"benefits": []string{
					"10-12x IOPS improvement (500 → 6,000 IOPS)",
					"12x throughput improvement (60 → 750 MBps)",
					"Lower latency (SSD vs HDD)",
					"Better for databases and transaction-heavy workloads",
					"Improved application responsiveness",
					"Minimal cost increase for significant performance gain",
				},
			}
		}
		// For larger disks, consider Premium SSD
		return map[string]interface{}{
			"current_tier":                "Standard HDD",
			"recommended_sku":             "Premium_LRS",
			"recommended_tier":            "Premium SSD",
			"estimated_savings_monthly":   0.0,
			"cost_impact_note":            fmt.Sprintf("Cost increases by ~$%.2f/month, but provides enterprise-grade performance", costIncreasePremium),
			"iops_improvement":            fmt.Sprintf("500 → %d IOPS", minInt(int(diskSizeGB)*5, 20000)),
			"throughput_improvement_mbps": fmt.Sprintf("60 → %d MBps", minInt(int(diskSizeGB), 900)),
			"reason":                      "Large Standard HDD disk with only 500 IOPS. Premium SSD provides enterprise-grade performance with up to 20,000 IOPS and 900 MBps throughput, essential for production workloads.",
			"benefits": []string{
				"Up to 40x IOPS improvement",
				"Up to 15x throughput improvement",
				"Single-digit millisecond latency",
				"99.9% SLA for single instance VMs",
				"Enterprise-grade reliability",
				"Suitable for production databases and mission-critical apps",
			},
		}
	}

	// StandardSSD → Premium SSD (for workloads needing higher IOPS)
	if currentSKU == "StandardSSD_LRS" && diskSizeGB >= 128 {
		costIncrease := diskSizeGB * (0.12 - 0.075)
		return map[string]interface{}{
			"current_tier":                "Standard SSD",
			"recommended_sku":             "Premium_LRS",
			"recommended_tier":            "Premium SSD",
			"estimated_savings_monthly":   0.0,
			"cost_impact_note":            fmt.Sprintf("Cost increases by ~$%.2f/month for premium performance", costIncrease),
			"iops_improvement":            fmt.Sprintf("6,000 → %d IOPS", minInt(int(diskSizeGB)*5, 20000)),
			"throughput_improvement_mbps": fmt.Sprintf("750 → %d MBps", minInt(int(diskSizeGB), 900)),
			"reason":                      "Standard SSD provides good performance but may bottleneck high-throughput workloads. Premium SSD offers up to 3x better IOPS and improved latency for production databases.",
			"benefits": []string{
				"Up to 3x IOPS improvement",
				"Lower latency",
				"99.9% SLA for single instance VMs",
				"Better for SQL Server, Oracle, and high-traffic applications",
				"Consistent performance under load",
			},
		}
	}

	return nil
}

// minInt helper function for disk recommendations
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (s *diskService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	_, err := s.ApplyCommand(ctx, account, providers.ApplyCommandRequest{
		ServiceName: recommendation.ResourceServiceName,
		ResourceId:  recommendation.ResourceId,
		Command:     recommendation.RuleName,
		Args:        recommendation.Data,
	})
	return err
}

func (s *diskService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	logger := ctx.GetLogger()
	cred, _, err := getAzureCredsForAccount(ctx, account)
	if err != nil {
		return providers.ApplyCommandResponse{Success: false, Message: "failed to create azure credential"}, fmt.Errorf("failed to create azure credential: %w", err)
	}

	subId, err := extractSubscriptionID(command.ResourceId)
	if err != nil {
		return providers.ApplyCommandResponse{Success: false, Message: "failed to extract subscription id"}, fmt.Errorf("failed to extract subscription id from resource id: %w", err)
	}

	rg, err := extractResourceGroup(command.ResourceId)
	if err != nil {
		return providers.ApplyCommandResponse{Success: false, Message: "failed to extract resource group"}, fmt.Errorf("failed to extract resource group from resource id: %w", err)
	}

	parts := strings.Split(command.ResourceId, "/")
	diskName := parts[len(parts)-1]

	disksClient, err := armcompute.NewDisksClient(subId, cred, getAzureAuditOpts(ctx))
	if err != nil {
		return providers.ApplyCommandResponse{Success: false, Message: "failed to create disks client"}, fmt.Errorf("failed to create disks client: %w", err)
	}

	switch command.Command {
	case "azure_disk_unattached_volume":
		logger.Info("applying recommendation: deleting unattached disk", "diskName", diskName)
		poller, err := disksClient.BeginDelete(ctx.GetContext(), rg, diskName, nil)
		if err != nil {
			return providers.ApplyCommandResponse{Success: false, Message: "failed to begin disk deletion"}, fmt.Errorf("failed to begin disk deletion: %w", err)
		}
		_, err = poller.PollUntilDone(ctx.GetContext(), nil)
		if err != nil {
			return providers.ApplyCommandResponse{Success: false, Message: "failed to complete disk deletion"}, fmt.Errorf("failed to complete disk deletion: %w", err)
		}
		return providers.ApplyCommandResponse{Success: true, Message: "Successfully deleted unattached disk: " + diskName}, nil
	case "azure_disk_unattached_unencrypted", "azure_disk_unattached_cmk_missing", "azure_disk_public_network_access_enabled":
		return providers.ApplyCommandResponse{Success: false, Message: "modifying disk encryption or network access requires manual intervention"}, errors.New("unsupported action")
	default:
		return providers.ApplyCommandResponse{Success: false, Message: "unknown command"}, fmt.Errorf("unknown command: %s", command.Command)
	}
}

func (s *diskService) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
	// Azure Disks do not have their own diagnostic settings that can be queried via armmonitor.
	// Logging and metrics are typically associated with the parent Virtual Machine or Storage Account.
	// Therefore, a direct log group lookup is not applicable for a disk resource itself.
	return "", errors.New("log group not applicable for Azure Disk resources")
}

func (s *diskService) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, resource providers.Resource) (providers.ServiceMapApplication, error) {
	rg, err := extractResourceGroup(resource.Id)
	if err != nil {
		return providers.ServiceMapApplication{}, fmt.Errorf("failed to extract resource group: %w", err)
	}

	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resource.Id,
			Kind:      s.Name(),
			Namespace: resource.Region,
		},
		Upstreams: []providers.UpstreamLink{
			{
				Id: providers.ServiceApplicationId{
					Name:      rg,
					Kind:      "Microsoft.Resources/resourceGroups",
					Namespace: resource.Region,
				}.Key(),
			},
		},
		Downstreams: []providers.DownstreamLink{},
		Status:      "Unknown",
	}

	if managedBy, ok := resource.Meta["managedBy"].(string); ok && managedBy != "" {
		app.Upstreams = append(app.Upstreams, providers.ServiceApplicationLink{
			Id: providers.ServiceApplicationId{
				Name:      managedBy,
				Kind:      "Microsoft.Compute/virtualMachines",
				Namespace: resource.Region,
			},
		}.ToUpstreamLink())
	}
	return app, nil
}

func (s *diskService) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAzureMonitorMetrics(ctx, account, filter)
}
