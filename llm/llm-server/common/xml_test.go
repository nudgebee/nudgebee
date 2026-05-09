package common

import (
	"encoding/xml"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestXmlEscapeAmpersands(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "bare ampersand",
			input:    "<query>foo & bar</query>",
			expected: "<query>foo &amp; bar</query>",
		},
		{
			name:     "already encoded amp",
			input:    "<query>foo &amp; bar</query>",
			expected: "<query>foo &amp; bar</query>",
		},
		{
			name:     "lt and gt entities untouched",
			input:    "<query>a &lt; b &gt; c</query>",
			expected: "<query>a &lt; b &gt; c</query>",
		},
		{
			name:     "numeric entity untouched",
			input:    "<query>&#65; &#x41;</query>",
			expected: "<query>&#65; &#x41;</query>",
		},
		{
			name:     "multiple bare ampersands",
			input:    "foo & bar & baz",
			expected: "foo &amp; bar &amp; baz",
		},
		{
			name:     "no ampersand",
			input:    "<query>hello world</query>",
			expected: "<query>hello world</query>",
		},
		{
			name:     "mixed: bare and encoded",
			input:    "a & b &amp; c &lt; d",
			expected: "a &amp; b &amp; c &lt; d",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := XmlEscapeAmpersands(tt.input)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestXmlFixMismatchedTag(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		xmlErr      string
		wantFixed   bool
		wantContent string
	}{
		{
			name:        "mismatched query closed by tool",
			content:     "<plan_response><step><query>get pods</tool></step></plan_response>",
			xmlErr:      "XML syntax error on line 1: element <query> closed by </tool>",
			wantFixed:   true,
			wantContent: "<plan_response><step><query>get pods</query></step></plan_response>",
		},
		{
			name:        "no mismatch pattern in error",
			content:     "<plan_response></plan_response>",
			xmlErr:      "XML syntax error on line 1: unexpected EOF",
			wantFixed:   false,
			wantContent: "<plan_response></plan_response>",
		},
		{
			name:        "same open and close tag - no change",
			content:     "<plan_response><query>text</query></plan_response>",
			xmlErr:      "XML syntax error on line 1: element <query> closed by </query>",
			wantFixed:   false,
			wantContent: "<plan_response><query>text</query></plan_response>",
		},
		{
			name:        "multiple occurrences of wrong closing tag replaced",
			content:     "<step><id>E1</reason><query>get pods</reason></step>",
			xmlErr:      "XML syntax error on line 1: element <id> closed by </reason>",
			wantFixed:   true,
			wantContent: "<step><id>E1</id><query>get pods</id></step>",
		},
		{
			name:        "empty error string",
			content:     "<plan_response></plan_response>",
			xmlErr:      "",
			wantFixed:   false,
			wantContent: "<plan_response></plan_response>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, fixed := XmlFixMismatchedTag(tt.content, tt.xmlErr)
			assert.Equal(t, tt.wantFixed, fixed)
			assert.Equal(t, tt.wantContent, got)
		})
	}
}

// TestXmlEscapeAmpersands_MakesValidXML verifies that escaping bare & turns
// otherwise-invalid XML into parseable XML.
func TestXmlEscapeAmpersands_MakesValidXML(t *testing.T) {
	type doc struct {
		Query string `xml:"query"`
	}

	raw := "<doc><query>start &amp; stop</query></doc>"
	// Confirm correctly encoded XML still parses.
	var d1 doc
	assert.NoError(t, xml.Unmarshal([]byte(raw), &d1))
	assert.Equal(t, "start & stop", d1.Query)

	// Content with a bare & should fail xml.Unmarshal.
	invalid := "<doc><query>start & stop</query></doc>"
	var d2 doc
	assert.Error(t, xml.Unmarshal([]byte(invalid), &d2))

	// After escaping it should succeed.
	escaped := XmlEscapeAmpersands(invalid)
	var d3 doc
	assert.NoError(t, xml.Unmarshal([]byte(escaped), &d3))
	assert.Equal(t, "start & stop", d3.Query)
}
