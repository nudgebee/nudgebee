package event

import (
	"testing"

	"nudgebee/services/security"

	"github.com/stretchr/testify/assert"
)

// An aggregation_key derived from a per-delivery identifier can never match
// EventRuleExists, so registration would create a fresh event_rules row on every
// firing. registerNativeEventTypeRule must bail out before it claims the key —
// an unclaimed key is the observable proof it returned early, and it keeps the
// test off the database entirely.
func TestRegisterNativeEventTypeRule_SkipsUnstableAggregationKey(t *testing.T) {
	evt := map[string]any{
		"cloud_account_id": "acct-unstable",
		"tenant":           "tenant-unstable",
		"aggregation_key":  "fjtuytA7jxKNH2wCHVfFny",
		"source":           "zenduty_webhook",
		"labels": map[string]any{
			LabelUnstableAggregationKey: "true",
		},
	}

	registerNativeEventTypeRule(&security.RequestContext{}, evt)

	_, claimed := nativeEventRuleSeen.Load("tenant-unstable:acct-unstable:fjtuytA7jxKNH2wCHVfFny")
	assert.False(t, claimed, "an unstable aggregation_key must not reach registration")
}

func TestHasUnstableAggregationKey(t *testing.T) {
	tests := []struct {
		name string
		evt  map[string]any
		want bool
	}{
		{
			// The shape the post-process consumer produces: models.Event is
			// JSON round-tripped, so labels arrive as map[string]any.
			name: "flagged, map[string]any labels",
			evt:  map[string]any{"labels": map[string]any{LabelUnstableAggregationKey: "true"}},
			want: true,
		},
		{
			name: "flagged, map[string]string labels",
			evt:  map[string]any{"labels": map[string]string{LabelUnstableAggregationKey: "true"}},
			want: true,
		},
		{
			name: "real alertname carries no flag",
			evt:  map[string]any{"labels": map[string]any{"alertname": "ApplicationAPIFailures"}},
			want: false,
		},
		{
			// Only the literal "true" counts — a stray value must not silently
			// suppress registration for a legitimate event type.
			name: "non-true value is not a flag",
			evt:  map[string]any{"labels": map[string]any{LabelUnstableAggregationKey: "false"}},
			want: false,
		},
		{name: "no labels at all", evt: map[string]any{}, want: false},
		{name: "labels of an unexpected type", evt: map[string]any{"labels": "not-a-map"}, want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, hasUnstableAggregationKey(tc.evt))
		})
	}
}
