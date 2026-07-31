package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func requestWithTriggerSource(header string, value string) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/workflows/wf-1/trigger", nil)
	if header != "" {
		c.Request.Header.Set(header, value)
	}
	return c
}

func TestIsAITriggeredRequest(t *testing.T) {
	tests := []struct {
		name   string
		header string
		value  string
		want   bool
	}{
		{"ai marks the request", HeaderTriggerSource, TriggerSourceAI, true},
		{"case insensitive", HeaderTriggerSource, "AI", true},
		{"surrounding whitespace tolerated", HeaderTriggerSource, "  ai  ", true},
		{"no header at all", "", "", false},
		{"empty value", HeaderTriggerSource, "", false},
		{"some other source", HeaderTriggerSource, "scheduler", false},
		// Guards against a substring match ever creeping in: only an exact "ai"
		// should subject a request to the AI gate, and nothing else should be
		// able to accidentally claim it.
		{"value merely containing ai", HeaderTriggerSource, "airflow", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isAITriggeredRequest(requestWithTriggerSource(tt.header, tt.value)))
		})
	}

	t.Run("nil context is not AI", func(t *testing.T) {
		assert.False(t, isAITriggeredRequest(nil))
		assert.False(t, isAITriggeredRequest(&gin.Context{}))
	})
}
