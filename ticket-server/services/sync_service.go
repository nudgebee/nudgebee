package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"nudgebee/tickets-server/clients"
	"nudgebee/tickets-server/common"
	"nudgebee/tickets-server/database"
	"nudgebee/tickets-server/models"
	"nudgebee/tickets-server/services/ticket"
	"nudgebee/tickets-server/services/tools"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
)

// fetchConfigurationList loads all enabled ticketing integrations with their
// config values in a single JOIN query instead of 2N+1 round-trips (1 listing
// + N metadata re-fetches + N config-value fetches).
func fetchConfigurationList() ([]models.TicketConfigurations, error) {
	dbManager, err := database.GetDatabaseManager()
	if err != nil {
		slog.Error("Unable to get database manager:", "error", slog.AnyValue(err))
		return nil, err
	}

	query := `
		SELECT
			i.id,
			i.tenant_id,
			i.type,
			i.name,
			i.status,
			i.created_by,
			COALESCE(icv.name, '')                AS config_name,
			COALESCE(icv.value, '')               AS config_value,
			COALESCE(icv.is_encrypted, false)     AS is_encrypted
		FROM integrations i
		LEFT JOIN integration_config_values icv ON i.id = icv.integration_id
		WHERE i.type IN ('jira', 'github', 'gitlab', 'servicenow', 'pagerduty', 'zenduty', 'freshdesk')
		  AND i.status = 'enabled'
		ORDER BY i.created_at DESC, i.id
	`

	type joinRow struct {
		ID          string  `db:"id"`
		TenantID    string  `db:"tenant_id"`
		Type        string  `db:"type"`
		Name        string  `db:"name"`
		Status      string  `db:"status"`
		CreatedBy   *string `db:"created_by"`
		ConfigName  string  `db:"config_name"`
		ConfigValue string  `db:"config_value"`
		IsEncrypted bool    `db:"is_encrypted"`
	}

	var rows []joinRow
	if err = dbManager.Select(&rows, query); err != nil {
		slog.Error("Unable to fetch integrations with config values:", "error", slog.AnyValue(err))
		return nil, err
	}

	seen := make(map[string]int, len(rows))
	configurations := make([]models.TicketConfigurations, 0, len(rows))
	failedIntegrations := make(map[string]bool)

	for _, r := range rows {
		if failedIntegrations[r.ID] {
			continue
		}

		idx, exists := seen[r.ID]
		if !exists {
			idx = len(configurations)
			seen[r.ID] = idx
			configurations = append(configurations, models.TicketConfigurations{
				ID:        r.ID,
				Tenant:    r.TenantID,
				Tool:      r.Type,
				Name:      r.Name,
				Status:    r.Status,
				CreatedBy: r.CreatedBy,
				IsActive:  r.Status == "enabled",
			})
		}

		if r.ConfigName == "" {
			continue
		}

		value := r.ConfigValue
		if r.IsEncrypted && value != "" {
			decrypted, decErr := common.Decrypt(value)
			if decErr != nil {
				slog.Error("Failed to decrypt config value:", "name", r.ConfigName, "error", decErr, "integration_id", r.ID)
				failedIntegrations[r.ID] = true
				continue
			}
			value = decrypted
		}

		cfg := &configurations[idx]
		switch r.ConfigName {
		case "url":
			cfg.URL = value
		case "username":
			cfg.Username = value
		case "password":
			cfg.Password = value
		case "auth_type":
			cfg.AuthType = value
		case "projects":
			if value != "" {
				if err := json.Unmarshal([]byte(value), &cfg.Projects); err != nil {
					slog.Warn("Failed to unmarshal projects config value", "integration_id", r.ID, "error", err)
				}
			}
		case "priorities":
			if value != "" {
				if err := json.Unmarshal([]byte(value), &cfg.Priorities); err != nil {
					slog.Warn("Failed to unmarshal priorities config value", "integration_id", r.ID, "error", err)
				}
			}
		case "users":
			if value != "" {
				if err := json.Unmarshal([]byte(value), &cfg.Users); err != nil {
					slog.Warn("Failed to unmarshal users config value", "integration_id", r.ID, "error", err)
				}
			}
		case "last_connected":
			if value != "" {
				lastConnected, parseErr := time.Parse(time.RFC3339, value)
				if parseErr == nil {
					cfg.LastConnected = &lastConnected
				}
			}
		default:
			slog.Debug("Unhandled integration config key", "name", r.ConfigName, "integration_id", r.ID)
		}
	}

	// Remove integrations that failed decryption, preserving order.
	n := 0
	for _, cfg := range configurations {
		if !failedIntegrations[cfg.ID] {
			configurations[n] = cfg
			n++
		}
	}
	for i := n; i < len(configurations); i++ {
		configurations[i] = models.TicketConfigurations{}
	}
	configurations = configurations[:n]

	return configurations, nil
}

// ListTickets returns the tickets still worth polling upstream for a config.
// Tickets already in a terminal status are excluded — the list mirrors the
// statuses api-server's ticketOutcomeForStatus settles on, so anything past
// that point can no longer change a resolution. A non-zero updatedBefore
// additionally skips rows written since the previous sync run (the tickets
// updated_at trigger bumps on every write, so freshly written-through or
// just-synced rows are already current).
func ListTickets(configurationId string, updatedBefore time.Time) ([]models.Ticket, error) {
	dbManager, err := database.GetDatabaseManager()
	if err != nil {
		return nil, fmt.Errorf("database unavailable: %v", err)
	}

	query := `
		SELECT id, integration_id,
		       COALESCE(assignee, '') as assignee,
		       COALESCE(severity, '') as severity,
		       COALESCE(status, '') as status,
		       ticket_id,
		       COALESCE(url, '') as url
		FROM tickets
		WHERE integration_id = $1
		  AND LOWER(TRIM(COALESCE(status, ''))) NOT IN
		      ('resolved', 'closed', 'done', 'complete', 'completed',
		       'rejected', 'cancelled', 'canceled', 'declined')`
	args := []interface{}{configurationId}
	if !updatedBefore.IsZero() {
		query += ` AND (updated_at IS NULL OR updated_at < $2)`
		args = append(args, updatedBefore)
	}

	var tickets []models.Ticket
	err = dbManager.Select(&tickets, query, args...)
	if err != nil {
		slog.Error("Error querying tickets", "integrationID", configurationId, "error", err)
		return nil, err
	}
	return tickets, nil
}

// lastTicketSyncAt is the start time (unix seconds) of the previous
// SyncTickets run; zero until the first run after process start.
var lastTicketSyncAt atomic.Int64

// consecutiveFetchFailureLimit aborts a config's sync for the run once this
// many upstream fetches fail in a row — correlated failures (bad credentials,
// unreachable host) would otherwise error-log once per ticket every run.
const consecutiveFetchFailureLimit = 3

func SyncTickets() {
	var updatedBefore time.Time
	if prev := lastTicketSyncAt.Swap(time.Now().Unix()); prev > 0 {
		updatedBefore = time.Unix(prev, 0).UTC()
	}

	configurations, err := fetchConfigurationList()
	if err != nil {
		slog.Error("Unable to fetch configuration list:", "error", slog.AnyValue(err))
		return
	}

	dbManager, err := database.GetDatabaseManager()
	if err != nil {
		slog.Error("Unable to get database manager for ticket sync:", "error", slog.AnyValue(err))
		return
	}

	for _, configuration := range configurations {
		tickets, err := ListTickets(configuration.ID, updatedBefore)
		if err != nil {
			slog.Error("Unable to list tickets", "error", err, "configuration_id", configuration.ID)
			continue
		}

		if len(tickets) == 0 {
			continue
		}

		if configuration.Tool == "jira" {
			if configuration.URL == "" || configuration.Password == "" {
				slog.Debug("Skipping ticket sync for incompletely configured integration", "configuration_id", configuration.ID)
				continue
			}
			syncJiraTickets(dbManager, configuration, tickets)
			continue
		}

		syncTicketsViaManager(dbManager, configuration, tickets)
	}

}

// normalizeJiraKey makes a Jira issue key comparable regardless of case or
// incidental whitespace, since Jira keys are case-insensitive but Go map
// lookups are not.
func normalizeJiraKey(key string) string {
	return strings.ToUpper(strings.TrimSpace(key))
}

// syncJiraTickets refreshes status/severity/assignee for a Jira
// configuration's still-open tickets via batched "key in (...)" JQL searches
// instead of one GET per ticket (tools.FetchIssuesByKeys), which is the
// per-GET pattern Jira Cloud rate-limits under load.
func syncJiraTickets(dbManager *database.DatabaseManager, configuration models.TicketConfigurations, tickets []models.Ticket) {
	jiraClient, err := clients.CreateJiraClient(configuration.AuthType, configuration.Username, configuration.Password, configuration.URL)
	if err != nil {
		slog.Error("Unable to create Jira client", "error", err, "configuration_id", configuration.ID)
		return
	}

	// Keyed by normalized key: Jira issue keys are case-insensitive, and the
	// batch search below returns Jira's own canonical-case key, which isn't
	// guaranteed to match a stored ticket_id byte-for-byte.
	byKey := make(map[string]models.Ticket, len(tickets))
	keys := make([]string, 0, len(tickets))
	for _, t := range tickets {
		byKey[normalizeJiraKey(t.TicketID)] = t
		keys = append(keys, t.TicketID)
	}

	issues, err := tools.FetchIssuesByKeys(context.Background(), jiraClient, keys)
	if err != nil {
		slog.Warn("Jira ticket batch sync incomplete", "configuration_id", configuration.ID, "error", slog.AnyValue(err), "fetched", len(issues), "requested", len(keys))
	}

	for _, issue := range issues {
		t, ok := byKey[normalizeJiraKey(issue.Key)]
		if !ok || issue.Fields == nil {
			continue
		}

		update := false
		updateFields := models.UpdateFields{}
		if issue.Fields.Status != nil && t.Status != issue.Fields.Status.Name {
			updateFields.Status = issue.Fields.Status.Name
			update = true
		}
		if issue.Fields.Priority != nil && t.Severity != issue.Fields.Priority.Name {
			updateFields.Severity = issue.Fields.Priority.Name
			update = true
		}
		if issue.Fields.Assignee != nil && t.Assignee != issue.Fields.Assignee.DisplayName {
			updateFields.Assignee = issue.Fields.Assignee.DisplayName
			update = true
		}
		if !update {
			continue
		}

		slog.Debug("Updating ticket fields", "ticketID", t.TicketID, "updateFields", updateFields)
		applyTicketUpdate(dbManager, t.ID, updateFields)
	}
}

// ticketUpdate pairs a tickets row id with the fields a sync pass decided to
// write for it.
type ticketUpdate struct {
	rowID  string
	fields models.UpdateFields
}

// diffTicketUpdate reports the fields that differ between a ticket's stored
// row and what the source platform currently reports. A field the platform
// left blank (e.g. GitHub/GitLab never report severity, an unassigned ticket
// has no assignee) means "unknown", not "cleared" — it never overwrites a
// stored value, mirroring the nil-field guards the Jira path uses on
// issue.Fields.
func diffTicketUpdate(stored, current models.Ticket) (models.UpdateFields, bool) {
	update := false
	fields := models.UpdateFields{}
	if current.Status != "" && stored.Status != current.Status {
		fields.Status = current.Status
		update = true
	}
	if current.Severity != "" && stored.Severity != current.Severity {
		fields.Severity = current.Severity
		update = true
	}
	if current.Assignee != "" && stored.Assignee != current.Assignee {
		fields.Assignee = current.Assignee
		update = true
	}
	return fields, update
}

// collectManagerUpdates calls manager.Get for each ticket and returns the
// rows whose fields differ from what is stored, aborting early after
// consecutiveFetchFailureLimit consecutive fetch errors (the same guard the
// Jira path's old per-ticket loop used).
func collectManagerUpdates(ctx *gin.Context, manager ticket.TicketManager, configuration models.TicketConfigurations, tickets []models.Ticket) []ticketUpdate {
	var updates []ticketUpdate
	consecutiveFailures := 0
	notFoundCount := 0
	for _, t := range tickets {
		current, err := manager.Get(ctx, configuration, t.TicketID)
		if err != nil {
			if errors.Is(err, ticket.ErrNotFound) {
				// Permanent and ticket-specific (deleted upstream, or never
				// valid) — never counts toward the consecutive-failure abort
				// below, which exists for transient/connectivity failures.
				notFoundCount++
				slog.Debug("Ticket not found upstream, skipping", "ticketID", t.TicketID, "tool", configuration.Tool)
				continue
			}
			consecutiveFailures++
			if consecutiveFailures >= consecutiveFetchFailureLimit {
				slog.Warn("Aborting ticket sync for integration after repeated fetch failures", "configuration_id", configuration.ID, "tool", configuration.Tool, "error", slog.AnyValue(err))
				break
			}
			slog.Error("Error fetching ticket details", "ticketID", t.TicketID, "tool", configuration.Tool, "error", slog.AnyValue(err))
			continue
		}
		consecutiveFailures = 0
		if current == nil {
			continue
		}

		fields, update := diffTicketUpdate(t, *current)
		if !update {
			continue
		}
		slog.Debug("Updating ticket fields", "ticketID", t.TicketID, "tool", configuration.Tool, "updateFields", fields)
		updates = append(updates, ticketUpdate{rowID: t.ID, fields: fields})
	}
	if notFoundCount > 0 {
		// A handful of not-found tickets scattered through a large backlog is
		// expected background noise; every ticket coming back not-found is the
		// one shape that actually signals a real problem (wrong project/repo
		// key, revoked access) — distinguish the two instead of relying on the
		// per-ticket Debug line above, which production's Info log level drops.
		level := slog.LevelInfo
		if notFoundCount == len(tickets) {
			level = slog.LevelWarn
		}
		slog.Log(context.Background(), level, "Ticket sync skipped tickets not found upstream",
			"configuration_id", configuration.ID, "tool", configuration.Tool,
			"not_found_count", notFoundCount, "total_tickets", len(tickets))
	}
	return updates
}

// newSafeGinContext builds a *gin.Context usable outside any HTTP request,
// for the ctx argument TicketManager implementations take. Its
// context.Context methods (Done/Err/Deadline) are no-ops either way (gin only
// wires them up once a request's own context is attached), but a non-nil
// Request also means gin.Context methods a future TicketManager
// implementation might call directly - GetHeader, ClientIP, and the like,
// none of which any current implementation uses - read an empty request
// instead of dereferencing a nil one.
func newSafeGinContext() *gin.Context {
	req, _ := http.NewRequest(http.MethodGet, "/", nil)
	return &gin.Context{Request: req}
}

// syncTicketsViaManager refreshes status/severity/assignee for every
// non-Jira tool by dispatching to its registered ticket.TicketManager.Get,
// one call per ticket.
func syncTicketsViaManager(dbManager *database.DatabaseManager, configuration models.TicketConfigurations, tickets []models.Ticket) {
	manager, ok := ticket.GetTicketManager(configuration.Tool)
	if !ok {
		slog.Debug("No ticket manager registered for tool; skipping sync", "tool", configuration.Tool, "configuration_id", configuration.ID)
		return
	}

	for _, u := range collectManagerUpdates(newSafeGinContext(), manager, configuration, tickets) {
		applyTicketUpdate(dbManager, u.rowID, u.fields)
	}
}

// applyTicketUpdate writes a ticket's diffed fields. Shared by the Jira batch
// path and the generic per-tool path.
func applyTicketUpdate(dbManager *database.DatabaseManager, ticketRowID string, fields models.UpdateFields) {
	setClauses, args := buildUpdateClauses(fields)
	if len(setClauses) == 0 {
		return
	}
	argIdx := len(args) + 1
	query := fmt.Sprintf("UPDATE tickets SET %s WHERE id = $%d", strings.Join(setClauses, ", "), argIdx)
	args = append(args, ticketRowID)
	if _, err := dbManager.Exec(query, args...); err != nil {
		slog.Info("Unable to update ticket:", "error", slog.AnyValue(err))
	}
}

// authStatusRx matches a 401/403 status the way these SDKs actually emit it —
// Jira's "Status code: 401", PagerDuty's "status code 401", ZenDuty's
// "status 403: ...", or the bare "<method> <url>: 401 ..." GitHub/GitLab
// produce — rather than a bare "401"/"403" substring, which would also match
// a Jira issue key (PROJ-403), a port number, or similar incidental digits.
var authStatusRx = regexp.MustCompile(`(?i)(?:\bstatus(?:\s+code)?\s*:?\s*|:\s+)(401|403)\b`)

// isAuthFailure reports whether err indicates rejected credentials or
// insufficient permissions (HTTP 401/403) rather than a transient failure
// (timeout, 5xx, 429, DNS, network unreachable). Freshdesk and the
// ServiceNow wrapper (interpretServiceNowError) already rewrite 401/403 into
// a human-readable phrase instead of a status code, so those are matched by
// wording instead of authStatusRx.
func isAuthFailure(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	if authStatusRx.MatchString(msg) {
		return true
	}
	msgLower := strings.ToLower(msg)
	for _, needle := range []string{"unauthorized", "forbidden", "authentication failed", "lacks permission"} {
		if strings.Contains(msgLower, needle) {
			return true
		}
	}
	return false
}

// configSyncTimeout bounds a single configuration's metadata validation so a
// hung upstream can't block the rest of this sequential loop indefinitely.
const configSyncTimeout = time.Minute

// SyncConfigurations refreshes metadata for all enabled ticketing integrations.
// Uses context.Background() since it runs as a background goroutine detached from any HTTP request.
func SyncConfigurations() {
	ctx := context.Background()

	configurationTools := map[string]bool{
		"jira":       true,
		"github":     true,
		"gitlab":     true,
		"servicenow": true,
		"pagerduty":  true,
		"zenduty":    true,
		"freshdesk":  true,
	}

	configurations, err := fetchConfigurationList()
	if err != nil {
		slog.Error("Unable to fetch configuration list:", "error", slog.AnyValue(err))
		return
	}

	dbManager, err := database.GetDatabaseManager()
	if err != nil {
		slog.Error("Unable to get database manager:", "error", slog.AnyValue(err))
		return
	}

	for _, configuration := range configurations {
		if !configurationTools[configuration.Tool] {
			continue
		}

		iterCtx, cancel := context.WithTimeout(ctx, configSyncTimeout)
		metadata, err := ValidateAndGetMetadataWithContext(iterCtx, configuration)
		cancel()
		if err != nil {
			if !isAuthFailure(err) {
				// Transient failure (timeout, 5xx, 429, DNS, unreachable host,
				// …) — leave the integration enabled so the next sync run can
				// self-heal instead of disabling it after one slow upstream.
				slog.Warn("Transient failure validating configuration; leaving enabled", "error", slog.AnyValue(err), "id", configuration.ID)
				continue
			}

			_, dbErr := dbManager.Exec(`
				UPDATE integrations
				SET status = $1, updated_at = NOW()
				WHERE id = $2
			`, "disabled", configuration.ID)
			if dbErr != nil {
				slog.Error("Unable to update integration status:", "error", slog.AnyValue(dbErr), "id", configuration.ID)
			}
			slog.Error("Disabling integration: credentials rejected", "error", slog.AnyValue(err), "id", configuration.ID)
			continue
		}

		if err := updateMetadataConfigValues(configuration.ID, metadata, configuration.CreatedBy); err != nil {
			slog.Error("Failed to update metadata config values during sync",
				"integration_id", configuration.ID, "tool", configuration.Tool, "error", err)
		}
	}
}
