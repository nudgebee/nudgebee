package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestSingleLearnedService covers the rule that decides whether a repeat alert
// title can skip the LLM: only a title whose learned mappings all name the same
// service answers on its own. Anything ambiguous falls through to the agent.
func TestSingleLearnedService(t *testing.T) {
	tests := []struct {
		name         string
		serviceLists []string
		want         string
	}{
		{name: "no rows", serviceLists: nil, want: ""},
		{name: "single row single service", serviceLists: []string{"payment-service"}, want: "payment-service"},
		{name: "same service across sources", serviceLists: []string{"payment-service", "payment-service"}, want: "payment-service"},
		{name: "row lists two services", serviceLists: []string{"payment-service,courier-worker"}, want: ""},
		{name: "sources disagree", serviceLists: []string{"payment-service", "courier-worker"}, want: ""},
		{name: "whitespace trimmed", serviceLists: []string{" payment-service , payment-service "}, want: "payment-service"},
		{name: "empty entries skipped", serviceLists: []string{"", ",,", "payment-service"}, want: "payment-service"},
		{name: "service names are case sensitive", serviceLists: []string{"payment-service", "Payment-Service"}, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, singleLearnedService(tt.serviceLists))
		})
	}
}
