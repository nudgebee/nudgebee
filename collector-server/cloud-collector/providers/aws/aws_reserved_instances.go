package aws

import (
	"context"
	"errors"
	"fmt"
	"nudgebee/collector/cloud/providers"
	"nudgebee/collector/cloud/providers/constants"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
	cetypes "github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	rdstypes "github.com/aws/aws-sdk-go-v2/service/rds/types"
	"github.com/aws/smithy-go"
	"golang.org/x/sync/errgroup"
)

// The SERVICE values Cost Explorer files Reserved Instance usage under.
const (
	ceServiceEc2 = "Amazon Elastic Compute Cloud - Compute"
	ceServiceRds = "Amazon Relational Database Service"
)

type ec2ReservedInstancesAPI interface {
	DescribeReservedInstances(ctx context.Context, params *ec2.DescribeReservedInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeReservedInstancesOutput, error)
}

type rdsReservedInstancesAPI interface {
	DescribeReservedDBInstances(ctx context.Context, params *rds.DescribeReservedDBInstancesInput, optFns ...func(*rds.Options)) (*rds.DescribeReservedDBInstancesOutput, error)
}

type reservationUtilizationAPI interface {
	GetReservationUtilization(ctx context.Context, params *costexplorer.GetReservationUtilizationInput, optFns ...func(*costexplorer.Options)) (*costexplorer.GetReservationUtilizationOutput, error)
}

// reservedInstance is a Reserved Instance in the provider-neutral model, with
// what is needed to find its row in Cost Explorer's utilization report.
type reservedInstance struct {
	commitment providers.Commitment
	// matchKeys are the identifiers Cost Explorer may file the reservation
	// under: its id, lease id or ARN.
	matchKeys []string
	currency  string
}

// reservedInstanceSource lists the active Reserved Instances of one service in
// one region. ceService is the Cost Explorer SERVICE its utilization is under.
type reservedInstanceSource struct {
	ceService string
	list      func(ctx providers.CloudProviderContext, region string) ([]reservedInstance, error)
}

// reservedInstanceSources returns the services whose Reserved Instances are
// reported. Reserved Instances are regional, so each source is asked once per
// region.
func reservedInstanceSources(cfg aws.Config, account providers.Account) []reservedInstanceSource {
	return []reservedInstanceSource{
		{
			ceService: ceServiceEc2,
			list: func(ctx providers.CloudProviderContext, region string) ([]reservedInstance, error) {
				regional := cfg.Copy()
				regional.Region = region
				return listEc2ReservedInstances(ctx, ec2.NewFromConfig(regional), region, account.AccountNumber)
			},
		},
		{
			ceService: ceServiceRds,
			list: func(ctx providers.CloudProviderContext, region string) ([]reservedInstance, error) {
				regional := cfg.Copy()
				regional.Region = region
				return listRdsReservedInstances(ctx, rds.NewFromConfig(regional), region, account.AccountNumber)
			},
		},
	}
}

func listEc2ReservedInstances(ctx providers.CloudProviderContext, client ec2ReservedInstancesAPI, region, accountNumber string) ([]reservedInstance, error) {
	output, err := client.DescribeReservedInstances(ctx.GetContext(), &ec2.DescribeReservedInstancesInput{
		Filters: []ec2types.Filter{{Name: aws.String("state"), Values: []string{string(ec2types.ReservedInstanceStateActive)}}},
	})
	if err != nil {
		return nil, err
	}

	reservations := []reservedInstance{}
	for _, ri := range output.ReservedInstances {
		id := aws.ToString(ri.ReservedInstancesId)
		if id == "" {
			continue
		}
		reservations = append(reservations, reservedInstance{
			commitment: providers.Commitment{
				ID:        id,
				Provider:  constants.ProviderAWS,
				AccountID: accountNumber,
				Type:      providers.CommitmentTypeReservedInstance,
				Resources: []providers.CommitmentResource{{
					Type:         "instance",
					Quantity:     float64(aws.ToInt32(ri.InstanceCount)),
					Unit:         "INSTANCES",
					Service:      ServiceNameEc2,
					Region:       region,
					ResourceType: string(ri.InstanceType),
					Metadata: map[string]any{
						"offering_class":      string(ri.OfferingClass),
						"offering_type":       string(ri.OfferingType),
						"scope":               string(ri.Scope),
						"availability_zone":   aws.ToString(ri.AvailabilityZone),
						"product_description": string(ri.ProductDescription),
					},
				}},
				StartTime: aws.ToTime(ri.Start),
				EndTime:   aws.ToTime(ri.End),
				Status:    string(ri.State),
			},
			matchKeys: []string{id},
			currency:  string(ri.CurrencyCode),
		})
	}
	return reservations, nil
}

func listRdsReservedInstances(ctx providers.CloudProviderContext, client rdsReservedInstancesAPI, region, accountNumber string) ([]reservedInstance, error) {
	reservations := []reservedInstance{}
	var marker *string
	for {
		output, err := client.DescribeReservedDBInstances(ctx.GetContext(), &rds.DescribeReservedDBInstancesInput{Marker: marker})
		if err != nil {
			return nil, err
		}
		for _, ri := range output.ReservedDBInstances {
			if reservation, ok := rdsReservedInstanceToCommitment(ri, region, accountNumber); ok {
				reservations = append(reservations, reservation)
			}
		}
		if aws.ToString(output.Marker) == "" {
			return reservations, nil
		}
		marker = output.Marker
	}
}

// rdsReservedInstanceToCommitment converts an active RDS Reserved Instance. It
// reports false for one that is not active or has no name.
//
// An RDS reservation's id is a name the customer chose, unique only within a
// region, so the commitment id carries the region too. RDS reports a start and
// a duration rather than an end date.
func rdsReservedInstanceToCommitment(ri rdstypes.ReservedDBInstance, region, accountNumber string) (reservedInstance, bool) {
	name := aws.ToString(ri.ReservedDBInstanceId)
	if name == "" || !strings.EqualFold(aws.ToString(ri.State), "active") {
		return reservedInstance{}, false
	}

	var endTime time.Time
	if ri.StartTime != nil && aws.ToInt32(ri.Duration) > 0 {
		endTime = ri.StartTime.Add(time.Duration(aws.ToInt32(ri.Duration)) * time.Second)
	}

	matchKeys := []string{}
	for _, key := range []string{aws.ToString(ri.LeaseId), aws.ToString(ri.ReservedDBInstanceArn)} {
		if key != "" {
			matchKeys = append(matchKeys, key)
		}
	}

	return reservedInstance{
		commitment: providers.Commitment{
			ID:        region + "/" + name,
			Provider:  constants.ProviderAWS,
			AccountID: accountNumber,
			Type:      providers.CommitmentTypeReservedInstance,
			Resources: []providers.CommitmentResource{{
				Type:         "instance",
				Quantity:     float64(aws.ToInt32(ri.DBInstanceCount)),
				Unit:         "INSTANCES",
				Service:      ServiceNameRDS,
				Region:       region,
				ResourceType: aws.ToString(ri.DBInstanceClass),
				Metadata: map[string]any{
					"reserved_db_instance_id": name,
					"offering_type":           aws.ToString(ri.OfferingType),
					"multi_az":                aws.ToBool(ri.MultiAZ),
					"product_description":     aws.ToString(ri.ProductDescription),
				},
			}},
			StartTime: aws.ToTime(ri.StartTime),
			EndTime:   endTime,
			Status:    aws.ToString(ri.State),
		},
		matchKeys: matchKeys,
		currency:  aws.ToString(ri.CurrencyCode),
	}, true
}

// isRegionNotReadable reports whether a regional call failed in a way that
// settles the region as having nothing we can report: it is not enabled, not
// served, not reachable, or the role is not permitted there (organizations
// commonly deny whole regions). Anything else — a throttle, a server error —
// leaves the region unknown and must fail the scan instead, because reporting a
// partial list would archive the recommendations of the regions left out.
func isRegionNotReadable(err error) bool {
	if isRegionDisabled(err) || isRegionEndpointMissing(err) || isRegionUnreachable(err) || isServiceUnavailableInRegion(err) || isAccessDeniedError(err) {
		return true
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "UnauthorizedOperation", "AccessDenied":
			return true
		}
	}
	return false
}

// reservedInstanceCommitments returns the account's active Reserved Instances
// across the given regions with each one's utilization over the lookback window.
func reservedInstanceCommitments(ctx providers.CloudProviderContext, sources []reservedInstanceSource, utilizationAPI reservationUtilizationAPI, account providers.Account, regions []string, now time.Time, lookbackDays int) ([]providers.Commitment, []providers.Utilization, error) {
	commitments := []providers.Commitment{}
	utilizations := []providers.Utilization{}
	for _, source := range sources {
		reservations, err := listReservedInstances(ctx, source, regions)
		if err != nil {
			return nil, nil, err
		}
		if len(reservations) == 0 {
			continue
		}
		for _, reservation := range reservations {
			commitments = append(commitments, reservation.commitment)
		}

		serviceUtilizations, err := reservationUtilizations(ctx, utilizationAPI, source.ceService, account, reservations, now, lookbackDays)
		if err != nil {
			// As for Savings Plans: utilization that cannot be read still leaves the
			// expiry of each reservation known.
			var dataUnavailable *cetypes.DataUnavailableException
			if isAccessDeniedError(err) || errors.As(err, &dataUnavailable) {
				ctx.GetLogger().Info("reserved instance utilization not available, reporting expiry only", "service", source.ceService, "error", err)
				continue
			}
			return nil, nil, fmt.Errorf("get reservation utilization for %s: %w", source.ceService, err)
		}
		if len(serviceUtilizations) == 0 {
			// Either Cost Explorer has no usage for these reservations yet, or it
			// files them under identifiers matchReservation does not recognise;
			// the two look the same from here, so say which reservations are
			// affected.
			ctx.GetLogger().Warn("no utilization matched any reserved instance, reporting expiry only", "service", source.ceService, "reservations", len(reservations))
		}
		utilizations = append(utilizations, serviceUtilizations...)
	}
	return commitments, utilizations, nil
}

// reservedInstanceRegionConcurrency bounds how many regions are asked at once.
// Asking them one after another makes a listing's response time grow with the
// number of regions the account has enabled.
const reservedInstanceRegionConcurrency = 8

// listReservedInstances asks one source in every region, returning the
// reservations in region order.
func listReservedInstances(ctx providers.CloudProviderContext, source reservedInstanceSource, regions []string) ([]reservedInstance, error) {
	byRegion := make([][]reservedInstance, len(regions))
	group := errgroup.Group{}
	group.SetLimit(reservedInstanceRegionConcurrency)
	for i, region := range regions {
		group.Go(func() error {
			regional, err := source.list(ctx, region)
			if err != nil {
				if isRegionNotReadable(err) {
					ctx.GetLogger().Info("reserved instances not readable in region, skipping", "service", source.ceService, "region", region, "error", err)
					return nil
				}
				return fmt.Errorf("describe reserved instances for %s in %s: %w", source.ceService, region, err)
			}
			byRegion[i] = regional
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return nil, err
	}

	reservations := []reservedInstance{}
	for _, regional := range byRegion {
		reservations = append(reservations, regional...)
	}
	return reservations, nil
}

// reservationUtilizations returns the utilization of each of the given
// reservations of one service over the lookback window. Reserved Instances are
// measured in hours; Cost Explorer also reports what the unused hours cost, so
// the waste as money is its own figure, not hours times an assumed rate.
func reservationUtilizations(ctx providers.CloudProviderContext, client reservationUtilizationAPI, ceService string, account providers.Account, reservations []reservedInstance, now time.Time, lookbackDays int) ([]providers.Utilization, error) {
	start, end := costExplorerWindow(now, lookbackDays)

	type hours struct{ purchased, used, unused, unusedCost float64 }
	hoursByReservation := map[string]*hours{}
	var nextToken *string
	for {
		output, err := client.GetReservationUtilization(ctx.GetContext(), &costexplorer.GetReservationUtilizationInput{
			TimePeriod: &cetypes.DateInterval{
				Start: aws.String(start.Format(time.DateOnly)),
				End:   aws.String(end.Format(time.DateOnly)),
			},
			GroupBy: []cetypes.GroupDefinition{{
				Type: cetypes.GroupDefinitionTypeDimension,
				Key:  aws.String("SUBSCRIPTION_ID"),
			}},
			Filter: &cetypes.Expression{And: []cetypes.Expression{
				{Dimensions: &cetypes.DimensionValues{Key: cetypes.DimensionService, Values: []string{ceService}}},
				{Dimensions: &cetypes.DimensionValues{Key: cetypes.DimensionLinkedAccount, Values: []string{account.AccountNumber}}},
			}},
			NextPageToken: nextToken,
		})
		if err != nil {
			return nil, err
		}

		for _, period := range output.UtilizationsByTime {
			for _, group := range period.Groups {
				reservation, ok := matchReservation(reservations, group)
				if !ok || group.Utilization == nil {
					continue
				}
				purchased, purchasedErr := parseFloat64(aws.ToString(group.Utilization.PurchasedHours))
				used, usedErr := parseFloat64(aws.ToString(group.Utilization.TotalActualHours))
				unused, unusedErr := parseFloat64(aws.ToString(group.Utilization.UnusedHours))
				if err := errors.Join(purchasedErr, usedErr, unusedErr); err != nil {
					ctx.GetLogger().Warn("skipping reservation utilization with unreadable hours", "reservation", reservation.commitment.ID, "error", err)
					continue
				}
				// The cost of the unused hours is optional: without it the waste is
				// reported in hours alone.
				unusedCost, _ := parseFloat64(aws.ToString(group.Utilization.RICostForUnusedHours))

				id := reservation.commitment.ID
				if _, seen := hoursByReservation[id]; !seen {
					hoursByReservation[id] = &hours{}
				}
				hoursByReservation[id].purchased += purchased
				hoursByReservation[id].used += used
				hoursByReservation[id].unused += unused
				hoursByReservation[id].unusedCost += unusedCost
			}
		}

		if aws.ToString(output.NextPageToken) == "" {
			break
		}
		nextToken = output.NextPageToken
	}

	utilizations := []providers.Utilization{}
	for _, reservation := range reservations {
		h, ok := hoursByReservation[reservation.commitment.ID]
		if !ok || h.purchased <= 0 {
			continue
		}
		utilization := providers.Utilization{
			Provider:       constants.ProviderAWS,
			CommitmentID:   reservation.commitment.ID,
			CommitmentType: providers.CommitmentTypeReservedInstance,
			UtilizationPct: h.used / h.purchased * 100,
			CommittedUsage: h.purchased,
			UsedUsage:      h.used,
			UnusedUsage:    h.unused,
			UsageUnit:      "HOURS",
			StartTime:      start,
			EndTime:        end,
		}
		if reservation.currency != "" {
			utilization.UnusedCost = h.unusedCost
			utilization.Currency = reservation.currency
		}
		utilizations = append(utilizations, utilization)
	}
	return utilizations, nil
}

// matchReservation finds the reservation a Cost Explorer utilization group is
// for. The group is keyed by a subscription id that the describe APIs do not
// return, and carries the reservation's own identifiers only as attributes
// whose names Cost Explorer does not document, so the group's value and every
// attribute value are compared against the reservation's id, lease id and ARN.
func matchReservation(reservations []reservedInstance, group cetypes.ReservationUtilizationGroup) (reservedInstance, bool) {
	values := []string{aws.ToString(group.Value)}
	for _, value := range group.Attributes {
		values = append(values, value)
	}
	for _, reservation := range reservations {
		for _, key := range reservation.matchKeys {
			for _, value := range values {
				if value == key || strings.HasSuffix(value, "/"+key) {
					return reservation, true
				}
			}
		}
	}
	return reservedInstance{}, false
}
