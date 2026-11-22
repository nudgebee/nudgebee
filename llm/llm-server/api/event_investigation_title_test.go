package api

import (
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/analytics"
)

// The Investigations tab and the investigation counts find an investigation's
// event by the first UUID in its conversation title. After step 1 moved to the
// plain LLM agent (#41579) every title became "Processing LLM Request" and both
// went to zero, so the title is pinned here.
func TestInvestigationConversationTitle_CarriesEventId(t *testing.T) {
	eventId := "aa3f7199-b608-40dc-80a3-61c918b64e0a"
	title := investigationConversationTitle(eventId)

	uuid := regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	assert.Equal(t, eventId, uuid.FindString(title), "consumers take the first UUID in the title")
	assert.NotEqual(t, analytics.EventDetailsRetrievalTitle, title)
	assert.False(t, strings.HasPrefix(title, analytics.EventDetailsRetrievalTitlePrefix), "excluded from investigation counts")
}

// Any step in the investigation's session (event-<fingerprint>) can be the one
// that creates its conversation — step 1 is skipped when a summary exists — so
// every one names it. The RCA report runs in its own event-rca- session and stays
// unnamed, or each investigation would be counted twice.
func TestEventAnalyzer_InvestigationSessionStepsSetTitle(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "event_analyzer.go", nil, 0)
	require.NoError(t, err)

	calls := 0
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || !isSelector(call.Fun, "core", "HandleConversationSessionRequest") {
			return true
		}
		calls++
		require.Greater(t, len(call.Args), 4)
		session, ok := call.Args[4].(*ast.Ident)
		require.True(t, ok, "session argument of call %d is not a plain variable", calls)

		titled := false
		for _, arg := range call.Args {
			if option, ok := arg.(*ast.CallExpr); ok && isSelector(option.Fun, "core", "ConversationSessionRequestWithTitle") {
				titled = true
			}
		}
		switch session.Name {
		case "parentConversationId":
			assert.True(t, titled, "call %d runs in the investigation session but sets no title", calls)
		case "parentSessionId":
			assert.False(t, titled, "call %d is the RCA report session and must not carry the investigation title", calls)
		default:
			t.Errorf("call %d uses session %q; decide whether it names the investigation conversation", calls, session.Name)
		}
		return true
	})
	assert.Positive(t, calls)
}

func isSelector(expr ast.Expr, pkg, name string) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != name {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	return ok && ident.Name == pkg
}
