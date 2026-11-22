package aws

import (
	"context"
	"errors"
	"testing"
	"time"

	"nudgebee/collector/cloud/providers"
	"nudgebee/collector/cloud/providers/constants"

	"github.com/aws/aws-sdk-go-v2/aws"
	cetypes "github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	rdstypes "github.com/aws/aws-sdk-go-v2/service/rds/types"
	sptypes "github.com/aws/aws-sdk-go-v2/service/savingsplans/types"
	"github.com/aws/smithy-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testEc2ReservationId = "0f1e2d3c-aaaa-bbbb-cccc-1234567890ab"

type fakeEc2ReservedInstances struct {
	reservations []ec2types.ReservedInstances
	calls        []*ec2.DescribeReservedInstancesInput
}

func (f *fakeEc2ReservedInstances) DescribeReservedInstances(_ context.Context, params *ec2.DescribeReservedInstancesInput, _ ...func(*ec2.Options)) (*ec2.DescribeReservedInstancesOutput, error) {
	f.calls = append(f.calls, params)
	return &ec2.DescribeReservedInstancesOutput{ReservedInstances: f.reservations}, nil
}

// fakeRdsReservedInstances serves DescribeReservedDBInstances one page per call.
type fakeRdsReservedInstances struct {
	pages [][]rdstypes.ReservedDBInstance
	calls []*rds.DescribeReservedDBInstancesInput
}

func (f *fakeRdsReservedInstances) DescribeReservedDBInstances(_ context.Context, params *rds.DescribeReservedDBInstancesInput, _ ...func(*rds.Options)) (*rds.DescribeReservedDBInstancesOutput, error) {
	f.calls = append(f.calls, params)
	page := len(f.calls) - 1
	output := &rds.DescribeReservedDBInstancesOutput{ReservedDBInstances: f.pages[page]}
	if page < len(f.pages)-1 {
		output.Marker = aws.String("next")
	}
	return output, nil
}

func testReservation(id string, endsIn time.Duration) reservedInstance {
	return reservedInstance{
		commitment: providers.Commitment{
			ID:        id,
			Provider:  constants.ProviderAWS,
			AccountID: commitmentsTestAccount,
			Type:      providers.CommitmentTypeReservedInstance,
			EndTime:   commitmentsTestNow.Add(endsIn),
			Status:    "active",
		},
		matchKeys: []string{id},
		currency:  "USD",
	}
}

func testReservationGroup(attributes map[string]string, purchased, used, unused, unusedCost string) cetypes.ReservationUtilizationGroup {
	return cetypes.ReservationUtilizationGroup{
		Key:        aws.String("SUBSCRIPTION_ID"),
		Value:      aws.String("1234567890"),
		Attributes: attributes,
		Utilization: &cetypes.ReservationAggregates{
			PurchasedHours:       aws.String(purchased),
			TotalActualHours:     aws.String(used),
			UnusedHours:          aws.String(unused),
			RICostForUnusedHours: aws.String(unusedCost),
		},
	}
}

func fixedSource(ceService string, byRegion map[string][]reservedInstance, errByRegion map[string]error) reservedInstanceSource {
	return reservedInstanceSource{
		ceService: ceService,
		list: func(_ providers.CloudProviderContext, region string) ([]reservedInstance, error) {
			if err := errByRegion[region]; err != nil {
				return nil, err
			}
			return byRegion[region], nil
		},
	}
}

func TestListEc2ReservedInstances(t *testing.T) {
	start := time.Date(2025, 11, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	client := &fakeEc2ReservedInstances{reservations: []ec2types.ReservedInstances{
		{
			ReservedInstancesId: aws.String(testEc2ReservationId),
			InstanceType:        ec2types.InstanceTypeM5Large,
			InstanceCount:       aws.Int32(3),
			OfferingClass:       ec2types.OfferingClassTypeStandard,
			Scope:               ec2types.ScopeRegional,
			CurrencyCode:        ec2types.CurrencyCodeValuesUsd,
			Start:               &start,
			End:                 &end,
			State:               ec2types.ReservedInstanceStateActive,
		},
		{InstanceType: ec2types.InstanceTypeM5Large}, // no id: cannot be identified
	}}

	got, err := listEc2ReservedInstances(commitmentsTestCtx(), client, "us-east-1", commitmentsTestAccount)
	require.NoError(t, err)

	// Only active reservations are asked for.
	require.Len(t, client.calls, 1)
	require.Len(t, client.calls[0].Filters, 1)
	assert.Equal(t, "state", aws.ToString(client.calls[0].Filters[0].Name))
	assert.Equal(t, []string{"active"}, client.calls[0].Filters[0].Values)

	require.Len(t, got, 1)
	commitment := got[0].commitment
	assert.Equal(t, testEc2ReservationId, commitment.ID)
	assert.Equal(t, providers.CommitmentTypeReservedInstance, commitment.Type)
	assert.Equal(t, commitmentsTestAccount, commitment.AccountID)
	assert.Equal(t, end, commitment.EndTime)
	assert.Equal(t, []string{testEc2ReservationId}, got[0].matchKeys)
	assert.Equal(t, "USD", got[0].currency)

	require.Len(t, commitment.Resources, 1)
	assert.Equal(t, 3.0, commitment.Resources[0].Quantity)
	assert.Equal(t, ServiceNameEc2, commitment.Resources[0].Service)
	assert.Equal(t, "us-east-1", commitment.Resources[0].Region)
	assert.Equal(t, "m5.large", commitment.Resources[0].ResourceType)
}

func TestListRdsReservedInstances(t *testing.T) {
	start := time.Date(2025, 11, 1, 0, 0, 0, 0, time.UTC)
	active := rdstypes.ReservedDBInstance{
		ReservedDBInstanceId:  aws.String("prod-db-ri"),
		ReservedDBInstanceArn: aws.String("arn:aws:rds:eu-west-1:123456789012:ri:prod-db-ri"),
		LeaseId:               aws.String("lease-1"),
		DBInstanceClass:       aws.String("db.r5.large"),
		DBInstanceCount:       aws.Int32(2),
		Duration:              aws.Int32(31536000),
		StartTime:             &start,
		State:                 aws.String("active"),
		CurrencyCode:          aws.String("USD"),
	}
	retired := active
	retired.ReservedDBInstanceId = aws.String("old-db-ri")
	retired.State = aws.String("retired")
	client := &fakeRdsReservedInstances{pages: [][]rdstypes.ReservedDBInstance{{retired}, {active}}}

	got, err := listRdsReservedInstances(commitmentsTestCtx(), client, "eu-west-1", commitmentsTestAccount)
	require.NoError(t, err)
	require.Len(t, client.calls, 2)
	assert.Equal(t, "next", aws.ToString(client.calls[1].Marker))

	require.Len(t, got, 1)
	commitment := got[0].commitment
	// The name is only unique within a region, so the id carries the region.
	assert.Equal(t, "eu-west-1/prod-db-ri", commitment.ID)
	assert.Equal(t, providers.CommitmentTypeReservedInstance, commitment.Type)
	// RDS gives a start and a duration: one year after 2025-11-01.
	assert.Equal(t, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), commitment.EndTime)
	assert.Equal(t, []string{"lease-1", "arn:aws:rds:eu-west-1:123456789012:ri:prod-db-ri"}, got[0].matchKeys)
	assert.Equal(t, ServiceNameRDS, commitment.Resources[0].Service)
	assert.Equal(t, "db.r5.large", commitment.Resources[0].ResourceType)
	assert.Equal(t, 2.0, commitment.Resources[0].Quantity)
}

func TestReservationUtilizations(t *testing.T) {
	account := providers.Account{AccountNumber: commitmentsTestAccount}
	reservations := []reservedInstance{testReservation(testEc2ReservationId, 200*24*time.Hour)}

	t.Run("reads every page for the service and account, matched by the reservation's id", func(t *testing.T) {
		client := &fakeSavingsPlansUtilization{reservationPages: [][]cetypes.ReservationUtilizationGroup{
			{testReservationGroup(map[string]string{"leaseId": "some-other-reservation"}, "720", "720", "0", "0")},
			{testReservationGroup(map[string]string{"reservationARN": "arn:aws:ec2:us-east-1:123456789012:reserved-instances/" + testEc2ReservationId}, "720", "432", "288", "28.8")},
		}}

		got, err := reservationUtilizations(commitmentsTestCtx(), client, ceServiceEc2, account, reservations, commitmentsTestNow, 30)
		require.NoError(t, err)

		require.Len(t, client.reservationCalls, 2)
		first := client.reservationCalls[0]
		assert.Equal(t, "2026-09-06", aws.ToString(first.TimePeriod.Start))
		assert.Equal(t, "2026-10-06", aws.ToString(first.TimePeriod.End))
		assert.Equal(t, "SUBSCRIPTION_ID", aws.ToString(first.GroupBy[0].Key))
		require.Len(t, first.Filter.And, 2)
		assert.Equal(t, []string{ceServiceEc2}, first.Filter.And[0].Dimensions.Values)
		assert.Equal(t, []string{commitmentsTestAccount}, first.Filter.And[1].Dimensions.Values)
		assert.Equal(t, "next", aws.ToString(client.reservationCalls[1].NextPageToken))

		require.Len(t, got, 1)
		utilization := got[0]
		assert.Equal(t, testEc2ReservationId, utilization.CommitmentID)
		assert.Equal(t, providers.CommitmentTypeReservedInstance, utilization.CommitmentType)
		assert.InDelta(t, 60.0, utilization.UtilizationPct, 0.0001)
		assert.Equal(t, 720.0, utilization.CommittedUsage)
		assert.Equal(t, 432.0, utilization.UsedUsage)
		assert.Equal(t, 288.0, utilization.UnusedUsage)
		assert.Equal(t, "HOURS", utilization.UsageUnit)
		// The waste as money is Cost Explorer's own figure for the unused hours.
		assert.Equal(t, 28.8, utilization.UnusedCost)
		assert.Equal(t, "USD", utilization.Currency)
	})

	t.Run("no currency leaves the waste in hours only", func(t *testing.T) {
		reservation := testReservation(testEc2ReservationId, 200*24*time.Hour)
		reservation.currency = ""
		client := &fakeSavingsPlansUtilization{reservationPages: [][]cetypes.ReservationUtilizationGroup{
			{testReservationGroup(map[string]string{"leaseId": testEc2ReservationId}, "720", "432", "288", "28.8")},
		}}

		got, err := reservationUtilizations(commitmentsTestCtx(), client, ceServiceEc2, account, []reservedInstance{reservation}, commitmentsTestNow, 30)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Empty(t, got[0].Currency)
		assert.Zero(t, got[0].UnusedCost)
	})

	t.Run("nothing purchased in the window and unreadable hours report nothing", func(t *testing.T) {
		client := &fakeSavingsPlansUtilization{reservationPages: [][]cetypes.ReservationUtilizationGroup{{
			testReservationGroup(map[string]string{"leaseId": testEc2ReservationId}, "0", "0", "0", "0"),
			testReservationGroup(map[string]string{"leaseId": testEc2ReservationId}, "", "432", "288", "28.8"),
		}}}

		got, err := reservationUtilizations(commitmentsTestCtx(), client, ceServiceEc2, account, reservations, commitmentsTestNow, 30)
		require.NoError(t, err)
		assert.Empty(t, got)
	})
}

func TestReservedInstanceCommitments(t *testing.T) {
	account := providers.Account{AccountNumber: commitmentsTestAccount}
	regions := []string{"us-east-1", "eu-west-1"}

	t.Run("reads every region and each service's utilization", func(t *testing.T) {
		rdsReservation := testReservation("eu-west-1/prod-db-ri", 200*24*time.Hour)
		rdsReservation.matchKeys = []string{"lease-1"}
		sources := []reservedInstanceSource{
			fixedSource(ceServiceEc2, map[string][]reservedInstance{"us-east-1": {testReservation(testEc2ReservationId, 200*24*time.Hour)}}, nil),
			fixedSource(ceServiceRds, map[string][]reservedInstance{"eu-west-1": {rdsReservation}}, nil),
		}
		// The fake serves one page per call: the EC2 call gets this page, and the
		// RDS call that follows gets an empty one.
		client := &fakeSavingsPlansUtilization{reservationPages: [][]cetypes.ReservationUtilizationGroup{{
			testReservationGroup(map[string]string{"leaseId": testEc2ReservationId}, "720", "432", "288", "28.8"),
		}}}

		commitments, utilizations, err := reservedInstanceCommitments(commitmentsTestCtx(), sources, client, account, regions, commitmentsTestNow, 30)
		require.NoError(t, err)
		require.Len(t, commitments, 2)
		assert.Equal(t, testEc2ReservationId, commitments[0].ID)
		assert.Equal(t, "eu-west-1/prod-db-ri", commitments[1].ID)

		require.Len(t, client.reservationCalls, 2)
		assert.Equal(t, []string{ceServiceEc2}, client.reservationCalls[0].Filter.And[0].Dimensions.Values)
		assert.Equal(t, []string{ceServiceRds}, client.reservationCalls[1].Filter.And[0].Dimensions.Values)
		require.Len(t, utilizations, 1)
		assert.Equal(t, testEc2ReservationId, utilizations[0].CommitmentID)
	})

	t.Run("a service with no reservations is not asked for utilization", func(t *testing.T) {
		client := &fakeSavingsPlansUtilization{}
		commitments, utilizations, err := reservedInstanceCommitments(commitmentsTestCtx(), []reservedInstanceSource{fixedSource(ceServiceEc2, nil, nil)}, client, account, regions, commitmentsTestNow, 30)
		require.NoError(t, err)
		assert.Empty(t, commitments)
		assert.Empty(t, utilizations)
		assert.Empty(t, client.reservationCalls)
	})

	t.Run("a region the role cannot read is skipped", func(t *testing.T) {
		for _, regionErr := range []error{
			&smithy.GenericAPIError{Code: "UnauthorizedOperation", Message: "not authorized"},
			&smithy.GenericAPIError{Code: "AuthFailure", Message: "region not enabled"},
		} {
			source := fixedSource(ceServiceEc2,
				map[string][]reservedInstance{"us-east-1": {testReservation(testEc2ReservationId, 200*24*time.Hour)}},
				map[string]error{"eu-west-1": regionErr},
			)
			commitments, _, err := reservedInstanceCommitments(commitmentsTestCtx(), []reservedInstanceSource{source}, &fakeSavingsPlansUtilization{}, account, regions, commitmentsTestNow, 30)
			require.NoError(t, err)
			assert.Len(t, commitments, 1)
		}
	})

	// A partial list would archive the recommendations of the regions left out.
	t.Run("any other regional failure fails the read", func(t *testing.T) {
		source := fixedSource(ceServiceEc2, nil, map[string]error{"eu-west-1": &smithy.GenericAPIError{Code: "RequestLimitExceeded", Message: "rate exceeded"}})
		_, _, err := reservedInstanceCommitments(commitmentsTestCtx(), []reservedInstanceSource{source}, &fakeSavingsPlansUtilization{}, account, regions, commitmentsTestNow, 30)
		require.Error(t, err)
	})

	t.Run("unavailable utilization still reports the reservations for expiry", func(t *testing.T) {
		source := fixedSource(ceServiceEc2, map[string][]reservedInstance{"us-east-1": {testReservation(testEc2ReservationId, 200*24*time.Hour)}}, nil)
		client := &fakeSavingsPlansUtilization{reservationErr: &cetypes.DataUnavailableException{Message: aws.String("no data")}}

		commitments, utilizations, err := reservedInstanceCommitments(commitmentsTestCtx(), []reservedInstanceSource{source}, client, account, regions, commitmentsTestNow, 30)
		require.NoError(t, err)
		assert.Len(t, commitments, 1)
		assert.Empty(t, utilizations)
	})

	t.Run("any other utilization failure fails the read", func(t *testing.T) {
		source := fixedSource(ceServiceEc2, map[string][]reservedInstance{"us-east-1": {testReservation(testEc2ReservationId, 200*24*time.Hour)}}, nil)
		client := &fakeSavingsPlansUtilization{reservationErr: errors.New("ThrottlingException: rate exceeded")}

		_, _, err := reservedInstanceCommitments(commitmentsTestCtx(), []reservedInstanceSource{source}, client, account, regions, commitmentsTestNow, 30)
		require.Error(t, err)
	})
}

// A role that may not list regions still gets its Savings Plan findings; any
// other failure to list them leaves the reservations unknown and fails the read.
func TestCollectCommitments_RegionsNotListable(t *testing.T) {
	account := providers.Account{AccountNumber: commitmentsTestAccount}
	clientsFailingWith := func(regionsErr error) commitmentClients {
		return commitmentClients{
			plans:             &fakeSavingsPlans{pages: [][]sptypes.SavingsPlan{{testSavingsPlan(commitmentsTestAccount, "sp-1")}}},
			costExplorer:      &fakeSavingsPlansUtilization{},
			reservedInstances: []reservedInstanceSource{fixedSource(ceServiceEc2, nil, nil)},
			regions:           func() ([]string, error) { return nil, regionsErr },
		}
	}

	commitments, _, _, err := collectCommitments(commitmentsTestCtx(), clientsFailingWith(&smithy.GenericAPIError{Code: "UnauthorizedOperation", Message: "not authorized to perform ec2:DescribeRegions"}), account, commitmentsTestNow, 30)
	require.NoError(t, err)
	require.Len(t, commitments, 1)
	assert.Equal(t, "sp-1", commitments[0].ID)

	_, _, _, err = collectCommitments(commitmentsTestCtx(), clientsFailingWith(errors.New("RequestLimitExceeded: rate exceeded")), account, commitmentsTestNow, 30)
	require.Error(t, err)
}

// A role denied only the Savings Plans API still has its Reserved Instances
// reported: one unreadable source does not take the others with it.
func TestCommitmentRecommendations_PlansDeniedStillReportsReservedInstances(t *testing.T) {
	clients := commitmentClients{
		plans:        &fakeSavingsPlans{err: errors.New("operation error savingsplans: DescribeSavingsPlans, AccessDeniedException: not authorized")},
		costExplorer: &fakeSavingsPlansUtilization{},
		reservedInstances: []reservedInstanceSource{
			fixedSource(ceServiceEc2, map[string][]reservedInstance{"us-east-1": {testReservation(testEc2ReservationId, 20*24*time.Hour)}}, nil),
		},
		regions: func() ([]string, error) { return []string{"us-east-1"}, nil },
	}

	got, err := commitmentRecommendations(commitmentsTestCtx(), clients, providers.Account{AccountNumber: commitmentsTestAccount}, commitmentsTestNow)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, constants.CommitmentExpiringSoon, got[0].RuleName)
	assert.Equal(t, testEc2ReservationId, got[0].ResourceId)
}

// Cost Explorer keys a group by subscription id, which is also checked in case
// it is the reservation's own id.
func TestMatchReservation_ByGroupValue(t *testing.T) {
	group := testReservationGroup(nil, "720", "432", "288", "28.8")
	group.Value = aws.String(testEc2ReservationId)
	got, ok := matchReservation([]reservedInstance{testReservation(testEc2ReservationId, time.Hour)}, group)
	require.True(t, ok)
	assert.Equal(t, testEc2ReservationId, got.commitment.ID)
}

// Savings Plans and Reserved Instances go through the same rules: an under-used
// reservation that ends in 20 days raises both findings on the reservation's id.
func TestCommitmentRecommendations_ReservedInstances(t *testing.T) {
	clients := commitmentClients{
		plans: &fakeSavingsPlans{pages: [][]sptypes.SavingsPlan{{}}},
		costExplorer: &fakeSavingsPlansUtilization{reservationPages: [][]cetypes.ReservationUtilizationGroup{
			{testReservationGroup(map[string]string{"leaseId": testEc2ReservationId}, "720", "288", "432", "43.2")},
		}},
		reservedInstances: []reservedInstanceSource{
			fixedSource(ceServiceEc2, map[string][]reservedInstance{"us-east-1": {testReservation(testEc2ReservationId, 20*24*time.Hour)}}, nil),
		},
		regions: func() ([]string, error) { return []string{"us-east-1"}, nil },
	}

	got, err := commitmentRecommendations(commitmentsTestCtx(), clients, providers.Account{AccountNumber: commitmentsTestAccount}, commitmentsTestNow)
	require.NoError(t, err)
	require.Len(t, got, 2)

	rules := map[string]providers.Recommendation{}
	for _, r := range got {
		rules[r.RuleName] = r
		assert.Equal(t, testEc2ReservationId, r.ResourceId)
		assert.Equal(t, "reserved-instance", r.ResourceType)
	}
	underused := rules[constants.UnderutilizedCommitment]
	assert.Equal(t, providers.RecommendationSeverityHigh, underused.Severity) // 40% used
	assert.Equal(t, "HOURS", underused.Data["usage_unit"])
	assert.Equal(t, 432.0, underused.Data["unused_commitment"])
	assert.Equal(t, 43.2, underused.Data["unused_cost"])
	assert.Equal(t, 20, rules[constants.CommitmentExpiringSoon].Data["days_remaining"])
}
