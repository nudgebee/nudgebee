package services

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"nudgebee/tickets-server/common"
	"nudgebee/tickets-server/database"
	"nudgebee/tickets-server/models"
	ticketmgr "nudgebee/tickets-server/services/ticket"
	_ "nudgebee/tickets-server/services/tools" // Import to trigger init() registrations
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const ActionCreated = "created"
const MessageCreatedSuccess = "Ticket created successfully"
const ActionCommented = "commented"
const MessageCommentedSuccess = "Existing ticket found, Added comment successfully"

func GetErrorResponse(err string) models.TicketInsertResponse {
	return models.TicketInsertResponse{
		Data: models.Data{
			InsertTicketsOne: models.InsertTicketsOne{
				Error: err,
			},
		},
	}
}

// syncTicketToDB updates the Nudgebee tickets table after a successful external tool operation.
// This is fire-and-forget: runs in a goroutine so the API response is not blocked, and if the
// DB update fails we log the error but don't fail the already-succeeded external operation.
// Only columns known to exist in the tickets table are synced (status, severity, assignee,
// description). Fields like labels and project_key are excluded because they have no
// corresponding column in the tickets table.
func syncTicketToDB(ticketID, integrationID string, fields models.UpdateFields) {
	setClauses, args := buildUpdateClauses(fields)
	if len(setClauses) == 0 {
		return
	}
	go func() {
		dbManager, err := database.GetDatabaseManager()
		if err != nil {
			slog.Error("Failed to get database manager for ticket sync", "error", err)
			return
		}
		argIdx := len(args) + 1
		query := fmt.Sprintf("UPDATE tickets SET %s WHERE ticket_id = $%d AND integration_id = $%d",
			strings.Join(setClauses, ", "), argIdx, argIdx+1)
		args = append(args, ticketID, integrationID)
		_, err = dbManager.Exec(query, args...)
		if err != nil {
			slog.Error("Failed to sync ticket update to DB",
				"ticketID", ticketID, "integrationID", integrationID, "error", err)
		}
	}()
}

// buildUpdateClauses builds SET clause fragments and args from UpdateFields.
// Returns ([]string like ["status = $1"], []interface{} args).
func buildUpdateClauses(fields models.UpdateFields) ([]string, []interface{}) {
	var setClauses []string
	var args []interface{}
	idx := 1
	if fields.Status != "" {
		setClauses = append(setClauses, fmt.Sprintf("status = $%d", idx))
		args = append(args, fields.Status)
		idx++
	}
	if fields.Severity != "" {
		setClauses = append(setClauses, fmt.Sprintf("severity = $%d", idx))
		args = append(args, fields.Severity)
		idx++
	}
	if fields.Assignee != "" {
		setClauses = append(setClauses, fmt.Sprintf("assignee = $%d", idx))
		args = append(args, fields.Assignee)
		idx++
	}
	if fields.Description != "" {
		setClauses = append(setClauses, fmt.Sprintf("description = $%d", idx))
		args = append(args, fields.Description)
	}
	return setClauses, args
}

func fetchToolConfigurationByTenantIdAndTool(tenantId, tool string) (models.TicketConfigurations, error) {
	dbManager, err := database.GetDatabaseManager()
	if err != nil {
		slog.Error("Failed to get database manager:", "error", err)
		return models.TicketConfigurations{}, err
	}

	// Fetch integration with config values
	var integrationID string
	err = dbManager.Get(&integrationID, `
		SELECT id FROM integrations
		WHERE tenant_id = $1 AND type = $2 AND status = 'enabled'
		LIMIT 1
	`, tenantId, tool)
	if err != nil {
		slog.Error("Error fetching integration:", "error", err)
		return models.TicketConfigurations{}, fmt.Errorf("no ticket configuration found for tenant %s with tool %s", tenantId, tool)
	}

	return fetchIntegrationWithConfigValues(dbManager, integrationID)
}

func fetchToolConfiguration(configurationId string) (models.TicketConfigurations, error) {
	dbManager, err := database.GetDatabaseManager()
	if err != nil {
		slog.Error("Failed to get database manager:", "error", err)
		return models.TicketConfigurations{}, err
	}

	return fetchIntegrationWithConfigValues(dbManager, configurationId)
}

// fetchToolConfigurationForTenant fetches a tool configuration and verifies it belongs to the given tenant.
func fetchToolConfigurationForTenant(configurationId, tenantId string) (models.TicketConfigurations, error) {
	config, err := fetchToolConfiguration(configurationId)
	if err != nil {
		return config, err
	}
	if config.Tenant != tenantId {
		return models.TicketConfigurations{}, fmt.Errorf("integration %s does not belong to tenant %s", configurationId, tenantId)
	}
	return config, nil
}

// fetchIntegrationWithConfigValues fetches an integration and its config values, then reconstructs TicketConfigurations
func fetchIntegrationWithConfigValues(dbManager *database.DatabaseManager, integrationID string) (models.TicketConfigurations, error) {
	// Validate integrationID is not empty
	if integrationID == "" {
		return models.TicketConfigurations{}, fmt.Errorf("integration_id is required to fetch ticket configuration")
	}

	// Fetch integration metadata
	var config models.TicketConfigurations
	err := dbManager.Get(&config, `
		SELECT id, tenant_id as tenant, type as tool, name, status, created_by
		FROM integrations
		WHERE id = $1
	`, integrationID)
	if err != nil {
		slog.Error("Error fetching integration:", "error", err)
		return models.TicketConfigurations{}, fmt.Errorf("no ticket configuration found for integration_id %s", integrationID)
	}

	// Map status to is_active
	config.IsActive = config.Status == "enabled"

	// Fetch config values
	type ConfigValue struct {
		Name        string `db:"name"`
		Value       string `db:"value"`
		IsEncrypted bool   `db:"is_encrypted"`
	}

	var configValues []ConfigValue
	err = dbManager.Select(&configValues, `
		SELECT name, value, COALESCE(is_encrypted, false) as is_encrypted
		FROM integration_config_values
		WHERE integration_id = $1
	`, integrationID)
	if err != nil {
		slog.Error("Error fetching config values:", "error", err)
		return models.TicketConfigurations{}, err
	}

	// Parse config values into TicketConfigurations
	for _, cv := range configValues {
		value := cv.Value

		// Decrypt if encrypted
		if cv.IsEncrypted && value != "" {
			decrypted, err := common.Decrypt(value)
			if err != nil {
				slog.Error("Failed to decrypt config value:", "name", cv.Name, "error", err)
				return models.TicketConfigurations{}, err
			}
			value = decrypted
		}

		// Map config values to TicketConfigurations fields
		switch cv.Name {
		case "url":
			config.URL = value
		case "username":
			config.Username = value
		case "password":
			config.Password = value
		case "auth_type":
			config.AuthType = value
		case "projects":
			if value != "" {
				if err := json.Unmarshal([]byte(value), &config.Projects); err != nil {
					slog.Warn("Failed to unmarshal projects config value", "integration_id", integrationID, "error", err)
				}
			}
		case "priorities":
			if value != "" {
				if err := json.Unmarshal([]byte(value), &config.Priorities); err != nil {
					slog.Warn("Failed to unmarshal priorities config value", "integration_id", integrationID, "error", err)
				}
			}
		case "users":
			if value != "" {
				if err := json.Unmarshal([]byte(value), &config.Users); err != nil {
					slog.Warn("Failed to unmarshal users config value", "integration_id", integrationID, "error", err)
				}
			}
		case "last_connected":
			if value != "" {
				lastConnected, err := time.Parse(time.RFC3339, value)
				if err == nil {
					config.LastConnected = &lastConnected
				}
			}
		}
	}

	return config, nil
}

func CreateIssue(ctx *gin.Context, ticket models.Ticket) (models.TicketInsertResponse, error) {
	slog.Info("New ticket create request received for:", "ticket", ticket)

	configuration, err := fetchToolConfigurationForTenant(ticket.IntegrationID, ticket.Tenant)
	if err != nil {
		slog.Error("Error fetching tool configuration:", "integrationID", ticket.IntegrationID, "tenant", ticket.Tenant, "error", slog.AnyValue(err))
		return GetErrorResponse(fmt.Sprintf("unable to find ticket configuration for integration %s", ticket.IntegrationID)), err
	}

	if !configuration.IsActive {
		slog.Error("Ticket configuration is disabled", "integrationID", configuration.ID, "tool", configuration.Tool)
		return GetErrorResponse(fmt.Sprintf("ticket configuration %q (%s) is disabled", configuration.Name, configuration.Tool)), fmt.Errorf("ticket configuration %s (%s) is disabled, cannot create ticket", configuration.ID, configuration.Tool)
	}

	if !ticket.New {
		tickets, err := checkIfTicketExistsAndAddComment(ctx, configuration, ticket.ReferenceID)
		if err != nil {
			if !strings.Contains(err.Error(), "duplicate ticket") {
				slog.Error("Error checking if ticket exists and adding comment:", "error", slog.AnyValue(err))
			}
			return GetErrorResponse(err.Error()), err
		}
		if len(tickets) > 0 {
			slog.Info("Found ticket, duplicate for:", "Id", tickets[0].ID)
			var response models.TicketInsertResponse
			response.Data.InsertTicketsOne.ID = tickets[0].ID
			response.Data.InsertTicketsOne.TicketID = tickets[0].TicketID
			response.Data.InsertTicketsOne.ReferenceId = tickets[0].ReferenceID
			response.Data.InsertTicketsOne.Status = tickets[0].Status
			response.Data.InsertTicketsOne.Severity = tickets[0].Severity
			response.Data.InsertTicketsOne.URL = tickets[0].URL
			response.Data.InsertTicketsOne.Platform = tickets[0].Platform
			response.Data.InsertTicketsOne.Action = ActionCommented
			response.Data.InsertTicketsOne.Message = MessageCommentedSuccess
			return response, nil
		}
	}

	manager, ok := ticketmgr.GetTicketManager(configuration.Tool)
	if !ok {
		return GetErrorResponse("unsupported ticketing tool."), fmt.Errorf("unsupported tool: %s", configuration.Tool)
	}
	ticket, err = manager.Create(ctx, configuration, ticket)

	if err != nil {
		return GetErrorResponse(fmt.Sprintf("failed to create %s ticket: %v", configuration.Tool, err)), fmt.Errorf("unable to create ticket for %s (integration %s): %v", configuration.Tool, configuration.ID, err)
	}

	dbManager, dbErr := database.GetDatabaseManager()
	if dbErr != nil {
		return GetErrorResponse(fmt.Sprintf("failed to save %s ticket %s to database", configuration.Tool, ticket.TicketID)), fmt.Errorf("database unavailable: %v", dbErr)
	}

	var inserted models.Ticket
	err = dbManager.Get(&inserted, `
		INSERT INTO tickets (
			title, description, severity, tags, ticket_type, project_key,
			assignee, reporter, platform, source, reference_id, created_by,
			tenant, account_id, status, ticket_id, integration_id, url
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8, $9, $10, $11, $12,
			$13, $14, $15, $16, $17, $18
		) RETURNING id, platform, reference_id, severity, status, ticket_id, url`,
		ticket.Title, ticket.Description, ticket.Severity, ticket.Tags, ticket.TicketType, ticket.ProjectKey,
		ticket.Assignee, ticket.Reporter, ticket.Platform, ticket.Source, ticket.ReferenceID, ticket.CreatedBy,
		ticket.Tenant, ticket.AccountID, ticket.Status, ticket.TicketID, ticket.IntegrationID, ticket.URL,
	)
	if err != nil {
		return GetErrorResponse(fmt.Sprintf("failed to save %s ticket %s to database", configuration.Tool, ticket.TicketID)),
			fmt.Errorf("error executing insert ticket query for %s ticket %s: %v", configuration.Tool, ticket.TicketID, err)
	}

	return models.TicketInsertResponse{
		Data: models.Data{
			InsertTicketsOne: models.InsertTicketsOne{
				ID:          inserted.ID,
				Platform:    inserted.Platform,
				ReferenceId: inserted.ReferenceID,
				Severity:    inserted.Severity,
				Status:      inserted.Status,
				TicketID:    inserted.TicketID,
				URL:         inserted.URL,
				Action:      ActionCreated,
				Message:     MessageCreatedSuccess,
			},
		},
	}, nil
}

func checkIfTicketExistsAndAddComment(ctx *gin.Context, configuration models.TicketConfigurations, referenceId string) ([]models.Ticket, error) {
	dbManager, err := database.GetDatabaseManager()
	if err != nil {
		return nil, fmt.Errorf("database unavailable: %v", err)
	}

	var tickets []models.Ticket
	err = dbManager.Select(&tickets, `
		SELECT id, platform, title, ticket_id, integration_id, reference_id,
		       severity, status, url, description, project_key
		FROM tickets
		WHERE reference_id = $1 AND platform = $2`,
		referenceId, configuration.Tool)
	if err != nil {
		slog.Error("Error querying existing tickets", "error", err)
		return nil, err
	}

	if len(tickets) > 0 {
		manager, ok := ticketmgr.GetTicketManager(configuration.Tool)
		if !ok {
			return nil, fmt.Errorf("unsupported tool: %s", configuration.Tool)
		}
		for _, t := range tickets {
			if err := manager.AddComment(ctx, configuration, t); err != nil {
				slog.Error("Failed to add comment to existing ticket", "ticketID", t.TicketID, "tool", configuration.Tool, "error", err)
				return nil, fmt.Errorf("failed to add comment to ticket %s: %w", t.TicketID, err)
			}
		}
	}

	return tickets, nil
}

func FindTicketsByReferenceId(filter models.TicketFilter) (interface{}, error) {
	dbManager, err := database.GetDatabaseManager()
	if err != nil {
		return nil, fmt.Errorf("database unavailable: %v", err)
	}

	var tickets []models.Ticket
	err = dbManager.Select(&tickets, `
		SELECT id, platform, reference_id, severity, status, ticket_id, url
		FROM tickets
		WHERE source = $1 AND reference_id = $2`,
		filter.Source, filter.ReferenceId)
	if err != nil {
		slog.Error("Error executing ticket lookup query", "referenceId", filter.ReferenceId, "source", filter.Source, "error", err)
		return nil, fmt.Errorf("failed to lookup tickets for reference %s (source: %s): %v", filter.ReferenceId, filter.Source, err)
	}

	return models.TicketResponse{
		Data: struct {
			Tickets []models.Ticket `json:"tickets"`
		}{Tickets: tickets},
	}, nil
}

func FetchTicketComments(ctx *gin.Context, ticket models.Ticket) (models.CommentsResponse, error) {
	configuration, err := fetchToolConfiguration(ticket.IntegrationID)
	if err != nil {
		slog.Error("Error fetching tool configuration:", "integrationID", ticket.IntegrationID, "tenant", ticket.Tenant, "error", slog.AnyValue(err))
		return GetErrorCommentResponse(fmt.Sprintf("unable to fetch configuration for integration %s", ticket.IntegrationID)), err
	}

	// Override project key for platforms that need it (GitHub, GitLab).
	// Deep copy the Projects slice to avoid mutating shared configuration state.
	if ticket.ProjectKey != "" {
		projects := make([]models.Project, len(configuration.Projects))
		copy(projects, configuration.Projects)
		configuration.Projects = projects
		if len(configuration.Projects) > 0 {
			configuration.Projects[0].Key = ticket.ProjectKey
		} else {
			configuration.Projects = []models.Project{{Key: ticket.ProjectKey}}
		}
	} else {
		tool := strings.ToLower(configuration.Tool)
		if tool == "github" || tool == "gitlab" {
			return GetErrorCommentResponse(fmt.Sprintf("project_key (owner/repo) is required for %s — set it in the request or configure it in the integration", configuration.Tool)), nil
		}
	}

	manager, ok := ticketmgr.GetTicketManager(configuration.Tool)
	if !ok {
		return GetErrorCommentResponse("unsupported platform"), nil
	}

	comments, err := manager.GetComments(ctx, configuration, ticket.TicketID)
	if errors.Is(err, ticketmgr.ErrNotSupported) {
		return GetErrorCommentResponse("comments not supported for this platform"), nil
	}
	if err != nil {
		return GetErrorCommentResponse(fmt.Sprintf("unable to fetch comments for %s ticket %s", configuration.Tool, ticket.TicketID)), err
	}

	return models.CommentsResponse{
		TicketID: ticket.TicketID,
		Comments: comments,
	}, nil
}

func AddCommentToTicket(ctx *gin.Context, ticket models.Ticket) (models.CommentsResponse, error) {
	var (
		err      error
		config   models.TicketConfigurations
		comments []models.Comments
	)

	switch {
	case ticket.IntegrationID != "":
		config, err = fetchToolConfigurationForTenant(ticket.IntegrationID, ticket.Tenant)
	case ticket.Source != "":
		config, err = fetchToolConfigurationByTenantIdAndTool(ticket.Tenant, ticket.Source)
	default:
		return GetErrorCommentResponse("integration_id or source is required to add a comment"), nil
	}

	if err != nil {
		slog.Error("failed to fetch tool configuration", "integrationID", ticket.IntegrationID, "source", ticket.Source, "error", err)
		return GetErrorCommentResponse(fmt.Sprintf("unable to find ticket configuration for adding comment to ticket %s", ticket.TicketID)), err
	}

	ticket.Source = strings.ToLower(config.Tool)

	manager, ok := ticketmgr.GetTicketManager(config.Tool)
	if !ok {
		slog.Error("unsupported platform for adding comment", "source", ticket.Source, "config.Tool", config.Tool)
		return GetErrorCommentResponse(fmt.Sprintf("unsupported platform: %s", ticket.Source)), nil
	}

	if err := manager.AddComment(ctx, config, ticket); err != nil {
		slog.Error("failed to add comment", "ticketID", ticket.TicketID, "tool", config.Tool, "error", err)
		return GetErrorCommentResponse("unable to add comments to ticket"), err
	}

	// Try to fetch comments after adding (supported by some platforms like Jira)
	comments, err = manager.GetComments(ctx, config, ticket.TicketID)
	if err != nil && !errors.Is(err, ticketmgr.ErrNotSupported) {
		slog.Warn("failed to fetch comments after adding", "ticketID", ticket.TicketID, "error", err)
	}

	return models.CommentsResponse{
		TicketID: ticket.TicketID,
		Comments: comments,
	}, nil
}

func GetErrorCommentResponse(err string) models.CommentsResponse {
	return models.CommentsResponse{
		Error: err,
	}
}

// GetTicketByID retrieves a ticket from the external system by its ID.
func GetTicketByID(ctx *gin.Context, ticket models.Ticket) (*models.Ticket, error) {
	configuration, err := fetchToolConfiguration(ticket.IntegrationID)
	if err != nil {
		slog.Error("Error fetching tool configuration:", "error", slog.AnyValue(err))
		return nil, fmt.Errorf("unable to find ticket configuration")
	}

	if configuration.Tenant != ticket.Tenant {
		slog.Warn("Cross-tenant access attempt", "requestTenant", ticket.Tenant, "configTenant", configuration.Tenant)
		return nil, fmt.Errorf("integration not found")
	}

	// Override project key for platforms that need it (GitHub, GitLab).
	// Deep copy the Projects slice to avoid mutating shared configuration state.
	if ticket.ProjectKey != "" {
		projects := make([]models.Project, len(configuration.Projects))
		copy(projects, configuration.Projects)
		configuration.Projects = projects
		if len(configuration.Projects) > 0 {
			configuration.Projects[0].Key = ticket.ProjectKey
		} else {
			configuration.Projects = []models.Project{{Key: ticket.ProjectKey}}
		}
	}

	manager, ok := ticketmgr.GetTicketManager(configuration.Tool)
	if !ok {
		return nil, fmt.Errorf("unsupported tool")
	}

	result, err := manager.Get(ctx, configuration, ticket.TicketID)
	if errors.Is(err, ticketmgr.ErrNotSupported) {
		return nil, fmt.Errorf("get ticket not supported for this platform")
	}
	if err != nil {
		slog.Error("Failed to get ticket from external system", "error", err)
		return nil, fmt.Errorf("failed to get ticket")
	}

	return result, nil
}

// AcknowledgeTicket acknowledges an incident ticket.
// This is only supported for incident management platforms (PagerDuty, ZenDuty).
func AcknowledgeTicket(ctx *gin.Context, ticket models.Ticket) error {
	configuration, err := fetchToolConfiguration(ticket.IntegrationID)
	if err != nil {
		slog.Error("Error fetching tool configuration:", "error", slog.AnyValue(err))
		return fmt.Errorf("unable to find ticket configuration")
	}

	if configuration.Tenant != ticket.Tenant {
		slog.Warn("Cross-tenant access attempt", "requestTenant", ticket.Tenant, "configTenant", configuration.Tenant)
		return fmt.Errorf("integration not found")
	}

	incidentManager, ok := ticketmgr.GetIncidentManager(configuration.Tool)
	if !ok {
		return fmt.Errorf("acknowledge not supported for platform: %s", configuration.Tool)
	}

	if err := incidentManager.Acknowledge(ctx, configuration, ticket.TicketID); err != nil {
		return err
	}
	syncTicketToDB(ticket.TicketID, ticket.IntegrationID, models.UpdateFields{Status: "acknowledged"})
	return nil
}

// EscalateTicket escalates an incident ticket.
// This is only supported for incident management platforms (PagerDuty, ZenDuty).
func EscalateTicket(ctx *gin.Context, ticket models.Ticket, escalationPolicy string) error {
	configuration, err := fetchToolConfiguration(ticket.IntegrationID)
	if err != nil {
		slog.Error("Error fetching tool configuration:", "error", slog.AnyValue(err))
		return fmt.Errorf("unable to find ticket configuration")
	}

	if configuration.Tenant != ticket.Tenant {
		slog.Warn("Cross-tenant access attempt", "requestTenant", ticket.Tenant, "configTenant", configuration.Tenant)
		return fmt.Errorf("integration not found")
	}

	incidentManager, ok := ticketmgr.GetIncidentManager(configuration.Tool)
	if !ok {
		return fmt.Errorf("escalate not supported for platform: %s", configuration.Tool)
	}

	if err := incidentManager.Escalate(ctx, configuration, ticket.TicketID, escalationPolicy); err != nil {
		return err
	}
	syncTicketToDB(ticket.TicketID, ticket.IntegrationID, models.UpdateFields{Status: "escalated"})
	return nil
}

// ResolveTicket resolves an incident ticket.
// This is only supported for incident management platforms (PagerDuty, ZenDuty).
func ResolveTicket(ctx *gin.Context, ticket models.Ticket, resolution string) error {
	configuration, err := fetchToolConfiguration(ticket.IntegrationID)
	if err != nil {
		slog.Error("Error fetching tool configuration:", "error", slog.AnyValue(err))
		return fmt.Errorf("unable to find ticket configuration")
	}

	if configuration.Tenant != ticket.Tenant {
		slog.Warn("Cross-tenant access attempt", "requestTenant", ticket.Tenant, "configTenant", configuration.Tenant)
		return fmt.Errorf("integration not found")
	}

	incidentManager, ok := ticketmgr.GetIncidentManager(configuration.Tool)
	if !ok {
		return fmt.Errorf("resolve not supported for platform: %s", configuration.Tool)
	}

	if err := incidentManager.Resolve(ctx, configuration, ticket.TicketID, resolution); err != nil {
		return err
	}
	syncTicketToDB(ticket.TicketID, ticket.IntegrationID, models.UpdateFields{Status: "resolved"})
	return nil
}

// UpdateTicket updates fields on a ticket.
func UpdateTicket(ctx *gin.Context, ticket models.Ticket, updateFields models.UpdateFields) error {
	configuration, err := fetchToolConfiguration(ticket.IntegrationID)
	if err != nil {
		slog.Error("Error fetching tool configuration:", "error", slog.AnyValue(err))
		return fmt.Errorf("unable to find ticket configuration")
	}

	if configuration.Tenant != ticket.Tenant {
		slog.Warn("Cross-tenant access attempt", "requestTenant", ticket.Tenant, "configTenant", configuration.Tenant)
		return fmt.Errorf("integration not found")
	}

	// Thread project_key from ticket to update fields for platforms that need it (GitHub, GitLab)
	if updateFields.ProjectKey == "" && ticket.ProjectKey != "" {
		updateFields.ProjectKey = ticket.ProjectKey
	}

	manager, ok := ticketmgr.GetTicketManager(configuration.Tool)
	if !ok {
		return fmt.Errorf("unsupported platform: %s", configuration.Tool)
	}

	if err := manager.Update(ctx, configuration, ticket.TicketID, updateFields); err != nil {
		return err
	}
	syncTicketToDB(ticket.TicketID, ticket.IntegrationID, updateFields)
	return nil
}

// ListTicketsFromProvider retrieves tickets from the external system with filtering and pagination.
func ListTicketsFromProvider(ctx *gin.Context, integrationID, tenantID string, params models.ListParams) (*models.ListResult, error) {
	configuration, err := fetchToolConfigurationForTenant(integrationID, tenantID)
	if err != nil {
		slog.Error("Error fetching tool configuration:", "integrationID", integrationID, "tenant", tenantID, "error", slog.AnyValue(err))
		return nil, fmt.Errorf("unable to find ticket configuration for integration %s", integrationID)
	}

	if !configuration.IsActive {
		return nil, fmt.Errorf("ticket configuration %s (%s) is disabled", configuration.ID, configuration.Tool)
	}

	manager, ok := ticketmgr.GetTicketManager(configuration.Tool)
	if !ok {
		return nil, fmt.Errorf("unsupported tool: %s", configuration.Tool)
	}

	result, err := manager.List(ctx, configuration, params)
	if errors.Is(err, ticketmgr.ErrNotSupported) {
		return nil, fmt.Errorf("list tickets not supported for platform: %s", configuration.Tool)
	}
	if err != nil {
		slog.Error("Failed to list tickets from external system", "tool", configuration.Tool, "error", err)
		return nil, fmt.Errorf("failed to list tickets: %w", err)
	}

	return result, nil
}

// TransitionTicket changes the status of a ticket.
func TransitionTicket(ctx *gin.Context, ticket models.Ticket, status string) error {
	configuration, err := fetchToolConfiguration(ticket.IntegrationID)
	if err != nil {
		slog.Error("Error fetching tool configuration:", "error", slog.AnyValue(err))
		return fmt.Errorf("unable to find ticket configuration")
	}

	if configuration.Tenant != ticket.Tenant {
		slog.Warn("Cross-tenant access attempt", "requestTenant", ticket.Tenant, "configTenant", configuration.Tenant)
		return fmt.Errorf("integration not found")
	}

	// Override project key for platforms that need it (GitHub, GitLab).
	// Deep copy the Projects slice to avoid mutating shared configuration state.
	if ticket.ProjectKey != "" {
		projects := make([]models.Project, len(configuration.Projects))
		copy(projects, configuration.Projects)
		configuration.Projects = projects
		if len(configuration.Projects) > 0 {
			configuration.Projects[0].Key = ticket.ProjectKey
		} else {
			configuration.Projects = []models.Project{{Key: ticket.ProjectKey}}
		}
	}

	manager, ok := ticketmgr.GetTicketManager(configuration.Tool)
	if !ok {
		return fmt.Errorf("unsupported platform: %s", configuration.Tool)
	}

	if err := manager.Transition(ctx, configuration, ticket.TicketID, status); err != nil {
		return err
	}
	syncTicketToDB(ticket.TicketID, ticket.IntegrationID, models.UpdateFields{Status: status})
	return nil
}
