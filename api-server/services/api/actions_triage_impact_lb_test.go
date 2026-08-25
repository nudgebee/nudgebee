package api

import (
	"testing"

	"nudgebee/services/knowledge_graph/core"
)

// TestLoadBalancerCandidateDerived pins that a load-balancer alarm's subject name
// yields the bare node name as a lookup candidate. Without it the resolver only
// ever searches for a node literally called "app/nb-demo-alb/3c47…", which no
// graph contains, so the alarm produced no blast radius and no knowledge_graph
// card however complete the topology was.
func TestLoadBalancerCandidateDerived(t *testing.T) {
	const subject = "app/nb-demo-alb/3c4737067cb64e8e"
	if got := core.ELBV2LoadBalancerName(subject); got != "nb-demo-alb" {
		t.Fatalf("derived name = %q, want nb-demo-alb", got)
	}
}

// TestNonLoadBalancerSubjectsUnaffected guards the blast radius of the change:
// the helper returns "" for everything that is not a load-balancer dimension, and
// the resolver's `add` skips empty strings — so no other subject type gains a
// candidate.
func TestNonLoadBalancerSubjectsUnaffected(t *testing.T) {
	for _, subject := range []string{
		"nb-demo-db",                // RDS
		"i-0f568ef22d52139bb",       // EC2
		"checkout-7d9f8c6b5d-abcde", // pod
		"checkout",                  // workload
		"my-classic-elb",            // Classic ELB, name used directly
	} {
		if got := core.ELBV2LoadBalancerName(subject); got != "" {
			t.Errorf("%q gained an unexpected candidate %q", subject, got)
		}
	}
}
