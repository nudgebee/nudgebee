package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestConversationSessionRequestWithIsResume_OptionPropagation verifies
// the IsResume flag flows through the option pattern and reaches the
// final NBAgentRequest. This is the wire that enables the resume worker
// to bypass handleConversationRequest's IN_PROGRESS guard at line 705 / 779.
func TestConversationSessionRequestWithIsResume_OptionPropagation(t *testing.T) {
	t.Run("default is false (new turn)", func(t *testing.T) {
		cfg := additionalConversationSessionRequestConfig{}
		assert.False(t, cfg.isResume)
	})

	t.Run("WithIsResume(true) sets the flag", func(t *testing.T) {
		cfg := additionalConversationSessionRequestConfig{}
		ConversationSessionRequestWithIsResume(true).apply(&cfg)
		assert.True(t, cfg.isResume)
	})

	t.Run("WithIsResume(false) keeps the flag false", func(t *testing.T) {
		cfg := additionalConversationSessionRequestConfig{}
		ConversationSessionRequestWithIsResume(false).apply(&cfg)
		assert.False(t, cfg.isResume)
	})

	t.Run("multiple calls — last write wins", func(t *testing.T) {
		cfg := additionalConversationSessionRequestConfig{}
		ConversationSessionRequestWithIsResume(true).apply(&cfg)
		ConversationSessionRequestWithIsResume(false).apply(&cfg)
		assert.False(t, cfg.isResume, "later With call should override earlier")
	})
}
