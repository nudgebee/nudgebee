package flow_sources

import (
	"testing"
)

// TestGcpServiceDNS pins the canonical hostname for each GCP service the
// synthesizer covers. Only per-resource hostnames are emitted — see the
// function doc for the parity rationale with AwsServiceDNS.
func TestGcpServiceDNS(t *testing.T) {
	cases := []struct {
		name          string
		service       string
		region        string
		project       string
		resourceID    string
		wantCanonical string
	}{
		// Cloud Storage — bucket name in leftmost label, per-bucket host.
		{
			name:          "gcs_bucket_canonical",
			service:       "Cloud Storage",
			resourceID:    "nudgebee-gcp-templates",
			wantCanonical: "nudgebee-gcp-templates.storage.googleapis.com",
		},
		{
			name:    "gcs_no_bucket_no_match",
			service: "Cloud Storage",
		},

		// Region-/project-scoped service endpoints — must NOT synthesize.
		{name: "pubsub_not_synthesized", service: "Cloud Pub/Sub", region: "us-central1"},
		{name: "bigquery_not_synthesized", service: "BigQuery"},
		{name: "cloudsql_not_synthesized_here", service: "Cloud SQL", region: "us-central1"}, // handled by extractGCPCloudSQLMetadata directly
		{name: "gke_not_synthesized_here", service: "Kubernetes Engine", region: "us-central1"},
		{name: "cloud_run_not_synthesized_here", service: "Cloud Run", region: "us-central1"}, // handled by extractGCPCloudRunMetadata
		{name: "artifact_registry_not_synthesized", service: "Artifact Registry", region: "us-central1"},
		{name: "spanner_not_synthesized", service: "Spanner"},

		// Unknown service — null.
		{name: "unknown_service_no_match", service: "Cloud Function Beta"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotCanonical, _ := GcpServiceDNS(tc.service, tc.region, tc.project, tc.resourceID)
			if gotCanonical != tc.wantCanonical {
				t.Errorf("canonical = %q, want %q", gotCanonical, tc.wantCanonical)
			}
		})
	}
}

// TestGcpServiceFromResourceType — same independence-of-synthesis rationale
// as the AWS counterpart: the type→service mapping is complete, callers
// observe GcpServiceDNS returning "" today for non-synthesizable services.
func TestGcpServiceFromResourceType(t *testing.T) {
	cases := map[string]string{
		"storage.googleapis.com/Bucket":    "Cloud Storage",
		"cloud-storage":                    "Cloud Storage",
		"sqladmin.googleapis.com/Instance": "", // no DNS synthesis (SQL Auth Proxy connectionName lives elsewhere)
		"run.googleapis.com/Service":       "", // Cloud Run url lives in meta, not synthesized
		"unknown":                          "",
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			if got := gcpServiceFromResourceType(in); got != want {
				t.Errorf("gcpServiceFromResourceType(%q) = %q, want %q", in, got, want)
			}
		})
	}
}
