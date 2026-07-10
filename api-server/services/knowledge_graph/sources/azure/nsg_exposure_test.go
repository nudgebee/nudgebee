package azure

import (
	"reflect"
	"testing"
)

func TestExtractNSGMetadata_InternetExposure(t *testing.T) {
	s := &AzureSource{}
	props := map[string]interface{}{}
	// metaMap is the NSG `properties` sub-object (as classify.go passes propsMap).
	meta := map[string]interface{}{
		"securityRules": []interface{}{
			map[string]interface{}{"properties": map[string]interface{}{
				"direction": "Inbound", "access": "Allow",
				"sourceAddressPrefix": "Internet", "destinationPortRange": "22",
			}},
			map[string]interface{}{"properties": map[string]interface{}{
				"direction": "Inbound", "access": "Allow",
				"sourceAddressPrefix": "10.0.0.0/8", "destinationPortRange": "8080",
			}},
			map[string]interface{}{"properties": map[string]interface{}{ // Deny rule → not exposure
				"direction": "Inbound", "access": "Deny",
				"sourceAddressPrefix": "*", "destinationPortRange": "3389",
			}},
		},
	}
	s.extractNSGMetadata(props, meta)

	if props["open_to_internet"] != true {
		t.Errorf("open_to_internet = %v, want true", props["open_to_internet"])
	}
	if got := props["exposed_ports"]; !reflect.DeepEqual(got, []string{"22"}) {
		t.Errorf("exposed_ports = %v, want [22] (only the internet-open Allow rule)", got)
	}
	if got := props["ingress_cidrs"]; !reflect.DeepEqual(got, []string{"*", "10.0.0.0/8", "Internet"}) {
		t.Errorf("ingress_cidrs = %v", got)
	}
}

func TestExtractNSGMetadata_NotExposed(t *testing.T) {
	s := &AzureSource{}
	props := map[string]interface{}{}
	meta := map[string]interface{}{
		"securityRules": []interface{}{
			map[string]interface{}{"properties": map[string]interface{}{
				"direction": "Inbound", "access": "Allow",
				"sourceAddressPrefix": "VirtualNetwork", "destinationPortRange": "443",
			}},
		},
	}
	s.extractNSGMetadata(props, meta)

	if props["open_to_internet"] != false {
		t.Errorf("open_to_internet = %v, want false", props["open_to_internet"])
	}
	if _, ok := props["exposed_ports"]; ok {
		t.Errorf("exposed_ports should be absent: %v", props["exposed_ports"])
	}
}
