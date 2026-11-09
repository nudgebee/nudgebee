package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAccountRequiredForConnectionTest(t *testing.T) {
	assert.False(t, accountRequiredForConnectionTest(stubIntegration{name: "llm"}, "llm"),
		"an LLM config with no accounts serves the whole organisation, so it can be tested without one")
	assert.True(t, accountRequiredForConnectionTest(stubIntegration{name: "ES"}, "ES"))
}
