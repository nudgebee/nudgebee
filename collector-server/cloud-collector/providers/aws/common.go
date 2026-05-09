package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"nudgebee/collector/cloud/common"
	"nudgebee/collector/cloud/providers"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// GetAwsConfigFromAccount creates an AWS config for the given account, handling
// static credentials, cross-account role assumption, and profile-based auth.
func GetAwsConfigFromAccount(ctx context.Context, account providers.Account) (aws.Config, error) {
	return getAwsConfigFromAccount(ctx, account)
}

func getAwsConfigFromAccount(ctx context.Context, account providers.Account) (aws.Config, error) {
	region := "us-east-1"
	if account.Region != nil {
		region = *account.Region
	}

	opts := []func(*config.LoadOptions) error{
		config.WithRegion(region),
	}

	// Allow AWS profile to be configured via environment variable
	// Falls back to default profile if not set
	if profile := os.Getenv("AWS_PROFILE"); profile != "" {
		opts = append(opts, config.WithSharedConfigProfile(profile))
	} else if profile := os.Getenv("CLOUD_COLLECTOR_AWS_PROFILE"); profile != "" {
		// Also support a cloud-collector specific env var to avoid conflicts
		opts = append(opts, config.WithSharedConfigProfile(profile))
	}

	if account.AccessKey != nil && account.AccessSecret != nil && *account.AccessSecret != "" {
		decryptedAccessSecret, err := common.Decrypt(*account.AccessSecret)
		if err != nil {
			return aws.Config{}, err
		}
		creds := credentials.NewStaticCredentialsProvider(*account.AccessKey, decryptedAccessSecret, "")
		opts = append(opts, config.WithCredentialsProvider(creds))
	} else if account.AssumeRole != nil {
		baseCfg, err := config.LoadDefaultConfig(ctx, opts...)
		if err != nil {
			return aws.Config{}, err
		}
		stsClient := sts.NewFromConfig(baseCfg)
		assumeRoleProvider := stscreds.NewAssumeRoleProvider(stsClient, *account.AssumeRole, func(o *stscreds.AssumeRoleOptions) {
			o.RoleSessionName = "nudgebee-cloud-collector"
		})
		opts = append(opts, config.WithCredentialsProvider(aws.NewCredentialsCache(assumeRoleProvider)))
	}

	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return aws.Config{}, err
	}

	// Attach permission audit middleware if audit context is present
	if info := getPermAuditInfo(ctx); info != nil {
		cfg.APIOptions = append(cfg.APIOptions, addPermissionAuditMiddleware(info))
	}

	return cfg, nil
}

func isPublicSubnet(ctx context.Context, cfg aws.Config, subnetId string) bool {
	client := ec2.NewFromConfig(cfg)
	input := &ec2.DescribeSubnetsInput{
		SubnetIds: []string{subnetId},
	}
	result, err := client.DescribeSubnets(ctx, input)
	if err != nil {
		return false
	}
	if len(result.Subnets) == 0 {
		return false
	}
	return aws.ToBool(result.Subnets[0].MapPublicIpOnLaunch)
}

func structToMap(v any) map[string]any {
	data, err := json.Marshal(v)
	if err != nil {
		slog.Warn("structToMap: marshal failed", "error", err, "type", fmt.Sprintf("%T", v))
		return map[string]any{}
	}
	if len(data) == 0 || string(data) == "null" {
		return map[string]any{}
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		slog.Warn("structToMap: unmarshal failed", "error", err, "type", fmt.Sprintf("%T", v))
		return map[string]any{}
	}
	return result
}

const (
	ServiceNameCloudTrail       = "AWSCloudTrail"
	ServiceNameCloudWatch       = "AmazonCloudWatch"
	ServiceNameCodeArtifact     = "AWSCodeArtifact"
	ServiceNameEc2              = "AmazonEC2"
	ServiceNameECRPublic        = "AmazonECRPublic"
	ServiceNameECR              = "AmazonECR"
	ServiceNameEKS              = "AmazonEKS"
	ServiceNameELB              = "AWSELB"
	ServiceNameKMS              = "AWSKMS"
	ServiceNameLambda           = "AWSLambda"
	ServiceNameRDS              = "AmazonRDS"
	ServiceNameS3               = "AmazonS3"
	ServiceNameSecretsManager   = "AWSSecretsManager"
	ServiceNameSES              = "AmazonSES"
	ServiceNameSNS              = "AmazonSNS"
	ServiceNameSQS              = "AWSQueueService"
	ServiceNameVPC              = "AmazonVPC"
	ServiceNameElastiCache      = "AmazonElastiCache"
	ServiceNameSecurityHub      = "AWSSecurityHub"
	ServiceNameMSK              = "AmazonMSK"
	ServiceNameCloudFormation   = "AWSCloudFormation"
	ServiceNameAutoScaling      = "AutoScaling"
	ServiceNameCFM              = "AWSCFM"
	ServiceNameIAM              = "AWSIAM"
	ServiceNameRoute53          = "AmazonRoute53"
	ServiceNameEventBridge      = "AmazonEventBridge"
	ServiceNameQ                = "AmazonQ"
	ServiceNameSSM              = "AWSSSM"
	ServiceNameGlue             = "AWSGlue"
	ServiceNameCloudShell       = "AWSCloudShell"
	ServiceNameMigrationHub     = "AWSMigrationHubRefactorSpaces"
	ServiceNameLocationService  = "AmazonLocationService"
	ServiceNameSavingPlan       = "ComputeSavingsPlans"
	ServiceNameRedshift         = "AmazonRedshift"
	ServiceNameDataTransfer     = "AWSDataTransfer"
	ServiceNameBackup           = "AWSBackup"
	ServiceNameBedrock          = "AmazonBedrock"
	ServiceNameEFS              = "AmazonEFS"
	ServiceNameES               = "AmazonES"
	ServiceNameSageMaker        = "AmazonSageMaker"
	ServiceNameCloudFront       = "AmazonCloudFront"
	ServiceNameECS              = "AmazonECS"
	ServiceNameFargate          = "AWSFargate"
	ServiceNameDynamoDB         = "AmazonDynamoDB"
	ServiceNameGuardDuty        = "AmazonGuardDuty"
	ServiceNameXray             = "AWSXRay"
	ServiceNameElasticBeanstalk = "AWSElasticBeanstalk"
	ServiceNameStepFunctions    = "AWSStepFunctions"
	ServiceNameDirectConnect    = "AWSDirectConnect"
	ServiceNameWAF              = "AWSWAF"
	ServiceNameInspector        = "AmazonInspector"
	ServiceNameConfig           = "AWSConfig"
	ServiceNameSystemsManager   = "AWSSystemsManager"
	ServiceNameRoute53Domains   = "AmazonRoute53Domains"
)

var serviceResourceTypeMap = map[string]map[string]string{
	ServiceNameCloudTrail:     {"trail": "trail", "eventdatastore": "eventdatastore"},
	ServiceNameCloudWatch:     {"alarm": "alarm", "log-group": "log-group", "metric": "metric", "dashboard": "dashboard"},
	ServiceNameCodeArtifact:   {"repository": "repository"},
	ServiceNameEc2:            {"compute-instance": "compute-instance", "instance": "compute-instance", "subnet": "subnect", "vpc": "vpc", "security_group": "security_group", "key_pair": "key_pair", "snapshot": "snapshot", "volume": "storage", "storage": "storage", "networkinterface": "network-interface", "network-interface": "network-interface"},
	ServiceNameECRPublic:      {"repository": "repository"},
	ServiceNameECR:            {"repository": "repository"},
	ServiceNameEKS:            {"cluster": "cluster"},
	ServiceNameELB:            {"load_balancer": "loadbalancer", "loadbalancer": "loadbalancer", "application_loadbalancer": "application_loadbalancer", "network_loadbalancer": "network_loadbalancer", "gateway_loadbalancer": "gateway_loadbalancer"},
	ServiceNameKMS:            {"key": "key"},
	ServiceNameLambda:         {"function": "function"},
	ServiceNameRDS:            {"instance": "db", "db": "db"},
	ServiceNameS3:             {"bucket": "storage", "storage": "storage"},
	ServiceNameSecretsManager: {"secret": "secret"},
	ServiceNameSES:            {"email": "email", "identity": "identity", "configuration": "configuration"},
	ServiceNameSNS:            {"topic": "topic"},
	ServiceNameSQS:            {"queue": "queue"},
	ServiceNameVPC:            {"subnet": "subnet", "vpc": "vpc", "security_group": "security_group"},
	ServiceNameElastiCache:    {"cluster": "cluster"},
	ServiceNameMSK:            {"cluster": "cluster"},
	ServiceNameECS:            {"cluster": "cluster", "service": "service", "task": "task"},
	ServiceNameFargate:        {"service": "service", "task": "task"},
}

func getAwsServiceResourceType(serviceName string, resourceType string) string {
	if resourceTypeMap, ok := serviceResourceTypeMap[serviceName]; ok {
		if resourceType, ok := resourceTypeMap[resourceType]; ok {
			return resourceType
		}
	}
	return resourceType
}

// averageFloat64 calculates the average of a slice of float64 values.
// Returns 0 if the slice is empty.
func averageFloat64(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}
