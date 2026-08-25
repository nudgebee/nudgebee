package azure

import (
	"net/http"
	"nudgebee/collector/cloud/providers"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

// metricAlertsAPIVersion is an api-version that ARM actually serves for the
// Microsoft.Insights/metricAlerts resource type.
//
// armmonitor v0.13.0 hardcodes api-version 2026-01-01 for every MetricAlerts
// operation, and ARM rejects it:
//
//	404 InvalidResourceType — The resource type 'metricalerts' could not be found
//	in the namespace 'Microsoft.Insights' for api version '2026-01-01'. The
//	supported api-versions are '2017-09-01-preview,2018-03-01,2024-01-01-preview,
//	2024-03-01-preview'.
//
// That broke every metric-alert call — resource sync, alarm enrichment, event
// rule listing, and the alarm create/update/delete paths — for every Azure
// account, from the v0.11.0 -> v0.13.0 bump onward.
//
// 2024-03-01-preview is the version armmonitor v0.12.0 itself sent, and
// MetricAlertProperties is unchanged between v0.12.0 and v0.13.0, so the typed
// models in the current SDK deserialize its responses correctly.
//
// Remove this override (and the policy below) once armmonitor ships a
// MetricAlerts client whose api-version ARM accepts. Downgrading the module is
// not an option: NewDiagnosticSettingsClient only exists from v0.13.0 and is
// used across ~20 azure_*.go files.
const metricAlertsAPIVersion = "2024-03-01-preview"

// metricAlertsPathFragment matches every metricAlerts URL — subscription-scoped
// and resource-group-scoped list calls, and the per-alert Get/Delete/Update
// paths — in the lowercased request path.
const metricAlertsPathFragment = "/providers/microsoft.insights/metricalerts"

// apiVersionOverridePolicy rewrites the api-version query parameter on requests
// whose path contains pathFragment. It is a per-call policy, so it runs after
// the SDK has built the request and set its own api-version.
type apiVersionOverridePolicy struct {
	pathFragment string
	apiVersion   string
}

func (p *apiVersionOverridePolicy) Do(req *policy.Request) (*http.Response, error) {
	raw := req.Raw()
	if raw != nil && raw.URL != nil && strings.Contains(strings.ToLower(raw.URL.Path), p.pathFragment) {
		// Only rewrite a version the SDK actually set, so this cannot bolt an
		// api-version onto a request that never carried one.
		if query := raw.URL.Query(); query.Get("api-version") != "" {
			query.Set("api-version", p.apiVersion)
			raw.URL.RawQuery = query.Encode()
		}
	}
	return req.Next()
}

// getAzureMetricAlertsOpts returns client options for armmonitor's MetricAlerts
// client: the usual permission-audit policy plus the api-version override above.
//
// It is deliberately separate from getAzureAuditOpts rather than folded into it.
// The override is scoped to metricAlerts paths and would be a no-op elsewhere,
// but every Azure client in this package shares getAzureAuditOpts and none of
// them need this.
func getAzureMetricAlertsOpts(ctx providers.CloudProviderContext) *arm.ClientOptions {
	opts := getAzureAuditOpts(ctx)
	if opts == nil {
		// getAzureAuditOpts returns nil when no audit info is in the context.
		// The override still has to be applied, so build options regardless.
		opts = &arm.ClientOptions{}
	}
	opts.PerCallPolicies = append(opts.PerCallPolicies, &apiVersionOverridePolicy{
		pathFragment: metricAlertsPathFragment,
		apiVersion:   metricAlertsAPIVersion,
	})
	return opts
}
