package aws

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	logstypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	"github.com/stretchr/testify/assert"
)

func TestLogGroupTaggingArn(t *testing.T) {
	const (
		withSuffix    = "arn:aws:logs:us-east-1:123456789012:log-group:vpc-flow-logs:*"
		withoutSuffix = "arn:aws:logs:us-east-1:123456789012:log-group:vpc-flow-logs"
	)
	tests := []struct {
		name     string
		logGroup logstypes.LogGroup
		want     string
	}{
		{
			name:     "uses LogGroupArn, the field AWS documents for the tagging APIs",
			logGroup: logstypes.LogGroup{Arn: aws.String(withSuffix), LogGroupArn: aws.String(withoutSuffix)},
			want:     withoutSuffix,
		},
		{
			name:     "strips the trailing :* that ListTagsForResource rejects",
			logGroup: logstypes.LogGroup{Arn: aws.String(withSuffix)},
			want:     withoutSuffix,
		},
		{
			name:     "an Arn without the suffix is used as it is",
			logGroup: logstypes.LogGroup{Arn: aws.String(withoutSuffix)},
			want:     withoutSuffix,
		},
		{
			name:     "an empty LogGroupArn falls back to Arn",
			logGroup: logstypes.LogGroup{Arn: aws.String(withSuffix), LogGroupArn: aws.String("")},
			want:     withoutSuffix,
		},
		{
			name:     "a log group name containing :* is only trimmed at the end",
			logGroup: logstypes.LogGroup{Arn: aws.String("arn:aws:logs:us-east-1:123456789012:log-group:a:*b:*")},
			want:     "arn:aws:logs:us-east-1:123456789012:log-group:a:*b",
		},
		{
			name:     "no ARN at all",
			logGroup: logstypes.LogGroup{},
			want:     "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, logGroupTaggingArn(tt.logGroup))
		})
	}
}
