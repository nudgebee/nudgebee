package egressfilter

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- PIIEmailRegex --------------------------------------------------------

func TestPIIEmailRegex_MatchesCommonShapes(t *testing.T) {
	cases := []string{
		"alice@acme.co",
		"jane.doe+labels-1@sub.acme.io",
		"a_b_c%d@x-y.z.example",
	}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			assert.Regexp(t, PIIEmailRegex, c)
		})
	}
}

func TestPIIEmailRegex_RejectsNonEmails(t *testing.T) {
	cases := []string{
		"user @ example.com", // spaces
		"@example.com",       // no local part
		"user@",              // no domain
		"user@localhost",     // no TLD (2+ letters required)
	}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			assert.NotRegexp(t, PIIEmailRegex, c)
		})
	}
}

// --- FindPIIPhones (boundary-guarded phone matcher) -----------------------

func TestFindPIIPhones_AcceptsSupportedShapes(t *testing.T) {
	cases := []struct {
		name  string
		text  string
		match string
	}{
		{"US hyphenated", "call 212-555-0147 now", "212-555-0147"},
		{"US parens", "call (212) 555-0147 now", "(212) 555-0147"},
		// E.164 shapes — the regex expects groupable 2-4 / 3-4 / 3-4 digit
		// runs separated by ` .-`. A run like `98765` (5 digits) with no
		// separator would leave a trailing digit unredacted and gets
		// rejected by the boundary guard (matches Python behaviour).
		{"E.164 with hyphens", "call +91-987-654-3210 now", "+91-987-654-3210"},
		{"E.164 with spaces", "call +1 555 123 4567 now", "+1 555 123 4567"},
		{"space-separated 3-3-4", "call 555 123 4567 now", "555 123 4567"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ranges := FindPIIPhones(tc.text)
			require.Len(t, ranges, 1, "expected one match")
			assert.Equal(t, tc.match, tc.text[ranges[0][0]:ranges[0][1]])
		})
	}
}

func TestFindPIIPhones_RejectsDotOnlyMetricValue(t *testing.T) {
	// The exact false-positive from #35432 — dot-only "phone" shape
	// is really a metric value / version string. Must not match.
	assert.Nil(t, FindPIIPhones("latency p99 = 100.500.1000"))
	assert.Nil(t, FindPIIPhones("running app v1.234.5678"))
}

func TestFindPIIPhones_RejectsPartialInsideLongerDigitRun(t *testing.T) {
	// A phone-shape prefix inside a longer digit run must NOT match —
	// partial-match would leave trailing digits unredacted (worse than
	// not matching at all). Matches Python's `(?<!\d)` + `(?!\d)`
	// guards via post-filtering.
	assert.Nil(t, FindPIIPhones("id 12345551234567 next"), "left-adjacent digit blocks match")
	assert.Nil(t, FindPIIPhones("call (555) 123-4567890 now"), "right-adjacent digit blocks match")
	// `.<digit>` on the right also blocks — otherwise 555-123-4567.89
	// would incorrectly match the 555-123-4567 prefix.
	assert.Nil(t, FindPIIPhones("call 555-123-4567.89 now"), "right-adjacent .digit blocks match")
}

func TestFindPIIPhones_SeparatedByWhitespaceStillMatches(t *testing.T) {
	// Sanity: a legit phone with a preceding unrelated number space-
	// separated still matches, because the space acts as a separator.
	ranges := FindPIIPhones("call 12345 555-123-4567 now")
	require.Len(t, ranges, 1)
}

// --- piiTokenizer + ScrubPIIInProcess -------------------------------------

func TestScrubPIIInProcess_TokenizesEmailAndPhone(t *testing.T) {
	scrubbed, mapping := ScrubPIIInProcess([]string{
		"page alice@acme.co on 212-555-0147",
	})
	require.Len(t, scrubbed, 1)
	// Both the raw email and the raw phone are gone.
	assert.NotContains(t, scrubbed[0], "alice@acme.co")
	assert.NotContains(t, scrubbed[0], "212-555-0147")
	// Tokens appear in the output.
	assert.Contains(t, scrubbed[0], "[EMAIL_1]")
	assert.Contains(t, scrubbed[0], "[PHONE_1]")
	// Mapping round-trips each value.
	assert.Equal(t, "alice@acme.co", mapping["[EMAIL_1]"])
	assert.Equal(t, "212-555-0147", mapping["[PHONE_1]"])
}

func TestScrubPIIInProcess_DedupWithinPiece(t *testing.T) {
	// Same value repeated in one piece must resolve to the same token.
	scrubbed, mapping := ScrubPIIInProcess([]string{
		"ping alice@acme.co, then alice@acme.co again",
	})
	assert.Equal(t, "ping [EMAIL_1], then [EMAIL_1] again", scrubbed[0])
	assert.Len(t, mapping, 1)
}

func TestScrubPIIInProcess_DedupAcrossBatch(t *testing.T) {
	// Same value appearing in different pieces must SHARE one token —
	// unified batch mapping is the whole point of the client-server
	// batch contract, and the in-process path must preserve it.
	scrubbed, mapping := ScrubPIIInProcess([]string{
		"first mention alice@acme.co",
		"second mention alice@acme.co",
		"unrelated bob@acme.co",
	})
	require.Len(t, scrubbed, 3)
	// alice tokenizes once, shared across pieces 0 and 1.
	aliceTok := findTokenFor(t, mapping, "alice@acme.co")
	assert.Contains(t, scrubbed[0], aliceTok)
	assert.Contains(t, scrubbed[1], aliceTok)
	// bob gets a distinct token.
	bobTok := findTokenFor(t, mapping, "bob@acme.co")
	assert.NotEqual(t, aliceTok, bobTok)
	assert.Contains(t, scrubbed[2], bobTok)
	assert.Len(t, mapping, 2)
}

func TestScrubPIIInProcess_InputOrderPreserved(t *testing.T) {
	scrubbed, _ := ScrubPIIInProcess([]string{
		"first alice@acme.co",
		"",
		"third 212-555-0147",
	})
	require.Len(t, scrubbed, 3)
	assert.True(t, strings.HasPrefix(scrubbed[0], "first "))
	assert.Equal(t, "", scrubbed[1])
	assert.True(t, strings.HasPrefix(scrubbed[2], "third "))
}

func TestScrubPIIInProcess_EmptyInput(t *testing.T) {
	scrubbed, mapping := ScrubPIIInProcess(nil)
	assert.Empty(t, scrubbed)
	assert.Empty(t, mapping)

	scrubbed2, mapping2 := ScrubPIIInProcess([]string{"", "", ""})
	assert.Equal(t, []string{"", "", ""}, scrubbed2)
	assert.Empty(t, mapping2)
}

func TestScrubPIIInProcess_NoMatchesPassesThrough(t *testing.T) {
	scrubbed, mapping := ScrubPIIInProcess([]string{"restart the nginx pod"})
	assert.Equal(t, []string{"restart the nginx pod"}, scrubbed)
	assert.Empty(t, mapping)
}

func TestScrubPIIInProcess_TokenFormatMatchesPythonSession(t *testing.T) {
	// Verbatim compatibility with scrubclient/http path — a downstream
	// rehydration built for Python's `[TYPE_n]` tokens must work
	// identically on in-process output.
	_, mapping := ScrubPIIInProcess([]string{
		"a@x.co",
		"call 555-123-4567",
		"b@y.co",
	})
	for tok := range mapping {
		assert.Regexp(t, `^\[(EMAIL|PHONE)_\d+\]$`, tok, "unexpected token shape")
	}
}

// findTokenFor is a test helper — find the token whose mapping value
// equals `value`, or fail the test.
func findTokenFor(t *testing.T, mapping map[string]string, value string) string {
	t.Helper()
	for k, v := range mapping {
		if v == value {
			return k
		}
	}
	t.Fatalf("no token found for %q in mapping %v", value, mapping)
	return ""
}
