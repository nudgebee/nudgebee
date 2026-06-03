package clients

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const (
	ZenDutyBaseURL = "https://www.zenduty.com/api"
)

// ZenDutyClient provides methods to interact with the ZenDuty API.
type ZenDutyClient struct {
	apiKey     string
	httpClient *http.Client
}

// CreateZenDutyClient creates a new ZenDuty API client.
func CreateZenDutyClient(apiKey string) *ZenDutyClient {
	return &ZenDutyClient{
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// doRequest performs an HTTP request to the ZenDuty API.
func (c *ZenDutyClient) doRequest(ctx context.Context, method, endpoint string, body interface{}) ([]byte, error) {
	url := ZenDutyBaseURL + endpoint

	var reqBody io.Reader
	if body != nil {
		jsonData, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(jsonData)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Token "+c.apiKey)
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
		return nil, fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

// getJSON performs a GET request and unmarshals the response.
func (c *ZenDutyClient) getJSON(ctx context.Context, endpoint string, result interface{}) error {
	body, err := c.doRequest(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, result)
}

// postJSON performs a POST request and unmarshals the response.
func (c *ZenDutyClient) postJSON(ctx context.Context, endpoint string, reqBody interface{}, result interface{}) error {
	body, err := c.doRequest(ctx, http.MethodPost, endpoint, reqBody)
	if err != nil {
		return err
	}
	if result != nil && len(body) > 0 {
		return json.Unmarshal(body, result)
	}
	return nil
}

// patchJSON performs a PATCH request and unmarshals the response.
func (c *ZenDutyClient) patchJSON(ctx context.Context, endpoint string, reqBody interface{}, result interface{}) error {
	body, err := c.doRequest(ctx, http.MethodPatch, endpoint, reqBody)
	if err != nil {
		return err
	}
	if result != nil && len(body) > 0 {
		return json.Unmarshal(body, result)
	}
	return nil
}

// ZenDuty API Response Types

// ZenDutyTeam represents a team in ZenDuty.
type ZenDutyTeam struct {
	UniqueID    string `json:"unique_id"`
	Name        string `json:"name"`
	Owner       string `json:"owner"`
	Description string `json:"description"`
	CreatedAt   string `json:"creation_date"`
}

// ZenDutyService represents a service in ZenDuty.
type ZenDutyService struct {
	UniqueID           string `json:"unique_id"`
	Name               string `json:"name"`
	Description        string `json:"description"`
	CreatedAt          string `json:"creation_date"`
	TeamID             string `json:"team"`
	EscalationPolicyID string `json:"escalation_policy"`
	AcknowledgeTimeout int    `json:"acknowledgement_timeout"`
	Status             int    `json:"status"`
	AutoResolveTimeout int    `json:"auto_resolve_timeout"`
	UnderMaintenance   bool   `json:"under_maintenance"`
	CollatedAlerts     bool   `json:"collated_alerts"`
}

// ZenDutyUser represents an account member in ZenDuty. The /account/users/
// endpoint returns account-membership objects with the user's identity nested
// under a "user" key — email/username/name live there, NOT at the top level.
// UniqueID is the membership id; User.Username is the user's own unique id.
type ZenDutyUser struct {
	UniqueID string `json:"unique_id"`
	Role     int    `json:"role"`
	User     struct {
		Username  string `json:"username"`
		Email     string `json:"email"`
		FirstName string `json:"first_name"`
		LastName  string `json:"last_name"`
	} `json:"user"`
}

// ZenDutyIncident represents an incident in ZenDuty.
type ZenDutyIncident struct {
	UniqueID         string                `json:"unique_id"`
	Title            string                `json:"title"`
	Summary          string                `json:"summary"`
	Status           int                   `json:"status"`
	Urgency          int                   `json:"urgency"`
	CreationDate     string                `json:"creation_date"`
	AcknowledgedAt   string                `json:"acknowledged_date,omitempty"`
	ResolvedAt       string                `json:"resolved_date,omitempty"`
	ServiceID        string                `json:"service"`
	EscalationPolicy string                `json:"escalation_policy,omitempty"`
	HTMLURL          string                `json:"html_url,omitempty"`
	Number           int                   `json:"incident_number,omitempty"`
	AssignedTo       FlexibleZenDutyAssign `json:"assigned_to,omitempty"`
	Tags             []string              `json:"tags,omitempty"`
}

// FlexibleZenDutyAssign handles ZenDuty's inconsistent assigned_to field
// which can be a string, an array of strings, or an array of user objects.
type FlexibleZenDutyAssign []ZenDutyUserRef

func (f *FlexibleZenDutyAssign) UnmarshalJSON(data []byte) error {
	// Try as array of user objects first (most common response)
	var refs []ZenDutyUserRef
	if err := json.Unmarshal(data, &refs); err == nil {
		*f = refs
		return nil
	}

	// Try as a single string (user UUID)
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		if single != "" {
			*f = []ZenDutyUserRef{{UniqueID: single}}
		}
		return nil
	}

	// Try as array of strings
	var strings []string
	if err := json.Unmarshal(data, &strings); err == nil {
		result := make([]ZenDutyUserRef, len(strings))
		for i, s := range strings {
			result[i] = ZenDutyUserRef{UniqueID: s}
		}
		*f = result
		return nil
	}

	// If nothing works, ignore the field rather than failing the entire unmarshal
	*f = nil
	return nil
}

// ZenDutyUserRef represents a user reference in ZenDuty responses.
type ZenDutyUserRef struct {
	UniqueID  string `json:"unique_id"`
	Username  string `json:"username"`
	Email     string `json:"email"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}

// ZenDutyIncidentNote represents a note/comment on an incident.
type ZenDutyIncidentNote struct {
	UniqueID  string `json:"unique_id"`
	Note      string `json:"note"`
	CreatedAt string `json:"creation_date"`
	User      string `json:"user"`
}

// CreateIncidentRequest represents the request body for creating an incident.
type CreateIncidentRequest struct {
	Title            string   `json:"title"`
	Summary          string   `json:"summary"`
	ServiceID        string   `json:"service"`
	Urgency          int      `json:"urgency,omitempty"`
	EscalationPolicy string   `json:"escalation_policy,omitempty"`
	AssignedTo       []string `json:"assigned_to,omitempty"`
	Tags             []string `json:"tags,omitempty"`
}

// CreateIncidentNoteRequest represents the request body for adding a note.
type CreateIncidentNoteRequest struct {
	Note string `json:"note"`
}

// UpdateIncidentRequest represents the request body for updating an incident.
// Uses PATCH with omitempty so only the fields being changed are sent.
type UpdateIncidentRequest struct {
	Status  int    `json:"status,omitempty"`
	Urgency int    `json:"urgency,omitempty"`
	Summary string `json:"summary,omitempty"`
}

// ZenDuty Status Constants
const (
	ZenDutyStatusTriggered    = 0
	ZenDutyStatusAcknowledged = 1
	ZenDutyStatusResolved     = 2
	ZenDutyStatusSuppressed   = 3
)

// ZenDuty Urgency Constants
const (
	ZenDutyUrgencyLow    = 0
	ZenDutyUrgencyMedium = 1
	ZenDutyUrgencyHigh   = 2
)

// ListTeams fetches all teams from ZenDuty.
func (c *ZenDutyClient) ListTeams(ctx context.Context) ([]ZenDutyTeam, error) {
	var teams []ZenDutyTeam
	if err := c.getJSON(ctx, "/account/teams/", &teams); err != nil {
		return nil, fmt.Errorf("failed to list teams: %w", err)
	}
	return teams, nil
}

// ListServices fetches all services for a team from ZenDuty.
func (c *ZenDutyClient) ListServices(ctx context.Context, teamID string) ([]ZenDutyService, error) {
	var services []ZenDutyService
	endpoint := fmt.Sprintf("/account/teams/%s/services/", teamID)
	if err := c.getJSON(ctx, endpoint, &services); err != nil {
		return nil, fmt.Errorf("failed to list services: %w", err)
	}
	return services, nil
}

// ZenDutyServiceWithTeam represents a service with its team information.
type ZenDutyServiceWithTeam struct {
	ZenDutyService
	TeamName string `json:"team_name"`
}

// ListAllServices fetches services from all teams.
func (c *ZenDutyClient) ListAllServices(ctx context.Context) ([]ZenDutyService, error) {
	teams, err := c.ListTeams(ctx)
	if err != nil {
		return nil, err
	}

	var allServices []ZenDutyService
	for _, team := range teams {
		services, err := c.ListServices(ctx, team.UniqueID)
		if err != nil {
			continue // Skip teams that fail
		}
		allServices = append(allServices, services...)
	}

	return allServices, nil
}

// ListAllServicesWithTeams fetches all services with their team names.
func (c *ZenDutyClient) ListAllServicesWithTeams(ctx context.Context) ([]ZenDutyServiceWithTeam, []ZenDutyTeam, error) {
	teams, err := c.ListTeams(ctx)
	if err != nil {
		return nil, nil, err
	}

	var allServices []ZenDutyServiceWithTeam
	for _, team := range teams {
		services, err := c.ListServices(ctx, team.UniqueID)
		if err != nil {
			// Log the error but continue with other teams
			fmt.Printf("ZenDuty: failed to fetch services for team %s (%s): %v\n", team.Name, team.UniqueID, err)
			continue
		}
		for _, svc := range services {
			allServices = append(allServices, ZenDutyServiceWithTeam{
				ZenDutyService: svc,
				TeamName:       team.Name,
			})
		}
	}

	return allServices, teams, nil
}

// ListUsers fetches all users from ZenDuty.
func (c *ZenDutyClient) ListUsers(ctx context.Context) ([]ZenDutyUser, error) {
	var users []ZenDutyUser
	if err := c.getJSON(ctx, "/account/users/", &users); err != nil {
		return nil, fmt.Errorf("failed to list users: %w", err)
	}
	return users, nil
}

// CreateIncident creates a new incident in ZenDuty.
func (c *ZenDutyClient) CreateIncident(ctx context.Context, req *CreateIncidentRequest) (*ZenDutyIncident, error) {
	var incident ZenDutyIncident
	if err := c.postJSON(ctx, "/incidents/", req, &incident); err != nil {
		return nil, fmt.Errorf("failed to create incident: %w", err)
	}
	return &incident, nil
}

// GetIncident fetches an incident by ID from ZenDuty.
func (c *ZenDutyClient) GetIncident(ctx context.Context, incidentID string) (*ZenDutyIncident, error) {
	var incident ZenDutyIncident
	endpoint := fmt.Sprintf("/incidents/%s/", incidentID)
	if err := c.getJSON(ctx, endpoint, &incident); err != nil {
		return nil, fmt.Errorf("failed to get incident: %w", err)
	}
	return &incident, nil
}

// UpdateIncident partially updates an incident in ZenDuty using PATCH.
// PATCH allows sending only the fields to update, unlike PUT which requires all fields.
func (c *ZenDutyClient) UpdateIncident(ctx context.Context, incidentID string, req *UpdateIncidentRequest) (*ZenDutyIncident, error) {
	var incident ZenDutyIncident
	endpoint := fmt.Sprintf("/incidents/%s/", incidentID)
	if err := c.patchJSON(ctx, endpoint, req, &incident); err != nil {
		return nil, fmt.Errorf("failed to update incident: %w", err)
	}
	return &incident, nil
}

// AddIncidentNote adds a note to an incident in ZenDuty.
func (c *ZenDutyClient) AddIncidentNote(ctx context.Context, incidentID, note string) error {
	req := CreateIncidentNoteRequest{Note: note}
	endpoint := fmt.Sprintf("/incidents/%s/note/", incidentID)
	_, err := c.doRequest(ctx, http.MethodPost, endpoint, req)
	if err != nil {
		return fmt.Errorf("failed to add incident note: %w", err)
	}
	return nil
}

// GetIncidentNotes fetches all notes for an incident from ZenDuty.
func (c *ZenDutyClient) GetIncidentNotes(ctx context.Context, incidentID string) ([]ZenDutyIncidentNote, error) {
	var notes []ZenDutyIncidentNote
	endpoint := fmt.Sprintf("/incidents/%s/notes/", incidentID)
	if err := c.getJSON(ctx, endpoint, &notes); err != nil {
		return nil, fmt.Errorf("failed to get incident notes: %w", err)
	}
	return notes, nil
}

// AcknowledgeIncident acknowledges an incident in ZenDuty.
func (c *ZenDutyClient) AcknowledgeIncident(ctx context.Context, incidentID string) (*ZenDutyIncident, error) {
	req := &UpdateIncidentRequest{Status: ZenDutyStatusAcknowledged}
	return c.UpdateIncident(ctx, incidentID, req)
}

// ResolveIncident resolves an incident in ZenDuty.
func (c *ZenDutyClient) ResolveIncident(ctx context.Context, incidentID string) (*ZenDutyIncident, error) {
	req := &UpdateIncidentRequest{Status: ZenDutyStatusResolved}
	return c.UpdateIncident(ctx, incidentID, req)
}

// ListIncidents fetches incidents from ZenDuty with optional query parameters.
func (c *ZenDutyClient) ListIncidents(ctx context.Context, queryParams map[string]string) ([]ZenDutyIncident, error) {
	endpoint := "/incidents/"
	if len(queryParams) > 0 {
		vals := url.Values{}
		for k, v := range queryParams {
			vals.Set(k, v)
		}
		endpoint += "?" + vals.Encode()
	}
	var incidents []ZenDutyIncident
	if err := c.getJSON(ctx, endpoint, &incidents); err != nil {
		return nil, fmt.Errorf("failed to list incidents: %w", err)
	}
	return incidents, nil
}

// MapUrgencyFromString converts string urgency to ZenDuty numeric urgency.
func MapUrgencyFromString(urgency string) int {
	switch urgency {
	case "high":
		return ZenDutyUrgencyHigh
	case "medium":
		return ZenDutyUrgencyMedium
	case "low":
		return ZenDutyUrgencyLow
	default:
		return ZenDutyUrgencyMedium
	}
}

// MapStatusToString converts ZenDuty numeric status to string.
func MapStatusToString(status int) string {
	switch status {
	case ZenDutyStatusTriggered:
		return "triggered"
	case ZenDutyStatusAcknowledged:
		return "acknowledged"
	case ZenDutyStatusResolved:
		return "resolved"
	case ZenDutyStatusSuppressed:
		return "suppressed"
	default:
		return "triggered"
	}
}
