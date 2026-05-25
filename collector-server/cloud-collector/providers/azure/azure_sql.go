package azure

import (
	"context"
	"errors"
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/sql/armsql"
)

type sqlDatabaseService struct {
}

func (s *sqlDatabaseService) Name() string {
	return "Microsoft.Sql/servers"
}

// Scope returns the service scope - this is a regional service
func (s *sqlDatabaseService) Scope() ServiceScope {
	return ServiceScopeRegional
}

func (s *sqlDatabaseService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
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
		serversClient, err := armsql.NewServersClient(subID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to create sql servers client: %w", err)
		}

		databasesClient, err := armsql.NewDatabasesClient(subID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to create sql databases client: %w", err)
		}

		serversPager := serversClient.NewListPager(nil)
		for serversPager.More() {
			serversPage, err := serversPager.NextPage(ctx.GetContext())
			if err != nil {
				return nil, fmt.Errorf("failed to list sql servers: %w", err)
			}

			for _, server := range serversPage.Value {
				// Extract resource group name from server ID
				resourceGroup, err := extractResourceGroup(*server.ID)
				if err != nil {
					return nil, err
				}

				databasesPager := databasesClient.NewListByServerPager(resourceGroup, *server.Name, nil)
				for databasesPager.More() {
					databasesPage, err := databasesPager.NextPage(ctx.GetContext())
					if err != nil {
						return nil, fmt.Errorf("failed to list sql databases: %w", err)
					}
					for _, db := range databasesPage.Value {
						status := providers.ResourceStatusUnknown
						if db.Properties.Status != nil {
							if val, ok := nbStatusFromAzureProvisioningState[string(*db.Properties.Status)]; ok {
								status = val
							}
						}
						createdAt := time.Time{}
						if db.Properties.CreationDate != nil {
							createdAt = *db.Properties.CreationDate
						}
						allResources = append(allResources, providers.Resource{
							Id:          *db.ID,
							Name:        *db.Name,
							Type:        *db.Type,
							Region:      *db.Location,
							Tags:        toAzureTags(db.Tags),
							Meta:        structToMap(db),
							Status:      status,
							CreatedAt:   createdAt,
							Arn:         *db.ID,
							ServiceName: s.Name(),
						})
					}
				}
			}
		}
	}
	return allResources, nil
}

func (s *sqlDatabaseService) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAzureMonitorMetrics(ctx, account, filter)
}

func (s *sqlDatabaseService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	recommendations := []providers.Recommendation{}
	cred, _, err := getAzureCredsForAccount(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("failed to create azure credential: %w", err)
	}
	checkedServers := make(map[string]bool)
	pricingCache := GetPricingCache()
	for _, resource := range existingResources {
		if len(resource.Tags) == 0 {
			recommendations = append(recommendations, providers.Recommendation{
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
					"reason":          "SQL database has no tags applied. Tags are essential for cost allocation, environment identification, compliance tracking, database lifecycle management, and tracking database ownership across development teams.",
					"benefits": []string{
						"Cost tracking and chargeback by project/application",
						"Environment identification (dev/test/staging/prod)",
						"Compliance and audit trail tracking",
						"Database lifecycle management and retention policies",
						"Ownership and responsibility assignment",
						"Automated backup and maintenance scheduling by tag",
					},
					"recommended_tags": []string{"environment", "owner", "application", "cost-center", "backup-policy", "compliance-level", "data-classification"},
				},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		if properties, ok := resource.Meta["properties"].(map[string]interface{}); ok {
			// Check for Geo-Redundant Backups
			if geoRedundantBackup, ok := properties["isGeoBackup"].(bool); !ok || !geoRedundantBackup {
				// Calculate cost impact using dynamic pricing
				lrsBackupPrice, err1 := pricingCache.GetBackupStoragePrice(ctx, "LRS", resource.Region)
				grsBackupPrice, err2 := pricingCache.GetBackupStoragePrice(ctx, "GRS", resource.Region)

				costImpact := "GRS backups cost ~2x LRS backups (~$0.20/GB vs $0.10/GB per month)"
				if err1 == nil && err2 == nil {
					costImpact = fmt.Sprintf("GRS backups cost $%.3f/GB vs $%.3f/GB per month for LRS (%.1fx cost)",
						grsBackupPrice, lrsBackupPrice, grsBackupPrice/lrsBackupPrice)
				}

				recommendations = append(recommendations, providers.Recommendation{
					CategoryName: providers.RecommendationCategoryConfiguration,
					RuleName:     "azure_sql_geo_redundant_backups_disabled",
					Severity:     providers.RecommendationSeverityHigh,
					Savings:      0,
					Data: map[string]any{
						"resource_id":               resource.Id,
						"resource_name":             resource.Name,
						"resource_type":             resource.Type,
						"resource_region":           resource.Region,
						"service_name":              resource.ServiceName,
						"current_backup_policy":     "Locally redundant backups only",
						"recommended_backup_policy": "Geo-redundant backups",
						"reason":                    "Database backups are not geo-redundant. Geo-redundant backup provides protection against regional disasters by replicating backups to a paired Azure region. Essential for business continuity and disaster recovery (BCDR) compliance.",
						"benefits": []string{
							"Protection against regional disasters and outages",
							"99.99999999999999% (16 nines) backup durability",
							"Geo-restore capability to any Azure region",
							"Compliance with BCDR requirements",
							"7-35 day point-in-time restore retention",
							"No additional management overhead",
						},
						"cost_impact":       costImpact,
						"rpo_note":          "Recovery Point Objective (RPO): Up to 1 hour with geo-replication",
						"compliance_impact": "Required for SOC 2, ISO 27001, and enterprise BCDR policies",
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}

			// Check for Public Network Access
			if publicNetworkAccess, ok := properties["publicNetworkAccess"].(string); ok && publicNetworkAccess != "Disabled" {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName: providers.RecommendationCategorySecurity,
					RuleName:     "azure_sql_public_network_access_enabled",
					Severity:     providers.RecommendationSeverityCritical,
					Savings:      0,
					Data: map[string]any{
						"resource_id":        resource.Id,
						"resource_name":      resource.Name,
						"resource_type":      resource.Type,
						"resource_region":    resource.Region,
						"service_name":       resource.ServiceName,
						"current_access":     "Public network access enabled",
						"recommended_access": "Private endpoint or VNet service endpoint only",
						"reason":             "SQL database allows public internet access. This exposes the database to potential attacks including SQL injection, brute force password attempts, and unauthorized access. Disable public access and use Private Endpoints or VNet service endpoints for secure, private connectivity.",
						"benefits": []string{
							"Eliminates public internet exposure",
							"Protection against brute force attacks",
							"Defense against SQL injection from external sources",
							"Network isolation and segmentation",
							"Private IP addressing within your VNet",
							"No data exfiltration over public internet",
							"Compliance with Zero Trust security model",
						},
						"security_risks":     []string{"SQL injection attacks", "brute force attempts", "credential stuffing", "data exfiltration", "DDoS exposure"},
						"compliance_impact":  "Required for PCI DSS, HIPAA, SOC 2, and Zero Trust architecture",
						"alternative_access": "Use Private Endpoints ($0.01/hour) or VNet service endpoints (free)",
						"cost_impact":        "Private Endpoints: ~$7/month per endpoint",
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}

			// Check for Microsoft Entra ID Admin
			if administrators, ok := properties["administrators"].(map[string]interface{}); ok {
				if len(administrators) == 0 {
					recommendations = append(recommendations, providers.Recommendation{
						CategoryName: providers.RecommendationCategorySecurity,
						RuleName:     "azure_sql_entra_id_admin_not_configured",
						Severity:     providers.RecommendationSeverityHigh,
						Savings:      0,
						Data: map[string]any{
							"resource_id":                resource.Id,
							"resource_name":              resource.Name,
							"resource_type":              resource.Type,
							"resource_region":            resource.Region,
							"service_name":               resource.ServiceName,
							"current_authentication":     "SQL authentication only",
							"recommended_authentication": "Microsoft Entra ID (Azure AD) authentication",
							"reason":                     "SQL server does not have a Microsoft Entra ID (Azure AD) administrator configured. Azure AD authentication provides centralized identity management, multi-factor authentication (MFA), conditional access policies, and eliminates the need to manage SQL logins and passwords.",
							"benefits": []string{
								"Centralized identity and access management",
								"Multi-factor authentication (MFA) support",
								"Conditional access policies (location, device compliance)",
								"Passwordless authentication options",
								"Role-based access control (RBAC) integration",
								"Group-based permissions management",
								"Comprehensive audit logs in Azure AD",
								"Eliminates SQL login password management",
							},
							"security_risks":    []string{"password-based attacks", "weak passwords", "credential sharing", "no MFA enforcement"},
							"compliance_impact": "Required for Zero Trust security model and enterprise compliance",
							"migration_note":    "Azure AD auth can coexist with SQL authentication during migration",
							"cost_impact":       "No additional cost",
						},
						Action:              providers.RecommendationActionModify,
						ResourceServiceName: resource.ServiceName,
						ResourceId:          resource.Id,
						ResourceType:        resource.Type,
						ResourceRegion:      resource.Region,
					})
				}
			}

			// Check for Long-Term Retention
			if retentionDays, ok := properties["longTermRetentionBackupResourceId"].(string); ok && retentionDays == "" {
				// Calculate LTR storage cost using dynamic pricing
				ltrPrice, err := pricingCache.GetBackupStoragePrice(ctx, "RA-GRS", resource.Region)
				costImpact := "LTR storage: ~$0.10/GB/month (RA-GRS pricing)"
				if err == nil {
					costImpact = fmt.Sprintf("LTR storage: $%.3f/GB/month (RA-GRS pricing)", ltrPrice)
				}

				recommendations = append(recommendations, providers.Recommendation{
					CategoryName: providers.RecommendationCategoryConfiguration,
					RuleName:     "azure_sql_long_term_retention_not_configured",
					Severity:     providers.RecommendationSeverityMedium,
					Savings:      0,
					Data: map[string]any{
						"resource_id":           resource.Id,
						"resource_name":         resource.Name,
						"resource_type":         resource.Type,
						"resource_region":       resource.Region,
						"service_name":          resource.ServiceName,
						"current_retention":     "Short-term only (7-35 days)",
						"recommended_retention": "Long-term retention (up to 10 years)",
						"reason":                "Database does not have long-term retention (LTR) configured. LTR enables backups to be retained for up to 10 years, essential for compliance requirements (SOX, HIPAA, GDPR) and long-term data recovery scenarios.",
						"benefits": []string{
							"Backup retention up to 10 years",
							"Compliance with regulatory requirements (SOX, HIPAA, GDPR)",
							"Protection against accidental deletion or corruption",
							"Point-in-time restore for extended periods",
							"Independent of database lifecycle",
							"Automated backup management",
						},
						"retention_options": []string{"Weekly backups", "Monthly backups", "Yearly backups"},
						"compliance_impact": "Required for SOX (7 years), HIPAA (6 years), GDPR (varies by region)",
						"cost_impact":       costImpact,
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}

			// Check for Transparent Data Encryption (TDE)
			// TDE should be enabled by default, but verify
			if tdeStatus, ok := properties["transparentDataEncryption"].(map[string]interface{}); ok {
				if state, ok := tdeStatus["state"].(string); ok && state != "Enabled" {
					recommendations = append(recommendations, providers.Recommendation{
						CategoryName: providers.RecommendationCategorySecurity,
						RuleName:     "azure_sql_transparent_data_encryption_disabled",
						Severity:     providers.RecommendationSeverityCritical,
						Savings:      0,
						Data: map[string]any{
							"resource_id":            resource.Id,
							"resource_name":          resource.Name,
							"resource_type":          resource.Type,
							"resource_region":        resource.Region,
							"service_name":           resource.ServiceName,
							"current_encryption":     "Disabled",
							"recommended_encryption": "Transparent Data Encryption (TDE) enabled",
							"reason":                 "Transparent Data Encryption (TDE) is disabled. TDE encrypts database files at rest (data files, log files, backups) to protect against unauthorized access to storage media. This is a critical security control for protecting sensitive data.",
							"benefits": []string{
								"Real-time encryption/decryption of data at rest",
								"Protects data files, log files, and backups",
								"AES-256 encryption algorithm",
								"No application code changes required",
								"Compliance with security standards (PCI DSS, HIPAA)",
								"Protection against theft of storage media",
								"Transparent to applications (no performance impact)",
							},
							"encryption_scope":   []string{"Data files (.mdf)", "Log files (.ldf)", "Backup files", "Tempdb"},
							"compliance_impact":  "Required for PCI DSS 3.2, HIPAA, SOC 2, and GDPR compliance",
							"performance_impact": "Minimal (<3% overhead in most scenarios)",
							"cost_impact":        "No additional cost (included in database pricing)",
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

		if sku, ok := resource.Meta["sku"].(map[string]interface{}); ok {
			// Check for Storage Auto-Growth
			if capacity, ok := sku["capacity"].(float64); ok && capacity > 0 {
				if properties, ok := resource.Meta["properties"].(map[string]interface{}); ok {
					if maxSizeBytes, ok := properties["maxSizeBytes"].(float64); ok && maxSizeBytes <= 0 {
						// Calculate storage cost using dynamic pricing
						storagePrice, err := pricingCache.GetSQLStoragePrice(ctx, "vCore", resource.Region)
						costImpact := "Storage costs increase as database grows (~$0.115/GB/month for vCore)"
						if err == nil {
							costImpact = fmt.Sprintf("Storage costs increase as database grows ($%.3f/GB/month for vCore)", storagePrice)
						}

						recommendations = append(recommendations, providers.Recommendation{
							CategoryName: providers.RecommendationCategoryConfiguration,
							RuleName:     "azure_sql_storage_auto_growth_disabled",
							Severity:     providers.RecommendationSeverityMedium,
							Savings:      0,
							Data: map[string]any{
								"resource_id":         resource.Id,
								"resource_name":       resource.Name,
								"resource_type":       resource.Type,
								"resource_region":     resource.Region,
								"service_name":        resource.ServiceName,
								"current_setting":     "Fixed maximum size",
								"recommended_setting": "Auto-growth enabled",
								"reason":              "Database has a fixed maximum size without auto-growth. If the database reaches this limit, write operations will fail, causing application outages. Enable auto-growth to prevent out-of-space errors.",
								"benefits": []string{
									"Prevents database out-of-space errors",
									"Automatic storage expansion as needed",
									"No application downtime from space issues",
									"Gradual cost increase vs sudden failure",
									"Configurable maximum limits",
								},
								"risk":        "Database will become read-only when reaching max size",
								"cost_impact": costImpact,
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
		// Check for Advanced Data Security
		parts := strings.Split(resource.Id, "/")
		if len(parts) >= 9 {
			subscriptionID := parts[2]
			resourceGroup := parts[4]
			serverName := parts[8]
			serverID := strings.Join(parts[:9], "/")

			if !checkedServers[serverID] {
				policiesClient, err := armsql.NewServerSecurityAlertPoliciesClient(subscriptionID, cred, getAzureAuditOpts(ctx))
				if err == nil {
					policy, err := policiesClient.Get(ctx.GetContext(), resourceGroup, serverName, "Default", nil)
					if err == nil && policy.Properties.State != nil && *policy.Properties.State != armsql.SecurityAlertsPolicyStateEnabled {
						recommendations = append(recommendations, providers.Recommendation{
							CategoryName: providers.RecommendationCategorySecurity,
							RuleName:     "azure_sql_advanced_data_security_disabled",
							Severity:     providers.RecommendationSeverityHigh,
							Savings:      0,
							Data: map[string]any{
								"resource_id":          serverID,
								"resource_name":        serverName,
								"resource_type":        "Microsoft.Sql/servers",
								"resource_region":      resource.Region,
								"service_name":         resource.ServiceName,
								"current_security":     "Advanced Data Security (ADS) disabled",
								"recommended_security": "Advanced Data Security enabled",
								"reason":               "Advanced Data Security (formerly Advanced Threat Protection) is disabled. ADS provides advanced SQL threat detection, vulnerability assessment, and data discovery & classification. Essential for identifying and mitigating database security threats in real-time.",
								"benefits": []string{
									"SQL injection attack detection",
									"Anomalous database access patterns detection",
									"Brute force attack identification",
									"Vulnerability assessment and remediation guidance",
									"Data discovery and classification (PII, sensitive data)",
									"Compliance reporting (GDPR, HIPAA, PCI DSS)",
									"Security alerts and recommendations",
								},
								"features":                  []string{"Threat Detection", "Vulnerability Assessment", "Data Discovery & Classification"},
								"security_threats_detected": []string{"SQL injection", "SQL injection vulnerability", "Anomalous database access", "Potential unsafe action", "Brute force attacks"},
								"compliance_impact":         "Required for GDPR, HIPAA, and PCI DSS compliance monitoring",
								"cost_impact":               "~$15/server/month + $0.003/GB scanned for vulnerability assessment",
							},
							Action:              providers.RecommendationActionModify,
							ResourceServiceName: "Microsoft.Sql/servers",
							ResourceId:          serverID,
							ResourceType:        "Microsoft.Sql/servers",
							ResourceRegion:      resource.Region,
						})
					}
				}
				checkedServers[serverID] = true
			}
		}

		// Check for DTU → vCore pricing model upgrade opportunities
		if properties, ok := resource.Meta["properties"].(map[string]interface{}); ok {
			if sku, ok := resource.Meta["sku"].(map[string]interface{}); ok {
				if skuName, ok := sku["name"].(string); ok {
					// Check for DTU-based databases that can benefit from vCore model
					pricingModelOptimization := getSQLPricingModelOptimization(ctx, pricingCache, skuName, properties, resource.Region)
					if pricingModelOptimization != nil {
						recommendations = append(recommendations, providers.Recommendation{
							CategoryName: providers.RecommendationCategoryInfraUpgrade,
							RuleName:     "azure_sql_database_pricing_model_upgrade",
							Severity:     providers.RecommendationSeverityMedium,
							Savings:      pricingModelOptimization["estimated_savings_monthly"].(float64),
							Data: map[string]any{
								"resource_id":               resource.Id,
								"resource_name":             resource.Name,
								"resource_type":             resource.Type,
								"resource_region":           resource.Region,
								"service_name":              resource.ServiceName,
								"current_pricing_model":     pricingModelOptimization["current_pricing_model"],
								"current_sku":               skuName,
								"current_tier":              pricingModelOptimization["current_tier"],
								"recommended_pricing_model": pricingModelOptimization["recommended_pricing_model"],
								"recommended_sku":           pricingModelOptimization["recommended_sku"],
								"recommended_tier":          pricingModelOptimization["recommended_tier"],
								"reason":                    pricingModelOptimization["reason"],
								"benefits":                  pricingModelOptimization["benefits"],
								"estimated_savings_type":    "monthly",
								"savings_source":            "Azure SQL Database pricing comparison - DTU vs vCore",
								"migration_consideration":   pricingModelOptimization["migration_consideration"],
								"compute_model_flexibility": pricingModelOptimization["compute_model_flexibility"],
							},
							Action:              providers.RecommendationActionModify,
							ResourceServiceName: resource.ServiceName,
							ResourceId:          resource.Id,
							ResourceType:        resource.Type,
							ResourceRegion:      resource.Region,
						})
					}

					// Check for compute tier optimization (Provisioned → Serverless for low-usage databases)
					computeTierOptimization := getSQLComputeTierOptimization(ctx, pricingCache, skuName, properties, resource.Region)
					if computeTierOptimization != nil {
						recommendations = append(recommendations, providers.Recommendation{
							CategoryName: providers.RecommendationCategoryRightSizing,
							RuleName:     "azure_sql_serverless_optimization",
							Severity:     providers.RecommendationSeverityMedium,
							Savings:      computeTierOptimization["estimated_savings_monthly"].(float64),
							Data: map[string]any{
								"resource_id":              resource.Id,
								"resource_name":            resource.Name,
								"resource_type":            resource.Type,
								"resource_region":          resource.Region,
								"service_name":             resource.ServiceName,
								"current_compute_tier":     computeTierOptimization["current_compute_tier"],
								"recommended_compute_tier": computeTierOptimization["recommended_compute_tier"],
								"reason":                   computeTierOptimization["reason"],
								"benefits":                 computeTierOptimization["benefits"],
								"estimated_savings_type":   "monthly",
								"savings_source":           "Azure SQL Serverless pricing - pay-per-second billing",
								"auto_pause_capability":    computeTierOptimization["auto_pause_capability"],
								"ideal_workload":           computeTierOptimization["ideal_workload"],
							},
							Action:              providers.RecommendationActionModify,
							ResourceServiceName: resource.ServiceName,
							ResourceId:          resource.Id,
							ResourceType:        resource.Type,
							ResourceRegion:      resource.Region,
						})
					}

					// Check for RightSizing opportunities - oversized databases
					rightSizingRecommendation := getSQLDatabaseRightSizing(ctx, pricingCache, skuName, properties, resource.Region)
					if rightSizingRecommendation != nil {
						recommendations = append(recommendations, providers.Recommendation{
							CategoryName: providers.RecommendationCategoryRightSizing,
							RuleName:     rightSizingRecommendation["rule_name"].(string),
							Severity:     providers.RecommendationSeverityMedium,
							Savings:      rightSizingRecommendation["estimated_savings_monthly"].(float64),
							Data: map[string]any{
								"resource_id":            resource.Id,
								"resource_name":          resource.Name,
								"resource_type":          resource.Type,
								"resource_region":        resource.Region,
								"service_name":           resource.ServiceName,
								"current_sku":            skuName,
								"recommended_sku":        rightSizingRecommendation["recommended_sku"],
								"current_tier":           rightSizingRecommendation["current_tier"],
								"recommended_tier":       rightSizingRecommendation["recommended_tier"],
								"reason":                 rightSizingRecommendation["reason"],
								"benefits":               rightSizingRecommendation["benefits"],
								"estimated_savings_type": "monthly",
								"savings_source":         rightSizingRecommendation["savings_source"],
								"workload_suitability":   rightSizingRecommendation["workload_suitability"],
								"performance_impact":     rightSizingRecommendation["performance_impact"],
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

		// Check for missing Azure Monitor metric alerts
		sqlAlarmTemplates, loadErr := LoadAzureAlarmTemplates("sql")
		if loadErr != nil {
			ctx.GetLogger().Warn("Failed to load SQL alarm templates", "error", loadErr, "resourceId", resource.Id)
		} else {
			for _, template := range sqlAlarmTemplates {
				// Check if we should recommend this alarm based on metric type and conditions
				if !ShouldRecommendAzureAlarm(resource, template) {
					continue
				}

				// Check if alert is missing
				if !IsAzureAlertMissing(resource, template) {
					continue
				}

				// Calculate threshold based on resource properties
				threshold, threshErr := CalculateAzureThreshold(resource, template)
				if threshErr != nil {
					ctx.GetLogger().Warn("Error calculating Azure threshold", "error", threshErr, "template", template.Name, "resourceId", resource.Id)
					continue
				}

				// Build alarm configuration for the recommendation data
				alarmConfig := buildAzureAlarmConfig(resource, template, threshold)

				recommendations = append(recommendations, providers.Recommendation{
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
	return recommendations, nil
}

// getSQLPricingModelOptimization returns DTU → vCore upgrade recommendations
func getSQLPricingModelOptimization(ctx providers.CloudProviderContext, pricingCache *PricingCache, skuName string, properties map[string]interface{}, region string) map[string]interface{} {
	// Detect DTU-based SKUs (Basic, Standard, Premium)
	// DTU model: fixed compute/memory/IO bundle
	// vCore model: flexible compute, memory, and storage selection

	skuUpper := strings.ToUpper(skuName)

	// Basic tier → General Purpose vCore
	if strings.HasPrefix(skuUpper, "BASIC") {

		basicPrice, err1 := pricingCache.GetSQLComputePrice(ctx, "Basic", skuName, region)
		vCorePrice, err2 := pricingCache.GetSQLComputePrice(ctx, "GP", "GP_Gen5_2", region)

		estimatedSavings := 20.0 // Fallback
		if err1 == nil && err2 == nil {
			// Convert hourly to monthly (730 hours)
			estimatedSavings = (basicPrice - vCorePrice) * 730
		} else {
			ctx.GetLogger().Warn("Failed to get dynamic pricing for SQL model optimization, using fallback.", "sku", skuName, "region", region, "basicPriceErr", err1, "vCorePriceErr", err2)
		}
		if basicPrice > 0 && vCorePrice > 0 {
			// Convert hourly to monthly (730 hours)
			estimatedSavings = (basicPrice - vCorePrice) * 730
		}
		return map[string]interface{}{
			"current_pricing_model":     "DTU-based",
			"current_tier":              "Basic",
			"recommended_pricing_model": "vCore-based",
			"recommended_sku":           "GP_Gen5_2",
			"recommended_tier":          "General Purpose (Gen5)",
			"estimated_savings_monthly": estimatedSavings,
			"reason":                    "Database is using DTU-based Basic tier. vCore model provides better price-performance, more flexibility in resource allocation, and access to latest hardware generations. For Basic workloads, General Purpose vCore (2 cores) provides similar performance with better scalability options.",
			"benefits": []string{
				"15-25% cost savings for equivalent performance",
				"Flexible compute and storage scaling",
				"Latest Gen5 hardware with better performance",
				"Reserved capacity pricing options (up to 80% savings)",
				"Hybrid benefit support for additional savings",
				"Better control over compute, memory, and IOPS",
			},
			"migration_consideration":   "vCore allows independent scaling of compute and storage, better suited for production workloads",
			"compute_model_flexibility": "Can choose provisioned or serverless compute",
		}
	}

	// Standard tier → General Purpose vCore
	if strings.HasPrefix(skuUpper, "S") && (strings.Contains(skuUpper, "S0") || strings.Contains(skuUpper, "S1") || strings.Contains(skuUpper, "S2") || strings.Contains(skuUpper, "S3")) {
		// Calculate savings using dynamic pricing
		stdPrice, _ := pricingCache.GetSQLComputePrice(ctx, "Standard", skuName, region)
		vCorePrice, _ := pricingCache.GetSQLComputePrice(ctx, "GP", "GP_Gen5_2", region)

		estimatedSavings := 40.0 // Fallback
		if stdPrice > 0 && vCorePrice > 0 {
			estimatedSavings = (stdPrice - vCorePrice) * 730
		}

		return map[string]interface{}{
			"current_pricing_model":     "DTU-based",
			"current_tier":              "Standard (DTU)",
			"recommended_pricing_model": "vCore-based",
			"recommended_sku":           "GP_Gen5_2",
			"recommended_tier":          "General Purpose (Gen5)",
			"estimated_savings_monthly": estimatedSavings,
			"reason":                    "Database uses DTU-based Standard tier. vCore General Purpose tier offers better price-performance ratio, especially with reserved capacity. For S0-S3 workloads, 2 vCores provides comparable or better performance.",
			"benefits": []string{
				"20-30% cost savings",
				"Better resource allocation control",
				"Up to 80% additional savings with 3-year reserved capacity",
				"Azure Hybrid Benefit for SQL Server licenses",
				"Latest processor generations",
				"Geo-replication at lower cost",
			},
			"migration_consideration":   "vCore model recommended for all production databases - better performance predictability",
			"compute_model_flexibility": "Serverless option available for intermittent workloads (auto-pause during inactivity)",
		}
	}

	// Premium tier → Business Critical vCore
	if strings.HasPrefix(skuUpper, "P") {
		// Calculate savings using dynamic pricing
		premiumPrice, _ := pricingCache.GetSQLComputePrice(ctx, "Premium", skuName, region)
		bcPrice, _ := pricingCache.GetSQLComputePrice(ctx, "BC", "BC_Gen5_4", region)

		estimatedSavings := 100.0 // Fallback
		if premiumPrice > 0 && bcPrice > 0 {
			estimatedSavings = (premiumPrice - bcPrice) * 730
		}

		return map[string]interface{}{
			"current_pricing_model":     "DTU-based",
			"current_tier":              "Premium (DTU)",
			"recommended_pricing_model": "vCore-based",
			"recommended_sku":           "BC_Gen5_4",
			"recommended_tier":          "Business Critical (Gen5)",
			"estimated_savings_monthly": estimatedSavings,
			"reason":                    "Database uses DTU-based Premium tier. vCore Business Critical tier provides equivalent high-performance features (in-memory OLTP, higher IOPS) with better cost efficiency and flexibility. Premium P1-P4 maps well to Business Critical 2-4 vCores.",
			"benefits": []string{
				"25-35% cost savings",
				"Same in-memory OLTP support",
				"Higher IOPS and throughput capabilities",
				"Reserved capacity discounts (up to 80%)",
				"Hybrid Benefit for additional 40% savings",
				"Local SSD storage included",
				"Built-in high availability",
			},
			"migration_consideration":   "Business Critical vCore provides identical or better performance with lower cost",
			"compute_model_flexibility": "Provisioned compute for mission-critical workloads",
		}
	}

	return nil
}

// getSQLComputeTierOptimization returns provisioned → serverless recommendations
func getSQLComputeTierOptimization(ctx providers.CloudProviderContext, pricingCache *PricingCache, skuName string, properties map[string]interface{}, region string) map[string]interface{} {
	skuUpper := strings.ToUpper(skuName)

	// Check if database is using provisioned compute (General Purpose vCore)
	// Serverless is ideal for intermittent, unpredictable usage patterns
	if strings.Contains(skuUpper, "GP_") && !strings.Contains(skuUpper, "SERVERLESS") {
		// Only recommend serverless for Gen5 2-16 vCores (serverless range)
		if strings.Contains(skuUpper, "GEN5") {
			// Calculate potential savings
			// Serverless saves 60-70% during idle periods (assume 50% idle time on average)
			provisionedPrice, _ := pricingCache.GetSQLComputePrice(ctx, "GP", skuName, region)

			estimatedSavings := 60.0 // Fallback
			if provisionedPrice > 0 {
				// Assume 50% idle time on average for intermittent workloads
				// Serverless only charges when active
				estimatedSavings = provisionedPrice * 730 * 0.5
			}

			return map[string]interface{}{
				"current_compute_tier":      "Provisioned",
				"recommended_compute_tier":  "Serverless",
				"estimated_savings_monthly": estimatedSavings,
				"reason":                    "Database uses provisioned compute tier with continuous billing. If this database has intermittent or unpredictable usage patterns (dev/test, low-traffic apps, seasonal workloads), Serverless compute tier can save 60-70% during idle periods with auto-pause feature.",
				"benefits": []string{
					"Pay-per-second billing (only when active)",
					"Auto-pause during idle periods (no compute charges)",
					"Auto-resume on connection",
					"60-70% cost savings for intermittent workloads",
					"Same performance capabilities as provisioned",
					"No code changes required",
				},
				"auto_pause_capability": "Database automatically pauses after 1 hour of inactivity (configurable)",
				"ideal_workload":        "Development/test databases, low-traffic applications, seasonal workloads, PoC/demo environments",
			}
		}
	}

	return nil
}

// getSQLDatabaseRightSizing analyzes database SKU and suggests rightsizing opportunities
func getSQLDatabaseRightSizing(ctx providers.CloudProviderContext, pricingCache *PricingCache, skuName string, properties map[string]interface{}, region string) map[string]interface{} {
	skuUpper := strings.ToUpper(skuName)

	// Check for oversized vCore databases (large core count that could be reduced)
	// GP_Gen5_8 or higher can often be reduced based on actual usage
	if strings.Contains(skuUpper, "GP_GEN5_") {
		// Extract vCore count from SKU name (e.g., GP_Gen5_8 → 8 cores)
		if strings.Contains(skuUpper, "_8") || strings.Contains(skuUpper, "_16") || strings.Contains(skuUpper, "_24") || strings.Contains(skuUpper, "_32") {
			// Get vCore pricing dynamically
			vCorePrice, _ := pricingCache.GetSQLComputePrice(ctx, "GP", "GP_Gen5_2", region)
			savingsPerCore := 73.0 // Fallback
			if vCorePrice > 0 {
				savingsPerCore = vCorePrice * 730 // Convert hourly to monthly
			}

			if strings.Contains(skuUpper, "_32") {
				// Recommend 8 cores instead of 32
				return map[string]interface{}{
					"rule_name":                 "azure_sql_database_oversized_compute",
					"current_tier":              "General Purpose Gen5 32 vCores",
					"current_sku":               skuName,
					"recommended_tier":          "General Purpose Gen5 8 vCores",
					"recommended_sku":           "GP_Gen5_8",
					"estimated_savings_monthly": savingsPerCore * 24, // Saving 24 cores worth
					"reason":                    "Database is using 32 vCores. Unless you have sustained high CPU utilization (>70%), downsizing to 8 vCores can provide significant cost savings while maintaining adequate performance for substantial workloads.",
					"benefits": []string{
						"75% cost reduction on compute",
						"Same Gen5 hardware and features",
						"Maintains all General Purpose capabilities",
						"Can scale back up if needed",
						"Storage charged separately (no change)",
					},
					"savings_source":       "Azure SQL vCore pricing: ~$73/vCore/month → $1,752/month savings (24 vCores @ $73)",
					"workload_suitability": "Suitable for high-throughput OLTP workloads that don't require extreme concurrency",
					"performance_impact":   "Reduced concurrency. Monitor CPU utilization before downsizing.",
				}
			}

			if strings.Contains(skuUpper, "_24") {
				// Recommend 8 cores instead of 24
				return map[string]interface{}{
					"rule_name":                 "azure_sql_database_oversized_compute",
					"current_tier":              "General Purpose Gen5 24 vCores",
					"current_sku":               skuName,
					"recommended_tier":          "General Purpose Gen5 8 vCores",
					"recommended_sku":           "GP_Gen5_8",
					"estimated_savings_monthly": savingsPerCore * 16, // Saving 16 cores worth
					"reason":                    "Database is using 24 vCores. Unless you have sustained high CPU utilization (>70%), downsizing to 8 vCores can provide significant cost savings.",
					"benefits": []string{
						"66% cost reduction on compute",
						"Same Gen5 hardware and features",
						"Maintains all General Purpose capabilities",
						"Can scale back up if needed",
						"Storage charged separately (no change)",
					},
					"savings_source":       "Azure SQL vCore pricing: ~$73/vCore/month → $1,168/month savings (16 vCores @ $73)",
					"workload_suitability": "Suitable for moderate to high OLTP workloads",
					"performance_impact":   "Reduced concurrency. Monitor CPU utilization before downsizing.",
				}
			}

			if strings.Contains(skuUpper, "_16") {
				// Recommend 4 cores instead of 16
				return map[string]interface{}{
					"rule_name":                 "azure_sql_database_oversized_compute",
					"current_tier":              "General Purpose Gen5 16 vCores",
					"current_sku":               skuName,
					"recommended_tier":          "General Purpose Gen5 4 vCores",
					"recommended_sku":           "GP_Gen5_4",
					"estimated_savings_monthly": savingsPerCore * 12, // Saving 12 cores worth
					"reason":                    "Database is using 16 vCores. Unless you have sustained high CPU utilization (>70%), most OLTP workloads perform well with 4 vCores. Monitor CPU, DTU, or vCore utilization to verify if downsizing is appropriate.",
					"benefits": []string{
						"75% cost reduction on compute",
						"Same Gen5 hardware and features",
						"Maintains all General Purpose capabilities",
						"Can scale back up if needed",
						"Storage charged separately (no change)",
					},
					"savings_source":       "Azure SQL vCore pricing: ~$73/vCore/month → $876/month savings (12 vCores @ $73)",
					"workload_suitability": "Suitable for most OLTP workloads with moderate concurrency",
					"performance_impact":   "Reduced concurrency (240 vs 960 workers). Monitor CPU utilization before downsizing.",
				}
			}

			if strings.Contains(skuUpper, "_8") {
				return map[string]interface{}{
					"rule_name":                 "azure_sql_database_oversized_compute",
					"current_tier":              "General Purpose Gen5 8 vCores",
					"current_sku":               skuName,
					"recommended_tier":          "General Purpose Gen5 4 vCores",
					"recommended_sku":           "GP_Gen5_4",
					"estimated_savings_monthly": savingsPerCore * 4, // Saving 4 cores
					"reason":                    "Database is using 8 vCores. If CPU utilization is consistently below 50%, downsizing to 4 vCores can provide significant cost savings while maintaining adequate performance for most workloads.",
					"benefits": []string{
						"50% cost reduction on compute",
						"Same hardware generation and features",
						"Adequate for moderate workloads",
						"Easy to scale up if needed",
						"No change to storage pricing",
					},
					"savings_source":       "Azure SQL vCore pricing: ~$73/vCore/month → $292/month savings (4 vCores @ $73)",
					"workload_suitability": "Suitable for moderate OLTP workloads, dev/test, smaller production apps",
					"performance_impact":   "Reduced max workers (120 vs 240). Verify CPU <50% before downsizing.",
				}
			}
		}
	}

	// Check for Business Critical that could use General Purpose
	// BC is 2.7x more expensive than GP
	if strings.Contains(skuUpper, "BC_GEN5_") {
		// Calculate savings using dynamic pricing
		bcPrice, _ := pricingCache.GetSQLComputePrice(ctx, "BC", skuName, region)
		recommendedSKU := strings.Replace(skuName, "BC_", "GP_", 1)
		gpPrice, _ := pricingCache.GetSQLComputePrice(ctx, "GP", recommendedSKU, region)

		estimatedSavings := 250.0 // Fallback
		if bcPrice > 0 && gpPrice > 0 {
			estimatedSavings = (bcPrice - gpPrice) * 730
		}

		return map[string]interface{}{
			"rule_name":                 "azure_sql_business_critical_to_general_purpose",
			"current_tier":              "Business Critical (High Performance)",
			"current_sku":               skuName,
			"recommended_tier":          "General Purpose",
			"recommended_sku":           recommendedSKU,
			"estimated_savings_monthly": estimatedSavings,
			"reason":                    "Database is using Business Critical tier which costs 2.7x more than General Purpose. Unless you require local SSD storage, in-memory OLTP, or read replicas, General Purpose provides excellent performance at lower cost. Business Critical is for mission-critical workloads with stringent performance requirements.",
			"benefits": []string{
				"63% cost reduction (2.7x → 1x pricing)",
				"Same vCore count and memory",
				"Adequate for 95% of production workloads",
				"99.99% SLA maintained",
				"Same backup and geo-replication options",
			},
			"savings_source":       "BC: ~$200/vCore/month, GP: ~$73/vCore/month → ~$127/vCore savings",
			"workload_suitability": "General Purpose suitable for most production workloads without in-memory OLTP requirements",
			"performance_impact":   "Higher latency (5-10ms vs 1-2ms), remote storage vs local SSD. BC only needed for: in-memory OLTP, ultra-low latency (<2ms), built-in read replicas.",
		}
	}

	// Check for large DTU tiers that haven't migrated to vCore
	// S3+ or P1+ that should consider vCore for better pricing
	if strings.HasPrefix(skuUpper, "S") && (strings.Contains(skuUpper, "S6") || strings.Contains(skuUpper, "S7") || strings.Contains(skuUpper, "S9") || strings.Contains(skuUpper, "S12")) {
		// Calculate savings using dynamic pricing
		stdPrice, _ := pricingCache.GetSQLComputePrice(ctx, "Standard", skuName, region)
		vCorePrice, _ := pricingCache.GetSQLComputePrice(ctx, "GP", "GP_Gen5_2", region)

		estimatedSavings := 80.0 // Fallback
		if stdPrice > 0 && vCorePrice > 0 {
			estimatedSavings = (stdPrice - vCorePrice) * 730
		}

		return map[string]interface{}{
			"rule_name":                 "azure_sql_large_dtu_rightsizing",
			"current_tier":              "Standard DTU (S6-S12)",
			"current_sku":               skuName,
			"recommended_tier":          "General Purpose vCore (2-4 cores)",
			"recommended_sku":           "GP_Gen5_2",
			"estimated_savings_monthly": estimatedSavings,
			"reason":                    "Database is using large Standard DTU tier (S6-S12). These higher Standard tiers are expensive compared to equivalent vCore options. Migrating to General Purpose vCore provides better price-performance and flexibility.",
			"benefits": []string{
				"30-40% cost savings vs high-tier DTU",
				"Better performance with Gen5 hardware",
				"Flexible resource allocation",
				"Reserved capacity pricing available",
				"Hybrid benefit for SQL Server licenses",
			},
			"savings_source":       "Standard S9-S12: $600-2400/month vs GP_Gen5_2-4: $146-292/month",
			"workload_suitability": "vCore General Purpose suitable for all Standard tier workloads",
			"performance_impact":   "Better performance with newer hardware and independent compute/storage scaling",
		}
	}

	// Check for Premium P6+ that should consider Business Critical vCore
	if strings.HasPrefix(skuUpper, "P") && (strings.Contains(skuUpper, "P6") || strings.Contains(skuUpper, "P11") || strings.Contains(skuUpper, "P15")) {
		// Calculate savings using dynamic pricing
		premiumPrice, _ := pricingCache.GetSQLComputePrice(ctx, "Premium", skuName, region)
		bcPrice, _ := pricingCache.GetSQLComputePrice(ctx, "BC", "BC_Gen5_4", region)

		estimatedSavings := 200.0 // Fallback
		if premiumPrice > 0 && bcPrice > 0 {
			estimatedSavings = (premiumPrice - bcPrice) * 730
		}

		return map[string]interface{}{
			"rule_name":                 "azure_sql_large_premium_dtu_rightsizing",
			"current_tier":              "Premium DTU (P6-P15)",
			"current_sku":               skuName,
			"recommended_tier":          "Business Critical vCore",
			"recommended_sku":           "BC_Gen5_4",
			"estimated_savings_monthly": estimatedSavings,
			"reason":                    "Database is using large Premium DTU tier (P6-P15). These tiers cost $1,000-$7,000/month. Business Critical vCore provides equivalent performance (in-memory OLTP, local SSD) at 30-50% lower cost with better flexibility.",
			"benefits": []string{
				"30-50% cost savings vs high-tier Premium DTU",
				"Same in-memory OLTP support",
				"Local SSD storage (like Premium)",
				"Reserved capacity discounts available",
				"Better scaling granularity",
			},
			"savings_source":       "Premium P6-P15: $1,000-7,000/month vs BC vCore: $400-1,600/month",
			"workload_suitability": "Business Critical vCore equivalent to Premium DTU for high-performance workloads",
			"performance_impact":   "Equivalent or better performance with latest hardware",
		}
	}

	return nil
}

func (s *sqlDatabaseService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	// Check if this is an alarm/metric alert recommendation
	if _, ok := recommendation.Data["alarm_config"]; ok {
		ctx.GetLogger().Info("azure: applying alarm recommendation for sql",
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
	})
	return err
}

func (s *sqlDatabaseService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	logger := ctx.GetLogger()
	cred, session, err := getAzureCredsForAccount(ctx, account)
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to create azure credential: %v", err),
		}, err
	}

	// Extract subscription ID, resource group, server name and database name from resource ID
	// Format: /subscriptions/{subscriptionId}/resourceGroups/{resourceGroup}/providers/Microsoft.Sql/servers/{serverName}/databases/{databaseName}
	parts := strings.Split(command.ResourceId, "/")
	var subscriptionID, resourceGroup, serverName, databaseName string
	for i, part := range parts {
		if part == "subscriptions" && i+1 < len(parts) {
			subscriptionID = parts[i+1]
		}
		if part == "resourceGroups" && i+1 < len(parts) {
			resourceGroup = parts[i+1]
		}
		if part == "servers" && i+1 < len(parts) {
			serverName = parts[i+1]
		}
		if part == "databases" && i+1 < len(parts) {
			databaseName = parts[i+1]
		}
	}

	if subscriptionID == "" {
		subscriptionID = session.SubscriptionID
	}
	if resourceGroup == "" || serverName == "" || databaseName == "" {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to extract resource group, server name or database name from resource ID: %s", command.ResourceId),
		}, fmt.Errorf("invalid resource ID")
	}

	// Handle different commands
	switch command.Command {
	case "azure_sql_geo_redundant_backups_disabled":
		// Cannot auto-fix - requires manual configuration
		return providers.ApplyCommandResponse{
			Success: false,
			Message: "cannot auto-apply command: geo-redundant backup configuration requires manual setup",
		}, fmt.Errorf("geo-redundant backups require manual configuration")

	case "azure_sql_public_network_access_enabled":
		// Disable public network access on the server
		logger.Info("applying command: disabling public network access", "serverName", serverName)

		serversClient, err := armsql.NewServersClient(subscriptionID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to create SQL servers client: %v", err),
			}, err
		}

		serverResp, err := serversClient.Get(ctx.GetContext(), resourceGroup, serverName, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to get SQL server: %v", err),
			}, err
		}

		server := serverResp.Server
		if server.Properties == nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: "SQL server properties are nil",
			}, fmt.Errorf("SQL server properties are nil")
		}

		// Disable public network access
		publicNetworkAccess := armsql.ServerNetworkAccessFlagDisabled
		server.Properties.PublicNetworkAccess = &publicNetworkAccess

		// Update server
		poller, err := serversClient.BeginCreateOrUpdate(ctx.GetContext(), resourceGroup, serverName, server, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to update SQL server: %v", err),
			}, err
		}

		_, err = poller.PollUntilDone(ctx.GetContext(), nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to wait for SQL server update: %v", err),
			}, err
		}

		logger.Info("successfully disabled public network access", "serverName", serverName)
		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("successfully disabled public network access for SQL server '%s'", serverName),
		}, nil

	case "azure_sql_transparent_data_encryption_disabled":
		// Enable TDE
		logger.Info("applying command: enabling transparent data encryption", "databaseName", databaseName)

		tdeClient, err := armsql.NewTransparentDataEncryptionsClient(subscriptionID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to create TDE client: %v", err),
			}, err
		}

		state := armsql.TransparentDataEncryptionStateEnabled
		tdeParams := armsql.LogicalDatabaseTransparentDataEncryption{
			Properties: &armsql.TransparentDataEncryptionProperties{
				State: &state,
			},
		}
		_, err = tdeClient.CreateOrUpdate(ctx.GetContext(), resourceGroup, serverName, databaseName, armsql.TransparentDataEncryptionNameCurrent, tdeParams, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to enable TDE: %v", err),
			}, err
		}

		logger.Info("successfully enabled transparent data encryption", "databaseName", databaseName)
		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("successfully enabled TDE for database '%s'", databaseName),
		}, nil

	case "azure_sql_advanced_threat_protection_disabled":
		// Cannot auto-fix - requires configuration
		return providers.ApplyCommandResponse{
			Success: false,
			Message: "cannot auto-apply command: advanced threat protection requires manual configuration and policy setup",
		}, fmt.Errorf("advanced threat protection requires manual configuration")

	case "azure_missing_tags":
		// Cannot auto-fix - requires user to specify tags
		return providers.ApplyCommandResponse{
			Success: false,
			Message: "cannot auto-apply command: tags must be specified manually",
		}, fmt.Errorf("tags require manual specification")

	case "pause":
		// Pause Azure SQL Database (data warehouse only)
		logger.Info("applying command: pausing SQL database", "databaseName", databaseName)

		dbClient, err := armsql.NewDatabasesClient(subscriptionID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			resultMessage := fmt.Sprintf("failed to create databases client: %v", err)
			if auditErr := logResourceActionAudit(ctx, command, account, "FAILURE", resultMessage); auditErr != nil {
				logger.Warn("failed to log audit record", "error", auditErr)
			}
			return providers.ApplyCommandResponse{
				Success: false,
				Message: resultMessage,
			}, err
		}

		// Pause database (only works for data warehouse SKUs)
		poller, err := dbClient.BeginPause(ctx.GetContext(), resourceGroup, serverName, databaseName, nil)
		if err != nil {
			resultMessage := fmt.Sprintf("failed to pause database: %v", err)
			if auditErr := logResourceActionAudit(ctx, command, account, "FAILURE", resultMessage); auditErr != nil {
				logger.Warn("failed to log audit record", "error", auditErr)
			}
			return providers.ApplyCommandResponse{
				Success: false,
				Message: resultMessage,
			}, err
		}

		// Create context with timeout for polling
		pollCtx, cancel := context.WithTimeout(ctx.GetContext(), 270*time.Second)
		defer cancel()

		_, err = poller.PollUntilDone(pollCtx, nil)
		if err != nil {
			resultMessage := fmt.Sprintf("failed to wait for database pause: %v", err)
			if auditErr := logResourceActionAudit(ctx, command, account, "FAILURE", resultMessage); auditErr != nil {
				logger.Warn("failed to log audit record", "error", auditErr)
			}
			return providers.ApplyCommandResponse{
				Success: false,
				Message: resultMessage,
			}, err
		}

		logger.Info("successfully paused SQL database", "databaseName", databaseName)
		resultMessage := fmt.Sprintf("successfully paused SQL database '%s'", databaseName)
		if auditErr := logResourceActionAudit(ctx, command, account, "SUCCESS", resultMessage); auditErr != nil {
			logger.Warn("failed to log audit record", "error", auditErr)
		}
		return providers.ApplyCommandResponse{
			Success: true,
			Message: resultMessage,
		}, nil

	case "resume":
		// Resume Azure SQL Database (data warehouse only)
		logger.Info("applying command: resuming SQL database", "databaseName", databaseName)

		dbClient, err := armsql.NewDatabasesClient(subscriptionID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			resultMessage := fmt.Sprintf("failed to create databases client: %v", err)
			if auditErr := logResourceActionAudit(ctx, command, account, "FAILURE", resultMessage); auditErr != nil {
				logger.Warn("failed to log audit record", "error", auditErr)
			}
			return providers.ApplyCommandResponse{
				Success: false,
				Message: resultMessage,
			}, err
		}

		// Resume database
		poller, err := dbClient.BeginResume(ctx.GetContext(), resourceGroup, serverName, databaseName, nil)
		if err != nil {
			resultMessage := fmt.Sprintf("failed to resume database: %v", err)
			if auditErr := logResourceActionAudit(ctx, command, account, "FAILURE", resultMessage); auditErr != nil {
				logger.Warn("failed to log audit record", "error", auditErr)
			}
			return providers.ApplyCommandResponse{
				Success: false,
				Message: resultMessage,
			}, err
		}

		// Create context with timeout for polling
		// 4.5 minutes to stay well under Hasura's 5-minute (300s) action timeout
		pollCtx, cancel := context.WithTimeout(ctx.GetContext(), 270*time.Second)
		defer cancel()

		_, err = poller.PollUntilDone(pollCtx, nil)
		if err != nil {
			resultMessage := fmt.Sprintf("failed to wait for database resume: %v", err)
			if auditErr := logResourceActionAudit(ctx, command, account, "FAILURE", resultMessage); auditErr != nil {
				logger.Warn("failed to log audit record", "error", auditErr)
			}
			return providers.ApplyCommandResponse{
				Success: false,
				Message: resultMessage,
			}, err
		}

		logger.Info("successfully resumed SQL database", "databaseName", databaseName)
		resultMessage := fmt.Sprintf("successfully resumed SQL database '%s'", databaseName)
		if auditErr := logResourceActionAudit(ctx, command, account, "SUCCESS", resultMessage); auditErr != nil {
			logger.Warn("failed to log audit record", "error", auditErr)
		}
		return providers.ApplyCommandResponse{
			Success: true,
			Message: resultMessage,
		}, nil

	default:
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("unknown command: %s", command.Command),
		}, fmt.Errorf("unknown command: %s", command.Command)
	}
}

func (s *sqlDatabaseService) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
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

func (s *sqlDatabaseService) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, resource providers.Resource) (providers.ServiceMapApplication, error) {
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

	// Add the SQL server as an upstream dependency
	parts := strings.Split(resource.Id, "/")
	if len(parts) > 8 {
		serverID := strings.Join(parts[:9], "/")
		app.Upstreams = append(app.Upstreams, providers.ServiceApplicationLink{
			Id: providers.ServiceApplicationId{
				Name:      serverID,
				Kind:      "Microsoft.Sql/servers",
				Namespace: resource.Region,
			},
		}.ToUpstreamLink())
	}

	return app, nil
}
