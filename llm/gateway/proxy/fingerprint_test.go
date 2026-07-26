package proxy

import (
	"testing"

	"nudgebee/llm-gateway/auth"

	"github.com/stretchr/testify/assert"
)

func TestPrefixFingerprint(t *testing.T) {
	id := auth.Identity{TenantID: "t1", UserID: "u1"}

	// Stable across a conversation's turns: turn 1 (one user msg) and turn 2 (user +
	// assistant + user) share system + tools + FIRST user → same fingerprint.
	turn1 := []byte(`{"system":"you are helpful","tools":[{"name":"t"}],"messages":[{"role":"user","content":"hi"}]}`)
	turn2 := []byte(`{"system":"you are helpful","tools":[{"name":"t"}],"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":"hello"},{"role":"user","content":"more"}]}`)
	fp1 := prefixFingerprint(id, turn1)
	assert.NotEmpty(t, fp1)
	assert.Equal(t, fp1, prefixFingerprint(id, turn2), "turns of one conversation share the fingerprint")

	// Identity scoping: a DIFFERENT user opening identically → a different fingerprint
	// (otherwise everyone starting with "hi" would be grouped as one conversation).
	assert.NotEqual(t, fp1, prefixFingerprint(auth.Identity{TenantID: "t1", UserID: "u2"}, turn1), "different user → different fingerprint")
	// A different tenant likewise diverges.
	assert.NotEqual(t, fp1, prefixFingerprint(auth.Identity{TenantID: "t2", UserID: "u1"}, turn1), "different tenant → different fingerprint")

	// A different opening → a different fingerprint.
	other := []byte(`{"system":"you are helpful","tools":[{"name":"t"}],"messages":[{"role":"user","content":"different"}]}`)
	assert.NotEqual(t, fp1, prefixFingerprint(id, other))

	// A different system prompt → a different fingerprint.
	otherSys := []byte(`{"system":"you are terse","tools":[{"name":"t"}],"messages":[{"role":"user","content":"hi"}]}`)
	assert.NotEqual(t, fp1, prefixFingerprint(id, otherSys))

	// OpenAI carries the system in messages (role=system); still fingerprints.
	oai := []byte(`{"messages":[{"role":"system","content":"sys"},{"role":"user","content":"q"}]}`)
	assert.NotEmpty(t, prefixFingerprint(id, oai))

	// Gemini: systemInstruction + contents.
	gem := []byte(`{"systemInstruction":{"parts":[{"text":"s"}]},"contents":[{"role":"user","parts":[{"text":"q"}]}]}`)
	assert.NotEmpty(t, prefixFingerprint(id, gem))

	// No stable prefix / bad input → empty (identity alone is never a prefix).
	assert.Empty(t, prefixFingerprint(id, []byte(`{"model":"x"}`)))
	assert.Empty(t, prefixFingerprint(id, nil))
	assert.Empty(t, prefixFingerprint(id, []byte("garbage")))
}
