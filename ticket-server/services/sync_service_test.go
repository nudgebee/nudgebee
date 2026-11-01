package services

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"nudgebee/tickets-server/models"
	ticketmgr "nudgebee/tickets-server/services/ticket"

	"github.com/gin-gonic/gin"
)

// captureSlog swaps the default slog logger for one writing text-formatted
// records to a buffer, restoring the original on test cleanup.
func captureSlog(t *testing.T, level slog.Level) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	original := slog.Default()
	t.Cleanup(func() { slog.SetDefault(original) })
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: level})))
	return &buf
}

// stubTicketManager answers Get from a canned map keyed by ticket ID, or an
// error for IDs not present (or explicitly listed in errs).
type stubTicketManager struct {
	ticketmgr.TicketManager
	byID map[string]*models.Ticket
	errs map[string]error
	gets []string // ticket IDs Get was called with, in order
}

func (m *stubTicketManager) Get(_ *gin.Context, _ models.TicketConfigurations, ticketID string) (*models.Ticket, error) {
	m.gets = append(m.gets, ticketID)
	if err, ok := m.errs[ticketID]; ok {
		return nil, err
	}
	if t, ok := m.byID[ticketID]; ok {
		return t, nil
	}
	return nil, errors.New("not found")
}

// A ServiceNow (or any non-Jira) ticket resolved upstream must produce an
// update once synced — this was a bug: SyncTickets
// used to hard-skip every tool but Jira, so a ServiceNow incident closed
// upstream stayed "open" in Nudgebee forever.
func TestCollectManagerUpdates_NonJiraToolProducesUpdate(t *testing.T) {
	stored := []models.Ticket{
		{ID: "row-1", TicketID: "INC001", Status: "open"},
	}
	manager := &stubTicketManager{byID: map[string]*models.Ticket{
		"INC001": {TicketID: "INC001", Status: "Resolved"},
	}}

	updates := collectManagerUpdates(newSafeGinContext(), manager, models.TicketConfigurations{Tool: "servicenow"}, stored)

	if len(updates) != 1 {
		t.Fatalf("got %d updates, want 1", len(updates))
	}
	if updates[0].rowID != "row-1" || updates[0].fields.Status != "Resolved" {
		t.Fatalf("update = %+v, want rowID=row-1 status=Resolved", updates[0])
	}
}

func TestCollectManagerUpdates_NoChangeProducesNoUpdate(t *testing.T) {
	stored := []models.Ticket{{ID: "row-1", TicketID: "INC001", Status: "open"}}
	manager := &stubTicketManager{byID: map[string]*models.Ticket{
		"INC001": {TicketID: "INC001", Status: "open"},
	}}

	updates := collectManagerUpdates(newSafeGinContext(), manager, models.TicketConfigurations{Tool: "servicenow"}, stored)
	if len(updates) != 0 {
		t.Fatalf("got %d updates, want 0", len(updates))
	}
}

// A field the platform reports blank (unassigned, no severity concept) must
// not clear a previously recorded value — only a platform-reported value
// overwrites what's stored.
func TestDiffTicketUpdate_BlankFieldDoesNotClearStoredValue(t *testing.T) {
	stored := models.Ticket{Status: "open", Severity: "P2", Assignee: "jdoe"}
	current := models.Ticket{Status: "closed"} // severity/assignee left blank, as GitHub/GitLab always report

	fields, update := diffTicketUpdate(stored, current)
	if !update {
		t.Fatal("expected an update for the changed status")
	}
	if fields.Status != "closed" {
		t.Errorf("Status = %q, want closed", fields.Status)
	}
	if fields.Severity != "" {
		t.Errorf("Severity = %q, want empty (blank platform field must not clear stored value)", fields.Severity)
	}
	if fields.Assignee != "" {
		t.Errorf("Assignee = %q, want empty (blank platform field must not clear stored value)", fields.Assignee)
	}
}

// consecutiveFetchFailureLimit consecutive errors abort the remaining tickets
// for that configuration's run, but a success in between resets the streak.
func TestCollectManagerUpdates_AbortsAfterConsecutiveFailures(t *testing.T) {
	stored := []models.Ticket{
		{ID: "row-1", TicketID: "T1", Status: "open"},
		{ID: "row-2", TicketID: "T2", Status: "open"},
		{ID: "row-3", TicketID: "T3", Status: "open"},
		{ID: "row-4", TicketID: "T4", Status: "open"}, // never reached
	}
	manager := &stubTicketManager{
		byID: map[string]*models.Ticket{},
		errs: map[string]error{
			"T1": errors.New("boom"),
			"T2": errors.New("boom"),
			"T3": errors.New("boom"),
		},
	}

	collectManagerUpdates(newSafeGinContext(), manager, models.TicketConfigurations{Tool: "github"}, stored)

	if len(manager.gets) != 3 {
		t.Fatalf("Get was called %d times, want 3 (abort before T4)", len(manager.gets))
	}
}

func TestCollectManagerUpdates_SuccessResetsFailureStreak(t *testing.T) {
	stored := []models.Ticket{
		{ID: "row-1", TicketID: "T1", Status: "open"},
		{ID: "row-2", TicketID: "T2", Status: "open"},
		{ID: "row-3", TicketID: "T3", Status: "open"},
		{ID: "row-4", TicketID: "T4", Status: "open"},
	}
	manager := &stubTicketManager{
		byID: map[string]*models.Ticket{
			"T2": {TicketID: "T2", Status: "open"}, // success, resets the streak
			"T4": {TicketID: "T4", Status: "closed"},
		},
		errs: map[string]error{
			"T1": errors.New("boom"),
			"T3": errors.New("boom"),
		},
	}

	updates := collectManagerUpdates(newSafeGinContext(), manager, models.TicketConfigurations{Tool: "github"}, stored)

	if len(manager.gets) != 4 {
		t.Fatalf("Get was called %d times, want 4 (no abort, since no 3 consecutive failures)", len(manager.gets))
	}
	if len(updates) != 1 || updates[0].rowID != "row-4" {
		t.Fatalf("updates = %+v, want a single update for row-4", updates)
	}
}

// A ticket deleted upstream (404/410) is a permanent, ticket-specific
// condition, not a transient one — it must never count toward the
// consecutive-failure abort guard, or a backlog containing a few deleted
// tickets would stop sync from ever reaching healthy ones after them
// (this is exactly why GitHub's 407-ticket
// backlog, which has deleted issues scattered through it, never synced a
// single ticket, while ServiceNow's much smaller backlog did).
func TestCollectManagerUpdates_NotFoundDoesNotCountTowardFailureLimit(t *testing.T) {
	stored := []models.Ticket{
		{ID: "row-1", TicketID: "T1", Status: "open"},
		{ID: "row-2", TicketID: "T2", Status: "open"},
		{ID: "row-3", TicketID: "T3", Status: "open"},
		{ID: "row-4", TicketID: "T4", Status: "open"}, // must still be reached
	}
	manager := &stubTicketManager{
		byID: map[string]*models.Ticket{
			"T4": {TicketID: "T4", Status: "closed"},
		},
		errs: map[string]error{
			"T1": fmt.Errorf("github issue not found: %w", ticketmgr.ErrNotFound),
			"T2": fmt.Errorf("github issue not found: %w", ticketmgr.ErrNotFound),
			"T3": fmt.Errorf("github issue not found: %w", ticketmgr.ErrNotFound),
		},
	}

	updates := collectManagerUpdates(newSafeGinContext(), manager, models.TicketConfigurations{Tool: "github"}, stored)

	if len(manager.gets) != 4 {
		t.Fatalf("Get was called %d times, want 4 (not-found errors must not abort)", len(manager.gets))
	}
	if len(updates) != 1 || updates[0].rowID != "row-4" {
		t.Fatalf("updates = %+v, want a single update for row-4", updates)
	}
}

// Skipping not-found tickets must stay visible in production: the per-ticket
// line is Debug (filtered out by main.go's Info floor), so a summary line
// carries the signal — Info for ordinary backlog noise, Warn when every
// ticket in the run came back not-found, which usually means a config
// pointing at the wrong project/repo rather than a few deleted tickets.
func TestCollectManagerUpdates_NotFoundSummaryLogLevel(t *testing.T) {
	t.Run("some not found logs at Info", func(t *testing.T) {
		buf := captureSlog(t, slog.LevelInfo)
		stored := []models.Ticket{
			{ID: "row-1", TicketID: "T1", Status: "open"},
			{ID: "row-2", TicketID: "T2", Status: "open"},
		}
		manager := &stubTicketManager{
			byID: map[string]*models.Ticket{"T2": {TicketID: "T2", Status: "open"}},
			errs: map[string]error{"T1": fmt.Errorf("not found: %w", ticketmgr.ErrNotFound)},
		}

		collectManagerUpdates(newSafeGinContext(), manager, models.TicketConfigurations{Tool: "github"}, stored)

		out := buf.String()
		if !strings.Contains(out, "level=INFO") || !strings.Contains(out, "not_found_count=1") {
			t.Fatalf("log output = %q, want an INFO summary with not_found_count=1", out)
		}
		if strings.Contains(out, "level=WARN") {
			t.Fatalf("log output = %q, want no WARN (not every ticket was not-found)", out)
		}
	})

	t.Run("every ticket not found logs at Warn", func(t *testing.T) {
		buf := captureSlog(t, slog.LevelInfo)
		stored := []models.Ticket{
			{ID: "row-1", TicketID: "T1", Status: "open"},
			{ID: "row-2", TicketID: "T2", Status: "open"},
		}
		manager := &stubTicketManager{
			errs: map[string]error{
				"T1": fmt.Errorf("not found: %w", ticketmgr.ErrNotFound),
				"T2": fmt.Errorf("not found: %w", ticketmgr.ErrNotFound),
			},
		}

		collectManagerUpdates(newSafeGinContext(), manager, models.TicketConfigurations{Tool: "github"}, stored)

		out := buf.String()
		if !strings.Contains(out, "level=WARN") || !strings.Contains(out, "not_found_count=2") {
			t.Fatalf("log output = %q, want a WARN summary with not_found_count=2 (every ticket missing signals misconfiguration)", out)
		}
	})

	t.Run("no not-found tickets logs no summary", func(t *testing.T) {
		buf := captureSlog(t, slog.LevelInfo)
		stored := []models.Ticket{{ID: "row-1", TicketID: "T1", Status: "open"}}
		manager := &stubTicketManager{byID: map[string]*models.Ticket{"T1": {TicketID: "T1", Status: "open"}}}

		collectManagerUpdates(newSafeGinContext(), manager, models.TicketConfigurations{Tool: "github"}, stored)

		if out := buf.String(); strings.Contains(out, "not found upstream") {
			t.Fatalf("log output = %q, want no not-found summary when nothing was skipped", out)
		}
	})
}

// Jira issue keys are case-insensitive; the batch sync path matches a search
// result back to its stored ticket by key, so a case mismatch there would
// silently skip that ticket's update.
func TestNormalizeJiraKey(t *testing.T) {
	cases := map[string]string{
		"PROJ-123":  "PROJ-123",
		"proj-123":  "PROJ-123",
		" PROJ-123": "PROJ-123",
		"Proj-123 ": "PROJ-123",
	}
	for in, want := range cases {
		if got := normalizeJiraKey(in); got != want {
			t.Errorf("normalizeJiraKey(%q) = %q, want %q", in, got, want)
		}
	}
}

// A TicketManager.Get implementation receives the concrete *gin.Context, not
// just the context.Context interface, so it could call a gin-specific method
// like GetHeader or ClientIP that dereferences Request directly — those would
// nil-panic the background sync goroutine on a zero-value &gin.Context{}.
func TestNewSafeGinContext_HasNonNilRequest(t *testing.T) {
	ctx := newSafeGinContext()
	if ctx.Request == nil {
		t.Fatal("Request is nil; a gin.Context method that reads it directly would panic")
	}
}

// isAuthFailure is what SyncConfigurations relies on to tell a real
// credential rejection (which should disable the integration) apart from a
// transient failure (which should not — a brief
// upstream outage used to disable the integration permanently since any
// error, transient or not, was treated as fatal). Every tool's SDK formats
// its error differently, so each one is exercised with its actual wording.
func TestIsAuthFailure(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"jira 401", errors.New("request failed. Please analyze the request body for more details. Status code: 401"), true},
		{"jira 403", errors.New("request failed. Please analyze the request body for more details. Status code: 403"), true},
		{"github 401", errors.New("GET https://api.github.com/user: 401 Bad credentials []"), true},
		{"gitlab 403", errors.New("GET https://gitlab.example.com/api/v4/projects: 403 Forbidden"), true},
		{"pagerduty 401", errors.New("HTTP response failed with status code 401 and no JSON error object was present"), true},
		{"zenduty 403", errors.New("request failed with status 403: forbidden"), true},
		{"freshdesk 401", errors.New("freshdesk GET /tickets failed: authentication failed (invalid API key)"), true},
		{"freshdesk 403", errors.New("freshdesk GET /groups failed: API key lacks permission"), true},
		{"servicenow 401", errors.New("servicenow authentication failed: invalid username or password"), true},
		{"servicenow 403", errors.New("servicenow authentication failed: user lacks permission to read the incident table"), true},
		{"timeout", errors.New("context deadline exceeded"), false},
		{"server error", errors.New("request failed. Please analyze the request body for more details. Status code: 503"), false},
		{"rate limited", errors.New("request failed with status 429: too many requests"), false},
		{"dns failure", errors.New(`Get "https://jira.example.com": dial tcp: lookup jira.example.com: no such host`), false},
		{"transient error with ticket ID containing 403", errors.New(`Get "https://jira.example.com/rest/api/2/issue/PROJ-403": dial tcp: lookup jira.example.com: no such host`), false},
		{"nil error", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isAuthFailure(c.err); got != c.want {
				t.Errorf("isAuthFailure(%q) = %v, want %v", c.err, got, c.want)
			}
		})
	}
}
