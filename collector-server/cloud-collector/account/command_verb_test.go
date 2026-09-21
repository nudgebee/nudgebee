package account

import (
	"strings"
	"testing"
)

func TestCommandVerb(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    string
	}{
		// Real command shapes issued by the knowledge graph.
		{
			name:    "aws with filter argument",
			command: `aws ec2 describe-network-interfaces --filters "Name=addresses.private-ip-address,Values=10.0.0.1" --output json`,
			want:    "aws.ec2.describe-network-interfaces",
		},
		{
			name:    "aws route53 records",
			command: "aws route53 list-resource-record-sets --hosted-zone-id Z123 --output json",
			want:    "aws.route53.list-resource-record-sets",
		},
		{
			name:    "aws elbv2 with jmespath query",
			command: "aws elbv2 describe-load-balancers --region us-east-1 --query 'LoadBalancers[?DNSName==`x`].LoadBalancerArn' --output json",
			want:    "aws.elbv2.describe-load-balancers",
		},
		{
			name:    "aws rds no arguments",
			command: "aws rds describe-db-instances --output json",
			want:    "aws.rds.describe-db-instances",
		},
		{
			name:    "gcloud four-token verb",
			command: "gcloud compute backend-services list --global --format=json",
			want:    "gcloud.compute.backend-services.list",
		},
		{
			name:    "gcloud dns record-sets scoped to a zone",
			command: "gcloud dns record-sets list --zone=my-zone --format=json",
			want:    "gcloud.dns.record-sets.list",
		},
		{
			name:    "az resource show by arm id",
			command: `az resource show --ids '/subscriptions/000/resourceGroups/rg/providers/x' --output json`,
			want:    "az.resource.show",
		},
		{
			name:    "az network nic list",
			command: "az network nic list --output json",
			want:    "az.network.nic.list",
		},

		// Redaction: the verb must never carry the argument.
		{
			name:    "az login does not leak the client secret",
			command: "az login --service-principal -u 00000000-0000-0000-0000-000000000000 -p s3cr3t --tenant t",
			want:    "az.login",
		},
		{
			name:    "arn positional argument is not captured",
			command: "aws elbv2 describe-tags --resource-arns arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/app/x",
			want:    "aws.elbv2.describe-tags",
		},

		// Boundaries.
		{
			name:    "verb longer than the cap is truncated",
			command: "gcloud compute instances network-interfaces describe extra",
			want:    "gcloud.compute.instances.network-interfaces",
		},
		{
			name:    "empty command",
			command: "",
			want:    "unknown",
		},
		{
			name:    "whitespace only",
			command: "   \t  ",
			want:    "unknown",
		},
		{
			name:    "leading flag yields no verb",
			command: "--help",
			want:    "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CommandVerb(tt.command); got != tt.want {
				t.Errorf("CommandVerb(%q) = %q, want %q", tt.command, got, tt.want)
			}
		})
	}
}

// TestCommandVerbNeverLeaksSecrets guards the property the log line depends on:
// whatever the argument shape, nothing past the first flag reaches the verb.
func TestCommandVerbNeverLeaksSecrets(t *testing.T) {
	secrets := []string{
		"s3cr3t-client-secret",
		"arn:aws:iam::123456789012:role/admin",
		"/subscriptions/000/resourceGroups/rg",
		"10.0.0.1",
		"Z0123456789ABCDEFGHIJ",
	}

	commands := []string{
		"az login --service-principal -u id -p s3cr3t-client-secret",
		"aws sts assume-role --role-arn arn:aws:iam::123456789012:role/admin",
		"az resource show --ids /subscriptions/000/resourceGroups/rg",
		`aws ec2 describe-network-interfaces --filters "Name=addresses.private-ip-address,Values=10.0.0.1"`,
		"aws route53 list-resource-record-sets --hosted-zone-id Z0123456789ABCDEFGHIJ",
	}

	for _, command := range commands {
		verb := CommandVerb(command)
		for _, secret := range secrets {
			if strings.Contains(verb, secret) {
				t.Errorf("CommandVerb(%q) = %q, leaked %q", command, verb, secret)
			}
		}
	}
}
