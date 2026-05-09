package soul

import (
	"fmt"
	"strings"
)

// Render produces the <user_style> prompt block for a Soul.
// Returns empty string for a nil or empty Soul (cold-start graceful degradation).
func Render(s *Soul) string {
	if s.IsEmpty() {
		return ""
	}

	var b strings.Builder
	b.WriteString("<user_style>\n")

	// Structured fields first, deterministic order for prompt-cache stability.
	if s.Style.Tone != "" {
		fmt.Fprintf(&b, "tone: %s\n", s.Style.Tone)
	}
	if s.Style.Verbosity != "" {
		fmt.Fprintf(&b, "verbosity: %s\n", s.Style.Verbosity)
	}
	if s.Style.ExpertiseLevel != "" {
		fmt.Fprintf(&b, "expertise_level: %s\n", s.Style.ExpertiseLevel)
	}
	if s.Style.RiskPosture != "" {
		fmt.Fprintf(&b, "risk_posture: %s\n", s.Style.RiskPosture)
	}
	if s.Style.ConfirmDestructive {
		b.WriteString("confirm_before_destructive: true\n")
	}
	if s.Style.PreferCLI {
		b.WriteString("prefer_cli_over_console: true\n")
	}
	if s.Style.DiagnosticStyle != "" {
		fmt.Fprintf(&b, "diagnostic_style: %s\n", s.Style.DiagnosticStyle)
	}

	// Freeform prose after structured fields.
	md := strings.TrimSpace(s.Markdown)
	if md != "" {
		b.WriteString("\n")
		b.WriteString(md)
		b.WriteString("\n")
	}

	b.WriteString("</user_style>")
	return b.String()
}
