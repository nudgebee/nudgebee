package tools

import (
	"fmt"
	"log/slog"
	"nudgebee/tickets-server/clients"
	"nudgebee/tickets-server/models"
	"nudgebee/tickets-server/services/ticket"
	"nudgebee/tickets-server/utils"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	pagerduty "github.com/PagerDuty/go-pagerduty"
)

type PagerDutyService struct{}

var _ ticket.IncidentManager = (*PagerDutyService)(nil)

func init() {
	ticket.RegisterIncidentManager("pagerduty", &PagerDutyService{})
}

// resolveIncidentAPIID resolves a ticket ID (which may be a numeric incident number
// like "7903" or an alphanumeric API ID like "Q3H55HZ939W3WI") to the PagerDuty
// alphanumeric API ID required by the PagerDuty REST API.
// Supports both formats for backwards compatibility with existing stored tickets.
func resolveIncidentAPIID(ctx *gin.Context, client *pagerduty.Client, ticketID string) (string, error) {
	// If it's not purely numeric, assume it's already an API ID
	if _, err := strconv.ParseUint(ticketID, 10, 64); err != nil {
		return ticketID, nil
	}

	// It's a numeric incident number — try to get the incident directly first.
	// PagerDuty's GetIncident only accepts the alphanumeric ID, so this will fail
	// for numeric IDs. We use it as a fast-path for alphanumeric IDs.
	incident, err := client.GetIncidentWithContext(ctx, ticketID)
	if err == nil && incident != nil {
		return incident.ID, nil
	}

	// Numeric ID failed — search by listing incidents and matching by number.
	incidentNum, _ := strconv.ParseUint(ticketID, 10, 64)

	// Page through incidents to find the matching number
	offset := uint(0)
	for {
		resp, err := client.ListIncidentsWithContext(ctx, pagerduty.ListIncidentsOptions{
			Limit:     100,
			Offset:    offset,
			DateRange: "all",
		})
		if err != nil {
			return "", fmt.Errorf("failed to resolve incident number %s: %w", ticketID, err)
		}

		for _, inc := range resp.Incidents {
			if uint64(inc.IncidentNumber) == incidentNum {
				return inc.ID, nil
			}
		}

		if !resp.More {
			break
		}
		offset += 100
	}

	return "", fmt.Errorf("PagerDuty incident with number %s not found", ticketID)
}

func (s *PagerDutyService) Create(ctx *gin.Context, config models.TicketConfigurations, t models.Ticket) (models.Ticket, error) {
	return CreatePagerDutyIncident(ctx, config, t)
}

func (s *PagerDutyService) GetCreateMeta(ctx *gin.Context, config models.TicketConfigurations, projectKey string) (interface{}, error) {
	return FetchPagerDutyIncidentCreateMeta(ctx, config, projectKey)
}

func (s *PagerDutyService) AddComment(ctx *gin.Context, config models.TicketConfigurations, t models.Ticket) error {
	return AddPagerDutyIncidentComment(ctx, config, t)
}

func (s *PagerDutyService) GetComments(ctx *gin.Context, config models.TicketConfigurations, ticketID string) ([]models.Comments, error) {
	if err := utils.ValidatePagerDutyIncidentID(ticketID); err != nil {
		return nil, fmt.Errorf("invalid ticket ID: %w", err)
	}

	client := clients.CreatePagerdutyClient(config.Password)

	apiID, err := resolveIncidentAPIID(ctx, client, ticketID)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve PagerDuty incident for comments: %w", err)
	}

	notes, err := client.ListIncidentNotesWithContext(ctx, apiID)
	if err != nil {
		return nil, fmt.Errorf("failed to list PagerDuty incident notes: %w", err)
	}

	comments := make([]models.Comments, len(notes))
	for i, note := range notes {
		comments[i] = models.Comments{
			Author:  note.User.Summary,
			Comment: note.Content,
			Created: note.CreatedAt,
			Updated: note.CreatedAt,
		}
	}
	return comments, nil
}

func (s *PagerDutyService) Get(ctx *gin.Context, config models.TicketConfigurations, ticketID string) (*models.Ticket, error) {
	client := clients.CreatePagerdutyClient(config.Password)

	apiID, err := resolveIncidentAPIID(ctx, client, ticketID)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve PagerDuty incident: %w", err)
	}

	incident, err := client.GetIncidentWithContext(ctx, apiID)
	if err != nil {
		return nil, fmt.Errorf("failed to get PagerDuty incident: %w", err)
	}

	var createdAt *time.Time
	if incident.CreatedAt != "" {
		parsed, err := time.Parse(time.RFC3339, incident.CreatedAt)
		if err == nil {
			createdAt = &parsed
		}
	}

	var urgency string
	if incident.Urgency != "" {
		urgency = incident.Urgency
	}

	return &models.Ticket{
		TicketID:    incident.ID,
		Title:       incident.Title,
		Description: incident.Description,
		Status:      incident.Status,
		Severity:    urgency,
		Platform:    "pagerduty",
		URL:         incident.HTMLURL,
		CreatedAt:   createdAt,
		Raw:         marshalToMap(incident),
	}, nil
}

// Acknowledge acknowledges a PagerDuty incident.
func (s *PagerDutyService) Acknowledge(ctx *gin.Context, config models.TicketConfigurations, incidentID string) error {
	if err := utils.ValidatePagerDutyIncidentID(incidentID); err != nil {
		return fmt.Errorf("invalid incident ID: %w", err)
	}

	client := clients.CreatePagerdutyClient(config.Password)

	apiID, err := resolveIncidentAPIID(ctx, client, incidentID)
	if err != nil {
		return err
	}

	_, err = client.ManageIncidentsWithContext(ctx, config.Username, []pagerduty.ManageIncidentsOptions{
		{
			ID:     apiID,
			Type:   "incident_reference",
			Status: "acknowledged",
		},
	})
	return err
}

// Escalate escalates a PagerDuty incident.
func (s *PagerDutyService) Escalate(ctx *gin.Context, config models.TicketConfigurations, incidentID string, escalationPolicy string) error {
	if err := utils.ValidatePagerDutyIncidentID(incidentID); err != nil {
		return fmt.Errorf("invalid incident ID: %w", err)
	}

	client := clients.CreatePagerdutyClient(config.Password)

	apiID, err := resolveIncidentAPIID(ctx, client, incidentID)
	if err != nil {
		return err
	}

	_, err = client.ManageIncidentsWithContext(ctx, config.Username, []pagerduty.ManageIncidentsOptions{
		{
			ID:               apiID,
			Type:             "incident_reference",
			EscalationPolicy: &pagerduty.APIReference{ID: escalationPolicy, Type: "escalation_policy_reference"},
		},
	})
	return err
}

// Resolve resolves a PagerDuty incident.
func (s *PagerDutyService) Resolve(ctx *gin.Context, config models.TicketConfigurations, incidentID string, resolution string) error {
	if err := utils.ValidatePagerDutyIncidentID(incidentID); err != nil {
		return fmt.Errorf("invalid incident ID: %w", err)
	}

	client := clients.CreatePagerdutyClient(config.Password)

	apiID, err := resolveIncidentAPIID(ctx, client, incidentID)
	if err != nil {
		return err
	}

	// Add resolution note if provided
	if resolution != "" {
		note := pagerduty.IncidentNote{
			Content: resolution,
			User: pagerduty.APIObject{
				Summary: config.Username,
				Type:    "user_reference",
			},
		}
		if _, err := client.CreateIncidentNoteWithContext(ctx, apiID, note); err != nil {
			slog.Warn("Failed to add resolution note", "error", err, "incident_id", incidentID)
		}
	}

	_, err = client.ManageIncidentsWithContext(ctx, config.Username, []pagerduty.ManageIncidentsOptions{
		{
			ID:     apiID,
			Type:   "incident_reference",
			Status: "resolved",
		},
	})
	return err
}

// GetUrgencies returns available urgency levels for PagerDuty.
func (s *PagerDutyService) GetUrgencies() []string {
	return []string{"low", "high"}
}

// Update updates fields on a PagerDuty incident
func (s *PagerDutyService) Update(ctx *gin.Context, config models.TicketConfigurations, ticketID string, updateFields models.UpdateFields) error {
	if err := utils.ValidatePagerDutyIncidentID(ticketID); err != nil {
		return fmt.Errorf("invalid ticket ID: %w", err)
	}

	// PagerDuty supports only status transitions; reject other fields so the
	// workflow author sees an explicit error rather than a silent no-op.
	if updateFields.Severity != "" || updateFields.Assignee != "" || updateFields.Description != "" || len(updateFields.Labels) > 0 {
		return fmt.Errorf("PagerDuty update supports only status; severity, assignee, description, and labels are not supported")
	}

	if updateFields.Status != "" {
		return s.Transition(ctx, config, ticketID, updateFields.Status)
	}
	return nil
}

// Transition changes the status of a PagerDuty incident
func (s *PagerDutyService) Transition(ctx *gin.Context, config models.TicketConfigurations, ticketID string, status string) error {
	if err := utils.ValidatePagerDutyIncidentID(ticketID); err != nil {
		return fmt.Errorf("invalid ticket ID: %w", err)
	}

	client := clients.CreatePagerdutyClient(config.Password)

	apiID, err := resolveIncidentAPIID(ctx, client, ticketID)
	if err != nil {
		return err
	}

	// Map common status names to PagerDuty statuses
	pdStatus := status
	switch strings.ToLower(status) {
	case "acknowledged", "ack":
		pdStatus = "acknowledged"
	case "resolved", "done", "closed":
		pdStatus = "resolved"
	case "triggered", "open", "reopened":
		// Cannot re-trigger a resolved incident via status change
		return fmt.Errorf("cannot transition to 'triggered' status")
	default:
		// Use as-is
	}

	_, err = client.ManageIncidentsWithContext(ctx, config.Username, []pagerduty.ManageIncidentsOptions{
		{
			ID:     apiID,
			Type:   "incident_reference",
			Status: pdStatus,
		},
	})
	return err
}

// List retrieves incidents from PagerDuty with filtering and pagination.
func (s *PagerDutyService) List(ctx *gin.Context, config models.TicketConfigurations, params models.ListParams) (*models.ListResult, error) {
	client := clients.CreatePagerdutyClient(config.Password)

	opts := pagerduty.ListIncidentsOptions{
		Limit:  uint(params.Limit),
		Offset: uint(params.Offset),
	}

	if params.ProjectKey != "" {
		opts.ServiceIDs = []string{params.ProjectKey}
	}
	if params.Status != "" {
		opts.Statuses = []string{params.Status}
	}
	if params.Priority != "" {
		opts.Urgencies = []string{params.Priority}
	}
	if params.SortBy == "updated_at" {
		// PagerDuty doesn't support sort by updated_at for incidents
		// Use created_at as fallback
		opts.SortBy = "created_at:" + params.SortOrder
	} else {
		opts.SortBy = "created_at:" + params.SortOrder
	}
	if params.CreatedAfter != "" {
		opts.Since = params.CreatedAfter
	}
	if params.CreatedBefore != "" {
		opts.Until = params.CreatedBefore
	}

	response, err := client.ListIncidentsWithContext(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to list PagerDuty incidents: %w", err)
	}

	tickets := make([]models.Ticket, 0, len(response.Incidents))
	for _, incident := range response.Incidents {
		var createdAt *time.Time
		if incident.CreatedAt != "" {
			if parsed, parseErr := time.Parse(time.RFC3339, incident.CreatedAt); parseErr == nil {
				createdAt = &parsed
			}
		}

		tickets = append(tickets, models.Ticket{
			TicketID:  incident.ID,
			Title:     incident.Title,
			Status:    incident.Status,
			Severity:  incident.Urgency,
			Platform:  "pagerduty",
			URL:       incident.HTMLURL,
			CreatedAt: createdAt,
		})
	}

	total := int(response.Total)

	return &models.ListResult{
		Tickets: tickets,
		Total:   total,
		Limit:   params.Limit,
		Offset:  params.Offset,
	}, nil
}

func CreatePagerDutyIncident(ctx *gin.Context, configuration models.TicketConfigurations, ticket models.Ticket) (models.Ticket, error) {
	client := clients.CreatePagerdutyClient(configuration.Password)

	incident := &pagerduty.CreateIncidentOptions{
		Title: ticket.Title,
		Service: &pagerduty.APIReference{
			ID:   ticket.ProjectKey,
			Type: "service_reference",
		},
		Body: &pagerduty.APIDetails{
			Details: ticket.Description,
			Type:    "incident_body",
		},
	}
	createdIncident, err := client.CreateIncidentWithContext(ctx, configuration.Username, incident)
	if err != nil {
		slog.Error("Error creating PagerDuty incident:", "error", slog.AnyValue(err))
		return ticket, err
	}

	slog.Info("PagerDuty incident created:", "ID", createdIncident.ID, "Number", createdIncident.IncidentNumber)

	// Store the alphanumeric API ID as ticket ID — PagerDuty REST API requires this
	// for all operations (get, update, acknowledge, resolve, notes, etc.).
	// The numeric IncidentNumber is user-facing only and can be retrieved from incident details.
	ticket.TicketID = createdIncident.ID
	ticket.Status = "triggered"
	ticket.Severity = "NA"
	ticket.URL = createdIncident.HTMLURL
	ticket.Platform = "pagerduty"
	now := time.Now()
	ticket.CreatedAt = &now

	return ticket, nil
}

// FetchPagerDutyIncidentCreateMeta fetches and formats the PagerDuty incident creation metadata.
func FetchPagerDutyIncidentCreateMeta(ctx *gin.Context, configuration models.TicketConfigurations, serviceID string) (any, error) {
	client := clients.CreatePagerdutyClient(configuration.Password)

	// Fetch users for possible assignees
	users, err := client.ListUsersWithContext(ctx, pagerduty.ListUsersOptions{})
	if err != nil {
		slog.Error("Error fetching PagerDuty users:", "error", slog.AnyValue(err))
		return nil, err
	}

	assigneeValues := make([]interface{}, len(users.Users))
	for i, user := range users.Users {
		assigneeValues[i] = map[string]interface{}{
			"id":    user.ID,
			"name":  user.Name,
			"value": user.ID,
		}
	}

	// Fetch services
	services, err := client.ListServicesWithContext(ctx, pagerduty.ListServiceOptions{})
	if err != nil {
		return nil, err
	}

	serviceValues := make([]interface{}, len(services.Services))
	for i, service := range services.Services {
		serviceValues[i] = map[string]interface{}{
			"id":    service.ID,
			"name":  service.Name,
			"value": service.ID,
		}
	}

	// Format the data
	template := Template{
		Name: "PagerDuty Incident",
		Fields: map[string]FieldInfo{
			"assignee": {
				AllowedValues: assigneeValues,
				Key:           "assignee",
				Name:          "Assignee",
				Required:      false,
				Type:          "select",
			},
			"service": {
				AllowedValues: serviceValues,
				Key:           "service",
				Name:          "Service",
				Required:      true,
				Type:          "select",
			},
			"summary": {
				AllowedValues: nil,
				Key:           "summary",
				Name:          "Summary",
				Required:      true,
				Type:          "string",
			},
			"description": {
				AllowedValues: nil,
				Key:           "description",
				Name:          "Description",
				Required:      false,
				Type:          "string",
			},
		},
	}

	// Match the Jira/GitHub shape: {"data": [Template, ...]}.
	return map[string]interface{}{"data": []Template{template}}, nil
}

func AddPagerDutyIncidentComment(ctx *gin.Context, configuration models.TicketConfigurations, ticket models.Ticket) error {
	client := clients.CreatePagerdutyClient(configuration.Password)
	if client == nil {
		return fmt.Errorf("failed to create PagerDuty client for incident %s (integration %s)", ticket.TicketID, configuration.ID)
	}

	// Resolve to API ID — new tickets already store the alphanumeric API ID,
	// but old tickets may have stored a numeric incident number.
	apiID, err := resolveIncidentAPIID(ctx, client, ticket.TicketID)
	if err != nil {
		return fmt.Errorf("failed to resolve PagerDuty incident for comment: %w", err)
	}

	noteBody := ticket.Comment
	if noteBody == "" {
		noteBody = fmt.Sprintf(
			"Found *%s* again at *%s*\n\n*Description:*\n%s",
			ticket.Title,
			time.Now().Format("02 Jan 2006 15:04:05"),
			ticket.Description,
		)
	}

	note := pagerduty.IncidentNote{
		Content: noteBody,
		User: pagerduty.APIObject{
			Summary: configuration.Username,
			Type:    "user_reference",
		},
	}

	_, err = client.CreateIncidentNoteWithContext(ctx, apiID, note)
	if err != nil {
		slog.Error("failed to create PagerDuty incident note",
			"incidentID", ticket.TicketID,
			"error", err,
		)
		return fmt.Errorf("failed to create PagerDuty note for incident %s: %w", ticket.TicketID, err)
	}

	slog.Info("PagerDuty incident note added successfully", "incidentID", ticket.TicketID)
	return nil
}
