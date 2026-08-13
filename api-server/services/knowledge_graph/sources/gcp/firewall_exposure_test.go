package gcp

import (
	"encoding/json"
	"reflect"
	"testing"

	"nudgebee/services/knowledge_graph/core"
)

func TestEnrichFirewallRule_InternetExposure(t *testing.T) {
	s := &GCPSource{}

	var open GCPFirewallRule
	_ = json.Unmarshal([]byte(`{"name":"allow-ssh","direction":"INGRESS","sourceRanges":["0.0.0.0/0","10.0.0.0/8"],"allowed":[{"IPProtocol":"tcp","ports":["22"]}]}`), &open)
	var internal GCPFirewallRule
	_ = json.Unmarshal([]byte(`{"name":"allow-internal","direction":"INGRESS","sourceRanges":["10.0.0.0/8"],"allowed":[{"IPProtocol":"tcp","ports":["5432"]}]}`), &internal)

	cli := &gcpCLIData{firewallRules: map[string]*GCPFirewallRule{
		"allow-ssh":      &open,
		"allow-internal": &internal,
	}}

	// Internet-open rule.
	n1 := &core.DbNode{Properties: map[string]interface{}{}}
	s.enrichFirewallRuleFromCLI(n1, "allow-ssh", cli)
	if n1.Properties["open_to_internet"] != true {
		t.Errorf("allow-ssh open_to_internet = %v, want true", n1.Properties["open_to_internet"])
	}
	if got := n1.Properties["exposed_ports"]; !reflect.DeepEqual(got, []string{"22"}) {
		t.Errorf("allow-ssh exposed_ports = %v, want [22]", got)
	}
	if got := n1.Properties["ingress_cidrs"]; !reflect.DeepEqual(got, []string{"0.0.0.0/0", "10.0.0.0/8"}) {
		t.Errorf("allow-ssh ingress_cidrs = %v", got)
	}

	// Private-only rule: not exposed, no exposed_ports.
	n2 := &core.DbNode{Properties: map[string]interface{}{}}
	s.enrichFirewallRuleFromCLI(n2, "allow-internal", cli)
	if n2.Properties["open_to_internet"] != false {
		t.Errorf("allow-internal open_to_internet = %v, want false", n2.Properties["open_to_internet"])
	}
	if _, ok := n2.Properties["exposed_ports"]; ok {
		t.Errorf("allow-internal should have no exposed_ports: %v", n2.Properties["exposed_ports"])
	}
}
