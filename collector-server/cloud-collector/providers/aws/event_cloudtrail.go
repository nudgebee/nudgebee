package aws

import (
	"nudgebee/collector/cloud/common"
	"nudgebee/collector/cloud/providers"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/cloudtrail/types"
)

type eventResourceDetail struct {
	ResourceType string `json:"resource_type"`
	EventName    string `json:"event_name"`
	ServiceName  string `json:"service_name"`
	Region       string `json:"region"`
	ResourceId   string `json:"resource_id"`
	Raw          map[string]any
}

var eventSourceServiceMap = map[string]string{
	"ec2.amazonaws.com":                   ServiceNameEc2,
	"s3.amazonaws.com":                    ServiceNameS3,
	"rds.amazonaws.com":                   ServiceNameRDS,
	"ecr.amazonaws.com":                   ServiceNameECR,
	"cloudtrail.amazonaws.com":            ServiceNameCloudTrail,
	"kms.amazonaws.com":                   ServiceNameKMS,
	"lambda.amazonaws.com":                ServiceNameLambda,
	"sns.amazonaws.com":                   ServiceNameSNS,
	"ses.amazonaws.com":                   ServiceNameSES,
	"sqs.amazonaws.com":                   ServiceNameSQS,
	"ecr-public.amazonaws.com":            ServiceNameECRPublic,
	"vpc.amazonaws.com":                   ServiceNameVPC,
	"elasticache.amazonaws.com":           ServiceNameElastiCache,
	"cloudwatch.amazonaws.com":            ServiceNameCloudWatch,
	"logs.amazonaws.com":                  ServiceNameCloudWatch,
	"monitoring.amazonaws.com":            ServiceNameCloudWatch,
	"eks.amazonaws.com":                   ServiceNameEKS,
	"codeartifact.amazonaws.com":          ServiceNameCodeArtifact,
	"elb.amazonaws.com":                   ServiceNameELB,
	"secretsmanager.amazonaws.com":        ServiceNameSecretsManager,
	"cloudformation.amazonaws.com":        ServiceNameCloudFormation,
	"autoscaling.amazonaws.com":           ServiceNameAutoScaling,
	"securityhub.amazonaws.com":           ServiceNameSecurityHub,
	"elasticloadbalancing.amazonaws.com":  ServiceNameELB,
	"iam.amazonaws.com":                   ServiceNameIAM,
	"route53.amazonaws.com":               ServiceNameRoute53,
	"events.amazonaws.com":                ServiceNameEventBridge,
	"pipes.amazonaws.com":                 ServiceNameEventBridge,
	"q.amazonaws.com":                     ServiceNameQ,
	"cur.amazonaws.com":                   ServiceNameCFM,
	"cost-optimization-hub.amazonaws.com": ServiceNameCFM,
	"savingsplans.amazonaws.com":          ServiceNameCFM,
	"ssm.amazonaws.com":                   ServiceNameSSM,
	"ecs.amazonaws.com":                   ServiceNameECS,
	"xray.amazonaws.com":                  ServiceNameXray,
	"dynamodb.amazonaws.com":              ServiceNameDynamoDB,
	"backup.amazonaws.com":                ServiceNameBackup,
	"guardduty.amazonaws.com":             ServiceNameGuardDuty,
	"msk.amazonaws.com":                   ServiceNameMSK,
	"sagemaker.amazonaws.com":             ServiceNameSageMaker,
	"redshift.amazonaws.com":              ServiceNameRedshift,
	"es.amazonaws.com":                    ServiceNameES,
	"efs.amazonaws.com":                   ServiceNameEFS,
	"bedrock.amazonaws.com":               ServiceNameBedrock,
	"queueservice.amazonaws.com":          ServiceNameSQS,
}

var serviceResourceRefereshEvents = map[string][]string{
	ServiceNameEc2: {
		"RunInstances",
		"TerminateInstances",
		"StartInstances",
		"StopInstances",
		"RebootInstances",
		"CreateVolume",
		"DeleteVolume",
	},
	ServiceNameRDS: {
		"CreateDBInstance",
		"DeleteDBInstance",
	},
	ServiceNameS3: {
		"CreateBucket",
		"DeleteBucket",
	},
	ServiceNameLambda: {
		"CreateFunction",
		"DeleteFunction",
	},
	ServiceNameECR: {
		"CreateRepository",
		"DeleteRepository",
	},
	ServiceNameSNS: {
		"CreateTopic",
		"DeleteTopic",
	},
	ServiceNameSES: {
		"CreateConfigurationSet",
		"DeleteConfigurationSet",
	},
	ServiceNameSQS: {
		"CreateQueue",
		"DeleteQueue",
	},
	ServiceNameECRPublic: {
		"CreateRepository",
		"DeleteRepository",
	},
	ServiceNameVPC: {
		"CreateVpc",
		"DeleteVpc",
	},
	ServiceNameELB: {
		"CreateLoadBalancer",
		"DeleteLoadBalancer",
	},
	ServiceNameKMS: {
		"CreateKey",
		"DeleteKey",
	},
	ServiceNameSecretsManager: {
		"CreateSecret",
		"DeleteSecret",
	},
	ServiceNameCloudWatch: {
		"CreateAlarm",
		"DeleteAlarm",
	},
	ServiceNameEKS: {
		"CreateCluster",
		"DeleteCluster",
	},
	ServiceNameCodeArtifact: {
		"CreateRepository",
		"DeleteRepository",
	},
	ServiceNameECS: {
		"CreateCluster",
		"DeleteCluster",
		"CreateService",
		"DeleteService",
		"CreateTaskDefinition",
		"DeleteTaskDefinition",
		"UpdateService",
		"UpdateTaskDefinition",
		"RegisterTaskDefinition",
		"DeregisterTaskDefinition",
	},
	ServiceNameXray: {
		"CreateSamplingRule",
		"DeleteSamplingRule",
	},
	ServiceNameDynamoDB: {
		"CreateTable",
		"DeleteTable",
	},
}

var managementEventsToExclude = []string{
	"CompleteLayerUpload",
	"CreateLayer",
	"DeleteLayer",
	"GetDownloadUrlForLayer",
	"InitiateLayerUpload",
	"UploadLayerPart",
	"RetireGrant",
	"CreateSnapshot",
	"DeleteSnapshot",
	"DeleteNetworkInterface",
	"PutImage",
	"ModifyNetworkInterfaceAttribute",
	"AttachNetworkInterface",
	"CreateNetworkInterface",
	"DetachNetworkInterface",
	"CreateTags",
	"CreateGrant",
	"ChangePassword",
	"ConsoleLogin",
	"CheckMfa",
	"CreateAnalyzer",
	"DeleteLaunchTemplate",
	"CreateLaunchTemplate",
	"DeregisterInstancesFromLoadBalancer",
	"DeleteTags",
	"RegisterInstancesWithLoadBalancer",
	"SharedSnapshotVolumeCreated",
	"CreateFleet",
	"AssignPrivateIpAddresses",
	"RegisterManagedInstance",
	"StartConversation",
	"SendMessage",
	"StartQuery",
	"GetAllowedPaymentMethods",
	"GetPaymentPreference",
}

func getServiceDetailsFromCloudTrailEvent(ctx providers.CloudProviderContext, trailEvent types.Event) eventResourceDetail {
	event := map[string]any{}
	err := common.UnmarshalJson([]byte(*trailEvent.CloudTrailEvent), &event)
	if err != nil {
		ctx.GetLogger().Error("error parsing event", "error", err.Error())
		return eventResourceDetail{}
	}

	resourceRegion := "global"
	if event["awsRegion"] != nil {
		resourceRegion = event["awsRegion"].(string)
	}
	eventName := event["eventName"].(string)
	eventSource := event["eventSource"].(string)
	serviceName := eventSource
	if svc, ok := eventSourceServiceMap[eventSource]; ok {
		serviceName = svc
	}
	resourceType := ""
	resourceId := ""

	for _, r := range trailEvent.Resources {
		if r.ResourceName != nil {
			resourceId = *r.ResourceName
		}
		if r.ResourceType != nil {
			splits := strings.Split(*r.ResourceType, "::")
			resourceType = splits[len(splits)-1]
		}
		if resourceId != "" && resourceType != "" {
			break
		}
	}

	resourceType = strings.ToLower(resourceType)
	resourceType = getAwsServiceResourceType(serviceName, resourceType)

	// Fallback lookups when resourceId is empty (e.g. management events that don't
	// populate the Resources array). Try common request parameter fields.
	if resourceId == "" {
		if requestParameters, ok := event["requestParameters"].(map[string]any); ok {
			resourceIdFields := []string{
				"instanceId", "InstanceId",
				"bucketName",
				"functionName",
				"dbInstanceIdentifier",
				"clusterName",
				"groupId", "groupName",
				"vpcId",
				"subnetId",
				"networkInterfaceId",
				"volumeId",
				"imageId",
				"snapshotId",
				"loadBalancerName",
				"targetGroupArn",
				"topicArn",
				"queueUrl",
			}
			for _, field := range resourceIdFields {
				if val, ok := requestParameters[field]; ok {
					if str, ok := val.(string); ok && str != "" {
						resourceId = str
						break
					}
				}
			}
		}
	}

	event["cloudtrail_resources"] = trailEvent.Resources
	event["cloudtrail_username"] = ""
	if trailEvent.Username != nil {
		event["cloudtrail_source"] = *trailEvent.EventSource
	}

	return eventResourceDetail{
		ResourceType: resourceType,
		EventName:    eventName,
		ServiceName:  serviceName,
		Region:       resourceRegion,
		ResourceId:   resourceId,
		Raw:          event,
	}
}
