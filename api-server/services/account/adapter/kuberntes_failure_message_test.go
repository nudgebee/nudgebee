package adapter

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAgentFailureMessage(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			"agent failure response",
			`{"msg": "rightsizing_resource: StatefulSet rightsizing is not supported", "success": false, "task_processing_duration": 0}`,
			"rightsizing_resource: StatefulSet rightsizing is not supported",
		},
		{"plain text is kept", "task timed out", "task timed out"},
		{"json without a msg is kept", `{"success": false}`, `{"success": false}`},
		{"blank msg is kept", `{"msg": "  ", "success": false}`, `{"msg": "  ", "success": false}`},
		{"non-string msg is kept", `{"msg": {"code": 1}}`, `{"msg": {"code": 1}}`},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, agentFailureMessage(tt.in))
		})
	}
}
