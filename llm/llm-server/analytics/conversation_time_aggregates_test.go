package analytics

import (
	"strings"
	"testing"

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

// TestEventDetailsRetrievalTitlePrefix_MatchesTheUUIDBearingTitle pins the
// prefix to the title it exists for. That title carries the event id, so it
// passes the EventScoped UUID filter and was counted as an investigation —
// and its stale agent rows drove the time-saved widget negative.
func TestEventDetailsRetrievalTitlePrefix_MatchesTheUUIDBearingTitle(t *testing.T) {
	title := "Get the details of Event with id - 52b4d494-403f-4ea7-aec7-5f5e5c5b5a5d"

	assert.True(t, strings.HasPrefix(title, EventDetailsRetrievalTitlePrefix))
	assert.NotEqual(t, EventDetailsRetrievalTitle, title, "the exact-title filter cannot reach this one")
}
