package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsShellSyntax(t *testing.T) {
	tests := []struct {
		name     string
		command  string
		expected bool
	}{
		// Legitimate AWS CLI commands — should NOT be flagged
		{
			name:     "simple aws command",
			command:  "aws ec2 describe-instances --region us-east-1",
			expected: false,
		},
		{
			name:     "JMESPath query with && and ||",
			command:  `aws cloudwatch describe-alarms --region eu-west-1 --query 'MetricAlarms[?Dimensions[?Name==` + "`InstanceId`" + ` && Value==` + "`i-09c7ef246af8d88aa`" + `] && (MetricName==` + "`StatusCheckFailed`" + ` || MetricName==` + "`StatusCheckFailed_Instance`" + `)].[AlarmName, MetricName, StateValue]' --output table`,
			expected: false,
		},
		{
			name:     "JMESPath with double-quoted string containing &&",
			command:  `aws ec2 describe-instances --query "Reservations[*].Instances[?State.Name=='running' && Tags[?Key=='Env']]"`,
			expected: false,
		},
		{
			name:     "JMESPath with || in single quotes",
			command:  `aws ec2 describe-instances --query 'Reservations[*].Instances[?State.Name==` + "`running`" + ` || State.Name==` + "`stopped`" + `]'`,
			expected: false,
		},
		{
			name:     "simple filter with single quotes",
			command:  `aws ec2 describe-instances --filters "Name=tag:Environment,Values=production"`,
			expected: false,
		},

		// Shell syntax — SHOULD be flagged
		{
			name:     "command chaining with &&",
			command:  "aws s3 ls && aws ec2 describe-instances",
			expected: true,
		},
		{
			name:     "command chaining with ||",
			command:  "aws s3 ls || echo failed",
			expected: true,
		},
		{
			name:     "command substitution with $()",
			command:  "aws ec2 describe-instances --instance-ids $(aws ec2 describe-instances --query 'Reservations[*].Instances[*].InstanceId' --output text)",
			expected: true,
		},
		{
			name:     "for loop",
			command:  "for i in 1 2 3; do aws s3 ls s3://bucket-$i; done",
			expected: true,
		},
		{
			name:     "while loop",
			command:  "while true; do aws sqs receive-message --queue-url q; done",
			expected: true,
		},
		{
			name:     "if then conditional",
			command:  "if aws s3 ls s3://bucket; then echo exists; fi",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isShellSyntax(tt.command)
			assert.Equal(t, tt.expected, result, "command: %s", tt.command)
		})
	}
}

func TestStripQuotedContent(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "no quotes",
			input:    "aws ec2 describe-instances",
			expected: "aws ec2 describe-instances",
		},
		{
			name:     "single-quoted content stripped",
			input:    "aws --query 'foo && bar'",
			expected: "aws --query ''",
		},
		{
			name:     "double-quoted content stripped",
			input:    `aws --query "foo && bar"`,
			expected: `aws --query ""`,
		},
		{
			name:     "mixed quotes",
			input:    `aws --query 'foo && bar' --filter "baz || qux"`,
			expected: `aws --query '' --filter ""`,
		},
		{
			name:     "unquoted && preserved",
			input:    "aws s3 ls && aws ec2 list",
			expected: "aws s3 ls && aws ec2 list",
		},
		{
			name:     "escaped quote in double quotes",
			input:    `aws --query "foo \"&&\" bar"`,
			expected: `aws --query ""`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := stripQuotedContent(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}
