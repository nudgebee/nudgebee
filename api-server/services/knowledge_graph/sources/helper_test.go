package sources

import (
	"testing"
)

func TestGetResourceIDFromARN(t *testing.T) {
	tests := []struct {
		name     string
		arn      string
		expected string
	}{
		{
			name:     "EC2 instance with slash separator",
			arn:      "arn:aws:ec2:us-east-1:123456789012:instance/i-1234567890abcdef0",
			expected: "i-1234567890abcdef0",
		},
		{
			name:     "RDS database with colon separator",
			arn:      "arn:aws:rds:us-east-1:123456789012:db:my-database",
			expected: "my-database",
		},
		{
			name:     "S3 bucket without resource type",
			arn:      "arn:aws:s3:::my-bucket",
			expected: "my-bucket",
		},
		{
			name:     "Load balancer with complex path",
			arn:      "arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/app/my-lb/50dc6c495c0c9188",
			expected: "app/my-lb/50dc6c495c0c9188",
		},
		{
			name:     "VPC with slash separator",
			arn:      "arn:aws:ec2:us-east-1:123456789012:vpc/vpc-12345678",
			expected: "vpc-12345678",
		},
		{
			name:     "Security group with slash separator",
			arn:      "arn:aws:ec2:us-east-1:123456789012:security-group/sg-12345678",
			expected: "sg-12345678",
		},
		{
			name:     "Lambda function with colon separator",
			arn:      "arn:aws:lambda:us-east-1:123456789012:function:my-function",
			expected: "my-function",
		},
		{
			name:     "DynamoDB table with slash separator",
			arn:      "arn:aws:dynamodb:us-east-1:123456789012:table/my-table",
			expected: "my-table",
		},
		{
			name:     "EKS cluster with slash separator",
			arn:      "arn:aws:eks:us-east-1:123456789012:cluster/my-cluster",
			expected: "my-cluster",
		},
		{
			name:     "ElastiCache cluster with colon separator",
			arn:      "arn:aws:elasticache:us-east-1:123456789012:cluster:my-cache-cluster",
			expected: "my-cache-cluster",
		},
		{
			name:     "NAT Gateway with slash separator",
			arn:      "arn:aws:ec2:us-east-1:123456789012:natgateway/nat-12345678",
			expected: "nat-12345678",
		},
		{
			name:     "Empty ARN",
			arn:      "",
			expected: "",
		},
		{
			name:     "Invalid ARN with too few parts",
			arn:      "arn:aws:ec2",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetResourceIDFromARN(tt.arn)
			if result != tt.expected {
				t.Errorf("GetResourceIDFromARN(%q) = %q, expected %q", tt.arn, result, tt.expected)
			}
		})
	}
}

func TestGetResourceTypeFromARN(t *testing.T) {
	tests := []struct {
		name     string
		arn      string
		expected string
	}{
		{
			name:     "EC2 instance",
			arn:      "arn:aws:ec2:us-east-1:123456789012:instance/i-1234567890abcdef0",
			expected: "instance",
		},
		{
			name:     "RDS database",
			arn:      "arn:aws:rds:us-east-1:123456789012:db:my-database",
			expected: "db",
		},
		{
			name:     "S3 bucket (no resource type)",
			arn:      "arn:aws:s3:::my-bucket",
			expected: "",
		},
		{
			name:     "Load balancer",
			arn:      "arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/app/my-lb/50dc6c495c0c9188",
			expected: "loadbalancer",
		},
		{
			name:     "VPC",
			arn:      "arn:aws:ec2:us-east-1:123456789012:vpc/vpc-12345678",
			expected: "vpc",
		},
		{
			name:     "Lambda function",
			arn:      "arn:aws:lambda:us-east-1:123456789012:function:my-function",
			expected: "function",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetResourceTypeFromARN(tt.arn)
			if result != tt.expected {
				t.Errorf("GetResourceTypeFromARN(%q) = %q, expected %q", tt.arn, result, tt.expected)
			}
		})
	}
}

func TestGetServiceFromARN(t *testing.T) {
	tests := []struct {
		name     string
		arn      string
		expected string
	}{
		{
			name:     "EC2 service",
			arn:      "arn:aws:ec2:us-east-1:123456789012:instance/i-1234567890abcdef0",
			expected: "ec2",
		},
		{
			name:     "RDS service",
			arn:      "arn:aws:rds:us-east-1:123456789012:db:my-database",
			expected: "rds",
		},
		{
			name:     "S3 service",
			arn:      "arn:aws:s3:::my-bucket",
			expected: "s3",
		},
		{
			name:     "Lambda service",
			arn:      "arn:aws:lambda:us-east-1:123456789012:function:my-function",
			expected: "lambda",
		},
		{
			name:     "Empty ARN",
			arn:      "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetServiceFromARN(tt.arn)
			if result != tt.expected {
				t.Errorf("GetServiceFromARN(%q) = %q, expected %q", tt.arn, result, tt.expected)
			}
		})
	}
}

func TestGetRegionFromARN(t *testing.T) {
	tests := []struct {
		name     string
		arn      string
		expected string
	}{
		{
			name:     "EC2 in us-east-1",
			arn:      "arn:aws:ec2:us-east-1:123456789012:instance/i-1234567890abcdef0",
			expected: "us-east-1",
		},
		{
			name:     "RDS in eu-west-1",
			arn:      "arn:aws:rds:eu-west-1:123456789012:db:my-database",
			expected: "eu-west-1",
		},
		{
			name:     "S3 (no region)",
			arn:      "arn:aws:s3:::my-bucket",
			expected: "",
		},
		{
			name:     "Lambda in ap-southeast-1",
			arn:      "arn:aws:lambda:ap-southeast-1:123456789012:function:my-function",
			expected: "ap-southeast-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetRegionFromARN(tt.arn)
			if result != tt.expected {
				t.Errorf("GetRegionFromARN(%q) = %q, expected %q", tt.arn, result, tt.expected)
			}
		})
	}
}

func TestGetAccountIDFromARN(t *testing.T) {
	tests := []struct {
		name     string
		arn      string
		expected string
	}{
		{
			name:     "EC2 with account ID",
			arn:      "arn:aws:ec2:us-east-1:123456789012:instance/i-1234567890abcdef0",
			expected: "123456789012",
		},
		{
			name:     "RDS with account ID",
			arn:      "arn:aws:rds:us-east-1:987654321098:db:my-database",
			expected: "987654321098",
		},
		{
			name:     "S3 (no account ID)",
			arn:      "arn:aws:s3:::my-bucket",
			expected: "",
		},
		{
			name:     "Lambda with account ID",
			arn:      "arn:aws:lambda:us-east-1:555555555555:function:my-function",
			expected: "555555555555",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetAccountIDFromARN(tt.arn)
			if result != tt.expected {
				t.Errorf("GetAccountIDFromARN(%q) = %q, expected %q", tt.arn, result, tt.expected)
			}
		})
	}
}

func TestExtractEndpointAddress(t *testing.T) {
	tests := []struct {
		name     string
		input    interface{}
		expected string
	}{
		{"nil", nil, ""},
		{"bare string", "my-cache.region.cache.amazonaws.com", "my-cache.region.cache.amazonaws.com"},
		{"empty string", "", ""},
		{"object with Address", map[string]interface{}{"Address": "primary.region.cache.amazonaws.com", "Port": 6379.0}, "primary.region.cache.amazonaws.com"},
		{"object without Address", map[string]interface{}{"Port": 6379.0}, ""},
		{"object with non-string Address", map[string]interface{}{"Address": 123}, ""},
		{"unsupported type", 42, ""},
		{"slice", []interface{}{"x"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractEndpointAddress(tt.input)
			if got != tt.expected {
				t.Errorf("extractEndpointAddress(%v) = %q, expected %q", tt.input, got, tt.expected)
			}
		})
	}
}
