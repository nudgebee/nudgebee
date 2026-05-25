package core

import (
	"testing"

	"github.com/stretchr/testify/assert"

	toolcore "nudgebee/llm/tools/core"
)

func TestDedupeSkillReferences_NilAndEmpty(t *testing.T) {
	assert.Nil(t, dedupeSkillReferences(nil))
	assert.Equal(t, []toolcore.NBToolResponseReference{}, dedupeSkillReferences([]toolcore.NBToolResponseReference{}))
}

func TestDedupeSkillReferences_RemovesDuplicateSkillsByUrl(t *testing.T) {
	refs := []toolcore.NBToolResponseReference{
		{Type: "skill", Url: "kb-a", Text: "alpha"},
		{Type: "skill", Url: "kb-b", Text: "beta"},
		{Type: "skill", Url: "kb-a", Text: "alpha (dup from sub-agent)"},
		{Type: "skill", Url: "kb-c", Text: "gamma"},
	}
	got := dedupeSkillReferences(refs)
	assert.Equal(t, 3, len(got))
	urls := []string{got[0].Url, got[1].Url, got[2].Url}
	assert.ElementsMatch(t, []string{"kb-a", "kb-b", "kb-c"}, urls)
	// First occurrence wins — the parent's original text should survive.
	for _, r := range got {
		if r.Url == "kb-a" {
			assert.Equal(t, "alpha", r.Text)
		}
	}
}

func TestDedupeSkillReferences_NonSkillRefsArePreservedVerbatim(t *testing.T) {
	refs := []toolcore.NBToolResponseReference{
		{Type: "link", Url: "https://example.com", Text: "example"},
		{Type: "file", Url: "/tmp/out.txt", Text: "output"},
		{Type: "link", Url: "https://example.com", Text: "example duplicate"},
		{Type: "k8s_resource", Url: "pod/foo", Text: "foo"},
	}
	got := dedupeSkillReferences(refs)
	// Non-skill refs must be preserved verbatim, duplicates included — the UI
	// semantics for those types are different and outside the scope of this dedup.
	assert.Equal(t, len(refs), len(got))
	for i, r := range refs {
		assert.Equal(t, r, got[i])
	}
}

func TestDedupeSkillReferences_MixedInteraction(t *testing.T) {
	refs := []toolcore.NBToolResponseReference{
		{Type: "link", Url: "https://docs/a", Text: "docs a"},
		{Type: "skill", Url: "kb-1", Text: "first"},
		{Type: "file", Url: "/tmp/x", Text: "x"},
		{Type: "skill", Url: "kb-1", Text: "first (dup)"},
		{Type: "link", Url: "https://docs/b", Text: "docs b"},
		{Type: "skill", Url: "kb-2", Text: "second"},
	}
	got := dedupeSkillReferences(refs)
	// Expect 5 back: both links, both distinct skills, the file, no skill dup.
	assert.Equal(t, 5, len(got))
	var skillCount int
	for _, r := range got {
		if r.Type == "skill" {
			skillCount++
		}
	}
	assert.Equal(t, 2, skillCount, "skill dup on kb-1 must be removed")
}

func TestDedupeSkillReferences_EmptyUrlSkillNotDeduped(t *testing.T) {
	// Defensive: if a skill somehow has no Url (shouldn't happen — we always
	// set Url to kb.id — but guard against silently collapsing unrelated
	// rows under the empty-string key).
	refs := []toolcore.NBToolResponseReference{
		{Type: "skill", Url: "", Text: "weird 1"},
		{Type: "skill", Url: "", Text: "weird 2"},
	}
	got := dedupeSkillReferences(refs)
	assert.Equal(t, 2, len(got))
}
