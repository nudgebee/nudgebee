package aws

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEnrichCloudWatchAlarm_LookupIds(t *testing.T) {
	ctx := context.Background()
	alarm := func(namespace string, dims ...AlarmDimension) CloudWatchAlarmInfo {
		return CloudWatchAlarmInfo{
			AlarmName:       "an-alarm",
			MetricNamespace: namespace,
			Region:          "us-east-1",
			AccountNumber:   "111122223333",
			AlarmArn:        "arn:aws:cloudwatch:us-east-1:111122223333:alarm:an-alarm",
			StateValue:      "ALARM",
			Dimensions:      dims,
		}
	}

	t.Run("application load balancer alarm carries the load balancer ARN", func(t *testing.T) {
		got := EnrichCloudWatchAlarm(ctx, alarm("AWS/ApplicationELB", AlarmDimension{Name: "LoadBalancer", Value: "app/web/abc123"}), false, nil, nil)
		assert.Equal(t, "app/web/abc123", got.ResourceId, "the subject stays the dimension")
		assert.Equal(t, []string{"arn:aws:elasticloadbalancing:us-east-1:111122223333:loadbalancer/app/web/abc123"}, got.ResourceLookupIds)
	})

	t.Run("load balancer ARN keeps the alarm's partition", func(t *testing.T) {
		info := alarm("AWS/NetworkELB", AlarmDimension{Name: "LoadBalancer", Value: "net/edge/def456"})
		info.Region = "us-gov-west-1"
		info.AlarmArn = "arn:aws-us-gov:cloudwatch:us-gov-west-1:111122223333:alarm:an-alarm"
		got := EnrichCloudWatchAlarm(ctx, info, false, nil, nil)
		assert.Equal(t, []string{"arn:aws-us-gov:elasticloadbalancing:us-gov-west-1:111122223333:loadbalancer/net/edge/def456"}, got.ResourceLookupIds)
	})

	t.Run("classic load balancer gets no ARN", func(t *testing.T) {
		got := EnrichCloudWatchAlarm(ctx, alarm("AWS/ELB", AlarmDimension{Name: "LoadBalancerName", Value: "legacy"}), false, nil, nil)
		assert.Empty(t, got.ResourceLookupIds)
	})

	t.Run("ECS service alarm is about the service", func(t *testing.T) {
		got := EnrichCloudWatchAlarm(ctx, alarm("AWS/ECS",
			AlarmDimension{Name: "ClusterName", Value: "prod"},
			AlarmDimension{Name: "ServiceName", Value: "checkout"}), false, nil, nil)
		assert.Equal(t, "prod/checkout", got.ResourceId)
		assert.Equal(t, "service", got.ResourceType)
		assert.Equal(t, []string{"checkout"}, got.ResourceLookupIds)
	})

	t.Run("ECS cluster alarm is still about the cluster", func(t *testing.T) {
		got := EnrichCloudWatchAlarm(ctx, alarm("AWS/ECS", AlarmDimension{Name: "ClusterName", Value: "prod"}), false, nil, nil)
		assert.Equal(t, "prod", got.ResourceId)
		assert.Equal(t, "cluster", got.ResourceType)
		assert.Empty(t, got.ResourceLookupIds)
	})

	t.Run("FSx alarm is an FSx file system, not EFS", func(t *testing.T) {
		got := EnrichCloudWatchAlarm(ctx, alarm("AWS/FSx",
			AlarmDimension{Name: "FileSystemId", Value: "fs-0123"},
			AlarmDimension{Name: "VolumeId", Value: "fsvol-0456"}), false, nil, nil)
		assert.Equal(t, "fs-0123", got.ResourceId)
		assert.Equal(t, ServiceNameFSx, got.ResourceServiceName)
		assert.Equal(t, "file-system", got.ResourceType)
	})
}
