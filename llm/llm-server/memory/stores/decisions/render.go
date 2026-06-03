package decisions

import (
	"fmt"
	"strings"
)

// Render produces the <past_decisions> prompt block for a list of decisions.
// Includes decision type and subject; empty input returns "".
func Render(decs []Decision) string {
	if len(decs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<past_decisions>\n")
	for _, d := range decs {
		fmt.Fprintf(&b, "- [%s] %s\n", d.DecisionType, d.Subject)
	}
	b.WriteString("</past_decisions>")
	return b.String()
}
