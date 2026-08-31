package core

import (
	"os"
	"strings"
	"testing"
	"time"

	"nudgebee/llm/security"

	"github.com/stretchr/testify/assert"
)

// TestHandleConversationTimeAggregatesApi_InvalidStartDate verifies that the
// handler rejects malformed start_date values up front rather than passing
// garbage into the DAO time-window filter.
func TestHandleConversationTimeAggregatesApi_InvalidStartDate(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	request := ConversationTimeAggregatesRequest{
		StartDate: "not-a-date",
		EndDate:   "2026-05-01T00:00:00Z",
	}
	_, err := HandleConversationTimeAggregatesApi(ctx, request)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "start_date")
}

// TestHandleConversationTimeAggregatesApi_InvalidEndDate mirrors the start_date
// check on the other bound — a typo'd ISO string from the UI should not
// silently degrade into a zero window.
func TestHandleConversationTimeAggregatesApi_InvalidEndDate(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	request := ConversationTimeAggregatesRequest{
		StartDate: "2026-05-01T00:00:00Z",
		EndDate:   "yesterday",
	}
	_, err := HandleConversationTimeAggregatesApi(ctx, request)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "end_date")
}

// TestHandleConversationTimeAggregatesApi_ForbiddenAccount asserts that an
// explicit account_id the caller cannot read is rejected before the DAO
// is consulted, preventing cross-account data leaks via this endpoint.
// The caller is granted one account; the request asks for a different one.
func TestHandleConversationTimeAggregatesApi_ForbiddenAccount_LegacyCoverage(t *testing.T) {
	allowedAccount := "00000000-0000-0000-0000-000000000001"
	deniedAccount := "11111111-1111-1111-1111-111111111111"

	ctx := security.NewRequestContextForTenantAccountAdmin(
		"00000000-0000-0000-0000-000000000000",
		"00000000-0000-0000-0000-000000000099",
		[]string{allowedAccount},
	)
	if ctx == nil {
		t.Skip("Skipping test: security context constructor needs DB to resolve tenant accounts")
	}

	request := ConversationTimeAggregatesRequest{
		AccountId: deniedAccount,
		StartDate: "2026-05-01T00:00:00Z",
		EndDate:   "2026-05-02T00:00:00Z",
	}
	_, err := HandleConversationTimeAggregatesApi(ctx, request)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "forbidden")
}

// TestGetConversationTimeAggregates_NoAccounts confirms the DAO short-circuits
// to a zero result (no DB query) when the caller has no readable accounts.
// This protects against accidentally running an unbounded scan if a future
// change misses the empty-list check at the handler boundary.
func TestGetConversationTimeAggregates_NoAccounts(t *testing.T) {
	if GetConversationDao() == nil {
		t.Skip("Skipping test: database not available")
	}

	filter := ConversationTimeAggregatesFilter{
		AccountIDs: nil,
		StartDate:  time.Now().Add(-24 * time.Hour),
		EndDate:    time.Now(),
	}
	result, err := GetConversationDao().GetConversationTimeAggregates(filter)

	assert.Nil(t, err)
	assert.Equal(t, ConversationTimeAggregates{}, result, "empty accountIDs must return zero without touching the DB")
}

// TestGetConversationTimeAggregates_RollupShape exercises the real SQL against
// a real database. It asserts only structural invariants — non-negativity and
// the completed/total relationship — rather than absolute values, so the test
// is stable against the live data in whatever environment it runs in.
//
// This is the smoke test the PR reviewer asked for: it would fail loudly on
// an SQL typo, a wrong CTE join, or a status-filter regression that lets
// in-progress rows through. Skips when no DB or no test account is configured.
func TestGetConversationTimeAggregates_RollupShape(t *testing.T) {
	if GetConversationDao() == nil {
		t.Skip("Skipping test: database not available")
	}
	accountID := os.Getenv("TEST_ACCOUNT")
	if accountID == "" {
		t.Skip("Skipping test: TEST_ACCOUNT not set")
	}

	filter := ConversationTimeAggregatesFilter{
		AccountIDs:     []string{accountID},
		StartDate:      time.Now().Add(-7 * 24 * time.Hour),
		EndDate:        time.Now(),
		ExcludedTitles: []string{EventDetailsRetrievalTitle},
	}
	result, err := GetConversationDao().GetConversationTimeAggregates(filter)

	assert.Nil(t, err)
	assert.GreaterOrEqual(t, result.CompletedCount, 0)
	assert.GreaterOrEqual(t, result.TotalCount, result.CompletedCount, "TotalCount must include CompletedCount")
	assert.GreaterOrEqual(t, result.TotalWallTimeSeconds, 0.0)
	assert.GreaterOrEqual(t, result.TotalAgentActiveTimeSeconds, 0.0)
	assert.GreaterOrEqual(t, result.TotalToolTimeSeconds, 0.0)

	// All three time totals are scoped to COMPLETED rows. If completed_count
	// is zero, every total must be zero — catches a regression where the
	// time CTEs accidentally widen back to all rows.
	if result.CompletedCount == 0 {
		assert.Equal(t, 0.0, result.TotalWallTimeSeconds, "wall time must be 0 when no completed rows")
		assert.Equal(t, 0.0, result.TotalAgentActiveTimeSeconds, "agent time must be 0 when no completed rows")
		assert.Equal(t, 0.0, result.TotalToolTimeSeconds, "tool time must be 0 when no completed rows")
	}
}

// TestEscapeLikePattern verifies that a literal title prefix cannot widen the
// exclusion. "%" and "_" are LIKE wildcards, so an unescaped prefix containing
// either would silently drop conversations that should have been counted.
func TestEscapeLikePattern(t *testing.T) {
	cases := []struct {
		name     string
		literal  string
		expected string
	}{
		{"plain prefix is unchanged", "Get the details of Event with id", "Get the details of Event with id"},
		{"percent is escaped", "100% done", `100\% done`},
		{"underscore is escaped", "event_id", `event\_id`},
		{"backslash is escaped first", `a\b`, `a\\b`},
		{"backslash before wildcard stays literal", `a\%`, `a\\\%`},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.expected, escapeLikePattern(testCase.literal))
		})
	}
}

// TestEventDetailsRetrievalTitlePrefix_MatchesTheUUIDBearingTitle pins the
// prefix to the title it exists for. That title carries the event id, so it
// passes the EventScoped UUID filter and was counted as an investigation —
// and its stale agent rows drove the time-saved widget negative.
func TestEventDetailsRetrievalTitlePrefix_MatchesTheUUIDBearingTitle(t *testing.T) {
	title := "Get the details of Event with id - 52b4d494-403f-4ea7-aec7-5f5e5c5b5a5d"

	assert.True(t, strings.HasPrefix(title, EventDetailsRetrievalTitlePrefix))
	assert.NotEqual(t, EventDetailsRetrievalTitle, title, "the exact-title filter cannot reach this one")
}
