package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsDataRetrievalOrActionRequest(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"get pods", true},
		{"list services", true},
		{"show logs", true},
		{"give me the list of running pods in nudgebee namespace", true},
		{"can you list the pods", true},
		{"please show me the logs", true},
		{"could you please provide the list of nodes", true},
		{"check the events", true},
		{"what are the pods", true},
		{"who are you", false},
		{"hello", false},
		{"how are you", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, IsDataRetrievalOrActionRequest(tt.input))
		})
	}
}

func TestIsInvestigationRequestTask(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"investigate why the pod is failing", true},
		{"troubleshoot oom issue", true},
		{"is there any oom error?", true},
		{"do we have oom?", true},
		{"list pods", false},
		{"who are you", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, IsInvestigationRequestTask(tt.input))
		})
	}
}

func TestIsConversationalQuery(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"hi", true},
		{"hello", true},
		{"who are you", true},
		{"what are you", true},
		{"what is nubi", true},
		{"help", true},
		{"get pods", false},
		{"list services", false},
		{"investigate oom", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, IsConversationalQuery(tt.input))
		})
	}
}
