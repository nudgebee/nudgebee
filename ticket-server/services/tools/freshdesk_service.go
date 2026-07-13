package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"nudgebee/tickets-server/common"
	"nudgebee/tickets-server/models"
	"nudgebee/tickets-server/services/ticket"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	// freshdeskMaxBody caps a single response read. Freshdesk ticket / conversation
	// payloads are small, but the include=conversations expansion can grow.
	freshdeskMaxBody = 4 << 20 // 4 MiB
	// freshdeskMaxRetries bounds 429 (rate-limit) retries.
	freshdeskMaxRetries = 3
	// freshdeskMaxRetryWait caps how long we honor a Retry-After header so a large
	// value can't stall the caller indefinitely.
	freshdeskMaxRetryWait = 30 * time.Second
	// freshdeskListPageSize is the per_page value for list/group/agent calls (Freshdesk max is 100).
	freshdeskListPageSize = 100
	// freshdeskSearchPageSize is Freshdesk's fixed page size for /search/tickets.
	freshdeskSearchPageSize = 30
)

type FreshdeskService struct{}

var _ ticket.TicketManager = (*FreshdeskService)(nil)

func init() {
	ticket.RegisterTicketManager("freshdesk", &FreshdeskService{})
}

// freshdeskAttachment is a file attachment returned on a ticket or a conversation.
// AttachmentURL is a pre-signed, short-lived S3 URL fetchable without a Freshdesk key.
type freshdeskAttachment struct {
	Name          string `json:"name"`
	ContentType   string `json:"content_type"`
	Size          int64  `json:"size"`
	AttachmentURL string `json:"attachment_url"`
}

// freshdeskTicket is the subset of the Freshdesk REST v2 ticket object we map to
// models.Ticket. Freshdesk status/priority are integers; created/updated are ISO
// 8601 and unmarshal straight into *time.Time.
type freshdeskTicket struct {
	ID              int64                   `json:"id"`
	Subject         string                  `json:"subject"`
	Description     string                  `json:"description"`
	DescriptionText string                  `json:"description_text"`
	Status          int                     `json:"status"`
	Priority        int                     `json:"priority"`
	Tags            []string                `json:"tags"`
	GroupID         *int64                  `json:"group_id"`
	ResponderID     *int64                  `json:"responder_id"`
	RequesterID     *int64                  `json:"requester_id"`
	Email           string                  `json:"email"`
	CreatedAt       *time.Time              `json:"created_at"`
	UpdatedAt       *time.Time              `json:"updated_at"`
	Attachments     []freshdeskAttachment   `json:"attachments"`
	Conversations   []freshdeskConversation `json:"conversations"`
}

// freshdeskConversation is a reply or private note returned by /tickets/{id}/conversations
// or embedded in a ticket fetched with ?include=conversations.
type freshdeskConversation struct {
	ID          int64                 `json:"id"`
	BodyText    string                `json:"body_text"`
	Body        string                `json:"body"`
	FromEmail   string                `json:"from_email"`
	UserID      *int64                `json:"user_id"`
	Private     bool                  `json:"private"`
	CreatedAt   string                `json:"created_at"`
	UpdatedAt   string                `json:"updated_at"`
	Attachments []freshdeskAttachment `json:"attachments"`
}

var freshdeskTicketIDRe = regexp.MustCompile(`^[0-9]+$`)

// validateFreshdeskTicketID rejects non-numeric ids so they can't rewrite the URL path.
func validateFreshdeskTicketID(id string) error {
	if !freshdeskTicketIDRe.MatchString(id) {
		return fmt.Errorf("invalid freshdesk ticket ID %q: must be numeric", id)
	}
	return nil
}

// freshdeskHost normalizes the configured URL to a scheme+host base with no
// trailing slash, e.g. "https://yourco.freshdesk.com". An existing scheme is
// preserved (so http:// test stubs work); a bare host defaults to https://.
func freshdeskHost(rawURL string) string {
	base := strings.TrimSuffix(rawURL, "/")
	if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
		base = "https://" + base
	}
	return base
}

// freshdeskTicketURL is the human-facing agent URL for a ticket.
func freshdeskTicketURL(rawURL, id string) string {
	return freshdeskHost(rawURL) + "/a/tickets/" + id
}

// freshdeskRequest issues an authenticated Freshdesk REST v2 call and returns the
// body bytes and HTTP status. Freshdesk basic auth places the API key in the
// username position and a literal "X" in the password position. HTTP 429 is
// retried up to freshdeskMaxRetries, honoring the Retry-After header.
func freshdeskRequest(ctx context.Context, config models.TicketConfigurations, method, path string, body []byte) ([]byte, int, error) {
	endpoint := freshdeskHost(config.URL) + "/api/v2" + path

	for attempt := 0; ; attempt++ {
		var reader io.Reader
		if body != nil {
			reader = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
		if err != nil {
			return nil, 0, fmt.Errorf("freshdesk: build request: %w", err)
		}
		req.SetBasicAuth(config.Password, "X")
		req.Header.Set("Accept", "application/json")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, err := common.HttpClient().Do(req)
		if err != nil {
			return nil, 0, fmt.Errorf("freshdesk: request failed: %w", err)
		}
		respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, freshdeskMaxBody))
		retryAfter := resp.Header.Get("Retry-After")
		_ = resp.Body.Close()
		if readErr != nil {
			return nil, resp.StatusCode, fmt.Errorf("freshdesk: read body: %w", readErr)
		}

		// Retry on rate limit while attempts remain.
		if resp.StatusCode == http.StatusTooManyRequests && attempt < freshdeskMaxRetries {
			wait := freshdeskRetryWait(retryAfter, attempt)
			timer := time.NewTimer(wait)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return nil, resp.StatusCode, ctx.Err()
			}
			continue
		}

		return respBody, resp.StatusCode, nil
	}
}

// freshdeskRetryWait resolves the wait before a 429 retry: the Retry-After header
// (seconds) when present and sane, otherwise exponential backoff (1s, 2s, 4s…).
// Capped at freshdeskMaxRetryWait.
func freshdeskRetryWait(header string, attempt int) time.Duration {
	wait := time.Duration(1<<attempt) * time.Second
	if header != "" {
		if secs, err := strconv.Atoi(strings.TrimSpace(header)); err == nil && secs > 0 {
			wait = time.Duration(secs) * time.Second
		}
	}
	if wait > freshdeskMaxRetryWait {
		wait = freshdeskMaxRetryWait
	}
	return wait
}

// freshdeskStatusError builds a user-actionable error for a non-2xx response.
func freshdeskStatusError(op string, statusCode int, body []byte) error {
	switch statusCode {
	case http.StatusUnauthorized:
		return fmt.Errorf("freshdesk %s failed: authentication failed (invalid API key)", op)
	case http.StatusForbidden:
		return fmt.Errorf("freshdesk %s failed: API key lacks permission", op)
	case http.StatusNotFound:
		return fmt.Errorf("freshdesk %s failed: not found", op)
	case http.StatusTooManyRequests:
		return fmt.Errorf("freshdesk %s failed: rate limited (429), please retry later", op)
	default:
		return fmt.Errorf("freshdesk %s failed: status %d: %s", op, statusCode, freshdeskTruncate(body))
	}
}

// freshdeskTruncate trims a response body for inclusion in an error message.
func freshdeskTruncate(body []byte) string {
	s := strings.TrimSpace(string(body))
	const max = 500
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}

// --- status / priority mapping (Freshdesk uses integers) ---

// freshdeskStatusToString maps a Freshdesk status int to a readable string.
// Custom statuses (any int outside the built-ins) pass through as their number.
func freshdeskStatusToString(status int) string {
	switch status {
	case 2:
		return "Open"
	case 3:
		return "Pending"
	case 4:
		return "Resolved"
	case 5:
		return "Closed"
	default:
		return strconv.Itoa(status)
	}
}

// freshdeskStatusToInt maps a status name or numeric string back to a Freshdesk
// status int. Unknown numeric strings pass through (custom statuses). Returns
// ok=false only for a non-empty, non-numeric, unrecognized name.
func freshdeskStatusToInt(status string) (int, bool) {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "open":
		return 2, true
	case "pending":
		return 3, true
	case "resolved":
		return 4, true
	case "closed":
		return 5, true
	}
	if n, err := strconv.Atoi(strings.TrimSpace(status)); err == nil {
		return n, true
	}
	return 0, false
}

// freshdeskPriorityToString maps a Freshdesk priority int to a readable string.
func freshdeskPriorityToString(priority int) string {
	switch priority {
	case 1:
		return "Low"
	case 2:
		return "Medium"
	case 3:
		return "High"
	case 4:
		return "Urgent"
	default:
		return strconv.Itoa(priority)
	}
}

// freshdeskPriorityToInt maps a priority name or numeric string to a Freshdesk
// priority int, defaulting to Medium (2) when unrecognized.
func freshdeskPriorityToInt(priority string) int {
	switch strings.ToLower(strings.TrimSpace(priority)) {
	case "low":
		return 1
	case "medium":
		return 2
	case "high":
		return 3
	case "urgent":
		return 4
	}
	if n, err := strconv.Atoi(strings.TrimSpace(priority)); err == nil {
		return n
	}
	return 2
}

// splitFreshdeskTags splits a comma-joined tag string into a trimmed, non-empty slice.
func splitFreshdeskTags(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// freshdeskTicketToModel converts a Freshdesk ticket into a normalized models.Ticket.
// raw is the full source JSON (populated on Get; nil for list rows).
func freshdeskTicketToModel(config models.TicketConfigurations, ft freshdeskTicket, raw map[string]any) models.Ticket {
	idStr := strconv.FormatInt(ft.ID, 10)
	t := models.Ticket{
		ID:          idStr,
		TicketID:    idStr,
		Title:       ft.Subject,
		Description: ft.DescriptionText,
		Status:      freshdeskStatusToString(ft.Status),
		Severity:    freshdeskPriorityToString(ft.Priority),
		Tags:        strings.Join(ft.Tags, ","),
		Platform:    "freshdesk",
		URL:         freshdeskTicketURL(config.URL, idStr),
		CreatedAt:   ft.CreatedAt,
		UpdatedAt:   ft.UpdatedAt,
		Raw:         raw,
	}
	if ft.GroupID != nil {
		t.ProjectKey = strconv.FormatInt(*ft.GroupID, 10)
	}
	if ft.ResponderID != nil {
		t.Assignee = strconv.FormatInt(*ft.ResponderID, 10)
		t.Assignees = []string{t.Assignee}
	}
	if ft.Email != "" {
		t.Reporter = ft.Email
	} else if ft.RequesterID != nil {
		t.Reporter = strconv.FormatInt(*ft.RequesterID, 10)
	}
	if len(ft.Tags) > 0 {
		t.Labels = ft.Tags
	}

	// File attachments: ticket-level, then per-conversation (tagged by source).
	// For list rows both slices are empty (the list endpoint returns neither), so
	// this stays a no-op there.
	for _, a := range ft.Attachments {
		t.Attachments = append(t.Attachments, freshdeskFileAttachment(a, "ticket"))
	}
	for _, c := range ft.Conversations {
		for _, a := range c.Attachments {
			t.Attachments = append(t.Attachments, freshdeskFileAttachment(a, fmt.Sprintf("conversation:%d", c.ID)))
		}
	}

	// Inline images embedded in the description / conversation HTML (many tickets
	// paste screenshots inline rather than attaching files). List rows carry no
	// description or conversations, so this is a no-op there too.
	t.Attachments = append(t.Attachments, freshdeskInlineImages(ft.Description, "inline")...)
	for _, c := range ft.Conversations {
		t.Attachments = append(t.Attachments, freshdeskInlineImages(c.Body, fmt.Sprintf("inline:conversation:%d", c.ID))...)
	}

	return t
}

// freshdeskFileAttachment maps a Freshdesk file attachment to the normalized model.
func freshdeskFileAttachment(a freshdeskAttachment, source string) models.Attachment {
	return models.Attachment{
		Name:        a.Name,
		ContentType: a.ContentType,
		Size:        a.Size,
		URL:         a.AttachmentURL,
		Source:      source,
	}
}

// freshdeskImgSrcRe extracts the src of each <img> tag from HTML. A bounded regex
// is sufficient for Freshdesk's Froala-generated markup (see the gotcha in the plan).
var freshdeskImgSrcRe = regexp.MustCompile(`(?i)<img[^>]+src="([^"]+)"`)

// freshdeskInlineImages returns inline images embedded in a description/conversation
// HTML body as attachments, keeping only Freshdesk-hosted inline URLs so email
// signature/logo images don't leak in. The URL is a token link a human can open;
// the agent can't read images, so these are surfaced for human review only.
func freshdeskInlineImages(htmlContent, source string) []models.Attachment {
	if htmlContent == "" {
		return nil
	}
	var out []models.Attachment
	for _, m := range freshdeskImgSrcRe.FindAllStringSubmatch(htmlContent, -1) {
		// src comes from an HTML attribute, so entity-decode it (e.g. &amp; -> &)
		// or a multi-param query string would be surfaced as a broken URL.
		src := html.UnescapeString(m[1])
		if !strings.Contains(src, "freshdesk") || !strings.Contains(src, "/inline") {
			continue
		}
		out = append(out, models.Attachment{
			Name:        fmt.Sprintf("inline-image-%d", len(out)+1),
			ContentType: "image",
			URL:         src,
			Source:      source,
		})
	}
	return out
}

// Get retrieves a Freshdesk ticket (with its conversation thread) and maps it to
// a normalized ticket, populating Raw with the full source JSON.
func (s *FreshdeskService) Get(ctx *gin.Context, config models.TicketConfigurations, ticketID string) (*models.Ticket, error) {
	if err := validateFreshdeskTicketID(ticketID); err != nil {
		return nil, err
	}

	body, statusCode, err := freshdeskRequest(ctx, config, http.MethodGet, "/tickets/"+ticketID+"?include=conversations", nil)
	if err != nil {
		return nil, err
	}
	if statusCode != http.StatusOK {
		return nil, freshdeskStatusError("get ticket", statusCode, body)
	}

	var ft freshdeskTicket
	if err := json.Unmarshal(body, &ft); err != nil {
		return nil, fmt.Errorf("freshdesk: unmarshal ticket: %w", err)
	}
	var raw map[string]any
	_ = json.Unmarshal(body, &raw)

	t := freshdeskTicketToModel(config, ft, raw)
	return &t, nil
}

// GetComments returns the conversation thread (public replies + private notes).
// Private notes are included (useful for internal RCA) and labeled.
func (s *FreshdeskService) GetComments(ctx *gin.Context, config models.TicketConfigurations, ticketID string) ([]models.Comments, error) {
	if err := validateFreshdeskTicketID(ticketID); err != nil {
		return nil, err
	}

	body, statusCode, err := freshdeskRequest(ctx, config, http.MethodGet, fmt.Sprintf("/tickets/%s/conversations?per_page=%d", ticketID, freshdeskListPageSize), nil)
	if err != nil {
		return nil, err
	}
	if statusCode != http.StatusOK {
		return nil, freshdeskStatusError("get comments", statusCode, body)
	}

	var convs []freshdeskConversation
	if err := json.Unmarshal(body, &convs); err != nil {
		return nil, fmt.Errorf("freshdesk: unmarshal conversations: %w", err)
	}

	comments := make([]models.Comments, 0, len(convs))
	for _, c := range convs {
		author := c.FromEmail
		if author == "" && c.UserID != nil {
			author = strconv.FormatInt(*c.UserID, 10)
		}
		text := c.BodyText
		if text == "" {
			text = c.Body
		}
		if c.Private {
			text = "[private note] " + text
		}
		comments = append(comments, models.Comments{
			Author:  author,
			Comment: text,
			Created: c.CreatedAt,
			Updated: c.UpdatedAt,
		})
	}
	return comments, nil
}

// Create opens a new Freshdesk ticket. Freshdesk requires a requester (email or
// numeric requester_id) plus a subject; priority/status are sent as integers.
func (s *FreshdeskService) Create(ctx *gin.Context, config models.TicketConfigurations, t models.Ticket) (models.Ticket, error) {
	if strings.TrimSpace(t.Title) == "" {
		return t, fmt.Errorf("freshdesk: subject (title) is required to create a ticket")
	}

	body := map[string]any{
		"subject":     t.Title,
		"description": t.Description,
		"priority":    freshdeskPriorityToInt(t.Severity),
	}

	status := 2 // Open
	if t.Status != "" {
		if st, ok := freshdeskStatusToInt(t.Status); ok {
			status = st
		}
	}
	body["status"] = status

	// Requester is mandatory: an email or a numeric requester_id. It arrives via
	// Reporter, or (the only channel an agent has) additional_fields.
	requesterKey, requesterVal, err := resolveFreshdeskRequester(t)
	if err != nil {
		return t, err
	}
	body[requesterKey] = requesterVal

	if t.ProjectKey != "" {
		gid, err := strconv.ParseInt(t.ProjectKey, 10, 64)
		if err != nil {
			return t, fmt.Errorf("freshdesk: project_key (group_id) must be numeric, got %q", t.ProjectKey)
		}
		body["group_id"] = gid
	}
	if t.Assignee != "" {
		rid, err := strconv.ParseInt(t.Assignee, 10, 64)
		if err != nil {
			return t, fmt.Errorf("freshdesk: assignee (responder_id) must be numeric, got %q", t.Assignee)
		}
		body["responder_id"] = rid
	}
	if t.Tags != "" {
		body["tags"] = splitFreshdeskTags(t.Tags)
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return t, fmt.Errorf("freshdesk: marshal create body: %w", err)
	}

	respBody, statusCode, err := freshdeskRequest(ctx, config, http.MethodPost, "/tickets", payload)
	if err != nil {
		return t, err
	}
	if statusCode < 200 || statusCode >= 300 {
		return t, freshdeskStatusError("create ticket", statusCode, respBody)
	}

	var created freshdeskTicket
	if err := json.Unmarshal(respBody, &created); err != nil {
		return t, fmt.Errorf("freshdesk: unmarshal create response: %w", err)
	}

	idStr := strconv.FormatInt(created.ID, 10)
	t.TicketID = idStr
	t.ID = idStr
	t.Status = freshdeskStatusToString(created.Status)
	t.Severity = freshdeskPriorityToString(created.Priority)
	t.Platform = "freshdesk"
	t.URL = freshdeskTicketURL(config.URL, idStr)
	if created.CreatedAt != nil {
		t.CreatedAt = created.CreatedAt
	} else {
		now := time.Now()
		t.CreatedAt = &now
	}
	return t, nil
}

// resolveFreshdeskRequester determines the requester field for a create call.
// Prefers Ticket.Reporter; falls back to additional_fields {email|requester_id}.
// Returns the Freshdesk body key ("email" or "requester_id") and its value.
func resolveFreshdeskRequester(t models.Ticket) (string, any, error) {
	if reporter := strings.TrimSpace(t.Reporter); reporter != "" {
		if strings.Contains(reporter, "@") {
			return "email", reporter, nil
		}
		if id, err := strconv.ParseInt(reporter, 10, 64); err == nil {
			return "requester_id", id, nil
		}
		return "", nil, fmt.Errorf("freshdesk: reporter must be a requester email or numeric requester_id, got %q", reporter)
	}

	if fields, ok := t.AdditionalFields.(map[string]interface{}); ok {
		if email, ok := freshdeskFieldString(fields["email"]); ok && email != "" {
			return "email", email, nil
		}
		if rid, ok := freshdeskFieldInt(fields["requester_id"]); ok {
			return "requester_id", rid, nil
		}
	}

	return "", nil, fmt.Errorf("freshdesk: a requester is required to create a ticket — set reporter (or additional_fields.email / additional_fields.requester_id) to a requester email or numeric requester_id")
}

// freshdeskFieldString reads a string from an additional_fields value.
func freshdeskFieldString(v any) (string, bool) {
	s, ok := v.(string)
	return strings.TrimSpace(s), ok
}

// freshdeskFieldInt reads an int64 from an additional_fields value, which may be a
// JSON number (float64) or a numeric string.
func freshdeskFieldInt(v any) (int64, bool) {
	switch n := v.(type) {
	case float64:
		return int64(n), true
	case string:
		if id, err := strconv.ParseInt(strings.TrimSpace(n), 10, 64); err == nil {
			return id, true
		}
	}
	return 0, false
}

// Update applies field changes to a Freshdesk ticket via PUT /tickets/{id}.
func (s *FreshdeskService) Update(ctx *gin.Context, config models.TicketConfigurations, ticketID string, updateFields models.UpdateFields) error {
	if err := validateFreshdeskTicketID(ticketID); err != nil {
		return fmt.Errorf("invalid ticket ID: %w", err)
	}

	body := map[string]any{}

	if updateFields.Status != "" {
		st, ok := freshdeskStatusToInt(updateFields.Status)
		if !ok {
			return fmt.Errorf("freshdesk: unsupported status %q", updateFields.Status)
		}
		body["status"] = st
	}
	if updateFields.Severity != "" {
		body["priority"] = freshdeskPriorityToInt(updateFields.Severity)
	}
	if assignees := updateFields.AssigneeList(); len(assignees) > 0 {
		if len(assignees) > 1 {
			return fmt.Errorf("only a single assignee (responder) is supported for Freshdesk; %d were provided", len(assignees))
		}
		rid, err := strconv.ParseInt(assignees[0], 10, 64)
		if err != nil {
			return fmt.Errorf("freshdesk: assignee (responder_id) must be numeric, got %q", assignees[0])
		}
		body["responder_id"] = rid
	}
	if updateFields.Description != "" {
		body["description"] = updateFields.Description
	}
	if len(updateFields.Labels) > 0 {
		body["tags"] = updateFields.Labels
	}
	if updateFields.ProjectKey != "" {
		gid, err := strconv.ParseInt(updateFields.ProjectKey, 10, 64)
		if err != nil {
			return fmt.Errorf("freshdesk: project_key (group_id) must be numeric, got %q", updateFields.ProjectKey)
		}
		body["group_id"] = gid
	}

	if len(body) == 0 {
		return nil
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("freshdesk: marshal update body: %w", err)
	}

	respBody, statusCode, err := freshdeskRequest(ctx, config, http.MethodPut, "/tickets/"+ticketID, payload)
	if err != nil {
		return err
	}
	if statusCode < 200 || statusCode >= 300 {
		return freshdeskStatusError("update ticket", statusCode, respBody)
	}
	return nil
}

// Transition changes only the status of a Freshdesk ticket.
func (s *FreshdeskService) Transition(ctx *gin.Context, config models.TicketConfigurations, ticketID string, status string) error {
	if err := validateFreshdeskTicketID(ticketID); err != nil {
		return fmt.Errorf("invalid ticket ID: %w", err)
	}

	st, ok := freshdeskStatusToInt(status)
	if !ok {
		return fmt.Errorf("freshdesk: unsupported status %q", status)
	}

	payload, err := json.Marshal(map[string]any{"status": st})
	if err != nil {
		return fmt.Errorf("freshdesk: marshal transition body: %w", err)
	}

	respBody, statusCode, err := freshdeskRequest(ctx, config, http.MethodPut, "/tickets/"+ticketID, payload)
	if err != nil {
		return err
	}
	if statusCode < 200 || statusCode >= 300 {
		return freshdeskStatusError("transition ticket", statusCode, respBody)
	}
	return nil
}

// AddComment posts an internal (private) note to a Freshdesk ticket. A private
// note matches ServiceNow work_notes semantics — the right default for internal RCA.
func (s *FreshdeskService) AddComment(ctx *gin.Context, config models.TicketConfigurations, t models.Ticket) error {
	if err := validateFreshdeskTicketID(t.TicketID); err != nil {
		return fmt.Errorf("invalid ticket ID: %w", err)
	}

	var commentBody string
	if t.Comment != "" {
		commentBody = t.Comment
	} else {
		commentBody = fmt.Sprintf("Found %s again at %s\n\nDescription:\n%s", t.Title, time.Now().Format("02 Jan 2006 15:04:05"), t.Description)
	}
	// Freshdesk note bodies are HTML. RCA text routinely contains <, >, & (k8s
	// resources like <namespace>/pod, shell operators, comparisons), so escape
	// the content first, then convert newlines to <br> to preserve line breaks.
	htmlBody := strings.ReplaceAll(html.EscapeString(commentBody), "\n", "<br>")

	payload, err := json.Marshal(map[string]any{"body": htmlBody, "private": true})
	if err != nil {
		return fmt.Errorf("freshdesk: marshal note body: %w", err)
	}

	respBody, statusCode, err := freshdeskRequest(ctx, config, http.MethodPost, "/tickets/"+t.TicketID+"/notes", payload)
	if err != nil {
		return err
	}
	if statusCode < 200 || statusCode >= 300 {
		return freshdeskStatusError("add note", statusCode, respBody)
	}
	return nil
}

// List retrieves Freshdesk tickets. When any filter (status/priority/assignee/
// group) is set it uses /search/tickets (filter query); otherwise it uses the
// plain /tickets listing with updated_since / order_by / order_type / page.
func (s *FreshdeskService) List(ctx *gin.Context, config models.TicketConfigurations, params models.ListParams) (*models.ListResult, error) {
	limit := params.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > freshdeskListPageSize {
		limit = freshdeskListPageSize
	}

	if params.Status != "" || params.Priority != "" || params.Assignee != "" || params.ProjectKey != "" {
		return s.listViaSearch(ctx, config, params, limit)
	}
	return s.listViaTickets(ctx, config, params, limit)
}

func (s *FreshdeskService) listViaTickets(ctx *gin.Context, config models.TicketConfigurations, params models.ListParams, limit int) (*models.ListResult, error) {
	q := url.Values{}
	q.Set("per_page", strconv.Itoa(limit))
	page := params.Offset/limit + 1
	q.Set("page", strconv.Itoa(page))

	if params.CreatedAfter != "" {
		t, err := time.Parse(time.RFC3339, params.CreatedAfter)
		if err != nil {
			return nil, fmt.Errorf("invalid created_after filter: must be RFC3339 format (e.g. 2026-01-02T15:04:05Z)")
		}
		q.Set("updated_since", t.UTC().Format(time.RFC3339))
	}

	orderBy := "created_at"
	if params.SortBy == "updated_at" {
		orderBy = "updated_at"
	}
	q.Set("order_by", orderBy)
	orderType := "desc"
	if params.SortOrder == "asc" {
		orderType = "asc"
	}
	q.Set("order_type", orderType)

	body, statusCode, err := freshdeskRequest(ctx, config, http.MethodGet, "/tickets?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	if statusCode != http.StatusOK {
		return nil, freshdeskStatusError("list tickets", statusCode, body)
	}

	var raw []freshdeskTicket
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("freshdesk: unmarshal tickets: %w", err)
	}

	tickets := make([]models.Ticket, 0, len(raw))
	for _, ft := range raw {
		tickets = append(tickets, freshdeskTicketToModel(config, ft, nil))
	}

	return &models.ListResult{
		Tickets: tickets,
		Total:   params.Offset + len(tickets), // /tickets has no total; estimate like other adapters
		Limit:   limit,
		Offset:  params.Offset,
	}, nil
}

func (s *FreshdeskService) listViaSearch(ctx *gin.Context, config models.TicketConfigurations, params models.ListParams, limit int) (*models.ListResult, error) {
	var clauses []string
	if params.Status != "" {
		st, ok := freshdeskStatusToInt(params.Status)
		if !ok {
			return nil, fmt.Errorf("unsupported status filter: %s", params.Status)
		}
		clauses = append(clauses, fmt.Sprintf("status:%d", st))
	}
	if params.Priority != "" {
		clauses = append(clauses, fmt.Sprintf("priority:%d", freshdeskPriorityToInt(params.Priority)))
	}
	if params.ProjectKey != "" {
		gid, err := strconv.ParseInt(params.ProjectKey, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid project_key (group_id) filter: %s", params.ProjectKey)
		}
		clauses = append(clauses, fmt.Sprintf("group_id:%d", gid))
	}
	if params.Assignee != "" {
		aid, err := strconv.ParseInt(params.Assignee, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid assignee filter: must be a numeric agent id, got %s", params.Assignee)
		}
		clauses = append(clauses, fmt.Sprintf("agent_id:%d", aid))
	}
	if params.CreatedAfter != "" {
		t, err := time.Parse(time.RFC3339, params.CreatedAfter)
		if err != nil {
			return nil, fmt.Errorf("invalid created_after filter: must be RFC3339 format (e.g. 2026-01-02T15:04:05Z)")
		}
		clauses = append(clauses, fmt.Sprintf("created_at:>'%s'", t.UTC().Format("2006-01-02")))
	}

	query := strings.Join(clauses, " AND ")
	page := params.Offset/freshdeskSearchPageSize + 1

	q := url.Values{}
	q.Set("query", `"`+query+`"`)
	q.Set("page", strconv.Itoa(page))

	body, statusCode, err := freshdeskRequest(ctx, config, http.MethodGet, "/search/tickets?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	if statusCode != http.StatusOK {
		return nil, freshdeskStatusError("search tickets", statusCode, body)
	}

	var searchResp struct {
		Results []freshdeskTicket `json:"results"`
		Total   int               `json:"total"`
	}
	if err := json.Unmarshal(body, &searchResp); err != nil {
		return nil, fmt.Errorf("freshdesk: unmarshal search results: %w", err)
	}

	// Freshdesk search returns up to 30 results/page. Trim to the requested limit.
	results := searchResp.Results
	if len(results) > limit {
		results = results[:limit]
	}
	tickets := make([]models.Ticket, 0, len(results))
	for _, ft := range results {
		tickets = append(tickets, freshdeskTicketToModel(config, ft, nil))
	}

	return &models.ListResult{
		Tickets: tickets,
		Total:   searchResp.Total,
		Limit:   limit,
		Offset:  params.Offset,
	}, nil
}

// GetCreateMeta returns the create-ticket template for Freshdesk: the basic
// title/description/severity fields plus live Groups (Freshdesk's "project"
// concept) and assignable Agents, and a required requester-email field.
func (s *FreshdeskService) GetCreateMeta(ctx *gin.Context, config models.TicketConfigurations, projectKey string) (interface{}, error) {
	template := Template{
		Name: "Freshdesk Ticket",
		Fields: map[string]FieldInfo{
			"requester": {
				AllowedValues: nil,
				Key:           "requester",
				Name:          "Requester Email",
				Required:      true,
				Type:          "string",
			},
			"group": {
				AllowedValues: freshdeskGroupsToAllowedValues(ctx, config),
				Key:           "group",
				Name:          "Group",
				Required:      false,
				Type:          "select",
			},
			"responder": {
				AllowedValues: fetchFreshdeskAgents(ctx, config),
				Key:           "responder",
				Name:          "Assignee",
				Required:      false,
				Type:          "select",
			},
			// Priority is the severity source (int values 1..4 -> Low..Urgent).
			"priority": {
				AllowedValues: freshdeskPriorityAllowedValues(),
				Key:           "priority",
				Name:          "Priority",
				Required:      false,
				Type:          "select",
				Group:         FieldGroupSeverity,
			},
			"summary": {
				AllowedValues: nil,
				Key:           "summary",
				Name:          "Subject",
				Required:      true,
				Type:          "string",
				Group:         FieldGroupTitle,
			},
			"description": {
				AllowedValues: nil,
				Key:           "description",
				Name:          "Description",
				Required:      false,
				Type:          "string",
				Group:         FieldGroupDescription,
			},
		},
	}

	return map[string]interface{}{"data": []Template{template}}, nil
}

// freshdeskPriorityAllowedValues returns the four static Freshdesk priorities as
// create-meta options. value is the name, which freshdeskPriorityToInt maps back.
func freshdeskPriorityAllowedValues() []interface{} {
	return []interface{}{
		map[string]interface{}{"id": "1", "name": "Low", "value": "Low"},
		map[string]interface{}{"id": "2", "name": "Medium", "value": "Medium"},
		map[string]interface{}{"id": "3", "name": "High", "value": "High"},
		map[string]interface{}{"id": "4", "name": "Urgent", "value": "Urgent"},
	}
}

// freshdeskGroupsToAllowedValues fetches Freshdesk groups as create-meta options
// ({id, name, value: group_id}). Best-effort: on error it logs and returns empty
// so the template still renders.
func freshdeskGroupsToAllowedValues(ctx context.Context, config models.TicketConfigurations) []interface{} {
	projects, err := FetchFreshdeskGroups(ctx, config)
	if err != nil {
		slog.Warn("Freshdesk: failed to fetch groups for create-meta", "error", err)
		return []interface{}{}
	}
	vals := make([]interface{}, 0, len(projects))
	for _, p := range projects {
		vals = append(vals, map[string]interface{}{"id": p.Key, "name": p.Name, "value": p.Key})
	}
	return vals
}

// fetchFreshdeskAgents fetches assignable Freshdesk agents as create-meta options
// ({id, name, value: agent_id}). Best-effort: on error it logs and returns empty.
func fetchFreshdeskAgents(ctx context.Context, config models.TicketConfigurations) []interface{} {
	body, statusCode, err := freshdeskRequest(ctx, config, http.MethodGet, fmt.Sprintf("/agents?per_page=%d", freshdeskListPageSize), nil)
	if err != nil || statusCode != http.StatusOK {
		slog.Warn("Freshdesk: failed to fetch agents for create-meta", "status", statusCode, "error", err)
		return []interface{}{}
	}

	var agents []struct {
		ID      int64 `json:"id"`
		Contact struct {
			Name  string `json:"name"`
			Email string `json:"email"`
		} `json:"contact"`
	}
	if err := json.Unmarshal(body, &agents); err != nil {
		slog.Warn("Freshdesk: failed to unmarshal agents", "error", err)
		return []interface{}{}
	}

	vals := make([]interface{}, 0, len(agents))
	for _, a := range agents {
		idStr := strconv.FormatInt(a.ID, 10)
		name := a.Contact.Name
		if name == "" {
			name = a.Contact.Email
		}
		if name == "" {
			name = idStr
		}
		vals = append(vals, map[string]interface{}{"id": idStr, "name": name, "value": idStr})
	}
	return vals
}

// FetchFreshdeskGroups lists Freshdesk groups (helpdesk teams) as projects, where
// Project.Key is the numeric group_id. Shared by the create-meta builder and the
// api-side configuration validator so both surface the same list.
func FetchFreshdeskGroups(ctx context.Context, config models.TicketConfigurations) ([]models.Project, error) {
	body, statusCode, err := freshdeskRequest(ctx, config, http.MethodGet, fmt.Sprintf("/groups?per_page=%d", freshdeskListPageSize), nil)
	if err != nil {
		return nil, err
	}
	if statusCode != http.StatusOK {
		return nil, freshdeskStatusError("list groups", statusCode, body)
	}

	var groups []struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(body, &groups); err != nil {
		return nil, fmt.Errorf("freshdesk: unmarshal groups: %w", err)
	}

	projects := make([]models.Project, 0, len(groups))
	for _, g := range groups {
		projects = append(projects, models.Project{Name: g.Name, Key: strconv.FormatInt(g.ID, 10)})
	}
	return projects, nil
}

// QuickValidateFreshdesk performs an auth-only probe (list one ticket) for the
// configuration service's lightweight credential check.
func QuickValidateFreshdesk(ctx context.Context, config models.TicketConfigurations) error {
	if config.URL == "" || config.Password == "" {
		return fmt.Errorf("freshdesk url and api key are required")
	}
	body, statusCode, err := freshdeskRequest(ctx, config, http.MethodGet, "/tickets?per_page=1", nil)
	if err != nil {
		return err
	}
	if statusCode != http.StatusOK {
		return freshdeskStatusError("validate credentials", statusCode, body)
	}
	return nil
}
