package soul

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRender_NilReturnsEmpty(t *testing.T) {
	assert.Equal(t, "", Render(nil))
}

func TestRender_EmptySoulReturnsEmpty(t *testing.T) {
	assert.Equal(t, "", Render(&Soul{}))
}

func TestRender_StructuredFields(t *testing.T) {
	s := &Soul{
		Style: Style{
			Tone:               "terse",
			Verbosity:          "minimal",
			ExpertiseLevel:     "expert",
			RiskPosture:        "conservative",
			ConfirmDestructive: true,
			PreferCLI:          true,
			DiagnosticStyle:    "logs_first",
		},
	}
	got := Render(s)

	// Deterministic ordering for prompt-cache stability.
	wantOrder := []string{
		"tone: terse",
		"verbosity: minimal",
		"expertise_level: expert",
		"risk_posture: conservative",
		"confirm_before_destructive: true",
		"prefer_cli_over_console: true",
		"diagnostic_style: logs_first",
	}
	prev := -1
	for _, want := range wantOrder {
		idx := strings.Index(got, want)
		assert.GreaterOrEqual(t, idx, 0, "missing line %q in block:\n%s", want, got)
		assert.Greater(t, idx, prev, "wrong order: %q should come after previous field", want)
		prev = idx
	}
	assert.True(t, strings.HasPrefix(got, "<user_style>"), "block should open with tag")
	assert.True(t, strings.HasSuffix(got, "</user_style>"), "block should close with tag")
}

func TestRender_MarkdownAfterStructured(t *testing.T) {
	s := &Soul{
		Style:    Style{Tone: "terse"},
		Markdown: "Always prefer AWS CLI over console.",
	}
	got := Render(s)

	toneIdx := strings.Index(got, "tone: terse")
	mdIdx := strings.Index(got, "Always prefer AWS CLI")
	assert.Greater(t, mdIdx, toneIdx, "markdown should render AFTER structured fields")
}

func TestRender_OmitsFalseBooleans(t *testing.T) {
	s := &Soul{Style: Style{Tone: "terse", ConfirmDestructive: false, PreferCLI: false}}
	got := Render(s)
	assert.NotContains(t, got, "confirm_before_destructive")
	assert.NotContains(t, got, "prefer_cli_over_console")
}

func TestIsEmpty(t *testing.T) {
	assert.True(t, (*Soul)(nil).IsEmpty())
	assert.True(t, (&Soul{}).IsEmpty())
	assert.False(t, (&Soul{Style: Style{Tone: "terse"}}).IsEmpty())
	assert.False(t, (&Soul{Markdown: "hello"}).IsEmpty())
}
