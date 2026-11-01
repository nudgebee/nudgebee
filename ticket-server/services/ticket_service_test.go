package services

import (
	"strings"
	"testing"

	"nudgebee/tickets-server/models"
)

func TestTicketStatusPersistence(t *testing.T) {
	for _, tc := range []struct{ tool, requested, want string }{
		{"github", "github-project:P1:F1:O1", ""},
		{"github", "Ready", ""},
		{"github", "In Progress", ""},
		{"github", "Open", "open"},
		{"github", "CLOSED", "closed"},
		{"github", "", ""},
		{"jira", "Ready", "Ready"},
		{"gitlab", "closed", "closed"},
	} {
		t.Run(tc.tool+"/"+tc.requested, func(t *testing.T) {
			if got := ticketStatusToPersist(tc.tool, tc.requested); got != tc.want {
				t.Fatalf("local status = %q, want %q", got, tc.want)
			}
		})
	}
}

// unlinkedTicketResponse must surface the ticket that was actually created
// upstream (ID/URL/platform), not an error, and its message must tell the
// caller not to retry — a retry with no local row to dedup against is what
// created the duplicate-ticket bug in issue #39098.
func TestUnlinkedTicketResponse(t *testing.T) {
	ticket := models.Ticket{
		TicketID: "PROJ-123",
		URL:      "https://example.atlassian.net/browse/PROJ-123",
		Platform: "jira",
	}

	resp := unlinkedTicketResponse("jira", ticket)
	got := resp.Data.InsertTicketsOne

	if got.Action != ActionCreatedUnlinked {
		t.Errorf("Action = %q, want %q", got.Action, ActionCreatedUnlinked)
	}
	if got.Error != "" {
		t.Errorf("Error = %q, want empty so this isn't treated as a failure", got.Error)
	}
	if got.TicketID != ticket.TicketID {
		t.Errorf("TicketID = %q, want %q", got.TicketID, ticket.TicketID)
	}
	if got.URL != ticket.URL {
		t.Errorf("URL = %q, want %q", got.URL, ticket.URL)
	}
	if !strings.Contains(got.Message, ticket.TicketID) {
		t.Errorf("Message = %q, want it to name the ticket ID so the user can find it", got.Message)
	}
	if !strings.Contains(got.Message, "do not retry") {
		t.Errorf("Message = %q, want it to warn against retrying", got.Message)
	}
}
