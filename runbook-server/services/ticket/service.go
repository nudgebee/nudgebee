package ticket

import (
	"fmt"
	"io"
	"nudgebee/runbook/common"
	"nudgebee/runbook/config"
	"nudgebee/runbook/services/security"
)

type CreateTicketRequest struct {
	ReferenceId      string         `json:"reference_id" validate:"required"`
	TicketType       string         `json:"ticket_type" validate:"required"`
	Assignee         string         `json:"assignee"`
	ProjectKey       string         `json:"project_key" validate:"required"`
	Title            string         `json:"title" validate:"required"`
	Description      string         `json:"description" validate:"required"`
	Source           string         `json:"source"`
	IntegrationId    string         `json:"integration_id" validate:"required"`
	AdditionalFields map[string]any `json:"additional_fields"`
	AccountId        string         `json:"account_id" validate:"required"`
	Severity         string         `json:"severity"`
	IsNew            bool           `json:"is_new"`
	Tenant           string         `json:"tenant"`
}

type CreateTicketResponse struct {
	Id          string `json:"id" mapstructure:"id"`
	Action      string `json:"action" mapstructure:"action"`
	Message     string `json:"message" mapstructure:"message"`
	Severity    string `json:"severity" mapstructure:"severity"`
	Platform    string `json:"platform" mapstructure:"platform"`
	ReferenceId string `json:"reference_id" mapstructure:"reference_id"`
	TicketId    string `json:"ticket_id" mapstructure:"ticket_id"`
	URL         string `json:"url" mapstructure:"url"`
	Status      string `json:"status" mapstructure:"status"`
}

func CreateTicket(ctx *security.RequestContext, request CreateTicketRequest) (CreateTicketResponse, error) {
	if request.Source == "" {
		request.Source = "runbook"
	}
	request.Tenant = ctx.GetSecurityContext().GetTenantId()
	headers := map[string]string{
		"x-tenant-id": ctx.GetSecurityContext().GetTenantId(),
		"x-user-id":   ctx.GetSecurityContext().GetUserId(),
	}
	resp, err := common.HttpPost(config.Config.TicketServerEndpoint+"/tickets/create-ticket", common.HttpWithHeaders(headers), common.HttpWithJsonBody(request))
	if err != nil {
		ctx.GetLogger().Error("tickets: unable to process request", "error", err)
		return CreateTicketResponse{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		ctx.GetLogger().Error("tickets: unable to read body", "error", err)
		return CreateTicketResponse{}, err
	}

	if resp.StatusCode != 200 {
		ctx.GetLogger().Error("tickets: unable to create ticket", "status", resp.StatusCode, "body", string(body))
		return CreateTicketResponse{}, fmt.Errorf("tickets: unable to create - %s", string(body))
	}

	responseBody := map[string]any{}
	err = common.UnmarshalJson(body, &responseBody)
	if err != nil {
		ctx.GetLogger().Error("tickets: unable to read body", "status", resp.StatusCode, "body", string(body))
		return CreateTicketResponse{}, fmt.Errorf("tickets: unable to create - %s", string(body))
	}

	if ticketDataBody, ok := responseBody["data"]; ok {
		if ticketDataAny, ok2 := ticketDataBody.(map[string]any); ok2 && ticketDataAny["insert_tickets_one"] != nil {
			if ticketInsert, ok3 := ticketDataAny["insert_tickets_one"].(map[string]any); ok3 {
				resp := CreateTicketResponse{}
				if err := common.DecodeMapToStruct(ticketInsert, &resp); err != nil {
					ctx.GetLogger().Error("tickets: unable to read body", "body", string(body))
					return CreateTicketResponse{}, err
				}
				return resp, nil
			}
		}
	}

	return CreateTicketResponse{}, nil
}

type AddTicketCommentRequest struct {
	TicketId      string `json:"ticket_id"`
	Comment       string `json:"comment"`
	IntegrationId string `json:"integration_id"`
	Source        string `json:"source"`
	AccountId     string `json:"account_id"`
	ProjectKey    string `json:"project_key"`
}

type AddTicketCommentResponse struct {
	TicketID string     `json:"ticket_id"`
	Comments []Comments `json:"comments"`
	Error    string     `json:"error,omitempty"`
}

type Comments struct {
	Author  string `json:"author"`
	Comment string `json:"comment"`
	Created string `json:"created_at"`
	Updated string `json:"updated_at"`
}

// Internal structs to match Hasura/Ticket-Server expectations
type ActionTicketRequest struct {
	Action           ActionAction           `json:"action"`
	Input            ActionTicketInput      `json:"input"`
	RequestQuery     string                 `json:"request_query"`
	SessionVariables ActionSessionVariables `json:"session_variables"`
}

type ActionAction struct {
	Name string `json:"name"`
}

type ActionTicketInput struct {
	Object ActionTicketObject `json:"object"`
}

type ActionTicketObject struct {
	TicketID      string `json:"ticket_id"`
	Comment       string `json:"comment"`
	IntegrationID string `json:"integration_id,omitempty"`
	Source        string `json:"source,omitempty"`
	AccountID     string `json:"account_id"`
	Tenant        string `json:"tenant"`
	CreatedBy     string `json:"created_by"`
	ProjectKey    string `json:"project_key,omitempty"`
}

type ActionSessionVariables struct {
	Role         string `json:"role"`
	UserID       string `json:"user_id"`
	UserTenantID string `json:"tenant_id"`
}

// GetTicketRequest represents a request to get ticket details
type GetTicketRequest struct {
	TicketId      string `json:"ticket_id"`
	IntegrationId string `json:"integration_id"`
	AccountId     string `json:"account_id"`
	Source        string `json:"source"`
	ProjectKey    string `json:"project_key"`
}

// GetTicketResponse represents the response from getting a ticket
type GetTicketResponse struct {
	ID          string `json:"id" mapstructure:"id"`
	Title       string `json:"title" mapstructure:"title"`
	Description string `json:"description" mapstructure:"description"`
	Status      string `json:"status" mapstructure:"status"`
	Severity    string `json:"severity" mapstructure:"severity"`
	Assignee    string `json:"assignee" mapstructure:"assignee"`
	URL         string `json:"url" mapstructure:"url"`
	Platform    string `json:"platform" mapstructure:"platform"`
	TicketID    string `json:"ticket_id" mapstructure:"ticket_id"`
	CreatedAt   string `json:"created_at" mapstructure:"created_at"`
	// Raw carries provider-specific fields not in the normalized struct
	// (e.g. ServiceNow cmdb_ci, business_service, all u_* custom fields).
	// nil when the source platform connector does not populate it.
	Raw   map[string]any `json:"raw,omitempty" mapstructure:"raw"`
	Error string         `json:"error,omitempty" mapstructure:"error"`
}

// GetTicket fetches ticket details by ID
func GetTicket(ctx *security.RequestContext, request GetTicketRequest) (GetTicketResponse, error) {
	tenantID := ctx.GetSecurityContext().GetTenantId()
	userID := ctx.GetSecurityContext().GetUserId()

	actionReq := ActionTicketRequest{
		Action: ActionAction{
			Name: "get_ticket",
		},
		Input: ActionTicketInput{
			Object: ActionTicketObject{
				TicketID:      request.TicketId,
				IntegrationID: request.IntegrationId,
				Source:        request.Source,
				AccountID:     request.AccountId,
				Tenant:        tenantID,
				CreatedBy:     userID,
				ProjectKey:    request.ProjectKey,
			},
		},
		SessionVariables: ActionSessionVariables{
			UserID:       userID,
			UserTenantID: tenantID,
		},
	}

	headers := map[string]string{}
	resp, err := common.HttpPost(config.Config.TicketServerEndpoint+"/tickets/hasura/get-ticket", common.HttpWithHeaders(headers), common.HttpWithJsonBody(actionReq))
	if err != nil {
		ctx.GetLogger().Error("tickets: unable to process get ticket request", "error", err)
		return GetTicketResponse{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		ctx.GetLogger().Error("tickets: unable to read response body", "error", err)
		return GetTicketResponse{}, err
	}

	if resp.StatusCode != 200 {
		ctx.GetLogger().Error("tickets: unable to get ticket", "status", resp.StatusCode, "body", string(body))
		return GetTicketResponse{}, fmt.Errorf("tickets: unable to get ticket - %s", string(body))
	}

	var responseBody map[string]any
	err = common.UnmarshalJson(body, &responseBody)
	if err != nil {
		ctx.GetLogger().Error("tickets: unable to unmarshal response body", "body", string(body))
		return GetTicketResponse{}, fmt.Errorf("tickets: unable to parse response - %s", string(body))
	}

	if dataBody, ok := responseBody["data"]; ok {
		response := GetTicketResponse{}
		dataMap, ok := dataBody.(map[string]any)
		if !ok {
			ctx.GetLogger().Error("tickets: data is not a map", "body", string(body))
			return GetTicketResponse{}, fmt.Errorf("tickets: unexpected data format")
		}
		if err := common.DecodeMapToStruct(dataMap, &response); err != nil {
			ctx.GetLogger().Error("tickets: unable to decode ticket data", "body", string(body))
			return GetTicketResponse{}, err
		}
		return response, nil
	}

	if errMsg, ok := responseBody["error"].(string); ok {
		return GetTicketResponse{}, fmt.Errorf("tickets: %s", errMsg)
	}

	return GetTicketResponse{}, nil
}

// SearchTicketsRequest represents a request to search tickets
type SearchTicketsRequest struct {
	ReferenceId string `json:"reference_id"`
	Source      string `json:"source"`
}

// SearchTicketsResponse represents the response from searching tickets
type SearchTicketsResponse struct {
	Tickets []TicketSummary `json:"tickets"`
	Error   string          `json:"error,omitempty"`
}

// TicketSummary represents a ticket in search results
type TicketSummary struct {
	ID          string `json:"id" mapstructure:"id"`
	Title       string `json:"title" mapstructure:"title"`
	Status      string `json:"status" mapstructure:"status"`
	Severity    string `json:"severity" mapstructure:"severity"`
	Platform    string `json:"platform" mapstructure:"platform"`
	TicketID    string `json:"ticket_id" mapstructure:"ticket_id"`
	URL         string `json:"url" mapstructure:"url"`
	ReferenceId string `json:"reference_id" mapstructure:"reference_id"`
}

// SearchTickets searches for tickets by reference ID and source
func SearchTickets(ctx *security.RequestContext, request SearchTicketsRequest) (SearchTicketsResponse, error) {
	headers := map[string]string{}
	resp, err := common.HttpPost(config.Config.TicketServerEndpoint+"/tickets/search", common.HttpWithHeaders(headers), common.HttpWithJsonBody(request))
	if err != nil {
		ctx.GetLogger().Error("tickets: unable to process search request", "error", err)
		return SearchTicketsResponse{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		ctx.GetLogger().Error("tickets: unable to read response body", "error", err)
		return SearchTicketsResponse{}, err
	}

	if resp.StatusCode != 200 {
		ctx.GetLogger().Error("tickets: unable to search tickets", "status", resp.StatusCode, "body", string(body))
		return SearchTicketsResponse{}, fmt.Errorf("tickets: unable to search - %s", string(body))
	}

	var tickets []TicketSummary
	err = common.UnmarshalJson(body, &tickets)
	if err != nil {
		ctx.GetLogger().Error("tickets: unable to unmarshal search response", "body", string(body))
		return SearchTicketsResponse{}, fmt.Errorf("tickets: unable to parse response - %s", string(body))
	}

	return SearchTicketsResponse{Tickets: tickets}, nil
}

// GetTicketCommentsRequest represents a request to get ticket comments
type GetTicketCommentsRequest struct {
	TicketId      string `json:"ticket_id"`
	IntegrationId string `json:"integration_id"`
	AccountId     string `json:"account_id"`
	ProjectKey    string `json:"project_key"`
}

// GetTicketCommentsResponse represents the response from getting ticket comments
type GetTicketCommentsResponse struct {
	TicketID string     `json:"ticket_id"`
	Comments []Comments `json:"comments"`
	Error    string     `json:"error,omitempty"`
}

// GetTicketComments fetches comments for a ticket
func GetTicketComments(ctx *security.RequestContext, request GetTicketCommentsRequest) (GetTicketCommentsResponse, error) {
	tenantID := ctx.GetSecurityContext().GetTenantId()
	userID := ctx.GetSecurityContext().GetUserId()

	actionReq := ActionTicketRequest{
		Action: ActionAction{
			Name: "get_comments",
		},
		Input: ActionTicketInput{
			Object: ActionTicketObject{
				TicketID:      request.TicketId,
				IntegrationID: request.IntegrationId,
				AccountID:     request.AccountId,
				Tenant:        tenantID,
				CreatedBy:     userID,
				ProjectKey:    request.ProjectKey,
			},
		},
		SessionVariables: ActionSessionVariables{
			UserID:       userID,
			UserTenantID: tenantID,
		},
	}

	headers := map[string]string{}
	resp, err := common.HttpPost(config.Config.TicketServerEndpoint+"/tickets/hasura/get-comments", common.HttpWithHeaders(headers), common.HttpWithJsonBody(actionReq))
	if err != nil {
		ctx.GetLogger().Error("tickets: unable to process get comments request", "error", err)
		return GetTicketCommentsResponse{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		ctx.GetLogger().Error("tickets: unable to read response body", "error", err)
		return GetTicketCommentsResponse{}, err
	}

	if resp.StatusCode != 200 {
		ctx.GetLogger().Error("tickets: unable to get comments", "status", resp.StatusCode, "body", string(body))
		return GetTicketCommentsResponse{}, fmt.Errorf("tickets: unable to get comments - %s", string(body))
	}

	var response GetTicketCommentsResponse
	err = common.UnmarshalJson(body, &response)
	if err != nil {
		ctx.GetLogger().Error("tickets: unable to unmarshal comments response", "body", string(body))
		return GetTicketCommentsResponse{}, fmt.Errorf("tickets: unable to parse response - %s", string(body))
	}

	if response.Error != "" {
		return response, fmt.Errorf("tickets: %s", response.Error)
	}

	return response, nil
}

// AcknowledgeTicketRequest represents a request to acknowledge a ticket/incident
type AcknowledgeTicketRequest struct {
	TicketId      string `json:"ticket_id"`
	IntegrationId string `json:"integration_id"`
	AccountId     string `json:"account_id"`
}

// AcknowledgeTicketResponse represents the response from acknowledging a ticket
type AcknowledgeTicketResponse struct {
	TicketID string `json:"ticket_id"`
	Status   string `json:"status"`
	Message  string `json:"message"`
	Error    string `json:"error,omitempty"`
}

// AcknowledgeTicket acknowledges an incident
func AcknowledgeTicket(ctx *security.RequestContext, request AcknowledgeTicketRequest) (AcknowledgeTicketResponse, error) {
	tenantID := ctx.GetSecurityContext().GetTenantId()
	userID := ctx.GetSecurityContext().GetUserId()

	actionReq := ActionTicketRequest{
		Action: ActionAction{
			Name: "acknowledge",
		},
		Input: ActionTicketInput{
			Object: ActionTicketObject{
				TicketID:      request.TicketId,
				IntegrationID: request.IntegrationId,
				AccountID:     request.AccountId,
				Tenant:        tenantID,
				CreatedBy:     userID,
			},
		},
		SessionVariables: ActionSessionVariables{
			UserID:       userID,
			UserTenantID: tenantID,
		},
	}

	headers := map[string]string{}
	resp, err := common.HttpPost(config.Config.TicketServerEndpoint+"/tickets/hasura/acknowledge", common.HttpWithHeaders(headers), common.HttpWithJsonBody(actionReq))
	if err != nil {
		ctx.GetLogger().Error("tickets: unable to process acknowledge request", "error", err)
		return AcknowledgeTicketResponse{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		ctx.GetLogger().Error("tickets: unable to read response body", "error", err)
		return AcknowledgeTicketResponse{}, err
	}

	if resp.StatusCode != 200 {
		ctx.GetLogger().Error("tickets: unable to acknowledge ticket", "status", resp.StatusCode, "body", string(body))
		return AcknowledgeTicketResponse{}, fmt.Errorf("tickets: unable to acknowledge - %s", string(body))
	}

	var response AcknowledgeTicketResponse
	err = common.UnmarshalJson(body, &response)
	if err != nil {
		ctx.GetLogger().Error("tickets: unable to unmarshal response", "body", string(body))
		return AcknowledgeTicketResponse{}, fmt.Errorf("tickets: unable to parse response - %s", string(body))
	}

	if response.Error != "" {
		return response, fmt.Errorf("tickets: %s", response.Error)
	}

	return response, nil
}

// EscalateTicketRequest represents a request to escalate a ticket/incident
type EscalateTicketRequest struct {
	TicketId         string `json:"ticket_id"`
	IntegrationId    string `json:"integration_id"`
	AccountId        string `json:"account_id"`
	EscalationPolicy string `json:"escalation_policy"`
}

// EscalateTicketResponse represents the response from escalating a ticket
type EscalateTicketResponse struct {
	TicketID string `json:"ticket_id"`
	Status   string `json:"status"`
	Message  string `json:"message"`
	Error    string `json:"error,omitempty"`
}

// EscalateTicket escalates an incident
func EscalateTicket(ctx *security.RequestContext, request EscalateTicketRequest) (EscalateTicketResponse, error) {
	tenantID := ctx.GetSecurityContext().GetTenantId()
	userID := ctx.GetSecurityContext().GetUserId()

	// Build hasura request with escalation policy in additional fields
	actionReq := map[string]any{
		"action": map[string]string{
			"name": "escalate",
		},
		"input": map[string]any{
			"object": map[string]any{
				"ticket_id":      request.TicketId,
				"integration_id": request.IntegrationId,
				"account_id":     request.AccountId,
				"tenant":         tenantID,
				"created_by":     userID,
				"additional_fields": map[string]any{
					"escalation_policy": request.EscalationPolicy,
				},
			},
		},
		"session_variables": map[string]string{
			"user_id":   userID,
			"tenant_id": tenantID,
		},
	}

	headers := map[string]string{}
	resp, err := common.HttpPost(config.Config.TicketServerEndpoint+"/tickets/hasura/escalate", common.HttpWithHeaders(headers), common.HttpWithJsonBody(actionReq))
	if err != nil {
		ctx.GetLogger().Error("tickets: unable to process escalate request", "error", err)
		return EscalateTicketResponse{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		ctx.GetLogger().Error("tickets: unable to read response body", "error", err)
		return EscalateTicketResponse{}, err
	}

	if resp.StatusCode != 200 {
		ctx.GetLogger().Error("tickets: unable to escalate ticket", "status", resp.StatusCode, "body", string(body))
		return EscalateTicketResponse{}, fmt.Errorf("tickets: unable to escalate - %s", string(body))
	}

	var response EscalateTicketResponse
	err = common.UnmarshalJson(body, &response)
	if err != nil {
		ctx.GetLogger().Error("tickets: unable to unmarshal response", "body", string(body))
		return EscalateTicketResponse{}, fmt.Errorf("tickets: unable to parse response - %s", string(body))
	}

	if response.Error != "" {
		return response, fmt.Errorf("tickets: %s", response.Error)
	}

	return response, nil
}

// ResolveTicketRequest represents a request to resolve a ticket/incident
type ResolveTicketRequest struct {
	TicketId      string `json:"ticket_id"`
	IntegrationId string `json:"integration_id"`
	AccountId     string `json:"account_id"`
	Resolution    string `json:"resolution"`
}

// ResolveTicketResponse represents the response from resolving a ticket
type ResolveTicketResponse struct {
	TicketID string `json:"ticket_id"`
	Status   string `json:"status"`
	Message  string `json:"message"`
	Error    string `json:"error,omitempty"`
}

// ResolveTicket resolves an incident
// UpdateTicketRequest represents a request to update a ticket
type UpdateTicketRequest struct {
	TicketId      string   `json:"ticket_id"`
	IntegrationId string   `json:"integration_id"`
	AccountId     string   `json:"account_id"`
	Status        string   `json:"status"`
	Severity      string   `json:"severity"`
	Assignee      string   `json:"assignee"`
	Description   string   `json:"description"`
	Labels        []string `json:"labels"`
	ProjectKey    string   `json:"project_key"`
}

// UpdateTicketResponse represents the response from updating a ticket
type UpdateTicketResponse struct {
	TicketID string `json:"ticket_id"`
	Status   string `json:"status"`
	Message  string `json:"message"`
	Error    string `json:"error,omitempty"`
}

// UpdateTicket updates a ticket's fields
func UpdateTicket(ctx *security.RequestContext, request UpdateTicketRequest) (UpdateTicketResponse, error) {
	tenantID := ctx.GetSecurityContext().GetTenantId()
	userID := ctx.GetSecurityContext().GetUserId()

	// Build additional_fields with only non-empty values
	additionalFields := map[string]any{}
	if request.Status != "" {
		additionalFields["status"] = request.Status
	}
	if request.Severity != "" {
		additionalFields["severity"] = request.Severity
	}
	if request.Assignee != "" {
		additionalFields["assignee"] = request.Assignee
	}
	if request.Description != "" {
		additionalFields["description"] = request.Description
	}
	if len(request.Labels) > 0 {
		additionalFields["labels"] = request.Labels
	}

	// Build hasura request with update fields in additional_fields
	actionReq := map[string]any{
		"action": map[string]string{
			"name": "update",
		},
		"input": map[string]any{
			"object": map[string]any{
				"ticket_id":         request.TicketId,
				"integration_id":    request.IntegrationId,
				"account_id":        request.AccountId,
				"project_key":       request.ProjectKey,
				"tenant":            tenantID,
				"created_by":        userID,
				"additional_fields": additionalFields,
			},
		},
		"session_variables": map[string]string{
			"user_id":   userID,
			"tenant_id": tenantID,
		},
	}

	headers := map[string]string{}
	resp, err := common.HttpPost(config.Config.TicketServerEndpoint+"/tickets/hasura/update", common.HttpWithHeaders(headers), common.HttpWithJsonBody(actionReq))
	if err != nil {
		ctx.GetLogger().Error("tickets: unable to process update request", "error", err)
		return UpdateTicketResponse{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		ctx.GetLogger().Error("tickets: unable to read response body", "error", err)
		return UpdateTicketResponse{}, err
	}

	if resp.StatusCode != 200 {
		ctx.GetLogger().Error("tickets: unable to update ticket", "status", resp.StatusCode, "body", string(body))
		return UpdateTicketResponse{}, fmt.Errorf("tickets: unable to update - %s", string(body))
	}

	var response UpdateTicketResponse
	err = common.UnmarshalJson(body, &response)
	if err != nil {
		ctx.GetLogger().Error("tickets: unable to unmarshal response", "body", string(body))
		return UpdateTicketResponse{}, fmt.Errorf("tickets: unable to parse response - %s", string(body))
	}

	if response.Error != "" {
		return response, fmt.Errorf("tickets: %s", response.Error)
	}

	return response, nil
}

// TransitionTicketRequest represents a request to transition a ticket's status
type TransitionTicketRequest struct {
	TicketId      string `json:"ticket_id"`
	IntegrationId string `json:"integration_id"`
	AccountId     string `json:"account_id"`
	Status        string `json:"status"`
	ProjectKey    string `json:"project_key"`
}

// TransitionTicketResponse represents the response from transitioning a ticket
type TransitionTicketResponse struct {
	TicketID string `json:"ticket_id"`
	Status   string `json:"status"`
	Message  string `json:"message"`
	Error    string `json:"error,omitempty"`
}

// TransitionTicket changes the status of a ticket
func TransitionTicket(ctx *security.RequestContext, request TransitionTicketRequest) (TransitionTicketResponse, error) {
	tenantID := ctx.GetSecurityContext().GetTenantId()
	userID := ctx.GetSecurityContext().GetUserId()

	// Build hasura request with status in additional_fields
	actionReq := map[string]any{
		"action": map[string]string{
			"name": "transition",
		},
		"input": map[string]any{
			"object": map[string]any{
				"ticket_id":      request.TicketId,
				"integration_id": request.IntegrationId,
				"account_id":     request.AccountId,
				"tenant":         tenantID,
				"created_by":     userID,
				"project_key":    request.ProjectKey,
				"additional_fields": map[string]any{
					"status": request.Status,
				},
			},
		},
		"session_variables": map[string]string{
			"user_id":   userID,
			"tenant_id": tenantID,
		},
	}

	headers := map[string]string{}
	resp, err := common.HttpPost(config.Config.TicketServerEndpoint+"/tickets/hasura/transition", common.HttpWithHeaders(headers), common.HttpWithJsonBody(actionReq))
	if err != nil {
		ctx.GetLogger().Error("tickets: unable to process transition request", "error", err)
		return TransitionTicketResponse{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		ctx.GetLogger().Error("tickets: unable to read response body", "error", err)
		return TransitionTicketResponse{}, err
	}

	if resp.StatusCode != 200 {
		ctx.GetLogger().Error("tickets: unable to transition ticket", "status", resp.StatusCode, "body", string(body))
		return TransitionTicketResponse{}, fmt.Errorf("tickets: unable to transition - %s", string(body))
	}

	var response TransitionTicketResponse
	err = common.UnmarshalJson(body, &response)
	if err != nil {
		ctx.GetLogger().Error("tickets: unable to unmarshal response", "body", string(body))
		return TransitionTicketResponse{}, fmt.Errorf("tickets: unable to parse response - %s", string(body))
	}

	if response.Error != "" {
		return response, fmt.Errorf("tickets: %s", response.Error)
	}

	return response, nil
}

// AssignTicketRequest represents a request to assign a ticket
type AssignTicketRequest struct {
	TicketId      string `json:"ticket_id"`
	IntegrationId string `json:"integration_id"`
	AccountId     string `json:"account_id"`
	Assignee      string `json:"assignee"`
	ProjectKey    string `json:"project_key"`
}

// AssignTicketResponse represents the response from assigning a ticket
type AssignTicketResponse struct {
	TicketID string `json:"ticket_id"`
	Assignee string `json:"assignee"`
	Message  string `json:"message"`
	Error    string `json:"error,omitempty"`
}

// AssignTicket assigns a ticket to a user
func AssignTicket(ctx *security.RequestContext, request AssignTicketRequest) (AssignTicketResponse, error) {
	tenantID := ctx.GetSecurityContext().GetTenantId()
	userID := ctx.GetSecurityContext().GetUserId()

	// Build hasura request with assignee in additional_fields
	actionReq := map[string]any{
		"action": map[string]string{
			"name": "assign",
		},
		"input": map[string]any{
			"object": map[string]any{
				"ticket_id":      request.TicketId,
				"integration_id": request.IntegrationId,
				"account_id":     request.AccountId,
				"tenant":         tenantID,
				"created_by":     userID,
				"project_key":    request.ProjectKey,
				"additional_fields": map[string]any{
					"assignee": request.Assignee,
				},
			},
		},
		"session_variables": map[string]string{
			"user_id":   userID,
			"tenant_id": tenantID,
		},
	}

	headers := map[string]string{}
	resp, err := common.HttpPost(config.Config.TicketServerEndpoint+"/tickets/hasura/assign", common.HttpWithHeaders(headers), common.HttpWithJsonBody(actionReq))
	if err != nil {
		ctx.GetLogger().Error("tickets: unable to process assign request", "error", err)
		return AssignTicketResponse{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		ctx.GetLogger().Error("tickets: unable to read response body", "error", err)
		return AssignTicketResponse{}, err
	}

	if resp.StatusCode != 200 {
		ctx.GetLogger().Error("tickets: unable to assign ticket", "status", resp.StatusCode, "body", string(body))
		return AssignTicketResponse{}, fmt.Errorf("tickets: unable to assign - %s", string(body))
	}

	var response AssignTicketResponse
	err = common.UnmarshalJson(body, &response)
	if err != nil {
		ctx.GetLogger().Error("tickets: unable to unmarshal response", "body", string(body))
		return AssignTicketResponse{}, fmt.Errorf("tickets: unable to parse response - %s", string(body))
	}

	if response.Error != "" {
		return response, fmt.Errorf("tickets: %s", response.Error)
	}

	return response, nil
}

func ResolveTicket(ctx *security.RequestContext, request ResolveTicketRequest) (ResolveTicketResponse, error) {
	tenantID := ctx.GetSecurityContext().GetTenantId()
	userID := ctx.GetSecurityContext().GetUserId()

	actionReq := ActionTicketRequest{
		Action: ActionAction{
			Name: "resolve",
		},
		Input: ActionTicketInput{
			Object: ActionTicketObject{
				TicketID:      request.TicketId,
				IntegrationID: request.IntegrationId,
				AccountID:     request.AccountId,
				Tenant:        tenantID,
				CreatedBy:     userID,
				Comment:       request.Resolution,
			},
		},
		SessionVariables: ActionSessionVariables{
			UserID:       userID,
			UserTenantID: tenantID,
		},
	}

	headers := map[string]string{}
	resp, err := common.HttpPost(config.Config.TicketServerEndpoint+"/tickets/hasura/resolve", common.HttpWithHeaders(headers), common.HttpWithJsonBody(actionReq))
	if err != nil {
		ctx.GetLogger().Error("tickets: unable to process resolve request", "error", err)
		return ResolveTicketResponse{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		ctx.GetLogger().Error("tickets: unable to read response body", "error", err)
		return ResolveTicketResponse{}, err
	}

	if resp.StatusCode != 200 {
		ctx.GetLogger().Error("tickets: unable to resolve ticket", "status", resp.StatusCode, "body", string(body))
		return ResolveTicketResponse{}, fmt.Errorf("tickets: unable to resolve - %s", string(body))
	}

	var response ResolveTicketResponse
	err = common.UnmarshalJson(body, &response)
	if err != nil {
		ctx.GetLogger().Error("tickets: unable to unmarshal response", "body", string(body))
		return ResolveTicketResponse{}, fmt.Errorf("tickets: unable to parse response - %s", string(body))
	}

	if response.Error != "" {
		return response, fmt.Errorf("tickets: %s", response.Error)
	}

	return response, nil
}

func AddTicketComment(ctx *security.RequestContext, request AddTicketCommentRequest) (AddTicketCommentResponse, error) {
	tenantID := ctx.GetSecurityContext().GetTenantId()
	userID := ctx.GetSecurityContext().GetUserId()

	actionReq := ActionTicketRequest{
		Action: ActionAction{
			Name: "add_comment",
		},
		Input: ActionTicketInput{
			Object: ActionTicketObject{
				TicketID:      request.TicketId,
				Comment:       request.Comment,
				IntegrationID: request.IntegrationId,
				Source:        request.Source,
				AccountID:     request.AccountId,
				Tenant:        tenantID,
				CreatedBy:     userID,
				ProjectKey:    request.ProjectKey,
			},
		},
		SessionVariables: ActionSessionVariables{
			UserID:       userID,
			UserTenantID: tenantID,
		},
	}

	headers := map[string]string{}
	resp, err := common.HttpPost(config.Config.TicketServerEndpoint+"/tickets/hasura/add-comment", common.HttpWithHeaders(headers), common.HttpWithJsonBody(actionReq))
	if err != nil {
		ctx.GetLogger().Error("tickets: unable to process add comment request", "error", err)
		return AddTicketCommentResponse{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		ctx.GetLogger().Error("tickets: unable to read response body", "error", err)
		return AddTicketCommentResponse{}, err
	}

	if resp.StatusCode != 200 {
		ctx.GetLogger().Error("tickets: unable to add comment", "status", resp.StatusCode, "body", string(body))
		return AddTicketCommentResponse{}, fmt.Errorf("tickets: unable to add comment - %s", string(body))
	}

	var response AddTicketCommentResponse
	err = common.UnmarshalJson(body, &response)
	if err != nil {
		ctx.GetLogger().Error("tickets: unable to unmarshal response body", "status", resp.StatusCode, "body", string(body))
		return AddTicketCommentResponse{}, fmt.Errorf("tickets: unable to parse response - %s", string(body))
	}

	if response.Error != "" {
		return response, fmt.Errorf("tickets: error from server - %s", response.Error)
	}

	return response, nil
}
