package aws

import (
	"strings"
	"time"

	"nudgebee/collector/cloud/providers"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
)

/*
CloudWatch Metrics Insights — the SQL dialect GetMetricData runs in place of a
MetricStat selector, e.g.

	SELECT AVG(CPUUtilization) FROM "AWS/EC2" GROUP BY InstanceId ORDER BY AVG() DESC LIMIT 10

This is the only path where the caller writes the query rather than picking a
service, metric and dimensions, so none of the structured selector's resolution
(service registry, resource discovery, dimension preparation) applies — the
query carries all of it.

Two AWS limits shape this file: a GetMetricData call accepts exactly ONE Metrics
Insights query, so there is no chunking as there is on the structured path, and
the query reads at most the last two weeks of data.
*/
func queryAwsMetricsInsights(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws config", "error", err, "accountNumber", account.AccountNumber)
		return providers.QueryMetricsResponse{}, err
	}

	// Metric data is regional, but a hand-written query carries no region — the
	// account's own region is the sensible default (getAwsConfigFromAccount has
	// already applied it), so an absent one is not an error the way it is on the
	// structured path.
	region := filter.Region
	// Global services (CloudFront, Route53, IAM) hold their metrics in us-east-1.
	if region == "global" {
		region = "us-east-1"
	}
	if region != "" {
		cfg.Region = region
	}
	svc := cloudwatch.NewFromConfig(cfg)

	startTime := time.Now().UTC().Add(-time.Hour)
	if filter.StartDate != nil {
		startTime = *filter.StartDate
	}
	endTime := time.Now().UTC()
	if filter.EndDate != nil {
		endTime = *filter.EndDate
	}
	step := 60 * time.Second
	if filter.Step > 0 {
		step = filter.Step
	}

	query := types.MetricDataQuery{
		Id:         aws.String("q1"),
		Expression: aws.String(strings.TrimSpace(filter.Query)),
		Period:     aws.Int32(int32(step.Seconds())),
		ReturnData: aws.Bool(true),
	}

	// Keyed by label so a paginated answer extends the series it belongs to
	// instead of adding a second one under the same name. `order` keeps the
	// caller's series in the order CloudWatch ranked them, which is what an
	// ORDER BY ... LIMIT query is asking for.
	byLabel := map[string]*providers.MetricItem{}
	order := []string{}

	var token *string
	for {
		result, err := svc.GetMetricData(ctx.GetContext(), &cloudwatch.GetMetricDataInput{
			StartTime:         aws.Time(startTime),
			EndTime:           aws.Time(endTime),
			MetricDataQueries: []types.MetricDataQuery{query},
			// Ascending so the points arrive oldest-first, as the charting layer
			// and every other metrics source present them. CloudWatch defaults to
			// descending.
			ScanBy:    types.ScanByTimestampAscending,
			NextToken: token,
		})
		if err != nil {
			ctx.GetLogger().Error("cloudwatch: metrics insights query failed",
				"error", err, "accountNumber", account.AccountNumber, "region", cfg.Region, "query", filter.Query)
			return providers.QueryMetricsResponse{}, err
		}

		for _, r := range result.MetricDataResults {
			// Metrics Insights names each series in Label — the GROUP BY value
			// ("i-0abc…") when the query groups, the metric name when it does not.
			label := ""
			if r.Label != nil {
				label = *r.Label
			}
			item, seen := byLabel[label]
			if !seen {
				item = &providers.MetricItem{
					Name:       label,
					ResourceId: label,
					Region:     cfg.Region,
				}
				byLabel[label] = item
				order = append(order, label)
			}
			item.Values = append(item.Values, r.Values...)
			item.Timestamps = append(item.Timestamps, r.Timestamps...)
		}

		if result.NextToken == nil {
			break
		}
		token = result.NextToken
	}

	items := make([]providers.MetricItem, 0, len(order))
	for _, label := range order {
		items = append(items, *byLabel[label])
	}

	return providers.QueryMetricsResponse{
		Items:     items,
		StartDate: startTime,
		EndDate:   endTime,
		Step:      step,
	}, nil
}
