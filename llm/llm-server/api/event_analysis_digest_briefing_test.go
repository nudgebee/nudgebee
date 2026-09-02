package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseDigestBriefing(t *testing.T) {
	body := `{
		"snapshot": ["Recurring: 7 (15%)"],
		"what_broke_lede": "Capacity dominated the week.",
		"patterns": [{"title": "llm-server hotspot", "stance": "harden", "body": "Three findings."}],
		"plan": [{"priority": "P1", "action": "Raise memory limits.", "area": "llm-server", "owner": "dev-team"}],
		"hygiene": {"noise": "46 events", "pipeline": "83 of 93", "confidence": "2 low"}
	}`

	cases := []struct {
		name string
		in   string
	}{
		// The prompt forbids a fence, but models emit one anyway; treating that as a
		// parse failure would mark an otherwise good week partial and retry it.
		{"bare json", body},
		{"fenced", "```json\n" + body + "\n```"},
		{"fenced without language", "```\n" + body + "\n```"},
		{"surrounding whitespace", "\n\n  " + body + "  \n"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b, err := parseDigestBriefing(c.in)
			require.NoError(t, err)
			assert.Equal(t, "Capacity dominated the week.", b.WhatBrokeLede)
			require.Len(t, b.Patterns, 1)
			assert.Equal(t, "harden", b.Patterns[0].Stance)
			require.Len(t, b.Plan, 1)
			assert.Equal(t, "P1", b.Plan[0].Priority)
			assert.Equal(t, "dev-team", b.Plan[0].Owner)
			assert.Equal(t, "2 low", b.Hygiene.Confidence)
		})
	}
}

// A non-JSON response must fail loudly rather than store an empty review: the
// caller marks the week partial and the gap scan retries it.
func TestParseDigestBriefingRejectsProse(t *testing.T) {
	_, err := parseDigestBriefing("## Telemetry Snapshot\n- Recurring: 7")
	assert.Error(t, err)
}
