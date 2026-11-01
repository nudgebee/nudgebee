package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A webhook that can only offer candidate names (the elasticsearch container
// name, the dynatrace impacted-entity list) must still reach
// MatchWorkloadAndEnrich. Gating enrichment on EventSubjectName alone left those
// events unresolved: MatchWorkloadAndEnrich itself accepts a candidate-only
// event, but enrichEventsWithSubjectResolution never called it for one.
func TestHasWorkloadValidationInput(t *testing.T) {
	withCandidates := &EventIncomingWebhook{
		Investigation: EventIncomingWebhookInvestigation{
			Labels: map[string]string{WorkloadCandidatesLabel: "ecr-proxy-server"},
		},
	}
	assert.True(t, hasWorkloadValidationInput(withCandidates), "candidate-only event must be validated")

	withSubject := &EventIncomingWebhook{
		EventSubjectName: "checkout",
		Investigation:    EventIncomingWebhookInvestigation{Labels: map[string]string{}},
	}
	assert.True(t, hasWorkloadValidationInput(withSubject))

	withBoth := &EventIncomingWebhook{
		EventSubjectName: "checkout",
		Investigation: EventIncomingWebhookInvestigation{
			Labels: map[string]string{WorkloadCandidatesLabel: "checkout-svc"},
		},
	}
	assert.True(t, hasWorkloadValidationInput(withBoth))

	// Nothing to look up — the event falls through to the title/LLM resolvers.
	neither := &EventIncomingWebhook{
		Investigation: EventIncomingWebhookInvestigation{Labels: map[string]string{"cluster": "dev"}},
	}
	assert.False(t, hasWorkloadValidationInput(neither))

	// Labels are nil until enrichEventsWithSubjectResolution initialises them;
	// a nil map read must not panic.
	nilLabels := &EventIncomingWebhook{}
	assert.False(t, hasWorkloadValidationInput(nilLabels))
}

func TestTrimPodTemplateHash(t *testing.T) {
	cases := map[string]string{
		"checkout-6f665f49dd":    "checkout",
		"orders-api-7c75686877":  "orders-api",
		"payments-f5678b6":       "payments-f5678b6", // 7 chars: below a real hash's length
		"web-backend":            "web-backend",      // vowels never appear in the hash alphabet
		"redis-master":           "redis-master",
		"api-v2":                 "api-v2",
		"worker-10000000":        "worker-10000000", // 0 and 1 are not in the alphabet
		"checkout":               "checkout",
		"":                       "",
		"checkout-6f665f49dd-x7": "checkout-6f665f49dd-x7", // a pod name is not a ReplicaSet name
	}
	for in, want := range cases {
		assert.Equal(t, want, TrimPodTemplateHash(in), in)
	}
}
