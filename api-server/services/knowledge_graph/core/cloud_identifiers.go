package core

import "regexp"

// elbV2Dimension matches the CloudWatch LoadBalancer dimension of an ELBv2 load
// balancer — "app/<name>/<id>" for Application, "net/<name>/<id>" for Network,
// "gwy/<name>/<id>" for Gateway.
//
// That string is the only identifier a load-balancer alarm carries: it is the
// dimension CloudWatch scopes the metric by. The graph node is keyed by
// "<name>" alone, so an alarm can never be matched to its resource by name
// unless the name is lifted back out of the dimension.
var elbV2Dimension = regexp.MustCompile(`^(?:app|net|gwy)/([^/]+)/[0-9a-fA-F]+$`)

// ELBV2LoadBalancerName returns the load-balancer name inside a CloudWatch
// LoadBalancer dimension, or "" when the input is not one.
//
// Callers should treat "" as "this is not a load-balancer dimension" and fall
// back to the identifier they already had, rather than as an error — most
// subjects are not load balancers.
func ELBV2LoadBalancerName(dimension string) string {
	m := elbV2Dimension.FindStringSubmatch(dimension)
	if m == nil {
		return ""
	}
	return m[1]
}
