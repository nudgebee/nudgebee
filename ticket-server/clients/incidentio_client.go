package clients

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// incident.io's public API is versioned PER RESOURCE, not globally: incidents,
// incident_updates and users are V2, while severities, incident_statuses and
// incident_types are still V1. Requesting the wrong version returns 404 (not
// 401), so a mistake here surfaces as a confusing "not found" rather than an
// auth error. These prefixes were verified against the live API — do not
// "normalize" them to a single version.
const (
	incidentIOPathIncidents       = "/v2/incidents"
	incidentIOPathIncidentUpdates = "/v2/incident_updates"
	incidentIOPathUsers           = "/v2/users"
	incidentIOPathSeverities      = "/v1/severities"
	incidentIOPathStatuses        = "/v1/incident_statuses"
	incidentIOPathIncidentTypes   = "/v1/incident_types"
)

const (
	// DefaultIncidentIOAPIEndpoint is incident.io's public API host. It is
	// configurable per-integration so EU-resident tenants (and tests) can point
	// elsewhere without a code change.
	DefaultIncidentIOAPIEndpoint = "https://api.incident.io"

	// incidentIOHTTPTimeout is the timeout for the incident.io HTTP client.
	incidentIOHTTPTimeout = 30 * time.Second
)

// Incident status categories. incident.io lets each organisation name its own
// statuses ("Investigating", "Mitigated", "Fixing"…), but every status belongs
// to one of these fixed categories. Anything portable — our normalized ticket
// status, "is this resolved?", "move it out of triage" — must key off the
// category, never the org-specific name.
const (
	IncidentIOCategoryTriage       = "triage"
	IncidentIOCategoryActive       = "active"
	IncidentIOCategoryPostIncident = "post_incident"
	IncidentIOCategoryClosed       = "closed"
	IncidentIOCategoryDeclined     = "declined"
	IncidentIOCategoryCanceled     = "canceled"
	IncidentIOCategoryMerged       = "merged"
)

// NormalizeIncidentIOEndpoint turns a user-entered host into a usable base URL.
// Passing the configured URL through here is what causes a typo in the form
// (e.g. "api.incident.io5") to surface as a connection error at validation time
// rather than being silently replaced by the default endpoint.
func NormalizeIncidentIOEndpoint(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return DefaultIncidentIOAPIEndpoint
	}
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		rawURL = "https://" + rawURL
	}
	return strings.TrimRight(rawURL, "/")
}

// IncidentIOClient provides methods to interact with the incident.io V2 API.
type IncidentIOClient struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// CreateIncidentIOClient creates a new incident.io API client bound to the
// configured endpoint.
func CreateIncidentIOClient(apiKey, rawURL string) *IncidentIOClient {
	return &IncidentIOClient{
		apiKey:     apiKey,
		baseURL:    NormalizeIncidentIOEndpoint(rawURL),
		httpClient: &http.Client{Timeout: incidentIOHTTPTimeout},
	}
}

// doRequest performs an HTTP request against the incident.io API.
func (c *IncidentIOClient) doRequest(ctx context.Context, method, endpoint string, body interface{}) ([]byte, error) {
	var reqBody io.Reader
	if body != nil {
		jsonData, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(jsonData)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+endpoint, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("incident.io request failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

func (c *IncidentIOClient) getJSON(ctx context.Context, endpoint string, result interface{}) error {
	body, err := c.doRequest(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, result)
}

func (c *IncidentIOClient) postJSON(ctx context.Context, endpoint string, reqBody, result interface{}) error {
	body, err := c.doRequest(ctx, http.MethodPost, endpoint, reqBody)
	if err != nil {
		return err
	}
	if result != nil && len(body) > 0 {
		return json.Unmarshal(body, result)
	}
	return nil
}

// incident.io API types. Only the fields NudgeBee maps are declared; the full
// upstream payload still reaches workflow consumers via Ticket.Raw.

// IncidentIOStatus is an incident status. Name is org-specific; Category is the
// fixed taxonomy (see the IncidentIOCategory* constants).
type IncidentIOStatus struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Category    string `json:"category"`
	Description string `json:"description,omitempty"`
	Rank        int    `json:"rank,omitempty"`
}

// IncidentIOSeverity is a configurable severity level. Rank orders them, with
// higher meaning more severe.
type IncidentIOSeverity struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Rank        int    `json:"rank,omitempty"`
}

// IncidentIOIncidentType groups incidents. This is the closest analogue to a
// PagerDuty/ZenDuty "service", so NudgeBee maps ProjectKey onto it.
type IncidentIOIncidentType struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	IsDefault   bool   `json:"is_default,omitempty"`
}

// IncidentIOUser is a user reference.
type IncidentIOUser struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email,omitempty"`
	Role  string `json:"role,omitempty"`
}

// IncidentIORole is an incident role (Lead, Communications, …).
type IncidentIORole struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	RoleType  string `json:"role_type,omitempty"`
	Shortform string `json:"shortform,omitempty"`
}

// IncidentIORoleAssignment binds a user to a role on an incident. incident.io
// has no single "assignee" — the lead role is the closest equivalent.
type IncidentIORoleAssignment struct {
	Assignee *IncidentIOUser `json:"assignee,omitempty"`
	Role     IncidentIORole  `json:"role"`
}

// IncidentIOIncident is an incident.io incident.
type IncidentIOIncident struct {
	ID                      string                     `json:"id"`
	Name                    string                     `json:"name"`
	Summary                 string                     `json:"summary,omitempty"`
	Reference               string                     `json:"reference,omitempty"`
	Permalink               string                     `json:"permalink,omitempty"`
	CreatedAt               string                     `json:"created_at,omitempty"`
	UpdatedAt               string                     `json:"updated_at,omitempty"`
	Mode                    string                     `json:"mode,omitempty"`
	Visibility              string                     `json:"visibility,omitempty"`
	IncidentStatus          *IncidentIOStatus          `json:"incident_status,omitempty"`
	Severity                *IncidentIOSeverity        `json:"severity,omitempty"`
	IncidentType            *IncidentIOIncidentType    `json:"incident_type,omitempty"`
	IncidentRoleAssignments []IncidentIORoleAssignment `json:"incident_role_assignments,omitempty"`
}

// IncidentIOIncidentUpdate is a timeline update on an incident. This is the
// read side of what NudgeBee surfaces as ticket comments.
type IncidentIOIncidentUpdate struct {
	ID          string              `json:"id"`
	IncidentID  string              `json:"incident_id"`
	Message     string              `json:"message,omitempty"`
	CreatedAt   string              `json:"created_at,omitempty"`
	NewStatus   *IncidentIOStatus   `json:"new_incident_status,omitempty"`
	NewSeverity *IncidentIOSeverity `json:"new_severity,omitempty"`
	Updater     *struct {
		User   *IncidentIOUser `json:"user,omitempty"`
		APIKey *struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"api_key,omitempty"`
	} `json:"updater,omitempty"`
}

// IncidentIOPaginationMeta is the cursor block returned by list endpoints.
type IncidentIOPaginationMeta struct {
	After            string `json:"after,omitempty"`
	PageSize         int    `json:"page_size,omitempty"`
	TotalRecordCount int    `json:"total_record_count,omitempty"`
}

// IncidentIOEditPayload is the mutable subset of an incident accepted by the
// edit endpoint. Every field is omitempty: incident.io treats an omitted field
// as "leave unchanged", so sending a zero value would blank it.
type IncidentIOEditPayload struct {
	Name             string `json:"name,omitempty"`
	Summary          string `json:"summary,omitempty"`
	SeverityID       string `json:"severity_id,omitempty"`
	IncidentStatusID string `json:"incident_status_id,omitempty"`
}

// CreateIncidentIORequest is the body for POST /v2/incidents.
type CreateIncidentIORequest struct {
	IdempotencyKey   string `json:"idempotency_key"`
	Visibility       string `json:"visibility"`
	Name             string `json:"name"`
	Summary          string `json:"summary,omitempty"`
	SeverityID       string `json:"severity_id,omitempty"`
	IncidentStatusID string `json:"incident_status_id,omitempty"`
	IncidentTypeID   string `json:"incident_type_id,omitempty"`
	Mode             string `json:"mode,omitempty"`
}

// GetIncident fetches a single incident by ULID.
func (c *IncidentIOClient) GetIncident(ctx context.Context, incidentID string) (*IncidentIOIncident, error) {
	var resp struct {
		Incident IncidentIOIncident `json:"incident"`
	}
	if err := c.getJSON(ctx, incidentIOPathIncidents+"/"+incidentID, &resp); err != nil {
		return nil, fmt.Errorf("failed to get incident.io incident: %w", err)
	}
	return &resp.Incident, nil
}

// ListIncidents fetches incidents with optional query parameters.
func (c *IncidentIOClient) ListIncidents(ctx context.Context, queryParams map[string]string) ([]IncidentIOIncident, IncidentIOPaginationMeta, error) {
	endpoint := incidentIOPathIncidents
	if len(queryParams) > 0 {
		vals := url.Values{}
		for k, v := range queryParams {
			vals.Set(k, v)
		}
		endpoint += "?" + vals.Encode()
	}

	var resp struct {
		Incidents      []IncidentIOIncident     `json:"incidents"`
		PaginationMeta IncidentIOPaginationMeta `json:"pagination_meta"`
	}
	if err := c.getJSON(ctx, endpoint, &resp); err != nil {
		return nil, IncidentIOPaginationMeta{}, fmt.Errorf("failed to list incident.io incidents: %w", err)
	}
	return resp.Incidents, resp.PaginationMeta, nil
}

// CreateIncident creates a new incident.
func (c *IncidentIOClient) CreateIncident(ctx context.Context, req *CreateIncidentIORequest) (*IncidentIOIncident, error) {
	var resp struct {
		Incident IncidentIOIncident `json:"incident"`
	}
	if err := c.postJSON(ctx, incidentIOPathIncidents, req, &resp); err != nil {
		return nil, fmt.Errorf("failed to create incident.io incident: %w", err)
	}
	return &resp.Incident, nil
}

// EditIncident updates the mutable fields of an incident.
//
// incident.io models edits as an action sub-resource
// (POST /v2/incidents/{id}/actions/edit) rather than a PATCH on the incident,
// and the payload is wrapped in an "incident" object alongside the
// notify_incident_channel flag.
func (c *IncidentIOClient) EditIncident(ctx context.Context, incidentID string, payload IncidentIOEditPayload, notifyChannel bool) (*IncidentIOIncident, error) {
	body := struct {
		Incident              IncidentIOEditPayload `json:"incident"`
		NotifyIncidentChannel bool                  `json:"notify_incident_channel"`
	}{Incident: payload, NotifyIncidentChannel: notifyChannel}

	var resp struct {
		Incident IncidentIOIncident `json:"incident"`
	}
	if err := c.postJSON(ctx, incidentIOPathIncidents+"/"+incidentID+"/actions/edit", body, &resp); err != nil {
		return nil, fmt.Errorf("failed to edit incident.io incident: %w", err)
	}
	return &resp.Incident, nil
}

// ListIncidentUpdates fetches the timeline updates for one incident.
func (c *IncidentIOClient) ListIncidentUpdates(ctx context.Context, incidentID string) ([]IncidentIOIncidentUpdate, error) {
	vals := url.Values{}
	vals.Set("incident_id", incidentID)

	var resp struct {
		IncidentUpdates []IncidentIOIncidentUpdate `json:"incident_updates"`
	}
	if err := c.getJSON(ctx, incidentIOPathIncidentUpdates+"?"+vals.Encode(), &resp); err != nil {
		return nil, fmt.Errorf("failed to list incident.io incident updates: %w", err)
	}
	return resp.IncidentUpdates, nil
}

// ListSeverities fetches the organisation's configured severities.
func (c *IncidentIOClient) ListSeverities(ctx context.Context) ([]IncidentIOSeverity, error) {
	var resp struct {
		Severities []IncidentIOSeverity `json:"severities"`
	}
	if err := c.getJSON(ctx, incidentIOPathSeverities, &resp); err != nil {
		return nil, fmt.Errorf("failed to list incident.io severities: %w", err)
	}
	return resp.Severities, nil
}

// ListIncidentStatuses fetches the organisation's configured incident statuses.
func (c *IncidentIOClient) ListIncidentStatuses(ctx context.Context) ([]IncidentIOStatus, error) {
	var resp struct {
		IncidentStatuses []IncidentIOStatus `json:"incident_statuses"`
	}
	if err := c.getJSON(ctx, incidentIOPathStatuses, &resp); err != nil {
		return nil, fmt.Errorf("failed to list incident.io incident statuses: %w", err)
	}
	return resp.IncidentStatuses, nil
}

// ListIncidentTypes fetches the organisation's configured incident types.
func (c *IncidentIOClient) ListIncidentTypes(ctx context.Context) ([]IncidentIOIncidentType, error) {
	var resp struct {
		IncidentTypes []IncidentIOIncidentType `json:"incident_types"`
	}
	if err := c.getJSON(ctx, incidentIOPathIncidentTypes, &resp); err != nil {
		return nil, fmt.Errorf("failed to list incident.io incident types: %w", err)
	}
	return resp.IncidentTypes, nil
}

// ListUsers fetches the organisation's users.
func (c *IncidentIOClient) ListUsers(ctx context.Context) ([]IncidentIOUser, error) {
	var resp struct {
		Users []IncidentIOUser `json:"users"`
	}
	if err := c.getJSON(ctx, incidentIOPathUsers, &resp); err != nil {
		return nil, fmt.Errorf("failed to list incident.io users: %w", err)
	}
	return resp.Users, nil
}

// FindSeverityByName resolves a severity name (case-insensitive) to its ID.
// Returns "" when there is no match, which callers treat as "let incident.io
// apply its own default" rather than an error.
func FindSeverityByName(severities []IncidentIOSeverity, name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	for _, s := range severities {
		if strings.EqualFold(s.Name, name) {
			return s.ID
		}
	}
	return ""
}

// FindStatusByCategory returns the lowest-ranked status in the given category.
// Statuses are org-defined, so this is how NudgeBee expresses portable
// intents ("acknowledge" = move to active, "resolve" = move to closed).
func FindStatusByCategory(statuses []IncidentIOStatus, category string) *IncidentIOStatus {
	var best *IncidentIOStatus
	for i := range statuses {
		if !strings.EqualFold(statuses[i].Category, category) {
			continue
		}
		if best == nil || statuses[i].Rank < best.Rank {
			best = &statuses[i]
		}
	}
	return best
}

// NormalizeIncidentIOStatusCategory maps the status vocabulary NudgeBee uses
// across providers onto an incident.io status category. Returns "" when the
// token has no incident.io equivalent.
func NormalizeIncidentIOStatusCategory(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "triggered", "open", "new", "triage":
		return IncidentIOCategoryTriage
	case "acknowledged", "ack", "active", "investigating", "in_progress":
		return IncidentIOCategoryActive
	case "post_incident", "postincident", "learning", "monitoring":
		return IncidentIOCategoryPostIncident
	case "resolved", "closed", "done", "complete", "completed":
		return IncidentIOCategoryClosed
	case "declined":
		return IncidentIOCategoryDeclined
	case "canceled", "cancelled":
		return IncidentIOCategoryCanceled
	default:
		return ""
	}
}
