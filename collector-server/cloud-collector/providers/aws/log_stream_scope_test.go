package aws

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEc2LogStreamPrefix(t *testing.T) {
	ec2 := &amazonEc2{}
	assert.Equal(t, "i-0123456789abcdef0", ec2.LogStreamPrefix("i-0123456789abcdef0"))
	assert.Equal(t, "i-0abc1234", ec2.LogStreamPrefix("i-0abc1234"))
	// Anything that is not a plain instance id stays unscoped: it would be
	// interpolated into an Insights regex.
	for _, id := range []string{"", "tag:Name=web", "i-/.*", "arn:aws:ec2:us-east-1:123456789012:instance/i-0abc1234", "I-0ABC1234"} {
		assert.Empty(t, ec2.LogStreamPrefix(id), id)
	}
}

func TestLogStreamPrefixThroughAuditWrapper(t *testing.T) {
	// GetAwsService hands out the audit wrapper, so the scoping must survive it.
	wrappedEc2 := &auditedAwsService{inner: &amazonEc2{}, serviceName: "ec2"}
	assert.Equal(t, "i-0abc1234", wrappedEc2.LogStreamPrefix("i-0abc1234"))

	// A service with a dedicated log group per resource is not scoped.
	wrappedLambda := &auditedAwsService{inner: &awsLambda{}, serviceName: "lambda"}
	assert.Empty(t, wrappedLambda.LogStreamPrefix("i-0abc1234"))

	svc, ok := GetAwsService("AmazonEC2")
	if assert.True(t, ok) {
		scoper, ok := svc.(logStreamScoper)
		if assert.True(t, ok) {
			assert.Equal(t, "i-0abc1234", scoper.LogStreamPrefix("i-0abc1234"))
		}
	}
}

func TestScopeQueryToStreamPrefix(t *testing.T) {
	got := scopeQueryToStreamPrefix("fields @timestamp, @message | stats count(*) by bin(5m) | limit 20", "i-0abc1234")
	// The filter runs first, so a query ending in stats or limit still sees @logStream.
	assert.Equal(t, "filter @logStream like /^i-0abc1234/\n| fields @timestamp, @message | stats count(*) by bin(5m) | limit 20", got)
}
