package aws

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"nudgebee/collector/cloud/common"
	"nudgebee/collector/cloud/config"
	"nudgebee/collector/cloud/providers"
	"nudgebee/collector/cloud/providers/aws/servicemap"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudtrail"
	trailtypes "github.com/aws/aws-sdk-go-v2/service/cloudtrail/types"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	logstypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	"github.com/aws/smithy-go/ptr"
	"github.com/google/shlex"
	"go.uber.org/multierr"
	"golang.org/x/sync/semaphore"
)

const (
	// cloudWatchTimestampLayout is the expected format for @timestamp fields from CloudWatch Logs Insights.
	cloudWatchTimestampLayout = "2006-01-02 15:04:05.000"
)

const (
	queryPollInterval    = 3 * time.Second
	maxQueryPollAttempts = 20 // Results in a 1-minute timeout (20 * 3s)
)

type regionCacheEntry struct {
	regions    []string
	lastUpdate time.Time
}

var (
	regionsCache      map[string]regionCacheEntry
	regionsCacheMutex sync.Mutex
)

const regionsCacheTTL = 6 * time.Hour

var awsServiceMap map[string]awsService
var serviceNameToKey map[string]string

func init() {
	awsServiceMap = map[string]awsService{
		"ec2":                 &amazonEc2{},
		"rds":                 &amazonRds{},
		"s3":                  &amazonS3{},
		"lambda":              &awsLambda{},
		"ecr":                 &amazonEcr{},
		"sns":                 &amazonSns{},
		"ses":                 &awsSes{},
		"cloudtrail":          &awsCloudTrail{},
		"sqs":                 &awsSqs{},
		"ecrpublic":           &amazonEcrPublic{},
		"vpc":                 &amazonVpc{},
		"elb":                 &awsElb{},
		"kms":                 &awsKms{},
		"secretsmanager":      &awsSecretsManager{},
		"cloudwatch":          &amazonCloudwatch{},
		"eks":                 &amazonEks{},
		"codeartifact":        &awsCodeArtifact{},
		"elasticache":         &amazonElasticCache{},
		"securityhub":         &awsSecurityHub{},
		"msk":                 &amazonMsk{},
		"sagemaker":           &amazonSagemaker{},
		"redshift":            &amazonRedshift{},
		"es":                  &amazonES{},
		"efs":                 &amazonEFS{},
		"bedrock":             &amazonBedrock{},
		"queueservice":        &awsQueueService{},
		"cloudformation":      &awsCloudFormation{},
		"xray":                &amazonXray{},
		"backup":              &awsBackup{},
		"cloudshell":          &awsCloudShell{},
		"cloudfront":          &amazonCloudFront{},
		"ecs":                 &amazonEcs{},
		"fargate":             &amazonFargate{},
		"dynamodb":            &amazonDynamoDB{},
		"guardduty":           &awsGuardDuty{},
		"iam":                 &IAMRecommendationsProvider{},
		"elasticbeanstalk":    &awsElasticBeanstalk{},
		"stepfunctions":       &awsStepFunctions{},
		"route53":             &awsRoute53{},
		"directconnect":       &awsDirectConnect{},
		"waf":                 &awsWAF{},
		"wafv2":               &awsWAF{},
		"inspector":           &awsInspector{},
		"inspector2":          &awsInspector{},
		"config":              &awsConfig{},
		"ssm":                 &awsSystemsManager{},
		"systemsmanager":      &awsSystemsManager{},
		"costoptimizationhub": &awsCostOptimizationHub{},
		"costexplorer":        &awsCostExplorer{},
		"computeoptimizer":    &awsComputeOptimizer{},
		"trustedadvisor":      &awsTrustedAdvisor{},
	}
	regionsCache = make(map[string]regionCacheEntry)

	// Wrap all services with permission audit decorator
	for key, svc := range awsServiceMap {
		awsServiceMap[key] = &auditedAwsService{inner: svc, serviceName: key}
	}

	// Initialize service name to key mapping for ApplyRecommendation routing
	serviceNameToKey = map[string]string{
		ServiceNameEc2:              "ec2",
		ServiceNameRDS:              "rds",
		ServiceNameS3:               "s3",
		ServiceNameLambda:           "lambda",
		ServiceNameECR:              "ecr",
		ServiceNameSNS:              "sns",
		ServiceNameSES:              "ses",
		ServiceNameCloudTrail:       "cloudtrail",
		ServiceNameSQS:              "queueservice",
		ServiceNameECRPublic:        "ecrpublic",
		ServiceNameVPC:              "vpc",
		ServiceNameELB:              "elb",
		ServiceNameKMS:              "kms",
		ServiceNameSecretsManager:   "secretsmanager",
		ServiceNameCloudWatch:       "cloudwatch",
		ServiceNameEKS:              "eks",
		ServiceNameCodeArtifact:     "codeartifact",
		ServiceNameElastiCache:      "elasticache",
		ServiceNameSecurityHub:      "securityhub",
		ServiceNameMSK:              "msk",
		ServiceNameSageMaker:        "sagemaker",
		ServiceNameRedshift:         "redshift",
		ServiceNameES:               "es",
		ServiceNameEFS:              "efs",
		ServiceNameBedrock:          "bedrock",
		ServiceNameCloudFormation:   "cloudformation",
		ServiceNameXray:             "xray",
		ServiceNameBackup:           "backup",
		ServiceNameCloudShell:       "cloudshell",
		ServiceNameCloudFront:       "cloudfront",
		ServiceNameECS:              "ecs",
		ServiceNameFargate:          "fargate",
		ServiceNameDynamoDB:         "dynamodb",
		ServiceNameGuardDuty:        "guardduty",
		ServiceNameIAM:              "iam",
		ServiceNameElasticBeanstalk: "elasticbeanstalk",
		ServiceNameStepFunctions:    "stepfunctions",
		ServiceNameRoute53:          "route53",
		ServiceNameDirectConnect:    "directconnect",
		ServiceNameWAF:              "waf",
		ServiceNameInspector:        "inspector",
		ServiceNameConfig:           "config",
	}
}

func GetAwsService(serviceName string) (awsService, bool) {
	serviceName = strings.ToLower(serviceName)
	if serviceName == "" {
		return nil, false
	}
	serviceName = strings.TrimPrefix(serviceName, "aws")
	serviceName = strings.TrimPrefix(serviceName, "amazon")

	service, ok := awsServiceMap[serviceName]
	return service, ok
}

type awsProvider struct {
}

var (
	logGroupCacheMu sync.RWMutex
	logGroupCache   = map[string]string{}
)

// logGroupCacheMaxSize caps unbounded growth in long-running collector processes.
// When the limit is hit the entire cache is dropped; a brief cold-start is
// preferable to an OOM in a multi-tenant deployment.
const logGroupCacheMaxSize = 10_000

var servicesWithoutDirectLogs = map[string]struct{}{
	"dynamodb":      {},
	"rds":           {},
	"s3":            {},
	"api gateway":   {},
	"cloudtrail":    {},
	"queueservice":  {},
	"cloudwatch":    {},
	"sqs":           {},
	"sns":           {},
	"iam":           {},
	"route53":       {},
	"cloudfront":    {},
	"directconnect": {},
	"kms":           {},
}

func (a *awsProvider) QueryLogs(ctx providers.CloudProviderContext, account providers.Account, query providers.QueryLogsRequest) (providers.QueryLogsResponse, error) {
	logger := ctx.GetLogger()
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		logger.Error("failed to create aws session for QueryLogs", "error", err, "accountNumber", account.AccountNumber)
		return providers.QueryLogsResponse{}, fmt.Errorf("failed to create aws session: %w", err)
	}

	if query.Region == "" {
		query.Region = cfg.Region
	}
	cfg.Region = query.Region

	logsSvc := cloudwatchlogs.NewFromConfig(cfg)

	//detect loggroup
	detectionAttempted := false
	if query.LogGroupName == "" {
		if service, found := GetAwsService(query.ServiceName); found {
			detectionAttempted = true
			cacheKey := fmt.Sprintf("%s-%s-%s", account.AccountNumber, query.Region, query.ResourceId)
			logGroupCacheMu.RLock()
			loggroup, ok := logGroupCache[cacheKey]
			logGroupCacheMu.RUnlock()
			if ok {
				query.LogGroupName = loggroup
			} else {
				var err error
				loggroup, err = service.GetLogGroupName(ctx, account, query.Region, query.ResourceId)
				if err != nil {
					logger.Error("failed to get log group name", "error", err, "accountNumber", account.AccountNumber, "service", query.ServiceName, "region", query.Region, "resource", query.ResourceId)
				} else {
					// Only cache on success — caching an empty string on error would
					// permanently suppress retries for transient failures.
					logGroupCacheMu.Lock()
					if len(logGroupCache) >= logGroupCacheMaxSize {
						logGroupCache = map[string]string{}
					}
					logGroupCache[cacheKey] = loggroup
					logGroupCacheMu.Unlock()
				}
				if loggroup != "" {
					query.LogGroupName = loggroup
				}
			}
		}
	}

	if query.LogGroupName == "" {
		normalizedServiceName := strings.TrimPrefix(strings.TrimPrefix(strings.ToLower(query.ServiceName), "aws"), "amazon")
		if _, ok := servicesWithoutDirectLogs[normalizedServiceName]; ok {
			ctx.GetLogger().Warn(
				"logs not directly available in CloudWatch; ensure log exports or CloudTrail are configured",
				"service", query.ServiceName,
				"region", query.Region,
				"accountNumber", account.AccountNumber,
				"resource", query.ResourceId,
			)
			return providers.QueryLogsResponse{}, nil
		} else if detectionAttempted {
			// Detection was attempted via GetLogGroupName but found nothing —
			// the resource simply doesn't have CloudWatch logs configured
			ctx.GetLogger().Info(
				"logs: no log group found after detection attempt",
				"service", query.ServiceName,
				"region", query.Region,
				"accountNumber", account.AccountNumber,
				"resource", query.ResourceId,
			)
			return providers.QueryLogsResponse{}, nil
		} else {
			ctx.GetLogger().Error(
				"logs: unable to detect log group name",
				"service", query.ServiceName,
				"region", query.Region,
				"accountNumber", account.AccountNumber,
				"resource", query.ResourceId,
			)
			return providers.QueryLogsResponse{}, fmt.Errorf("logs: logGroupName is required, unable to detect")
		}
	}

	if query.EndTime == nil {
		query.EndTime = aws.Time(time.Now())
	}

	if query.StartTime == nil {
		query.StartTime = aws.Time(time.Now().Add(-1 * time.Hour))
	}

	if query.Limit == nil || *query.Limit == 0 {
		query.Limit = aws.Int64(1000)
	}

	// When a metric filter pattern is provided, use FilterLogEvents API
	// which natively supports the same pattern syntax as CloudWatch metric filters.
	if query.FilterPattern != "" {
		return a.queryLogsWithFilterPattern(ctx, logsSvc, query)
	}

	if query.QueryString == "" {
		query.QueryString = "fields @timestamp, @message"
	}

	startQueryInput := &cloudwatchlogs.StartQueryInput{
		LogGroupName: ptr.String(query.LogGroupName),
		QueryString:  ptr.String(query.QueryString),
		StartTime:    ptr.Int64(query.StartTime.UTC().UnixMilli()),
		EndTime:      ptr.Int64(query.EndTime.UTC().UnixMilli()),
		Limit:        ptr.Int32(int32(*query.Limit)),
	}

	startQueryOutput, err := logsSvc.StartQuery(context.TODO(), startQueryInput)
	if err != nil {
		if strings.Contains(err.Error(), "ResourceNotFoundException") {
			logger.Warn("CloudWatch Logs query failed because log group does not exist", "logGroupName", query.LogGroupName, "accountNumber", account.AccountNumber, "error", err)
			return providers.QueryLogsResponse{
				Status:  string(logstypes.QueryStatusFailed), // Or a custom status like "LogGroupNotFound"
				Results: []providers.LogMessage{},            // Empty results
			}, nil // Return nil error to allow other actions to proceed, but indicate failure via status
		}
		logger.Error("failed to start CloudWatch Logs query", "error", err, "logGroupName", query.LogGroupName, "accountNumber", account.AccountNumber)
		return providers.QueryLogsResponse{}, fmt.Errorf("failed to start logs query: %w", err)
	}

	queryId := ptr.ToString(startQueryOutput.QueryId)
	logger.Info("CloudWatch Logs query started", "queryId", queryId, "logGroupName", query.LogGroupName)

	getQueryResultsInput := &cloudwatchlogs.GetQueryResultsInput{
		QueryId: ptr.String(queryId),
	}

	var queryResultsOutput *cloudwatchlogs.GetQueryResultsOutput
	for i := 0; i < maxQueryPollAttempts; i++ {
		queryResultsOutput, err = logsSvc.GetQueryResults(context.TODO(), getQueryResultsInput)
		if err != nil {
			logger.Error("failed to get CloudWatch Logs query results", "error", err, "queryId", queryId, "accountNumber", account.AccountNumber)
			return providers.QueryLogsResponse{QueryId: queryId}, fmt.Errorf("failed to get query results for queryId %s: %w", queryId, err)
		}

		status := queryResultsOutput.Status
		logger.Info("Polling CloudWatch Logs query", "queryId", queryId, "status", status, "attempt", i+1)

		switch status {
		case logstypes.QueryStatusComplete:
			logMessages := make([]providers.LogMessage, 0, len(queryResultsOutput.Results))
			for _, sdkResultRow := range queryResultsOutput.Results {
				var currentLogMessage providers.LogMessage
				currentLogMessage.Labels = []providers.LogLabel{}

				for _, sdkField := range sdkResultRow {
					fieldName := ptr.ToString(sdkField.Field)
					fieldValue := ptr.ToString(sdkField.Value)

					switch fieldName {
					case "@message":
						currentLogMessage.Message = fieldValue
					case "@timestamp":
						// CloudWatch Logs Insights @timestamp is in "YYYY-MM-DD HH:MM:SS.mmm" format
						parsedTime, err := time.Parse(cloudWatchTimestampLayout, fieldValue)
						if err != nil {
							logger.Warn("Failed to parse @timestamp field from CloudWatch Logs", "value", fieldValue, "expectedFormat", cloudWatchTimestampLayout, "error", err, "queryId", queryId)
							currentLogMessage.Timestamp = 0 // Default to 0 or handle as appropriate
						} else {
							currentLogMessage.Timestamp = parsedTime.UnixMilli()
						}
					default:
						currentLogMessage.Labels = append(currentLogMessage.Labels, providers.LogLabel{Label: fieldName, Value: fieldValue})
					}
				}
				logMessages = append(logMessages, currentLogMessage)
			}

			stats := providers.LogQueryStatistics{}
			if queryResultsOutput.Statistics != nil {
				stats.BytesScanned = queryResultsOutput.Statistics.BytesScanned
				stats.RecordsMatched = queryResultsOutput.Statistics.RecordsMatched
				stats.RecordsScanned = queryResultsOutput.Statistics.RecordsScanned
			}

			return providers.QueryLogsResponse{
					QueryId:    queryId,
					Results:    logMessages,
					Status:     string(status),
					Statistics: stats,
				},
				nil
		case logstypes.QueryStatusFailed, logstypes.QueryStatusCancelled, logstypes.QueryStatusTimeout:
			logger.Error("CloudWatch Logs query did not complete successfully", "queryId", queryId, "status", status, "accountNumber", account.AccountNumber)
			return providers.QueryLogsResponse{QueryId: queryId, Status: string(status)}, fmt.Errorf("query %s %s", queryId, strings.ToLower(string(status)))
		}
		time.Sleep(queryPollInterval)
	}

	logger.Warn("CloudWatch Logs query polling timed out", "queryId", queryId, "logGroupName", query.LogGroupName, "accountNumber", account.AccountNumber)
	return providers.QueryLogsResponse{QueryId: queryId, Status: string(logstypes.QueryStatusTimeout)}, fmt.Errorf("polling timed out for queryId %s", queryId)
}

// queryLogsWithFilterPattern uses the FilterLogEvents API which natively supports
// CloudWatch metric filter patterns. This returns exactly the log events that
// would increment a metric filter counter — i.e., the logs that triggered the alarm.
func (a *awsProvider) queryLogsWithFilterPattern(ctx providers.CloudProviderContext, logsSvc *cloudwatchlogs.Client, query providers.QueryLogsRequest) (providers.QueryLogsResponse, error) {
	logger := ctx.GetLogger()
	logger.Info("using FilterLogEvents with metric filter pattern",
		"logGroupName", query.LogGroupName, "filterPattern", query.FilterPattern)

	var allMessages []providers.LogMessage
	limit := int32(*query.Limit)

	input := &cloudwatchlogs.FilterLogEventsInput{
		LogGroupName:  ptr.String(query.LogGroupName),
		FilterPattern: ptr.String(query.FilterPattern),
		StartTime:     ptr.Int64(query.StartTime.UTC().UnixMilli()),
		EndTime:       ptr.Int64(query.EndTime.UTC().UnixMilli()),
		Limit:         ptr.Int32(limit),
	}

	paginator := cloudwatchlogs.NewFilterLogEventsPaginator(logsSvc, input)
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(context.TODO())
		if err != nil {
			if strings.Contains(err.Error(), "ResourceNotFoundException") {
				logger.Warn("FilterLogEvents: log group does not exist",
					"logGroupName", query.LogGroupName)
				return providers.QueryLogsResponse{Results: []providers.LogMessage{}}, nil
			}
			return providers.QueryLogsResponse{}, fmt.Errorf("FilterLogEvents failed: %w", err)
		}

		for _, event := range page.Events {
			msg := providers.LogMessage{
				Message:   ptr.ToString(event.Message),
				Timestamp: ptr.ToInt64(event.Timestamp),
				Labels: []providers.LogLabel{
					{Label: "@logStream", Value: ptr.ToString(event.LogStreamName)},
				},
			}
			allMessages = append(allMessages, msg)
		}

		if int32(len(allMessages)) >= limit {
			allMessages = allMessages[:limit]
			break
		}
	}

	return providers.QueryLogsResponse{
		Results: allMessages,
		Status:  string(logstypes.QueryStatusComplete),
	}, nil
}

func (a *awsProvider) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	// A Metrics Insights query names its own namespace, metrics and grouping, so
	// there is no service to resolve — going through the registry would reject it
	// as an unsupported service and return an empty result.
	if strings.TrimSpace(filter.Query) != "" {
		return queryAwsMetricsInsights(ctx, account, filter)
	}
	service, ok := GetAwsService(filter.ServiceName)
	if !ok {
		// Callers that already know the CloudWatch namespace have no NudgeBee
		// service name to dispatch on — the alarm-driven aws_get_metric action
		// passes the namespace taken from the alarm itself (e.g.
		// "AWS/ApplicationELB") as ServiceName, and no such service exists.
		// Querying CloudWatch directly honours that namespace; returning an
		// empty result here silently produced blank evidence cards instead.
		if filter.MetricNamespace != "" {
			return getAwsCloudwatchMetrics(ctx, account, filter)
		}
		return providers.QueryMetricsResponse{
			Items: []providers.MetricItem{},
		}, nil
	}
	return service.QueryMetrices(ctx, account, filter)
}

func (a *awsProvider) ListMetrics(ctx providers.CloudProviderContext, account providers.Account, request providers.ListMetricsRequest) (providers.ListMetricsResponse, error) {
	cacheKey := "aws:" + account.ID + ":" + request.ServiceName
	if cached := providers.GetCachedMetrics(cacheKey); cached != nil {
		return *cached, nil
	}

	namespace := getNamespaceForService(request.ServiceName)
	if namespace != "" {
		cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
		if err == nil {
			cwClient := cloudwatch.NewFromConfig(cfg)
			resp, err := listAwsCloudwatchMetricsDynamic(ctx.GetContext(), cwClient, namespace)
			if err == nil && len(resp.Metrics) > 0 {
				providers.SetCachedMetrics(cacheKey, resp)
				return resp, nil
			}
			ctx.GetLogger().Warn("dynamic ListMetrics failed, falling back to static", "namespace", namespace, "error", err)
		}
	}
	resp, err := listAwsCloudwatchMetrics(request)
	if err == nil {
		providers.SetCachedMetrics(cacheKey, resp)
	}
	return resp, err
}

func (a *awsProvider) QueryDatabasePerformance(ctx providers.CloudProviderContext, account providers.Account, request providers.DatabasePerformanceRequest) (providers.DatabasePerformanceResponse, error) {
	// RDS is the only AWS service that supports Performance Insights
	rdsService := amazonRds{}
	return rdsService.QueryDatabasePerformance(ctx, account, request)
}

// filterResourcesByIds keeps the resources a full GetResources scan returned that
// match a requested id by Id, Name, Arn or an Arn substring. It is a no-op when no
// ids were requested.
func filterResourcesByIds(resources []providers.Resource, resourceIds []string) []providers.Resource {
	if len(resourceIds) == 0 {
		return resources
	}
	filteredResources := []providers.Resource{}
	for _, resource := range resources {
		for _, resourceId := range resourceIds {
			if resource.Id == resourceId || resource.Name == resourceId || resource.Arn == resourceId || strings.Contains(resource.Arn, resourceId) {
				filteredResources = append(filteredResources, resource)
				break
			}
		}
	}
	return filteredResources
}

func (a *awsProvider) ListResources(ctx providers.CloudProviderContext, account providers.Account, query providers.ListResourceRequest) (providers.ListResourcesResponse, error) {
	if query.ServiceName == "" {
		return providers.ListResourcesResponse{
			Items: []providers.Resource{},
		}, errors.New("aws: service_name is required")
	}

	resources := []providers.Resource{}
	regions := query.Regions
	if len(regions) == 0 {
		var err error
		regions, err = a.getRegions(ctx, account)
		if err != nil {
			return providers.ListResourcesResponse{
				Items: resources,
			}, err
		}
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	errChan := make(chan error, len(regions))
	sem := semaphore.NewWeighted(5) // limit concurrent region fetches

	// Track per-region outcomes so we can distinguish "this account genuinely has
	// no resources" from "every region we tried was network-unreachable". The
	// latter must NOT surface as an empty-but-successful result: StoreResources
	// archives (is_active=false, status='Deleted') all of a service's resources
	// when it gets zero items for an explicit region set, so a collector-wide
	// egress failure would mass-archive live resources. See the all-unreachable
	// guard after wg.Wait(), which mirrors the zero-regions safeguard in
	// account.StoreResources.
	var successCount, unreachableCount, throttledCount, disabledCount int
	// Regions requested but not observed this run (unreachable, throttled, or
	// disabled for the account). Reported to the caller via SkippedRegions so
	// archival never treats an unobserved region's absence as deletion.
	var skippedRegions []string

	service, ok := GetAwsService(query.ServiceName)
	if !ok {
		return providers.ListResourcesResponse{
			Items: resources,
		}, nil
	}

	// When ResourceIds are specified, try targeted fetch first (avoids full service scan).
	// Falls back to full GetResources + client-side filter if the service doesn't support
	// it or the targeted call fails. The filter is applied wherever the full scan ran,
	// per region: a failed targeted call must not hand back the whole region unfiltered.
	useTargetedFetch := len(query.ResourceIds) > 0

	// Global services (e.g., S3, IAM, CloudFront) expose a single account-wide listing
	// API. Iterating per-region would either duplicate the same call or — worse, in the
	// case of S3 — silently drop resources whose actual region isn't in the iteration
	// set. Call once and let the implementation set Resource.Region per item.
	if service.IsGlobal() {
		ctx.GetLogger().Info("fetching global resources", "service", query.ServiceName)
		var serviceResources []providers.Resource
		var err error
		targeted := useTargetedFetch
		if targeted {
			serviceResources, err = service.GetResourcesByIds(ctx, account, "", query.ResourceIds)
			if errors.Is(err, errors.ErrUnsupported) {
				err = nil
				targeted = false
			}
		}
		if !targeted || err != nil {
			serviceResources, err = service.GetResources(ctx, account, "")
			if err == nil {
				serviceResources = filterResourcesByIds(serviceResources, query.ResourceIds)
			}
		}
		if err != nil {
			ctx.GetLogger().Error("failed to fetch global resources", "error", err, "service", query.ServiceName)
			return providers.ListResourcesResponse{Items: serviceResources}, err
		}

		// Honor an explicit query.Regions filter even for global services. Callers
		// that pass regions (e.g. /v1/cloud/store_resources with a specific region)
		// want only those regions back. The cron path passes empty Regions for
		// global services (see account.StoreResources) so the filter is a no-op
		// there — preserving the bug-fix that lets new-region resources land.
		if len(query.Regions) > 0 {
			regionSet := make(map[string]struct{}, len(query.Regions))
			for _, r := range query.Regions {
				regionSet[r] = struct{}{}
			}
			filtered := make([]providers.Resource, 0, len(serviceResources))
			for _, res := range serviceResources {
				if _, ok := regionSet[res.Region]; ok {
					filtered = append(filtered, res)
				}
			}
			resources = filtered
		} else {
			resources = serviceResources
		}
		// Fall through to ResourceIds/Labels post-filtering below.
		regions = nil
	}

	for _, regionName := range regions {
		if regionName == "global" {
			continue
		}
		if err := sem.Acquire(ctx.GetContext(), 1); err != nil {
			errChan <- fmt.Errorf("failed to acquire semaphore for %s: %w", regionName, err)
			continue
		}
		wg.Add(1)
		go func(regionName string) {
			defer wg.Done()
			defer sem.Release(1)

			// A region this account could not reach for the services listed just
			// before is set aside instead of timing out again. It counts as
			// unreachable, so it is reported in SkippedRegions and never archived.
			if resourceListBackoff.shouldSkip(account.AccountNumber, regionName) {
				ctx.GetLogger().Debug("skipping resource listing in recently unreachable region", "service", query.ServiceName, "region", regionName)
				mu.Lock()
				unreachableCount++
				skippedRegions = append(skippedRegions, regionName)
				mu.Unlock()
				return
			}

			var serviceResources []providers.Resource
			var err error

			// Per region: regions run concurrently, so this must not write the shared flag.
			targeted := useTargetedFetch
			if targeted {
				serviceResources, err = service.GetResourcesByIds(ctx, account, regionName, query.ResourceIds)
				if errors.Is(err, errors.ErrUnsupported) {
					// Service doesn't support targeted fetch, fall back to full scan
					err = nil
					targeted = false
				}
			}

			if !targeted || err != nil {
				ctx.GetLogger().Info("fetching resources", "service", query.ServiceName, "region", regionName)
				serviceResources, err = service.GetResources(ctx, account, regionName)
				if err == nil {
					serviceResources = filterResourcesByIds(serviceResources, query.ResourceIds)
				}
			}

			if err != nil {
				if kind := classifyRegionSkip(err); kind != regionSkipNone {
					logRegionSkip(ctx.GetLogger(), kind, err, regionName, "service", query.ServiceName)
					mu.Lock()
					switch kind {
					case regionSkipUnreachable:
						resourceListBackoff.recordUnreachable(account.AccountNumber, regionName)
						unreachableCount++
						skippedRegions = append(skippedRegions, regionName)
					case regionSkipDisabled:
						disabledCount++
						skippedRegions = append(skippedRegions, regionName)
					case regionSkipThrottled:
						throttledCount++
						skippedRegions = append(skippedRegions, regionName)
					}
					mu.Unlock()
					return
				}
				ctx.GetLogger().Error("failed to fetch resources", "error", err, "service", query.ServiceName, "region", regionName)
				errChan <- fmt.Errorf("failed to fetch %s resources in %s: %w", query.ServiceName, regionName, err)
				return
			}
			resourceListBackoff.recordSuccess(account.AccountNumber, regionName)
			mu.Lock()
			resources = append(resources, serviceResources...)
			successCount++
			mu.Unlock()
		}(regionName)
	}

	wg.Wait()
	close(errChan)

	var allErrors error
	for err := range errChan {
		allErrors = multierr.Append(allErrors, err)
	}

	// Fail-open guard: if not one region succeeded and at least one was skipped as
	// unreachable or throttled, the empty resource list is an artifact of a
	// network/egress failure or rate limiting — not a true "account has no
	// resources". Returning nil here would let StoreResources archive every
	// existing row for this service. Surface an error so the caller skips archival,
	// exactly as it does when zero regions are queried.
	if successCount == 0 && (unreachableCount > 0 || throttledCount > 0 || disabledCount > 0) {
		allErrors = multierr.Append(allErrors, fmt.Errorf("all attempted region(s) failed for service %s (%d unreachable, %d throttled, %d disabled); refusing to report an empty result", query.ServiceName, unreachableCount, throttledCount, disabledCount))
	}

	if len(query.Labels) > 0 {
		filteredByLabel := []providers.Resource{}
		for _, resource := range resources {
			match := true
			for labelKey, labelValue := range query.Labels {
				tagValues, ok := resource.Tags[labelKey]
				if !ok {
					match = false
					break
				}

				foundValue := false
				for _, v := range tagValues {
					if v == labelValue {
						foundValue = true
						break
					}
				}

				if !foundValue {
					match = false
					break
				}
			}
			if match {
				filteredByLabel = append(filteredByLabel, resource)
			}
		}
		resources = filteredByLabel
	}

	return providers.ListResourcesResponse{
		Items:          resources,
		SkippedRegions: skippedRegions,
	}, allErrors
}

func (a *awsProvider) getRegions(ctx providers.CloudProviderContext, account providers.Account) ([]string, error) {
	// An operator-configured allowlist wins over discovery, and is resolved
	// before the cache read on purpose: the cache is keyed on account number
	// with a 6h TTL, so resolving after it would leave an edited list inert for
	// up to six hours. Reading account.Data is free — no API call, no lock
	// contention worth caching around.
	//
	// This sits in getRegions rather than at the StoreResources call site so
	// that every caller is covered — resource discovery, CloudWatch alarm event
	// discovery, and ListEventRules all reach regions through here, and the
	// latter two never pass an explicit region set.
	if regions := configuredRegions(account); len(regions) > 0 {
		return regions, nil
	}

	regionsCacheMutex.Lock()
	defer regionsCacheMutex.Unlock()

	if cacheEntry, ok := regionsCache[account.AccountNumber]; ok && time.Since(cacheEntry.lastUpdate) < regionsCacheTTL {
		regionsCopy := make([]string, len(cacheEntry.regions))
		copy(regionsCopy, cacheEntry.regions)
		return regionsCopy, nil
	}

	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws session for getRegions", "error", err, "accountNumber", account.AccountNumber)
		return nil, err
	}
	ec2Svc := ec2.NewFromConfig(cfg)
	regionsList, err := ec2Svc.DescribeRegions(context.TODO(), &ec2.DescribeRegionsInput{})
	if err != nil {
		ctx.GetLogger().Error("failed to fetch regions for getRegions", "error", err, "accountNumber", account.AccountNumber)
		return nil, err
	}

	fetchedRegions := make([]string, 0, len(regionsList.Regions))
	for _, region := range regionsList.Regions {
		fetchedRegions = append(fetchedRegions, *region.RegionName)
	}

	regionsCache[account.AccountNumber] = regionCacheEntry{
		regions:    fetchedRegions,
		lastUpdate: time.Now(),
	}

	regionsCopy := make([]string, len(fetchedRegions))
	copy(regionsCopy, fetchedRegions)
	return regionsCopy, nil
}

// GetECSServiceDetails implements the awsProviderAPI method to fetch specific ECS service details.
// clusterIdentifier can be cluster name or ARN. serviceIdentifier can be service name or ARN.
func (a *awsProvider) GetECSServiceDetails(ctx providers.CloudProviderContext, account providers.Account, region, clusterIdentifier, serviceIdentifier string) (providers.Resource, error) {
	logger := ctx.GetLogger()
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		logger.Error("failed to create aws session for GetECSServiceDetails", "error", err, "accountNumber", account.AccountNumber, "region", region)
		return providers.Resource{}, fmt.Errorf("failed to create aws session: %w", err)
	}

	if region == "" {
		// Default to session region if not specified
		if cfg.Region != "" {
			region = cfg.Region
		} else {
			return providers.Resource{}, fmt.Errorf("region not specified and cannot be inferred for GetECSServiceDetails")
		}
	}
	cfg.Region = region

	svc := ecs.NewFromConfig(cfg)

	input := &ecs.DescribeServicesInput{
		Cluster:  ptr.String(clusterIdentifier),
		Services: []string{serviceIdentifier},
		Include:  []ecstypes.ServiceField{ecstypes.ServiceFieldTags},
	}

	output, err := svc.DescribeServices(ctx.GetContext(), input)
	if err != nil {
		return providers.Resource{}, fmt.Errorf("failed to describe ECS service '%s' in cluster '%s' (region %s): %w", serviceIdentifier, clusterIdentifier, region, err)
	}

	if len(output.Services) == 0 {
		return providers.Resource{}, fmt.Errorf("ECS service '%s' not found in cluster '%s' (region %s)", serviceIdentifier, clusterIdentifier, region)
	}

	ecsService := output.Services[0]

	jsonArr, err := common.MarshalJson(ecsService)
	if err != nil {
		slog.Error("unable to serialize json", "error", err)
	}

	ecsServiceMap := map[string]any{}
	err = common.UnmarshalJson(jsonArr, &ecsServiceMap)
	if err != nil {
		slog.Error("unable to deserialize json", "error", err)
	}

	// Populate providers.Resource with key fields and add full details to Meta
	resource := providers.Resource{
		Id:          ptr.ToString(ecsService.ServiceArn), // Use ARN as primary ID
		Arn:         ptr.ToString(ecsService.ServiceArn),
		Name:        ptr.ToString(ecsService.ServiceName),
		Type:        "service",
		ServiceName: ServiceNameECS,
		Region:      region,
		Status:      ecsStatusToNbStatus(ecsService.Status),
		Meta:        ecsServiceMap,
	}
	if ecsService.CreatedAt != nil {
		resource.CreatedAt = *ecsService.CreatedAt
	}

	resource.Tags = make(map[string][]string)
	for _, tag := range ecsService.Tags {
		if tag.Key != nil && tag.Value != nil {
			resource.Tags[*tag.Key] = append(resource.Tags[*tag.Key], *tag.Value)
		}
	}

	return resource, nil
}

// GetECSTaskDefinitionDetails implements the awsProviderAPI method to fetch specific ECS task definition details.
func (a *awsProvider) GetECSTaskDefinitionDetails(ctx providers.CloudProviderContext, account providers.Account, region, taskDefinitionIdentifier string) (providers.Resource, error) {
	logger := ctx.GetLogger()
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		logger.Error("failed to create aws session for GetECSTaskDefinitionDetails", "error", err, "accountNumber", account.AccountNumber, "region", region)
		return providers.Resource{}, fmt.Errorf("failed to create aws session: %w", err)
	}

	if region == "" {
		if cfg.Region != "" {
			region = cfg.Region
		} else {
			return providers.Resource{}, fmt.Errorf("region not specified and cannot be inferred for GetECSTaskDefinitionDetails")
		}
	}
	cfg.Region = region

	svc := ecs.NewFromConfig(cfg)

	input := &ecs.DescribeTaskDefinitionInput{
		TaskDefinition: ptr.String(taskDefinitionIdentifier),
		Include:        []ecstypes.TaskDefinitionField{ecstypes.TaskDefinitionFieldTags},
	}

	output, err := svc.DescribeTaskDefinition(ctx.GetContext(), input)
	if err != nil {
		return providers.Resource{}, fmt.Errorf("failed to describe ECS task definition '%s' (region %s): %w", taskDefinitionIdentifier, region, err)
	}

	if output.TaskDefinition == nil {
		return providers.Resource{}, fmt.Errorf("ECS task definition '%s' not found (region %s)", taskDefinitionIdentifier, region)
	}

	taskDef := output.TaskDefinition

	jsonArr, err := common.MarshalJson(taskDef)
	if err != nil {
		slog.Error("unable to marhal json", "error", err)
	}
	taskDefMap := map[string]any{}
	err = common.UnmarshalJson(jsonArr, &taskDefMap)
	if err != nil {
		slog.Error("unable to deserialize json", "error", err)
	}

	// Populate providers.Resource with)
	resource := providers.Resource{
		Id:          ptr.ToString(taskDef.TaskDefinitionArn),
		Arn:         ptr.ToString(taskDef.TaskDefinitionArn),
		Name:        ptr.ToString(taskDef.Family),
		Type:        "task-definition",
		ServiceName: ServiceNameECS,
		Region:      region,
		Status:      ecsTaskStatusToNbStatus(ptr.String(string(taskDef.Status))), // Assuming ecsTaskStatusToNbStatus can handle task def status
		Meta:        taskDefMap,
	}

	resource.Tags = make(map[string][]string)
	for _, tag := range output.Tags { // Tags are at the top level of DescribeTaskDefinitionOutput
		if tag.Key != nil && tag.Value != nil {
			resource.Tags[*tag.Key] = append(resource.Tags[*tag.Key], *tag.Value)
		}
	}
	return resource, nil
}

// LookupCloudTrailEvents implements the awsProviderAPI method to fetch CloudTrail events.
// This wraps the cloudtrail.LookupEvents API call.
func (a *awsProvider) LookupCloudTrailEvents(ctx providers.CloudProviderContext, account providers.Account, region string, input *cloudtrail.LookupEventsInput) ([]trailtypes.Event, error) {
	logger := ctx.GetLogger()
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		logger.Error("failed to create aws session for LookupCloudTrailEvents", "error", err, "accountNumber", account.AccountNumber, "region", region)
		return nil, fmt.Errorf("failed to create aws session: %w", err)
	}

	// CloudTrail LookupEvents is a regional API, but some events are global.
	// The API call must be made in a region where CloudTrail is enabled and logging.
	// It's common to query the region where the event occurred, or us-east-1 for global events.
	// The input region parameter should guide this.
	// For simplicity, we'll use the provided region.
	if region == "" {
		// Fallback to session region or us-east-1 if region is not specified
		if cfg.Region != "" {
			region = cfg.Region
		} else {
			region = "us-east-1" // Default CloudTrail region
		}
		logger.Info("Using default region for CloudTrail lookup", "region", region)
	}
	cfg.Region = region

	svc := cloudtrail.NewFromConfig(cfg)

	// Note: This implementation does NOT handle pagination (NextToken).
	// For action use cases, MaxResults is typically sufficient.
	// If full historical lookup is needed, pagination would be required here.
	output, err := svc.LookupEvents(ctx.GetContext(), input)
	return output.Events, err
}

func (a *awsProvider) DescribeECSTasks(ctx providers.CloudProviderContext, account providers.Account, region, clusterIdentifier string, taskArns []string) ([]ecstypes.Task, error) {
	logger := ctx.GetLogger()
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		logger.Error("failed to create aws session for DescribeECSTasks", "error", err, "accountNumber", account.AccountNumber, "region", region)
		return nil, fmt.Errorf("failed to create aws session: %w", err)
	}

	if region == "" {
		if cfg.Region != "" {
			region = cfg.Region
		} else {
			return nil, fmt.Errorf("region not specified and cannot be inferred for DescribeECSTasks")
		}
	}
	cfg.Region = region

	svc := ecs.NewFromConfig(cfg)

	input := &ecs.DescribeTasksInput{
		Cluster: ptr.String(clusterIdentifier),
		Tasks:   taskArns,
	}

	output, err := svc.DescribeTasks(ctx.GetContext(), input)
	if err != nil {
		return nil, fmt.Errorf("failed to describe ECS tasks in cluster '%s' (region %s): %w", clusterIdentifier, region, err)
	}

	return output.Tasks, nil
}

func (a *awsProvider) GetUsageReport(ctx providers.CloudProviderContext, account providers.Account, month time.Month, year int) (providers.GetUsageReportResponse, error) {
	return getAwsUsageReport(ctx, account, month, year)
}

func (a *awsProvider) StreamUsageReport(ctx providers.CloudProviderContext, account providers.Account, month time.Month, year int, emit func([]providers.UsageReportItem) error) error {
	return streamAwsUsageReport(ctx, account, month, year, emit)
}

// GetAvailableUsageReportPeriods implements providers.UsageReportPeriodDiscoverer
// by enumerating the CUR billing-period folders present in the account's S3 bucket.
func (a *awsProvider) GetAvailableUsageReportPeriods(ctx providers.CloudProviderContext, account providers.Account, maxMonths int) ([]providers.UsageReportPeriod, error) {
	return discoverAvailableBillingPeriods(ctx, account, maxMonths)
}

func (a *awsProvider) ListRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) (resp providers.ListRecommendationsResponse, err error) {
	service, ok := GetAwsService(filter.ServiceName)
	if !ok {
		return providers.ListRecommendationsResponse{
			Items: []providers.Recommendation{},
		}, nil
	}

	defer func() {
		if r := recover(); r != nil {
			ctx.GetLogger().Error("panic in GetRecommendations", "service", filter.ServiceName, "accountNumber", account.AccountNumber, "panic", r)
			resp = providers.ListRecommendationsResponse{}
			err = fmt.Errorf("panic in GetRecommendations for service %s: %v", filter.ServiceName, r)
		}
	}()

	recommendations, err := service.GetRecommendations(ctx, account, filter, existingResources)
	if err != nil {
		ctx.GetLogger().Error("failed to get recommendations", "error", err, "service", filter.ServiceName)
		return providers.ListRecommendationsResponse{}, err
	}
	return providers.ListRecommendationsResponse{Items: recommendations}, nil
}

func (a *awsProvider) ListSupportedRecommendations(ctx providers.CloudProviderContext) []providers.ListSupportedRecommendationsResponse {
	return []providers.ListSupportedRecommendationsResponse{}
}

func (a *awsProvider) ListEvents(ctx providers.CloudProviderContext, account providers.Account, query providers.ListEventRequest) (providers.ListEventResponse, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws session", "error", err, "accountNumber", account.AccountNumber)
		return providers.ListEventResponse{}, err
	}
	eventList := []providers.Event{}
	eventSummary := []providers.EventSummary{}
	eventSummaryMap := map[string]map[string]map[string]int{}

	if config.Config.CloudCollectorServerEventCloudTrailEnabled {
		svc := cloudtrail.NewFromConfig(cfg)
		var token *string
		maxResults := 1000

		eventsToExclude := managementEventsToExclude
		if query.ExcludeEvents != nil {
			eventsToExclude = query.ExcludeEvents
		}

		endDate := time.Now()
		if query.EndDate != nil {
			endDate = *query.EndDate
		}

		startDate := endDate.Add(-24 * time.Hour)
		if query.StartDate != nil {
			startDate = *query.StartDate
		}

		for {
			input := &cloudtrail.LookupEventsInput{
				StartTime: ptr.Time(startDate),
				EndTime:   ptr.Time(endDate),
				LookupAttributes: []trailtypes.LookupAttribute{
					{
						AttributeKey:   trailtypes.LookupAttributeKeyReadOnly,
						AttributeValue: ptr.String("false"),
					},
				},
				NextToken: token,
			}
			events, err := svc.LookupEvents(context.TODO(), input)
			if err != nil {
				ctx.GetLogger().Error("failed to fetch cloudtrail events", "error", err, "accountNumber", account.AccountNumber)
				return providers.ListEventResponse{}, err
			}
			token = events.NextToken

			for _, event := range events.Events {
				if slices.Contains(eventsToExclude, *event.EventName) {
					continue
				}

				username := ""
				if event.Username != nil {
					username = *event.Username
				}
				eventResourceDetail := getServiceDetailsFromCloudTrailEvent(ctx, event)
				resourceDisplay := eventResourceDetail.ResourceId
				if resourceDisplay == "" {
					resourceDisplay = eventResourceDetail.ServiceName
				}
				eventList = append(eventList, providers.Event{
					Title:               fmt.Sprintf("%s On %s By %s", *event.EventName, resourceDisplay, username),
					EventName:           *event.EventName,
					Date:                *event.EventTime,
					Username:            username,
					EventSource:         "AWS_CloudTrail",
					EventId:             *event.EventId,
					EventStatus:         providers.EventStatusClosed,
					EventSeverity:       providers.EventSeverityInfo,
					ResourceRegion:      eventResourceDetail.Region,
					ResourceType:        eventResourceDetail.ResourceType,
					ResourceId:          eventResourceDetail.ResourceId,
					ResourceServiceName: eventResourceDetail.ServiceName,
					Raw:                 eventResourceDetail.Raw,
					Labels: map[string]string{
						"aws_event_instance": eventResourceDetail.ResourceId,
						"aws_region":         eventResourceDetail.Region,
						"aws_service_name":   eventResourceDetail.ServiceName,
						"aws_account":        account.AccountNumber,
						"aws_event_name":     eventResourceDetail.EventName,
						"aws_event_user":     username,
					},
				})

				if serviceResourceRefereshEvents[eventResourceDetail.ServiceName] != nil && slices.Contains(serviceResourceRefereshEvents[eventResourceDetail.ServiceName], eventResourceDetail.EventName) {
					if _, ok := eventSummaryMap[eventResourceDetail.ServiceName]; !ok {
						eventSummaryMap[eventResourceDetail.ServiceName] = map[string]map[string]int{}
					}
					if _, ok := eventSummaryMap[eventResourceDetail.ServiceName][eventResourceDetail.Region]; !ok {
						eventSummaryMap[eventResourceDetail.ServiceName][eventResourceDetail.Region] = map[string]int{}
					}
					// shortcut based on observed pattern, though may be wrong
					if strings.HasPrefix(*event.EventName, "Create") {
						eventSummaryMap[eventResourceDetail.ServiceName][eventResourceDetail.Region]["Create"] = eventSummaryMap[eventResourceDetail.ServiceName][eventResourceDetail.Region]["Create"] + 1
					} else if strings.HasPrefix(*event.EventName, "Delete") {
						eventSummaryMap[eventResourceDetail.ServiceName][eventResourceDetail.Region]["Delete"] = eventSummaryMap[eventResourceDetail.ServiceName][eventResourceDetail.Region]["Delete"] + 1
					} else {
						eventSummaryMap[eventResourceDetail.ServiceName][eventResourceDetail.Region]["Update"] = eventSummaryMap[eventResourceDetail.ServiceName][eventResourceDetail.Region]["Update"] + 1
					}
				}
			}
			// do not fetch more than maxResults
			if token == nil || len(events.Events) == 0 || len(eventList) >= maxResults {
				break
			}
		}

		if len(eventList) > maxResults {
			eventList = eventList[:maxResults]
		}

		for serviceName, regionMap := range eventSummaryMap {
			for region, summaryMap := range regionMap {
				eventSummary = append(eventSummary, providers.EventSummary{
					ServiceName:      serviceName,
					Region:           region,
					ResourcesCreated: summaryMap["Create"],
					ResourceDeleted:  summaryMap["Delete"],
					ResourceUpdated:  summaryMap["Update"],
				})
			}
		}

	}

	// check for alarm events across all active regions
	var ongoing *providers.OngoingSnapshot
	if config.Config.CloudCollectorServerEventCloudWatchAlarmEnabled {
		regions, err := a.getRegions(ctx, account)
		if err != nil {
			ctx.GetLogger().Error("failed to fetch regions for cloudwatch alarms", "error", err, "accountNumber", account.AccountNumber)
		} else {
			// A region joins the snapshot only when its whole listing succeeded:
			// the store ends stored firings that the snapshot no longer shows, and a
			// region it could not read must not look like a region with no alarms.
			ongoing = newAlarmSnapshot(time.Now().UTC())
			historySince := clearedFiringWindowStart(query.StartDate, time.Now().UTC())
			var alarmsMu sync.Mutex
			var alarmsWg sync.WaitGroup
			alarmsSem := semaphore.NewWeighted(5)

			for _, region := range regions {
				alarmsWg.Add(1)
				go func(region string) {
					defer alarmsWg.Done()
					if err := alarmsSem.Acquire(ctx.GetContext(), 1); err != nil {
						return
					}
					defer alarmsSem.Release(1)

					// A region this account could not reach on recent polls is set
					// aside for a while instead of timing out again every cycle.
					if alarmPollBackoff.shouldSkip(account.AccountNumber, region) {
						ctx.GetLogger().Debug("skipping cloudwatch alarm poll for recently unreachable region", "accountNumber", account.AccountNumber, "region", region)
						return
					}
					alarms, err := getAwsCloudwatchAlarms(ctx, account, AlarmsFilter{
						Status:       AlarmStatusAlarm,
						Region:       region,
						HistorySince: historySince,
					})
					if err != nil {
						// A skipped region stays out of the snapshot below, so the
						// firings stored for it are kept rather than ended.
						if kind := classifyRegionSkip(err); kind != regionSkipNone {
							if kind == regionSkipUnreachable {
								alarmPollBackoff.recordUnreachable(account.AccountNumber, region)
							}
							logRegionSkip(ctx.GetLogger(), kind, err, region, "service", "cloudwatch alarms", "accountNumber", account.AccountNumber)
							return
						}
						ctx.GetLogger().Error("failed to fetch cloudwatch alarms", "error", err, "accountNumber", account.AccountNumber, "region", region)
						return
					}
					alarmPollBackoff.recordSuccess(account.AccountNumber, region)
					alarmsMu.Lock()
					eventList = append(eventList, alarms.Items...)
					mergeAlarmRegion(ongoing, alarms.Ongoing)
					alarmsMu.Unlock()
				}(region)
			}
			alarmsWg.Wait()
		}
	}

	return providers.ListEventResponse{
		Items:   eventList,
		Summary: eventSummary,
		Ongoing: ongoing,
	}, nil
}

func (a *awsProvider) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	// Map service name to service key in awsServiceMap
	serviceKey := getServiceKeyFromServiceName(recommendation.ResourceServiceName)
	if serviceKey == "" {
		ctx.GetLogger().Error("Unknown service name for recommendation",
			"serviceName", recommendation.ResourceServiceName,
			"ruleName", recommendation.RuleName)
		return fmt.Errorf("unknown service name: %s", recommendation.ResourceServiceName)
	}

	// Get the service provider
	service, ok := awsServiceMap[serviceKey]
	if !ok {
		ctx.GetLogger().Error("Service provider not found",
			"serviceKey", serviceKey,
			"serviceName", recommendation.ResourceServiceName)
		return fmt.Errorf("service provider not found for: %s", serviceKey)
	}

	// Call the service's ApplyRecommendation method
	return service.ApplyRecommendation(ctx, account, recommendation)
}

// getServiceKeyFromServiceName maps service names (e.g., "AWSELB") to service keys (e.g., "elb")
// The mapping is initialized once at package load time in init()
func getServiceKeyFromServiceName(serviceName string) string {
	return serviceNameToKey[serviceName]
}

func (a *awsProvider) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	service, ok := GetAwsService(command.ServiceName)
	if !ok {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("AWS service '%s' not found", command.ServiceName),
		}, fmt.Errorf("service not found: %s", command.ServiceName)
	}

	return service.ApplyCommand(ctx, account, command)
}

var awsBlockedCommands = []string{
	"configure",
	"sso",
	"sts",
}

var awsV2BlockedStsOperations = map[string]bool{
	"assume-role":                   true,
	"assume-role-with-saml":         true,
	"assume-role-with-web-identity": true,
	"get-federation-token":          true,
	"get-session-token":             true,
}

var awsGlobalOptionsWithValue = map[string]bool{
	"--ca-bundle":           true,
	"--cli-binary-format":   true,
	"--cli-connect-timeout": true,
	"--cli-read-timeout":    true,
	"--color":               true,
	"--endpoint-url":        true,
	"--output":              true,
	"--profile":             true,
	"--query":               true,
	"--region":              true,
}

func awsCommandPositionals(args []string) ([]string, error) {
	positionals := make([]string, 0, 2)
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "-") {
			positionals = append(positionals, arg)
			if len(positionals) == 2 {
				return positionals, nil
			}
			continue
		}
		if strings.Contains(arg, "=") {
			continue
		}
		if awsGlobalOptionsWithValue[arg] {
			if i+1 >= len(args) {
				return nil, fmt.Errorf("AWS CLI option %s requires a value", arg)
			}
			i++
		}
	}
	return positionals, nil
}

func validateAwsV2Command(command string) error {
	args, err := shlex.Split(command)
	if err != nil {
		return fmt.Errorf("failed to parse AWS CLI command: %w", err)
	}
	if len(args) < 2 || args[0] != "aws" {
		return fmt.Errorf("AWS CLI command must start with aws")
	}
	if err := validateAwsV2Arguments(args[1:]); err != nil {
		return err
	}
	positionals, err := awsCommandPositionals(args)
	if err != nil {
		return err
	}
	if len(positionals) == 0 {
		return fmt.Errorf("AWS CLI command must name a service")
	}
	service := positionals[0]
	if service == "configure" || service == "sso" {
		return fmt.Errorf("command %q is not allowed", service)
	}
	if service == "sts" && len(positionals) > 1 && awsV2BlockedStsOperations[positionals[1]] {
		return fmt.Errorf("command %q is not allowed", "sts "+positionals[1])
	}
	if len(positionals) > 1 && awsV2BlockedOperations[service][positionals[1]] {
		return fmt.Errorf("AWS workspace CLI operation %s %s is not allowed", service, positionals[1])
	}
	return nil
}

func validateAwsV2Arguments(args []string) error {
	for _, arg := range args {
		lower := strings.ToLower(arg)
		switch {
		case matchesAwsV2BlockedOption(lower, "--profile"):
			return fmt.Errorf("AWS profiles must be resolved by the workspace proxy")
		case matchesAwsV2BlockedOption(lower, "--endpoint-url"):
			return fmt.Errorf("custom AWS endpoints are not allowed")
		case matchesAwsV2BlockedOption(lower, "--ca-bundle") || matchesAwsV2BlockedOption(lower, "--no-verify-ssl"):
			return fmt.Errorf("custom AWS TLS settings are not allowed")
		case matchesAwsV2BlockedOption(lower, "--with-decryption"):
			return fmt.Errorf("decrypting AWS parameter values in the workspace CLI is not allowed")
		case strings.Contains(lower, "file://") || strings.Contains(lower, "fileb://"):
			return fmt.Errorf("AWS workspace CLI cannot read files from cloud-collector")
		}
	}
	return nil
}

// matchesAwsV2BlockedOption also matches long-option abbreviations accepted by
// AWS CLI. Ambiguous prefixes fail closed. The comparison remains scoped to
// the explicit transport-boundary denylist above; unrelated AWS flags pass.
func matchesAwsV2BlockedOption(arg, blockedOption string) bool {
	option, _, _ := strings.Cut(arg, "=")
	return strings.HasPrefix(option, "--") && len(option) > len("--") && strings.HasPrefix(blockedOption, option)
}

// awsV2BlockedOperations are unsafe at the cloud-collector transport boundary
// regardless of whether the outer tool call was approved. They expose fresh
// credentials or require collector-local file access; ordinary AWS mutations
// are authorized and approval-gated by llm-server before reaching this API.
var awsV2BlockedOperations = map[string]map[string]bool{
	"codeartifact": {
		"get-authorization-token": true,
	},
	"ecr": {
		"get-authorization-token": true,
		"get-login-password":      true,
	},
	"eks": {
		"get-token": true,
	},
	"cognito-identity": {
		"get-credentials-for-identity": true,
		"get-open-id-token":            true,
	},
	"lightsail": {
		"get-instance-access-details":                  true,
		"get-relational-database-master-user-password": true,
	},
	"rds": {
		"generate-db-auth-token": true,
	},
	"redshift": {
		"get-cluster-credentials":          true,
		"get-cluster-credentials-with-iam": true,
	},
	"redshift-serverless": {
		"get-credentials": true,
	},
	"s3": {
		"cp":      true,
		"mv":      true,
		"presign": true,
		"sync":    true,
	},
	"s3api": {
		"get-object":            true,
		"get-object-torrent":    true,
		"put-object":            true,
		"select-object-content": true,
		"upload-part":           true,
	},
	"secretsmanager": {
		"batch-get-secret-value": true,
		"get-secret-value":       true,
	},
}

func awsCliEnv(ctx context.Context, account providers.Account, command string) ([]string, error) {
	var cmdEnv []string
	region := "us-east-1"
	if account.Region != nil {
		region = *account.Region
	}
	if !strings.Contains(command, "--region") {
		cmdEnv = append(cmdEnv, "AWS_REGION="+region)
	}

	cfg, err := getAwsConfigFromAccount(ctx, account)
	if err != nil {
		return nil, err
	}
	awsCreds, err := cfg.Credentials.Retrieve(ctx)
	if err != nil {
		return nil, err
	}
	cmdEnv = append(cmdEnv,
		"AWS_ACCESS_KEY_ID="+awsCreds.AccessKeyID,
		"AWS_SECRET_ACCESS_KEY="+awsCreds.SecretAccessKey,
	)
	if awsCreds.SessionToken != "" {
		cmdEnv = append(cmdEnv, "AWS_SESSION_TOKEN="+awsCreds.SessionToken)
	}
	if os.Getenv("AWS_PROFILE") != "" {
		cmdEnv = append(cmdEnv, "AWS_PROFILE="+os.Getenv("AWS_PROFILE"))
	}
	return cmdEnv, nil
}

func (a *awsProvider) ExecuteCliCommand(ctx providers.CloudProviderContext, account providers.Account, command string) (string, error) {
	command = strings.TrimSpace(command)
	if !strings.HasPrefix(command, "aws ") {
		command = "aws " + command
	}

	if err := common.ValidateCliCommand(command, awsBlockedCommands); err != nil {
		return "", err
	}

	cmdEnv, err := awsCliEnv(ctx.GetContext(), account, command)
	if err != nil {
		return "", err
	}

	// Handle backslash line continuations before parsing
	cleanCommand := strings.ReplaceAll(command, "\\\r\n", " ")
	cleanCommand = strings.ReplaceAll(cleanCommand, "\\\n", " ")

	var stdout, stderr string
	opts := common.SecureCommandOptions{
		Command: cleanCommand,
		Env:     cmdEnv,
	}

	// Determine if the command uses a pipe (pipeline)
	// We use shlex to properly handle quoted strings so that a pipe character inside a quote
	// (e.g., in a query string) doesn't trigger pipeline execution.
	usePipeline := false
	args, err := shlex.Split(cleanCommand)
	if err == nil {
		for _, arg := range args {
			if arg == "|" {
				usePipeline = true
				break
			}
		}
	} else {
		// If parsing fails, we fall back to a naive check
		if strings.Contains(cleanCommand, "|") {
			usePipeline = true
		}
	}

	if usePipeline {
		stdout, stderr, err = common.SecureExecutePipeline(ctx.GetContext(), opts)
	} else {
		stdout, stderr, err = common.SecureExecute(ctx.GetContext(), opts)
	}

	if err != nil {
		ctx.GetLogger().Error("AWS CLI command execution failed", "error", err, "stderr", stderr, "command", command)
		return stdout, fmt.Errorf("AWS CLI command failed: %w, Stderr: %s", err, stderr)
	}

	return stdout, nil
}

// ExecuteCliCommandV2 executes exactly one AWS CLI process for workspace
// shims. Shell pipelines and local processing remain in the workspace; command
// exits are returned as data so the shim can preserve ordinary shell behavior.
func (a *awsProvider) ExecuteCliCommandV2(ctx providers.CloudProviderContext, account providers.Account, command string) (providers.CliExecutionResult, error) {
	command = strings.TrimSpace(command)
	if err := validateAwsV2Command(command); err != nil {
		return providers.CliExecutionResult{}, err
	}
	cmdEnv, err := awsCliEnv(ctx.GetContext(), account, command)
	if err != nil {
		return providers.CliExecutionResult{}, err
	}
	timeout := time.Duration(config.Config.CloudCollectorCliTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	maxOutput := config.Config.CloudCollectorCliMaxOutputBytes
	if maxOutput <= 0 {
		maxOutput = 1024 * 1024
	}
	outputTruncated := false
	stdout, stderr, execErr := common.SecureExecute(ctx.GetContext(), common.SecureCommandOptions{
		Command:         command,
		Env:             cmdEnv,
		Timeout:         timeout,
		MaxOutputBytes:  maxOutput,
		OutputTruncated: &outputTruncated,
	})
	result := providers.CliExecutionResult{Stdout: stdout, Stderr: stderr, OutputTruncated: outputTruncated}
	if execErr == nil {
		return result, nil
	}
	var exitErr *common.CommandExitError
	if errors.As(execErr, &exitErr) {
		result.ExitCode = exitErr.ExitCode
		return result, nil
	}
	var timeoutErr *common.CommandTimeoutError
	if errors.As(execErr, &timeoutErr) {
		result.ExitCode = 124
		result.TimedOut = true
		return result, nil
	}
	return result, execErr
}

func (a *awsProvider) QueryServiceMap(ctx providers.CloudProviderContext, account providers.Account, query providers.QueryServiceMapRequest) (providers.QueryServiceMapResponse, error) {
	// Check if new multi-source engine is enabled
	if a.shouldUseMultiSourceEngine(account) {
		ctx.GetLogger().Info("using multi-source service map engine", "account", account.AccountNumber)
		return a.queryServiceMapWithEngine(ctx, account, query)
	}

	// Legacy path: Try AWS Config first
	response, err := a.queryServiceMapWithConfig(ctx, account, query)
	if err != nil {
		if isConfigServiceNotEnabledError(err) {
			ctx.GetLogger().Warn("AWS Config is not enabled, falling back to service-specific discovery.", "account", account.AccountNumber, "region", query.Region)
			return a.queryServiceMapWithFallback(ctx, account, query)
		}
		return providers.QueryServiceMapResponse{}, err
	}
	return response, nil
}

// shouldUseMultiSourceEngine checks if the multi-source engine should be used
func (a *awsProvider) shouldUseMultiSourceEngine(account providers.Account) bool {
	// Global toggle (env: ENABLE_MULTI_SOURCE_SERVICEMAP)
	if config.Config.EnableMultiSourceServicemap {
		return true
	}

	// Check account-level configuration (if Account.Data supports it in the future)
	// For now, default to false for gradual rollout
	return false
}

// AWSProviderContext wraps CloudProviderContext with AWS-specific data
type AWSProviderContext struct {
	providers.CloudProviderContext
	cfg      aws.Config
	account  providers.Account
	provider *awsProvider
}

// NewAWSProviderContext creates a context with AWS config and account embedded
func NewAWSProviderContext(
	baseCtx providers.CloudProviderContext,
	cfg aws.Config,
	account providers.Account,
	provider *awsProvider,
) *AWSProviderContext {
	return &AWSProviderContext{
		CloudProviderContext: baseCtx,
		cfg:                  cfg,
		account:              account,
		provider:             provider,
	}
}

// GetAWSConfig extracts AWS config from context
func (a *AWSProviderContext) GetAWSConfig() aws.Config {
	return a.cfg
}

// GetAccount extracts account from context
func (a *AWSProviderContext) GetAccount() providers.Account {
	return a.account
}

// GetProvider extracts provider from context
func (a *AWSProviderContext) GetProvider() *awsProvider {
	return a.provider
}

// Deadline implements context.Context interface
func (a *AWSProviderContext) Deadline() (deadline time.Time, ok bool) {
	if a.CloudProviderContext == nil {
		return time.Time{}, false
	}
	return a.CloudProviderContext.GetContext().Deadline()
}

// Done implements context.Context interface
func (a *AWSProviderContext) Done() <-chan struct{} {
	if a.CloudProviderContext == nil {
		return nil
	}
	return a.CloudProviderContext.GetContext().Done()
}

// Err implements context.Context interface
func (a *AWSProviderContext) Err() error {
	if a.CloudProviderContext == nil {
		return nil
	}
	return a.CloudProviderContext.GetContext().Err()
}

// Value implements context.Context interface
func (a *AWSProviderContext) Value(key interface{}) interface{} {
	// Check if requesting our custom values
	switch key {
	case "aws.Config":
		return a.cfg
	case "providers.Account":
		return a.account
	case "*awsProvider":
		return a.provider
	}

	// Delegate to embedded context
	if a.CloudProviderContext != nil {
		return a.CloudProviderContext.GetContext().Value(key)
	}
	return nil
}

// QueryServiceMapWithConfig makes queryServiceMapWithConfig available for interface compliance
func (a *awsProvider) QueryServiceMapWithConfig(
	ctx providers.CloudProviderContext,
	account providers.Account,
	query providers.QueryServiceMapRequest,
) (providers.QueryServiceMapResponse, error) {
	return a.queryServiceMapWithConfig(ctx, account, query)
}

// QueryServiceMapWithFallback makes queryServiceMapWithFallback available for interface compliance
func (a *awsProvider) QueryServiceMapWithFallback(
	ctx providers.CloudProviderContext,
	account providers.Account,
	query providers.QueryServiceMapRequest,
) (providers.QueryServiceMapResponse, error) {
	return a.queryServiceMapWithFallback(ctx, account, query)
}

// GetLogGroupName delegates to the appropriate service's GetLogGroupName method
func (a *awsProvider) GetLogGroupName(
	ctx providers.CloudProviderContext,
	account providers.Account,
	region string,
	resourceId string,
	serviceName string,
) (string, error) {
	// Get the service
	service, ok := GetAwsService(serviceName)
	if !ok {
		return "", fmt.Errorf("unknown service: %s", serviceName)
	}

	// Call the service's GetLogGroupName
	return service.GetLogGroupName(ctx, account, region, resourceId)
}

// GetResourceIPAddress extracts IP and port for a resource - delegates to aws_vpc_flowlogs.go
func (a *awsProvider) GetResourceIPAddress(
	ctx providers.CloudProviderContext,
	account providers.Account,
	serviceApplicationId providers.ServiceApplicationId,
) (string, int, error) {
	return GetResourceIPAddress(ctx, account, serviceApplicationId)
}

// MapIPToAWSResource maps an IP to a resource - delegates to aws_vpc_flowlogs.go
func (a *awsProvider) MapIPToAWSResource(
	ctx providers.CloudProviderContext,
	account providers.Account,
	cfg aws.Config,
	ip string,
	region string,
) (*providers.ServiceApplicationId, error) {
	return MapIPToAWSResource(ctx, account, cfg, ip, region)
}

// MapIPsToAWSResources maps multiple IPs to resources in bulk - delegates to aws_vpc_flowlogs.go
func (a *awsProvider) MapIPsToAWSResources(
	ctx providers.CloudProviderContext,
	account providers.Account,
	cfg aws.Config,
	ips []string,
	region string,
) (map[string]*providers.ServiceApplicationId, error) {

	// Database-only lookup (no AWS API fallback for performance)
	if account.AccountNumber == "" || len(ips) == 0 {
		return make(map[string]*providers.ServiceApplicationId), nil
	}

	ipToResource, err := queryResourcesByPrivateIPs(ctx, account, region, ips)
	if err != nil {
		ctx.GetLogger().Warn("bulk database query FAILED, returning empty results",
			"error", err,
			"ipCount", len(ips))
		return make(map[string]*providers.ServiceApplicationId), nil
	}

	// Log which IPs were not found in database
	if len(ipToResource) < len(ips) {
		notFoundCount := len(ips) - len(ipToResource)
		ctx.GetLogger().Info("bulk IP lookup completed (database only)",
			"queriedIPs", len(ips),
			"foundResources", len(ipToResource),
			"notFoundInDB", notFoundCount)
	} else {
		ctx.GetLogger().Info("bulk IP lookup completed (all found in database)",
			"queriedIPs", len(ips),
			"foundResources", len(ipToResource))
	}

	return ipToResource, nil
}

// DescribeResourceByService calls DescribeResource on the appropriate service
func (a *awsProvider) DescribeResourceByService(
	ctx providers.CloudProviderContext,
	account providers.Account,
	region, resourceId, serviceName string,
) (*servicemap.ResourceMetadata, error) {
	// Get the service
	service, ok := GetAwsService(serviceName)
	if !ok {
		return nil, fmt.Errorf("unsupported service: %s", serviceName)
	}

	// Call the service's DescribeResource method
	awsMetadata, err := service.DescribeResource(ctx, account, region, resourceId)
	if err != nil {
		return nil, err
	}

	// Convert aws.ResourceMetadata to servicemap.ResourceMetadata
	// This avoids circular dependency issues
	return &servicemap.ResourceMetadata{
		ResourceID:     awsMetadata.ResourceID,
		ResourceARN:    awsMetadata.ResourceARN,
		VpcID:          awsMetadata.VpcID,
		PrivateIP:      awsMetadata.PrivateIP,
		PublicIP:       awsMetadata.PublicIP,
		Port:           awsMetadata.Port,
		SecurityGroups: awsMetadata.SecurityGroups,
		Subnets:        awsMetadata.Subnets,
		Status:         awsMetadata.Status,
		Tags:           awsMetadata.Tags,
		Metadata:       awsMetadata.Metadata,
	}, nil
}

// queryServiceMapWithEngine uses the multi-source QueryEngine for parallel querying
func (a *awsProvider) queryServiceMapWithEngine(
	ctx providers.CloudProviderContext,
	account providers.Account,
	query providers.QueryServiceMapRequest,
) (providers.QueryServiceMapResponse, error) {
	// Get AWS config
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		return providers.QueryServiceMapResponse{}, fmt.Errorf("failed to create aws config: %w", err)
	}

	// Create enhanced context with AWS config and account
	enhancedCtx := NewAWSProviderContext(ctx, cfg, account, a)

	// Create sources
	sources := []servicemap.RelationshipSource{
		servicemap.NewAWSConfigSource(a, ctx.GetLogger()),
		servicemap.NewVPCFlowLogsSource(a, ctx.GetLogger()),
		servicemap.NewServiceSpecificSource(a, ctx.GetLogger()),
	}

	ctx.GetLogger().Info("initializing multi-source service map engine",
		"account", account.AccountNumber,
		"sources", len(sources),
		"region", query.Region)

	// Create engine
	engine := servicemap.NewQueryEngine(sources, ctx.GetLogger())
	engine.SetTimeout(60 * time.Second)

	// Convert request to QueryRequest format
	queryRequest := servicemap.QueryRequest{
		Resources: make([]servicemap.ResourceRequest, len(query.Resources)),
		TimeRange: nil, // Default to last 1 hour
	}

	for i, res := range query.Resources {
		resourceType := res.ServiceName
		if key, ok := serviceNameToKey[resourceType]; ok {
			resourceType = key
		}
		queryRequest.Resources[i] = servicemap.ResourceRequest{
			ResourceID:   res.Resource,
			ResourceType: resourceType,
			Region:       query.Region,
		}
	}

	// Execute parallel query
	applications, err := engine.Query(enhancedCtx, cfg, account, queryRequest)
	if err != nil {
		ctx.GetLogger().Error("multi-source engine failed, falling back to legacy",
			"error", err,
			"account", account.AccountNumber)
		// Fallback to legacy on error
		response, fallbackErr := a.queryServiceMapWithConfig(ctx, account, query)
		if fallbackErr != nil {
			if isConfigServiceNotEnabledError(fallbackErr) {
				return a.queryServiceMapWithFallback(ctx, account, query)
			}
			return providers.QueryServiceMapResponse{}, fallbackErr
		}
		return response, nil
	}

	ctx.GetLogger().Info("multi-source engine completed successfully",
		"account", account.AccountNumber,
		"applications", len(applications))

	return providers.QueryServiceMapResponse{
		Applications: applications,
	}, nil
}

func (a *awsProvider) ListEventRules(ctx providers.CloudProviderContext, account providers.Account) (providers.ListEventRules, error) {
	regions, err := a.getRegions(ctx, account)
	if err != nil {
		return providers.ListEventRules{}, fmt.Errorf("aws: failed to get regions: %w", err)
	}

	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		return providers.ListEventRules{}, fmt.Errorf("aws: failed to create aws session: %w", err)
	}

	var eventRules []providers.EventRule
	var wg sync.WaitGroup
	var mu sync.Mutex
	errChan := make(chan error, len(regions))

	for _, region := range regions {
		wg.Add(1)
		go func(regionName string) {
			defer wg.Done()
			// Each goroutine needs its own config: mutating the shared cfg.Region
			// raced across regions, so a client could be built for another region.
			regionCfg := cfg.Copy()
			regionCfg.Region = regionName

			// Deliberately not derived from the job context: this runs at the end
			// of the post-report job, often after its time budget has expired, and
			// the alarm sync would otherwise fail outright on those accounts. The
			// region still gets its own bound so a hung endpoint can't pin the
			// goroutine.
			regionCtx, cancel := context.WithTimeout(context.Background(), alarmRegionTimeout)
			defer cancel()
			rules, err := describeRegionAlarmRules(regionCtx, ctx.GetLogger(), cloudwatch.NewFromConfig(regionCfg), regionName, account.AccountNumber)
			if err != nil {
				if skipAlarmRegion(ctx.GetLogger(), err, regionName) {
					return
				}
				errChan <- fmt.Errorf("failed to describe alarms in %s: %w", regionName, err)
				return
			}
			mu.Lock()
			eventRules = append(eventRules, rules...)
			mu.Unlock()
		}(region)
	}

	wg.Wait()
	close(errChan)

	var allErrors error
	for err := range errChan {
		allErrors = multierr.Append(allErrors, err)
	}

	// get file based rules
	// ruleSet, err := GetEventRules("")
	// if err != nil {
	// 	return providers.ListEventRules{}, fmt.Errorf("failed to get event rules: %w", err)
	// }

	// for _, rule := range ruleSet.Rules {
	// 	var exprs []string
	// 	for _, filter := range rule.Triggers.EventFilters {
	// 		exprs = append(exprs, filter.Template)
	// 	}

	// 	var severity providers.EventDefinitionSeverity
	// 	switch strings.ToLower(rule.EventOutput.Severity) {
	// 	case "critical":
	// 		severity = providers.EventDefinitionSeverityInfo
	// 	case "error":
	// 		severity = providers.EventDefinitionSeverityInfo
	// 	case "warning":
	// 		severity = providers.EventDefinitionSeverityDebug
	// 	case "info":
	// 		severity = providers.EventDefinitionSeverityDebug
	// 	default:
	// 		severity = providers.EventDefinitionSeverityDebug
	// 	}

	// 	if rule.EventOutput.Title.Value == "" {
	// 		rule.EventOutput.Title.Value = rule.Name
	// 	}

	// 	eventRules = append(eventRules, providers.EventRule{
	// 		Name:        rule.Name,
	// 		Description: rule.EventOutput.Description.Value,
	// 		Summary:     rule.EventOutput.Title.Value,
	// 		Expr:        strings.Join(exprs, " && "),
	// 		Source:      rule.Triggers.SourceSystem,
	// 		Severity:    severity,
	// 	})
	// }

	return providers.ListEventRules{Items: eventRules}, allErrors
}

func (a *awsProvider) Name() string {
	return "AWS"
}

func init() {
	providers.RegisterProvider(&awsProvider{})
}
