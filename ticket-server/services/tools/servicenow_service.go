package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"nudgebee/tickets-server/common"
	"nudgebee/tickets-server/models"
	"nudgebee/tickets-server/services/ticket"
	"nudgebee/tickets-server/utils"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	servicenowsdkgo "github.com/michaeldcanady/servicenow-sdk-go"
	"github.com/michaeldcanady/servicenow-sdk-go/credentials"
	tableapi "github.com/michaeldcanady/servicenow-sdk-go/table-api"
)

const (
	servicenowGetTimeout = 10 * time.Second
	servicenowGetMaxBody = 4 << 20 // 4 MiB — incident records can be large with full enrichment
)

type ServiceNowService struct{}

var _ ticket.IncidentManager = (*ServiceNowService)(nil)

func init() {
	ticket.RegisterIncidentManager("servicenow", &ServiceNowService{})
}

func (s *ServiceNowService) Create(ctx *gin.Context, config models.TicketConfigurations, t models.Ticket) (models.Ticket, error) {
	return CreateServiceNowIncident(ctx, config, t)
}

func (s *ServiceNowService) GetCreateMeta(ctx *gin.Context, config models.TicketConfigurations, projectKey string) (interface{}, error) {
	return nil, ticket.ErrNotSupported
}

func (s *ServiceNowService) AddComment(ctx *gin.Context, config models.TicketConfigurations, t models.Ticket) error {
	return AddServiceNowIncidentComment(ctx, config, t)
}

func (s *ServiceNowService) GetComments(ctx *gin.Context, config models.TicketConfigurations, ticketID string) ([]models.Comments, error) {
	return nil, ticket.ErrNotSupported
}

// getRecordStringValue extracts a string value from a TableRecord field.
func getRecordStringValue(record *tableapi.TableRecord, key string) (string, error) {
	elem, err := record.Get(key)
	if err != nil {
		return "", err
	}
	val, err := elem.GetValue()
	if err != nil {
		return "", err
	}
	strVal, err := val.GetStringValue()
	if err != nil {
		return "", err
	}
	if strVal == nil {
		return "", nil
	}
	return *strVal, nil
}

// newServiceNowTableBuilder creates a ServiceNow client and table request builder.
func newServiceNowTableBuilder(config models.TicketConfigurations, tableName string) (*tableapi.TableRequestBuilder2[*tableapi.TableRecord], error) {
	cred := credentials.NewBasicProvider(config.Username, config.Password)
	baseURL := "https://" + strings.TrimPrefix(config.URL, "https://")
	client, err := servicenowsdkgo.NewServiceNowServiceClient(
		servicenowsdkgo.WithAuthenticationProvider(cred),
		servicenowsdkgo.WithURL(baseURL),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create ServiceNow client: %w", err)
	}
	return tableapi.NewDefaultTableRequestBuilder2Internal(map[string]string{
		"baseurl": baseURL,
		"table":   tableName,
	}, client.GetRequestAdapter()), nil
}

func (s *ServiceNowService) Get(ctx *gin.Context, config models.TicketConfigurations, ticketID string) (*models.Ticket, error) {
	// Direct Table API call with sysparm_display_value=all so reference fields
	// (cmdb_ci, business_service, caller_id, …) come back as
	// {"value": "...", "display_value": "..."} and every u_* / business_*
	// field is included. We bypass the SDK here (used elsewhere in this file
	// for backward compatibility) because the SDK's TableRecord parser
	// doesn't expose the raw key set, which we need to populate Ticket.Raw.
	record, err := getServiceNowIncidentRecord(ctx, config, ticketID)
	if err != nil {
		return nil, err
	}

	number := refOrString(record["number"])
	shortDesc := refOrString(record["short_description"])
	description := refOrString(record["description"])
	state := refValue(record["state"])     // raw value preferred for state-code mapping
	urgency := refValue(record["urgency"]) // raw value preferred for priority mapping
	sysID := refValue(record["sys_id"])
	createdOnStr := refValue(record["sys_created_on"])

	var createdAt *time.Time
	if createdOnStr != "" {
		// ServiceNow datetime format
		parsed, perr := time.Parse("2006-01-02 15:04:05", createdOnStr)
		if perr == nil {
			createdAt = &parsed
		}
	}

	return &models.Ticket{
		TicketID:    number,
		Title:       shortDesc,
		Description: description,
		Status:      mapServiceNowState(state),
		Severity:    getNudgebeePriority(urgency),
		Platform:    "servicenow",
		URL:         fmt.Sprintf("https://%s/incident.do?sys_id=%s", strings.TrimPrefix(config.URL, "https://"), sysID),
		CreatedAt:   createdAt,
		Raw:         record,
	}, nil
}

// getServiceNowIncidentRecord performs a single GET against the Table API for
// an incident identified by its number (e.g. INC0010072). Returns the full
// record map with reference fields in {value, display_value} shape. Bypasses
// the SDK so callers can introspect the full key set (the SDK keeps that
// list unexported on TableRecord).
func getServiceNowIncidentRecord(ctx context.Context, config models.TicketConfigurations, ticketID string) (map[string]any, error) {
	// SNOW query operators (^, =, <, >, !, …) embedded in the ticket id
	// would let an attacker rewrite the WHERE clause. Reject anything
	// outside the safe charset before composing the query.
	sanitizedID, err := sanitizeServiceNowQueryValue(ticketID)
	if err != nil {
		return nil, fmt.Errorf("invalid ticket ID: %w", err)
	}

	base := strings.TrimRight(config.URL, "/")
	if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
		base = "https://" + base
	}

	q := url.Values{}
	q.Set("sysparm_query", "number="+sanitizedID)
	q.Set("sysparm_limit", "1")
	q.Set("sysparm_display_value", "all")
	q.Set("sysparm_exclude_reference_link", "true")
	endpoint := fmt.Sprintf("%s/api/now/table/incident?%s", base, q.Encode())

	httpCtx, cancel := context.WithTimeout(ctx, servicenowGetTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(httpCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("snow get: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.SetBasicAuth(config.Username, config.Password)

	resp, err := common.HttpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("snow get: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, rerr := io.ReadAll(io.LimitReader(resp.Body, servicenowGetMaxBody))
	if rerr != nil {
		return nil, fmt.Errorf("snow get: read body: %w", rerr)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("snow get: returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var envelope struct {
		Result []map[string]any `json:"result"`
	}
	if jerr := json.Unmarshal(body, &envelope); jerr != nil {
		return nil, fmt.Errorf("snow get: unmarshal: %w", jerr)
	}
	if len(envelope.Result) == 0 {
		return nil, fmt.Errorf("incident not found: %s", ticketID)
	}
	return envelope.Result[0], nil
}

// refOrString returns display_value when v is a {value, display_value} map,
// or the bare string when v is already a string. Used for human-facing
// fields like short_description and reference fields like cmdb_ci where
// the display form is more useful than the sys_id.
func refOrString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case map[string]any:
		if dv, ok := t["display_value"].(string); ok && dv != "" {
			return dv
		}
		if rv, ok := t["value"].(string); ok {
			return rv
		}
	}
	return ""
}

// refValue returns the raw (database) value side of a SNOW field, regardless
// of whether the field came back as a scalar string or a {value,
// display_value} map. Used for codes that drive downstream mapping (state,
// urgency) where we want "1" not "New".
func refValue(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case map[string]any:
		if rv, ok := t["value"].(string); ok {
			return rv
		}
	}
	return ""
}

// mapServiceNowState maps ServiceNow state numbers to readable status strings.
func mapServiceNowState(state string) string {
	switch state {
	case "1":
		return "New"
	case "2":
		return "In Progress"
	case "3":
		return "On Hold"
	case "6":
		return "Resolved"
	case "7":
		return "Closed"
	case "8":
		return "Canceled"
	default:
		return state
	}
}

// Acknowledge acknowledges a ServiceNow incident by setting state to "In Progress".
func (s *ServiceNowService) Acknowledge(ctx *gin.Context, config models.TicketConfigurations, incidentID string) error {
	if err := utils.ValidateServiceNowTicketID(incidentID); err != nil {
		return fmt.Errorf("invalid incident ID: %w", err)
	}
	return updateServiceNowIncidentState(ctx, config, incidentID, "2") // 2 = In Progress
}

// Escalate escalates a ServiceNow incident by updating the escalation level.
func (s *ServiceNowService) Escalate(ctx *gin.Context, config models.TicketConfigurations, incidentID string, escalationPolicy string) error {
	if err := utils.ValidateServiceNowTicketID(incidentID); err != nil {
		return fmt.Errorf("invalid incident ID: %w", err)
	}
	// ServiceNow uses escalation field; if not provided, increment by 1
	escalation := "1"
	if escalationPolicy != "" {
		escalation = escalationPolicy
	}
	return updateServiceNowIncidentField(ctx, config, incidentID, "escalation", escalation)
}

// Resolve resolves a ServiceNow incident by setting state to "Resolved".
func (s *ServiceNowService) Resolve(ctx *gin.Context, config models.TicketConfigurations, incidentID string, resolution string) error {
	if err := utils.ValidateServiceNowTicketID(incidentID); err != nil {
		return fmt.Errorf("invalid incident ID: %w", err)
	}

	tableBuilder, err := newServiceNowTableBuilder(config, "incident")
	if err != nil {
		return err
	}

	getConfig := &tableapi.TableRequestBuilder2GetRequestConfiguration{
		QueryParameters: &tableapi.TableRequestBuilder2GetQueryParameters{
			Query:  "number=" + incidentID,
			Fields: []string{"sys_id"},
		},
	}
	response, err := tableBuilder.Get(ctx, getConfig)
	if err != nil {
		return fmt.Errorf("incident not found: %s", incidentID)
	}
	results, _ := response.GetResult()
	if len(results) == 0 {
		return fmt.Errorf("incident not found: %s", incidentID)
	}

	sysID, _ := getRecordStringValue(results[0], "sys_id")
	itemBuilder := tableBuilder.ById(sysID)

	record := tableapi.NewTableRecord()
	_ = record.SetValue("state", "6") // 6 = Resolved
	if resolution != "" {
		_ = record.SetValue("close_notes", resolution)
	}

	_, err = itemBuilder.Put(ctx, record, nil)
	return err
}

// GetUrgencies returns available urgency levels for ServiceNow.
func (s *ServiceNowService) GetUrgencies() []string {
	return []string{"1", "2", "3"} // 1=High, 2=Medium, 3=Low
}

// updateServiceNowIncidentState updates the state of a ServiceNow incident.
func updateServiceNowIncidentState(ctx context.Context, config models.TicketConfigurations, incidentID string, state string) error {
	return updateServiceNowIncidentField(ctx, config, incidentID, "state", state)
}

// updateServiceNowIncidentField updates a field on a ServiceNow incident.
func updateServiceNowIncidentField(ctx context.Context, config models.TicketConfigurations, incidentID string, field string, value string) error {
	tableBuilder, err := newServiceNowTableBuilder(config, "incident")
	if err != nil {
		return err
	}

	getConfig := &tableapi.TableRequestBuilder2GetRequestConfiguration{
		QueryParameters: &tableapi.TableRequestBuilder2GetQueryParameters{
			Query:  "number=" + incidentID,
			Fields: []string{"sys_id"},
		},
	}
	response, err := tableBuilder.Get(ctx, getConfig)
	if err != nil {
		return fmt.Errorf("incident not found: %s", incidentID)
	}
	results, _ := response.GetResult()
	if len(results) == 0 {
		return fmt.Errorf("incident not found: %s", incidentID)
	}

	sysID, _ := getRecordStringValue(results[0], "sys_id")
	itemBuilder := tableBuilder.ById(sysID)

	record := tableapi.NewTableRecord()
	_ = record.SetValue(field, value)

	_, err = itemBuilder.Put(ctx, record, nil)
	return err
}

// serviceNowSafeValue allows alphanumeric, spaces, hyphens, underscores, dots, colons, @, and +.
// This blocks ServiceNow query operators (^, =, <, >, !, etc.) to prevent query injection.
var serviceNowSafeValue = regexp.MustCompile(`^[a-zA-Z0-9 _\-\.@:+]+$`)

func sanitizeServiceNowQueryValue(value string) (string, error) {
	if !serviceNowSafeValue.MatchString(value) {
		return "", fmt.Errorf("invalid query value: contains disallowed characters")
	}
	return value, nil
}

// List retrieves incidents from ServiceNow with filtering and pagination.
func (s *ServiceNowService) List(ctx *gin.Context, config models.TicketConfigurations, params models.ListParams) (*models.ListResult, error) {
	tableBuilder, err := newServiceNowTableBuilder(config, "incident")
	if err != nil {
		return nil, err
	}

	// Build query
	var queryParts []string
	if params.Status != "" {
		// Map common status names to ServiceNow states
		state := ""
		switch strings.ToLower(params.Status) {
		case "new", "open":
			state = "1"
		case "in progress", "in_progress":
			state = "2"
		case "on hold", "on_hold":
			state = "3"
		case "resolved":
			state = "6"
		case "closed":
			state = "7"
		default:
			return nil, fmt.Errorf("unsupported status filter: %s", params.Status)
		}
		queryParts = append(queryParts, "state="+state)
	}
	if params.Priority != "" {
		queryParts = append(queryParts, "urgency="+getServiceNowPriority(params.Priority))
	}
	if params.Assignee != "" {
		sanitized, err := sanitizeServiceNowQueryValue(params.Assignee)
		if err != nil {
			return nil, fmt.Errorf("invalid assignee filter: %w", err)
		}
		queryParts = append(queryParts, "assigned_to="+sanitized)
	}
	if params.CreatedAfter != "" {
		t, err := time.Parse(time.RFC3339, params.CreatedAfter)
		if err != nil {
			return nil, fmt.Errorf("invalid created_after filter: must be RFC3339 format (e.g. 2026-01-02T15:04:05Z)")
		}
		queryParts = append(queryParts, "sys_created_on>"+t.Format("2006-01-02 15:04:05"))
	}
	if params.CreatedBefore != "" {
		t, err := time.Parse(time.RFC3339, params.CreatedBefore)
		if err != nil {
			return nil, fmt.Errorf("invalid created_before filter: must be RFC3339 format (e.g. 2026-01-02T15:04:05Z)")
		}
		queryParts = append(queryParts, "sys_created_on<"+t.Format("2006-01-02 15:04:05"))
	}

	query := strings.Join(queryParts, "^")

	// Add sort
	orderField := "sys_created_on"
	if params.SortBy == "updated_at" {
		orderField = "sys_updated_on"
	}
	if params.SortOrder == "asc" {
		if query != "" {
			query += "^"
		}
		query += "ORDERBY" + orderField
	} else {
		if query != "" {
			query += "^"
		}
		query += "ORDERBYDESC" + orderField
	}

	getConfig := &tableapi.TableRequestBuilder2GetRequestConfiguration{
		QueryParameters: &tableapi.TableRequestBuilder2GetQueryParameters{
			Query:  query,
			Fields: []string{"sys_id", "number", "short_description", "state", "urgency", "assigned_to", "sys_created_on"},
			Limit:  params.Limit,
			Offset: params.Offset,
		},
	}

	collectionResponse, err := tableBuilder.Get(ctx, getConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to query ServiceNow incidents: %w", err)
	}

	results, err := collectionResponse.GetResult()
	if err != nil {
		return nil, fmt.Errorf("failed to get ServiceNow results: %w", err)
	}

	// Estimate total from results (Count() is no longer available in v2 API)
	total := params.Offset + len(results)

	tickets := make([]models.Ticket, 0, len(results))
	host := strings.TrimPrefix(config.URL, "https://")
	for _, record := range results {
		number, _ := getRecordStringValue(record, "number")
		shortDesc, _ := getRecordStringValue(record, "short_description")
		state, _ := getRecordStringValue(record, "state")
		urgency, _ := getRecordStringValue(record, "urgency")
		sysID, _ := getRecordStringValue(record, "sys_id")
		createdOnStr, _ := getRecordStringValue(record, "sys_created_on")

		var createdAt *time.Time
		if createdOnStr != "" {
			if parsed, parseErr := time.Parse("2006-01-02 15:04:05", createdOnStr); parseErr == nil {
				createdAt = &parsed
			}
		}

		tickets = append(tickets, models.Ticket{
			TicketID:  number,
			Title:     shortDesc,
			Status:    mapServiceNowState(state),
			Severity:  getNudgebeePriority(urgency),
			Platform:  "servicenow",
			URL:       fmt.Sprintf("https://%s/incident.do?sys_id=%s", host, sysID),
			CreatedAt: createdAt,
		})
	}

	return &models.ListResult{
		Tickets: tickets,
		Total:   total,
		Limit:   params.Limit,
		Offset:  params.Offset,
	}, nil
}

func CreateServiceNowIncident(ctx *gin.Context, configuration models.TicketConfigurations, ticket models.Ticket) (models.Ticket, error) {
	if ticket.Severity == "" {
		ticket.Severity = "Low"
	}

	host := strings.TrimPrefix(configuration.URL, "https://")
	apiURL := "https://" + host + "/api/now/v1/table/incident"

	body := map[string]string{
		"short_description": ticket.Title,
		"description":       ticket.Description,
		"urgency":           getServiceNowPriority(ticket.Severity),
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return models.Ticket{}, fmt.Errorf("failed to marshal request body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(jsonBody))
	if err != nil {
		return models.Ticket{}, fmt.Errorf("failed to create HTTP request: %w", err)
	}
	req.SetBasicAuth(configuration.Username, configuration.Password)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := common.HttpClient().Do(req)
	if err != nil {
		slog.Error("Error creating ServiceNow incident:", "error", slog.AnyValue(err))
		return ticket, fmt.Errorf("failed to send request to ServiceNow: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return ticket, fmt.Errorf("failed to read ServiceNow response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		slog.Error("ServiceNow API error", "status", resp.StatusCode, "body", string(respBody))
		return ticket, fmt.Errorf("ServiceNow API returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Result struct {
			SysID   string `json:"sys_id"`
			Number  string `json:"number"`
			Urgency string `json:"urgency"`
		} `json:"result"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return ticket, fmt.Errorf("failed to parse ServiceNow response: %w", err)
	}

	ticket.TicketID = result.Result.Number
	ticket.Status = "open"
	ticket.Severity = getNudgebeePriority(result.Result.Urgency)
	ticket.URL = fmt.Sprintf("https://%s/incident.do?sys_id=%s", host, result.Result.SysID)
	ticket.Platform = "servicenow"
	now := time.Now()
	ticket.CreatedAt = &now

	return ticket, nil
}

func getServiceNowPriority(priority string) string {
	switch priority {
	case "High":
		return "1"
	case "Medium":
		return "2"
	case "Low":
		return "3"
	default:
		return "3"
	}
}

func getNudgebeePriority(priority string) string {
	switch priority {
	case "1":
		return "High"
	case "2":
		return "Medium"
	case "3":
		return "Low"
	default:
		return "Low"
	}
}

var (
	mdCodeFenceRe  = regexp.MustCompile("(?s)```[a-zA-Z0-9]*\\n?(.*?)```")
	mdInlineCodeRe = regexp.MustCompile("`([^`\\n]+?)`")
	mdImageRe      = regexp.MustCompile(`!\[([^\]]*)\]\([^)]*\)`)
	mdLinkRe       = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	mdHeaderRe     = regexp.MustCompile(`(?m)^#{1,6}\s+`)
	mdBulletRe     = regexp.MustCompile(`(?m)^(\s*)[-*+]\s+`)
	mdHrRe         = regexp.MustCompile(`(?m)^\s*(?:-{3,}|\*{3,}|={3,})\s*$`)
	mdBoldRe       = regexp.MustCompile(`(?:\*\*|__)(.+?)(?:\*\*|__)`)
	mdItalicAstRe  = regexp.MustCompile(`\*([^*\n]+?)\*`)
	mdItalicUndRe  = regexp.MustCompile(`\b_([^_\n]+?)_\b`)
	mdBlankLinesRe = regexp.MustCompile(`\n{3,}`)
)

// markdownToPlainText performs a best-effort conversion of common Markdown
// syntax to plain text suitable for ServiceNow work_notes/comments fields,
// which render as plain text and would otherwise show raw Markdown characters.
func markdownToPlainText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = mdCodeFenceRe.ReplaceAllString(s, "$1")
	s = mdInlineCodeRe.ReplaceAllString(s, "$1")
	s = mdImageRe.ReplaceAllString(s, "$1")
	s = mdLinkRe.ReplaceAllString(s, "$1 ($2)")
	s = mdHeaderRe.ReplaceAllString(s, "")
	s = mdBulletRe.ReplaceAllString(s, "$1• ")
	s = mdHrRe.ReplaceAllString(s, "")
	s = mdBoldRe.ReplaceAllString(s, "$1")
	s = mdItalicAstRe.ReplaceAllString(s, "$1")
	s = mdItalicUndRe.ReplaceAllString(s, "$1")
	s = mdBlankLinesRe.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

func AddServiceNowIncidentComment(ctx *gin.Context, configuration models.TicketConfigurations, ticket models.Ticket) error {
	tableBuilder, err := newServiceNowTableBuilder(configuration, "incident")
	if err != nil {
		slog.Error("Failed to create ServiceNow client", "error", slog.AnyValue(err))
		return fmt.Errorf("failed to create ServiceNow client: %w", err)
	}

	getConfig := &tableapi.TableRequestBuilder2GetRequestConfiguration{
		QueryParameters: &tableapi.TableRequestBuilder2GetQueryParameters{
			Query:  "number=" + ticket.TicketID,
			Fields: []string{"sys_id"},
		},
	}

	collectionResponse, err := tableBuilder.Get(ctx, getConfig)
	if err != nil {
		slog.Error("Failed to query ServiceNow incident", "ticket_id", ticket.TicketID, "error", err)
		return fmt.Errorf("failed to query ServiceNow for incident %s: %w", ticket.TicketID, err)
	}

	results, err := collectionResponse.GetResult()
	if err != nil {
		return fmt.Errorf("failed to get results for incident %s: %w", ticket.TicketID, err)
	}

	if len(results) == 0 {
		return fmt.Errorf("incident not found: %s", ticket.TicketID)
	}

	sysID, err := getRecordStringValue(results[0], "sys_id")
	if err != nil {
		return fmt.Errorf("failed to get sys_id for incident %s: %w", ticket.TicketID, err)
	}

	var commentText string
	if ticket.Comment != "" {
		// ServiceNow work_notes renders as plain text by default, so strip
		// Markdown (LLM outputs are typically Markdown) before posting.
		commentText = markdownToPlainText(ticket.Comment)
	} else {
		commentText = fmt.Sprintf(
			"Found *%s* again at *%s*\n\n*Description:*\n%s",
			ticket.Title,
			time.Now().Format("02 Jan 2006 15:04:05"),
			ticket.Description,
		)
	}

	itemBuilder := tableBuilder.ById(sysID)
	record := tableapi.NewTableRecord()
	_ = record.SetValue("work_notes", commentText)

	putResponse, err := itemBuilder.Put(ctx, record, nil)
	if err != nil {
		slog.Error("error updating ServiceNow incident work_notes",
			"ticket_id", ticket.TicketID,
			"sys_id", sysID,
			"error", err)
		return fmt.Errorf("failed to add work_note to ServiceNow incident %s: %w", ticket.TicketID, err)
	}

	slog.Debug("Work note added to ServiceNow incident", "ticket_id", ticket.TicketID, "sys_id", sysID, "response", putResponse)
	return nil
}

// Update updates fields on a ServiceNow incident
func (s *ServiceNowService) Update(ctx *gin.Context, config models.TicketConfigurations, ticketID string, updateFields models.UpdateFields) error {
	if err := utils.ValidateServiceNowTicketID(ticketID); err != nil {
		return fmt.Errorf("invalid ticket ID: %w", err)
	}

	if updateFields.Severity != "" {
		if err := updateServiceNowIncidentField(ctx, config, ticketID, "urgency", getServiceNowPriority(updateFields.Severity)); err != nil {
			return err
		}
	}

	if updateFields.Assignee != "" {
		if err := updateServiceNowIncidentField(ctx, config, ticketID, "assigned_to", updateFields.Assignee); err != nil {
			return err
		}
	}

	if updateFields.Description != "" {
		if err := updateServiceNowIncidentField(ctx, config, ticketID, "description", updateFields.Description); err != nil {
			return err
		}
	}

	// ServiceNow does not have a native labels concept; labels are ignored for this platform.

	if updateFields.Status != "" {
		if err := s.Transition(ctx, config, ticketID, updateFields.Status); err != nil {
			return err
		}
	}

	return nil
}

// Transition changes the state of a ServiceNow incident
func (s *ServiceNowService) Transition(ctx *gin.Context, config models.TicketConfigurations, ticketID string, status string) error {
	if err := utils.ValidateServiceNowTicketID(ticketID); err != nil {
		return fmt.Errorf("invalid ticket ID: %w", err)
	}

	// Map common status names to ServiceNow states
	state := ""
	switch strings.ToLower(status) {
	case "new", "open":
		state = "1"
	case "in progress", "in_progress", "acknowledged":
		state = "2"
	case "on hold", "on_hold":
		state = "3"
	case "resolved":
		state = "6"
	case "closed":
		state = "7"
	case "canceled", "cancelled":
		state = "8"
	default:
		// If it's a number, use it directly
		state = status
	}

	return updateServiceNowIncidentState(ctx, config, ticketID, state)
}
