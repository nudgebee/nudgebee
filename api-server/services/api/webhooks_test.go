package api

import (
	"nudgebee/services/common"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWebhookPayloadParsing(t *testing.T) {
	t.Run("ParseStubPayload", func(t *testing.T) {
		data := `{"trigger":{"name":"test_trigger"}}`
		var payload struct {
			Trigger struct {
				Name string `json:"name"`
			} `json:"trigger"`
		}
		err := common.UnmarshalJson([]byte(data), &payload)
		assert.Nil(t, err)
		assert.Equal(t, "test_trigger", payload.Trigger.Name)
	})
}
