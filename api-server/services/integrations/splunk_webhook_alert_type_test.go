package integrations

import "testing"

// A Splunk saved-search alert is an event search unless the result row proves
// otherwise. Getting this backwards is not cosmetic: CreateEventRule defaults an
// empty alert_type to 'metric', and a log alert stored as 'metric' sends the rule's
// expression through a PromQL parser on every investigation.
func TestSplunkEventRuleAlertType(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    string
	}{
		{
			name:    "plain event search defaults to log",
			payload: `{"sid":"s1","search_name":"errors","result":{"_raw":"boom","host":"h1","count":"8"}}`,
			want:    "log",
		},
		{
			name:    "mstats result carrying metric_name is a metric alert",
			payload: `{"sid":"s1","search_name":"cpu","result":{"metric_name":"k8s.pod.cpu","_value":"0.93"}}`,
			want:    "metric",
		},
		{
			name:    "_value alone is enough",
			payload: `{"sid":"s1","search_name":"mem","result":{"_value":"12"}}`,
			want:    "metric",
		},
		{
			name:    "metric marker in the results array is found too",
			payload: `{"sid":"s1","search_name":"cpu","results":[{"metric_name":"x","_value":"1"}]}`,
			want:    "metric",
		},
		{
			name:    "empty result is log, never the metric default",
			payload: `{"sid":"s1","search_name":"empty"}`,
			want:    "log",
		},
		{
			name:    "unparseable payload falls back to log",
			payload: `{not json`,
			want:    "log",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := splunkEventRuleAlertType(tt.payload); got != tt.want {
				t.Errorf("splunkEventRuleAlertType() = %q, want %q", got, tt.want)
			}
		})
	}
}

// createSplunkEventRule must not upsert a rule keyed on the per-firing SID. Without a
// saved-search name there is no stable identity, and every alert run would create a
// new rule.
func TestCreateSplunkEventRuleRequiresAlertName(t *testing.T) {
	// A nil RequestContext would panic the moment the function touched it; the
	// early return on an empty name means it never does. That is the assertion.
	createSplunkEventRule(nil, "acct", "", "title", "desc", "warning", "log")
}
