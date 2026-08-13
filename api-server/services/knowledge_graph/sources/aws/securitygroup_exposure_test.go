package aws

import (
	"reflect"
	"testing"
)

func TestExtractSecurityGroupMetadata_InternetExposure(t *testing.T) {
	s := &AWSSource{}
	props := map[string]interface{}{}
	// One rule opens SSH (22) to the world, one opens 8080 only to a private CIDR.
	meta := map[string]interface{}{
		"IpPermissions": []interface{}{
			map[string]interface{}{
				"IpProtocol": "tcp", "FromPort": float64(22), "ToPort": float64(22),
				"IpRanges": []interface{}{
					map[string]interface{}{"CidrIp": "0.0.0.0/0"},
				},
			},
			map[string]interface{}{
				"IpProtocol": "tcp", "FromPort": float64(8080), "ToPort": float64(8080),
				"IpRanges": []interface{}{
					map[string]interface{}{"CidrIp": "10.0.0.0/8"},
				},
			},
		},
	}
	s.extractSecurityGroupMetadata(props, meta)

	if props["open_to_internet"] != true {
		t.Errorf("open_to_internet = %v, want true", props["open_to_internet"])
	}
	if got := props["exposed_ports"]; !reflect.DeepEqual(got, []int{22}) {
		t.Errorf("exposed_ports = %v, want [22] (only the internet-open port)", got)
	}
	if got, want := props["ingress_cidrs"], []string{"0.0.0.0/0", "10.0.0.0/8"}; !reflect.DeepEqual(got, want) {
		t.Errorf("ingress_cidrs = %v, want %v", got, want)
	}
}

func TestExtractSecurityGroupMetadata_NotExposed(t *testing.T) {
	s := &AWSSource{}
	props := map[string]interface{}{}
	meta := map[string]interface{}{
		"IpPermissions": []interface{}{
			map[string]interface{}{
				"IpProtocol": "tcp", "FromPort": float64(443), "ToPort": float64(443),
				"IpRanges": []interface{}{map[string]interface{}{"CidrIp": "10.0.0.0/8"}},
			},
		},
	}
	s.extractSecurityGroupMetadata(props, meta)

	if props["open_to_internet"] != false {
		t.Errorf("open_to_internet = %v, want false", props["open_to_internet"])
	}
	if _, ok := props["exposed_ports"]; ok {
		t.Errorf("exposed_ports should be absent when nothing is internet-open: %v", props["exposed_ports"])
	}
}
