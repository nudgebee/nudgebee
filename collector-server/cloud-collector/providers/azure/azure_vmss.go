package azure

import (
	"errors"
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
)

type virtualMachineScaleSetService struct {
}

func (s *virtualMachineScaleSetService) Name() string {
	return "Microsoft.Compute/virtualMachineScaleSets"
}

// Scope returns the service scope - this is a regional service
func (s *virtualMachineScaleSetService) Scope() ServiceScope {
	return ServiceScopeRegional
}

func (s *virtualMachineScaleSetService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
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
		vmssClient, err := armcompute.NewVirtualMachineScaleSetsClient(subID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to create vmss client: %w", err)
		}

		pager := vmssClient.NewListAllPager(nil)

		for pager.More() {
			page, err := pager.NextPage(ctx.GetContext())
			if err != nil {
				return nil, fmt.Errorf("failed to get next page: %w", err)
			}
			for _, vmss := range page.Value {
				status := providers.ResourceStatusUnknown
				if vmss.Properties != nil && vmss.Properties.ProvisioningState != nil {
					if val, ok := nbStatusFromAzureProvisioningState[*vmss.Properties.ProvisioningState]; ok {
						status = val
					}
				}
				var createdAt time.Time
				if vmss.Properties != nil && vmss.Properties.TimeCreated != nil {
					createdAt = *vmss.Properties.TimeCreated
				} else {
					createdAt = getCreatedAtFromTags(vmss.Tags)
				}

				allResources = append(allResources, providers.Resource{
					Id:          *vmss.ID,
					Name:        *vmss.Name,
					Type:        *vmss.Type,
					Region:      *vmss.Location,
					Tags:        toAzureTags(vmss.Tags),
					Meta:        structToMap(vmss),
					Status:      status,
					CreatedAt:   createdAt,
					Arn:         *vmss.ID,
					ServiceName: s.Name(),
				})
			}
		}
	}

	return allResources, nil
}

func (s *virtualMachineScaleSetService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
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
					"reason":          "VMSS has no tags applied. Tags are essential for cost allocation, resource organization, compliance tracking, and automated management policies across scale set instances.",
					"benefits": []string{
						"Better cost tracking and allocation across all instances",
						"Easier resource organization and filtering",
						"Compliance and governance enforcement",
						"Automated policy application to scale sets",
						"Team ownership identification",
						"Simplified autoscaling policy management",
					},
					"recommended_tags": []string{"environment", "owner", "cost-center", "application", "project", "autoscaling-policy"},
				},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			}
		}

		// Rule: Check for Empty Virtual Machine Scale Sets
		sku, _ := resource.Meta["sku"].(map[string]interface{})
		if instanceCount, ok := sku["capacity"].(float64); ok && instanceCount == 0 {
			var vmSize string
			if sku != nil {
				if name, ok := sku["name"].(string); ok {
					vmSize = name
				}
			}
			monthlySavings := getVMCost(ctx, vmSize) * 0.0 // 0 instances, but keeping infrastructure
			resourceRecommendations["azure_vmss_empty"] = providers.Recommendation{
				CategoryName: providers.RecommendationCategoryRightSizing,
				RuleName:     "azure_vmss_empty",
				Severity:     providers.RecommendationSeverityLow,
				Savings:      monthlySavings,
				Data: map[string]any{
					"resource_id":            resource.Id,
					"resource_name":          resource.Name,
					"resource_type":          resource.Type,
					"resource_region":        resource.Region,
					"service_name":           resource.ServiceName,
					"current_instance_count": 0,
					"vm_size":                vmSize,
					"reason":                 "VMSS has zero instances running. Empty scale sets still incur management overhead and potential costs for associated resources (load balancers, public IPs, storage). Consider deleting if not actively used.",
					"benefits": []string{
						"Eliminate management overhead",
						"Remove unused infrastructure costs",
						"Simplified resource inventory",
						"Reduced security attack surface",
						"Cleaner resource organization",
					},
					"estimated_savings_type": "monthly",
					"cost_note":              "While no compute costs for 0 instances, associated resources (LB, IPs, storage) may incur charges",
				},
				Action:              providers.RecommendationActionDelete,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			}
		}

		properties, _ := resource.Meta["properties"].(map[string]interface{})

		// Rule: Disable Public IP Address Assignment for VMSS Instances
		if virtualMachineProfile, ok := properties["virtualMachineProfile"].(map[string]interface{}); ok {
			if networkProfile, ok := virtualMachineProfile["networkProfile"].(map[string]interface{}); ok {
				if networkInterfaceConfigurations, ok := networkProfile["networkInterfaceConfigurations"].([]interface{}); ok {
					for _, nicConfig := range networkInterfaceConfigurations {
						if nicConfigMap, ok := nicConfig.(map[string]interface{}); ok {
							if ipConfigurations, ok := nicConfigMap["ipConfigurations"].([]interface{}); ok {
								for _, ipConfig := range ipConfigurations {
									if ipConfigMap, ok := ipConfig.(map[string]interface{}); ok {
										if _, publicIPFound := ipConfigMap["publicIPAddressConfiguration"]; publicIPFound {
											resourceRecommendations["azure_vmss_instance_public_ip_assigned"] = providers.Recommendation{
												CategoryName: providers.RecommendationCategorySecurity,
												RuleName:     "azure_vmss_instance_public_ip_assigned",
												Severity:     providers.RecommendationSeverityHigh,
												Savings:      0,
												Data: map[string]any{
													"resource_id":               resource.Id,
													"resource_name":             resource.Name,
													"resource_type":             resource.Type,
													"resource_region":           resource.Region,
													"service_name":              resource.ServiceName,
													"current_configuration":     "Public IP per instance",
													"recommended_configuration": "Load Balancer with NAT rules or Azure Bastion",
													"reason":                    "VMSS instances have public IP addresses assigned. Direct public IP assignment increases attack surface, complicates network security, and increases costs. Use Azure Load Balancer with NAT rules or Azure Bastion for secure remote access instead.",
													"benefits": []string{
														"Reduced attack surface (no direct internet exposure)",
														"Centralized network security via Load Balancer",
														"Lower public IP costs (one LB IP vs. N instance IPs)",
														"Simplified network security group (NSG) rules",
														"Better DDoS protection with Load Balancer",
														"Improved compliance posture",
														"Easier to implement WAF and traffic filtering",
													},
													"security_risks":        []string{"direct internet exposure", "increased attack vectors", "DDoS vulnerability", "harder to audit traffic"},
													"compliance_impact":     "Required for PCI DSS, SOC 2, and Zero Trust security models",
													"alternative_solutions": []string{"Azure Load Balancer with inbound NAT rules", "Azure Bastion for SSH/RDP", "Application Gateway with WAF", "Azure Firewall"},
												},
												Action:              providers.RecommendationActionModify,
												ResourceServiceName: resource.ServiceName,
												ResourceId:          resource.Id,
												ResourceType:        resource.Type,
												ResourceRegion:      resource.Region,
											}
											break
										}
									}
								}
							}
						}
						if _, exists := resourceRecommendations["azure_vmss_instance_public_ip_assigned"]; exists {
							break
						}
					}
				}
			}
		}

		// Rule: Enable Automatic Instance Repairs
		if properties, ok := resource.Meta["properties"].(map[string]interface{}); ok {
			automaticRepairsDisabled := true
			if automaticRepairsPolicy, ok := properties["automaticRepairsPolicy"].(map[string]interface{}); ok {
				if enabled, ok := automaticRepairsPolicy["enabled"].(bool); ok && enabled {
					automaticRepairsDisabled = false
				}
			}
			if automaticRepairsDisabled {
				resourceRecommendations["azure_vmss_automatic_instance_repairs_disabled"] = providers.Recommendation{
					CategoryName: providers.RecommendationCategoryConfiguration,
					RuleName:     "azure_vmss_automatic_instance_repairs_disabled",
					Severity:     providers.RecommendationSeverityMedium,
					Savings:      0,
					Data: map[string]any{
						"resource_id":               resource.Id,
						"resource_name":             resource.Name,
						"resource_type":             resource.Type,
						"resource_region":           resource.Region,
						"service_name":              resource.ServiceName,
						"current_configuration":     "Manual repairs",
						"recommended_configuration": "Automatic instance repairs enabled",
						"reason":                    "Automatic instance repairs are disabled. When enabled, Azure VMSS automatically detects and replaces unhealthy instances using application health probes, improving availability and reducing manual intervention.",
						"benefits": []string{
							"Automatic detection and replacement of unhealthy instances",
							"Improved application availability and uptime",
							"Reduced manual intervention and operational overhead",
							"Self-healing infrastructure",
							"Integration with Application Health Extension",
							"Configurable grace period before repair",
							"Better SLA compliance",
						},
						"requirements": []string{"Application Health Extension or Load Balancer health probe", "Grace period configuration (default: 30 minutes)"},
						"cost_impact":  "No additional cost for automatic repairs feature",
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				}
			}
		}

		// Rule: Check for Zone-Redundant Virtual Machine Scale Sets
		if zones, ok := resource.Meta["zones"].([]interface{}); !ok || len(zones) == 0 {
			resourceRecommendations["azure_vmss_not_zone_redundant"] = providers.Recommendation{
				CategoryName: providers.RecommendationCategoryConfiguration,
				RuleName:     "azure_vmss_not_zone_redundant",
				Severity:     providers.RecommendationSeverityMedium,
				Savings:      0,
				Data: map[string]any{
					"resource_id":               resource.Id,
					"resource_name":             resource.Name,
					"resource_type":             resource.Type,
					"resource_region":           resource.Region,
					"service_name":              resource.ServiceName,
					"current_configuration":     "Single zone or non-zonal",
					"recommended_configuration": "Zone-redundant (multi-zone deployment)",
					"reason":                    "VMSS is not deployed across multiple availability zones. Zone-redundant deployment protects against datacenter-level failures, providing 99.99% SLA vs. 99.95% for single-zone deployments.",
					"benefits": []string{
						"99.99% SLA (vs. 99.95% single zone)",
						"Protection against datacenter-level failures",
						"Improved disaster recovery capabilities",
						"Higher availability for production workloads",
						"Automatic instance distribution across zones",
						"No additional compute cost for zone redundancy",
						"Better compliance with high-availability requirements",
					},
					"sla_improvement":   "99.95% → 99.99% (4.38 hours → 52 minutes downtime per year)",
					"supported_regions": "Not all regions support availability zones - check region capabilities",
					"cost_impact":       "No additional compute cost; potential data transfer costs between zones",
				},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			}
		}

		// Check for Boot Diagnostics
		if virtualMachineProfile, ok := properties["virtualMachineProfile"].(map[string]interface{}); ok {
			if diagnosticsProfile, ok := virtualMachineProfile["diagnosticsProfile"].(map[string]interface{}); ok {
				if bootDiagnostics, ok := diagnosticsProfile["bootDiagnostics"].(map[string]interface{}); ok {
					if enabled, ok := bootDiagnostics["enabled"].(bool); !ok || !enabled {
						resourceRecommendations["azure_vmss_boot_diagnostics_disabled"] = providers.Recommendation{
							CategoryName: providers.RecommendationCategoryConfiguration,
							RuleName:     "azure_vmss_boot_diagnostics_disabled",
							Severity:     providers.RecommendationSeverityMedium,
							Savings:      0,
							Data: map[string]any{
								"resource_id":     resource.Id,
								"resource_name":   resource.Name,
								"resource_type":   resource.Type,
								"resource_region": resource.Region,
								"service_name":    resource.ServiceName,
								"reason":          "Boot diagnostics is disabled for VMSS instances. Enabling boot diagnostics provides console output and screenshots for troubleshooting instance boot issues across the scale set, critical for production environments.",
								"benefits": []string{
									"Console output for boot troubleshooting across all instances",
									"Screenshot capture during boot failures",
									"Faster root cause analysis for scale set issues",
									"Better incident response capability",
									"Required for Azure support diagnostics",
									"Serial console access for emergency troubleshooting",
								},
								"cost_impact": "Minimal storage cost for diagnostic logs (~$0.10-0.50/month per scale set)",
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

		// Check for System-Assigned Managed Identity
		if identity, ok := resource.Meta["identity"].(map[string]interface{}); ok {
			if identityType, ok := identity["type"].(string); ok && !strings.Contains(strings.ToLower(identityType), "systemassigned") {
				resourceRecommendations["azure_vmss_system_assigned_identity_disabled"] = providers.Recommendation{
					CategoryName: providers.RecommendationCategorySecurity,
					RuleName:     "azure_vmss_system_assigned_identity_disabled",
					Severity:     providers.RecommendationSeverityMedium,
					Savings:      0,
					Data: map[string]any{
						"resource_id":          resource.Id,
						"resource_name":        resource.Name,
						"resource_type":        resource.Type,
						"resource_region":      resource.Region,
						"service_name":         resource.ServiceName,
						"current_identity":     identityType,
						"recommended_identity": "SystemAssigned",
						"reason":               "VMSS does not have system-assigned managed identity enabled. Managed identities eliminate the need to store credentials in code or configuration for all scale set instances, improving security posture significantly.",
						"benefits": []string{
							"No credentials stored in code or configuration",
							"Automatic credential rotation for all instances",
							"Secure access to Azure Key Vault",
							"Native Azure RBAC integration",
							"Reduced credential management overhead",
							"Better compliance with security standards",
							"Simplified secret management across scale set",
						},
						"use_cases": []string{"Azure Key Vault access", "Azure Storage access", "Azure SQL authentication", "Service-to-service authentication", "Container registry access"},
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				}
			}
		}

		// Check for Automatic OS Upgrades
		if properties, ok := resource.Meta["properties"].(map[string]interface{}); ok {
			automaticOSUpgradeDisabled := true
			if upgradePolicy, ok := properties["upgradePolicy"].(map[string]interface{}); ok {
				if automaticOSUpgradePolicy, ok := upgradePolicy["automaticOSUpgradePolicy"].(map[string]interface{}); ok {
					if enableAutomaticOSUpgrade, ok := automaticOSUpgradePolicy["enableAutomaticOSUpgrade"].(bool); ok && enableAutomaticOSUpgrade {
						automaticOSUpgradeDisabled = false
					}
				}
			}
			if automaticOSUpgradeDisabled {
				resourceRecommendations["azure_vmss_automatic_os_upgrade_disabled"] = providers.Recommendation{
					CategoryName: providers.RecommendationCategorySecurity,
					RuleName:     "azure_vmss_automatic_os_upgrade_disabled",
					Severity:     providers.RecommendationSeverityMedium,
					Savings:      0,
					Data: map[string]any{
						"resource_id":                   resource.Id,
						"resource_name":                 resource.Name,
						"resource_type":                 resource.Type,
						"resource_region":               resource.Region,
						"service_name":                  resource.ServiceName,
						"current_os_upgrade_policy":     "Manual",
						"recommended_os_upgrade_policy": "Automatic",
						"reason":                        "Automatic OS upgrades are disabled for VMSS. Enabling automatic OS upgrades ensures all instances receive critical security patches automatically with rolling updates, reducing vulnerability exposure and manual maintenance overhead.",
						"benefits": []string{
							"Automatic security patch deployment across all instances",
							"Reduced vulnerability exposure window",
							"Lower manual maintenance burden",
							"Rolling upgrade with configurable batch size",
							"Automatic health checks before upgrade",
							"Compliance with security policies",
							"Minimized downtime from security incidents",
						},
						"upgrade_behavior":  "Rolling upgrades with health checks to maintain availability",
						"security_impact":   "Critical security patches applied automatically within hours of release",
						"compliance_impact": "Required for many security compliance frameworks",
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				}
			}
		}

		// Check for Accelerated Networking
		if virtualMachineProfile, ok := properties["virtualMachineProfile"].(map[string]interface{}); ok {
			if networkProfile, ok := virtualMachineProfile["networkProfile"].(map[string]interface{}); ok {
				if networkInterfaceConfigurations, ok := networkProfile["networkInterfaceConfigurations"].([]interface{}); ok {
					for _, nicConfig := range networkInterfaceConfigurations {
						if nicConfigMap, ok := nicConfig.(map[string]interface{}); ok {
							if properties, ok := nicConfigMap["properties"].(map[string]interface{}); ok {
								if enableAcceleratedNetworking, ok := properties["enableAcceleratedNetworking"].(bool); !ok || !enableAcceleratedNetworking {
									resourceRecommendations["azure_vmss_accelerated_networking_disabled"] = providers.Recommendation{
										CategoryName: providers.RecommendationCategoryInfraUpgrade,
										RuleName:     "azure_vmss_accelerated_networking_disabled",
										Severity:     providers.RecommendationSeverityMedium,
										Savings:      0,
										Data: map[string]any{
											"resource_id":            resource.Id,
											"resource_name":          resource.Name,
											"resource_type":          resource.Type,
											"resource_region":        resource.Region,
											"service_name":           resource.ServiceName,
											"current_networking":     "Standard",
											"recommended_networking": "Accelerated Networking (SR-IOV)",
											"reason":                 "Accelerated Networking is disabled for VMSS instances. Enabling it provides up to 30 Gbps network throughput, lower latency (<1ms jitter), and reduced CPU utilization at no additional cost for supported VM sizes.",
											"benefits": []string{
												"Up to 30 Gbps network throughput per instance",
												"Lower latency and jitter (<1ms)",
												"Reduced CPU utilization (more CPU for applications)",
												"Better packet-per-second performance",
												"No additional cost",
												"Hardware-based network virtualization (SR-IOV)",
												"Improved performance for network-intensive applications",
											},
											"performance_improvement": "Up to 10x network performance improvement",
											"requirement":             "VM must support Accelerated Networking (most D/E/F/G/H/L series)",
											"cost_impact":             "No additional cost",
										},
										Action:              providers.RecommendationActionModify,
										ResourceServiceName: resource.ServiceName,
										ResourceId:          resource.Id,
										ResourceType:        resource.Type,
										ResourceRegion:      resource.Region,
									}
									break
								}
							}
						}
					}
				}
			}
		}

		// Check for Trusted Launch
		if virtualMachineProfile, ok := properties["virtualMachineProfile"].(map[string]interface{}); ok {
			if securityProfile, ok := virtualMachineProfile["securityProfile"].(map[string]interface{}); ok {
				if securityType, ok := securityProfile["securityType"].(string); ok && securityType != "TrustedLaunch" {
					resourceRecommendations["azure_vmss_trusted_launch_disabled"] = providers.Recommendation{
						CategoryName: providers.RecommendationCategorySecurity,
						RuleName:     "azure_vmss_trusted_launch_disabled",
						Severity:     providers.RecommendationSeverityHigh,
						Savings:      0,
						Data: map[string]any{
							"resource_id":               resource.Id,
							"resource_name":             resource.Name,
							"resource_type":             resource.Type,
							"resource_region":           resource.Region,
							"service_name":              resource.ServiceName,
							"current_security_type":     securityType,
							"recommended_security_type": "TrustedLaunch",
							"reason":                    "Trusted Launch is not enabled for VMSS instances. Trusted Launch protects against boot kits, rootkits, and kernel-level malware using Secure Boot and vTPM (virtual Trusted Platform Module). This is a foundational security feature for Gen2 VMs and should be enabled by default for production workloads.",
							"benefits": []string{
								"Secure Boot validation of boot chain integrity for all instances",
								"Protection against rootkits and boot kits",
								"vTPM for measured boot and attestation",
								"Boot integrity monitoring via Azure Monitor",
								"Defense against kernel-level malware",
								"No performance impact",
								"No additional cost",
								"Required for Microsoft Defender for Cloud advanced features",
							},
							"features":          []string{"Secure Boot", "vTPM (virtual Trusted Platform Module)", "Boot integrity monitoring"},
							"vm_requirement":    "Generation 2 VMs only",
							"compliance_impact": "Required for CIS Azure Foundations Benchmark and Azure Security Benchmark",
							"cost_impact":       "No additional cost",
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

		// Check for Overprovision Setting
		if properties, ok := resource.Meta["properties"].(map[string]interface{}); ok {
			if overprovision, ok := properties["overprovision"].(bool); !ok || !overprovision {
				resourceRecommendations["azure_vmss_overprovision_disabled"] = providers.Recommendation{
					CategoryName: providers.RecommendationCategoryConfiguration,
					RuleName:     "azure_vmss_overprovision_disabled",
					Severity:     providers.RecommendationSeverityLow,
					Savings:      0,
					Data: map[string]any{
						"resource_id":               resource.Id,
						"resource_name":             resource.Name,
						"resource_type":             resource.Type,
						"resource_region":           resource.Region,
						"service_name":              resource.ServiceName,
						"current_configuration":     "Overprovision disabled",
						"recommended_configuration": "Overprovision enabled",
						"reason":                    "Overprovision is disabled for VMSS. When enabled, Azure provisions more VMs than requested and only charges for the requested count, ensuring faster scale-out operations and more reliable deployments.",
						"benefits": []string{
							"Faster scale-out operations (extra VMs pre-provisioned)",
							"More reliable instance deployments",
							"Only charged for requested instance count",
							"Better handling of failed provisioning attempts",
							"Recommended for most production workloads",
							"No additional cost (extra VMs deleted after deployment)",
						},
						"use_case":    "Recommended for most scenarios except stateful workloads requiring specific instance management",
						"cost_impact": "No additional cost - only charged for requested instance count",
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				}
			}
		}

		// Check for Upgrade Policy Mode
		if properties, ok := resource.Meta["properties"].(map[string]interface{}); ok {
			if upgradePolicy, ok := properties["upgradePolicy"].(map[string]interface{}); ok {
				if mode, ok := upgradePolicy["mode"].(string); ok && strings.ToLower(mode) == "manual" {
					resourceRecommendations["azure_vmss_manual_upgrade_policy"] = providers.Recommendation{
						CategoryName: providers.RecommendationCategoryConfiguration,
						RuleName:     "azure_vmss_manual_upgrade_policy",
						Severity:     providers.RecommendationSeverityMedium,
						Savings:      0,
						Data: map[string]any{
							"resource_id":              resource.Id,
							"resource_name":            resource.Name,
							"resource_type":            resource.Type,
							"resource_region":          resource.Region,
							"service_name":             resource.ServiceName,
							"current_upgrade_mode":     "Manual",
							"recommended_upgrade_mode": "Rolling or Automatic",
							"reason":                   "VMSS upgrade policy is set to Manual. Manual upgrades require explicit updates to each instance, increasing operational overhead. Consider using Rolling or Automatic upgrade mode for streamlined deployments and updates.",
							"benefits": []string{
								"Automated instance updates when model changes",
								"Controlled rolling upgrades with batch configuration",
								"Reduced manual intervention and operational overhead",
								"Health check integration during upgrades",
								"Configurable pause between upgrade batches",
								"Better for CI/CD integration",
							},
							"upgrade_modes": map[string]string{
								"Automatic": "Instances upgraded immediately when model changes (not recommended for production)",
								"Rolling":   "Instances upgraded in batches with health checks (recommended for production)",
								"Manual":    "Instances require explicit upgrade commands (current setting)",
							},
							"recommendation": "Use Rolling mode for production workloads with health probe integration",
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

		// Check for Application Health Extension or Load Balancer Health Probe
		applicationHealthConfigured := false
		if virtualMachineProfile, ok := properties["virtualMachineProfile"].(map[string]interface{}); ok {
			if extensionProfile, ok := virtualMachineProfile["extensionProfile"].(map[string]interface{}); ok {
				if extensions, ok := extensionProfile["extensions"].([]interface{}); ok {
					for _, ext := range extensions {
						if extMap, ok := ext.(map[string]interface{}); ok {
							if properties, ok := extMap["properties"].(map[string]interface{}); ok {
								if typeVal, ok := properties["type"].(string); ok && strings.Contains(strings.ToLower(typeVal), "applicationhealth") {
									applicationHealthConfigured = true
									break
								}
							}
						}
					}
				}
			}
		}
		if !applicationHealthConfigured {
			resourceRecommendations["azure_vmss_application_health_extension_missing"] = providers.Recommendation{
				CategoryName: providers.RecommendationCategoryConfiguration,
				RuleName:     "azure_vmss_application_health_extension_missing",
				Severity:     providers.RecommendationSeverityMedium,
				Savings:      0,
				Data: map[string]any{
					"resource_id":           resource.Id,
					"resource_name":         resource.Name,
					"resource_type":         resource.Type,
					"resource_region":       resource.Region,
					"service_name":          resource.ServiceName,
					"current_configuration": "No application health monitoring",
					"recommended_extension": "Application Health Extension",
					"reason":                "Application Health Extension is not configured. This extension monitors application health on each instance and is required for automatic instance repairs, rolling upgrades, and maintaining application SLA.",
					"benefits": []string{
						"Application-level health monitoring (vs. VM-level only)",
						"Required for automatic instance repairs",
						"Better rolling upgrade decisions based on app health",
						"Improved SLA and availability",
						"HTTP/HTTPS or TCP health probes",
						"Integration with Load Balancer health probes",
						"No additional cost for the extension",
					},
					"probe_types":  []string{"HTTP", "HTTPS", "TCP"},
					"cost_impact":  "No additional cost",
					"required_for": []string{"Automatic instance repairs", "Rolling upgrades with health validation"},
				},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			}
		}

		for _, rec := range resourceRecommendations {
			allRecommendations = append(allRecommendations, rec)
		}
	}
	return allRecommendations, nil
}

func (s *virtualMachineScaleSetService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	// Check if this is an alarm/metric alert recommendation
	if _, ok := recommendation.Data["alarm_config"]; ok {
		ctx.GetLogger().Info("azure: applying alarm recommendation for vmss",
			"rule_name", recommendation.RuleName,
			"resource_id", recommendation.ResourceId)
		return CreateAzureMetricAlertFromRecommendation(ctx, account, recommendation)
	}

	_, err := s.ApplyCommand(ctx, account, providers.ApplyCommandRequest{
		ResourceId: recommendation.ResourceId,
		Command:    recommendation.RuleName,
		Args:       recommendation.Data,
	})
	return err
}

func (s *virtualMachineScaleSetService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	logger := ctx.GetLogger()
	cred, session, err := getAzureCredsForAccount(ctx, account)
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to create azure credential: %v", err),
		}, err
	}

	// Extract subscription ID, resource group and VMSS name from resource ID
	parts := strings.Split(command.ResourceId, "/")
	var subscriptionID, resourceGroup, vmssName string
	for i, part := range parts {
		if part == "subscriptions" && i+1 < len(parts) {
			subscriptionID = parts[i+1]
		}
		if part == "resourceGroups" && i+1 < len(parts) {
			resourceGroup = parts[i+1]
		}
		if part == "virtualMachineScaleSets" && i+1 < len(parts) {
			vmssName = parts[i+1]
		}
	}

	if subscriptionID == "" {
		subscriptionID = session.SubscriptionID
	}
	if resourceGroup == "" || vmssName == "" {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to extract resource group or VMSS name from resource ID: %s", command.ResourceId),
		}, fmt.Errorf("invalid resource ID")
	}

	client, err := armcompute.NewVirtualMachineScaleSetsClient(subscriptionID, cred, getAzureAuditOpts(ctx))
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to create VMSS client: %v", err),
		}, err
	}

	// Handle different commands
	switch command.Command {
	case "azure_vmss_overprovision_disabled":
		// Enable overprovision
		logger.Info("applying command: enabling overprovision", "vmssName", vmssName)

		vmssResp, err := client.Get(ctx.GetContext(), resourceGroup, vmssName, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to get VMSS: %v", err),
			}, err
		}

		vmss := vmssResp.VirtualMachineScaleSet
		if vmss.Properties == nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: "VMSS properties are nil",
			}, fmt.Errorf("VMSS properties are nil")
		}

		enabled := true
		vmss.Properties.Overprovision = &enabled

		poller, err := client.BeginCreateOrUpdate(ctx.GetContext(), resourceGroup, vmssName, vmss, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to update VMSS: %v", err),
			}, err
		}

		_, err = poller.PollUntilDone(ctx.GetContext(), nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to wait for VMSS update: %v", err),
			}, err
		}

		logger.Info("successfully enabled overprovision", "vmssName", vmssName)
		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("successfully enabled overprovision for VMSS '%s'", vmssName),
		}, nil

	case "azure_vmss_upgrade_policy_not_automatic":
		// Set upgrade policy to automatic (with caution warning)
		logger.Info("applying command: setting upgrade policy to automatic", "vmssName", vmssName)

		vmssResp, err := client.Get(ctx.GetContext(), resourceGroup, vmssName, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to get VMSS: %v", err),
			}, err
		}

		vmss := vmssResp.VirtualMachineScaleSet
		if vmss.Properties == nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: "VMSS properties are nil",
			}, fmt.Errorf("VMSS properties are nil")
		}

		if vmss.Properties.UpgradePolicy == nil {
			vmss.Properties.UpgradePolicy = &armcompute.UpgradePolicy{}
		}
		mode := armcompute.UpgradeModeAutomatic
		vmss.Properties.UpgradePolicy.Mode = &mode

		poller, err := client.BeginCreateOrUpdate(ctx.GetContext(), resourceGroup, vmssName, vmss, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to update VMSS: %v", err),
			}, err
		}

		_, err = poller.PollUntilDone(ctx.GetContext(), nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to wait for VMSS update: %v", err),
			}, err
		}

		logger.Info("successfully set upgrade policy to automatic", "vmssName", vmssName)
		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("successfully set upgrade policy to automatic for VMSS '%s'", vmssName),
		}, nil

	case "scale_up", "scale_down":
		// Scale the VMSS
		if command.Args == nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: "capacity argument is required",
			}, fmt.Errorf("missing capacity argument")
		}

		var capacity int64
		switch v := command.Args["capacity"].(type) {
		case float64:
			capacity = int64(v)
		case int:
			capacity = int64(v)
		case int64:
			capacity = v
		default:
			return providers.ApplyCommandResponse{
				Success: false,
				Message: "capacity must be a number",
			}, fmt.Errorf("invalid capacity type")
		}

		logger.Info("applying command: scaling VMSS", "vmssName", vmssName, "capacity", capacity)

		vmssResp, err := client.Get(ctx.GetContext(), resourceGroup, vmssName, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to get VMSS: %v", err),
			}, err
		}

		vmss := vmssResp.VirtualMachineScaleSet
		if vmss.SKU == nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: "VMSS SKU is nil",
			}, fmt.Errorf("VMSS SKU is nil")
		}

		vmss.SKU.Capacity = &capacity

		poller, err := client.BeginCreateOrUpdate(ctx.GetContext(), resourceGroup, vmssName, vmss, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to scale VMSS: %v", err),
			}, err
		}

		_, err = poller.PollUntilDone(ctx.GetContext(), nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to wait for VMSS scaling: %v", err),
			}, err
		}

		logger.Info("successfully scaled VMSS", "vmssName", vmssName, "capacity", capacity)
		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("successfully scaled VMSS '%s' to capacity %d", vmssName, capacity),
		}, nil

	case "delete_vmss":
		// Delete the VMSS
		logger.Info("applying command: deleting VMSS", "vmssName", vmssName)
		poller, err := client.BeginDelete(ctx.GetContext(), resourceGroup, vmssName, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to delete VMSS: %v", err),
			}, err
		}
		_, err = poller.PollUntilDone(ctx.GetContext(), nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to wait for VMSS deletion: %v", err),
			}, err
		}
		logger.Info("successfully deleted VMSS", "vmssName", vmssName)
		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("successfully deleted VMSS '%s'", vmssName),
		}, nil

	default:
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("unknown command: %s", command.Command),
		}, fmt.Errorf("unknown command: %s", command.Command)
	}
}

func (s *virtualMachineScaleSetService) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
	cred, _, err := getAzureCredsForAccount(ctx, account)
	if err != nil {
		return "", fmt.Errorf("failed to create azure credential: %w", err)
	}

	_, err = extractSubscriptionID(resourceId)
	if err != nil {
		return "", fmt.Errorf("failed to extract subscription id from resource id: %w", err)
	}

	client, err := armmonitor.NewDiagnosticSettingsClient(cred, getAzureAuditOpts(ctx))
	if err != nil {
		return "", fmt.Errorf("failed to create diagnostic settings client: %w", err)
	}

	pager := client.NewListPager(resourceId, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx.GetContext())
		if err != nil {
			return "", fmt.Errorf("failed to get next page of diagnostic settings: %w", err)
		}

		for _, setting := range page.Value {
			if setting.Properties != nil && setting.Properties.WorkspaceID != nil && *setting.Properties.WorkspaceID != "" {
				return *setting.Properties.WorkspaceID, nil
			}
		}
	}

	return "", errors.New("log group name not found")
}

func (s *virtualMachineScaleSetService) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, resource providers.Resource) (providers.ServiceMapApplication, error) {
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

	if properties, ok := resource.Meta["properties"].(map[string]interface{}); ok {
		if vmProfile, ok := properties["virtualMachineProfile"].(map[string]interface{}); ok {
			if networkProfile, ok := vmProfile["networkProfile"].(map[string]interface{}); ok {
				if networkInterfaceConfigurations, ok := networkProfile["networkInterfaceConfigurations"].([]interface{}); ok {
					for _, nicConfig := range networkInterfaceConfigurations {
						if nicConfigMap, ok := nicConfig.(map[string]interface{}); ok {
							nicProps, _ := nicConfigMap["properties"].(map[string]interface{})
							if ipConfigurations, ok := nicProps["ipConfigurations"].([]interface{}); ok {
								for _, ipConfig := range ipConfigurations {
									if ipConfigMap, ok := ipConfig.(map[string]interface{}); ok {
										ipProps, _ := ipConfigMap["properties"].(map[string]interface{})
										if subnet, ok := ipProps["subnet"].(map[string]interface{}); ok {
											if id, ok := subnet["id"].(string); ok {
												app.Downstreams = append(app.Downstreams, providers.ServiceApplicationLink{
													Id: providers.ServiceApplicationId{
														Name:      id,
														Kind:      "Microsoft.Network/virtualNetworks/subnets",
														Namespace: resource.Region,
													},
												}.ToDownstreamLink())
											}
										}
									}
								}
							}
						}
					}
				}
			}
		}
	}
	return app, nil
}

func (s *virtualMachineScaleSetService) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAzureMonitorMetrics(ctx, account, filter)
}
