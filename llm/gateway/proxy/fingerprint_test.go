package proxy

import (
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/assert"
)

func TestPrefixFingerprint(t *testing.T) {
	// Stable across a conversation's turns: turn 1 (one user msg) and turn 2 (user +
	// assistant + user) share system + tools + FIRST user → same fingerprint.
	turn1 := []byte(`{"system":"you are helpful","tools":[{"name":"t"}],"messages":[{"role":"user","content":"hi"}]}`)
	turn2 := []byte(`{"system":"you are helpful","tools":[{"name":"t"}],"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":"hello"},{"role":"user","content":"more"}]}`)
	fp1 := prefixFingerprint(schemas.Anthropic, turn1)
	assert.NotEmpty(t, fp1)
	assert.Equal(t, fp1, prefixFingerprint(schemas.Anthropic, turn2), "turns of one conversation share the fingerprint")

	// A different opening → a different fingerprint.
	other := []byte(`{"system":"you are helpful","tools":[{"name":"t"}],"messages":[{"role":"user","content":"different"}]}`)
	assert.NotEqual(t, fp1, prefixFingerprint(schemas.Anthropic, other))

	// A different system prompt → a different fingerprint.
	otherSys := []byte(`{"system":"you are terse","tools":[{"name":"t"}],"messages":[{"role":"user","content":"hi"}]}`)
	assert.NotEqual(t, fp1, prefixFingerprint(schemas.Anthropic, otherSys))

	// OpenAI carries the system in messages (role=system); still fingerprints.
	oai := []byte(`{"messages":[{"role":"system","content":"sys"},{"role":"user","content":"q"}]}`)
	assert.NotEmpty(t, prefixFingerprint(schemas.OpenAI, oai))

	// Gemini: systemInstruction + contents.
	gem := []byte(`{"systemInstruction":{"parts":[{"text":"s"}]},"contents":[{"role":"user","parts":[{"text":"q"}]}]}`)
	assert.NotEmpty(t, prefixFingerprint(schemas.Gemini, gem))

	// No stable prefix / bad input → empty.
	assert.Empty(t, prefixFingerprint(schemas.Anthropic, []byte(`{"model":"x"}`)))
	assert.Empty(t, prefixFingerprint(schemas.Anthropic, nil))
	assert.Empty(t, prefixFingerprint(schemas.Anthropic, []byte("garbage")))
}
