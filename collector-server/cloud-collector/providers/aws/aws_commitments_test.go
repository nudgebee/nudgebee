package aws

import (
	"context"
	"errors"
	"testing"
	"time"

	"nudgebee/collector/cloud/config"
	"nudgebee/collector/cloud/providers"
	"nudgebee/collector/cloud/providers/constants"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
	cetypes "github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
	"github.com/aws/aws-sdk-go-v2/service/savingsplans"
	sptypes "github.com/aws/aws-sdk-go-v2/service/savingsplans/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const commitmentsTestAccount = "123456789012"

var commitmentsTestNow = time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)

// fakeSavingsPlans serves DescribeSavingsPlans one page per call.
type fakeSavingsPlans struct {
	pages [][]sptypes.SavingsPlan
	err   error
	calls []*savingsplans.DescribeSavingsPlansInput
}

func (f *fakeSavingsPlans) DescribeSavingsPlans(_ context.Context, params *savingsplans.DescribeSavingsPlansInput, _ ...func(*savingsplans.Options)) (*savingsplans.DescribeSavingsPlansOutput, error) {
	f.calls = append(f.calls, params)
	if f.err != nil {
		return nil, f.err
	}
	page := len(f.calls) - 1
	output := &savingsplans.DescribeSavingsPlansOutput{SavingsPlans: f.pages[page]}
	if page < len(f.pages)-1 {
		output.NextToken = aws.String("next")
	}
	return output, nil
}

// fakeSavingsPlansUtilization stands in for Cost Explorer, serving each of the
// commitment calls one page per call.
type fakeSavingsPlansUtilization struct {
	pages [][]cetypes.SavingsPlansUtilizationDetail
	err   error
	calls []*costexplorer.GetSavingsPlansUtilizationDetailsInput

	coveragePages [][]cetypes.SavingsPlansCoverage
	coverageErr   error
	coverageCalls []*costexplorer.GetSavingsPlansCoverageInput

	reservationPages [][]cetypes.ReservationUtilizationGroup
	reservationErr   error
	reservationCalls []*costexplorer.GetReservationUtilizationInput
}

func (f *fakeSavingsPlansUtilization) GetSavingsPlansUtilizationDetails(_ context.Context, params *costexplorer.GetSavingsPlansUtilizationDetailsInput, _ ...func(*costexplorer.Options)) (*costexplorer.GetSavingsPlansUtilizationDetailsOutput, error) {
	f.calls = append(f.calls, params)
	if f.err != nil {
		return nil, f.err
	}
	page := len(f.calls) - 1
	if page >= len(f.pages) {
		return &costexplorer.GetSavingsPlansUtilizationDetailsOutput{}, nil
	}
	output := &costexplorer.GetSavingsPlansUtilizationDetailsOutput{SavingsPlansUtilizationDetails: f.pages[page]}
	if page < len(f.pages)-1 {
		output.NextToken = aws.String("next")
	}
	return output, nil
}

func (f *fakeSavingsPlansUtilization) GetSavingsPlansCoverage(_ context.Context, params *costexplorer.GetSavingsPlansCoverageInput, _ ...func(*costexplorer.Options)) (*costexplorer.GetSavingsPlansCoverageOutput, error) {
	f.coverageCalls = append(f.coverageCalls, params)
	if f.coverageErr != nil {
		return nil, f.coverageErr
	}
	page := len(f.coverageCalls) - 1
	if page >= len(f.coveragePages) {
		return &costexplorer.GetSavingsPlansCoverageOutput{}, nil
	}
	output := &costexplorer.GetSavingsPlansCoverageOutput{SavingsPlansCoverages: f.coveragePages[page]}
	if page < len(f.coveragePages)-1 {
		output.NextToken = aws.String("next")
	}
	return output, nil
}

func (f *fakeSavingsPlansUtilization) GetReservationUtilization(_ context.Context, params *costexplorer.GetReservationUtilizationInput, _ ...func(*costexplorer.Options)) (*costexplorer.GetReservationUtilizationOutput, error) {
	f.reservationCalls = append(f.reservationCalls, params)
	if f.reservationErr != nil {
		return nil, f.reservationErr
	}
	page := len(f.reservationCalls) - 1
	if page >= len(f.reservationPages) {
		return &costexplorer.GetReservationUtilizationOutput{}, nil
	}
	output := &costexplorer.GetReservationUtilizationOutput{
		UtilizationsByTime: []cetypes.UtilizationByTime{{Groups: f.reservationPages[page]}},
	}
	if page < len(f.reservationPages)-1 {
		output.NextPageToken = aws.String("next")
	}
	return output, nil
}

func testSavingsPlanCoverage(service, covered, total string) cetypes.SavingsPlansCoverage {
	return cetypes.SavingsPlansCoverage{
		// The shape a real Cost Explorer response has when grouped by service.
		Attributes: map[string]string{"SERVICE": service, "REGION": "", "INSTANCE_TYPE_FAMILY": ""},
		Coverage: &cetypes.SavingsPlansCoverageData{
			SpendCoveredBySavingsPlans: aws.String(covered),
			TotalCost:                  aws.String(total),
		},
	}
}

func testSavingsPlan(owner, id string) sptypes.SavingsPlan {
	return sptypes.SavingsPlan{
		SavingsPlanId:   aws.String(id),
		SavingsPlanArn:  aws.String("arn:aws:savingsplans::" + owner + ":savingsplan/" + id),
		SavingsPlanType: sptypes.SavingsPlanTypeCompute,
		PaymentOption:   sptypes.SavingsPlanPaymentOptionNoUpfront,
		ProductTypes:    []sptypes.SavingsPlanProductType{sptypes.SavingsPlanProductTypeEc2, sptypes.SavingsPlanProductTypeFargate},
		Commitment:      aws.String("10.0"),
		Currency:        sptypes.CurrencyCodeUsd,
		Start:           aws.String("2025-11-01T00:00:00.000Z"),
		End:             aws.String("2026-11-01T00:00:00.000Z"),
		State:           sptypes.SavingsPlanStateActive,
	}
}

func testSavingsPlanUtilization(owner, id, total, used, unused, pct string) cetypes.SavingsPlansUtilizationDetail {
	return cetypes.SavingsPlansUtilizationDetail{
		SavingsPlanArn: aws.String("arn:aws:savingsplans::" + owner + ":savingsplan/" + id),
		Utilization: &cetypes.SavingsPlansUtilization{
			TotalCommitment:       aws.String(total),
			UsedCommitment:        aws.String(used),
			UnusedCommitment:      aws.String(unused),
			UtilizationPercentage: aws.String(pct),
		},
	}
}

func commitmentsTestCtx() providers.CloudProviderContext {
	return &targetedFetchTestCtx{ctx: context.Background()}
}

func TestSavingsPlanToCommitment(t *testing.T) {
	t.Run("converts to the provider-neutral model", func(t *testing.T) {
		plan := testSavingsPlan(commitmentsTestAccount, "sp-1")
		plan.Region = aws.String("us-east-1")
		plan.Ec2InstanceFamily = aws.String("m5")

		got, ok := savingsPlanToCommitment(plan)
		require.True(t, ok)
		assert.Equal(t, "sp-1", got.ID)
		assert.Equal(t, constants.ProviderAWS, got.Provider)
		assert.Equal(t, commitmentsTestAccount, got.AccountID)
		assert.Equal(t, providers.CommitmentTypeSavingsPlan, got.Type)
		assert.Equal(t, "active", got.Status)
		assert.Equal(t, time.Date(2025, 11, 1, 0, 0, 0, 0, time.UTC), got.StartTime.UTC())
		assert.Equal(t, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), got.EndTime.UTC())

		require.Len(t, got.Resources, 1)
		resource := got.Resources[0]
		assert.Equal(t, 10.0, resource.Quantity)
		assert.Equal(t, "USD_PER_HOUR", resource.Unit)
		assert.Equal(t, "EC2,Fargate", resource.Service)
		assert.Equal(t, "us-east-1", resource.Region)
		assert.Equal(t, "m5", resource.ResourceType)
		assert.Equal(t, "Compute", resource.Metadata["savings_plan_type"])
	})

	t.Run("unreadable end date is left unknown", func(t *testing.T) {
		plan := testSavingsPlan(commitmentsTestAccount, "sp-1")
		plan.End = aws.String("not-a-date")
		got, ok := savingsPlanToCommitment(plan)
		require.True(t, ok)
		assert.True(t, got.EndTime.IsZero())
	})

	t.Run("no id or no readable owner is rejected", func(t *testing.T) {
		noID := testSavingsPlan(commitmentsTestAccount, "sp-1")
		noID.SavingsPlanId = nil
		_, ok := savingsPlanToCommitment(noID)
		assert.False(t, ok)

		badArn := testSavingsPlan(commitmentsTestAccount, "sp-1")
		badArn.SavingsPlanArn = aws.String("sp-1")
		_, ok = savingsPlanToCommitment(badArn)
		assert.False(t, ok)
	})
}

func TestSavingsPlanCommitments(t *testing.T) {
	account := providers.Account{AccountNumber: commitmentsTestAccount}

	t.Run("reads every page and joins utilization to its plan", func(t *testing.T) {
		plans := &fakeSavingsPlans{pages: [][]sptypes.SavingsPlan{
			{testSavingsPlan(commitmentsTestAccount, "sp-1")},
			{testSavingsPlan(commitmentsTestAccount, "sp-2")},
		}}
		utilization := &fakeSavingsPlansUtilization{pages: [][]cetypes.SavingsPlansUtilizationDetail{
			{testSavingsPlanUtilization(commitmentsTestAccount, "sp-1", "7200", "4320", "2880", "60")},
			{testSavingsPlanUtilization(commitmentsTestAccount, "sp-2", "720", "720", "0", "100")},
		}}

		commitments, utilizations, err := savingsPlanCommitments(commitmentsTestCtx(), plans, utilization, account, commitmentsTestNow, savingsPlanUtilizationLookbackDays)
		require.NoError(t, err)
		require.Len(t, commitments, 2)
		require.Len(t, utilizations, 2)

		require.Len(t, plans.calls, 2)
		assert.Equal(t, []sptypes.SavingsPlanState{sptypes.SavingsPlanStateActive}, plans.calls[0].States)
		assert.Nil(t, plans.calls[0].NextToken)
		assert.Equal(t, "next", aws.ToString(plans.calls[1].NextToken))

		// A 30-day window ending yesterday: Cost Explorer rejects an end date that
		// is not before the current date.
		require.Len(t, utilization.calls, 2)
		assert.Equal(t, "2026-09-06", aws.ToString(utilization.calls[0].TimePeriod.Start))
		assert.Equal(t, "2026-10-06", aws.ToString(utilization.calls[0].TimePeriod.End))
		assert.Equal(t, "next", aws.ToString(utilization.calls[1].NextToken))

		first := utilizations[0]
		assert.Equal(t, "sp-1", first.CommitmentID)
		assert.Equal(t, 60.0, first.UtilizationPct)
		assert.Equal(t, 7200.0, first.CommittedUsage)
		assert.Equal(t, 4320.0, first.UsedUsage)
		assert.Equal(t, 2880.0, first.UnusedUsage)
		assert.Equal(t, 2880.0, first.UnusedCost)
		assert.Equal(t, "USD", first.Currency)
		assert.Equal(t, "USD", first.UsageUnit)
	})

	// A payer account sees its member accounts' plans. Reporting those here too
	// would raise the same finding under every connected account that sees them.
	t.Run("plans owned by another account are left out", func(t *testing.T) {
		plans := &fakeSavingsPlans{pages: [][]sptypes.SavingsPlan{{
			testSavingsPlan(commitmentsTestAccount, "sp-own"),
			testSavingsPlan("999999999999", "sp-other"),
		}}}
		utilization := &fakeSavingsPlansUtilization{pages: [][]cetypes.SavingsPlansUtilizationDetail{{
			testSavingsPlanUtilization(commitmentsTestAccount, "sp-own", "7200", "4320", "2880", "60"),
			testSavingsPlanUtilization("999999999999", "sp-other", "7200", "720", "6480", "10"),
		}}}

		commitments, utilizations, err := savingsPlanCommitments(commitmentsTestCtx(), plans, utilization, account, commitmentsTestNow, savingsPlanUtilizationLookbackDays)
		require.NoError(t, err)
		require.Len(t, commitments, 1)
		assert.Equal(t, "sp-own", commitments[0].ID)
		require.Len(t, utilizations, 1)
		assert.Equal(t, "sp-own", utilizations[0].CommitmentID)
	})

	t.Run("no owned plans skips the utilization call", func(t *testing.T) {
		plans := &fakeSavingsPlans{pages: [][]sptypes.SavingsPlan{{}}}
		utilization := &fakeSavingsPlansUtilization{}

		commitments, utilizations, err := savingsPlanCommitments(commitmentsTestCtx(), plans, utilization, account, commitmentsTestNow, savingsPlanUtilizationLookbackDays)
		require.NoError(t, err)
		assert.Empty(t, commitments)
		assert.Empty(t, utilizations)
		assert.Empty(t, utilization.calls)
	})

	t.Run("unreadable utilization amounts are skipped", func(t *testing.T) {
		plans := &fakeSavingsPlans{pages: [][]sptypes.SavingsPlan{{testSavingsPlan(commitmentsTestAccount, "sp-1")}}}
		utilization := &fakeSavingsPlansUtilization{pages: [][]cetypes.SavingsPlansUtilizationDetail{{
			testSavingsPlanUtilization(commitmentsTestAccount, "sp-1", "7200", "", "2880", "60"),
		}}}

		commitments, utilizations, err := savingsPlanCommitments(commitmentsTestCtx(), plans, utilization, account, commitmentsTestNow, savingsPlanUtilizationLookbackDays)
		require.NoError(t, err)
		assert.Len(t, commitments, 1)
		assert.Empty(t, utilizations)
	})

	t.Run("a failure to read plans is an error", func(t *testing.T) {
		for _, plansErr := range []error{
			errors.New("ThrottlingException: rate exceeded"),
			errors.New("operation error savingsplans: DescribeSavingsPlans, AccessDeniedException: not authorized"),
		} {
			_, _, err := savingsPlanCommitments(commitmentsTestCtx(), &fakeSavingsPlans{err: plansErr}, &fakeSavingsPlansUtilization{}, account, commitmentsTestNow, savingsPlanUtilizationLookbackDays)
			require.ErrorIs(t, err, plansErr)
		}
	})

	t.Run("the utilization window follows the requested lookback", func(t *testing.T) {
		plans := &fakeSavingsPlans{pages: [][]sptypes.SavingsPlan{{testSavingsPlan(commitmentsTestAccount, "sp-1")}}}
		utilization := &fakeSavingsPlansUtilization{pages: [][]cetypes.SavingsPlansUtilizationDetail{{}}}

		_, _, err := savingsPlanCommitments(commitmentsTestCtx(), plans, utilization, account, commitmentsTestNow, 7)
		require.NoError(t, err)
		require.Len(t, utilization.calls, 1)
		assert.Equal(t, "2026-09-29", aws.ToString(utilization.calls[0].TimePeriod.Start))
		assert.Equal(t, "2026-10-06", aws.ToString(utilization.calls[0].TimePeriod.End))
	})

	t.Run("unavailable utilization still reports the plans for expiry", func(t *testing.T) {
		for _, utilizationErr := range []error{
			&cetypes.DataUnavailableException{Message: aws.String("no data")},
			errors.New("AccessDeniedException: not authorized to perform ce:GetSavingsPlansUtilizationDetails"),
		} {
			plans := &fakeSavingsPlans{pages: [][]sptypes.SavingsPlan{{testSavingsPlan(commitmentsTestAccount, "sp-1")}}}

			commitments, utilizations, err := savingsPlanCommitments(commitmentsTestCtx(), plans, &fakeSavingsPlansUtilization{err: utilizationErr}, account, commitmentsTestNow, savingsPlanUtilizationLookbackDays)
			require.NoError(t, err)
			assert.Len(t, commitments, 1)
			assert.Empty(t, utilizations)
		}
	})

	t.Run("any other failure to read utilization is an error", func(t *testing.T) {
		plans := &fakeSavingsPlans{pages: [][]sptypes.SavingsPlan{{testSavingsPlan(commitmentsTestAccount, "sp-1")}}}

		_, _, err := savingsPlanCommitments(commitmentsTestCtx(), plans, &fakeSavingsPlansUtilization{err: errors.New("ThrottlingException: rate exceeded")}, account, commitmentsTestNow, savingsPlanUtilizationLookbackDays)
		require.Error(t, err)
	})
}

// The plan ends 2026-11-01, 24 days after the test's "now", and was 60% used:
// both rules fire and land on rows keyed by the plan's own id.
func TestSavingsPlanCommitments_FeedTheSharedRules(t *testing.T) {
	plans := &fakeSavingsPlans{pages: [][]sptypes.SavingsPlan{{testSavingsPlan(commitmentsTestAccount, "sp-1")}}}
	utilization := &fakeSavingsPlansUtilization{pages: [][]cetypes.SavingsPlansUtilizationDetail{{
		testSavingsPlanUtilization(commitmentsTestAccount, "sp-1", "7200", "4320", "2880", "60"),
	}}}

	commitments, utilizations, err := savingsPlanCommitments(commitmentsTestCtx(), plans, utilization, providers.Account{AccountNumber: commitmentsTestAccount}, commitmentsTestNow, savingsPlanUtilizationLookbackDays)
	require.NoError(t, err)

	recommendations := providers.CommitmentRecommendations(commitments, utilizations, commitmentsTestNow)
	require.Len(t, recommendations, 2)
	rules := []string{}
	for _, r := range recommendations {
		rules = append(rules, r.RuleName)
		assert.Equal(t, "sp-1", r.ResourceId)
		assert.Equal(t, ServiceNameCommitments, r.ResourceServiceName)
	}
	assert.ElementsMatch(t, []string{constants.UnderutilizedCommitment, constants.CommitmentExpiringSoon}, rules)
}

func TestSavingsPlanCoverages(t *testing.T) {
	account := providers.Account{AccountNumber: commitmentsTestAccount}

	t.Run("reads every page, limited to the account's own usage, grouped by service", func(t *testing.T) {
		client := &fakeSavingsPlansUtilization{coveragePages: [][]cetypes.SavingsPlansCoverage{
			{testSavingsPlanCoverage("Amazon Elastic Compute Cloud - Compute", "510", "900")},
			{testSavingsPlanCoverage("AWS Lambda", "90", "100")},
		}}

		got, err := savingsPlanCoverages(commitmentsTestCtx(), client, account, nil, commitmentsTestNow, 30)
		require.NoError(t, err)

		require.Len(t, client.coverageCalls, 2)
		first := client.coverageCalls[0]
		assert.Equal(t, "2026-09-06", aws.ToString(first.TimePeriod.Start))
		assert.Equal(t, "2026-10-06", aws.ToString(first.TimePeriod.End))
		require.Len(t, first.GroupBy, 1)
		assert.Equal(t, "SERVICE", aws.ToString(first.GroupBy[0].Key))
		assert.Equal(t, cetypes.DimensionLinkedAccount, first.Filter.Dimensions.Key)
		assert.Equal(t, []string{commitmentsTestAccount}, first.Filter.Dimensions.Values)
		assert.Equal(t, "next", aws.ToString(client.coverageCalls[1].NextToken))

		require.Len(t, got, 2)
		ec2 := got[0]
		assert.Equal(t, "Amazon Elastic Compute Cloud - Compute", ec2.Service)
		assert.Equal(t, providers.CommitmentTypeSavingsPlan, ec2.CommitmentType)
		assert.Equal(t, commitmentsTestAccount, ec2.AccountID)
		assert.Equal(t, 510.0, ec2.CoveredUsage)
		assert.Equal(t, 900.0, ec2.TotalUsage)
		assert.InDelta(t, 56.6667, ec2.CoveragePct, 0.001)
		assert.Equal(t, "USD", ec2.UsageUnit)
	})

	t.Run("rows for one service are added together", func(t *testing.T) {
		client := &fakeSavingsPlansUtilization{coveragePages: [][]cetypes.SavingsPlansCoverage{{
			testSavingsPlanCoverage("AWS Lambda", "40", "50"),
			testSavingsPlanCoverage("AWS Lambda", "50", "50"),
		}}}

		got, err := savingsPlanCoverages(commitmentsTestCtx(), client, account, nil, commitmentsTestNow, 30)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, 90.0, got[0].CoveredUsage)
		assert.Equal(t, 100.0, got[0].TotalUsage)
		assert.InDelta(t, 90.0, got[0].CoveragePct, 0.0001)
	})

	t.Run("coverage is in the currency the account's plans are in", func(t *testing.T) {
		plan := testSavingsPlan(commitmentsTestAccount, "sp-1")
		plan.Currency = sptypes.CurrencyCodeCny
		commitment, ok := savingsPlanToCommitment(plan)
		require.True(t, ok)
		client := &fakeSavingsPlansUtilization{coveragePages: [][]cetypes.SavingsPlansCoverage{{testSavingsPlanCoverage("AWS Lambda", "90", "100")}}}

		got, err := savingsPlanCoverages(commitmentsTestCtx(), client, account, []providers.Commitment{commitment}, commitmentsTestNow, 30)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, "CNY", got[0].UsageUnit)
	})

	t.Run("no eligible usage and unreadable amounts report nothing", func(t *testing.T) {
		client := &fakeSavingsPlansUtilization{coveragePages: [][]cetypes.SavingsPlansCoverage{{
			testSavingsPlanCoverage("AWS Lambda", "0", "0"),
			testSavingsPlanCoverage("Amazon SageMaker", "", "100"),
		}}}

		got, err := savingsPlanCoverages(commitmentsTestCtx(), client, account, nil, commitmentsTestNow, 30)
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("unavailable coverage reports none without failing", func(t *testing.T) {
		for _, coverageErr := range []error{
			&cetypes.DataUnavailableException{Message: aws.String("no data")},
			errors.New("AccessDeniedException: not authorized to perform ce:GetSavingsPlansCoverage"),
		} {
			got, err := savingsPlanCoverages(commitmentsTestCtx(), &fakeSavingsPlansUtilization{coverageErr: coverageErr}, account, nil, commitmentsTestNow, 30)
			require.NoError(t, err)
			assert.Empty(t, got)
		}
	})

	t.Run("any other failure is an error", func(t *testing.T) {
		_, err := savingsPlanCoverages(commitmentsTestCtx(), &fakeSavingsPlansUtilization{coverageErr: errors.New("ThrottlingException: rate exceeded")}, account, nil, commitmentsTestNow, 30)
		require.Error(t, err)
	})
}

// An account that owns no Savings Plan is already told to buy one by the Cost
// Explorer purchase recommendations, so its coverage raises nothing here; once
// it owns a plan, coverage that has fallen behind does.
func TestCommitmentRecommendations_CoverageNeedsAnOwnedPlan(t *testing.T) {
	account := providers.Account{AccountNumber: commitmentsTestAccount}
	uncovered := [][]cetypes.SavingsPlansCoverage{{testSavingsPlanCoverage("Amazon Elastic Compute Cloud - Compute", "0", "1800")}}

	costExplorer := &fakeSavingsPlansUtilization{coveragePages: uncovered}
	got, err := commitmentRecommendations(commitmentsTestCtx(), commitmentClients{plans: &fakeSavingsPlans{pages: [][]sptypes.SavingsPlan{{}}}, costExplorer: costExplorer}, account, commitmentsTestNow)
	require.NoError(t, err)
	assert.Empty(t, got)
	assert.Empty(t, costExplorer.calls, "no plans, so no utilization to ask for")

	plan := testSavingsPlan(commitmentsTestAccount, "sp-1")
	plan.End = aws.String("2027-11-01T00:00:00.000Z")
	got, err = commitmentRecommendations(commitmentsTestCtx(), commitmentClients{plans: &fakeSavingsPlans{pages: [][]sptypes.SavingsPlan{{plan}}}, costExplorer: &fakeSavingsPlansUtilization{coveragePages: uncovered}}, account, commitmentsTestNow)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, constants.LowCommitmentCoverage, got[0].RuleName)
	assert.Equal(t, providers.RecommendationSeverityHigh, got[0].Severity)
}

// The pass is behind a flag that is off by default; while off it reports
// nothing and makes no AWS call.
func TestCommitmentsPassIsOffByDefault(t *testing.T) {
	require.False(t, config.Config.CloudCollectorCommitmentRecommendationsEnabled)

	got, err := (&awsCommitments{}).GetRecommendations(commitmentsTestCtx(), providers.Account{AccountNumber: commitmentsTestAccount}, providers.ListRecommendationsRequest{ServiceName: "commitments"}, nil)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestCommitmentRecommendations_PlansNotReadable(t *testing.T) {
	account := providers.Account{AccountNumber: commitmentsTestAccount}

	// A role denied the Savings Plans API has no plans to report. That does not
	// fail the pass, and does not stop its other commitments being reported.
	t.Run("lack of permission reports nothing for plans", func(t *testing.T) {
		plans := &fakeSavingsPlans{err: errors.New("operation error savingsplans: DescribeSavingsPlans, AccessDeniedException: not authorized")}

		got, err := commitmentRecommendations(commitmentsTestCtx(), commitmentClients{plans: plans, costExplorer: &fakeSavingsPlansUtilization{}}, account, commitmentsTestNow)
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	// Reporting "no recommendations" for a failed scan would archive every
	// commitment recommendation the account has, so the failure must surface.
	t.Run("any other failure is an error", func(t *testing.T) {
		plans := &fakeSavingsPlans{err: errors.New("ThrottlingException: rate exceeded")}

		_, err := commitmentRecommendations(commitmentsTestCtx(), commitmentClients{plans: plans, costExplorer: &fakeSavingsPlansUtilization{}}, account, commitmentsTestNow)
		require.Error(t, err)
	})
}

func TestCommitmentsServiceIsRegistered(t *testing.T) {
	service, ok := GetAwsService("commitments")
	require.True(t, ok)
	assert.NotNil(t, service)
}
