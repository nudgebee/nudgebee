package aws

import (
	"fmt"
	"nudgebee/collector/cloud/providers"
	"nudgebee/collector/cloud/providers/constants"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
	cetypes "github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
)

const ServiceNameCostExplorer = "CostExplorer"

// ceRIServiceMap maps Cost Explorer RI service names to cloud_resourses service_name values
var ceRIServiceMap = map[string]string{
	"Amazon Elastic Compute Cloud - Compute": "AmazonEC2",
	"Amazon Relational Database Service":     "AmazonRDS",
	"Amazon OpenSearch Service":              "AmazonOpenSearchService",
	"Amazon ElastiCache":                     "AmazonElastiCache",
	"Amazon Redshift":                        "AmazonRedshift",
	"Amazon MemoryDB Service":                "AmazonMemoryDB",
	"Amazon DynamoDB Service":                "AmazonDynamoDB",
}

// ceSPTypeMap maps Savings Plan types to cloud_resourses service_name values
var ceSPTypeMap = map[cetypes.SupportedSavingsPlansType]string{
	cetypes.SupportedSavingsPlansTypeEc2InstanceSp: "AmazonEC2",
	cetypes.SupportedSavingsPlansTypeComputeSp:     "AmazonEC2",
	cetypes.SupportedSavingsPlansTypeSagemakerSp:   "AmazonSageMaker",
}

// Supported services for GetReservationPurchaseRecommendation
var ceRIServices = []string{
	"Amazon Elastic Compute Cloud - Compute",
	"Amazon Relational Database Service",
	"Amazon OpenSearch Service",
	"Amazon ElastiCache",
	"Amazon Redshift",
	"Amazon MemoryDB Service",
	"Amazon DynamoDB Service",
}

type awsCostExplorer struct {
	DefaultAwsServiceImpl
}

func (a *awsCostExplorer) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return providers.QueryMetricsResponse{}, nil
}

func (a *awsCostExplorer) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
	return nil, nil
}

func (a *awsCostExplorer) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	recommendations := []providers.Recommendation{}

	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws session for cost explorer", "error", err)
		return nil, err
	}

	// Cost Explorer API is only available in us-east-1
	cfg, err = awsconfig.LoadDefaultConfig(ctx.GetContext(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(cfg.Credentials),
	)
	if err != nil {
		ctx.GetLogger().Error("failed to create us-east-1 config for cost explorer", "error", err)
		return nil, err
	}

	client := costexplorer.NewFromConfig(cfg)

	// Fetch RI recommendations for each supported service
	riRecs := a.getReservedInstanceRecommendations(ctx, client, account)
	recommendations = append(recommendations, riRecs...)

	// Fetch Savings Plans recommendations
	spRecs := a.getSavingsPlanRecommendations(ctx, client, account)
	recommendations = append(recommendations, spRecs...)

	ctx.GetLogger().Info("fetched cost explorer recommendations", "count", len(recommendations))
	return recommendations, nil
}

func (a *awsCostExplorer) getReservedInstanceRecommendations(ctx providers.CloudProviderContext, client *costexplorer.Client, account providers.Account) []providers.Recommendation {
	recommendations := []providers.Recommendation{}

	for _, service := range ceRIServices {
		output, err := client.GetReservationPurchaseRecommendation(ctx.GetContext(), &costexplorer.GetReservationPurchaseRecommendationInput{
			Service:      aws.String(service),
			AccountScope: cetypes.AccountScopeLinked,
		})
		if err != nil {
			ctx.GetLogger().Warn("failed to get RI recommendation for service", "service", service, "error", err)
			continue
		}

		for _, rec := range output.Recommendations {
			for i, detail := range rec.RecommendationDetails {
				data := map[string]any{
					"source":  "aws",
					"service": service,
				}

				savings := 0.0
				resourceType := "reserved-instance"

				if detail.InstanceDetails != nil {
					a.extractInstanceDetails(detail.InstanceDetails, data)
				}
				if detail.EstimatedMonthlySavingsAmount != nil {
					if v, err := parseFloat64(*detail.EstimatedMonthlySavingsAmount); err == nil {
						savings = v
						data["estimated_monthly_savings"] = v
					}
				}
				if detail.EstimatedMonthlySavingsPercentage != nil {
					if v, err := parseFloat64(*detail.EstimatedMonthlySavingsPercentage); err == nil {
						data["estimated_savings_percentage"] = v
					}
				}
				if detail.UpfrontCost != nil {
					data["upfront_cost"] = *detail.UpfrontCost
				}
				if detail.RecurringStandardMonthlyCost != nil {
					data["recurring_monthly_cost"] = *detail.RecurringStandardMonthlyCost
				}
				if detail.AverageUtilization != nil {
					data["average_utilization"] = *detail.AverageUtilization
				}
				if detail.RecommendedNumberOfInstancesToPurchase != nil {
					data["recommended_instance_count"] = *detail.RecommendedNumberOfInstancesToPurchase
				}

				riServiceName := ServiceNameCostExplorer
				if mapped, ok := ceRIServiceMap[service]; ok {
					riServiceName = mapped
				}

				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryRightSizing,
					RuleName:            constants.AwsNativeCERI,
					Severity:            mapSavingsToSeverity(&savings),
					Savings:             savings,
					Action:              providers.RecommendationActionModify,
					Data:                data,
					ResourceServiceName: riServiceName,
					ResourceId:          fmt.Sprintf("ce-ri-%s-%d", service, i),
					ResourceType:        resourceType,
					ResourceRegion:      "us-east-1",
					DedupeGroup:         fmt.Sprintf("aws_commitment:%s:%s", account.AccountNumber, riServiceName),
				})
			}
		}
	}

	return recommendations
}

func (a *awsCostExplorer) extractInstanceDetails(details *cetypes.InstanceDetails, data map[string]any) {
	if details.EC2InstanceDetails != nil {
		d := details.EC2InstanceDetails
		if d.InstanceType != nil {
			data["instance_type"] = *d.InstanceType
		}
		if d.Region != nil {
			data["region"] = *d.Region
		}
		if d.Platform != nil {
			data["platform"] = *d.Platform
		}
		if d.Family != nil {
			data["family"] = *d.Family
		}
		data["current_generation"] = d.CurrentGeneration
	}
	if details.RDSInstanceDetails != nil {
		d := details.RDSInstanceDetails
		if d.InstanceType != nil {
			data["instance_type"] = *d.InstanceType
		}
		if d.Region != nil {
			data["region"] = *d.Region
		}
		if d.DatabaseEngine != nil {
			data["database_engine"] = *d.DatabaseEngine
		}
		if d.Family != nil {
			data["family"] = *d.Family
		}
		data["current_generation"] = d.CurrentGeneration
	}
	if details.ESInstanceDetails != nil {
		d := details.ESInstanceDetails
		if d.InstanceClass != nil {
			data["instance_class"] = *d.InstanceClass
		}
		if d.InstanceSize != nil {
			data["instance_size"] = *d.InstanceSize
		}
		if d.Region != nil {
			data["region"] = *d.Region
		}
		data["current_generation"] = d.CurrentGeneration
	}
	if details.ElastiCacheInstanceDetails != nil {
		d := details.ElastiCacheInstanceDetails
		if d.NodeType != nil {
			data["node_type"] = *d.NodeType
		}
		if d.Region != nil {
			data["region"] = *d.Region
		}
		if d.Family != nil {
			data["family"] = *d.Family
		}
		data["current_generation"] = d.CurrentGeneration
	}
	if details.RedshiftInstanceDetails != nil {
		d := details.RedshiftInstanceDetails
		if d.NodeType != nil {
			data["node_type"] = *d.NodeType
		}
		if d.Region != nil {
			data["region"] = *d.Region
		}
		if d.Family != nil {
			data["family"] = *d.Family
		}
		data["current_generation"] = d.CurrentGeneration
	}
}

func (a *awsCostExplorer) getSavingsPlanRecommendations(ctx providers.CloudProviderContext, client *costexplorer.Client, account providers.Account) []providers.Recommendation {
	recommendations := []providers.Recommendation{}

	spTypes := []cetypes.SupportedSavingsPlansType{
		cetypes.SupportedSavingsPlansTypeComputeSp,
		cetypes.SupportedSavingsPlansTypeEc2InstanceSp,
		cetypes.SupportedSavingsPlansTypeSagemakerSp,
	}

	terms := []cetypes.TermInYears{cetypes.TermInYearsOneYear, cetypes.TermInYearsThreeYears}
	payments := []cetypes.PaymentOption{cetypes.PaymentOptionAllUpfront, cetypes.PaymentOptionNoUpfront}

	for _, spType := range spTypes {
		for _, term := range terms {
			for _, payment := range payments {
				output, err := client.GetSavingsPlansPurchaseRecommendation(ctx.GetContext(), &costexplorer.GetSavingsPlansPurchaseRecommendationInput{
					SavingsPlansType:     spType,
					TermInYears:          term,
					PaymentOption:        payment,
					LookbackPeriodInDays: cetypes.LookbackPeriodInDaysThirtyDays,
					AccountScope:         cetypes.AccountScopeLinked,
				})
				if err != nil {
					ctx.GetLogger().Warn("failed to get savings plan recommendation", "type", spType, "error", err)
					continue
				}

				if output.SavingsPlansPurchaseRecommendation == nil {
					continue
				}

				rec := output.SavingsPlansPurchaseRecommendation
				summary := rec.SavingsPlansPurchaseRecommendationSummary
				if summary == nil {
					continue
				}

				savings := 0.0
				data := map[string]any{
					"source":            "aws",
					"savings_plan_type": string(spType),
					"term":              string(term),
					"payment_option":    string(payment),
				}

				if summary.EstimatedMonthlySavingsAmount != nil {
					if v, err := parseFloat64(*summary.EstimatedMonthlySavingsAmount); err == nil {
						savings = v
						data["estimated_monthly_savings"] = v
					}
				}
				if summary.EstimatedSavingsPercentage != nil {
					if v, err := parseFloat64(*summary.EstimatedSavingsPercentage); err == nil {
						data["estimated_savings_percentage"] = v
					}
				}
				if summary.HourlyCommitmentToPurchase != nil {
					data["hourly_commitment"] = *summary.HourlyCommitmentToPurchase
				}
				if summary.EstimatedOnDemandCostWithCurrentCommitment != nil {
					data["estimated_on_demand_cost"] = *summary.EstimatedOnDemandCostWithCurrentCommitment
				}
				if summary.EstimatedTotalCost != nil {
					data["estimated_total_cost"] = *summary.EstimatedTotalCost
				}

				// Only add if there are actual savings
				if savings <= 0 {
					continue
				}

				spServiceName := ServiceNameCostExplorer
				if mapped, ok := ceSPTypeMap[spType]; ok {
					spServiceName = mapped
				}

				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryRightSizing,
					RuleName:            constants.AwsNativeCESavingsPlan,
					Severity:            mapSavingsToSeverity(&savings),
					Savings:             savings,
					Action:              providers.RecommendationActionModify,
					Data:                data,
					ResourceServiceName: spServiceName,
					ResourceId:          fmt.Sprintf("ce-sp-%s-%s-%s", spType, term, payment),
					ResourceType:        "savings-plan",
					ResourceRegion:      "global",
					DedupeGroup:         fmt.Sprintf("aws_commitment:%s:%s", account.AccountNumber, spServiceName),
				})
			}
		}
	}

	// Fetch Database Savings Plans recommendations
	dbSpRecs := a.getDatabaseSavingsPlanRecommendations(ctx, client, account)
	recommendations = append(recommendations, dbSpRecs...)

	return recommendations
}

func parseFloat64(s string) (float64, error) {
	return strconv.ParseFloat(s, 64)
}

func (a *awsCostExplorer) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	return nil
}

func (a *awsCostExplorer) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	return providers.ApplyCommandResponse{}, nil
}

func (a *awsCostExplorer) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
	return "", nil
}

func (a *awsCostExplorer) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (providers.ServiceMapApplication, error) {
	return providers.ServiceMapApplication{}, nil
}

// getDatabaseSavingsPlanRecommendations fetches Database Savings Plans recommendations
// and compares them against Reserved Instance recommendations to determine the best strategy
func (a *awsCostExplorer) getDatabaseSavingsPlanRecommendations(ctx providers.CloudProviderContext, client *costexplorer.Client, account providers.Account) []providers.Recommendation {
	recommendations := []providers.Recommendation{}

	// Database services covered by Database Savings Plans:
	// RDS, Aurora, DynamoDB, ElastiCache, DocumentDB, Neptune, Keyspaces, Timestream, DMS
	databaseServices := []string{
		"Amazon Relational Database Service",
		"Amazon ElastiCache",
		"Amazon DynamoDB Service",
	}

	terms := []cetypes.TermInYears{cetypes.TermInYearsOneYear, cetypes.TermInYearsThreeYears}
	payments := []cetypes.PaymentOption{cetypes.PaymentOptionAllUpfront, cetypes.PaymentOptionNoUpfront}

	// Fetch Reserved Instance recommendations for database services to compare against
	// IMPORTANT: RI recommendations are fetched once per service with DEFAULT parameters (no term/payment specified).
	// AWS API returns default recommendations (typically 1-year, no upfront or partial upfront).
	// These DEFAULT RI recommendations are then compared against SP recommendations across ALL term/payment combinations.
	// This is an APPROXIMATE comparison - a 3-year All Upfront SP is being compared against a 1-year default RI.
	// For precise analysis, RI recommendations should be fetched with matching term/payment parameters for each SP iteration,
	// but that would require (2 terms * 2 payments = 4) additional API calls per database service.
	// The comparison is still valuable for directional guidance, but consumers should be aware of this limitation.
	riRecommendations := make(map[string]*databaseRIRecommendation)
	for _, service := range databaseServices {
		output, err := client.GetReservationPurchaseRecommendation(ctx.GetContext(), &costexplorer.GetReservationPurchaseRecommendationInput{
			Service:      aws.String(service),
			AccountScope: cetypes.AccountScopeLinked,
		})
		if err != nil {
			ctx.GetLogger().Warn("failed to get RI recommendation for database service", "service", service, "error", err)
			continue
		}

		if len(output.Recommendations) > 0 {
			rec := output.Recommendations[0]
			if rec.RecommendationSummary != nil {
				summary := rec.RecommendationSummary
				riRec := &databaseRIRecommendation{
					service: service,
				}

				if summary.TotalEstimatedMonthlySavingsAmount != nil {
					if v, err := parseFloat64(*summary.TotalEstimatedMonthlySavingsAmount); err == nil {
						riRec.monthlySavings = v
					}
				}

				// Extract on-demand cost and RI cost from recommendation details
				if len(rec.RecommendationDetails) > 0 {
					detail := rec.RecommendationDetails[0]
					if detail.EstimatedMonthlySavingsAmount != nil {
						if v, err := parseFloat64(*detail.EstimatedMonthlySavingsAmount); err == nil {
							riRec.monthlySavings = v
						}
					}
					if detail.EstimatedMonthlyOnDemandCost != nil {
						if v, err := parseFloat64(*detail.EstimatedMonthlyOnDemandCost); err == nil {
							riRec.onDemandCost = v
						}
					}
					if detail.EstimatedReservationCostForLookbackPeriod != nil {
						if v, err := parseFloat64(*detail.EstimatedReservationCostForLookbackPeriod); err == nil {
							// Convert to monthly cost (lookback is 30 days)
							riRec.riCost = v
						}
					}
					if detail.UpfrontCost != nil {
						riRec.upfrontCost = *detail.UpfrontCost
					}
					if detail.RecurringStandardMonthlyCost != nil {
						riRec.recurringMonthlyCost = *detail.RecurringStandardMonthlyCost
					}
				}

				riRecommendations[service] = riRec
			}
		}
	}

	// Fetch Database Savings Plans recommendations
	// NOTE: DATABASE_SP is not yet in the official AWS SDK SupportedSavingsPlansType enum.
	// The SDK currently only includes: COMPUTE_SP, EC2_INSTANCE_SP, SAGEMAKER_SP.
	// DATABASE_SP is a valid AWS API value for Database Savings Plans (covers RDS, Aurora, ElastiCache, etc.),
	// but the Go SDK hasn't been updated yet. Using string literal until SDK is updated.
	// If this API call fails, it may indicate the SDK/API doesn't support this type yet.
	for _, term := range terms {
		for _, payment := range payments {
			output, err := client.GetSavingsPlansPurchaseRecommendation(ctx.GetContext(), &costexplorer.GetSavingsPlansPurchaseRecommendationInput{
				SavingsPlansType:     "DATABASE_SP", // Not in SDK enum - see NOTE above
				TermInYears:          term,
				PaymentOption:        payment,
				LookbackPeriodInDays: cetypes.LookbackPeriodInDaysThirtyDays,
				AccountScope:         cetypes.AccountScopeLinked,
			})
			if err != nil {
				// Log at Info level for DATABASE_SP type errors specifically to monitor SDK/API support
				// If DATABASE_SP is not yet supported by AWS API, all iterations will fail here
				ctx.GetLogger().Info("database savings plan recommendation request failed - this may indicate DATABASE_SP is not yet supported by AWS API or SDK",
					"term", term,
					"payment", payment,
					"error", err,
					"savingsPlanType", "DATABASE_SP",
					"note", "DATABASE_SP not yet in official AWS SDK enum - feature may be pending AWS API/SDK update")
				continue
			}

			if output.SavingsPlansPurchaseRecommendation == nil {
				continue
			}

			rec := output.SavingsPlansPurchaseRecommendation
			summary := rec.SavingsPlansPurchaseRecommendationSummary
			if summary == nil {
				continue
			}

			spSavings := 0.0
			spOnDemandCost := 0.0
			spEstimatedCost := 0.0

			if summary.EstimatedMonthlySavingsAmount != nil {
				if v, err := parseFloat64(*summary.EstimatedMonthlySavingsAmount); err == nil {
					spSavings = v
				}
			}
			if summary.EstimatedOnDemandCostWithCurrentCommitment != nil {
				if v, err := parseFloat64(*summary.EstimatedOnDemandCostWithCurrentCommitment); err == nil {
					spOnDemandCost = v
				}
			}
			if summary.EstimatedTotalCost != nil {
				if v, err := parseFloat64(*summary.EstimatedTotalCost); err == nil {
					spEstimatedCost = v
				}
			}

			// Only process if there are actual savings
			if spSavings <= 0 {
				continue
			}

			// Compare Database SP with total RI savings across all database services
			totalRISavings := 0.0
			totalRIOnDemandCost := 0.0
			totalRICost := 0.0
			for _, riRec := range riRecommendations {
				totalRISavings += riRec.monthlySavings
				totalRIOnDemandCost += riRec.onDemandCost
				totalRICost += riRec.riCost
			}

			// Determine the best strategy: SP, RI, or None
			strategy := "SP"
			bestSavings := spSavings
			breakEvenMonths := 0.0
			termMonths := 12.0
			if term == cetypes.TermInYearsThreeYears {
				termMonths = 36.0
			}
			breakEvenViable := true

			if totalRISavings > spSavings {
				strategy = "RI"
				bestSavings = totalRISavings
			}

			// Calculate break-even point for All Upfront plans
			// Break-even = Upfront Cost / Monthly Savings
			// Monthly savings = what you save each month by using SP instead of on-demand
			if payment == cetypes.PaymentOptionAllUpfront && summary.HourlyCommitmentToPurchase != nil {
				hourlyCommitment, err := parseFloat64(*summary.HourlyCommitmentToPurchase)
				if err != nil {
					ctx.GetLogger().Warn("failed to parse hourly commitment for break-even", "error", err)
				} else if hourlyCommitment > 0 && spSavings > 0 {
					// For All Upfront: total upfront payment = hourly rate * hours in term
					// 730 hours/month is average (365*24/12)
					upfrontCost := hourlyCommitment * 730 * termMonths
					// spSavings is already the monthly savings amount from AWS API
					breakEvenMonths = upfrontCost / spSavings
					breakEvenViable = breakEvenMonths <= termMonths

					// Log warning if break-even exceeds term (indicates plan won't pay for itself)
					if !breakEvenViable {
						ctx.GetLogger().Warn("database savings plan has break-even exceeding term",
							"term", term,
							"payment", payment,
							"breakEvenMonths", breakEvenMonths,
							"termMonths", termMonths,
							"upfrontCost", upfrontCost,
							"monthlySavings", spSavings)
					}
				}
			}

			data := map[string]any{
				"source":            "aws",
				"savings_plan_type": "DATABASE_SP",
				"term":              string(term),
				"payment_option":    string(payment),
				"strategy":          strategy,
				"on_demand_cost":    spOnDemandCost,
				"sp_cost":           spEstimatedCost,
				"sp_savings":        spSavings,
				"ri_cost":           totalRICost,
				"ri_savings":        totalRISavings,
				"recommended":       strategy == "SP",
				"break_even_months": breakEvenMonths,
				"break_even_viable": breakEvenViable,
				"term_months":       termMonths,
				// Document RI comparison parameters so consumers understand the basis
				"ri_comparison_note": "RI recommendations use default AWS parameters (typically 1-year, no/partial upfront) and may not match SP term/payment",
			}

			if summary.EstimatedSavingsPercentage != nil {
				if v, err := parseFloat64(*summary.EstimatedSavingsPercentage); err == nil {
					data["estimated_savings_percentage"] = v
				}
			}
			if summary.HourlyCommitmentToPurchase != nil {
				data["hourly_commitment"] = *summary.HourlyCommitmentToPurchase
			}

			// Add comparison details for each database service
			serviceComparisons := make(map[string]any)
			for service, riRec := range riRecommendations {
				serviceComparisons[service] = map[string]any{
					"on_demand_cost":     riRec.onDemandCost,
					"ri_cost":            riRec.riCost,
					"ri_monthly_savings": riRec.monthlySavings,
					"ri_upfront_cost":    riRec.upfrontCost,
					"ri_recurring_cost":  riRec.recurringMonthlyCost,
				}
			}
			data["service_comparisons"] = serviceComparisons

			// Only emit recommendation if break-even is viable (i.e., plan pays for itself within term)
			// Non-viable plans (where break-even > term) are logged but not surfaced as recommendations
			if breakEvenViable {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryRightSizing,
					RuleName:            constants.AwsNativeDatabaseSavingsPlan,
					Severity:            mapSavingsToSeverity(&bestSavings),
					Savings:             bestSavings,
					Action:              providers.RecommendationActionModify,
					Data:                data,
					ResourceServiceName: "AmazonRDS",
					ResourceId:          fmt.Sprintf("ce-dbsp-%s-%s", term, payment),
					ResourceType:        "database-savings-plan",
					ResourceRegion:      "global",
					DedupeGroup:         fmt.Sprintf("aws_commitment:%s:AmazonRDS", account.AccountNumber),
				})
			}
		}
	}

	return recommendations
}

// databaseRIRecommendation holds Reserved Instance recommendation details for comparison
type databaseRIRecommendation struct {
	service              string
	monthlySavings       float64
	onDemandCost         float64
	riCost               float64
	upfrontCost          string
	recurringMonthlyCost string
}
