package flow_sources

import "testing"

// TestELBFromENIDescription — an ELB's ENIs are the addresses it answers on, and
// flow logs see backends talking back through them. Unresolved, they became bare
// private IPs that every workload behind a balancer appeared to depend on.
func TestELBFromENIDescription(t *testing.T) {
	tests := []struct {
		name, description, wantID, wantName string
	}{
		{
			name:        "application load balancer",
			description: "ELB app/pixelpulse-demo-alb/8f314ac75b1d43a8",
			wantID:      "app/pixelpulse-demo-alb/8f314ac75b1d43a8",
			wantName:    "pixelpulse-demo-alb",
		},
		{
			name:        "network load balancer",
			description: "ELB net/my-nlb/1a2b3c4d5e6f7890",
			wantID:      "net/my-nlb/1a2b3c4d5e6f7890",
			wantName:    "my-nlb",
		},
		{
			// Classic ELBs carry the bare name, so the identifier is the name.
			name:        "classic load balancer",
			description: "ELB my-classic-lb",
			wantID:      "my-classic-lb",
			wantName:    "my-classic-lb",
		},
		{"not an ELB interface", "Interface for NAT Gateway nat-010ea04de1c2538cf", "", ""},
		{"RDS interface", "RDSNetworkInterface", "", ""},
		{"empty", "", "", ""},
		{"prefix but nothing after it", "ELB ", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, name := elbFromENIDescription(tt.description)
			if id != tt.wantID || name != tt.wantName {
				t.Errorf("got (%q, %q), want (%q, %q)", id, name, tt.wantID, tt.wantName)
			}
		})
	}
}

// TestNATGatewayDescriptionParsing — every private-subnet instance egresses
// through the NAT gateway, so its address is the single noisiest unmapped one on
// a VPC: left unresolved, half the estate appears to depend on a bare IP.
func TestNATGatewayDescriptionParsing(t *testing.T) {
	const desc = natGatewayDescriptionPrefix + "nat-010ea04de1c2538cf"
	if got := trimNATGatewayID(desc); got != "nat-010ea04de1c2538cf" {
		t.Errorf("got %q, want the gateway id", got)
	}
	// A description that merely mentions a NAT gateway must not be mistaken for
	// the interface's own — the prefix is an exact AWS-generated form.
	if got := trimNATGatewayID("route to NAT Gateway nat-123"); got != "" {
		t.Errorf("got %q from a non-matching description, want empty", got)
	}
}
