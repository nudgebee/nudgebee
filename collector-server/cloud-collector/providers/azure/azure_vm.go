package azure

import (
	"context"
	"errors"
	"fmt"
	"nudgebee/collector/cloud/providers"
	"nudgebee/collector/cloud/providers/constants"
	"strconv"
	"strings"

	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
)

type virtualMachineService struct {
}

// parseAzureVMResourceID extracts (subscription, resourceGroup, vmName) from an
// Azure VM resource ID. Azure resource IDs are case-insensitive, so segment
// names are matched with EqualFold. Returns empty strings for any segment that
// is missing — callers must check before using the result.
func parseAzureVMResourceID(resourceID string) (subscriptionID, resourceGroup, vmName string) {
	parts := strings.Split(resourceID, "/")
	for i, part := range parts {
		if i+1 >= len(parts) {
			break
		}
		switch {
		case strings.EqualFold(part, "subscriptions"):
			subscriptionID = parts[i+1]
		case strings.EqualFold(part, "resourceGroups"):
			resourceGroup = parts[i+1]
		case strings.EqualFold(part, "virtualMachines"):
			vmName = parts[i+1]
		}
	}
	return subscriptionID, resourceGroup, vmName
}

func (s *virtualMachineService) Name() string {
	return "Microsoft.Compute/virtualMachines"
}

// Scope returns the service scope - this is a regional service
func (s *virtualMachineService) Scope() ServiceScope {
	return ServiceScopeRegional
}

func (s *virtualMachineService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
	cred, session, err := getAzureCredsForAccount(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("failed to create azure credential: %w", err)
	}

	var allResources []providers.Resource
	var subscriptionIDs = strings.Split(session.SubscriptionID, ",")

	// Fetch VM capabilities from Azure API (similar to AWS DescribeInstanceTypes)
	// This gives us real vCPU, cores, threads data - no more hardcoded assumptions!
	vmCapabilitiesMap, err := fetchVMCapabilities(ctx, account, region)
	if err != nil {
		ctx.GetLogger().Debug("azure: failed to fetch VM capabilities from API, will use fallback",
			"error", err, "region", region)
		// Don't fail - we'll fall back to static map
		vmCapabilitiesMap = nil
	}
	for _, subID := range subscriptionIDs {
		if strings.TrimSpace(subID) == "" {
			continue
		}
		vmClient, err := armcompute.NewVirtualMachinesClient(subID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to create vm client: %w", err)
		}

		startIdx := len(allResources)
		statusOnly := "false"
		pager := vmClient.NewListAllPager(&armcompute.VirtualMachinesClientListAllOptions{
			StatusOnly: &statusOnly,
		})

		for pager.More() {
			page, err := pager.NextPage(ctx.GetContext())
			if err != nil {
				return nil, fmt.Errorf("failed to get next page: %w", err)
			}
			for _, vm := range page.Value {
				status := providers.ResourceStatusUnknown
				if vm.Properties.ProvisioningState != nil {
					if val, ok := nbStatusFromAzureProvisioningState[*vm.Properties.ProvisioningState]; ok {
						status = val
					}
				}

				// Override status based on VM power state from instance view
				powerState := extractVMPowerState(vm.Properties.InstanceView)
				if ps := vmPowerStateToStatus(powerState); ps != "" {
					status = ps
				}

				createdAt := time.Time{}
				if vm.Properties.TimeCreated != nil {
					createdAt = *vm.Properties.TimeCreated
				}

				meta := structToMap(vm)
				if powerState != "" {
					meta["powerState"] = powerState
				}

				// Add InstanceTypeDetails using Azure API data (or fallback to static map)
				// This matches AWS format exactly for UI compatibility
				if vm.Properties != nil && vm.Properties.HardwareProfile != nil && vm.Properties.HardwareProfile.VMSize != nil {
					vmSize := string(*vm.Properties.HardwareProfile.VMSize)
					var instanceTypeDetails map[string]interface{}

					// PRIORITY 1: Try Azure API data (best - real data from Azure!)
					if vmCapabilitiesMap != nil {
						if capabilities, ok := vmCapabilitiesMap[vmSize]; ok {
							instanceTypeDetails = parseVMCapabilities(vmSize, capabilities)
							ctx.GetLogger().Debug("azure: using API data for VM capabilities",
								"vmSize", vmSize,
								"vCPUs", capabilities["vCPUs"],
								"vCPUsPerCore", capabilities["vCPUsPerCore"])
						}
					}

					// PRIORITY 2: Fallback to static map (acceptable - covers common VM sizes)
					if instanceTypeDetails == nil {
						if specs := getVMSpecsFromStaticMap(vmSize); specs != nil {
							vcpuCount, _ := toInt(specs["vcpu"])
							memoryGiB, _ := toFloat64(specs["memory_gib"])

							// Static map doesn't have threads_per_core, so we estimate
							// Azure VMs typically have 2 threads per core (hyperthreading)
							defaultThreadsPerCore := 2
							defaultCores := vcpuCount / defaultThreadsPerCore
							if defaultCores < 1 {
								defaultCores = 1
								defaultThreadsPerCore = vcpuCount
							}

							instanceTypeDetails = map[string]interface{}{
								"VCpuInfo": map[string]interface{}{
									"DefaultVCpus":          vcpuCount,
									"DefaultCores":          defaultCores,
									"DefaultThreadsPerCore": defaultThreadsPerCore,
									"ValidCores":            []int{defaultCores},
									"ValidThreadsPerCore":   []int{1, 2},
								},
								"MemoryInfo": map[string]interface{}{
									"SizeInMiB": int(memoryGiB * 1024),
								},
								"InstanceType": vmSize,
							}

							ctx.GetLogger().Debug("azure: using static map fallback for VM capabilities",
								"vmSize", vmSize,
								"note", "API data preferred - static map is fallback only")
						}
					}

					if instanceTypeDetails != nil {
						meta["InstanceTypeDetails"] = instanceTypeDetails
					} else {
						ctx.GetLogger().Debug("azure: no VM capabilities found for size",
							"vmSize", vmSize)
					}
				}

				allResources = append(allResources, providers.Resource{
					Id:          *vm.ID,
					Name:        *vm.Name,
					Type:        *vm.Type,
					Region:      *vm.Location,
					Tags:        toAzureTags(vm.Tags),
					Meta:        meta,
					Status:      status,
					CreatedAt:   createdAt,
					Arn:         *vm.ID,
					ServiceName: s.Name(),
				})
			}
		}

		// Enrich VMs with power state via individual Get calls with InstanceView expand
		expand := armcompute.InstanceViewTypesInstanceView
		for i := startIdx; i < len(allResources); i++ {
			rg, err := extractResourceGroup(allResources[i].Id)
			if err != nil {
				continue
			}
			resp, err := vmClient.Get(ctx.GetContext(), rg, allResources[i].Name,
				&armcompute.VirtualMachinesClientGetOptions{Expand: &expand})
			if err != nil {
				ctx.GetLogger().Debug("azure: failed to get VM instance view",
					"vm", allResources[i].Name, "error", err)
				continue
			}
			if resp.Properties == nil {
				continue
			}
			powerState := extractVMPowerState(resp.Properties.InstanceView)
			if powerState != "" {
				if allResources[i].Meta == nil {
					allResources[i].Meta = make(map[string]any)
				}
				allResources[i].Meta["powerState"] = powerState
				if ps := vmPowerStateToStatus(powerState); ps != "" {
					allResources[i].Status = ps
				}
			}
		}
	}

	return allResources, nil
}

func (s *virtualMachineService) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAzureMonitorMetrics(ctx, account, filter)
}

func (s *virtualMachineService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	var allRecommendations []providers.Recommendation
	for _, resource := range existingResources {
		resourceRecommendations := make(map[string]providers.Recommendation)

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
					"reason":          "Virtual Machine has no tags applied. Tags are essential for cost allocation, resource organization, compliance tracking, and automated management policies.",
					"benefits": []string{
						"Better cost tracking and allocation",
						"Easier resource organization and filtering",
						"Compliance and governance enforcement",
						"Automated policy application",
						"Team ownership identification",
					},
					"recommended_tags": []string{"environment", "owner", "cost-center", "application", "project"},
				},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			}
		}

		properties, _ := resource.Meta["properties"].(map[string]interface{})

		// Check for Boot Diagnostics
		if diagnosticsProfile, ok := properties["diagnosticsProfile"].(map[string]interface{}); ok {
			if bootDiagnostics, ok := diagnosticsProfile["bootDiagnostics"].(map[string]interface{}); ok {
				if enabled, ok := bootDiagnostics["enabled"].(bool); !ok || !enabled {
					resourceRecommendations[constants.AzureVMBootDiagnosticsDisabled] = providers.Recommendation{
						CategoryName: providers.RecommendationCategoryConfiguration,
						RuleName:     constants.AzureVMBootDiagnosticsDisabled,
						Severity:     providers.RecommendationSeverityMedium,
						Savings:      0,
						Data: map[string]any{
							"resource_id":     resource.Id,
							"resource_name":   resource.Name,
							"resource_type":   resource.Type,
							"resource_region": resource.Region,
							"service_name":    resource.ServiceName,
							"reason":          "Boot diagnostics is disabled. Enabling boot diagnostics provides console output and screenshots for troubleshooting VM boot issues, critical for production environments.",
							"benefits": []string{
								"Console output for boot troubleshooting",
								"Screenshot capture during boot failures",
								"Faster root cause analysis",
								"Better incident response capability",
								"Required for Azure support diagnostics",
							},
							"cost_impact": "Minimal storage cost for diagnostic logs",
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

		// Check for System-Assigned Managed Identity
		if identity, ok := resource.Meta["identity"].(map[string]interface{}); ok {
			if identityType, ok := identity["type"].(string); ok && !strings.Contains(strings.ToLower(identityType), "systemassigned") {
				resourceRecommendations[constants.AzureVMSystemAssignedIdentityDisabled] = providers.Recommendation{
					CategoryName: providers.RecommendationCategorySecurity,
					RuleName:     constants.AzureVMSystemAssignedIdentityDisabled,
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
						"reason":               "Virtual Machine does not have system-assigned managed identity enabled. Managed identities eliminate the need to store credentials in code or configuration, improving security posture significantly.",
						"benefits": []string{
							"No credentials stored in code or configuration",
							"Automatic credential rotation",
							"Secure access to Azure Key Vault",
							"Native Azure RBAC integration",
							"Reduced credential management overhead",
							"Better compliance with security standards",
						},
						"use_cases": []string{"Azure Key Vault access", "Azure Storage access", "Azure SQL authentication", "Service-to-service authentication"},
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				}
			}
		}

		// Check for Managed Disks
		if storageProfile, ok := properties["storageProfile"].(map[string]interface{}); ok {
			if osDisk, ok := storageProfile["osDisk"].(map[string]interface{}); ok {
				if _, ok := osDisk["managedDisk"]; !ok {
					resourceRecommendations[constants.AzureVMUnmanagedDisk] = providers.Recommendation{
						CategoryName: providers.RecommendationCategoryInfraUpgrade,
						RuleName:     constants.AzureVMUnmanagedDisk,
						Severity:     providers.RecommendationSeverityHigh,
						Savings:      20.0, // Managed disks eliminate storage account costs and management overhead
						Data: map[string]any{
							"resource_id":           resource.Id,
							"resource_name":         resource.Name,
							"resource_type":         resource.Type,
							"resource_region":       resource.Region,
							"service_name":          resource.ServiceName,
							"current_disk_type":     "Unmanaged Disk",
							"recommended_disk_type": "Managed Disk",
							"reason":                "VM is using unmanaged disks (deprecated). Managed disks provide better reliability, simpler management, higher SLA (99.999%), and eliminate storage account limits. Microsoft recommends migrating all VMs to managed disks.",
							"benefits": []string{
								"99.999% availability SLA",
								"No storage account limits (20,000 IOPS)",
								"Simpler backup and disaster recovery",
								"Better scalability (50,000 disks per region)",
								"Integrated with Azure Backup",
								"Snapshot and image support",
								"Eliminate storage account management",
							},
							"estimated_savings_type": "monthly",
							"savings_source":         "Eliminates storage account management costs and overhead",
							"migration_required":     true,
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

		// Check for Accelerated Networking
		if networkProfile, ok := properties["networkProfile"].(map[string]interface{}); ok {
			if networkInterfaces, ok := networkProfile["networkInterfaces"].([]interface{}); ok {
				for _, nic := range networkInterfaces {
					if nicMap, ok := nic.(map[string]interface{}); ok {
						if properties, ok := nicMap["properties"].(map[string]interface{}); ok {
							if enableAcceleratedNetworking, ok := properties["enableAcceleratedNetworking"].(bool); !ok || !enableAcceleratedNetworking {
								resourceRecommendations[constants.AzureVMAcceleratedNetworkingDisabled] = providers.Recommendation{
									CategoryName: providers.RecommendationCategoryInfraUpgrade,
									RuleName:     constants.AzureVMAcceleratedNetworkingDisabled,
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
										"reason":                 "Accelerated Networking is disabled. Enabling it provides up to 30 Gbps network throughput, lower latency (<1ms jitter), and reduced CPU utilization at no additional cost for supported VM sizes.",
										"benefits": []string{
											"Up to 30 Gbps network throughput",
											"Lower latency and jitter",
											"Reduced CPU utilization",
											"Better packet-per-second performance",
											"No additional cost",
											"Hardware-based network virtualization (SR-IOV)",
										},
										"performance_improvement": "Up to 10x network performance improvement",
										"requirement":             "VM must support Accelerated Networking (most general-purpose VMs D/E/F/G/H/L series)",
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

		// Check for Automatic OS Upgrades
		if osProfile, ok := properties["osProfile"].(map[string]interface{}); ok {
			automaticOSUpgradeDisabled := false
			if windowsConfiguration, ok := osProfile["windowsConfiguration"].(map[string]interface{}); ok {
				if automaticOSUpgradePolicy, ok := windowsConfiguration["automaticOSUpgradePolicy"].(map[string]interface{}); ok {
					if enableAutomaticOSUpgrade, ok := automaticOSUpgradePolicy["enableAutomaticOSUpgrade"].(bool); !ok || !enableAutomaticOSUpgrade {
						automaticOSUpgradeDisabled = true
					}
				}
			}
			if linuxConfiguration, ok := osProfile["linuxConfiguration"].(map[string]interface{}); ok {
				if automaticOSUpgradePolicy, ok := linuxConfiguration["automaticOSUpgradePolicy"].(map[string]interface{}); ok {
					if enableAutomaticOSUpgrade, ok := automaticOSUpgradePolicy["enableAutomaticOSUpgrade"].(bool); !ok || !enableAutomaticOSUpgrade {
						automaticOSUpgradeDisabled = true
					}
				}
			}
			if automaticOSUpgradeDisabled {
				resourceRecommendations[constants.AzureVMAutomaticOSUpgradeDisabled] = providers.Recommendation{
					CategoryName: providers.RecommendationCategorySecurity,
					RuleName:     constants.AzureVMAutomaticOSUpgradeDisabled,
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
						"reason":                        "Automatic OS upgrades are disabled. Enabling automatic OS upgrades ensures your VM receives critical security patches and updates automatically, reducing vulnerability exposure and manual maintenance overhead.",
						"benefits": []string{
							"Automatic security patch deployment",
							"Reduced vulnerability exposure window",
							"Lower manual maintenance burden",
							"Compliance with security policies",
							"Minimized downtime from security incidents",
							"Coordinated rolling updates for VM scale sets",
						},
						"security_impact": "Critical security patches applied automatically within hours of release",
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				}
			}
		}

		// Check for Endpoint Protection Extension
		endpointProtectionFound := false
		if resources, ok := resource.Meta["resources"].([]interface{}); ok {
			for _, res := range resources {
				if resMap, ok := res.(map[string]interface{}); ok {
					if typeVal, ok := resMap["type"].(string); ok && typeVal == "Microsoft.Compute/virtualMachines/extensions" {
						if properties, ok := resMap["properties"].(map[string]interface{}); ok {
							if publisher, ok := properties["publisher"].(string); ok && publisher == "Microsoft.Azure.Security" {
								if typeHandlerVersion, ok := properties["typeHandlerVersion"].(string); ok && strings.HasPrefix(typeHandlerVersion, "IaaSAntimalware") {
									endpointProtectionFound = true
									break
								}
							}
						}
					}
				}
			}
		}
		if !endpointProtectionFound {
			resourceRecommendations[constants.AzureVMEndpointProtectionMissing] = providers.Recommendation{
				CategoryName: providers.RecommendationCategorySecurity,
				RuleName:     constants.AzureVMEndpointProtectionMissing,
				Severity:     providers.RecommendationSeverityHigh,
				Savings:      0,
				Data: map[string]any{
					"resource_id":           resource.Id,
					"resource_name":         resource.Name,
					"resource_type":         resource.Type,
					"resource_region":       resource.Region,
					"service_name":          resource.ServiceName,
					"current_protection":    "None",
					"recommended_extension": "Microsoft.Azure.Security.IaaSAntimalware",
					"reason":                "Endpoint Protection extension (antimalware) is not installed on this VM. Installing Microsoft Antimalware provides real-time protection against viruses, spyware, and other malicious software, essential for protecting workloads and meeting compliance requirements.",
					"benefits": []string{
						"Real-time malware detection and removal",
						"Scheduled scanning capabilities",
						"Protection against ransomware and threats",
						"Compliance with security standards (PCI DSS, HIPAA)",
						"Cloud-optimized antimalware engine",
						"Automatic signature updates",
						"Quarantine and remediation capabilities",
					},
					"compliance_impact": "Required for PCI DSS, HIPAA, and SOC 2 compliance",
					"threat_types":      []string{"viruses", "spyware", "rootkits", "ransomware", "trojans"},
				},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			}
		}

		// Check for Guest-Level Diagnostics Extension
		guestDiagnosticsExtensionFound := false
		if resources, ok := resource.Meta["resources"].([]interface{}); ok {
			for _, res := range resources {
				if resMap, ok := res.(map[string]interface{}); ok {
					if typeVal, ok := resMap["type"].(string); ok && typeVal == "Microsoft.Compute/virtualMachines/extensions" {
						if properties, ok := resMap["properties"].(map[string]interface{}); ok {
							if publisher, ok := properties["publisher"].(string); ok && publisher == "Microsoft.Azure.Diagnostics" {
								guestDiagnosticsExtensionFound = true
								break
							}
						}
					}
				}
			}
		}
		if !guestDiagnosticsExtensionFound {
			resourceRecommendations[constants.AzureVMGuestLevelDiagnosticsMissing] = providers.Recommendation{
				CategoryName: providers.RecommendationCategoryConfiguration,
				RuleName:     constants.AzureVMGuestLevelDiagnosticsMissing,
				Severity:     providers.RecommendationSeverityMedium,
				Savings:      0,
				Data: map[string]any{
					"resource_id":           resource.Id,
					"resource_name":         resource.Name,
					"resource_type":         resource.Type,
					"resource_region":       resource.Region,
					"service_name":          resource.ServiceName,
					"current_diagnostics":   "None",
					"recommended_extension": "Microsoft.Azure.Diagnostics.LinuxDiagnostic or IaaSDiagnostics",
					"reason":                "Guest-level diagnostics extension is not installed. This extension provides deep visibility into VM performance metrics, application logs, and system health from inside the guest OS, essential for troubleshooting and performance monitoring.",
					"benefits": []string{
						"Guest OS performance counters (CPU, memory, disk I/O)",
						"Application event logs and system logs",
						"Custom performance counters",
						".NET application performance tracking",
						"Crash dump collection and analysis",
						"Integration with Azure Monitor and Log Analytics",
						"Historical performance data retention",
					},
					"metrics_collected": []string{"CPU usage", "memory usage", "disk I/O", "network throughput", "process metrics"},
					"cost_impact":       "Minimal storage cost for diagnostic data (~$1-5/month)",
				},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			}
		}

		// Check for Customer Managed Keys for Virtual Hard Disk Encryption
		if storageProfile, ok := properties["storageProfile"].(map[string]interface{}); ok {
			if osDisk, ok := storageProfile["osDisk"].(map[string]interface{}); ok {
				if managedDisk, ok := osDisk["managedDisk"].(map[string]interface{}); ok {
					if _, ok := managedDisk["diskEncryptionSet"]; !ok {
						resourceRecommendations[constants.AzureVMDiskEncryptionCMKMissing] = providers.Recommendation{
							CategoryName: providers.RecommendationCategorySecurity,
							RuleName:     constants.AzureVMDiskEncryptionCMKMissing,
							Severity:     providers.RecommendationSeverityHigh,
							Savings:      0,
							Data: map[string]any{
								"resource_id":            resource.Id,
								"resource_name":          resource.Name,
								"resource_type":          resource.Type,
								"resource_region":        resource.Region,
								"service_name":           resource.ServiceName,
								"current_encryption":     "Platform Managed Keys (PMK)",
								"recommended_encryption": "Customer Managed Keys (CMK)",
								"reason":                 "OS disk is using platform-managed encryption keys instead of customer-managed keys. Customer-managed keys provide enhanced security control, compliance capabilities, and the ability to revoke access to encrypted data at any time.",
								"benefits": []string{
									"Full control over encryption key lifecycle",
									"Ability to revoke access to data instantly",
									"Key rotation on your schedule",
									"Compliance with regulatory requirements (HIPAA, PCI DSS, GDPR)",
									"Integration with Azure Key Vault",
									"Audit trail for key access",
									"Cross-region key replication control",
								},
								"key_vault_required": true,
								"compliance_impact":  "Required for HIPAA, PCI DSS Level 1, and GDPR compliance",
								"encryption_type":    "AES-256",
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

		// Check for SSH Password Authentication for Linux VMs
		if osProfile, ok := properties["osProfile"].(map[string]interface{}); ok {
			if linuxConfiguration, ok := osProfile["linuxConfiguration"].(map[string]interface{}); ok {
				if disablePasswordAuthentication, ok := linuxConfiguration["disablePasswordAuthentication"].(bool); ok && !disablePasswordAuthentication {
					resourceRecommendations[constants.AzureVMSSHPasswordAuthenticationEnabled] = providers.Recommendation{
						CategoryName: providers.RecommendationCategorySecurity,
						RuleName:     constants.AzureVMSSHPasswordAuthenticationEnabled,
						Severity:     providers.RecommendationSeverityHigh,
						Savings:      0,
						Data: map[string]any{
							"resource_id":                resource.Id,
							"resource_name":              resource.Name,
							"resource_type":              resource.Type,
							"resource_region":            resource.Region,
							"service_name":               resource.ServiceName,
							"current_authentication":     "SSH Password Authentication",
							"recommended_authentication": "SSH Key-Based Authentication",
							"reason":                     "SSH password authentication is enabled on this Linux VM. Password-based authentication is vulnerable to brute-force attacks, dictionary attacks, and credential stuffing. SSH key-based authentication provides cryptographically strong authentication and is the industry standard for Linux VMs.",
							"benefits": []string{
								"Protection against brute-force attacks",
								"Immune to password dictionary attacks",
								"No credential stuffing vulnerability",
								"Cryptographically strong authentication (2048-4096 bit RSA)",
								"Eliminates password management overhead",
								"Supports automated deployments securely",
								"Required for PCI DSS and SOC 2 compliance",
							},
							"security_risks":    []string{"brute force attacks", "credential stuffing", "password reuse", "weak password selection"},
							"compliance_impact": "SSH key authentication required for PCI DSS, SOC 2, and CIS benchmarks",
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

		// Check for Premium SSD for OS Disk
		if storageProfile, ok := properties["storageProfile"].(map[string]interface{}); ok {
			if osDisk, ok := storageProfile["osDisk"].(map[string]interface{}); ok {
				if managedDisk, ok := osDisk["managedDisk"].(map[string]interface{}); ok {
					if storageAccountType, ok := managedDisk["storageAccountType"].(string); ok && strings.Contains(strings.ToUpper(storageAccountType), "PREMIUM") {
						var savings float64
						var diskSizeGB float64
						if size, err := toFloat64(osDisk["diskSizeGB"]); err == nil {
							diskSizeGB = size
							premiumCost := getDiskMonthlyCost(storageAccountType, diskSizeGB)
							standardCost := getDiskMonthlyCost("StandardSSD_LRS", diskSizeGB)
							if premiumCost > 0 && standardCost > 0 {
								savings = premiumCost - standardCost
							}
						}
						resourceRecommendations[constants.AzureVMPremiumSSDOSDiskUsed] = providers.Recommendation{
							CategoryName: providers.RecommendationCategoryRightSizing,
							RuleName:     constants.AzureVMPremiumSSDOSDiskUsed,
							Severity:     providers.RecommendationSeverityLow,
							Savings:      savings,
							Data: map[string]any{
								"resource_id":          resource.Id,
								"resource_name":        resource.Name,
								"resource_type":        resource.Type,
								"resource_region":      resource.Region,
								"service_name":         resource.ServiceName,
								"current_disk_sku":     "Premium_LRS",
								"disk_size_gb":         diskSizeGB,
								"recommended_disk_sku": "StandardSSD_LRS",
								"reason":               "OS disk is using Premium SSD which is typically over-provisioned for OS workloads. For most VMs, Standard SSD provides sufficient performance for OS operations (500 IOPS, 60 MBps) at significantly lower cost. Premium SSD is better suited for data disks with high I/O workloads.",
								"benefits": []string{
									"40-50% cost reduction for OS disk",
									"Standard SSD sufficient for OS boot and system operations",
									"Maintains 99.9% SLA",
									"No performance impact for typical OS workloads",
									"Keep Premium SSD for data disks where needed",
								},
								"estimated_savings_type": "monthly",
								"savings_source":         "Premium SSD vs Standard SSD pricing difference",
								"performance_note":       "Standard SSD provides 500 IOPS and 60 MBps - adequate for OS disk",
								"workload_suitability":   "OS boot, system files, and application binaries",
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

		// Check for Confidential Computing
		if securityProfile, ok := properties["securityProfile"].(map[string]interface{}); ok {
			if securityType, ok := securityProfile["securityType"].(string); ok && securityType != "ConfidentialVM" {
				resourceRecommendations[constants.AzureVMConfidentialComputingDisabled] = providers.Recommendation{
					CategoryName: providers.RecommendationCategorySecurity,
					RuleName:     constants.AzureVMConfidentialComputingDisabled,
					Severity:     providers.RecommendationSeverityHigh,
					Savings:      0,
					Data: map[string]any{
						"resource_id":               resource.Id,
						"resource_name":             resource.Name,
						"resource_type":             resource.Type,
						"resource_region":           resource.Region,
						"service_name":              resource.ServiceName,
						"current_security_type":     securityType,
						"recommended_security_type": "ConfidentialVM",
						"reason":                    "Confidential Computing is not enabled for this VM. Azure Confidential VMs provide hardware-based memory encryption using AMD SEV-SNP or Intel TDX technology, protecting data in use from cloud operators, malicious administrators, and host-level attacks. Essential for highly sensitive workloads.",
						"benefits": []string{
							"Memory encryption with hardware-based TEE (Trusted Execution Environment)",
							"Protection from cloud operator access",
							"Defense against malicious administrators",
							"Protection from hypervisor and host kernel attacks",
							"Confidential disk encryption with VM-specific keys",
							"Attestation and verification capabilities",
							"Regulatory compliance (healthcare, financial services)",
						},
						"use_cases":         []string{"healthcare PHI data", "financial transactions", "PII processing", "intellectual property protection", "multi-party computation"},
						"technology":        "AMD SEV-SNP or Intel TDX",
						"compliance_impact": "Required for HITRUST, financial services, and sensitive government workloads",
						"vm_requirement":    "DCasv5, DCadsv5, or ECasv5 series VMs",
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				}
			}
		}

		// Check for Trusted Launch
		if securityProfile, ok := properties["securityProfile"].(map[string]interface{}); ok {
			if securityType, ok := securityProfile["securityType"].(string); ok && securityType != "TrustedLaunch" {
				resourceRecommendations[constants.AzureVMTrustedLaunchDisabled] = providers.Recommendation{
					CategoryName: providers.RecommendationCategorySecurity,
					RuleName:     constants.AzureVMTrustedLaunchDisabled,
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
						"reason":                    "Trusted Launch is not enabled for this VM. Trusted Launch protects against boot kits, rootkits, and kernel-level malware using Secure Boot and vTPM (virtual Trusted Platform Module). This is a foundational security feature for Gen2 VMs and should be enabled by default for production workloads.",
						"benefits": []string{
							"Secure Boot validation of boot chain integrity",
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

		// Check for Virtual Machine Access using Microsoft Entra ID Authentication
		if osProfile, ok := properties["osProfile"].(map[string]interface{}); ok {
			if allowExtensionOperations, ok := osProfile["allowExtensionOperations"].(bool); !ok || !allowExtensionOperations {
				resourceRecommendations[constants.AzureVMEntraIDAuthenticationDisabled] = providers.Recommendation{
					CategoryName: providers.RecommendationCategorySecurity,
					RuleName:     constants.AzureVMEntraIDAuthenticationDisabled,
					Severity:     providers.RecommendationSeverityMedium,
					Savings:      0,
					Data: map[string]any{
						"resource_id":                resource.Id,
						"resource_name":              resource.Name,
						"resource_type":              resource.Type,
						"resource_region":            resource.Region,
						"service_name":               resource.ServiceName,
						"current_authentication":     "Local accounts or extension operations disabled",
						"recommended_authentication": "Microsoft Entra ID (Azure AD) Authentication",
						"reason":                     "VM is not configured for Microsoft Entra ID (Azure AD) authentication. Entra ID authentication enables centralized identity management, conditional access policies, multi-factor authentication, and eliminates local account management overhead. Essential for enterprise security and compliance.",
						"benefits": []string{
							"Centralized identity management with Entra ID",
							"Multi-factor authentication (MFA) enforcement",
							"Conditional access policies (location, device compliance)",
							"Role-based access control (RBAC) integration",
							"Passwordless authentication support",
							"Single sign-on (SSO) capabilities",
							"Comprehensive audit logs in Entra ID",
							"Eliminates local account credential management",
						},
						"authentication_methods": []string{"Azure AD credentials", "Passwordless (FIDO2, Windows Hello)", "Smart card", "MFA"},
						"compliance_impact":      "Required for enterprise security policies and Zero Trust architecture",
						"use_cases":              []string{"SSH login with Azure AD", "RDP login with Azure AD", "Conditional access enforcement", "MFA requirement"},
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				}
			}
		}

		// Check for Boot Disk Encryption
		if storageProfile, ok := properties["storageProfile"].(map[string]interface{}); ok {
			if osDisk, ok := storageProfile["osDisk"].(map[string]interface{}); ok {
				if encryptionSettings, ok := osDisk["encryptionSettings"].(map[string]interface{}); ok {
					if enabled, ok := encryptionSettings["enabled"].(bool); !ok || !enabled {
						resourceRecommendations[constants.AzureVMBootDiskEncryptionDisabled] = providers.Recommendation{
							CategoryName: providers.RecommendationCategorySecurity,
							RuleName:     constants.AzureVMBootDiskEncryptionDisabled,
							Severity:     providers.RecommendationSeverityHigh,
							Savings:      0,
							Data: map[string]any{
								"resource_id":            resource.Id,
								"resource_name":          resource.Name,
								"resource_type":          resource.Type,
								"resource_region":        resource.Region,
								"service_name":           resource.ServiceName,
								"current_encryption":     "Disabled",
								"recommended_encryption": "Azure Disk Encryption (ADE) or Server-Side Encryption",
								"reason":                 "Boot disk encryption is disabled. Enabling disk encryption protects data at rest from unauthorized physical access, theft, or data breaches. Azure Disk Encryption uses industry-standard BitLocker (Windows) or dm-crypt (Linux) for full disk encryption.",
								"benefits": []string{
									"Protection against physical disk theft",
									"Compliance with data protection regulations (GDPR, HIPAA)",
									"AES-256 encryption at rest",
									"Integration with Azure Key Vault for key management",
									"No performance impact with server-side encryption",
									"Meets PCI DSS and SOC 2 requirements",
									"Automatic encryption of snapshots and images",
								},
								"encryption_methods": []string{"Azure Disk Encryption (ADE)", "Server-Side Encryption with PMK", "Server-Side Encryption with CMK"},
								"compliance_impact":  "Required for GDPR, HIPAA, PCI DSS, and SOC 2 compliance",
								"cost_impact":        "No additional cost for server-side encryption",
							},
							Action:              providers.RecommendationActionModify,
							ResourceServiceName: resource.ServiceName,
							ResourceId:          resource.Id,
							ResourceType:        resource.Type,
							ResourceRegion:      resource.Region,
						}
					}
				} else {
					resourceRecommendations[constants.AzureVMBootDiskEncryptionDisabled] = providers.Recommendation{
						CategoryName: providers.RecommendationCategorySecurity,
						RuleName:     constants.AzureVMBootDiskEncryptionDisabled,
						Severity:     providers.RecommendationSeverityHigh,
						Savings:      0,
						Data: map[string]any{
							"resource_id":            resource.Id,
							"resource_name":          resource.Name,
							"resource_type":          resource.Type,
							"resource_region":        resource.Region,
							"service_name":           resource.ServiceName,
							"current_encryption":     "Not configured",
							"recommended_encryption": "Azure Disk Encryption (ADE) or Server-Side Encryption",
							"reason":                 "Boot disk encryption settings not found. Enabling disk encryption protects data at rest from unauthorized physical access, theft, or data breaches. Azure Disk Encryption uses industry-standard BitLocker (Windows) or dm-crypt (Linux) for full disk encryption.",
							"benefits": []string{
								"Protection against physical disk theft",
								"Compliance with data protection regulations (GDPR, HIPAA)",
								"AES-256 encryption at rest",
								"Integration with Azure Key Vault for key management",
								"No performance impact with server-side encryption",
								"Meets PCI DSS and SOC 2 requirements",
								"Automatic encryption of snapshots and images",
							},
							"encryption_methods": []string{"Azure Disk Encryption (ADE)", "Server-Side Encryption with PMK", "Server-Side Encryption with CMK"},
							"compliance_impact":  "Required for GDPR, HIPAA, PCI DSS, and SOC 2 compliance",
							"cost_impact":        "No additional cost for server-side encryption",
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

		// Check for Non-Boot (Data) Disk Encryption
		if storageProfile, ok := properties["storageProfile"].(map[string]interface{}); ok {
			if dataDisks, ok := storageProfile["dataDisks"].([]interface{}); ok {
				for _, disk := range dataDisks {
					if diskMap, ok := disk.(map[string]interface{}); ok {
						if encryptionSettings, ok := diskMap["encryptionSettings"].(map[string]interface{}); ok {
							if enabled, ok := encryptionSettings["enabled"].(bool); !ok || !enabled {
								resourceRecommendations[constants.AzureVMDataDiskEncryptionDisabled] = providers.Recommendation{
									CategoryName: providers.RecommendationCategorySecurity,
									RuleName:     constants.AzureVMDataDiskEncryptionDisabled,
									Severity:     providers.RecommendationSeverityHigh,
									Savings:      0,
									Data: map[string]any{
										"resource_id":            resource.Id,
										"resource_name":          resource.Name,
										"resource_type":          resource.Type,
										"resource_region":        resource.Region,
										"service_name":           resource.ServiceName,
										"disk_name":              diskMap["name"],
										"current_encryption":     "Disabled",
										"recommended_encryption": "Azure Disk Encryption (ADE) or Server-Side Encryption",
										"reason":                 "Data disk encryption is disabled. Data disks often contain application data, databases, and sensitive business information. Enabling encryption protects this data at rest from unauthorized access, theft, or breaches.",
										"benefits": []string{
											"Protection of application and database data",
											"Compliance with data protection regulations",
											"AES-256 encryption at rest",
											"Integration with Azure Key Vault",
											"Automatic encryption of disk snapshots",
											"No application changes required",
											"Meets regulatory requirements (GDPR, HIPAA, PCI DSS)",
										},
										"encryption_methods": []string{"Azure Disk Encryption (ADE)", "Server-Side Encryption with PMK", "Server-Side Encryption with CMK"},
										"compliance_impact":  "Required for GDPR, HIPAA, and PCI DSS compliance",
										"cost_impact":        "No additional cost for server-side encryption",
									},
									Action:              providers.RecommendationActionModify,
									ResourceServiceName: resource.ServiceName,
									ResourceId:          resource.Id,
									ResourceType:        resource.Type,
									ResourceRegion:      resource.Region,
								}
							}
						} else {
							resourceRecommendations[constants.AzureVMDataDiskEncryptionDisabled] = providers.Recommendation{
								CategoryName: providers.RecommendationCategorySecurity,
								RuleName:     constants.AzureVMDataDiskEncryptionDisabled,
								Severity:     providers.RecommendationSeverityHigh,
								Savings:      0,
								Data: map[string]any{
									"resource_id":            resource.Id,
									"resource_name":          resource.Name,
									"resource_type":          resource.Type,
									"resource_region":        resource.Region,
									"service_name":           resource.ServiceName,
									"disk_name":              diskMap["name"],
									"current_encryption":     "Not configured",
									"recommended_encryption": "Azure Disk Encryption (ADE) or Server-Side Encryption",
									"reason":                 "Data disk encryption settings not found. Data disks often contain application data, databases, and sensitive business information. Enabling encryption protects this data at rest from unauthorized access, theft, or breaches.",
									"benefits": []string{
										"Protection of application and database data",
										"Compliance with data protection regulations",
										"AES-256 encryption at rest",
										"Integration with Azure Key Vault",
										"Automatic encryption of disk snapshots",
										"No application changes required",
										"Meets regulatory requirements (GDPR, HIPAA, PCI DSS)",
									},
									"encryption_methods": []string{"Azure Disk Encryption (ADE)", "Server-Side Encryption with PMK", "Server-Side Encryption with CMK"},
									"compliance_impact":  "Required for GDPR, HIPAA, and PCI DSS compliance",
									"cost_impact":        "No additional cost for server-side encryption",
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
		}

		// Check for Azure Monitor Extensions for Performance Diagnostics and Health Monitoring
		azureMonitorExtensionFound := false
		if resources, ok := resource.Meta["resources"].([]interface{}); ok {
			for _, res := range resources {
				if resMap, ok := res.(map[string]interface{}); ok {
					if typeVal, ok := resMap["type"].(string); ok && typeVal == "Microsoft.Compute/virtualMachines/extensions" {
						if properties, ok := resMap["properties"].(map[string]interface{}); ok {
							if publisher, ok := properties["publisher"].(string); ok && publisher == "Microsoft.Azure.Monitor" {
								if typeHandlerVersion, ok := properties["typeHandlerVersion"].(string); ok && (strings.HasPrefix(typeHandlerVersion, "AzureMonitorWindowsAgent") || strings.HasPrefix(typeHandlerVersion, "AzureMonitorLinuxAgent")) {
									azureMonitorExtensionFound = true
									break
								}
							}
						}
					}
				}
			}
		}
		if !azureMonitorExtensionFound {
			resourceRecommendations[constants.AzureVMMonitorAgentMissing] = providers.Recommendation{
				CategoryName: providers.RecommendationCategoryConfiguration,
				RuleName:     constants.AzureVMMonitorAgentMissing,
				Severity:     providers.RecommendationSeverityMedium,
				Savings:      10.0, // Data collection rule filtering can reduce Log Analytics ingestion costs by 20-30%
				Data: map[string]any{
					"resource_id":       resource.Id,
					"resource_name":     resource.Name,
					"resource_type":     resource.Type,
					"resource_region":   resource.Region,
					"service_name":      resource.ServiceName,
					"current_agent":     "None or Legacy Agent (MMA/OMS)",
					"recommended_agent": "Azure Monitor Agent (AMA)",
					"reason":            "Azure Monitor Agent (AMA) is not installed. AMA is the modern replacement for legacy agents (MMA/OMS) with advanced data filtering capabilities that reduce Log Analytics ingestion costs by 20-30% through Data Collection Rules. It also provides better security, performance, and centralized management.",
					"benefits": []string{
						"20-30% reduction in Log Analytics ingestion costs",
						"Centralized configuration via Data Collection Rules",
						"Enhanced security and performance vs legacy agents",
						"Cost optimization via selective data collection",
						"Unified agent for Windows and Linux",
						"Required for Azure Sentinel and VM Insights",
						"Multi-homing support (send to multiple workspaces)",
					},
					"estimated_savings_type": "monthly",
					"savings_source":         "Reduced Log Analytics data ingestion through filtering (~$2.50/GB saved)",
					"legacy_agent_note":      "Replace MMA (Microsoft Monitoring Agent) or OMS agent",
				},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			}
		}

		// Check for Just-In-Time Access
		if properties, ok := resource.Meta["properties"].(map[string]interface{}); ok {
			if _, ok := properties["protectionSettings"]; !ok {
				resourceRecommendations[constants.AzureVMJITAccessDisabled] = providers.Recommendation{
					CategoryName: providers.RecommendationCategorySecurity,
					RuleName:     constants.AzureVMJITAccessDisabled,
					Severity:     providers.RecommendationSeverityHigh,
					Savings:      0,
					Data: map[string]any{
						"resource_id":        resource.Id,
						"resource_name":      resource.Name,
						"resource_type":      resource.Type,
						"resource_region":    resource.Region,
						"service_name":       resource.ServiceName,
						"current_access":     "Standard Access",
						"recommended_access": "Just-In-Time (JIT) Access",
						"reason":             "Just-In-Time (JIT) VM access is not enabled. JIT reduces exposure to attacks by locking down management ports (SSH/RDP) and opening them only when needed for a limited time.",
						"benefits": []string{
							"Drastically reduces attack surface",
							"Prevents persistent access to management ports",
							"Audits all access requests",
							"Compliance with security standards",
							"Integration with Azure Firewall",
						},
						"security_impact": "Mitigates brute-force attacks and port scanning",
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				}
			}
		}

		// Check for Auto-Shutdown
		if _, ok := resource.Tags["autoshutdown-schedule"]; !ok {
			var savings float64
			if properties, ok := resource.Meta["properties"].(map[string]interface{}); ok {
				if hardwareProfile, ok := properties["hardwareProfile"].(map[string]interface{}); ok {
					if vmSize, ok := hardwareProfile["vmSize"].(string); ok {
						// Estimate savings by assuming the VM runs 24/7 but only needs to run 10 hours/day on weekdays.
						// (730 hours/month - (10 hours/day * 22 days/month)) / 730 hours/month * monthly_cost
						// This is approximately a 70% reduction in runtime.
						monthlyCost := getVMCost(ctx, vmSize)
						if monthlyCost > 0 {
							savings = monthlyCost * 0.70
						}
					}
				}
			}
			resourceRecommendations[constants.AzureVMAutoShutdownDisabled] = providers.Recommendation{
				CategoryName: providers.RecommendationCategoryRightSizing,
				RuleName:     constants.AzureVMAutoShutdownDisabled,
				Severity:     providers.RecommendationSeverityMedium,
				Savings:      savings,
				Data: map[string]any{
					"resource_id":          resource.Id,
					"resource_name":        resource.Name,
					"resource_type":        resource.Type,
					"resource_region":      resource.Region,
					"service_name":         resource.ServiceName,
					"current_schedule":     "None",
					"recommended_schedule": "Auto-Shutdown Schedule",
					"reason":               "Auto-shutdown is not configured (based on tags). Enabling auto-shutdown for non-production VMs prevents paying for compute resources during non-business hours.",
					"benefits": []string{
						"Significant cost savings for dev/test workloads",
						"Automated cost control",
						"Prevents accidental resource sprawl",
						"Configurable shutdown notifications",
					},
					"cost_impact": "Reduces run-time costs by up to 70% for business-hours-only VMs",
				},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			}
		}

		// Check for Backups
		if properties, ok := resource.Meta["properties"].(map[string]interface{}); ok {
			if _, ok := properties["backupSettings"]; !ok {
				resourceRecommendations[constants.AzureVMBackupDisabled] = providers.Recommendation{
					CategoryName: providers.RecommendationCategoryConfiguration,
					RuleName:     constants.AzureVMBackupDisabled,
					Severity:     providers.RecommendationSeverityHigh,
					Savings:      0,
					Data: map[string]any{
						"resource_id":        resource.Id,
						"resource_name":      resource.Name,
						"resource_type":      resource.Type,
						"resource_region":    resource.Region,
						"service_name":       resource.ServiceName,
						"current_backup":     "Disabled",
						"recommended_backup": "Azure Backup",
						"reason":             "Azure Backup is not enabled. Regular backups are critical for recovering from accidental deletion, data corruption, and ransomware attacks.",
						"benefits": []string{
							"Protection against ransomware",
							"Application-consistent backups",
							"Long-term retention compliance",
							"Centralized management",
							"Point-in-time restore capabilities",
						},
						"compliance_impact": "Required for Business Continuity and Disaster Recovery (BCDR)",
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				}
			}
		}

		// Check for idle VMs based on CPU utilization
		startDate := time.Now().Add(-time.Hour * 24 * 7)
		endDate := time.Now()

		var cpuMetrics providers.QueryMetricsResponse
		var errCpuMetrics error

		cpuMetrics, errCpuMetrics = s.QueryMetrices(ctx, account, providers.QueryMetricsRequest{
			ResourceIds: []string{resource.Id},
			ServiceName: resource.ServiceName,
			StartDate:   &startDate,
			EndDate:     &endDate,
			Region:      resource.Region,
			MetricNames: []string{"Percentage CPU"},
			Step:        3600 * time.Second, // 1 hour step
			Statistics:  []string{"Maximum"},
		})

		if errCpuMetrics != nil {
			ctx.GetLogger().Error("Error getting CPU metrics for idle check", "resourceId", resource.Id, "error", errCpuMetrics)
		} else {
			// Check for idle VMs
			isIdle := false
			if len(cpuMetrics.Items) > 0 && len(cpuMetrics.Items[0].Values) > 0 {
				isIdle = true
				for _, v := range cpuMetrics.Items[0].Values {
					if v > 2 { // CPU Utilization > 2%
						isIdle = false
						break
					}
				}
			}

			if isIdle {
				savings := 0.0
				if properties, ok := resource.Meta["properties"].(map[string]interface{}); ok {
					if hardwareProfile, ok := properties["hardwareProfile"].(map[string]interface{}); ok {
						if vmSize, ok := hardwareProfile["vmSize"].(string); ok {
							currentPrice := getVMCostWithRegion(ctx, vmSize, resource.Region)
							if currentPrice > 0 {
								savings = currentPrice // Full monthly cost as savings
							}
						}
					}
				}

				idleData := map[string]any{
					"resource_id":     resource.Id,
					"resource_name":   resource.Name,
					"resource_type":   resource.Type,
					"resource_region": resource.Region,
					"service_name":    resource.ServiceName,
					"startDate":       startDate.Format(time.RFC3339),
					"endDate":         endDate.Format(time.RFC3339),
				}
				if len(cpuMetrics.Items) > 0 {
					idleData["cpuUsage"] = cpuMetrics.Items[0]
				}

				resourceRecommendations[constants.AzureVMIdleInstance] = providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryRightSizing,
					RuleName:            constants.AzureVMIdleInstance,
					Severity:            providers.RecommendationSeverityMedium,
					Savings:             savings,
					Data:                idleData,
					Action:              providers.RecommendationActionDelete,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				}
			}

			// Check for underutilized VMs
			isUnderUtilized := false
			if len(cpuMetrics.Items) > 0 && len(cpuMetrics.Items[0].Values) > 0 {
				isUnderUtilized = true
				for _, v := range cpuMetrics.Items[0].Values {
					if v > 60 { // CPU Utilization > 60%
						isUnderUtilized = false
						break
					}
				}
			}

			if isUnderUtilized {
				if properties, ok := resource.Meta["properties"].(map[string]interface{}); ok {
					if hardwareProfile, ok := properties["hardwareProfile"].(map[string]interface{}); ok {
						if vmSize, ok := hardwareProfile["vmSize"].(string); ok {
							currentPrice := getVMCostWithRegion(ctx, vmSize, resource.Region)

							// Get current VM details (vCPUs and memory) from VM size
							currentSpecs := getVMSpecs(vmSize)
							var recommendedVCpu, recommendedMemory int64

							if currentSpecs != nil {
								// Recommend reducing by 50%
								vcpu, _ := toInt(currentSpecs["vcpu"])
								recommendedVCpu = int64(vcpu) / 2
								if recommendedVCpu == 0 {
									recommendedVCpu = 1
								}
								memGiB, _ := toFloat64(currentSpecs["memory_gib"])
								recommendedMemory = int64(memGiB / 2)
							}

							recommendedVMSize := getSmallerVMSize(vmSize)

							var recommendedInstances []map[string]any
							if recommendedVMSize != "" {
								recommendedPrice := getVMCostWithRegion(ctx, recommendedVMSize, resource.Region)
								if recommendedPrice > 0 && recommendedPrice < currentPrice {
									recommendedInstances = append(recommendedInstances, map[string]any{
										"vmSize": recommendedVMSize,
										"price":  recommendedPrice,
									})
								}
							}

							savings := 0.0
							if currentPrice > 0 {
								if len(recommendedInstances) > 0 {
									if newPrice, ok := recommendedInstances[0]["price"].(float64); ok {
										savings = currentPrice - newPrice
									}
								} else {
									savings = currentPrice / 2 // Estimate 50% savings
								}
							}

							underutilizedData := map[string]any{
								"resource_id":          resource.Id,
								"resource_name":        resource.Name,
								"resource_type":        resource.Type,
								"resource_region":      resource.Region,
								"service_name":         resource.ServiceName,
								"current_vm_size":      vmSize,
								"recommendedInstances": recommendedInstances,
								"recommendedVCpu":      recommendedVCpu,
								"recommendedMemoryGiB": recommendedMemory,
							}
							if len(cpuMetrics.Items) > 0 {
								underutilizedData["cpu"] = cpuMetrics.Items[0]
							}

							resourceRecommendations[constants.AzureVMUnderutilized] = providers.Recommendation{
								CategoryName:        providers.RecommendationCategoryRightSizing,
								RuleName:            constants.AzureVMUnderutilized,
								Severity:            providers.RecommendationSeverityMedium,
								Savings:             savings,
								Data:                underutilizedData,
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
		}

		// Check for VM generation upgrades and rightsizing opportunities
		if properties, ok := resource.Meta["properties"].(map[string]interface{}); ok {
			if hardwareProfile, ok := properties["hardwareProfile"].(map[string]interface{}); ok {
				if vmSize, ok := hardwareProfile["vmSize"].(string); ok {
					// Check for older generation VMs that can be upgraded
					upgradeSuggestion := getVMGenerationUpgradeSuggestion(vmSize)
					if upgradeSuggestion != nil {
						upgradeSavings, _ := toFloat64(upgradeSuggestion["estimated_savings_monthly"])
						resourceRecommendations[constants.AzureVMGenerationUpgrade] = providers.Recommendation{
							CategoryName: providers.RecommendationCategoryInfraUpgrade,
							RuleName:     constants.AzureVMGenerationUpgrade,
							Severity:     providers.RecommendationSeverityMedium,
							Savings:      upgradeSavings,
							Data: map[string]any{
								"resource_id":                 resource.Id,
								"resource_name":               resource.Name,
								"resource_type":               resource.Type,
								"resource_region":             resource.Region,
								"service_name":                resource.ServiceName,
								"current_vm_size":             vmSize,
								"current_generation":          upgradeSuggestion["current_generation"],
								"recommended_vm_size":         upgradeSuggestion["recommended_vm_size"],
								"recommended_generation":      upgradeSuggestion["recommended_generation"],
								"reason":                      upgradeSuggestion["reason"],
								"benefits":                    upgradeSuggestion["benefits"],
								"estimated_savings_type":      "monthly",
								"savings_source":              "Azure pricing documentation and performance benchmarks",
								"performance_improvement_pct": upgradeSuggestion["performance_improvement_pct"],
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

		// Check for missing Azure Monitor metric alerts
		vmAlarmTemplates, err := LoadAzureAlarmTemplates("vm")
		if err != nil {
			ctx.GetLogger().Warn("Failed to load VM alarm templates", "error", err, "resourceId", resource.Id)
		} else {
			for _, template := range vmAlarmTemplates {
				// Check if we should recommend this alarm based on metric type and conditions
				if !ShouldRecommendAzureAlarm(resource, template) {
					continue
				}

				// Check if alert is missing
				if !IsAzureAlertMissing(resource, template) {
					continue
				}

				// Calculate threshold based on VM properties
				threshold, err := CalculateAzureThreshold(resource, template)
				if err != nil {
					ctx.GetLogger().Warn("Error calculating Azure threshold", "error", err, "template", template.Name, "resourceId", resource.Id)
					continue
				}

				// Build alarm configuration for the recommendation data
				alarmConfig := buildAzureAlarmConfig(resource, template, threshold)

				allRecommendations = append(allRecommendations, providers.Recommendation{
					CategoryName: providers.RecommendationCategoryConfiguration,
					RuleName:     template.Name,
					Severity:     providers.RecommendationSeverityFromString(template.Severity),
					Savings:      0,
					Data: map[string]any{
						"resource_id":     resource.Id,
						"resource_name":   resource.Name,
						"resource_type":   resource.Type,
						"resource_region": resource.Region,
						"service_name":    resource.ServiceName,
						"metric_name":     template.Configuration.MetricName,
						"threshold":       threshold,
						"alarm_config":    alarmConfig,
						"alarm_type":      template.AlarmType,
						"reason":          template.Description,
						"severity":        template.Severity,
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
	return allRecommendations, nil
}

// fetchVMCapabilities fetches VM size capabilities from Azure Resource Skus API
// This is similar to AWS DescribeInstanceTypes - it returns real data from Azure
// Returns: map[vmSize]map[capabilityName]capabilityValue
func fetchVMCapabilities(ctx providers.CloudProviderContext, account providers.Account, location string) (map[string]map[string]string, error) {
	cred, session, err := getAzureCredsForAccount(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("failed to create azure credential: %w", err)
	}

	// Normalize location (Azure uses lowercase, no spaces)
	location = strings.ToLower(strings.ReplaceAll(location, " ", ""))
	if location == "" {
		return nil, fmt.Errorf("location is empty - cannot fetch VM capabilities")
	}

	ctx.GetLogger().Debug("azure: fetching VM capabilities from API",
		"location", location,
		"subscription", session.SubscriptionID)

	// Only use first subscription ID for capabilities lookup
	subscriptionID := strings.Split(session.SubscriptionID, ",")[0]
	subscriptionID = strings.TrimSpace(subscriptionID)

	// Create Resource SKUs client
	skuClient, err := armcompute.NewResourceSKUsClient(subscriptionID, cred, getAzureAuditOpts(ctx))
	if err != nil {
		return nil, fmt.Errorf("failed to create resource SKUs client: %w", err)
	}

	// Filter by location and virtualMachines resource type
	filter := fmt.Sprintf("location eq '%s'", location)
	pager := skuClient.NewListPager(&armcompute.ResourceSKUsClientListOptions{
		Filter: &filter,
	})

	// Map: VM Size → Capabilities map
	vmCapabilities := make(map[string]map[string]string)

	for pager.More() {
		page, err := pager.NextPage(ctx.GetContext())
		if err != nil {
			return nil, fmt.Errorf("failed to get next page: %w", err)
		}

		for _, sku := range page.Value {
			// Only process virtualMachines resource type
			if sku.ResourceType == nil || *sku.ResourceType != "virtualMachines" {
				continue
			}

			if sku.Name == nil {
				continue
			}

			vmSize := *sku.Name
			capabilities := make(map[string]string)

			// Extract capabilities into a map
			if sku.Capabilities != nil {
				for _, cap := range sku.Capabilities {
					if cap.Name != nil && cap.Value != nil {
						capabilities[*cap.Name] = *cap.Value
					}
				}
			}

			vmCapabilities[vmSize] = capabilities
		}
	}

	if len(vmCapabilities) == 0 {
		ctx.GetLogger().Debug("azure: API returned 0 VM capabilities",
			"location", location,
			"note", "Check if location is valid Azure region (e.g., 'eastus2')")
		return nil, fmt.Errorf("no VM capabilities found for location: %s", location)
	}

	ctx.GetLogger().Info("azure: fetched VM capabilities from API",
		"location", location,
		"vmSizeCount", len(vmCapabilities))

	return vmCapabilities, nil
}

// parseVMCapabilities converts Azure capabilities into InstanceTypeDetails format
// Similar to how AWS processes DescribeInstanceTypes response
func parseVMCapabilities(vmSize string, capabilities map[string]string) map[string]interface{} {
	// Extract vCPUs
	vcpuCount := 0
	if vcpuStr, ok := capabilities["vCPUs"]; ok {
		if v, err := strconv.Atoi(vcpuStr); err == nil {
			vcpuCount = v
		}
	}

	// Extract memory (in GB, convert to MiB)
	memoryMiB := 0
	if memoryGB, ok := capabilities["MemoryGB"]; ok {
		if mem, err := strconv.ParseFloat(memoryGB, 64); err == nil {
			memoryMiB = int(mem * 1024)
		}
	}

	// Extract vCPUsPerCore (threads per core) - This is the KEY field from Azure API!
	// No more hardcoded assumptions!
	threadsPerCore := 2 // Default fallback if API doesn't provide it
	if vCPUsPerCoreStr, ok := capabilities["vCPUsPerCore"]; ok {
		if v, err := strconv.Atoi(vCPUsPerCoreStr); err == nil && v > 0 {
			threadsPerCore = v
		}
	}

	// Calculate cores from vCPUs and threads per core
	cores := vcpuCount / threadsPerCore
	if cores < 1 {
		cores = 1
		threadsPerCore = vcpuCount
	}

	// Build InstanceTypeDetails matching AWS format
	instanceTypeDetails := map[string]interface{}{
		"VCpuInfo": map[string]interface{}{
			"DefaultVCpus":          vcpuCount,
			"DefaultCores":          cores,
			"DefaultThreadsPerCore": threadsPerCore,
			"ValidCores":            []int{cores},
			"ValidThreadsPerCore":   []int{1, 2},
		},
		"MemoryInfo": map[string]interface{}{
			"SizeInMiB": memoryMiB,
		},
		"InstanceType": vmSize,
	}

	// Add additional Azure-specific capabilities (bonus data!)
	if acus, ok := capabilities["ACUs"]; ok {
		instanceTypeDetails["ACUs"] = acus
	}
	if premiumIO, ok := capabilities["PremiumIO"]; ok {
		instanceTypeDetails["PremiumIO"] = premiumIO
	}
	if hyperVGen, ok := capabilities["HyperVGenerations"]; ok {
		instanceTypeDetails["HyperVGenerations"] = hyperVGen
	}
	if maxDataDisks, ok := capabilities["MaxDataDiskCount"]; ok {
		instanceTypeDetails["MaxDataDiskCount"] = maxDataDisks
	}

	return instanceTypeDetails
}

// extractVMPowerState extracts the power state string from a VM's InstanceView.
// Azure InstanceView.Statuses contains entries like "PowerState/running", "PowerState/deallocated".
func extractVMPowerState(instanceView *armcompute.VirtualMachineInstanceView) string {
	if instanceView == nil {
		return ""
	}
	for _, s := range instanceView.Statuses {
		if s.Code != nil && strings.HasPrefix(*s.Code, "PowerState/") {
			return strings.TrimPrefix(*s.Code, "PowerState/")
		}
	}
	return ""
}

// vmPowerStateToStatus maps an Azure VM power state to the internal ResourceStatus.
// Returns empty string if the power state is unknown/unhandled (caller should keep existing status).
func vmPowerStateToStatus(powerState string) providers.ResourceStatus {
	switch strings.ToLower(powerState) {
	case "running", "starting":
		return providers.ResourceStatusActive
	case "deallocated", "deallocating", "stopped", "stopping":
		return providers.ResourceStatusInactive
	default:
		return ""
	}
}

// getVMSpecsFromStaticMap returns VM specs from static map (fallback only)
// This is kept as a fallback when API is unavailable
// Deprecated: Use fetchVMCapabilities() with Azure API instead
func getVMSpecsFromStaticMap(vmSize string) map[string]interface{} {
	vmSize = strings.ToUpper(vmSize)

	// Static map as fallback - only has vcpu and memory_gib
	// Does NOT have cores or threads_per_core (we have to calculate)
	specsMap := map[string]map[string]interface{}{
		// Common VM sizes - keep minimal set for fallback
		"STANDARD_B1S":    {"vcpu": 1, "memory_gib": 1.0},
		"STANDARD_B2S":    {"vcpu": 2, "memory_gib": 4.0},
		"STANDARD_D2S_V5": {"vcpu": 2, "memory_gib": 8.0},
		"STANDARD_D4S_V5": {"vcpu": 4, "memory_gib": 16.0},
		"STANDARD_D2S_V3": {"vcpu": 2, "memory_gib": 8.0},
		"STANDARD_D4S_V3": {"vcpu": 4, "memory_gib": 16.0},
	}

	if specs, ok := specsMap[vmSize]; ok {
		return specs
	}
	return nil
}

// getVMSpecs returns the vCPU and memory specifications for a given Azure VM size
// Returns nil if VM size is not found
func getVMSpecs(vmSize string) map[string]interface{} {
	vmSize = strings.ToUpper(vmSize)

	// Map of Azure VM sizes to their specifications (vCPU, memory in GiB)
	// Based on Azure documentation: https://learn.microsoft.com/en-us/azure/virtual-machines/sizes
	specsMap := map[string]map[string]interface{}{
		// D-series v5 (General Purpose)
		"STANDARD_D2S_V5":  {"vcpu": 2, "memory_gib": 8.0},
		"STANDARD_D4S_V5":  {"vcpu": 4, "memory_gib": 16.0},
		"STANDARD_D8S_V5":  {"vcpu": 8, "memory_gib": 32.0},
		"STANDARD_D16S_V5": {"vcpu": 16, "memory_gib": 64.0},
		"STANDARD_D32S_V5": {"vcpu": 32, "memory_gib": 128.0},
		"STANDARD_D2_V5":   {"vcpu": 2, "memory_gib": 8.0},
		"STANDARD_D4_V5":   {"vcpu": 4, "memory_gib": 16.0},
		"STANDARD_D8_V5":   {"vcpu": 8, "memory_gib": 32.0},
		"STANDARD_D16_V5":  {"vcpu": 16, "memory_gib": 64.0},
		"STANDARD_D32_V5":  {"vcpu": 32, "memory_gib": 128.0},

		// D-series v4 (General Purpose)
		"STANDARD_D2S_V4":  {"vcpu": 2, "memory_gib": 8.0},
		"STANDARD_D4S_V4":  {"vcpu": 4, "memory_gib": 16.0},
		"STANDARD_D8S_V4":  {"vcpu": 8, "memory_gib": 32.0},
		"STANDARD_D16S_V4": {"vcpu": 16, "memory_gib": 64.0},
		"STANDARD_D32S_V4": {"vcpu": 32, "memory_gib": 128.0},
		"STANDARD_D2_V4":   {"vcpu": 2, "memory_gib": 8.0},
		"STANDARD_D4_V4":   {"vcpu": 4, "memory_gib": 16.0},
		"STANDARD_D8_V4":   {"vcpu": 8, "memory_gib": 32.0},
		"STANDARD_D16_V4":  {"vcpu": 16, "memory_gib": 64.0},
		"STANDARD_D32_V4":  {"vcpu": 32, "memory_gib": 128.0},

		// D-series v3 (General Purpose)
		"STANDARD_D2S_V3":  {"vcpu": 2, "memory_gib": 8.0},
		"STANDARD_D4S_V3":  {"vcpu": 4, "memory_gib": 16.0},
		"STANDARD_D8S_V3":  {"vcpu": 8, "memory_gib": 32.0},
		"STANDARD_D16S_V3": {"vcpu": 16, "memory_gib": 64.0},
		"STANDARD_D32S_V3": {"vcpu": 32, "memory_gib": 128.0},
		"STANDARD_D2_V3":   {"vcpu": 2, "memory_gib": 8.0},
		"STANDARD_D4_V3":   {"vcpu": 4, "memory_gib": 16.0},
		"STANDARD_D8_V3":   {"vcpu": 8, "memory_gib": 32.0},
		"STANDARD_D16_V3":  {"vcpu": 16, "memory_gib": 64.0},
		"STANDARD_D32_V3":  {"vcpu": 32, "memory_gib": 128.0},

		// E-series v5 (Memory Optimized)
		"STANDARD_E2S_V5":  {"vcpu": 2, "memory_gib": 16.0},
		"STANDARD_E4S_V5":  {"vcpu": 4, "memory_gib": 32.0},
		"STANDARD_E8S_V5":  {"vcpu": 8, "memory_gib": 64.0},
		"STANDARD_E16S_V5": {"vcpu": 16, "memory_gib": 128.0},
		"STANDARD_E32S_V5": {"vcpu": 32, "memory_gib": 256.0},
		"STANDARD_E64S_V5": {"vcpu": 64, "memory_gib": 512.0},

		// E-series v4 (Memory Optimized)
		"STANDARD_E2S_V4":  {"vcpu": 2, "memory_gib": 16.0},
		"STANDARD_E4S_V4":  {"vcpu": 4, "memory_gib": 32.0},
		"STANDARD_E8S_V4":  {"vcpu": 8, "memory_gib": 64.0},
		"STANDARD_E16S_V4": {"vcpu": 16, "memory_gib": 128.0},
		"STANDARD_E32S_V4": {"vcpu": 32, "memory_gib": 256.0},
		"STANDARD_E64S_V4": {"vcpu": 64, "memory_gib": 512.0},

		// E-series v3 (Memory Optimized)
		"STANDARD_E2S_V3":  {"vcpu": 2, "memory_gib": 16.0},
		"STANDARD_E4S_V3":  {"vcpu": 4, "memory_gib": 32.0},
		"STANDARD_E8S_V3":  {"vcpu": 8, "memory_gib": 64.0},
		"STANDARD_E16S_V3": {"vcpu": 16, "memory_gib": 128.0},
		"STANDARD_E32S_V3": {"vcpu": 32, "memory_gib": 256.0},
		"STANDARD_E64S_V3": {"vcpu": 64, "memory_gib": 512.0},

		// F-series v2 (Compute Optimized)
		"STANDARD_F2S_V2":  {"vcpu": 2, "memory_gib": 4.0},
		"STANDARD_F4S_V2":  {"vcpu": 4, "memory_gib": 8.0},
		"STANDARD_F8S_V2":  {"vcpu": 8, "memory_gib": 16.0},
		"STANDARD_F16S_V2": {"vcpu": 16, "memory_gib": 32.0},
		"STANDARD_F32S_V2": {"vcpu": 32, "memory_gib": 64.0},

		// B-series (Burstable)
		"STANDARD_B1MS": {"vcpu": 1, "memory_gib": 2.0},
		"STANDARD_B2MS": {"vcpu": 2, "memory_gib": 8.0},
		"STANDARD_B4MS": {"vcpu": 4, "memory_gib": 16.0},
		"STANDARD_B8MS": {"vcpu": 8, "memory_gib": 32.0},
	}

	if specs, ok := specsMap[vmSize]; ok {
		return specs
	}

	return nil // VM size not found
}

// getSmallerVMSize returns a smaller VM size recommendation for underutilized VMs
// Returns empty string if no smaller size is available
func getSmallerVMSize(vmSize string) string {
	vmSize = strings.ToUpper(vmSize)

	// Map of common VM sizes to their smaller equivalents (approximately 50% reduction)
	// Based on Azure VM size families and vCPU counts
	sizeMap := map[string]string{
		// D-series (General Purpose)
		"STANDARD_D32S_V5": "STANDARD_D16S_V5",
		"STANDARD_D16S_V5": "STANDARD_D8S_V5",
		"STANDARD_D8S_V5":  "STANDARD_D4S_V5",
		"STANDARD_D4S_V5":  "STANDARD_D2S_V5",
		"STANDARD_D32S_V4": "STANDARD_D16S_V4",
		"STANDARD_D16S_V4": "STANDARD_D8S_V4",
		"STANDARD_D8S_V4":  "STANDARD_D4S_V4",
		"STANDARD_D4S_V4":  "STANDARD_D2S_V4",
		"STANDARD_D32S_V3": "STANDARD_D16S_V3",
		"STANDARD_D16S_V3": "STANDARD_D8S_V3",
		"STANDARD_D8S_V3":  "STANDARD_D4S_V3",
		"STANDARD_D4S_V3":  "STANDARD_D2S_V3",
		"STANDARD_D32_V5":  "STANDARD_D16_V5",
		"STANDARD_D16_V5":  "STANDARD_D8_V5",
		"STANDARD_D8_V5":   "STANDARD_D4_V5",
		"STANDARD_D4_V5":   "STANDARD_D2_V5",
		"STANDARD_D32_V4":  "STANDARD_D16_V4",
		"STANDARD_D16_V4":  "STANDARD_D8_V4",
		"STANDARD_D8_V4":   "STANDARD_D4_V4",
		"STANDARD_D4_V4":   "STANDARD_D2_V4",
		"STANDARD_D32_V3":  "STANDARD_D16_V3",
		"STANDARD_D16_V3":  "STANDARD_D8_V3",
		"STANDARD_D8_V3":   "STANDARD_D4_V3",
		"STANDARD_D4_V3":   "STANDARD_D2_V3",

		// E-series (Memory Optimized)
		"STANDARD_E64S_V5": "STANDARD_E32S_V5",
		"STANDARD_E32S_V5": "STANDARD_E16S_V5",
		"STANDARD_E16S_V5": "STANDARD_E8S_V5",
		"STANDARD_E8S_V5":  "STANDARD_E4S_V5",
		"STANDARD_E4S_V5":  "STANDARD_E2S_V5",
		"STANDARD_E64S_V4": "STANDARD_E32S_V4",
		"STANDARD_E32S_V4": "STANDARD_E16S_V4",
		"STANDARD_E16S_V4": "STANDARD_E8S_V4",
		"STANDARD_E8S_V4":  "STANDARD_E4S_V4",
		"STANDARD_E4S_V4":  "STANDARD_E2S_V4",
		"STANDARD_E64S_V3": "STANDARD_E32S_V3",
		"STANDARD_E32S_V3": "STANDARD_E16S_V3",
		"STANDARD_E16S_V3": "STANDARD_E8S_V3",
		"STANDARD_E8S_V3":  "STANDARD_E4S_V3",
		"STANDARD_E4S_V3":  "STANDARD_E2S_V3",

		// F-series (Compute Optimized)
		"STANDARD_F32S_V2": "STANDARD_F16S_V2",
		"STANDARD_F16S_V2": "STANDARD_F8S_V2",
		"STANDARD_F8S_V2":  "STANDARD_F4S_V2",
		"STANDARD_F4S_V2":  "STANDARD_F2S_V2",

		// B-series (Burstable)
		"STANDARD_B8MS": "STANDARD_B4MS",
		"STANDARD_B4MS": "STANDARD_B2MS",
		"STANDARD_B2MS": "STANDARD_B1MS",
	}

	if smallerSize, ok := sizeMap[vmSize]; ok {
		return smallerSize
	}

	return "" // No smaller size available
}

// getVMGenerationUpgradeSuggestion returns upgrade suggestions for older generation Azure VMs
// Returns nil if the VM is already on a current generation or if no upgrade path exists
func getVMGenerationUpgradeSuggestion(vmSize string) map[string]interface{} {
	// Map of old generation VMs to new generation equivalents
	// Based on Azure documentation: https://learn.microsoft.com/en-us/azure/virtual-machines/sizes
	vmSize = strings.ToUpper(vmSize)

	// Dv2/DSv2 → Dv5/Dsv5 (latest generation, ~20% better price-performance)
	if strings.Contains(vmSize, "_D") && (strings.Contains(vmSize, "V2") || strings.Contains(vmSize, "SV2")) {
		newSize := strings.Replace(vmSize, "V2", "V5", 1)
		newSize = strings.Replace(newSize, "SV2", "SV5", 1)
		return map[string]interface{}{
			"current_generation":          "Dv2/DSv2",
			"recommended_vm_size":         newSize,
			"recommended_generation":      "Dv5/DSv5",
			"estimated_savings_monthly":   50.0, // Approximate 15-20% cost savings
			"performance_improvement_pct": 20,
			"reason":                      "Current VM is running on older Dv2/DSv2 generation. Upgrading to Dv5/DSv5 provides better price-performance ratio, improved CPU performance, and lower costs.",
			"benefits": []string{
				"15-20% cost savings",
				"20% better CPU performance",
				"DDR4 to DDR5 memory upgrade",
				"Better network throughput",
				"Support for newer Azure features",
			},
		}
	}

	// Dv3/DSv3 → Dv5/Dsv5
	if strings.Contains(vmSize, "_D") && (strings.Contains(vmSize, "V3") || strings.Contains(vmSize, "SV3")) {
		newSize := strings.Replace(vmSize, "V3", "V5", 1)
		newSize = strings.Replace(newSize, "SV3", "SV5", 1)
		return map[string]interface{}{
			"current_generation":          "Dv3/DSv3",
			"recommended_vm_size":         newSize,
			"recommended_generation":      "Dv5/DSv5",
			"estimated_savings_monthly":   35.0, // Approximate 10-15% cost savings
			"performance_improvement_pct": 15,
			"reason":                      "Current VM is running on Dv3/DSv3 generation. Upgrading to Dv5/DSv5 provides improved price-performance ratio and latest Intel Ice Lake processors.",
			"benefits": []string{
				"10-15% cost savings",
				"15% better CPU performance",
				"Latest Intel Ice Lake processors",
				"Improved memory bandwidth",
				"Enhanced security features",
			},
		}
	}

	// Dv4/DSv4 → Dv5/Dsv5
	if strings.Contains(vmSize, "_D") && (strings.Contains(vmSize, "V4") || strings.Contains(vmSize, "SV4")) {
		newSize := strings.Replace(vmSize, "V4", "V5", 1)
		newSize = strings.Replace(newSize, "SV4", "SV5", 1)
		return map[string]interface{}{
			"current_generation":          "Dv4/DSv4",
			"recommended_vm_size":         newSize,
			"recommended_generation":      "Dv5/DSv5",
			"estimated_savings_monthly":   25.0, // Approximate 8-10% cost savings
			"performance_improvement_pct": 10,
			"reason":                      "Current VM is running on Dv4/DSv4 generation. Upgrading to Dv5/DSv5 provides latest processor generation with better efficiency.",
			"benefits": []string{
				"8-10% cost savings",
				"10% better CPU performance",
				"Ice Lake to Sapphire Rapids upgrade",
				"Better power efficiency",
				"Future-proof infrastructure",
			},
		}
	}

	// Ev3/Esv3 → Ev5/Esv5 (memory optimized)
	if strings.Contains(vmSize, "_E") && (strings.Contains(vmSize, "V3") || strings.Contains(vmSize, "SV3")) {
		newSize := strings.Replace(vmSize, "V3", "V5", 1)
		newSize = strings.Replace(newSize, "SV3", "SV5", 1)
		return map[string]interface{}{
			"current_generation":          "Ev3/ESv3",
			"recommended_vm_size":         newSize,
			"recommended_generation":      "Ev5/ESv5",
			"estimated_savings_monthly":   40.0,
			"performance_improvement_pct": 15,
			"reason":                      "Memory-optimized VM on older generation. Upgrading to Ev5/ESv5 provides better memory bandwidth and cost efficiency for memory-intensive workloads.",
			"benefits": []string{
				"10-15% cost savings",
				"Higher memory bandwidth",
				"Better price-per-GB ratio",
				"Improved CPU performance",
				"Support for larger VM sizes",
			},
		}
	}

	// Fv2 → Fsv2 or Dv5 (compute optimized)
	if strings.Contains(vmSize, "_F") && strings.Contains(vmSize, "V2") && !strings.Contains(vmSize, "SV2") {
		newSize := strings.Replace(vmSize, "V2", "SV2", 1)
		return map[string]interface{}{
			"current_generation":          "Fv2",
			"recommended_vm_size":         newSize,
			"recommended_generation":      "Fsv2",
			"estimated_savings_monthly":   20.0,
			"performance_improvement_pct": 12,
			"reason":                      "Compute-optimized VM can benefit from premium storage on Fsv2 series for better I/O performance.",
			"benefits": []string{
				"Premium storage support",
				"Better I/O performance",
				"Same high CPU-to-memory ratio",
				"Improved disk throughput",
			},
		}
	}

	// A-series → B-series or Dv5 (basic to burstable or general purpose)
	if strings.HasPrefix(vmSize, "STANDARD_A") {
		return map[string]interface{}{
			"current_generation":          "A-series",
			"recommended_vm_size":         "Standard_B2s", // Burstable alternative
			"recommended_generation":      "B-series (Burstable)",
			"estimated_savings_monthly":   60.0, // A-series is significantly more expensive
			"performance_improvement_pct": 25,
			"reason":                      "A-series VMs are the oldest generation and significantly more expensive. Consider B-series for burstable workloads or Dv5 for consistent performance.",
			"benefits": []string{
				"40-60% cost savings",
				"Modern processor architecture",
				"Better performance per dollar",
				"CPU burst capabilities (B-series)",
				"Support for premium storage",
			},
		}
	}

	return nil // No upgrade suggestion for this VM size
}

// getVMCost estimates the monthly cost of a VM using Azure Retail Prices API.
// Falls back to hardcoded estimates if API is disabled or fails.
func getVMCost(ctx providers.CloudProviderContext, vmSize string) float64 {
	return getVMCostWithRegion(ctx, vmSize, "eastus") // Default region
}

// getVMCostWithRegion gets VM cost for specific region using dynamic pricing
func getVMCostWithRegion(ctx providers.CloudProviderContext, vmSize, region string) float64 {
	// Try dynamic pricing first if enabled
	cache := GetPricingCache()
	if cache.IsEnabled() {
		price, err := cache.GetVMPrice(ctx, vmSize, region)
		if err == nil {
			ctx.GetLogger().Debug("Fetched dynamic price for VM", "vmSize", vmSize, "region", region, "price", price)
			return price
		}
		// Log error but continue to fallback
		// In production, you might want to log this with proper logger
	}

	// Fallback to hardcoded estimates
	return getVMCostFallback(vmSize)
}

// getVMCostFallback provides hardcoded cost estimates as fallback
// Prices are illustrative (in USD) and not accurate. Updated periodically.
func getVMCostFallback(vmSize string) float64 {
	// Simplified cost estimation based on VM series
	vmSizeUpper := strings.ToUpper(vmSize)
	if strings.Contains(vmSizeUpper, "B_") { // Burstable
		return 30.0
	}
	if strings.Contains(vmSizeUpper, "_D") { // General Purpose
		if strings.Contains(vmSizeUpper, "V5") {
			return 150.0
		}
		if strings.Contains(vmSizeUpper, "V4") {
			return 170.0
		}
		if strings.Contains(vmSizeUpper, "V3") {
			return 190.0
		}
		if strings.Contains(vmSizeUpper, "V2") {
			return 210.0
		}
		return 180.0
	}
	if strings.Contains(vmSizeUpper, "_E") { // Memory Optimized
		return 250.0
	}
	if strings.Contains(vmSizeUpper, "_F") { // Compute Optimized
		return 140.0
	}
	if strings.Contains(vmSizeUpper, "_M") { // Memory Optimized (Large)
		return 1000.0
	}
	return 100.0 // Default for unknown
}

// getDiskMonthlyCost estimates the monthly cost of a managed disk using Azure Retail Prices API.
// Falls back to hardcoded estimates if API is disabled or fails.
func getDiskMonthlyCost(sku string, sizeGB float64) float64 {
	return getDiskMonthlyCostWithRegion(sku, "eastus", sizeGB) // Default region
}

// getDiskMonthlyCostWithRegion gets disk cost for specific region using dynamic pricing
func getDiskMonthlyCostWithRegion(sku, region string, sizeGB float64) float64 {
	// Try dynamic pricing first if enabled
	cache := GetPricingCache()
	if cache.IsEnabled() {
		ctx := providers.NewCloudProviderContext(context.Background())
		price, err := cache.GetDiskPrice(ctx, sku, region, sizeGB)
		if err == nil {
			return price
		}
		// Log error but continue to fallback
	}

	// Fallback to hardcoded estimates
	return getDiskMonthlyCostFallback(sku, sizeGB)
}

// getDiskMonthlyCostFallback provides hardcoded cost estimates as fallback
// Prices are illustrative (in USD for East US) and not accurate.
func getDiskMonthlyCostFallback(sku string, sizeGB float64) float64 {
	sku = strings.ToLower(sku)
	// Pricing per GB/month (illustrative)
	var pricePerGB float64
	if strings.Contains(sku, "premium_lrs") {
		// P10 (128GB) is ~$19.71. So ~$0.154/GB
		pricePerGB = 0.154
	} else if strings.Contains(sku, "standardssd_lrs") {
		// E10 (128GB) is ~$9.60. So ~$0.075/GB
		pricePerGB = 0.075
	} else if strings.Contains(sku, "standard_lrs") {
		// S10 (128GB) is ~$5.12. So ~$0.04/GB
		pricePerGB = 0.04
	} else {
		return 0 // Unknown SKU
	}
	return sizeGB * pricePerGB
}

func (s *virtualMachineService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	// Check if this is an alarm/metric alert recommendation
	if _, ok := recommendation.Data["alarm_config"]; ok {
		ctx.GetLogger().Info("azure: applying alarm recommendation for vm",
			"rule_name", recommendation.RuleName,
			"resource_id", recommendation.ResourceId)
		return CreateAzureMetricAlertFromRecommendation(ctx, account, recommendation)
	}

	// Handle alarm creation recommendations
	if strings.HasSuffix(recommendation.RuleName, "_alarm_missing") {
		return CreateAzureMetricAlertFromRecommendation(ctx, account, recommendation)
	}

	_, err := s.ApplyCommand(ctx, account, providers.ApplyCommandRequest{
		ResourceId: recommendation.ResourceId,
		Command:    recommendation.RuleName,
		Args:       recommendation.Data,
	})
	return err
}

func (s *virtualMachineService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (resp providers.ApplyCommandResponse, retErr error) {
	// Always audit, including early-return / failure paths. Mirrors the
	// pattern in aws_ec2.go / aws_rds.go so the UI's Action History tab
	// reflects every attempted action, not just successes.
	defer func() {
		status := "SUCCESS"
		msg := resp.Message
		if retErr != nil {
			status = "FAILURE"
			if msg == "" {
				msg = retErr.Error()
			}
		}
		if auditErr := logResourceActionAudit(ctx, command, account, status, msg); auditErr != nil {
			ctx.GetLogger().Warn("failed to log audit record", "error", auditErr)
		}
	}()

	logger := ctx.GetLogger()
	cred, session, err := getAzureCredsForAccount(ctx, account)
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to create azure credential: %v", err),
		}, err
	}

	subscriptionID, resourceGroup, vmName := parseAzureVMResourceID(command.ResourceId)

	if subscriptionID == "" {
		subscriptionID = session.SubscriptionID
	}
	if resourceGroup == "" || vmName == "" {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to extract resource group or VM name from resource ID: %s", command.ResourceId),
		}, fmt.Errorf("invalid resource ID")
	}

	client, err := armcompute.NewVirtualMachinesClient(subscriptionID, cred, getAzureAuditOpts(ctx))
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to create VM client: %v", err),
		}, err
	}

	// Handle different commands
	switch command.Command {
	case constants.AzureVMAcceleratedNetworkingDisabled:
		// Cannot auto-fix - requires network interface reconfiguration
		return providers.ApplyCommandResponse{
			Success: false,
			Message: "cannot auto-apply command: accelerated networking requires manual network interface reconfiguration",
		}, fmt.Errorf("accelerated networking requires manual configuration")

	case constants.AzureVMUnmanagedDisk:
		// Cannot auto-fix - requires disk conversion
		return providers.ApplyCommandResponse{
			Success: false,
			Message: "cannot auto-apply command: converting to managed disks requires disk migration",
		}, fmt.Errorf("managed disks conversion requires manual migration")

	case constants.AzureVMBackupDisabled:
		// Cannot auto-fix - requires backup vault setup
		return providers.ApplyCommandResponse{
			Success: false,
			Message: "cannot auto-apply command: backup configuration requires Azure Backup vault setup",
		}, fmt.Errorf("backup requires manual vault configuration")

	case constants.AzureVMBootDiagnosticsDisabled:
		// Enable boot diagnostics
		logger.Info("applying command: enabling boot diagnostics", "vmName", vmName)

		vmResp, err := client.Get(ctx.GetContext(), resourceGroup, vmName, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to get VM: %v", err),
			}, err
		}

		vm := vmResp.VirtualMachine
		if vm.Properties == nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: "VM properties are nil",
			}, fmt.Errorf("VM properties are nil")
		}

		if vm.Properties.DiagnosticsProfile == nil {
			vm.Properties.DiagnosticsProfile = &armcompute.DiagnosticsProfile{}
		}
		if vm.Properties.DiagnosticsProfile.BootDiagnostics == nil {
			vm.Properties.DiagnosticsProfile.BootDiagnostics = &armcompute.BootDiagnostics{}
		}
		enabled := true
		vm.Properties.DiagnosticsProfile.BootDiagnostics.Enabled = &enabled

		poller, err := client.BeginCreateOrUpdate(ctx.GetContext(), resourceGroup, vmName, vm, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to update VM: %v", err),
			}, err
		}

		_, err = poller.PollUntilDone(ctx.GetContext(), nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to wait for VM update: %v", err),
			}, err
		}

		logger.Info("successfully enabled boot diagnostics", "vmName", vmName)
		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("successfully enabled boot diagnostics for VM '%s'", vmName),
		}, nil

	// "stop" maps to deallocate on Azure: powerOff still bills for compute,
	// deallocate releases the host and stops compute charges (matches the
	// semantics users expect from "Stop" on AWS/GCP).
	case "stop", "stop_vm", "deallocate", "deallocate_vm":
		logger.Info("applying command: deallocating VM", "vmName", vmName)
		poller, err := client.BeginDeallocate(ctx.GetContext(), resourceGroup, vmName, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to deallocate VM: %v", err),
			}, err
		}
		_, err = poller.PollUntilDone(ctx.GetContext(), nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to wait for VM deallocation: %v", err),
			}, err
		}
		logger.Info("successfully deallocated VM", "vmName", vmName)
		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("successfully deallocated VM '%s'", vmName),
		}, nil

	case "start", "start_vm":
		logger.Info("applying command: starting VM", "vmName", vmName)
		poller, err := client.BeginStart(ctx.GetContext(), resourceGroup, vmName, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to start VM: %v", err),
			}, err
		}
		_, err = poller.PollUntilDone(ctx.GetContext(), nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to wait for VM start: %v", err),
			}, err
		}
		logger.Info("successfully started VM", "vmName", vmName)
		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("successfully started VM '%s'", vmName),
		}, nil

	case "reboot", "restart", "restart_vm":
		logger.Info("applying command: restarting VM", "vmName", vmName)
		poller, err := client.BeginRestart(ctx.GetContext(), resourceGroup, vmName, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to restart VM: %v", err),
			}, err
		}
		_, err = poller.PollUntilDone(ctx.GetContext(), nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to wait for VM restart: %v", err),
			}, err
		}
		logger.Info("successfully restarted VM", "vmName", vmName)
		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("successfully restarted VM '%s'", vmName),
		}, nil

	case "delete_vm":
		// Delete the VM
		logger.Info("applying command: deleting VM", "vmName", vmName)
		poller, err := client.BeginDelete(ctx.GetContext(), resourceGroup, vmName, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to delete VM: %v", err),
			}, err
		}
		_, err = poller.PollUntilDone(ctx.GetContext(), nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to wait for VM deletion: %v", err),
			}, err
		}
		logger.Info("successfully deleted VM", "vmName", vmName)
		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("successfully deleted VM '%s'", vmName),
		}, nil

	default:
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("unknown command: %s", command.Command),
		}, fmt.Errorf("unknown command: %s", command.Command)
	}
}

func (s *virtualMachineService) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
	sysWrkSpaceID := ""
	logger := ctx.GetLogger()
	cred, session, err := getAzureCredsForAccount(ctx, account)
	if err != nil {
		logger.Error(
			"failed to get credential: " + err.Error(),
		)
		return "", err
	}

	dcraClient, err := armmonitor.NewDataCollectionRuleAssociationsClient(session.SubscriptionID, cred, getAzureAuditOpts(ctx))
	if err != nil {
		logger.Error("failed to create DCR association client", "error", err)
		return "", err
	}

	pager := dcraClient.NewListByResourcePager(resourceId, nil)

	for pager.More() {
		page, err := pager.NextPage(ctx.GetContext())
		if err != nil {
			logger.Error("failed to get next page of DCR associations", "error", err)
			return "", err
		}

		for _, assoc := range page.Value {
			if assoc.Properties == nil || assoc.Properties.DataCollectionRuleID == nil {
				continue
			}
			dcrID := *assoc.Properties.DataCollectionRuleID

			// Extract RG and name from DCR ID
			parts := strings.Split(dcrID, "/")
			var dcrRG, dcrName string
			for i, p := range parts {
				if strings.EqualFold(p, "resourceGroups") && i+1 < len(parts) {
					dcrRG = parts[i+1]
				}
				if strings.EqualFold(p, "dataCollectionRules") && i+1 < len(parts) {
					dcrName = parts[i+1]
				}
			}
			if dcrRG == "" || dcrName == "" {
				continue
			}

			// Get DCR details
			dcrClient, err := armmonitor.NewDataCollectionRulesClient(session.SubscriptionID, cred, getAzureAuditOpts(ctx))
			if err != nil {
				logger.Error("failed to create DCR client", "error", err)
				continue
			}
			dcr, err := dcrClient.Get(ctx.GetContext(), dcrRG, dcrName, nil)
			if err != nil {
				logger.Error("failed to get DCR", "dcrName", dcrName, "error", err)
				continue
			}

			// Check if this DCR has "user/application logs"
			if dcr.Properties != nil && dcr.Properties.DataFlows != nil {
				for _, flow := range dcr.Properties.DataFlows {
					for _, streamPtr := range flow.Streams {
						if streamPtr == nil {
							continue
						}
						streamStr := string(*streamPtr) // safely dereference pointer
						// Only pick user logs (example: Syslog or Custom)
						if strings.Contains(streamStr, "Syslog") {
							if dcr.Properties.Destinations != nil && dcr.Properties.Destinations.LogAnalytics != nil && len(dcr.Properties.Destinations.LogAnalytics) > 0 {
								streamStr = strings.TrimPrefix(streamStr, "Microsoft-")
								sysWrkSpaceID = fmt.Sprintf("%s/%s", *dcr.Properties.Destinations.LogAnalytics[0].WorkspaceID, streamStr)
							}
						}
						// if strings.Contains(streamStr, "Custom") {
						// 	if dcr.Properties.Destinations != nil && dcr.Properties.Destinations.LogAnalytics != nil {
						// 		// customWrkSpaceID = *dcr.Properties.Destinations.LogAnalytics[0].WorkspaceID
						// 		streamStr = strings.TrimPrefix(streamStr, "Custom-")
						// 		// customWrkSpaceID = fmt.Sprintf("%s/%s", *dcr.Properties.Destinations.LogAnalytics[0].WorkspaceID, streamStr)
						// 	}
						// }
					}
				}
			}
		}
	}
	// temp change to only give sys logs
	// if customWrkSpaceID != "" {
	// 	return customWrkSpaceID, nil
	// } else
	if sysWrkSpaceID != "" {
		return sysWrkSpaceID, nil
	}
	return "", errors.New(" workspace not found")
}

func (s *virtualMachineService) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, resource providers.Resource) (providers.ServiceMapApplication, error) {
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
		if networkProfile, ok := properties["networkProfile"].(map[string]interface{}); ok {
			if networkInterfaces, ok := networkProfile["networkInterfaces"].([]interface{}); ok {
				for _, nic := range networkInterfaces {
					if nicMap, ok := nic.(map[string]interface{}); ok {
						if id, ok := nicMap["id"].(string); ok {
							app.Downstreams = append(app.Downstreams, providers.ServiceApplicationLink{
								Id: providers.ServiceApplicationId{
									Name:      id,
									Kind:      "Microsoft.Network/networkInterfaces",
									Namespace: resource.Region,
								},
							}.ToDownstreamLink())
						}
					}
				}
			}
		}

		if storageProfile, ok := properties["storageProfile"].(map[string]interface{}); ok {
			if dataDisks, ok := storageProfile["dataDisks"].([]interface{}); ok {
				for _, disk := range dataDisks {
					if diskMap, ok := disk.(map[string]interface{}); ok {
						if managedDisk, ok := diskMap["managedDisk"].(map[string]interface{}); ok {
							if id, ok := managedDisk["id"].(string); ok {
								app.Downstreams = append(app.Downstreams, providers.ServiceApplicationLink{
									Id: providers.ServiceApplicationId{
										Name:      id,
										Kind:      "Microsoft.Compute/disks",
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
	return app, nil
}
