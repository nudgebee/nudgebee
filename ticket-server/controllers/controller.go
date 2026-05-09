package controllers

import (
	"fmt"
	"log/slog"
	"net/http"
	"nudgebee/tickets-server/common"
	"nudgebee/tickets-server/models"
	"nudgebee/tickets-server/services"
	"strings"

	"github.com/gin-gonic/gin"
)

func AddTicketConfiguration(ctx *gin.Context) {
	var configurationRequest models.ConfigurationRequest
	if err := ctx.ShouldBindJSON(&configurationRequest); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	configuration := configurationRequest.Input.Object
	userID := configurationRequest.SessionVariables.UserID
	configuration.CreatedBy = &userID
	configuration.UpdatedBy = &userID
	configuration.Tenant = configurationRequest.SessionVariables.UserTenantID

	configuration.Name = strings.TrimSpace(configuration.Name)
	if configuration.Name == "" {
		ctx.JSON(http.StatusBadRequest, gin.H{"message": "integration configuration name is required"})
		return
	}

	// On edit, the frontend omits the password when the user didn't re-enter it.
	// Rehydrate from the stored (encrypted) value so auth validation can run.
	if configuration.Password == "" {
		existing, found, err := services.LoadExistingPassword(configuration.ID, configuration.Tenant, configuration.Name, configuration.Tool)
		if err != nil {
			slog.Error("Failed to load existing password", "tool", configuration.Tool, "name", configuration.Name, "error", err)
			ctx.JSON(http.StatusInternalServerError, gin.H{"message": fmt.Sprintf("Failed to load existing integration: %v", err)})
			return
		}
		if found {
			configuration.Password = existing
		}
	}

	// Quick credential-only validation (fast, no full repo/project enumeration)
	if err := services.QuickValidateCredentials(ctx, configuration); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	// Save with empty metadata — projects/priorities/users will be populated asynchronously
	emptyMetadata := []map[string]interface{}{
		{"projects": []models.Project{}},
		{"priorities": []models.Priority{}},
	}
	config, err := services.SaveTicketConfiguration(configuration, emptyMetadata)
	if err != nil {
		slog.Error("Failed to save Ticket configuration", "tool", configuration.Tool, "error", err)
		ctx.JSON(http.StatusBadRequest, gin.H{"message": fmt.Sprintf("Failed to save %s ticket configuration: %v", configuration.Tool, err)})
		return
	}

	// Populate full metadata (projects, priorities, users) in the background
	services.PopulateMetadataAsync(config.ID, configuration)

	ctx.JSON(http.StatusOK, config)
}

func CreateHasuraTicket(c *gin.Context) {
	var ticketRequest models.TicketRequest
	if err := c.ShouldBindJSON(&ticketRequest); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	} else {
		ticket := ticketRequest.Input.Object
		ticket.CreatedBy = ticketRequest.SessionVariables.UserID
		ticket.Tenant = ticketRequest.SessionVariables.UserTenantID

		if ticket.AccountID == "" {
			slog.Error("Failed to create a ticket due to no account id for tenant:", "tenant", ticket.Tenant)
			c.JSON(http.StatusOK, services.GetErrorResponse("Account id should not be empty"))
			return
		}

		// Authorize user request
		auth := &common.Authorization{
			UserID:     ticket.CreatedBy,
			TenantID:   ticket.Tenant,
			AccountID:  ticket.AccountID,
			Permission: "read",
			Category:   "TICKETS",
		}

		if !auth.HasAccess() {
			c.JSON(http.StatusUnauthorized, services.GetErrorResponse("Unauthorized"))
			return
		}

		issue, err := services.CreateIssue(c, ticket)
		if err != nil {
			slog.Error("Failed to create a ticket due to:", "error", slog.AnyValue(err))
			c.JSON(http.StatusBadRequest, issue)
		} else {
			c.JSON(http.StatusOK, issue)
		}
	}
}

func CreateTicket(c *gin.Context) {
	var ticket models.Ticket
	if err := c.ShouldBindJSON(&ticket); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ticket.CreatedBy = c.Request.Header.Get("x-hasura-user-id")
	ticket.Tenant = c.Request.Header.Get("x-hasura-user-tenant-id")

	if ticket.AccountID == "" && ticket.Tenant == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Both account_id and tenant_id are missing"})
		return
	} else if ticket.AccountID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "account_id is required"})
		return
	} else if ticket.Tenant == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "tenant_id is required"})
		return
	}

	issue, err := services.CreateIssue(c, ticket)
	if err != nil {
		slog.Error("Failed to create ticket", "integrationID", ticket.IntegrationID, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to create ticket: %v", err)})
		return
	}

	c.JSON(http.StatusOK, issue)
}

func SearchTicket(c *gin.Context) {
	var filter models.TicketFilter
	if err := c.ShouldBindJSON(&filter); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	tickets, err := services.FindTicketsByReferenceId(filter)
	if err != nil {
		slog.Error("Failed to lookup ticket due to:", "error", slog.AnyValue(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to lookup ticket, " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, tickets)
}

func SyncTickets(c *gin.Context) {
	go services.SyncTickets()
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func SyncConfigurations(ctx *gin.Context) {
	go services.SyncConfigurations()
	ctx.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func GetIssueCreationTemplate(ctx *gin.Context) {
	var templateMetaRequest models.IssueTemplateMetaRequest
	if err := ctx.ShouldBindJSON(&templateMetaRequest); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	templates, err := services.FetchActiveIssueTemplates(ctx, templateMetaRequest)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Unable to fetch issue creation templates for integration %s, project %s: %v", templateMetaRequest.Input.IntegrationId, templateMetaRequest.Input.ProjectKey, err)})
		return
	}
	ctx.JSON(http.StatusOK, templates)
}

func QueryIssueFieldDetails(ctx *gin.Context) {
	var fieldValuesRequest models.FieldValuesRequest
	if err := ctx.ShouldBindJSON(&fieldValuesRequest); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	templates, err := services.QueryIssueFieldDetails(ctx, fieldValuesRequest)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Unable to fetch field details for integration %s, field %s: %v", fieldValuesRequest.Input.IntegrationId, fieldValuesRequest.Input.KEY, err)})
		return
	}
	ctx.JSON(http.StatusOK, templates)
}

func GetTicketComments(ctx *gin.Context) {
	ticket, ok := bindAndAuthoriseTicketRequest(ctx, "read")
	if !ok {
		return
	}

	issue, err := services.FetchTicketComments(ctx, ticket)
	if err != nil {
		slog.Error("Failed to fetch comments due to:", "error", slog.AnyValue(err))
		ctx.JSON(http.StatusBadRequest, issue)
	} else {
		ctx.JSON(http.StatusOK, issue)
	}
}

func AddTicketComment(ctx *gin.Context) {
	ticket, ok := bindAndAuthoriseTicketRequest(ctx, "write")
	if !ok {
		return
	}

	issue, err := services.AddCommentToTicket(ctx, ticket)
	if err != nil {
		slog.Error("Failed to Add comment due to:", "error", slog.AnyValue(err))
		ctx.JSON(http.StatusBadRequest, issue)
	} else {
		ctx.JSON(http.StatusOK, issue)
	}
}

func GetTicketByID(ctx *gin.Context) {
	ticket, ok := bindAndAuthoriseTicketRequest(ctx, "read")
	if !ok {
		return
	}

	result, err := services.GetTicketByID(ctx, ticket)
	if err != nil {
		slog.Error("Failed to get ticket:", "ticketID", ticket.TicketID, "error", err)
		ctx.JSON(http.StatusOK, gin.H{"error": "Failed to get ticket"})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{"data": result})
}

func GetTicket(c *gin.Context) {
	var ticket models.Ticket
	if err := c.ShouldBindJSON(&ticket); err != nil {
		slog.Error("Failed to bind ticket request", "error", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request payload"})
		return
	}

	ticket.CreatedBy = c.Request.Header.Get("x-hasura-user-id")

	if ticket.Tenant == "" {
		ticket.Tenant = c.Request.Header.Get("x-hasura-user-tenant-id")
	}

	if ticket.TicketID == "" || ticket.IntegrationID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ticket_id and integration_id are required"})
		return
	}

	if ticket.AccountID == "" || ticket.Tenant == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "account_id and tenant are required"})
		return
	}

	auth := &common.Authorization{
		UserID:     ticket.CreatedBy,
		TenantID:   ticket.Tenant,
		AccountID:  ticket.AccountID,
		Permission: "read",
		Category:   "TICKETS",
	}

	if !auth.HasAccess() {
		slog.Warn("Unauthorized access attempt", "user", ticket.CreatedBy, "tenant", ticket.Tenant)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	result, err := services.GetTicketByID(c, ticket)
	if err != nil {
		slog.Error("Failed to get ticket", "ticketID", ticket.TicketID, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get ticket"})
		return
	}

	c.JSON(http.StatusOK, result)
}

func AcknowledgeTicket(ctx *gin.Context) {
	ticket, ok := bindAndAuthoriseTicketRequest(ctx, "write")
	if !ok {
		return
	}

	err := services.AcknowledgeTicket(ctx, ticket)
	if err != nil {
		slog.Error("Failed to acknowledge ticket:", "ticketID", ticket.TicketID, "error", err)
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to acknowledge ticket: " + err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{
		"ticket_id": ticket.TicketID,
		"status":    "acknowledged",
		"message":   "Ticket acknowledged successfully",
	})
}

func EscalateTicket(ctx *gin.Context) {
	ticket, ok := bindAndAuthoriseTicketRequest(ctx, "write")
	if !ok {
		return
	}

	// Get escalation_policy from additional fields if present
	escalationPolicy := ""
	if ticket.AdditionalFields != nil {
		if fields, ok := ticket.AdditionalFields.(map[string]any); ok {
			if ep, ok := fields["escalation_policy"].(string); ok {
				escalationPolicy = ep
			}
		}
	}

	err := services.EscalateTicket(ctx, ticket, escalationPolicy)
	if err != nil {
		slog.Error("Failed to escalate ticket:", "ticketID", ticket.TicketID, "error", err)
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to escalate ticket: " + err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{
		"ticket_id": ticket.TicketID,
		"status":    "escalated",
		"message":   "Ticket escalated successfully",
	})
}

func ResolveTicket(ctx *gin.Context) {
	ticket, ok := bindAndAuthoriseTicketRequest(ctx, "write")
	if !ok {
		return
	}

	// Get resolution from comment field
	resolution := ticket.Comment

	err := services.ResolveTicket(ctx, ticket, resolution)
	if err != nil {
		slog.Error("Failed to resolve ticket:", "ticketID", ticket.TicketID, "error", err)
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to resolve ticket: " + err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{
		"ticket_id": ticket.TicketID,
		"status":    "resolved",
		"message":   "Ticket resolved successfully",
	})
}

func UpdateTicket(ctx *gin.Context) {
	ticket, ok := bindAndAuthoriseTicketRequest(ctx, "write")
	if !ok {
		return
	}

	// Build update fields from additional_fields
	updateFields := models.UpdateFields{}
	if ticket.AdditionalFields != nil {
		if fields, ok := ticket.AdditionalFields.(map[string]any); ok {
			if status, ok := fields["status"].(string); ok {
				updateFields.Status = status
			}
			if severity, ok := fields["severity"].(string); ok {
				updateFields.Severity = severity
			}
			if assignee, ok := fields["assignee"].(string); ok {
				updateFields.Assignee = assignee
			}
			if description, ok := fields["description"].(string); ok {
				updateFields.Description = description
			}
			if labelsRaw, ok := fields["labels"].([]any); ok {
				labels := make([]string, 0, len(labelsRaw))
				for _, l := range labelsRaw {
					if s, ok := l.(string); ok {
						labels = append(labels, s)
					}
				}
				updateFields.Labels = labels
			}
		}
	}

	err := services.UpdateTicket(ctx, ticket, updateFields)
	if err != nil {
		slog.Error("Failed to update ticket:", "ticketID", ticket.TicketID, "error", err)
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update ticket: " + err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{
		"ticket_id": ticket.TicketID,
		"status":    "updated",
		"message":   "Ticket updated successfully",
	})
}

func TransitionTicket(ctx *gin.Context) {
	ticket, ok := bindAndAuthoriseTicketRequest(ctx, "write")
	if !ok {
		return
	}

	// Get target status from additional_fields
	status := ""
	if ticket.AdditionalFields != nil {
		if fields, ok := ticket.AdditionalFields.(map[string]any); ok {
			if s, ok := fields["status"].(string); ok {
				status = s
			}
		}
	}

	if status == "" {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "status is required for transition"})
		return
	}

	err := services.TransitionTicket(ctx, ticket, status)
	if err != nil {
		slog.Error("Failed to transition ticket:", "ticketID", ticket.TicketID, "error", err)
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to transition ticket: " + err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{
		"ticket_id": ticket.TicketID,
		"status":    status,
		"message":   "Ticket transitioned successfully",
	})
}

func AssignTicket(ctx *gin.Context) {
	ticket, ok := bindAndAuthoriseTicketRequest(ctx, "write")
	if !ok {
		return
	}

	// Get assignee from additional_fields
	assignee := ""
	if ticket.AdditionalFields != nil {
		if fields, ok := ticket.AdditionalFields.(map[string]any); ok {
			if a, ok := fields["assignee"].(string); ok {
				assignee = a
			}
		}
	}

	if assignee == "" {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "assignee is required"})
		return
	}

	updateFields := models.UpdateFields{Assignee: assignee}
	err := services.UpdateTicket(ctx, ticket, updateFields)
	if err != nil {
		slog.Error("Failed to assign ticket:", "ticketID", ticket.TicketID, "error", err)
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to assign ticket: " + err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{
		"ticket_id": ticket.TicketID,
		"assignee":  assignee,
		"message":   "Ticket assigned successfully",
	})
}

func TestConnection(ctx *gin.Context) {
	var req models.TestConnectionRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	integrationID := req.Input.IntegrationID
	tenantID := req.SessionVariables.UserTenantID

	if integrationID == "" {
		ctx.JSON(http.StatusBadRequest, models.TestConnectionResponse{
			Success: false,
			Error:   "integration_id is required",
		})
		return
	}

	if tenantID == "" {
		ctx.JSON(http.StatusBadRequest, models.TestConnectionResponse{
			Success: false,
			Error:   "tenant_id is required",
		})
		return
	}

	result := services.TestConnection(ctx, integrationID, tenantID)
	ctx.JSON(http.StatusOK, result)
}

func ListTickets(c *gin.Context) {
	var req models.ListTicketsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request payload"})
		return
	}

	userID := c.Request.Header.Get("x-hasura-user-id")
	tenantID := c.Request.Header.Get("x-hasura-user-tenant-id")

	if req.IntegrationID == "" || req.AccountID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "integration_id and account_id are required"})
		return
	}

	if tenantID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "tenant_id is required"})
		return
	}

	if req.Params.ProjectKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "params.project_key is required"})
		return
	}

	// Apply defaults
	if req.Params.Limit <= 0 {
		req.Params.Limit = 20
	}
	if req.Params.Limit > 100 {
		req.Params.Limit = 100
	}
	if req.Params.Offset < 0 {
		req.Params.Offset = 0
	}
	if req.Params.SortBy == "" {
		req.Params.SortBy = "created_at"
	}
	if req.Params.SortOrder == "" {
		req.Params.SortOrder = "desc"
	}

	auth := &common.Authorization{
		UserID:     userID,
		TenantID:   tenantID,
		AccountID:  req.AccountID,
		Permission: "read",
		Category:   "TICKETS",
	}

	if !auth.HasAccess() {
		slog.Warn("Unauthorized access attempt", "user", userID, "tenant", tenantID)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	result, err := services.ListTicketsFromProvider(c, req.IntegrationID, tenantID, req.Params)
	if err != nil {
		slog.Error("Failed to list tickets", "integrationID", req.IntegrationID, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, result)
}

func bindAndAuthoriseTicketRequest(ctx *gin.Context, permission string) (models.Ticket, bool) {
	var req models.TicketRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		slog.Error("failed to bind ticket request", "error", err)
		ctx.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid request payload: %v", err)})
		return models.Ticket{}, false
	}

	ticket := req.Input.Object
	ticket.CreatedBy = req.SessionVariables.UserID
	ticket.Tenant = req.SessionVariables.UserTenantID

	if ticket.TicketID == "" || ticket.AccountID == "" {
		slog.Warn("input validation failed: missing TicketID or AccountID",
			"tenant", ticket.Tenant,
			"user", ticket.CreatedBy)
		ctx.JSON(http.StatusBadRequest, services.GetErrorCommentResponse("Account ID and Ticket ID should not be empty"))
		return models.Ticket{}, false
	}

	if req.SessionVariables.HasuraRole != "admin" {
		auth := &common.Authorization{
			UserID:     ticket.CreatedBy,
			TenantID:   ticket.Tenant,
			AccountID:  ticket.AccountID,
			Permission: permission,
			Category:   "TICKETS",
		}

		if !auth.HasAccess() {
			slog.Warn("unauthorized access attempt",
				"user", ticket.CreatedBy,
				"tenant", ticket.Tenant,
				"account", ticket.AccountID,
				"permission", permission)
			ctx.JSON(http.StatusUnauthorized, services.GetErrorCommentResponse("Unauthorized"))
			return models.Ticket{}, false
		}
	}

	return ticket, true
}
