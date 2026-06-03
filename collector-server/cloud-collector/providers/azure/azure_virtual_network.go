package azure

import (
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
)

type virtualNetworkService struct {
}

func (s *virtualNetworkService) Name() string {
	return "Microsoft.Network/virtualNetworks"
}

// Scope returns the service scope - this is a regional service
func (s *virtualNetworkService) Scope() ServiceScope {
	return ServiceScopeRegional
}

func (s *virtualNetworkService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
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
		client, err := armnetwork.NewVirtualNetworksClient(subID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to create virtual network client: %w", err)
		}

		pager := client.NewListAllPager(nil)
		for pager.More() {
			page, err := pager.NextPage(ctx.GetContext())
			if err != nil {
				return nil, fmt.Errorf("failed to get next page: %w", err)
			}
			for _, vnet := range page.Value {
				status := providers.ResourceStatusUnknown
				if vnet.Properties != nil && vnet.Properties.ProvisioningState != nil {
					if val, ok := nbStatusFromAzureProvisioningState[string(*vnet.Properties.ProvisioningState)]; ok {
						status = val
					}
				}

				allResources = append(allResources, providers.Resource{
					Id:          *vnet.ID,
					Name:        *vnet.Name,
					Type:        *vnet.Type,
					Region:      *vnet.Location,
					Tags:        toAzureTags(vnet.Tags),
					Meta:        structToMap(vnet),
					Status:      status,
					CreatedAt:   time.Time{},
					Arn:         *vnet.ID,
					ServiceName: s.Name(),
				})
			}
		}
	}
	return allResources, nil
}

func (s *virtualNetworkService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	_, err := s.ApplyCommand(ctx, account, providers.ApplyCommandRequest{
		ResourceId: recommendation.ResourceId,
		Command:    recommendation.RuleName,
	})
	return err
}

func (s *virtualNetworkService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	logger := ctx.GetLogger()
	cred, session, err := getAzureCredsForAccount(ctx, account)
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to create azure credential: %v", err),
		}, err
	}

	// Extract subscription ID, resource group and vnet name from resource ID
	parts := strings.Split(command.ResourceId, "/")
	var subscriptionID, resourceGroup, vnetName string
	for i, part := range parts {
		if part == "subscriptions" && i+1 < len(parts) {
			subscriptionID = parts[i+1]
		}
		if part == "resourceGroups" && i+1 < len(parts) {
			resourceGroup = parts[i+1]
		}
		if part == "virtualNetworks" && i+1 < len(parts) {
			vnetName = parts[i+1]
		}
	}

	if subscriptionID == "" {
		subscriptionID = session.SubscriptionID
	}
	if resourceGroup == "" || vnetName == "" {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to extract resource group or vnet name from resource ID: %s", command.ResourceId),
		}, fmt.Errorf("invalid resource ID")
	}

	client, err := armnetwork.NewVirtualNetworksClient(subscriptionID, cred, getAzureAuditOpts(ctx))
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to create virtual network client: %v", err),
		}, err
	}

	vnetResp, err := client.Get(ctx.GetContext(), resourceGroup, vnetName, nil)
	if err != nil {
		return providers.ApplyCommandResponse{Success: false, Message: fmt.Sprintf("failed to get virtual network: %v", err)}, err
	}
	vnet := vnetResp.VirtualNetwork

	switch command.Command {
	case "azure_vnet_ddos_protection_disabled":
		logger.Info("applying command: enabling DDoS protection", "vnetName", vnetName, "resourceGroup", resourceGroup, "subscriptionID", subscriptionID, "location", vnet.Location)
		// Enabling DDoS protection requires a DDoS Protection Plan to be associated.
		// This cannot be done automatically without knowing which plan to use.
		return providers.ApplyCommandResponse{Success: false, Message: "enabling DDoS protection requires manual selection of a DDoS Protection Plan"}, fmt.Errorf("manual configuration required")

	case "azure_vnet_vm_protection_disabled":
		logger.Info("applying command: enabling VM protection", "vnetName", vnetName, "resourceGroup", resourceGroup, "subscriptionID", subscriptionID, "location", vnet.Location)
		// VM Protection is part of Azure Bastion, which is a separate resource.
		// This is a conceptual recommendation, not a direct VNet property.
		return providers.ApplyCommandResponse{Success: false, Message: "enabling VM protection requires setting up Azure Bastion, which must be done manually"}, fmt.Errorf("manual configuration required")

	default:
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("unknown command: %s", command.Command),
		}, fmt.Errorf("unknown command: %s", command.Command)
	}
}

func (s *virtualNetworkService) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAzureMonitorMetrics(ctx, account, filter)
}

func (s *virtualNetworkService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	var allRecommendations []providers.Recommendation
	for _, resource := range existingResources {
		meta := resource.Meta
		if len(meta) == 0 {
			continue
		}

		// Check for Tags
		if len(resource.Tags) == 0 {
			allRecommendations = append(allRecommendations, providers.Recommendation{
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
					"reason":          "Virtual Network has no tags applied. Tags are essential for cost allocation, resource organization, compliance tracking, and network segmentation policies.",
					"benefits": []string{
						"Better cost tracking and allocation for network resources",
						"Easier network resource organization and filtering",
						"Compliance and governance enforcement",
						"Automated policy application",
						"Team ownership identification",
						"Network segmentation documentation",
					},
					"recommended_tags": []string{"environment", "owner", "cost-center", "application", "network-tier", "compliance-level"},
				},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		if props, ok := meta["properties"].(map[string]interface{}); ok {
			// Check DDoS protection
			if ddosProtectionPlan, ok := props["enableDdosProtection"].(bool); !ok || !ddosProtectionPlan {
				allRecommendations = append(allRecommendations, providers.Recommendation{
					CategoryName: providers.RecommendationCategorySecurity,
					RuleName:     "azure_vnet_ddos_protection_disabled",
					Severity:     providers.RecommendationSeverityHigh,
					Savings:      0,
					Data: map[string]any{
						"resource_id":               resource.Id,
						"resource_name":             resource.Name,
						"resource_type":             resource.Type,
						"resource_region":           resource.Region,
						"service_name":              resource.ServiceName,
						"current_configuration":     "DDoS Basic (included)",
						"recommended_configuration": "DDoS Network Protection (Standard)",
						"reason":                    "DDoS Network Protection is not enabled for VNet. Azure DDoS Network Protection provides enhanced DDoS mitigation features tuned specifically for Azure Virtual Network resources, protecting against volumetric, protocol, and application-layer attacks with real-time telemetry and adaptive tuning.",
						"benefits": []string{
							"Protection against volumetric attacks (UDP floods, amplification attacks)",
							"Protocol attack mitigation (SYN floods, fragmented packet attacks)",
							"Application-layer DDoS protection (Layer 7)",
							"Real-time attack telemetry and metrics",
							"Adaptive tuning based on traffic patterns",
							"DDoS Rapid Response support for active attacks",
							"Cost protection (credits for scale-out during attacks)",
							"Always-on traffic monitoring",
						},
						"attack_types_protected": []string{"volumetric attacks", "protocol attacks", "resource-layer attacks", "DNS amplification", "NTP amplification", "SYN floods"},
						"compliance_impact":      "Required for high-security and regulatory compliance workloads (finance, healthcare, government)",
						"cost_impact":            "~$2,944/month per DDoS plan (covers all VNets in subscription)",
						"sla_improvement":        "Financial SLA backed cost protection during DDoS attacks",
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}

			// Check VM protection (deprecated, recommend Azure Bastion instead)
			if vmProtection, ok := props["enableVmProtection"].(bool); !ok || !vmProtection {
				allRecommendations = append(allRecommendations, providers.Recommendation{
					CategoryName: providers.RecommendationCategorySecurity,
					RuleName:     "azure_vnet_vm_protection_disabled",
					Severity:     providers.RecommendationSeverityMedium,
					Savings:      0,
					Data: map[string]any{
						"resource_id":               resource.Id,
						"resource_name":             resource.Name,
						"resource_type":             resource.Type,
						"resource_region":           resource.Region,
						"service_name":              resource.ServiceName,
						"current_configuration":     "VM Protection disabled",
						"recommended_configuration": "Azure Bastion for secure VM access",
						"reason":                    "VM Protection (legacy) is disabled. Modern best practice is to deploy Azure Bastion for secure RDP/SSH access to VMs without exposing public IPs. Azure Bastion provides fully managed PaaS service for secure and seamless RDP/SSH connectivity.",
						"benefits": []string{
							"No public IPs required on VMs",
							"Secure RDP/SSH over SSL (port 443)",
							"Protection against port scanning and brute-force attacks",
							"Native integration with NSG rules",
							"Centralized access point for VM management",
							"Session recording and auditing capabilities",
							"No agent or client software required",
							"Hardened platform maintained by Microsoft",
						},
						"recommended_solution": "Deploy Azure Bastion in dedicated subnet (AzureBastionSubnet)",
						"security_improvement": "Eliminates public IP exposure for management access",
						"compliance_impact":    "Required for Zero Trust and PCI DSS network segmentation",
						"cost_impact":          "Azure Bastion Basic: ~$140/month; Standard: ~$210/month",
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}

			// Check for Service Endpoints
			serviceEndpointsConfigured := false
			if subnets, ok := props["subnets"].([]interface{}); ok {
				for _, subnet := range subnets {
					if subnetMap, ok := subnet.(map[string]interface{}); ok {
						if subnetProps, ok := subnetMap["properties"].(map[string]interface{}); ok {
							if serviceEndpoints, ok := subnetProps["serviceEndpoints"].([]interface{}); ok && len(serviceEndpoints) > 0 {
								serviceEndpointsConfigured = true
								break
							}
						}
					}
				}
			}
			if !serviceEndpointsConfigured {
				allRecommendations = append(allRecommendations, providers.Recommendation{
					CategoryName: providers.RecommendationCategoryConfiguration,
					RuleName:     "azure_vnet_service_endpoints_not_configured",
					Severity:     providers.RecommendationSeverityMedium,
					Savings:      0,
					Data: map[string]any{
						"resource_id":               resource.Id,
						"resource_name":             resource.Name,
						"resource_type":             resource.Type,
						"resource_region":           resource.Region,
						"service_name":              resource.ServiceName,
						"current_configuration":     "No service endpoints configured",
						"recommended_configuration": "Service endpoints for Azure PaaS services",
						"reason":                    "Virtual Network Service Endpoints are not configured. Service endpoints provide secure and direct connectivity to Azure services over Azure backbone network, eliminating internet exposure and improving security and performance.",
						"benefits": []string{
							"Secure direct access to Azure PaaS services (Storage, SQL, Key Vault)",
							"Traffic stays on Azure backbone (never traverses internet)",
							"No public IP required for accessing Azure services",
							"Improved latency and throughput",
							"Service firewall rules based on VNet/subnet",
							"No additional cost for service endpoints",
							"Protection against data exfiltration",
						},
						"supported_services": []string{
							"Microsoft.Storage (Azure Storage)",
							"Microsoft.Sql (Azure SQL Database)",
							"Microsoft.AzureCosmosDB",
							"Microsoft.KeyVault",
							"Microsoft.ServiceBus",
							"Microsoft.EventHub",
							"Microsoft.AzureActiveDirectory",
							"Microsoft.Web (App Service)",
						},
						"use_cases":       []string{"Database access from VNet", "Storage account access", "Key Vault secret retrieval", "Cosmos DB connectivity"},
						"security_impact": "Eliminates public internet exposure for Azure service access",
						"cost_impact":     "No additional cost for service endpoints",
						"alternative":     "Consider Private Endpoints (Private Link) for even stronger network isolation",
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}

			// Check for VNet Peering optimization
			if peerings, ok := props["virtualNetworkPeerings"].([]interface{}); ok && len(peerings) > 0 {
				for _, peering := range peerings {
					if peeringMap, ok := peering.(map[string]interface{}); ok {
						if peeringProps, ok := peeringMap["properties"].(map[string]interface{}); ok {
							// Check if gateway transit is optimally configured
							if useRemoteGateways, ok := peeringProps["useRemoteGateways"].(bool); ok && !useRemoteGateways {
								if allowGatewayTransit, ok := peeringProps["allowGatewayTransit"].(bool); !ok || !allowGatewayTransit {
									allRecommendations = append(allRecommendations, providers.Recommendation{
										CategoryName: providers.RecommendationCategoryConfiguration,
										RuleName:     "azure_vnet_gateway_transit_not_optimized",
										Severity:     providers.RecommendationSeverityLow,
										Savings:      0,
										Data: map[string]any{
											"resource_id":               resource.Id,
											"resource_name":             resource.Name,
											"resource_type":             resource.Type,
											"resource_region":           resource.Region,
											"service_name":              resource.ServiceName,
											"current_configuration":     "VNet peering without gateway transit optimization",
											"recommended_configuration": "Configure gateway transit for hub-spoke topology",
											"reason":                    "VNet peering exists but gateway transit is not optimally configured. In hub-spoke network topologies, enabling gateway transit allows spoke VNets to use the VPN/ExpressRoute gateway in the hub VNet, reducing costs and simplifying management.",
											"benefits": []string{
												"Single gateway for multiple peered VNets (hub-spoke model)",
												"Reduced gateway costs (one gateway vs. multiple)",
												"Simplified hybrid connectivity management",
												"Centralized on-premises connectivity",
												"Easier to maintain and monitor",
												"Better for large multi-VNet architectures",
											},
											"use_case":          "Hub-spoke network topology with centralized hybrid connectivity",
											"architecture_note": "Hub VNet: allowGatewayTransit=true; Spoke VNets: useRemoteGateways=true",
											"cost_impact":       "Potential savings by eliminating redundant VPN/ExpressRoute gateways (~$140-$3,500/month per gateway)",
										},
										Action:              providers.RecommendationActionModify,
										ResourceServiceName: resource.ServiceName,
										ResourceId:          resource.Id,
										ResourceType:        resource.Type,
										ResourceRegion:      resource.Region,
									})
									break
								}
							}
						}
					}
				}
			}

			// Check for DNS Configuration
			if dhcpOptions, ok := props["dhcpOptions"].(map[string]interface{}); ok {
				if dnsServers, ok := dhcpOptions["dnsServers"].([]interface{}); ok && len(dnsServers) == 0 {
					allRecommendations = append(allRecommendations, providers.Recommendation{
						CategoryName: providers.RecommendationCategoryConfiguration,
						RuleName:     "azure_vnet_custom_dns_not_configured",
						Severity:     providers.RecommendationSeverityLow,
						Savings:      0,
						Data: map[string]any{
							"resource_id":               resource.Id,
							"resource_name":             resource.Name,
							"resource_type":             resource.Type,
							"resource_region":           resource.Region,
							"service_name":              resource.ServiceName,
							"current_configuration":     "Using Azure-provided DNS",
							"recommended_configuration": "Custom DNS for enterprise scenarios",
							"reason":                    "VNet is using Azure-provided DNS. For enterprise environments with on-premises integration, Active Directory, or custom DNS requirements, configuring custom DNS servers improves name resolution and supports hybrid scenarios.",
							"benefits": []string{
								"Hybrid name resolution (Azure + on-premises)",
								"Active Directory domain integration",
								"Custom DNS forwarding rules",
								"Private DNS zones for internal resources",
								"Conditional forwarding support",
								"Better for hybrid cloud architectures",
							},
							"use_cases": []string{
								"Hybrid cloud with on-premises AD",
								"Custom internal DNS zones",
								"Private endpoint DNS resolution",
								"Cross-premises name resolution",
							},
							"alternative": "Azure Private DNS Zones for Azure-only scenarios",
							"cost_impact": "No additional cost for custom DNS configuration (DNS server costs separate)",
						},
						Action:              providers.RecommendationActionModify,
						ResourceServiceName: resource.ServiceName,
						ResourceId:          resource.Id,
						ResourceType:        resource.Type,
						ResourceRegion:      resource.Region,
					})
				}
			}

			// Check for empty or unused VNets
			if subnets, ok := props["subnets"].([]interface{}); ok && len(subnets) == 0 {
				allRecommendations = append(allRecommendations, providers.Recommendation{
					CategoryName: providers.RecommendationCategoryRightSizing,
					RuleName:     "azure_vnet_empty_no_subnets",
					Severity:     providers.RecommendationSeverityLow,
					Savings:      0,
					Data: map[string]any{
						"resource_id":     resource.Id,
						"resource_name":   resource.Name,
						"resource_type":   resource.Type,
						"resource_region": resource.Region,
						"service_name":    resource.ServiceName,
						"reason":          "Virtual Network has no subnets configured. Empty VNets provide no value and should be deleted to simplify network inventory and reduce management overhead.",
						"benefits": []string{
							"Simplified network inventory",
							"Reduced management overhead",
							"Cleaner resource organization",
							"Eliminates unused network quota consumption",
						},
						"action_note": "Verify VNet is truly unused before deletion",
					},
					Action:              providers.RecommendationActionDelete,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}

			// Check address space utilization
			if subnets, ok := props["subnets"].([]interface{}); ok && len(subnets) > 0 {
				// Calculate subnet utilization
				if addressSpace, ok := props["addressSpace"].(map[string]interface{}); ok {
					if addressPrefixes, ok := addressSpace["addressPrefixes"].([]interface{}); ok && len(addressPrefixes) > 0 {
						// Simple check: if many address prefixes but few subnets, might be over-provisioned
						if len(addressPrefixes) > 5 && len(subnets) < 3 {
							allRecommendations = append(allRecommendations, providers.Recommendation{
								CategoryName: providers.RecommendationCategoryRightSizing,
								RuleName:     "azure_vnet_address_space_overprovisioned",
								Severity:     providers.RecommendationSeverityLow,
								Savings:      0,
								Data: map[string]any{
									"resource_id":          resource.Id,
									"resource_name":        resource.Name,
									"resource_type":        resource.Type,
									"resource_region":      resource.Region,
									"service_name":         resource.ServiceName,
									"address_prefix_count": len(addressPrefixes),
									"subnet_count":         len(subnets),
									"reason":               "VNet has more address space prefixes than necessary for current subnet count. Excessive address space can lead to IP address management complexity and potential conflicts with VNet peering or VPN configurations.",
									"benefits": []string{
										"Simplified IP address management",
										"Reduced potential for address space conflicts",
										"Easier VNet peering configuration",
										"Better alignment with actual usage",
										"Simplified network documentation",
									},
									"recommendation": "Review and consolidate address space to match actual subnet requirements",
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
			}

			// Check for subnet NSG associations
			subnetWithoutNSG := false
			if subnets, ok := props["subnets"].([]interface{}); ok {
				for _, subnet := range subnets {
					if subnetMap, ok := subnet.(map[string]interface{}); ok {
						subnetName := ""
						if name, ok := subnetMap["name"].(string); ok {
							subnetName = name
						}
						// Skip special subnets that don't require NSGs
						if subnetName == "GatewaySubnet" || subnetName == "AzureFirewallSubnet" || subnetName == "AzureBastionSubnet" {
							continue
						}
						if subnetProps, ok := subnetMap["properties"].(map[string]interface{}); ok {
							if _, ok := subnetProps["networkSecurityGroup"]; !ok {
								subnetWithoutNSG = true
								break
							}
						}
					}
				}
			}
			if subnetWithoutNSG {
				allRecommendations = append(allRecommendations, providers.Recommendation{
					CategoryName: providers.RecommendationCategorySecurity,
					RuleName:     "azure_vnet_subnet_without_nsg",
					Severity:     providers.RecommendationSeverityHigh,
					Savings:      0,
					Data: map[string]any{
						"resource_id":               resource.Id,
						"resource_name":             resource.Name,
						"resource_type":             resource.Type,
						"resource_region":           resource.Region,
						"service_name":              resource.ServiceName,
						"current_configuration":     "Subnet(s) without NSG",
						"recommended_configuration": "NSG associated with every subnet",
						"reason":                    "One or more subnets do not have Network Security Groups (NSGs) associated. NSGs provide essential network-level access control and should be applied to all subnets except special-purpose subnets (GatewaySubnet, AzureFirewallSubnet, AzureBastionSubnet).",
						"benefits": []string{
							"Network-level access control and segmentation",
							"Protection against unauthorized network access",
							"Traffic filtering based on source/destination IP, port, protocol",
							"Compliance with network security requirements",
							"Logging and auditing of network traffic",
							"Defense-in-depth security architecture",
							"Support for micro-segmentation",
						},
						"security_impact":   "Critical for preventing unauthorized lateral movement and network attacks",
						"compliance_impact": "Required for PCI DSS, HIPAA, SOC 2, and Zero Trust architectures",
						"cost_impact":       "No additional cost for NSG association",
						"excluded_subnets":  []string{"GatewaySubnet", "AzureFirewallSubnet", "AzureBastionSubnet"},
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

func (s *virtualNetworkService) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, resource providers.Resource) (providers.ServiceMapApplication, error) {
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resource.Name,
			Kind:      "virtualnetwork",
			Namespace: resource.Region,
		},
		Upstreams:   []providers.UpstreamLink{},
		Downstreams: []providers.DownstreamLink{},
		Status:      "Unknown",
	}
	return app, nil
}

func (s *virtualNetworkService) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region string, resourceId string) (string, error) {
	// Azure Virtual Network logs are typically found in Log Analytics workspace
	// Format: /subscriptions/{subscription}/resourceGroups/{rg}/providers/Microsoft.Network/virtualNetworks/{name}
	return resourceId, nil
}
