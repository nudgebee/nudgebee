package core

import "testing"

// TestELBV2LoadBalancerName is the regression for a load-balancer alarm never
// resolving to its graph node. CloudWatch scopes the metric by the LoadBalancer
// dimension — "app/<name>/<id>" — and that is the only identifier the alarm
// carries, while the node is keyed by "<name>".
func TestELBV2LoadBalancerName(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		// The three ELBv2 flavours.
		{"app/nb-demo-alb/3c4737067cb64e8e", "nb-demo-alb"},
		{"net/my-nlb/abc123def456", "my-nlb"},
		{"gwy/my-gwlb/0123456789abcdef", "my-gwlb"},
		// Names legitimately contain hyphens and digits.
		{"app/prod-web-alb-01/1A2B3C4D", "prod-web-alb-01"},

		// Not dimensions — callers must fall back to the identifier they had.
		{"nb-demo-alb", ""},        // already the bare name
		{"my-classic-elb", ""},     // Classic ELB uses the name directly
		{"app/only-two-parts", ""}, // missing the id
		{"app/name/id/extra", ""},  // too many segments
		{"app//3c47", ""},          // empty name
		{"app/name/not-hex", ""},   // id is not a hex id
		{"other/name/3c47", ""},    // unknown prefix
		{"", ""},
		// A full ARN is not the dimension; the ARN path is handled elsewhere.
		{"arn:aws:elasticloadbalancing:us-east-1:1234:loadbalancer/app/nb-demo-alb/3c47", ""},
	}
	for _, tt := range tests {
		if got := ELBV2LoadBalancerName(tt.in); got != tt.want {
			t.Errorf("ELBV2LoadBalancerName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
