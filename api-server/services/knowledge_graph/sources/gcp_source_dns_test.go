package sources

import (
	"testing"
)

// TestSynthesizeGCPEndpointDNS pins what the GCP-source pipeline stamps onto
// `properties["dns_name"]` for resource types where GCP API metadata doesn't
// expose one (Cloud Storage today). Mirrors TestSynthesizeAWSEndpointDNS.
func TestSynthesizeGCPEndpointDNS(t *testing.T) {
	cases := []struct {
		name    string
		props   map[string]interface{}
		wantDNS string
	}{
		{
			name: "gcs_bucket",
			props: map[string]interface{}{
				"service_name": "Cloud Storage",
				"name":         "nudgebee-gcp-templates",
				"region":       "us",
			},
			wantDNS: "nudgebee-gcp-templates.storage.googleapis.com",
		},
		{
			name: "gcs_bucket_lowercased",
			props: map[string]interface{}{
				"service_name": "Cloud Storage",
				"name":         "MIXED-Case-Bucket",
				"region":       "us-central1",
			},
			wantDNS: "mixed-case-bucket.storage.googleapis.com",
		},
		// Service endpoints — synthesizer must NOT stamp.
		{
			name: "pubsub_not_synthesized",
			props: map[string]interface{}{
				"service_name": "Cloud Pub/Sub",
				"name":         "my-topic",
				"region":       "us-central1",
			},
			wantDNS: "",
		},
		{
			name: "bigquery_not_synthesized",
			props: map[string]interface{}{
				"service_name": "BigQuery",
				"name":         "my_dataset",
			},
			wantDNS: "",
		},
		{
			name: "preserves_existing_dns_name",
			props: map[string]interface{}{
				"service_name": "Cloud Storage",
				"name":         "preset-bucket",
				"dns_name":     "already-set.example.com",
			},
			wantDNS: "already-set.example.com",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synthesizeGCPEndpointDNS(tc.props)
			gotDNS, _ := tc.props["dns_name"].(string)
			if gotDNS != tc.wantDNS {
				t.Errorf("dns_name = %q, want %q", gotDNS, tc.wantDNS)
			}
		})
	}
}

// TestExtractGCPCloudRunURL covers the per-service Cloud Run URL extraction.
// Verifies dns_name is set from `meta.url` host and NOT overridden when
// dns_name is already populated.
func TestExtractGCPCloudRunURL(t *testing.T) {
	cases := []struct {
		name    string
		props   map[string]interface{}
		meta    map[string]interface{}
		wantDNS string
	}{
		{
			name:    "cloud_run_strips_scheme",
			props:   map[string]interface{}{},
			meta:    map[string]interface{}{"url": "https://nudgebee-booth-eln5wjp7uq-el.a.run.app"},
			wantDNS: "nudgebee-booth-eln5wjp7uq-el.a.run.app",
		},
		{
			name:    "cloud_run_lowercased",
			props:   map[string]interface{}{},
			meta:    map[string]interface{}{"url": "https://NudgeBee-Mixed.a.run.app"},
			wantDNS: "nudgebee-mixed.a.run.app",
		},
		{
			name:    "no_url_no_op",
			props:   map[string]interface{}{},
			meta:    map[string]interface{}{},
			wantDNS: "",
		},
		{
			name:    "url_wrong_type_no_op",
			props:   map[string]interface{}{},
			meta:    map[string]interface{}{"url": 42},
			wantDNS: "",
		},
		{
			name:    "preserves_existing_dns_name",
			props:   map[string]interface{}{"dns_name": "already-set.example.com"},
			meta:    map[string]interface{}{"url": "https://newly-derived.a.run.app"},
			wantDNS: "already-set.example.com",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			extractGCPCloudRunURL(tc.props, tc.meta)
			gotDNS, _ := tc.props["dns_name"].(string)
			if gotDNS != tc.wantDNS {
				t.Errorf("dns_name = %q, want %q", gotDNS, tc.wantDNS)
			}
		})
	}
}
