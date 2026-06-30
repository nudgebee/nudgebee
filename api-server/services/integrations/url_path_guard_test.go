package integrations

import "testing"

func TestHasURLPath(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want bool
	}{
		// Base URLs — accepted.
		{"bare host with scheme", "https://champion-cub.in2.signoz.cloud", false},
		{"trailing slash tolerated", "https://champion-cub.in2.signoz.cloud/", false},
		{"http scheme", "http://signoz.example.com", false},
		{"host with port", "https://es.example.com:9200", false},
		{"host with port and trailing slash", "https://es.example.com:9200/", false},
		{"whitespace trimmed", "  https://signoz.example.com/  ", false},
		{"empty", "", false},
		// Schemeless hosts (Datadog site) — accepted.
		{"schemeless bare host", "app.datadoghq.com", false},
		{"schemeless bare domain", "datadoghq.com", false},
		// Paths / query / fragment — rejected.
		{"stray path segment", "https://champion-cub.in2.signoz.cloud/wty234", true},
		{"browser settings path", "https://champion-cub.in2.signoz.cloud/settings/my-settings", true},
		{"kibana dashboards path", "https://my-domain.es.amazonaws.com/_dashboards/app/home", true},
		{"query string", "https://signoz.example.com?token=abc", true},
		{"fragment", "https://signoz.example.com#frag", true},
		{"schemeless host with path", "app.datadoghq.com/account/settings", true},
		{"path with port", "https://es.example.com:9200/_cluster/health", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasURLPath(tc.raw); got != tc.want {
				t.Errorf("hasURLPath(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}
