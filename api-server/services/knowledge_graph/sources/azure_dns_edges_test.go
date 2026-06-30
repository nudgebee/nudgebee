package sources

import "testing"

func TestParsePrivateDNSVNetLinks(t *testing.T) {
	t.Run("wrapped ARG result", func(t *testing.T) {
		out := `{"count":2,"data":[
          {"zoneId":"/subscriptions/s/resourceGroups/rg/providers/Microsoft.Network/privateDnsZones/privatelink.blob.core.windows.net","vnetId":"/subscriptions/s/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet1"},
          {"zoneId":"/subscriptions/s/.../privateDnsZones/z2","vnetId":"/subscriptions/s/.../virtualNetworks/vnet2"}
        ],"total_records":2}`
		links := parsePrivateDNSVNetLinks(out)
		if len(links) != 2 {
			t.Fatalf("expected 2 links, got %d", len(links))
		}
		if links[0].VnetID == "" || links[0].ZoneID == "" {
			t.Errorf("link fields not parsed: %+v", links[0])
		}
	})

	t.Run("bare array", func(t *testing.T) {
		out := `[{"zoneId":"z","vnetId":"v"}]`
		links := parsePrivateDNSVNetLinks(out)
		if len(links) != 1 || links[0].ZoneID != "z" || links[0].VnetID != "v" {
			t.Errorf("bare array not parsed: %+v", links)
		}
	})

	t.Run("empty and malformed", func(t *testing.T) {
		for _, in := range []string{"", "   ", "[]", "{}", "not json"} {
			if got := parsePrivateDNSVNetLinks(in); len(got) != 0 {
				t.Errorf("parsePrivateDNSVNetLinks(%q) = %v, want empty", in, got)
			}
		}
	})
}
