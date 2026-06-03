package aws

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"nudgebee/collector/cloud/common"
	"nudgebee/collector/cloud/providers"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	"github.com/aws/aws-sdk-go-v2/service/elasticache"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/kafka"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/samber/lo"
)

type serviceNamespace struct {
	Name                          string
	ResourceDimensionName         string
	ServiceName                   string
	ResourceType                  string
	Metrices                      map[string][]string
	MetricsStats                  map[string][]string
	AdditionalDimensions          map[string]map[string]string
	Instances                     func(cfg aws.Config, resourceType string) []string
	PrepareDimensionsAndNamespace func(
		ctx context.Context,
		cfg aws.Config,
		baseNamespaceName string, // The default namespace from the map (e.g., "AWS/ECS")
		resourceType string, // The requested resource type (e.g., "cluster", "service", "task")
		resourceIdentifiers []string, // The list of resource names or ARNs
		logger *slog.Logger,
	) (
		targetNamespace string, // The actual CloudWatch namespace to use for the query
		dimensionsByResourceID map[string][]types.Dimension, // Map: original resourceId -> its dimensions
		finalResourceIDs []string, // List of resource IDs for which dimensions were successfully prepared
		err error,
	)
}

func getEc2Instances(cfg aws.Config, resourceType string) []string {
	svc := ec2.NewFromConfig(cfg)
	paginator := ec2.NewDescribeInstancesPaginator(svc, &ec2.DescribeInstancesInput{
		Filters: []ec2types.Filter{
			{
				Name:   aws.String("instance-state-name"),
				Values: []string{"running"},
			},
		},
	})
	var instanceIds []string
	for paginator.HasMorePages() {
		output, err := paginator.NextPage(context.TODO())
		if err != nil {
			return nil
		}
		for _, reservation := range output.Reservations {
			for _, instance := range reservation.Instances {
				instanceIds = append(instanceIds, *instance.InstanceId)
			}
		}
	}
	return instanceIds
}

func getRdsInstances(cfg aws.Config, resourceType string) []string {
	svc := rds.NewFromConfig(cfg)
	paginator := rds.NewDescribeDBInstancesPaginator(svc, &rds.DescribeDBInstancesInput{})
	var instanceIds []string
	for paginator.HasMorePages() {
		output, err := paginator.NextPage(context.TODO())
		if err != nil {
			return nil
		}
		for _, dbInstance := range output.DBInstances {
			instanceIds = append(instanceIds, *dbInstance.DBInstanceIdentifier)
		}
	}
	return instanceIds
}

func getS3Buckets(cfg aws.Config, resourceType string) []string {
	svc := s3.NewFromConfig(cfg)
	output, err := svc.ListBuckets(context.TODO(), &s3.ListBucketsInput{})
	if err != nil {
		return nil
	}
	var bucketNames []string
	for _, bucket := range output.Buckets {
		bucketNames = append(bucketNames, *bucket.Name)
	}
	return bucketNames
}

func getLambdaFunctions(cfg aws.Config, resourceType string) []string {
	svc := lambda.NewFromConfig(cfg)
	paginator := lambda.NewListFunctionsPaginator(svc, &lambda.ListFunctionsInput{})
	var functionNames []string
	for paginator.HasMorePages() {
		output, err := paginator.NextPage(context.TODO())
		if err != nil {
			return nil
		}
		for _, function := range output.Functions {
			functionNames = append(functionNames, *function.FunctionName)
		}
	}
	return functionNames
}

func getElastiCacheClusters(cfg aws.Config, resourceType string) []string {
	svc := elasticache.NewFromConfig(cfg)
	paginator := elasticache.NewDescribeCacheClustersPaginator(svc, &elasticache.DescribeCacheClustersInput{})
	var clusterIds []string
	for paginator.HasMorePages() {
		output, err := paginator.NextPage(context.TODO())
		if err != nil {
			return nil
		}
		for _, cluster := range output.CacheClusters {
			clusterIds = append(clusterIds, *cluster.CacheClusterId)
		}
	}
	return clusterIds
}

func getIAMUsers(cfg aws.Config, resourceType string) []string {
	svc := iam.NewFromConfig(cfg)
	paginator := iam.NewListUsersPaginator(svc, &iam.ListUsersInput{})
	var userNames []string
	for paginator.HasMorePages() {
		output, err := paginator.NextPage(context.TODO())
		if err != nil {
			return nil
		}
		for _, user := range output.Users {
			userNames = append(userNames, *user.UserName)
		}
	}
	return userNames
}

func getMSKClusters(cfg aws.Config, resourceType string) []string {
	svc := kafka.NewFromConfig(cfg)
	paginator := kafka.NewListClustersPaginator(svc, &kafka.ListClustersInput{})
	var clusterNames []string
	for paginator.HasMorePages() {
		output, err := paginator.NextPage(context.TODO())
		if err != nil {
			return nil
		}
		for _, cluster := range output.ClusterInfoList {
			clusterNames = append(clusterNames, *cluster.ClusterName)
		}
	}
	return clusterNames
}

func getECSServices(cfg aws.Config, resourceType string) []string {
	ecsSvc := ecs.NewFromConfig(cfg)
	var serviceArns []string

	clustersPaginator := ecs.NewListClustersPaginator(ecsSvc, &ecs.ListClustersInput{})
	for clustersPaginator.HasMorePages() {
		clustersOutput, err := clustersPaginator.NextPage(context.TODO())
		if err != nil {
			// In a real implementation, you'd want to log this error
			continue
		}

		for _, clusterArn := range clustersOutput.ClusterArns {
			servicesPaginator := ecs.NewListServicesPaginator(ecsSvc, &ecs.ListServicesInput{
				Cluster: aws.String(clusterArn),
			})
			for servicesPaginator.HasMorePages() {
				servicesOutput, err := servicesPaginator.NextPage(context.TODO())
				if err != nil {
					// Log this error as well
					continue
				}
				serviceArns = append(serviceArns, servicesOutput.ServiceArns...)
			}
		}
	}
	return serviceArns
}

var serviceCloudwatchNamespaceMap = map[string]serviceNamespace{
	"amazonec2": {
		Name:                  "AWS/EC2",
		ResourceDimensionName: "InstanceId",
		ServiceName:           ServiceNameEc2,
		ResourceType:          "compute-instance",
		Metrices:              map[string][]string{"compute-instance": {"CPUUtilization", "DiskReadOps", "DiskWriteOps", "DiskReadBytes", "DiskWriteBytes", "NetworkIn", "NetworkOut"}},
		Instances:             getEc2Instances,
	},
	"amazonrds": {
		Name:                  "AWS/RDS",
		ResourceDimensionName: "DBInstanceIdentifier",
		ServiceName:           ServiceNameRDS,
		ResourceType:          "db",
		Metrices:              map[string][]string{"db": {"CPUUtilization", "FreeableMemory", "DatabaseConnections", "FreeStorageSpace", "ReadIOPS", "WriteIOPS", "ReadLatency", "WriteLatency", "ReadThroughput", "WriteThroughput"}},
		Instances:             getRdsInstances,
	},
	"amazonrdspi": {
		Name:                  "AWS/PI",
		ResourceDimensionName: "DBInstanceIdentifier",
		ServiceName:           ServiceNameRDS,
		ResourceType:          "db",
		Metrices:              map[string][]string{"db": {"db.load.avg", "db.load.cpu", "db.load.io", "db.load.lock"}},
		Instances:             getRdsInstances,
	},
	"amazons3": {
		Name:                  "AWS/S3",
		ResourceDimensionName: "BucketName",
		ServiceName:           ServiceNameS3,
		ResourceType:          "storage",
		Metrices:              map[string][]string{"storage": {"BucketSizeBytes", "NumberOfObjects"}},
		AdditionalDimensions:  map[string]map[string]string{"NumberOfObjects": {"StorageType": "AllStorageTypes"}, "BucketSizeBytes": {"StorageType": "StandardStorage"}},
		Instances:             getS3Buckets,
	},
	"awslambda": {
		Name:                  "AWS/Lambda",
		ResourceDimensionName: "FunctionName",
		ServiceName:           ServiceNameLambda,
		ResourceType:          "function",
		Metrices:              map[string][]string{"function": {"Invocations", "Errors", "Duration", "Throttles"}},
		Instances:             getLambdaFunctions,
	},
	"amazonelasticcache": {
		Name:                  "AWS/ElastiCache",
		ResourceDimensionName: "CacheClusterId",
		ServiceName:           ServiceNameElastiCache,
		ResourceType:          "cluster",
		Metrices:              map[string][]string{"cluster": {"CPUUtilization", "FreeableMemory", "NetworkBytesIn", "NetworkBytesOut", "EngineCPUUtilization", "Evictions", "CurrConnections", "NewConnections", "ReplicationLag", "CacheHitRate", "CacheMisses", "CacheHits", "BytesUsedForCache"}},
		Instances:             getElastiCacheClusters,
	},
	"awsiam": {
		Name:                  "AWS/IAM",
		ResourceDimensionName: "UserName",
		ServiceName:           ServiceNameIAM,
		ResourceType:          "user",
		Metrices:              map[string][]string{"user": {"UsersWithMFAEnabled", "UsersWithoutMFA"}},
		Instances:             getIAMUsers,
	},
	"amazonmsk": {
		Name:                  "AWS/Kafka",
		ResourceDimensionName: "Cluster Name",
		ServiceName:           ServiceNameMSK,
		ResourceType:          "cluster",
		Metrices:              map[string][]string{"cluster": {}},
		Instances:             getMSKClusters,
	},
	"amazonecs": {
		Name:                  "AWS/ECS",
		ResourceDimensionName: "ClusterName",
		ServiceName:           ServiceNameECS,
		ResourceType:          "cluster",
		Metrices: map[string][]string{
			"cluster": {
				"CPUUtilization",
				"MemoryUtilization",
				"CPUReservation",
				"MemoryReservation",
			},
			"service": {
				"CPUUtilization",
				"MemoryUtilization",
				"RunningTaskCount",
				"DesiredTaskCount",
				"PendingTaskCount",
			},
			"task": {"CpuUtilized", "MemoryUtilized", "NetworkRxBytes", "NetworkTxBytes", "StorageReadBytes", "StorageWriteBytes"},
		},
		MetricsStats: map[string][]string{
			"CPUUtilization":    {"Maximum"},
			"MemoryUtilization": {"Maximum"},
			"CpuUtilized":       {"Maximum"},
			"MemoryUtilized":    {"Maximum"},
			"RunningTaskCount":  {"Average"},
			"DesiredTaskCount":  {"Average"},
			"PendingTaskCount":  {"Average"},
			"NetworkRxBytes":    {"Sum"},
			"NetworkTxBytes":    {"Sum"},
			"StorageReadBytes":  {"Sum"},
			"StorageWriteBytes": {"Sum"},
		},
		Instances: getECSServices,
		PrepareDimensionsAndNamespace: func(
			ctx context.Context,
			cfg aws.Config,
			baseNamespaceName string,
			resourceType string,
			resourceIdentifiers []string,
			logger *slog.Logger,
		) (
			targetNamespace string,
			dimensionsByResourceID map[string][]types.Dimension,
			finalResourceIDs []string,
			err error,
		) {
			dimensionsByResourceID = make(map[string][]types.Dimension)
			ecsSvc := ecs.NewFromConfig(cfg)

			switch strings.ToLower(resourceType) {
			case "cluster":
				// For "cluster" resourceType, we aim to provide an aggregated view.
				// For Fargate, direct cluster CPU/Memory is less relevant than service/task utilization.
				// For EC2, direct cluster CPU/Memory reflects EC2 capacity.
				// Here, we will fetch metrics for all services within the specified cluster(s)
				// and rely on the downstream aggregation logic in getAwsCloudwatchMetrics
				// to average them out, giving a "cluster-wide average service utilization".
				// This provides a consistent approach for both Fargate and EC2 from a service perspective.
				// If direct EC2 capacity metrics are needed, a more specific resourceType or query
				// targeting AWS/EC2 namespace with appropriate tags might be necessary.

				targetNamespace = "AWS/ECS"
				// `resourceIdentifiers` here are cluster names/ARNs.
				for _, clusterIdentifier := range resourceIdentifiers {
					var nextTokenServices *string
					for {
						listServicesOutput, errListServices := ecsSvc.ListServices(ctx, &ecs.ListServicesInput{
							Cluster:   aws.String(clusterIdentifier), // Use cluster name/ARN
							NextToken: nextTokenServices,
						})
						if errListServices != nil {
							logger.Warn("Failed to list services for cluster, skipping for cluster metrics aggregation", "cluster", clusterIdentifier, "error", errListServices)
							break // Stop trying to list services for this cluster
						}

						for _, serviceArn := range listServicesOutput.ServiceArns {
							if serviceArn == "" {
								logger.Warn("Empty ECS service ARN encountered during cluster metrics prep", "cluster", clusterIdentifier)
							}
							// Parse service ARN to get ClusterName and ServiceName for dimensions
							arnParts := strings.Split(serviceArn, "/")
							if len(arnParts) < 2 {
								logger.Warn("Invalid ECS service ARN format for dimension parsing during cluster metrics prep", "arn", serviceArn)
								continue
							}
							parsedClusterName := arnParts[len(arnParts)-2] // This should match clusterIdentifier or its name part
							parsedServiceName := arnParts[len(arnParts)-1]

							dimensionsByResourceID[serviceArn] = []types.Dimension{
								{Name: aws.String("ClusterName"), Value: aws.String(parsedClusterName)}, // Use parsed cluster name
								{Name: aws.String("ServiceName"), Value: aws.String(parsedServiceName)},
							}
							finalResourceIDs = append(finalResourceIDs, serviceArn)
						}

						nextTokenServices = listServicesOutput.NextToken
						if nextTokenServices == nil {
							break
						}
					}
				}
			case "service":
				targetNamespace = "AWS/ECS" // Default for basic service metrics
				// For ContainerInsights, it would be "AWS/ECS/ContainerInsights"
				// This example uses AWS/ECS. If ContainerInsights is preferred, change targetNamespace
				// and ensure metrics in serviceCloudwatchNamespaceMap["amazonecs"].Metrices["service"] are compatible.
				// If using ContainerInsights, metrics like "CpuUtilized", "MemoryUtilized" are available.
				// If using AWS/ECS, metrics like "CPUUtilization", "MemoryUtilization" are available.
				// The current `Metrices` map for "service" lists "CPUUtilization", "MemoryUtilization", etc. which are AWS/ECS.

				for _, serviceArn := range resourceIdentifiers {
					// ARN: arn:aws:ecs:region:account-id:service/cluster-name/service-name
					arnParts := strings.Split(serviceArn, "/")
					if len(arnParts) < 2 { // Simplified check
						logger.Warn("Invalid ECS service ARN format for dimension parsing", "arn", serviceArn)
						continue
					}
					clusterName := arnParts[len(arnParts)-2]
					serviceName := arnParts[len(arnParts)-1]
					dimensionsByResourceID[serviceArn] = []types.Dimension{
						{Name: aws.String("ClusterName"), Value: aws.String(clusterName)},
						{Name: aws.String("ServiceName"), Value: aws.String(serviceName)},
					}
					finalResourceIDs = append(finalResourceIDs, serviceArn)
				}
			case "task": // Assumes Container Insights metrics
				targetNamespace = "AWS/ECS/ContainerInsights"
				// Group task ARNs by cluster for efficient DescribeTasks calls
				tasksByCluster := lo.GroupBy(resourceIdentifiers, func(arn string) string {
					// arn:aws:ecs:region:account-id:task/cluster-name/task-id
					parts := strings.Split(arn, "/")
					if len(parts) >= 2 {
						return parts[len(parts)-2] // cluster-name
					}
					return "" // Invalid ARN
				})

				for clusterName, taskArnsInCluster := range tasksByCluster {
					if clusterName == "" {
						logger.Warn("Skipping tasks with invalid ARNs or unparsable cluster name", "arns", taskArnsInCluster)
						continue
					}
					// DescribeTasks to get ServiceName (as TaskId alone isn't enough for all CI metrics)
					descTasksInput := &ecs.DescribeTasksInput{
						Cluster: aws.String(clusterName),
						Tasks:   taskArnsInCluster,
					}
					descTasksOutput, descErr := ecsSvc.DescribeTasks(ctx, descTasksInput)
					if descErr != nil {
						logger.Warn("Failed to describe ECS tasks for metrics", "error", descErr, "cluster", clusterName, "taskArns", taskArnsInCluster)
						continue // Skip this cluster's tasks
					}

					for _, task := range descTasksOutput.Tasks {
						if task.TaskArn == nil {
							continue
						}
						taskID := strings.Split(*task.TaskArn, "/")[len(strings.Split(*task.TaskArn, "/"))-1]
						serviceName := "UNKNOWN_SERVICE" // Default if not associated with a service
						if task.Group != nil && strings.HasPrefix(*task.Group, "service:") {
							serviceName = strings.TrimPrefix(*task.Group, "service:")
						}

						dimensionsByResourceID[*task.TaskArn] = []types.Dimension{
							{Name: aws.String("ClusterName"), Value: aws.String(clusterName)},
							{Name: aws.String("ServiceName"), Value: aws.String(serviceName)},
							{Name: aws.String("TaskId"), Value: aws.String(taskID)},
						}
						finalResourceIDs = append(finalResourceIDs, *task.TaskArn)
					}
				}
			default:
				return "", nil, nil, fmt.Errorf("unsupported ECS resource type for metrics: %s", resourceType)
			}
			return targetNamespace, dimensionsByResourceID, lo.Uniq(finalResourceIDs), nil
		},
	},
	"awsfargate": {
		Name:                  "AWS/ECS",
		ResourceDimensionName: "ServiceName",
		ServiceName:           ServiceNameFargate,
		ResourceType:          "service",
		Metrices: map[string][]string{
			"service": {
				"CPUUtilization",
				"MemoryUtilization",
				"RunningTaskCount",
				"DesiredTaskCount",
				"PendingTaskCount",
			},
			"task": {"CpuUtilized", "MemoryUtilized", "NetworkRxBytes", "NetworkTxBytes", "StorageReadBytes", "StorageWriteBytes"},
		},
		MetricsStats: map[string][]string{
			"CPUUtilization":    {"Maximum"},
			"MemoryUtilization": {"Maximum"},
			"CpuUtilized":       {"Maximum"},
			"MemoryUtilized":    {"Maximum"},
			"RunningTaskCount":  {"Average"},
			"DesiredTaskCount":  {"Average"},
			"PendingTaskCount":  {"Average"},
			"NetworkRxBytes":    {"Sum"},
			"NetworkTxBytes":    {"Sum"},
			"StorageReadBytes":  {"Sum"},
			"StorageWriteBytes": {"Sum"},
		},
		Instances: getECSServices,
		PrepareDimensionsAndNamespace: func(
			ctx context.Context,
			cfg aws.Config,
			baseNamespaceName string,
			resourceType string,
			resourceIdentifiers []string,
			logger *slog.Logger,
		) (
			targetNamespace string,
			dimensionsByResourceID map[string][]types.Dimension,
			finalResourceIDs []string,
			err error,
		) {
			dimensionsByResourceID = make(map[string][]types.Dimension)
			ecsSvc := ecs.NewFromConfig(cfg)

			switch strings.ToLower(resourceType) {
			case "service":
				targetNamespace = "AWS/ECS"
				for _, serviceArn := range resourceIdentifiers { // Expected format: arn:partition:service:region:account-id:service/cluster-name/service-name
					arnParts := strings.Split(serviceArn, "/")
					if len(arnParts) < 3 { // Need at least 3 parts to extract cluster-name and service-name
						logger.Warn("Invalid Fargate service ARN format for dimension parsing", "arn", serviceArn)
						continue
					}
					clusterName := arnParts[len(arnParts)-2]
					serviceName := arnParts[len(arnParts)-1]
					dimensionsByResourceID[serviceArn] = []types.Dimension{
						{Name: aws.String("ClusterName"), Value: aws.String(clusterName)},
						{Name: aws.String("ServiceName"), Value: aws.String(serviceName)},
					}
					finalResourceIDs = append(finalResourceIDs, serviceArn)
				}
			case "task":
				targetNamespace = "AWS/ECS/ContainerInsights"
				tasksByCluster := lo.GroupBy(resourceIdentifiers, func(arn string) string {
					parts := strings.Split(arn, "/") // Expected format: arn:partition:service:region:account-id:task/cluster-name/task-id
					if len(parts) >= 3 {             // Need at least 3 parts to extract cluster-name
						return parts[len(parts)-2]
					}
					return ""
				})

				for clusterName, taskArnsInCluster := range tasksByCluster {
					if clusterName == "" {
						logger.Warn("Skipping Fargate tasks with invalid ARNs", "arns", taskArnsInCluster)
						continue
					}
					descTasksInput := &ecs.DescribeTasksInput{
						Cluster: aws.String(clusterName),
						Tasks:   taskArnsInCluster,
					}
					descTasksOutput, descErr := ecsSvc.DescribeTasks(ctx, descTasksInput)
					if descErr != nil {
						logger.Warn("Failed to describe Fargate tasks for metrics", "error", descErr, "cluster", clusterName)
						continue
					}

					for _, task := range descTasksOutput.Tasks {
						if task.TaskArn == nil {
							continue
						}
						taskArnParts := strings.Split(*task.TaskArn, "/")
						taskID := taskArnParts[len(taskArnParts)-1]
						serviceName := "UNKNOWN_SERVICE"
						if task.Group != nil && strings.HasPrefix(*task.Group, "service:") {
							serviceName = strings.TrimPrefix(*task.Group, "service:")
						}

						dimensionsByResourceID[*task.TaskArn] = []types.Dimension{
							{Name: aws.String("ClusterName"), Value: aws.String(clusterName)},
							{Name: aws.String("ServiceName"), Value: aws.String(serviceName)},
							{Name: aws.String("TaskId"), Value: aws.String(taskID)},
						}
						finalResourceIDs = append(finalResourceIDs, *task.TaskArn)
					}
				}
			default:
				return "", nil, nil, fmt.Errorf("unsupported Fargate resource type for metrics: %s", resourceType)
			}
			return targetNamespace, dimensionsByResourceID, lo.Uniq(finalResourceIDs), nil
		},
	},
	"awselb": {
		Name:                  "AWS/ELB",
		ResourceDimensionName: "LoadBalancerName",
		ServiceName:           ServiceNameELB,
		ResourceType:          "loadbalancer",
		Metrices: map[string][]string{"loadbalancer": {
			"RequestCount", "HealthyHostCount", "UnHealthyHostCount",
			"Latency", "HTTPCode_Backend_2XX", "HTTPCode_Backend_4XX", "HTTPCode_Backend_5XX",
			"SurgeQueueLength", "SpilloverCount",
		}},
	},
	"awsqueueservice": {
		Name:                  "AWS/SQS",
		ResourceDimensionName: "QueueName",
		ServiceName:           ServiceNameSQS,
		ResourceType:          "queue",
		Metrices: map[string][]string{"queue": {
			"ApproximateNumberOfMessagesVisible", "ApproximateNumberOfMessagesNotVisible",
			"ApproximateAgeOfOldestMessage", "NumberOfMessagesSent",
			"NumberOfMessagesReceived", "NumberOfMessagesDeleted", "SentMessageSize",
		}},
	},
	"amazonsns": {
		Name:                  "AWS/SNS",
		ResourceDimensionName: "TopicName",
		ServiceName:           ServiceNameSNS,
		ResourceType:          "topic",
		Metrices: map[string][]string{"topic": {
			"NumberOfMessagesPublished", "NumberOfNotificationsDelivered",
			"NumberOfNotificationsFailed", "PublishSize",
		}},
	},
	"amazoneks": {
		Name:                  "AWS/EKS",
		ResourceDimensionName: "ClusterName",
		ServiceName:           ServiceNameEKS,
		ResourceType:          "cluster",
		Metrices: map[string][]string{"cluster": {
			"cluster_failed_node_count", "cluster_node_count",
		}},
	},
	"amazondynamodb": {
		Name:                  "AWS/DynamoDB",
		ResourceDimensionName: "TableName",
		ServiceName:           ServiceNameDynamoDB,
		ResourceType:          "table",
		Metrices: map[string][]string{"table": {
			"ConsumedReadCapacityUnits", "ConsumedWriteCapacityUnits",
			"ProvisionedReadCapacityUnits", "ProvisionedWriteCapacityUnits",
			"ReadThrottleEvents", "WriteThrottleEvents",
			"ThrottledRequests", "SystemErrors", "UserErrors",
		}},
	},
	"amazonredshift": {
		Name:                  "AWS/Redshift",
		ResourceDimensionName: "ClusterIdentifier",
		ServiceName:           ServiceNameRedshift,
		ResourceType:          "cluster",
		Metrices: map[string][]string{"cluster": {
			"CPUUtilization", "PercentageDiskSpaceUsed", "DatabaseConnections",
			"ReadIOPS", "WriteIOPS", "ReadLatency", "WriteLatency", "ReadThroughput", "WriteThroughput",
		}},
	},
	"amazoncloudfront": {
		Name:                  "AWS/CloudFront",
		ResourceDimensionName: "DistributionId",
		ServiceName:           ServiceNameCloudFront,
		ResourceType:          "distribution",
		Metrices: map[string][]string{"distribution": {
			"Requests", "BytesDownloaded", "BytesUploaded",
			"4xxErrorRate", "5xxErrorRate", "TotalErrorRate",
		}},
	},
	"amazonefs": {
		Name:                  "AWS/EFS",
		ResourceDimensionName: "FileSystemId",
		ServiceName:           ServiceNameEFS,
		ResourceType:          "filesystem",
		Metrices: map[string][]string{"filesystem": {
			"BurstCreditBalance", "ClientConnections",
			"DataReadIOBytes", "DataWriteIOBytes",
			"TotalIOBytes", "PercentIOLimit",
		}},
	},
	"amazones": {
		Name:                  "AWS/ES",
		ResourceDimensionName: "DomainName",
		ServiceName:           ServiceNameES,
		ResourceType:          "domain",
		Metrices: map[string][]string{"domain": {
			"CPUUtilization", "FreeStorageSpace", "ClusterUsedSpace",
			"SearchableDocuments", "Nodes", "SearchRate", "IndexingRate",
			"JVMMemoryPressure", "ThreadpoolSearchQueue",
		}},
	},
}

var metricsFunctions = map[string]func(vals []float64) float64{
	"Average": func(vals []float64) float64 {
		sum := 0.0
		for _, val := range vals {
			sum += val
		}
		return sum / float64(len(vals))
	},
	"Sum": func(vals []float64) float64 {
		sum := 0.0
		for _, val := range vals {
			sum += val
		}
		return sum
	},
	"Maximum": func(vals []float64) float64 {
		return lo.Max(vals)
	},
	"Minimum": func(vals []float64) float64 {
		return lo.Min(vals)
	},
	"Count": func(vals []float64) float64 {
		return float64(len(vals))
	},
}

// getNamespaceForService returns the CloudWatch namespace for a given service name.
// Returns empty string if service is not recognized.
func getNamespaceForService(serviceName string) string {
	serviceName = strings.ToLower(serviceName)
	if config, ok := serviceCloudwatchNamespaceMap[serviceName]; ok {
		return config.Name
	}
	return ""
}

// listAwsCloudwatchMetricsDynamic calls the CloudWatch ListMetrics API to discover metrics dynamically.
func listAwsCloudwatchMetricsDynamic(ctx context.Context, cwClient *cloudwatch.Client, namespace string) (providers.ListMetricsResponse, error) {
	metricSet := make(map[string]bool)
	paginator := cloudwatch.NewListMetricsPaginator(cwClient, &cloudwatch.ListMetricsInput{
		Namespace: aws.String(namespace),
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return providers.ListMetricsResponse{}, err
		}
		for _, m := range page.Metrics {
			if m.MetricName != nil {
				metricSet[*m.MetricName] = true
			}
		}
	}

	metrics := make([]providers.AvailableMetric, 0, len(metricSet))
	for name := range metricSet {
		metrics = append(metrics, providers.AvailableMetric{
			Name:      name,
			Namespace: namespace,
		})
	}
	sort.Slice(metrics, func(i, j int) bool { return metrics[i].Name < metrics[j].Name })
	return providers.ListMetricsResponse{Metrics: metrics}, nil
}

func listAwsCloudwatchMetrics(request providers.ListMetricsRequest) (providers.ListMetricsResponse, error) {
	serviceName := strings.ToLower(request.ServiceName)
	serviceConfig, ok := serviceCloudwatchNamespaceMap[serviceName]
	if !ok {
		return providers.ListMetricsResponse{Metrics: []providers.AvailableMetric{}}, nil
	}

	resourceType := strings.ToLower(request.ResourceType)
	if resourceType == "" && len(serviceConfig.Metrices) > 0 {
		for rt := range serviceConfig.Metrices {
			resourceType = rt
			break
		}
	}

	metricNames := serviceConfig.Metrices[resourceType]
	metrics := make([]providers.AvailableMetric, 0, len(metricNames))
	for _, name := range metricNames {
		info := providers.AvailableMetric{
			Name:      name,
			Namespace: serviceConfig.Name,
		}
		if stats, ok := serviceConfig.MetricsStats[name]; ok {
			info.Statistics = stats
		}
		metrics = append(metrics, info)
	}

	return providers.ListMetricsResponse{Metrics: metrics}, nil
}

func getAwsCloudwatchMetrics(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	err := common.ValidateStruct(filter)
	if err != nil {
		ctx.GetLogger().Error("invalid filter", "error", err)
		return providers.QueryMetricsResponse{}, err
	}
	// Create an AWS session
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws config", "error", err, "accountNumber", account.AccountNumber)
		return providers.QueryMetricsResponse{}, err
	}
	region := filter.Region
	if region == "" {
		return providers.QueryMetricsResponse{}, fmt.Errorf("region is required for CloudWatch metrics query")
	}
	// Global AWS services (CloudFront, Route53, IAM, etc.) store resources with
	// region "global" but their CloudWatch metrics are only in us-east-1.
	if region == "global" {
		region = "us-east-1"
	}
	cfg.Region = region
	svc := cloudwatch.NewFromConfig(cfg)

	// Define the time period for metrics
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

	var serviceConfig serviceNamespace
	metricsNames := filter.MetricNames
	resourceType := strings.ToLower(filter.ResourceType)

	if filter.MetricNamespace != "" {
		serviceConfig = serviceNamespace{
			Name: filter.MetricNamespace,
		}
		if filter.ServiceName != "" {
			if serviceConfig1, ok := serviceCloudwatchNamespaceMap[strings.ToLower(filter.ServiceName)]; ok {
				serviceConfig = serviceConfig1
				serviceConfig.Name = filter.MetricNamespace
			}
		}
	} else {
		// Get base service namespace configuration
		serviceConfig = serviceCloudwatchNamespaceMap[strings.ToLower(filter.ServiceName)]
		if serviceConfig.Name == "" {
			ctx.GetLogger().Info("cloudwatch: service not supported, skipping metrics", "service", filter.ServiceName)
			return providers.QueryMetricsResponse{}, nil
		}
		if resourceType == "" && len(serviceConfig.Metrices) > 0 {
			resourceType = slices.Collect(maps.Keys(serviceConfig.Metrices))[0]
		}
	}

	// try detect metrics
	if len(metricsNames) == 0 && resourceType != "" {
		metricsNames = serviceConfig.Metrices[strings.ToLower(resourceType)]
		if len(metricsNames) == 0 {
			ctx.GetLogger().Info("invalid resource type for service, or no metrics defined", "service", filter.ServiceName, "resourceType", resourceType)
			return providers.QueryMetricsResponse{}, nil
		}
	}

	// Get initial resource identifiers. If filter.ResourceIds is empty, use the Instances function.
	resourceIds := filter.ResourceIds
	if len(resourceIds) == 0 && len(filter.Dimensions) == 0 && serviceConfig.Instances != nil {
		resourceIds = serviceConfig.Instances(cfg, resourceType)
	}
	if len(resourceIds) > 0 {
		resourceIds = lo.Uniq(resourceIds)
	}

	// Prepare dimensions and namespace
	var targetNamespaceName string
	var dimensionsByResourceID map[string][]types.Dimension
	var finalResourceIDsForQuery []string

	if len(filter.Dimensions) > 0 {
		dimensionsByResourceID = make(map[string][]types.Dimension)
		dimensions := []types.Dimension{}
		resourceId := ""

		for _, dimension := range filter.Dimensions {
			if dimension["Name"] != "" && dimension["Value"] != "" {
				if resourceId == "" {
					resourceId = dimension["Value"]
				}
				dimensions = append(dimensions, types.Dimension{
					Name:  aws.String(dimension["Name"]),
					Value: aws.String(dimension["Value"]),
				})
			} else {
				for k, v := range dimension {
					if resourceId == "" {
						resourceId = v
					}
					resourceId2 := v
					dimension := k
					if resourceId2 == "" || dimension == "" {
						continue
					}
					dimensions = append(dimensions, types.Dimension{
						Name:  aws.String(dimension),
						Value: aws.String(resourceId2),
					})
				}
			}
		}
		dimensionsByResourceID[resourceId] = append(dimensionsByResourceID[resourceId], dimensions...)
		finalResourceIDsForQuery = slices.Collect(maps.Keys(dimensionsByResourceID))
		targetNamespaceName = serviceConfig.Name
	} else if serviceConfig.PrepareDimensionsAndNamespace != nil {
		targetNamespaceName, dimensionsByResourceID, finalResourceIDsForQuery, err = serviceConfig.PrepareDimensionsAndNamespace(ctx.GetContext(), cfg, serviceConfig.Name, resourceType, resourceIds, ctx.GetLogger())
		if err != nil {
			ctx.GetLogger().Error("failed to prepare custom dimensions and namespace", "error", err, "service", filter.ServiceName)
			return providers.QueryMetricsResponse{}, err
		}
	} else {
		if serviceConfig.ResourceDimensionName == "" {
			return providers.QueryMetricsResponse{}, errors.New("cloudwatch: unable to identify resource-dimension-name, please provide dimensions explicitly")
		}
		// Default behavior for services without custom preparation
		targetNamespaceName = serviceConfig.Name
		dimensionsByResourceID = make(map[string][]types.Dimension)
		for _, id := range resourceIds {
			dimensionsByResourceID[id] = []types.Dimension{
				{Name: aws.String(serviceConfig.ResourceDimensionName), Value: aws.String(id)},
			}
		}
		finalResourceIDsForQuery = resourceIds
	}

	if len(finalResourceIDsForQuery) == 0 {
		ctx.GetLogger().Info("No final resource IDs after dimension preparation", "service", filter.ServiceName, "resourceType", resourceType)
		return providers.QueryMetricsResponse{}, nil
	}

	type queryMeta struct {
		resourceId string
		metricName string
		stat       string
	}
	queries := []types.MetricDataQuery{}
	queryIdMap := map[string]queryMeta{}
	queryIndex := 0
	for _, originalResourceID := range finalResourceIDsForQuery {
		for _, metricName := range metricsNames {
			stats := []string{"Average"}
			if serviceConfig.MetricsStats != nil && len(serviceConfig.MetricsStats[metricName]) > 0 {
				stats = serviceConfig.MetricsStats[metricName]
			}
			if len(filter.Statistics) > 0 {
				stats = filter.Statistics
			}
			for _, stat := range stats {
				queryId := fmt.Sprintf("m%d", queryIndex)
				queryIndex++
				queryIdMap[queryId] = queryMeta{resourceId: originalResourceID, metricName: metricName, stat: stat}
				// Get dimensions for the current originalResourceID
				currentDimensions := dimensionsByResourceID[originalResourceID]
				if currentDimensions == nil {
					// This shouldn't happen if finalResourceIDsForQuery is derived correctly
					ctx.GetLogger().Warn("No dimensions found for resource ID, skipping metric query", "resourceId", originalResourceID, "metric", metricName)
					continue
				}

				// Make a copy to add additional dimensions without modifying the shared map
				queryDimensions := make([]types.Dimension, len(currentDimensions))
				copy(queryDimensions, currentDimensions)

				if additionalDimensions, ok := serviceConfig.AdditionalDimensions[metricName]; ok {
					for key, value := range additionalDimensions {
						queryDimensions = append(queryDimensions, types.Dimension{
							Name:  aws.String(key),
							Value: aws.String(value),
						})
					}
				}

				queries = append(queries,
					types.MetricDataQuery{
						Id:         aws.String(queryId),
						AccountId:  aws.String(account.AccountNumber),
						ReturnData: aws.Bool(true),
						MetricStat: &types.MetricStat{
							Period: aws.Int32(int32(step.Seconds())),
							Stat:   aws.String(stat),
							Metric: &types.Metric{
								Namespace:  aws.String(targetNamespaceName),
								MetricName: aws.String(metricName),
								Dimensions: queryDimensions,
							},
						},
					},
				)
			}
		}
	}

	// get results
	metrics := []providers.MetricItem{}
	var token *string
	for {
		result, err := svc.GetMetricData(ctx.GetContext(), &cloudwatch.GetMetricDataInput{
			StartTime:         aws.Time(startTime),
			EndTime:           aws.Time(endTime),
			MetricDataQueries: queries,
			NextToken:         token,
		})

		if err != nil {
			jsonQuery, _ := common.MarshalJson(queries)
			ctx.GetLogger().Error("failed to fetch metrics", "error", err, "accountNumber", account.AccountNumber, "region", filter.Region, "query", string(jsonQuery))
			return providers.QueryMetricsResponse{}, err
		}

		for _, result := range result.MetricDataResults {
			meta, ok := queryIdMap[*result.Id]
			if !ok {
				ctx.GetLogger().Warn("Could not map query ID back to metric metadata", "id", *result.Id)
				continue
			}
			metrics = append(metrics, providers.MetricItem{
				Name:       meta.metricName,
				Values:     result.Values,
				ResourceId: meta.resourceId,
				Timestamps: result.Timestamps,
				Statistics: meta.stat,
			})
		}

		if result.NextToken == nil {
			break
		}
		token = result.NextToken
	}

	//combine data for services that have multiple metrics
	if len(filter.ResourceIds) == 0 {
		groupedResources := lo.GroupBy(metrics, func(item providers.MetricItem) string {
			return item.Name + "::" + item.Statistics
		})

		aggregatedMetrices := lo.MapToSlice(groupedResources, func(key string, items []providers.MetricItem) providers.MetricItem {
			keyParts := strings.Split(key, "::")
			resourceId := ""

			//merge timestamp and values
			timestampedDataArray := map[time.Time][]float64{}

			for _, item := range items {
				resourceId = item.ResourceId
				for i, timestamp := range item.Timestamps {
					if _, ok := timestampedDataArray[timestamp]; !ok {
						timestampedDataArray[timestamp] = []float64{}
					}
					timestampedDataArray[timestamp] = append(timestampedDataArray[timestamp], item.Values[i])
				}
			}

			timestampedData := map[time.Time]float64{}

			timestamps := []time.Time{}
			values := []float64{}
			if len(timestampedDataArray) > 0 {
				for timestamp, values := range timestampedDataArray {
					value := 0.0
					if f, ok := metricsFunctions[keyParts[1]]; ok {
						value = f(values)
					} else {
						ctx.GetLogger().Error("invalid statistics", "statistics", keyParts[1])
					}
					timestampedData[timestamp] = value
				}

				// sort timestamps and values in arrays
				timestamps = slices.Collect(maps.Keys(timestampedData))
				sort.Slice(timestamps, func(i, j int) bool {
					return timestamps[i].Before(timestamps[j])
				})
				for _, timestamp := range timestamps {
					values = append(values, timestampedData[timestamp])
				}
			}

			return providers.MetricItem{
				Name:        keyParts[0],
				Values:      values,
				Timestamps:  timestamps,
				Statistics:  keyParts[1],
				ResourceId:  resourceId,
				Region:      filter.Region,
				ServiceName: filter.ServiceName,
			}
		})

		metrics = aggregatedMetrices

	}

	return providers.QueryMetricsResponse{
		Items:     metrics,
		StartDate: startTime,
		EndDate:   endTime,
		Step:      step,
	}, nil
}
