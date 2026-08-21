package azure

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
)

// captureTransport records the request that reached the wire and returns 200.
type captureTransport struct{ got *http.Request }

func (c *captureTransport) Do(req *http.Request) (*http.Response, error) {
	c.got = req
	return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Request: req}, nil
}

// runThroughPolicy sends rawURL through a pipeline carrying the override policy
// and returns the URL as the transport saw it.
func runThroughPolicy(t *testing.T, rawURL string) *url.URL {
	t.Helper()

	transport := &captureTransport{}
	pipeline := runtime.NewPipeline("test", "v1.0.0", runtime.PipelineOptions{}, &policy.ClientOptions{
		Transport: transport,
		PerCallPolicies: []policy.Policy{
			&apiVersionOverridePolicy{pathFragment: metricAlertsPathFragment, apiVersion: metricAlertsAPIVersion},
		},
	})

	req, err := runtime.NewRequest(context.Background(), http.MethodGet, rawURL)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := pipeline.Do(req)
	if err != nil {
		t.Fatalf("pipeline.Do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if transport.got == nil {
		t.Fatal("request never reached the transport")
	}
	return transport.got.URL
}

// armmonitor v0.13.0 sends 2026-01-01, which ARM rejects with 404
// InvalidResourceType. Every metricAlerts operation must go out on a version
// ARM actually serves.
func TestAPIVersionOverride_RewritesMetricAlertsRequests(t *testing.T) {
	cases := []struct {
		name string
		url  string
	}{
		{
			name: "subscription-scoped list",
			url:  "https://management.azure.com/subscriptions/sub-1/providers/Microsoft.Insights/metricAlerts?api-version=2026-01-01",
		},
		{
			name: "resource-group-scoped get",
			url:  "https://management.azure.com/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Insights/metricAlerts/my-alert?api-version=2026-01-01",
		},
		{
			// ARM resource IDs are case-insensitive and the SDK does not
			// normalize casing, so matching must not be case-sensitive.
			name: "lowercased provider segment",
			url:  "https://management.azure.com/subscriptions/sub-1/providers/microsoft.insights/metricalerts?api-version=2026-01-01",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := runThroughPolicy(t, tc.url).Query().Get("api-version")
			if got != metricAlertsAPIVersion {
				t.Fatalf("api-version = %q, want %q", got, metricAlertsAPIVersion)
			}
		})
	}
}

// The override must not touch any other resource type — several of them are on
// api-versions that only they support.
func TestAPIVersionOverride_LeavesOtherResourceTypesAlone(t *testing.T) {
	const rawURL = "https://management.azure.com/subscriptions/sub-1/providers/Microsoft.Insights/diagnosticSettings?api-version=2021-05-01-preview"

	if got := runThroughPolicy(t, rawURL).Query().Get("api-version"); got != "2021-05-01-preview" {
		t.Fatalf("api-version = %q, want it untouched", got)
	}
}

// The policy rewrites an existing api-version; it must never add one to a
// request that did not carry it.
func TestAPIVersionOverride_DoesNotAddMissingAPIVersion(t *testing.T) {
	const rawURL = "https://management.azure.com/subscriptions/sub-1/providers/Microsoft.Insights/metricAlerts"

	got := runThroughPolicy(t, rawURL)
	if got.Query().Has("api-version") {
		t.Fatalf("api-version was added where none existed: %s", got.RawQuery)
	}
}

// The whole point of the fix: the version we send must be one ARM listed as
// supported in the 404 it returned for 2026-01-01.
func TestMetricAlertsAPIVersionIsServedByARM(t *testing.T) {
	supported := "2017-09-01-preview,2018-03-01,2024-01-01-preview,2024-03-01-preview"

	for _, v := range strings.Split(supported, ",") {
		if v == metricAlertsAPIVersion {
			return
		}
	}
	t.Fatalf("metricAlertsAPIVersion %q is not in ARM's supported set (%s)", metricAlertsAPIVersion, supported)
}

// getAzureMetricAlertsOpts appends to the options getAzureAuditOpts returns,
// which is only safe because that helper builds a fresh struct and a fresh
// policy slice on every call. If it is ever changed to hand back a shared or
// cached object, appending would accumulate policies across clients — this
// pins the assumption so that change fails here instead of in production.
func TestAzureAuditClientOptionsAreNotShared(t *testing.T) {
	info := &AzureAuditInfo{TenantID: "tenant", CloudAccountID: "account", ServiceName: "service"}

	first, second := azureAuditClientOptions(info), azureAuditClientOptions(info)
	if first == second {
		t.Fatal("azureAuditClientOptions returned the same struct twice; appending to it would mutate shared state")
	}
	if len(first.PerCallPolicies) == 0 || len(second.PerCallPolicies) == 0 {
		t.Fatal("expected the audit policy to be present")
	}
	if &first.PerCallPolicies[0] == &second.PerCallPolicies[0] {
		t.Fatal("azureAuditClientOptions reused the policy slice; appending to it would leak across clients")
	}

	// Appending must not grow what the next caller sees.
	before := len(second.PerCallPolicies)
	first.PerCallPolicies = append(first.PerCallPolicies, &apiVersionOverridePolicy{})
	if got := len(azureAuditClientOptions(info).PerCallPolicies); got != before {
		t.Fatalf("policy count drifted to %d after an append, want %d", got, before)
	}
}
