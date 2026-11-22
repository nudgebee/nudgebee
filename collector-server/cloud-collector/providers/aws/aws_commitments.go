package aws

import (
	"context"
	"errors"
	"fmt"
	"nudgebee/collector/cloud/config"
	"nudgebee/collector/cloud/providers"
	"nudgebee/collector/cloud/providers/constants"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/arn"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
	cetypes "github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
	"github.com/aws/aws-sdk-go-v2/service/savingsplans"
	sptypes "github.com/aws/aws-sdk-go-v2/service/savingsplans/types"
)

const ServiceNameCommitments = providers.CommitmentServiceName

// savingsPlanUtilizationLookbackDays is the window the recommendation rules
// measure a commitment's utilization and coverage over, matching the lookback
// the purchase recommendations use.
const savingsPlanUtilizationLookbackDays = 30

type savingsPlansAPI interface {
	DescribeSavingsPlans(ctx context.Context, params *savingsplans.DescribeSavingsPlansInput, optFns ...func(*savingsplans.Options)) (*savingsplans.DescribeSavingsPlansOutput, error)
}

type savingsPlansUtilizationAPI interface {
	GetSavingsPlansUtilizationDetails(ctx context.Context, params *costexplorer.GetSavingsPlansUtilizationDetailsInput, optFns ...func(*costexplorer.Options)) (*costexplorer.GetSavingsPlansUtilizationDetailsOutput, error)
	GetSavingsPlansCoverage(ctx context.Context, params *costexplorer.GetSavingsPlansCoverageInput, optFns ...func(*costexplorer.Options)) (*costexplorer.GetSavingsPlansCoverageOutput, error)
}

// commitmentClients is everything a commitment read needs: the Savings Plans
// and Cost Explorer clients, and the regional sources of Reserved Instances
// with the regions to ask them in.
type commitmentClients struct {
	plans        savingsPlansAPI
	costExplorer interface {
		savingsPlansUtilizationAPI
		reservationUtilizationAPI
	}
	reservedInstances []reservedInstanceSource
	// regions lists the regions to ask the Reserved Instance sources in. It is
	// only called when there are sources to ask.
	regions func() ([]string, error)
}

// awsCommitments reports on the commitments an account already owns (Savings
// Plans, EC2 and RDS Reserved Instances) — how well they are used, when they
// end, and how much of the account's eligible spend they cover. Recommendations to buy new ones come from
// awsCostExplorer and awsCostOptimizationHub. It is a service of its own rather
// than part of the Cost Explorer scan so that a failure here cannot abandon
// that scan and retire the account's purchase recommendations.
type awsCommitments struct {
	DefaultAwsServiceImpl
}

func (a *awsCommitments) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return providers.QueryMetricsResponse{}, nil
}

func (a *awsCommitments) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
	return nil, nil
}

func (a *awsCommitments) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	if !config.Config.CloudCollectorCommitmentRecommendationsEnabled {
		return []providers.Recommendation{}, nil
	}
	clients, err := newCommitmentClients(ctx, account)
	if err != nil {
		return nil, err
	}
	return commitmentRecommendations(ctx, clients, account, time.Now().UTC())
}

// newCommitmentClients builds the clients a commitment read needs. The Savings
// Plans and Cost Explorer APIs are only available in us-east-1; Reserved
// Instances are read region by region.
func newCommitmentClients(ctx providers.CloudProviderContext, account providers.Account) (commitmentClients, error) {
	accountCfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws session for commitments", "error", err)
		return commitmentClients{}, err
	}

	cfg, err := awsconfig.LoadDefaultConfig(ctx.GetContext(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(accountCfg.Credentials),
	)
	if err != nil {
		ctx.GetLogger().Error("failed to create us-east-1 config for commitments", "error", err)
		return commitmentClients{}, err
	}

	return commitmentClients{
		plans:             savingsplans.NewFromConfig(cfg),
		costExplorer:      costexplorer.NewFromConfig(cfg),
		reservedInstances: reservedInstanceSources(accountCfg, account),
		regions: func() ([]string, error) {
			return (&awsProvider{}).getRegions(ctx, account)
		},
	}, nil
}

// collectCommitments reads every commitment the account owns — Savings Plans
// and Reserved Instances — with their utilization, and its Savings Plan
// coverage, over the last lookbackDays days.
func collectCommitments(ctx providers.CloudProviderContext, clients commitmentClients, account providers.Account, now time.Time, lookbackDays int) ([]providers.Commitment, []providers.Utilization, []providers.Coverage, error) {
	commitments, utilizations, err := savingsPlanCommitments(ctx, clients.plans, clients.costExplorer, account, now, lookbackDays)
	if err != nil {
		// A role denied the Savings Plans API (an organization policy, a
		// hand-written role) has no plans we can report, which is no reason to
		// withhold its Reserved Instances and coverage. Every source degrades on
		// its own like this. Any other failure leaves the plans unknown, and
		// reporting without them would archive their recommendations.
		if !isAccessDeniedError(err) {
			return nil, nil, nil, err
		}
		ctx.GetLogger().Info("savings plans not readable, reporting without them", "error", err)
		commitments, utilizations = []providers.Commitment{}, nil
	}

	// Coverage is reported for Savings Plans only. Reserved Instance coverage
	// counts usage a Savings Plan already covers as uncovered, so on its own it
	// would call a fully covered account poorly covered.
	coverages, err := savingsPlanCoverages(ctx, clients.costExplorer, account, commitments, now, lookbackDays)
	if err != nil {
		return nil, nil, nil, err
	}

	if len(clients.reservedInstances) == 0 {
		return commitments, utilizations, coverages, nil
	}
	regions, err := clients.regions()
	if err != nil {
		// A role that may not list regions cannot be asked about regional
		// Reserved Instances, which is no reason to withhold what its Savings
		// Plans showed. Any other failure leaves the reservations unknown.
		if isRegionNotReadable(err) {
			ctx.GetLogger().Info("regions not readable, skipping reserved instances", "error", err)
			return commitments, utilizations, coverages, nil
		}
		return nil, nil, nil, fmt.Errorf("list regions for reserved instances: %w", err)
	}
	reservedCommitments, reservedUtilizations, err := reservedInstanceCommitments(ctx, clients.reservedInstances, clients.costExplorer, account, regions, now, lookbackDays)
	if err != nil {
		return nil, nil, nil, err
	}
	commitments = append(commitments, reservedCommitments...)
	utilizations = append(utilizations, reservedUtilizations...)
	return commitments, utilizations, coverages, nil
}

func commitmentRecommendations(ctx providers.CloudProviderContext, clients commitmentClients, account providers.Account, now time.Time) ([]providers.Recommendation, error) {
	// A failure here leaves the commitments unknown, and answering "no
	// recommendations" to an unknown would archive every commitment
	// recommendation the account has. A source the role is simply not permitted
	// to read has already been left out by collectCommitments.
	commitments, utilizations, coverages, err := collectCommitments(ctx, clients, account, now, savingsPlanUtilizationLookbackDays)
	if err != nil {
		return nil, err
	}

	recommendations := providers.CommitmentRecommendations(commitments, utilizations, now)
	recommendations = append(recommendations, providers.CoverageRecommendations(coverages, commitments)...)
	ctx.GetLogger().Info("fetched commitment recommendations", "commitments", len(commitments), "count", len(recommendations))
	return recommendations, nil
}

// savingsPlanCommitments returns the active Savings Plans the account owns and
// each one's utilization over the last lookbackDays days.
//
// Only plans the account owns are returned. An organization's payer account can
// see plans its member accounts bought, so without this the same plan would be
// reported once per connected account that can see it, and its waste counted
// that many times.
func savingsPlanCommitments(ctx providers.CloudProviderContext, plansAPI savingsPlansAPI, utilizationAPI savingsPlansUtilizationAPI, account providers.Account, now time.Time, lookbackDays int) ([]providers.Commitment, []providers.Utilization, error) {
	plans, err := listActiveSavingsPlans(ctx, plansAPI)
	if err != nil {
		return nil, nil, fmt.Errorf("describe savings plans: %w", err)
	}

	commitments := []providers.Commitment{}
	ownedPlans := map[string]sptypes.SavingsPlan{}
	for _, plan := range plans {
		commitment, ok := savingsPlanToCommitment(plan)
		if !ok {
			ctx.GetLogger().Warn("skipping savings plan with no usable id or arn", "savingsPlanArn", aws.ToString(plan.SavingsPlanArn))
			continue
		}
		if commitment.AccountID != account.AccountNumber {
			continue
		}
		commitments = append(commitments, commitment)
		ownedPlans[aws.ToString(plan.SavingsPlanArn)] = plan
	}
	if len(commitments) == 0 {
		return commitments, nil, nil
	}

	utilizations, err := savingsPlanUtilizations(ctx, utilizationAPI, ownedPlans, now, lookbackDays)
	if err != nil {
		// Utilization that cannot be read (Cost Explorer not permitted, or no data
		// yet for a plan bought today) still leaves the expiry of each plan known.
		var dataUnavailable *cetypes.DataUnavailableException
		if isAccessDeniedError(err) || errors.As(err, &dataUnavailable) {
			ctx.GetLogger().Info("savings plan utilization not available, reporting expiry only", "error", err)
			return commitments, nil, nil
		}
		return nil, nil, fmt.Errorf("get savings plans utilization details: %w", err)
	}
	return commitments, utilizations, nil
}

func listActiveSavingsPlans(ctx providers.CloudProviderContext, client savingsPlansAPI) ([]sptypes.SavingsPlan, error) {
	plans := []sptypes.SavingsPlan{}
	var nextToken *string
	for {
		output, err := client.DescribeSavingsPlans(ctx.GetContext(), &savingsplans.DescribeSavingsPlansInput{
			States:    []sptypes.SavingsPlanState{sptypes.SavingsPlanStateActive},
			NextToken: nextToken,
		})
		if err != nil {
			return nil, err
		}
		plans = append(plans, output.SavingsPlans...)
		if aws.ToString(output.NextToken) == "" {
			return plans, nil
		}
		nextToken = output.NextToken
	}
}

// savingsPlanToCommitment converts a Savings Plan to the provider-neutral model.
// It reports false when the plan has no id or its owning account cannot be read
// from its ARN, since neither a stable identity nor an owner can then be given.
func savingsPlanToCommitment(plan sptypes.SavingsPlan) (providers.Commitment, bool) {
	id := aws.ToString(plan.SavingsPlanId)
	planArn, err := arn.Parse(aws.ToString(plan.SavingsPlanArn))
	if id == "" || err != nil || planArn.AccountID == "" {
		return providers.Commitment{}, false
	}

	productTypes := make([]string, 0, len(plan.ProductTypes))
	for _, productType := range plan.ProductTypes {
		productTypes = append(productTypes, string(productType))
	}
	hourlyCommitment, _ := parseFloat64(aws.ToString(plan.Commitment))

	return providers.Commitment{
		ID:        id,
		Provider:  constants.ProviderAWS,
		AccountID: planArn.AccountID,
		Type:      providers.CommitmentTypeSavingsPlan,
		Resources: []providers.CommitmentResource{{
			Type:         "spend",
			Quantity:     hourlyCommitment,
			Unit:         string(plan.Currency) + "_PER_HOUR",
			Service:      strings.Join(productTypes, ","),
			Region:       aws.ToString(plan.Region),
			ResourceType: aws.ToString(plan.Ec2InstanceFamily),
			Metadata: map[string]any{
				"savings_plan_arn":  aws.ToString(plan.SavingsPlanArn),
				"savings_plan_type": string(plan.SavingsPlanType),
				"payment_option":    string(plan.PaymentOption),
			},
		}},
		StartTime: parseSavingsPlanTime(plan.Start),
		EndTime:   parseSavingsPlanTime(plan.End),
		Status:    string(plan.State),
	}, true
}

// parseSavingsPlanTime returns the zero time for a missing or unreadable
// timestamp, which the commitment rules read as "end date unknown".
func parseSavingsPlanTime(value *string) time.Time {
	t, err := time.Parse(time.RFC3339, aws.ToString(value))
	if err != nil {
		return time.Time{}
	}
	return t
}

// savingsPlanUtilizations returns the utilization of each of the given plans,
// keyed by plan ARN, over the lookback window. Cost Explorer
// reports commitment amounts in the plan's own currency, so the unused
// commitment is the plan's waste as money without any price being assumed.
func savingsPlanUtilizations(ctx providers.CloudProviderContext, client savingsPlansUtilizationAPI, plans map[string]sptypes.SavingsPlan, now time.Time, lookbackDays int) ([]providers.Utilization, error) {
	start, end := costExplorerWindow(now, lookbackDays)

	utilizations := []providers.Utilization{}
	var nextToken *string
	for {
		output, err := client.GetSavingsPlansUtilizationDetails(ctx.GetContext(), &costexplorer.GetSavingsPlansUtilizationDetailsInput{
			TimePeriod: &cetypes.DateInterval{
				Start: aws.String(start.Format(time.DateOnly)),
				End:   aws.String(end.Format(time.DateOnly)),
			},
			NextToken: nextToken,
		})
		if err != nil {
			return nil, err
		}

		for _, detail := range output.SavingsPlansUtilizationDetails {
			plan, ok := plans[aws.ToString(detail.SavingsPlanArn)]
			if !ok || detail.Utilization == nil {
				continue
			}
			total, totalErr := parseFloat64(aws.ToString(detail.Utilization.TotalCommitment))
			used, usedErr := parseFloat64(aws.ToString(detail.Utilization.UsedCommitment))
			unused, unusedErr := parseFloat64(aws.ToString(detail.Utilization.UnusedCommitment))
			pct, pctErr := parseFloat64(aws.ToString(detail.Utilization.UtilizationPercentage))
			if err := errors.Join(totalErr, usedErr, unusedErr, pctErr); err != nil {
				ctx.GetLogger().Warn("skipping savings plan utilization with unreadable amounts", "savingsPlanArn", aws.ToString(detail.SavingsPlanArn), "error", err)
				continue
			}
			currency := string(plan.Currency)
			utilizations = append(utilizations, providers.Utilization{
				Provider:       constants.ProviderAWS,
				CommitmentID:   aws.ToString(plan.SavingsPlanId),
				CommitmentType: providers.CommitmentTypeSavingsPlan,
				UtilizationPct: pct,
				CommittedUsage: total,
				UsedUsage:      used,
				UnusedUsage:    unused,
				UsageUnit:      currency,
				UnusedCost:     unused,
				Currency:       currency,
				StartTime:      start,
				EndTime:        end,
			})
		}

		if aws.ToString(output.NextToken) == "" {
			return utilizations, nil
		}
		nextToken = output.NextToken
	}
}

// costExplorerWindow returns the lookbackDays-day window to ask Cost Explorer
// for. Cost Explorer requires the end date to be before the current date, and
// treats it as exclusive, so the window ends yesterday and its last full day is
// the day before.
func costExplorerWindow(now time.Time, lookbackDays int) (time.Time, time.Time) {
	end := now.UTC().Truncate(24*time.Hour).AddDate(0, 0, -1)
	return end.AddDate(0, 0, -lookbackDays), end
}

// savingsPlanCoverages returns how much of the account's own Savings Plan
// eligible spend was covered by a Savings Plan over the lookback window, per
// service.
//
// The result is limited to the account's own usage. A payer account's coverage
// otherwise includes every member account's usage, so the same uncovered spend
// would be reported under the payer and again under each connected member.
//
// Coverage that cannot be read (Cost Explorer not permitted, or no data yet)
// reports none rather than failing, so the plans' own findings still go out.
func savingsPlanCoverages(ctx providers.CloudProviderContext, client savingsPlansUtilizationAPI, account providers.Account, commitments []providers.Commitment, now time.Time, lookbackDays int) ([]providers.Coverage, error) {
	start, end := costExplorerWindow(now, lookbackDays)

	type spend struct{ covered, total float64 }
	spendByService := map[string]*spend{}
	services := []string{}
	var nextToken *string
	for {
		output, err := client.GetSavingsPlansCoverage(ctx.GetContext(), &costexplorer.GetSavingsPlansCoverageInput{
			TimePeriod: &cetypes.DateInterval{
				Start: aws.String(start.Format(time.DateOnly)),
				End:   aws.String(end.Format(time.DateOnly)),
			},
			GroupBy: []cetypes.GroupDefinition{{
				Type: cetypes.GroupDefinitionTypeDimension,
				Key:  aws.String(string(cetypes.DimensionService)),
			}},
			Filter: &cetypes.Expression{Dimensions: &cetypes.DimensionValues{
				Key:    cetypes.DimensionLinkedAccount,
				Values: []string{account.AccountNumber},
			}},
			NextToken: nextToken,
		})
		if err != nil {
			var dataUnavailable *cetypes.DataUnavailableException
			if isAccessDeniedError(err) || errors.As(err, &dataUnavailable) {
				ctx.GetLogger().Info("savings plan coverage not available, skipping", "error", err)
				return nil, nil
			}
			return nil, fmt.Errorf("get savings plans coverage: %w", err)
		}

		for _, row := range output.SavingsPlansCoverages {
			if row.Coverage == nil {
				continue
			}
			covered, coveredErr := parseFloat64(aws.ToString(row.Coverage.SpendCoveredBySavingsPlans))
			total, totalErr := parseFloat64(aws.ToString(row.Coverage.TotalCost))
			if err := errors.Join(coveredErr, totalErr); err != nil {
				ctx.GetLogger().Warn("skipping savings plan coverage with unreadable amounts", "attributes", row.Attributes, "error", err)
				continue
			}
			service := savingsPlanCoverageService(row.Attributes)
			if _, seen := spendByService[service]; !seen {
				spendByService[service] = &spend{}
				services = append(services, service)
			}
			spendByService[service].covered += covered
			spendByService[service].total += total
		}

		if aws.ToString(output.NextToken) == "" {
			break
		}
		nextToken = output.NextToken
	}

	// Cost Explorer does not say which currency coverage is in. It reports in
	// the account's billing currency, which is the currency its plans are in.
	currency := "USD"
	for _, commitment := range commitments {
		if len(commitment.Resources) > 0 {
			currency = strings.TrimSuffix(commitment.Resources[0].Unit, "_PER_HOUR")
			break
		}
	}

	coverages := []providers.Coverage{}
	for _, service := range services {
		s := spendByService[service]
		if s.total <= 0 {
			continue
		}
		coverages = append(coverages, providers.Coverage{
			Provider:       constants.ProviderAWS,
			CommitmentType: providers.CommitmentTypeSavingsPlan,
			AccountID:      account.AccountNumber,
			Service:        service,
			CoveragePct:    s.covered / s.total * 100,
			CoveredUsage:   s.covered,
			TotalUsage:     s.total,
			UsageUnit:      currency,
			StartTime:      start,
			EndTime:        end,
		})
	}
	return coverages, nil
}

// savingsPlanCoverageService reads the service a coverage row is for. Cost
// Explorer returns every groupable dimension as an attribute and leaves the
// ones not grouped by empty, for example
// {"SERVICE": "AWS Lambda", "REGION": "", "INSTANCE_TYPE_FAMILY": ""}. The key
// is matched without regard to case, and a lone attribute is accepted whatever
// it is called, since the API reference does not specify the spelling.
func savingsPlanCoverageService(attributes map[string]string) string {
	for key, value := range attributes {
		if strings.EqualFold(key, "service") {
			return value
		}
	}
	if len(attributes) == 1 {
		for _, value := range attributes {
			return value
		}
	}
	return ""
}

func (a *awsCommitments) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	return errors.ErrUnsupported
}

func (a *awsCommitments) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	return providers.ApplyCommandResponse{}, nil
}

func (a *awsCommitments) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
	return "", nil
}

func (a *awsCommitments) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (providers.ServiceMapApplication, error) {
	return providers.ServiceMapApplication{}, nil
}
