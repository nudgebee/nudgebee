package core

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// cdataExtract pulls every character that the standard CDATA parsing rules
// would expose from a correctly framed payload. It walks the string, respects
// `<![CDATA[` / `]]>` markers, and returns the concatenated visible text.
// Used by the tests below to prove that escapeCDATA preserves the net user
// content byte-for-byte regardless of how many times `]]>` appears.
func cdataExtract(t *testing.T, wrapped string) string {
	t.Helper()
	const open = "<![CDATA["
	const close = "]]>"
	var out strings.Builder
	i := 0
	for i < len(wrapped) {
		openIdx := strings.Index(wrapped[i:], open)
		assert.GreaterOrEqual(t, openIdx, 0, "wrapped content must open with CDATA")
		i += openIdx + len(open)
		closeIdx := strings.Index(wrapped[i:], close)
		assert.GreaterOrEqual(t, closeIdx, 0, "every CDATA section must close")
		out.WriteString(wrapped[i : i+closeIdx])
		i += closeIdx + len(close)
		if i >= len(wrapped) {
			break
		}
		// The only thing that can live BETWEEN two CDATA sections in our
		// escaping scheme is nothing at all — the next section must open
		// immediately. If anything else is there, the test fails.
		assert.True(t, strings.HasPrefix(wrapped[i:], open),
			"after closing CDATA we must either end or reopen immediately, got %q", wrapped[i:])
	}
	return out.String()
}

func TestEscapeCDATA_NoMarker(t *testing.T) {
	// Fast path: no `]]>` means no allocation, no change.
	in := "just a regular skill body with <tags> and lots of text\nand newlines"
	got := escapeCDATA(in)
	assert.Equal(t, in, got, "input without `]]>` must be returned verbatim")
}

func TestEscapeCDATA_Empty(t *testing.T) {
	assert.Equal(t, "", escapeCDATA(""))
}

func TestEscapeCDATA_SingleMarker(t *testing.T) {
	in := "foo]]>bar"
	got := escapeCDATA(in)
	assert.Equal(t, "foo]]]]><![CDATA[>bar", got)

	// Wrap it in a real CDATA section and verify a parser would reassemble
	// the exact original bytes.
	wrapped := "<![CDATA[" + got + "]]>"
	assert.Equal(t, in, cdataExtract(t, wrapped),
		"escaped payload inside a CDATA section must round-trip to the original bytes")
}

func TestEscapeCDATA_MultipleMarkers(t *testing.T) {
	in := "prefix]]>middle]]>suffix"
	got := escapeCDATA(in)
	wrapped := "<![CDATA[" + got + "]]>"
	assert.Equal(t, in, cdataExtract(t, wrapped))
}

func TestEscapeCDATA_AdjacentMarkers(t *testing.T) {
	// Back-to-back markers must also round-trip. Nasty edge case for naive
	// replacements that split on the first match and then miss the rest.
	in := "]]>]]>"
	got := escapeCDATA(in)
	wrapped := "<![CDATA[" + got + "]]>"
	assert.Equal(t, in, cdataExtract(t, wrapped))
}

func TestEscapeCDATA_AtBoundaries(t *testing.T) {
	cases := []string{
		"]]>starts with marker",
		"ends with marker]]>",
		"]]>",
	}
	for _, in := range cases {
		got := escapeCDATA(in)
		wrapped := "<![CDATA[" + got + "]]>"
		assert.Equal(t, in, cdataExtract(t, wrapped), "case %q must round-trip", in)
	}
}

func TestEscapeCDATA_PartialMarkerSequenceNotMistaken(t *testing.T) {
	// A lone `]]` or a `]>` must NOT be escaped — only the full `]]>` is the
	// CDATA terminator. This guards against an over-eager escape that would
	// mangle skill bodies containing closing brackets in prose.
	in := "one bracket ] two brackets ]] gt > all fine, neither ]> nor ] alone"
	got := escapeCDATA(in)
	assert.Equal(t, in, got)
}

func TestEscapeCDATA_XmlSkillExample(t *testing.T) {
	// Realistic scenario: a skill body that literally teaches CDATA usage
	// and therefore contains an embedded closing marker in sample text.
	in := "To embed raw text in XML use `<![CDATA[hello]]>` — everything\n" +
		"between the open and close is treated as literal characters."
	got := escapeCDATA(in)
	wrapped := "<![CDATA[" + got + "]]>"
	assert.Equal(t, in, cdataExtract(t, wrapped),
		"a skill that teaches CDATA must still round-trip inside our wrapper")
}

func TestRenderSkillsBlock_Empty(t *testing.T) {
	block, refs := renderSkillsBlock(nil)
	assert.Equal(t, "", block)
	assert.Nil(t, refs)

	block, refs = renderSkillsBlock([]agentSkillRow{})
	assert.Equal(t, "", block)
	assert.Nil(t, refs)
}

func TestRenderSkillsBlock_FramesAndReferences(t *testing.T) {
	rows := []agentSkillRow{
		{ID: "id-1", Name: "Scale Deployment", Description: "How to scale", Data: "kubectl scale ..."},
		{ID: "id-2", Name: "No Desc", Description: "", Data: "body two"},
	}
	block, refs := renderSkillsBlock(rows)

	assert.True(t, strings.HasPrefix(block, "<skills>\n"))
	assert.True(t, strings.HasSuffix(block, "</skills>"))
	assert.Contains(t, block, `<skill name="Scale Deployment">`)
	assert.Contains(t, block, "Description: How to scale")
	assert.Contains(t, block, "kubectl scale ...")
	// A row with no description must omit the Description line for that skill.
	assert.Contains(t, block, `<skill name="No Desc">`)
	assert.NotContains(t, block, "Description: \n")

	// One UI reference per row, carrying id/name/description.
	assert.Len(t, refs, 2)
	assert.Equal(t, "Scale Deployment", refs[0].Text)
	assert.Equal(t, "id-1", refs[0].Url)
	assert.Equal(t, "skill", refs[0].Type)
	assert.Equal(t, "How to scale", refs[0].Description)
}

func TestRenderSkillsBlock_EscapesCDATAMarkerInBody(t *testing.T) {
	// A skill body containing a literal `]]>` must not prematurely close the
	// CDATA section — renderSkillsBlock routes the body through escapeCDATA.
	rows := []agentSkillRow{{ID: "x", Name: "XML skill", Data: "before ]]> after"}}
	block, _ := renderSkillsBlock(rows)
	assert.NotContains(t, block, "before ]]> after")
	assert.Contains(t, block, "]]]]><![CDATA[>")
}

func TestKBCollectionName(t *testing.T) {
	// intID is env-sourced so a run can target a specific integration; the
	// fallback keeps `make test` / CI green when TEST_INTEGRATION_ID is unset.
	intID := os.Getenv("TEST_INTEGRATION_ID")
	if intID == "" {
		intID = "test-integration-id"
	}
	empty := ""
	tests := []struct {
		name          string
		kbType        string
		integrationID *string
		kbID          string
		want          string
	}{
		{"manual KB uses kb_<id>", KBTypeManual, nil, "abc123", "kb_abc123"},
		{"integration KB uses <integration_id>_knowledge_base", KBTypeIntegration, &intID, "abc123", intID + "_knowledge_base"},
		{"integration KB with nil integration id falls back to kb_<id>", KBTypeIntegration, nil, "abc123", "kb_abc123"},
		{"integration KB with empty integration id falls back to kb_<id>", KBTypeIntegration, &empty, "abc123", "kb_abc123"},
		{"unknown kb type falls back to kb_<id>", "", nil, "abc123", "kb_abc123"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, kbCollectionName(tt.kbType, tt.integrationID, tt.kbID))
		})
	}
}
