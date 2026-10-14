package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseMemoryPrestep(t *testing.T) {
	got, ok := parseMemoryPrestep(`{"search": ["default namespace", "checkout"]}`)
	assert.True(t, ok)
	assert.Equal(t, []string{"default namespace", "checkout"}, got)

	got, ok = parseMemoryPrestep("The question names the namespace, so only the service matters.\n{\"search\": [\"checkout\"]}")
	assert.True(t, ok, "the JSON object is found when the model reasons first")
	assert.Equal(t, []string{"checkout"}, got)

	got, ok = parseMemoryPrestep("```json\n{\"search\": [\"ingress\"]}\n```")
	assert.True(t, ok, "a code fence is fine")
	assert.Equal(t, []string{"ingress"}, got)

	got, ok = parseMemoryPrestep(`{"search": []}`)
	assert.True(t, ok, "an empty list is a real verdict: self-contained question")
	assert.Empty(t, got)

	_, ok = parseMemoryPrestep("null")
	assert.False(t, ok, "a null reply is unusable")

	_, ok = parseMemoryPrestep(`{"queries": ["x"]}`)
	assert.False(t, ok, "no search key → unusable, not 'nothing needed'")

	_, ok = parseMemoryPrestep("sure, here is what I would look up: default namespace")
	assert.False(t, ok, "no JSON → unusable")

	got, ok = parseMemoryPrestep(`{"search": [" a ", "A", "", "b", "c", "d", "e", "f", "g"]}`)
	assert.True(t, ok)
	assert.Equal(t, []string{"a", "b", "c", "d", "e", "f"}, got, "trimmed, deduped case-insensitively, capped at 6")

	got, ok = parseMemoryPrestep(`{"search": ["default_namespace", "default namespace", "Default-Namespace", "timezone"]}`)
	assert.True(t, ok)
	assert.Equal(t, []string{"default_namespace", "timezone"}, got, "separator spellings count as one phrase")
}

func TestRecentHistoryTail(t *testing.T) {
	assert.Equal(t, "", recentHistoryTail("   ", 10))
	assert.Equal(t, "short", recentHistoryTail("short", 10))
	assert.Equal(t, "…cdefghij", recentHistoryTail("abcdefghij", 8), "keeps the newest characters")
	assert.Equal(t, "…éfgh", recentHistoryTail("abcdéfgh", 4), "counts runes, not bytes")
	assert.Equal(t, "", recentHistoryTail("abc", 0))
}
