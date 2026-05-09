package tools

import (
	"fmt"
	"log/slog"
	"nudgebee/tickets-server/clients"
	"nudgebee/tickets-server/models"
	"nudgebee/tickets-server/services/ticket"
	"nudgebee/tickets-server/utils"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

type ZenDutyService struct{}

var _ ticket.IncidentManager = (*ZenDutyService)(nil)

func init() {
	ticket.RegisterIncidentManager("zenduty", &ZenDutyService{})
}

func (s *ZenDutyService) Create(ctx *gin.Context, config models.TicketConfigurations, ticket models.Ticket) (models.Ticket, error) {
	return CreateZenDutyIncident(ctx, config, ticket)
}

func (s *ZenDutyService) GetCreateMeta(ctx *gin.Context, config models.TicketConfigurations, projectKey string) (interface{}, error) {
	return FetchZenDutyIncidentCreateMeta(ctx, config, projectKey)
}

func (s *ZenDutyService) AddComment(ctx *gin.Context, config models.TicketConfigurations, ticket models.Ticket) error {
	return AddZenDutyIncidentComment(ctx, config, ticket)
}

func (s *ZenDutyService) GetComments(ctx *gin.Context, config models.TicketConfigurations, ticketID string) ([]models.Comments, error) {
	return GetZenDutyIncidentComments(ctx, config, ticketID)
}

func (s *ZenDutyService) Get(ctx *gin.Context, config models.TicketConfigurations, ticketID string) (*models.Ticket, error) {
	if err := utils.ValidateZenDutyIncidentID(ticketID); err != nil {
		return nil, fmt.Errorf("invalid incident ID: %w", err)
	}
	client := clients.CreateZenDutyClient(config.Password)
	incident, err := client.GetIncident(ctx, ticketID)
	if err != nil {
		return nil, err
	}

	createdAt, _ := time.Parse(time.RFC3339, incident.CreationDate)
	return &models.Ticket{
		TicketID:    incident.UniqueID,
		Title:       incident.Title,
		Description: incident.Summary,
		Status:      clients.MapStatusToString(incident.Status),
		Platform:    "zenduty",
		URL:         incident.HTMLURL,
		CreatedAt:   &createdAt,
		Raw:         marshalToMap(incident),
	}, nil
}

// Acknowledge acknowledges an incident.
func (s *ZenDutyService) Acknowledge(ctx *gin.Context, config models.TicketConfigurations, incidentID string) error {
	if err := utils.ValidateZenDutyIncidentID(incidentID); err != nil {
		return fmt.Errorf("invalid incident ID: %w", err)
	}

	client := clients.CreateZenDutyClient(config.Password)
	_, err := client.AcknowledgeIncident(ctx, incidentID)
	return err
}

// Escalate escalates an incident (ZenDuty handles this differently - reassign).
func (s *ZenDutyService) Escalate(ctx *gin.Context, config models.TicketConfigurations, incidentID string, escalationPolicy string) error {
	// ZenDuty doesn't have a direct escalate API like PagerDuty
	// This could be implemented by reassigning to a different user/team
	slog.Warn("ZenDuty escalate not directly supported, use reassignment instead", "incident_id", incidentID)
	return nil
}

// Resolve marks an incident as resolved.
func (s *ZenDutyService) Resolve(ctx *gin.Context, config models.TicketConfigurations, incidentID string, resolution string) error {
	if err := utils.ValidateZenDutyIncidentID(incidentID); err != nil {
		return fmt.Errorf("invalid incident ID: %w", err)
	}

	client := clients.CreateZenDutyClient(config.Password)

	// Add resolution note if provided
	if resolution != "" {
		if err := client.AddIncidentNote(ctx, incidentID, resolution); err != nil {
			slog.Warn("Failed to add resolution note", "error", err, "incident_id", incidentID)
		}
	}

	_, err := client.ResolveIncident(ctx, incidentID)
	return err
}

// GetUrgencies returns available urgency levels for ZenDuty.
func (s *ZenDutyService) GetUrgencies() []string {
	return []string{"low", "medium", "high"}
}

// Update updates fields on a ZenDuty incident
func (s *ZenDutyService) Update(ctx *gin.Context, config models.TicketConfigurations, ticketID string, updateFields models.UpdateFields) error {
	if err := utils.ValidateZenDutyIncidentID(ticketID); err != nil {
		return fmt.Errorf("invalid ticket ID: %w", err)
	}

	// ZenDuty supports only status transitions; reject other fields so the
	// workflow author sees an explicit error rather than a silent no-op.
	if updateFields.Severity != "" || updateFields.Assignee != "" || updateFields.Description != "" || len(updateFields.Labels) > 0 {
		return fmt.Errorf("ZenDuty update supports only status; severity, assignee, description, and labels are not supported")
	}

	if updateFields.Status != "" {
		return s.Transition(ctx, config, ticketID, updateFields.Status)
	}
	return nil
}

// Transition changes the status of a ZenDuty incident
func (s *ZenDutyService) Transition(ctx *gin.Context, config models.TicketConfigurations, ticketID string, status string) error {
	if err := utils.ValidateZenDutyIncidentID(ticketID); err != nil {
		return fmt.Errorf("invalid ticket ID: %w", err)
	}

	client := clients.CreateZenDutyClient(config.Password)

	// Map common status names to ZenDuty actions
	switch strings.ToLower(status) {
	case "acknowledged", "ack":
		_, err := client.AcknowledgeIncident(ctx, ticketID)
		return err
	case "resolved", "done", "closed":
		_, err := client.ResolveIncident(ctx, ticketID)
		return err
	default:
		return fmt.Errorf("unsupported status for ZenDuty: %s. Use 'acknowledged' or 'resolved'", status)
	}
}

// List retrieves incidents from ZenDuty with filtering.
func (s *ZenDutyService) List(ctx *gin.Context, config models.TicketConfigurations, params models.ListParams) (*models.ListResult, error) {
	client := clients.CreateZenDutyClient(config.Password)

	queryParams := make(map[string]string)
	if params.ProjectKey != "" {
		queryParams["service"] = params.ProjectKey
	}
	if params.Status != "" {
		// Map status string to ZenDuty numeric status
		switch strings.ToLower(params.Status) {
		case "triggered":
			queryParams["status"] = "0"
		case "acknowledged":
			queryParams["status"] = "1"
		case "resolved":
			queryParams["status"] = "2"
		}
	}
	if params.Priority != "" {
		// Map urgency string to ZenDuty numeric urgency
		switch strings.ToLower(params.Priority) {
		case "low":
			queryParams["urgency"] = "0"
		case "medium":
			queryParams["urgency"] = "1"
		case "high":
			queryParams["urgency"] = "2"
		}
	}

	incidents, err := client.ListIncidents(ctx, queryParams)
	if err != nil {
		return nil, fmt.Errorf("failed to list ZenDuty incidents: %w", err)
	}

	// Client-side pagination since ZenDuty API may not support offset/limit
	total := len(incidents)
	end := params.Offset + params.Limit
	if params.Offset > total {
		incidents = nil
	} else {
		if end > total {
			end = total
		}
		incidents = incidents[params.Offset:end]
	}

	tickets := make([]models.Ticket, 0, len(incidents))
	for _, incident := range incidents {
		createdAt, _ := time.Parse(time.RFC3339, incident.CreationDate)
		tickets = append(tickets, models.Ticket{
			TicketID:  incident.UniqueID,
			Title:     incident.Title,
			Status:    clients.MapStatusToString(incident.Status),
			Platform:  "zenduty",
			URL:       incident.HTMLURL,
			CreatedAt: &createdAt,
		})
	}

	return &models.ListResult{
		Tickets: tickets,
		Total:   total,
		Limit:   params.Limit,
		Offset:  params.Offset,
	}, nil
}

// CreateZenDutyIncident creates a new incident in ZenDuty.
func CreateZenDutyIncident(ctx *gin.Context, configuration models.TicketConfigurations, ticket models.Ticket) (models.Ticket, error) {
	client := clients.CreateZenDutyClient(configuration.Password)

	// Map urgency from severity
	urgency := clients.MapUrgencyFromString(ticket.Severity)
	if urgency == 0 {
		urgency = clients.ZenDutyUrgencyMedium
	}

	req := &clients.CreateIncidentRequest{
		Title:     ticket.Title,
		Summary:   ticket.Description,
		ServiceID: ticket.ProjectKey,
		Urgency:   urgency,
	}

	// Add assignee if specified
	if ticket.Assignee != "" {
		req.AssignedTo = []string{ticket.Assignee}
	}

	createdIncident, err := client.CreateIncident(ctx, req)
	if err != nil {
		slog.Error("Error creating ZenDuty incident:", "error", slog.AnyValue(err))
		return ticket, err
	}

	slog.Debug("ZenDuty incident created:", "ID", createdIncident.UniqueID, "Number", createdIncident.Number)

	ticket.TicketID = createdIncident.UniqueID
	ticket.Status = "triggered"
	ticket.Severity = "NA"
	ticket.URL = createdIncident.HTMLURL
	if ticket.URL == "" {
		ticket.URL = fmt.Sprintf("https://www.zenduty.com/dashboard/incidents/%s", createdIncident.UniqueID)
	}
	ticket.Platform = "zenduty"
	now := time.Now()
	ticket.CreatedAt = &now

	return ticket, nil
}

// FetchZenDutyIncidentCreateMeta fetches and formats the ZenDuty incident creation metadata.
func FetchZenDutyIncidentCreateMeta(ctx *gin.Context, configuration models.TicketConfigurations, serviceID string) (any, error) {
	client := clients.CreateZenDutyClient(configuration.Password)

	// Fetch users for possible assignees
	users, err := client.ListUsers(ctx)
	if err != nil {
		slog.Error("Error fetching ZenDuty users:", "error", slog.AnyValue(err))
		// Continue without users - they're optional
		users = []clients.ZenDutyUser{}
	}

	assigneeValues := make([]interface{}, len(users))
	for i, user := range users {
		displayName := user.Username
		if user.FirstName != "" || user.LastName != "" {
			displayName = fmt.Sprintf("%s %s", user.FirstName, user.LastName)
		}
		assigneeValues[i] = map[string]interface{}{
			"id":    user.UniqueID,
			"name":  displayName,
			"value": user.UniqueID,
		}
	}

	// Fetch all services
	services, err := client.ListAllServices(ctx)
	if err != nil {
		slog.Error("Error fetching ZenDuty services:", "error", slog.AnyValue(err))
		return nil, err
	}

	serviceValues := make([]interface{}, len(services))
	for i, service := range services {
		serviceValues[i] = map[string]interface{}{
			"id":    service.UniqueID,
			"name":  service.Name,
			"value": service.UniqueID,
		}
	}

	// Urgency options
	urgencyValues := []interface{}{
		map[string]interface{}{"id": "0", "name": "Low", "value": "low"},
		map[string]interface{}{"id": "1", "name": "Medium", "value": "medium"},
		map[string]interface{}{"id": "2", "name": "High", "value": "high"},
	}

	// Format the data
	template := Template{
		Name: "ZenDuty Incident",
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
			"urgency": {
				AllowedValues: urgencyValues,
				Key:           "urgency",
				Name:          "Urgency",
				Required:      false,
				Type:          "select",
			},
			"summary": {
				AllowedValues: nil,
				Key:           "summary",
				Name:          "Summary (Title)",
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

// AddZenDutyIncidentComment adds a note to an existing ZenDuty incident.
func AddZenDutyIncidentComment(ctx *gin.Context, configuration models.TicketConfigurations, ticket models.Ticket) error {
	client := clients.CreateZenDutyClient(configuration.Password)

	if err := utils.ValidateZenDutyIncidentID(ticket.TicketID); err != nil {
		return fmt.Errorf("invalid incident ID: %w", err)
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

	err := client.AddIncidentNote(ctx, ticket.TicketID, noteBody)
	if err != nil {
		slog.Error("failed to create ZenDuty incident note",
			"incidentID", ticket.TicketID,
			"error", err,
		)
		return fmt.Errorf("failed to create ZenDuty note for incident %s: %w", ticket.TicketID, err)
	}

	slog.Info("ZenDuty incident note added successfully", "incidentID", ticket.TicketID)
	return nil
}

// GetZenDutyIncidentComments retrieves all notes from a ZenDuty incident.
func GetZenDutyIncidentComments(ctx *gin.Context, configuration models.TicketConfigurations, incidentID string) ([]models.Comments, error) {
	if err := utils.ValidateZenDutyIncidentID(incidentID); err != nil {
		return nil, fmt.Errorf("invalid incident ID: %w", err)
	}
	client := clients.CreateZenDutyClient(configuration.Password)

	notes, err := client.GetIncidentNotes(ctx, incidentID)
	if err != nil {
		return nil, fmt.Errorf("failed to get ZenDuty incident notes: %w", err)
	}

	comments := make([]models.Comments, len(notes))
	for i, note := range notes {
		comments[i] = models.Comments{
			Author:  note.User,
			Comment: note.Note,
			Created: note.CreatedAt,
			Updated: note.CreatedAt,
		}
	}

	return comments, nil
}
