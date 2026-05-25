package ticket

import (
	"nudgebee/tickets-server/models"

	"github.com/gin-gonic/gin"
)

// TicketManager defines base operations for all ticketing tools (Jira, GitHub, ServiceNow, PagerDuty, ZenDuty).
// This interface provides a common contract for ticket/issue management operations.
type TicketManager interface {
	// Create creates a new ticket/issue in the external system.
	// Returns the updated ticket with external system details (ID, URL, status).
	Create(ctx *gin.Context, config models.TicketConfigurations, ticket models.Ticket) (models.Ticket, error)

	// GetCreateMeta fetches metadata for creating tickets (available fields, allowed values, etc.).
	// The projectKey parameter identifies the project/service to fetch metadata for.
	GetCreateMeta(ctx *gin.Context, config models.TicketConfigurations, projectKey string) (interface{}, error)

	// AddComment adds a comment/note to an existing ticket.
	// The ticket.TicketID identifies the target ticket, and ticket.Comment contains the comment text.
	AddComment(ctx *gin.Context, config models.TicketConfigurations, ticket models.Ticket) error

	// GetComments retrieves all comments from an existing ticket.
	GetComments(ctx *gin.Context, config models.TicketConfigurations, ticketID string) ([]models.Comments, error)

	// Get retrieves a ticket by its ID from the external system.
	Get(ctx *gin.Context, config models.TicketConfigurations, ticketID string) (*models.Ticket, error)

	// Update updates fields on an existing ticket.
	// The updateFields parameter specifies which fields to update.
	Update(ctx *gin.Context, config models.TicketConfigurations, ticketID string, updateFields models.UpdateFields) error

	// Transition changes the status/state of a ticket.
	Transition(ctx *gin.Context, config models.TicketConfigurations, ticketID string, status string) error

	// List retrieves tickets from the external system with filtering and pagination.
	List(ctx *gin.Context, config models.TicketConfigurations, params models.ListParams) (*models.ListResult, error)
}

// IncidentManager extends TicketManager with incident-specific operations.
// This interface is implemented by incident management tools like PagerDuty and ZenDuty.
type IncidentManager interface {
	TicketManager // Embeds base interface

	// Acknowledge acknowledges an incident (typically stops escalation).
	Acknowledge(ctx *gin.Context, config models.TicketConfigurations, incidentID string) error

	// Escalate escalates an incident to a different escalation policy or level.
	Escalate(ctx *gin.Context, config models.TicketConfigurations, incidentID string, escalationPolicy string) error

	// Resolve marks an incident as resolved with an optional resolution message.
	Resolve(ctx *gin.Context, config models.TicketConfigurations, incidentID string, resolution string) error

	// GetUrgencies returns the available urgency levels for the incident management system.
	// Example: []string{"low", "medium", "high"} for ZenDuty
	GetUrgencies() []string
}
