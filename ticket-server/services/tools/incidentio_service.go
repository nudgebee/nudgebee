package tools

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"nudgebee/tickets-server/clients"
	"nudgebee/tickets-server/models"
	"nudgebee/tickets-server/services/ticket"
	"nudgebee/tickets-server/utils"

	"github.com/gin-gonic/gin"
)

// IncidentIOPlatform is the platform token stored on tickets created here. It
// matches the integration type registered in api-server and the
// ticket_tool_types enum value, so it must stay a single lowercase word.
const IncidentIOPlatform = "incidentio"

type IncidentIOService struct{}

var _ ticket.IncidentManager = (*IncidentIOService)(nil)

func init() {
	ticket.RegisterIncidentManager(IncidentIOPlatform, &IncidentIOService{})
}

func incidentIOClient(config models.TicketConfigurations) *clients.IncidentIOClient {
	return clients.CreateIncidentIOClient(config.Password, config.URL)
}

// incidentIOTicket normalizes an incident.io incident into a NudgeBee ticket.
//
// Two mappings are worth calling out: Status carries the status *category*
// rather than its display name, because incident.io lets each organisation
// rename statuses freely and only the category is portable; and Assignee is
// drawn from incident role assignments, since incident.io has no single
// assignee field.
func incidentIOTicket(incident *clients.IncidentIOIncident) *models.Ticket {
	if incident == nil {
		return nil
	}

	var createdAt, updatedAt *time.Time
	if parsed, err := time.Parse(time.RFC3339, incident.CreatedAt); err == nil {
		createdAt = &parsed
	}
	if parsed, err := time.Parse(time.RFC3339, incident.UpdatedAt); err == nil {
		updatedAt = &parsed
	}

	assignees := make([]string, 0, len(incident.IncidentRoleAssignments))
	for _, a := range incident.IncidentRoleAssignments {
		if a.Assignee == nil {
			continue
		}
		if name := incidentIOUserLabel(*a.Assignee); name != "" {
			assignees = append(assignees, name)
		}
	}
	var assignee string
	if len(assignees) > 0 {
		assignee = assignees[0]
	}

	var status, severity, projectKey string
	if incident.IncidentStatus != nil {
		status = incident.IncidentStatus.Category
	}
	if incident.Severity != nil {
		severity = incident.Severity.Name
	}
	if incident.IncidentType != nil {
		projectKey = incident.IncidentType.ID
	}

	return &models.Ticket{
		TicketID:    incident.ID,
		Title:       incident.Name,
		Description: incident.Summary,
		Status:      status,
		Severity:    severity,
		ProjectKey:  projectKey,
		Assignee:    assignee,
		Assignees:   assignees,
		Platform:    IncidentIOPlatform,
		URL:         incident.Permalink,
		CreatedAt:   createdAt,
		UpdatedAt:   updatedAt,
		Raw:         marshalToMap(incident),
	}
}

// incidentIOUserLabel picks the most human-readable identifier available.
func incidentIOUserLabel(u clients.IncidentIOUser) string {
	if u.Name != "" {
		return u.Name
	}
	return u.Email
}

func (s *IncidentIOService) Get(ctx *gin.Context, config models.TicketConfigurations, ticketID string) (*models.Ticket, error) {
	if err := utils.ValidateIncidentIOIncidentID(ticketID); err != nil {
		return nil, fmt.Errorf("invalid ticket ID: %w", err)
	}

	incident, err := incidentIOClient(config).GetIncident(ctx, ticketID)
	if err != nil {
		return nil, err
	}
	return incidentIOTicket(incident), nil
}

func (s *IncidentIOService) Create(ctx *gin.Context, config models.TicketConfigurations, t models.Ticket) (models.Ticket, error) {
	client := incidentIOClient(config)

	// incident.io severities are org-defined, so a severity name has to be
	// resolved against the live list. A miss is not fatal — omitting
	// severity_id lets incident.io apply its own default.
	severityID := ""
	if t.Severity != "" {
		severities, err := client.ListSeverities(ctx)
		if err != nil {
			return t, fmt.Errorf("failed to resolve incident.io severity: %w", err)
		}
		severityID = clients.FindSeverityByName(severities, t.Severity)
		if severityID == "" {
			slog.Warn("incident.io: severity not found, falling back to the account default",
				"severity", t.Severity)
		}
	}

	req := &clients.CreateIncidentIORequest{
		// incident.io requires an idempotency key on every create so a retried
		// request cannot open a duplicate incident. Derive it from the ticket's
		// own identity where possible; otherwise fall back to a timestamp.
		IdempotencyKey: incidentIOIdempotencyKey(t),
		Visibility:     "public",
		Name:           t.Title,
		Summary:        t.Description,
		SeverityID:     severityID,
		IncidentTypeID: t.ProjectKey,
		Mode:           "standard",
	}

	created, err := client.CreateIncident(ctx, req)
	if err != nil {
		slog.Error("Error creating incident.io incident", "error", slog.AnyValue(err))
		return t, err
	}

	slog.Info("incident.io incident created", "ID", created.ID, "reference", created.Reference)

	normalized := incidentIOTicket(created)
	t.TicketID = normalized.TicketID
	t.Status = normalized.Status
	t.Severity = normalized.Severity
	if t.Severity == "" {
		t.Severity = "NA"
	}
	t.URL = normalized.URL
	t.Platform = IncidentIOPlatform
	now := time.Now()
	t.CreatedAt = &now

	return t, nil
}

// incidentIOIdempotencyKey builds the required create-time idempotency key.
// ReferenceID identifies the NudgeBee event a ticket was raised for, so reusing
// it means a retry of the same event cannot open a second incident.
func incidentIOIdempotencyKey(t models.Ticket) string {
	if t.ReferenceID != "" {
		return "nudgebee-" + t.ReferenceID
	}
	if t.ID != "" {
		return "nudgebee-" + t.ID
	}
	return "nudgebee-" + strconv.FormatInt(time.Now().UnixNano(), 10)
}

func (s *IncidentIOService) List(ctx *gin.Context, config models.TicketConfigurations, params models.ListParams) (*models.ListResult, error) {
	client := incidentIOClient(config)

	limit := normalizeLimit(params.Limit)
	offset := normalizeOffset(params.Offset)

	// incident.io paginates by opaque cursor and has no offset parameter, but
	// ListParams is provider-agnostic and carries one. Over-fetch by the offset
	// and trim below, so an offset actually advances the page instead of being
	// silently dropped — which would make page 2 return page 1.
	queryParams := map[string]string{
		"page_size": strconv.Itoa(limit + offset),
	}
	if params.ProjectKey != "" {
		queryParams["incident_type_id"] = params.ProjectKey
	}
	// incident.io filters statuses by category using its bracketed filter
	// syntax; an unmappable token is dropped rather than sent verbatim, which
	// would be rejected as a bad request.
	if category := clients.NormalizeIncidentIOStatusCategory(params.Status); category != "" {
		queryParams["status_category[one_of]"] = category
	}

	incidents, meta, err := client.ListIncidents(ctx, queryParams)
	if err != nil {
		return nil, err
	}

	// Drop the over-fetched leading rows to honor the requested offset.
	if offset > 0 {
		if offset >= len(incidents) {
			incidents = incidents[:0]
		} else {
			incidents = incidents[offset:]
		}
	}

	tickets := make([]models.Ticket, 0, len(incidents))
	for i := range incidents {
		if normalized := incidentIOTicket(&incidents[i]); normalized != nil {
			tickets = append(tickets, *normalized)
		}
	}

	// incident.io paginates by cursor, not offset, so total comes from the
	// response metadata; fall back to the page length when it is absent.
	total := meta.TotalRecordCount
	if total == 0 {
		total = len(tickets)
	}

	return &models.ListResult{
		Tickets: tickets,
		Total:   total,
		Limit:   limit,
		Offset:  offset,
	}, nil
}

func (s *IncidentIOService) GetCreateMeta(ctx *gin.Context, config models.TicketConfigurations, projectKey string) (interface{}, error) {
	client := incidentIOClient(config)

	incidentTypes, err := client.ListIncidentTypes(ctx)
	if err != nil {
		return nil, err
	}
	serviceValues := make([]interface{}, len(incidentTypes))
	for i, it := range incidentTypes {
		serviceValues[i] = map[string]interface{}{
			"id":    it.ID,
			"name":  it.Name,
			"value": it.ID,
		}
	}

	severities, err := client.ListSeverities(ctx)
	if err != nil {
		return nil, err
	}
	severityValues := make([]interface{}, len(severities))
	for i, sev := range severities {
		// Value is the severity NAME, not its ID: Create resolves names back to
		// IDs, and the same ticket.Severity string has to make sense when it is
		// echoed into the tickets table and the UI.
		severityValues[i] = map[string]interface{}{
			"id":    sev.ID,
			"name":  sev.Name,
			"value": sev.Name,
		}
	}

	// Users are optional metadata — an API key without user-read scope should
	// still be able to open incidents.
	assigneeValues := []interface{}{}
	if users, userErr := client.ListUsers(ctx); userErr != nil {
		slog.Warn("incident.io: failed to fetch users for create-meta", "error", userErr)
	} else {
		assigneeValues = make([]interface{}, len(users))
		for i, u := range users {
			assigneeValues[i] = map[string]interface{}{
				"id":    u.ID,
				"name":  incidentIOUserLabel(u),
				"value": u.ID,
			}
		}
	}

	template := Template{
		Name: "incident.io Incident",
		Fields: map[string]FieldInfo{
			// incident.io has no "service"; incident type is the closest
			// grouping, and it is keyed as "service" so the frontend's existing
			// provider-agnostic project selector picks it up unchanged.
			"service": {
				AllowedValues: serviceValues,
				Key:           "service",
				Name:          "Incident Type",
				Required:      true,
				Type:          "select",
			},
			"assignee": {
				AllowedValues: assigneeValues,
				Key:           "assignee",
				Name:          "Assignee",
				Required:      false,
				Type:          "select",
			},
			"severity": {
				AllowedValues: severityValues,
				Key:           "severity",
				Name:          "Severity",
				Required:      false,
				Type:          "select",
				Group:         FieldGroupSeverity,
			},
			"summary": {
				AllowedValues: nil,
				Key:           "summary",
				Name:          "Name",
				Required:      true,
				Type:          "string",
				Group:         FieldGroupTitle,
			},
			"description": {
				AllowedValues: nil,
				Key:           "description",
				Name:          "Summary",
				Required:      false,
				Type:          "string",
				Group:         FieldGroupDescription,
			},
		},
	}

	// Match the Jira/GitHub shape: {"data": [Template, ...]}.
	return map[string]interface{}{"data": []Template{template}}, nil
}

// AddComment is not supported.
//
// incident.io exposes incident updates read-only (GET /v2/incident_updates);
// updates are authored through Slack or the dashboard, and the public API has
// no write endpoint for them. Returning an explicit error rather than a silent
// no-op matches the PagerDuty/ZenDuty contract for unsupported operations, so a
// workflow author sees the failure instead of assuming the comment landed.
func (s *IncidentIOService) AddComment(ctx *gin.Context, config models.TicketConfigurations, t models.Ticket) error {
	return fmt.Errorf("incident.io does not support adding comments: incident updates are read-only in the public API")
}

func (s *IncidentIOService) GetComments(ctx *gin.Context, config models.TicketConfigurations, ticketID string) ([]models.Comments, error) {
	if err := utils.ValidateIncidentIOIncidentID(ticketID); err != nil {
		return nil, fmt.Errorf("invalid ticket ID: %w", err)
	}

	updates, err := incidentIOClient(config).ListIncidentUpdates(ctx, ticketID)
	if err != nil {
		return nil, err
	}

	comments := make([]models.Comments, 0, len(updates))
	for _, u := range updates {
		comments = append(comments, models.Comments{
			Author:  incidentIOUpdaterLabel(u),
			Comment: u.Message,
			Created: u.CreatedAt,
			Updated: u.CreatedAt,
		})
	}
	return comments, nil
}

// incidentIOUpdaterLabel names whoever posted an update. incident.io attributes
// updates to a user, an API key, or a workflow, so an update posted by an
// integration has no user at all — falling back to the API key name keeps the
// comment attributable instead of blank.
func incidentIOUpdaterLabel(u clients.IncidentIOIncidentUpdate) string {
	if u.Updater == nil {
		return ""
	}
	if u.Updater.User != nil {
		if label := incidentIOUserLabel(*u.Updater.User); label != "" {
			return label
		}
	}
	if u.Updater.APIKey != nil {
		return u.Updater.APIKey.Name
	}
	return ""
}

func (s *IncidentIOService) Acknowledge(ctx *gin.Context, config models.TicketConfigurations, incidentID string) error {
	// "Acknowledged" has no incident.io equivalent; the closest intent is
	// moving the incident out of triage into an active status.
	return s.moveToCategory(ctx, config, incidentID, clients.IncidentIOCategoryActive, "")
}

// Escalate is not supported.
//
// incident.io escalations are a separate product surface (escalation paths and
// the alerts API) rather than a property of an incident, so there is nothing to
// escalate an existing incident *to* through this integration. Failing loudly
// beats ZenDuty's silent no-op.
func (s *IncidentIOService) Escalate(ctx *gin.Context, config models.TicketConfigurations, incidentID string, escalationPolicy string) error {
	return fmt.Errorf("incident.io does not support escalating an existing incident via this integration")
}

func (s *IncidentIOService) Resolve(ctx *gin.Context, config models.TicketConfigurations, incidentID string, resolution string) error {
	return s.moveToCategory(ctx, config, incidentID, clients.IncidentIOCategoryClosed, resolution)
}

// moveToCategory transitions an incident to the lowest-ranked status in the
// requested category, optionally appending a note to the summary.
func (s *IncidentIOService) moveToCategory(ctx *gin.Context, config models.TicketConfigurations, incidentID, category, note string) error {
	if err := utils.ValidateIncidentIOIncidentID(incidentID); err != nil {
		return fmt.Errorf("invalid incident ID: %w", err)
	}

	client := incidentIOClient(config)

	statuses, err := client.ListIncidentStatuses(ctx)
	if err != nil {
		return fmt.Errorf("failed to resolve incident.io statuses: %w", err)
	}
	target := clients.FindStatusByCategory(statuses, category)
	if target == nil {
		return fmt.Errorf("incident.io account has no status in the %q category", category)
	}

	payload := clients.IncidentIOEditPayload{IncidentStatusID: target.ID}

	// incident.io has no notes API, so a resolution message can only be
	// preserved on the summary. Read the current summary and append rather than
	// replace it — overwriting would discard the incident's description.
	if strings.TrimSpace(note) != "" {
		incident, getErr := client.GetIncident(ctx, incidentID)
		if getErr != nil {
			return fmt.Errorf("failed to read incident.io incident before updating summary: %w", getErr)
		}
		payload.Summary = appendIncidentIONote(incident.Summary, note)
	}

	_, err = client.EditIncident(ctx, incidentID, payload, true)
	return err
}

// appendIncidentIONote appends a timestamped note to an existing summary.
func appendIncidentIONote(summary, note string) string {
	entry := fmt.Sprintf("[NudgeBee %s] %s", time.Now().UTC().Format("02 Jan 2006 15:04:05 UTC"), note)
	if strings.TrimSpace(summary) == "" {
		return entry
	}
	return summary + "\n\n" + entry
}

func (s *IncidentIOService) Transition(ctx *gin.Context, config models.TicketConfigurations, ticketID string, status string) error {
	category := clients.NormalizeIncidentIOStatusCategory(status)
	if category == "" {
		return fmt.Errorf("unsupported status for incident.io: %q (expected one of triage, active, post_incident, resolved, declined, canceled)", status)
	}
	return s.moveToCategory(ctx, config, ticketID, category, "")
}

func (s *IncidentIOService) Update(ctx *gin.Context, config models.TicketConfigurations, ticketID string, updateFields models.UpdateFields) error {
	if err := utils.ValidateIncidentIOIncidentID(ticketID); err != nil {
		return fmt.Errorf("invalid ticket ID: %w", err)
	}

	// Role assignments and labels are not writable through this integration;
	// reject them so the workflow author sees an explicit error rather than a
	// partially-applied update.
	if updateFields.HasAssignee() || len(updateFields.Labels) > 0 {
		return fmt.Errorf("incident.io update supports only status, severity and description; assignee and labels are not supported")
	}

	client := incidentIOClient(config)
	payload := clients.IncidentIOEditPayload{}

	if updateFields.Severity != "" {
		severities, err := client.ListSeverities(ctx)
		if err != nil {
			return fmt.Errorf("failed to resolve incident.io severity: %w", err)
		}
		severityID := clients.FindSeverityByName(severities, updateFields.Severity)
		if severityID == "" {
			return fmt.Errorf("incident.io severity %q not found in this account", updateFields.Severity)
		}
		payload.SeverityID = severityID
	}

	if updateFields.Description != "" {
		payload.Summary = updateFields.Description
	}

	if updateFields.Status != "" {
		category := clients.NormalizeIncidentIOStatusCategory(updateFields.Status)
		if category == "" {
			return fmt.Errorf("unsupported status for incident.io: %q", updateFields.Status)
		}
		statuses, err := client.ListIncidentStatuses(ctx)
		if err != nil {
			return fmt.Errorf("failed to resolve incident.io statuses: %w", err)
		}
		target := clients.FindStatusByCategory(statuses, category)
		if target == nil {
			return fmt.Errorf("incident.io account has no status in the %q category", category)
		}
		payload.IncidentStatusID = target.ID
	}

	if payload == (clients.IncidentIOEditPayload{}) {
		return nil
	}

	// Everything lands in a single edit call so a multi-field update cannot
	// half-apply and leave the incident inconsistent.
	_, err := client.EditIncident(ctx, ticketID, payload, true)
	return err
}

// QuickValidateIncidentIO checks incident.io credentials without fetching full
// metadata. Called by the integration modal's "Test connection" before a
// config is persisted.
func QuickValidateIncidentIO(ctx context.Context, configuration models.TicketConfigurations) error {
	if configuration.Password == "" {
		return fmt.Errorf("incident.io api key is required")
	}

	client := clients.CreateIncidentIOClient(configuration.Password, configuration.URL)
	// Severities is the cheapest authenticated read that every API key can
	// perform — unlike /v2/users, it does not need an extra scope, so a
	// correctly-scoped incident key never fails validation spuriously.
	if _, err := client.ListSeverities(ctx); err != nil {
		return fmt.Errorf("incident.io auth failed: %w", err)
	}
	return nil
}

// FetchIncidentIOIncidentTypes returns incident types in the Project shape the
// integration metadata pipeline persists as "projects". incident.io has no
// service concept, so incident type is what the ticket form's project selector
// offers.
func FetchIncidentIOIncidentTypes(ctx context.Context, configuration models.TicketConfigurations) ([]models.Project, error) {
	client := clients.CreateIncidentIOClient(configuration.Password, configuration.URL)

	incidentTypes, err := client.ListIncidentTypes(ctx)
	if err != nil {
		return nil, err
	}

	projects := make([]models.Project, 0, len(incidentTypes))
	for _, it := range incidentTypes {
		projects = append(projects, models.Project{Name: it.Name, Key: it.ID})
	}
	return projects, nil
}

// FetchIncidentIOSeverities returns the account's severities in the Priority
// shape. Unlike PagerDuty's fixed high/low, incident.io severities are
// org-defined, so they have to be read from the account rather than hardcoded.
func FetchIncidentIOSeverities(ctx context.Context, configuration models.TicketConfigurations) ([]models.Priority, error) {
	client := clients.CreateIncidentIOClient(configuration.Password, configuration.URL)

	severities, err := client.ListSeverities(ctx)
	if err != nil {
		return nil, err
	}

	priorities := make([]models.Priority, 0, len(severities))
	for _, sev := range severities {
		priorities = append(priorities, models.Priority{ID: sev.ID, Name: sev.Name})
	}
	return priorities, nil
}

// GetUrgencies returns the severity names NudgeBee offers when it cannot query
// the account. incident.io severities are configurable per organisation — the
// live list comes from GetCreateMeta; this is only the static fallback and
// matches incident.io's out-of-the-box severity set.
func (s *IncidentIOService) GetUrgencies() []string {
	return []string{"Minor", "Major", "Critical"}
}
