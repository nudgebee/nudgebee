//go:build e2e

package agents

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
)

// sqlToolNames are the tools whose results go through the SQL row cap.
var sqlToolNames = map[string]bool{
	"events_execute": true, "anomaly_execute": true, "recommendation_execute": true,
	"recommendation_resolution_execute": true, "security_execute": true, "list_events": true,
}

// fetchesMore matches a follow-up query that pages, raises its LIMIT, or counts.
var fetchesMore = regexp.MustCompile(`(?i)\b(offset|limit|count|group by)\b`)

// TestSqlRowCapNoteIsActedOn: with the real model on TEST_ACCOUNT, after a cut SQL result
// the model's next SQL call must page, raise its LIMIT, or count (#40194).
func TestSqlRowCapNoteIsActedOn(t *testing.T) {
	account, user := os.Getenv("TEST_ACCOUNT"), os.Getenv("TEST_USER")
	if account == "" || user == "" {
		t.Skip("TEST_ACCOUNT / TEST_USER not set")
	}
	sc := security.NewRequestContextForSuperAdmin()

	cases := []struct {
		session string
		agent   core.NBAgent
		query   string
	}{
		{"ut-sql-row-cap-recommendations", &FinOpsAgent{accountId: account},
			"List every open recommendation rule with its category, severity and count. I need the complete list, not a sample."},
		{"ut-sql-row-cap-events", AgentEvents{},
			"Which events fired in this account in the last 24 hours? Give me the complete list with titles."},
	}
	sawCut := false
	for _, tc := range cases {
		t.Run(tc.session, func(t *testing.T) {
			require.NoError(t, core.DeleteConversationBySession(tc.session, account, user))
			resp, err := core.HandleConversationSessionRequest(sc, tc.agent, user, account, tc.session, tc.query)
			require.NoError(t, err)

			steps := resp.AgentStepResponse
			for i, step := range steps {
				name := step.Response.Name
				if step.Call.FunctionCall != nil {
					name = step.Call.FunctionCall.Name
				}
				if !sqlToolNames[name] || !strings.Contains(step.Response.Content, `"has_more":true`) {
					continue
				}
				sawCut = true
				t.Logf("step %d: %s was cut; args: %s", i, name, args(step))
				var next *core.ToolInvocation
				for j := i + 1; j < len(steps); j++ {
					if f := steps[j].Call.FunctionCall; f != nil && sqlToolNames[f.Name] {
						next = &steps[j]
						break
					}
				}
				require.NotNil(t, next, "after a cut result the model must fetch more")
				t.Logf("next: %s args: %s", next.Call.FunctionCall.Name, args(*next))
				assert.Regexp(t, fetchesMore, args(*next), "the follow-up must page, raise its LIMIT, or count")
				return
			}
			t.Logf("no cut result in %d steps", len(steps))
		})
	}
	assert.True(t, sawCut, "at least one question must hit the row cap for this test to prove anything")
}

func args(step core.ToolInvocation) string {
	if step.Call.FunctionCall == nil {
		return ""
	}
	return fmt.Sprint(step.Call.FunctionCall.Arguments)
}

var offsetArg = regexp.MustCompile(`(?i)\boffset\s+(\d+)`)

// TestSqlRowCapPagingHasNoOverlap: when a listing needs more rows than one call
// returns, the next page's OFFSET must be the old OFFSET + rows shown, and it must
// not repeat a row of the page before (#40194).
func TestSqlRowCapPagingHasNoOverlap(t *testing.T) {
	account, user := os.Getenv("TEST_ACCOUNT"), os.Getenv("TEST_USER")
	if account == "" || user == "" {
		t.Skip("TEST_ACCOUNT / TEST_USER not set")
	}
	const session = "ut-sql-row-cap-paging"
	require.NoError(t, core.DeleteConversationBySession(session, account, user))
	resp, err := core.HandleConversationSessionRequest(security.NewRequestContextForSuperAdmin(), AgentEvents{}, user, account, session,
		"List the 150 most recent events with their id and title, newest first. Show every one of the 150; do not summarize, group or count.")
	require.NoError(t, err)

	steps := resp.AgentStepResponse
	for i, step := range steps {
		f := step.Call.FunctionCall
		if f == nil || !sqlToolNames[f.Name] || !strings.Contains(step.Response.Content, `"has_more":true`) {
			continue
		}
		var page struct {
			Rows     []map[string]any `json:"rows"`
			Returned int              `json:"returned"`
		}
		require.NoError(t, json.Unmarshal([]byte(step.Response.Content), &page))
		for j := i + 1; j < len(steps); j++ {
			g := steps[j].Call.FunctionCall
			if g == nil || g.Name != f.Name || !offsetArg.MatchString(g.Arguments) {
				continue
			}
			t.Logf("page 1 (%d rows): %s", page.Returned, f.Arguments)
			t.Logf("page 2: %s", g.Arguments)
			assert.Equal(t, offsetOf(f.Arguments)+page.Returned, offsetOf(g.Arguments), "next OFFSET = current OFFSET + rows shown")
			seen := map[any]bool{}
			for _, r := range page.Rows {
				seen[r["id"]] = true
			}
			next := pageRows(t, steps[j].Response.Content)
			require.NotEmpty(t, next)
			for _, r := range next {
				require.NotNil(t, r["id"], "rows must carry id to check overlap")
				assert.False(t, seen[r["id"]], "page 2 repeats row %v from page 1", r["id"])
			}
			return
		}
	}
	t.Fatalf("no cut result followed by an OFFSET page in %d steps", len(steps))
}

func offsetOf(query string) int {
	m := offsetArg.FindStringSubmatch(query)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

// pageRows reads a SQL tool's Data: a plain array, or the cut wrapper's rows.
func pageRows(t *testing.T, content string) []map[string]any {
	var rows []map[string]any
	if json.Unmarshal([]byte(content), &rows) == nil {
		return rows
	}
	var cut struct {
		Rows []map[string]any `json:"rows"`
	}
	require.NoError(t, json.Unmarshal([]byte(content), &cut))
	return cut.Rows
}
