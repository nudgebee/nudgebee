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
	"sort"
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
	// freshdeskAdminListPageSize is the per_page value for the admin-config list
	// endpoints added for Ticket Forms (/ticket-forms, /ticket_fields). Their max
	// is 50, not the 100 every other list endpoint in this file allows - confirmed
	// live: per_page=100 on /ticket-forms fails validation ("per_page ... Positive
	// Integer less than or equal to 50").
	freshdeskAdminListPageSize = 50
)

type FreshdeskService struct{}

var _ ticket.TicketManager = (*FreshdeskService)(nil)

func init() {
	ticket.RegisterTicketManager("freshdesk", &FreshdeskService{})
}

// freshdeskAttachment is a file attachment returned on a ticket or a conversation.
// AttachmentURL is a pre-signed, short-lived S3 URL fetchable without a Freshdesk key.
type freshdeskAttachment struct {
	ID            int64  `json:"id"`
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

// freshdeskInlineImageRe matches an <img> tag's src attribute. An image pasted
// mid-reply in Freshdesk's editor (as opposed to attached as a file) is embedded
// directly in the conversation's HTML body and never appears in its attachments
// array - confirmed live (body_text and attachments both lose it entirely, only
// body has it). This is the only way to recover it.
var freshdeskInlineImageRe = regexp.MustCompile(`<img\b[^>]*\bsrc="([^"]+)"[^>]*>`)

var (
	freshdeskBrRe       = regexp.MustCompile(`(?i)<br\s*/?>`)
	freshdeskBlockEndRe = regexp.MustCompile(`(?i)</(div|p)>`)
	freshdeskTagRe      = regexp.MustCompile(`<[^>]*>`)
	freshdeskBlankLines = regexp.MustCompile(`\n{3,}`)
)

// freshdeskBodyToTextWithMarkers converts a conversation's HTML body to plain
// text, replacing each inline <img> with an attachmentMarker so the frontend
// (routed through the same [[nb-attachment:<id>]] renderer as GitHub/Jira) can
// show it in its original position instead of always trailing the comment.
// Only called when the body actually contains an inline image (see GetComments)
// - for the common case without one, body_text is used as-is, since this
// regex-based HTML->text conversion is an approximation of Freshdesk's own and
// isn't worth the risk of subtly reformatting every other comment.
func freshdeskBodyToTextWithMarkers(bodyHTML string) (string, []models.CommentAttachment) {
	matches := freshdeskInlineImageRe.FindAllStringSubmatchIndex(bodyHTML, -1)
	if len(matches) == 0 {
		return "", nil
	}

	attachments := make([]models.CommentAttachment, 0, len(matches))
	var sb strings.Builder
	last := 0
	for i, m := range matches {
		sb.WriteString(bodyHTML[last:m[0]])
		id := fmt.Sprintf("fd-inline-%d", i+1)
		sb.WriteString(attachmentMarker(id))
		attachments = append(attachments, models.CommentAttachment{
			ID:   id,
			Name: fmt.Sprintf("inline-image-%d.png", i+1),
			// Freshdesk's inline markup carries no real content type; every
			// observed case is a pasted screenshot (PNG), and the frontend
			// only uses this to decide image-vs-link rendering.
			ContentType: "image/png",
			URL:         html.UnescapeString(bodyHTML[m[2]:m[3]]),
		})
		last = m[1]
	}
	sb.WriteString(bodyHTML[last:])

	text := freshdeskBrRe.ReplaceAllString(sb.String(), "\n")
	text = freshdeskBlockEndRe.ReplaceAllString(text, "\n")
	text = freshdeskTagRe.ReplaceAllString(text, "")
	text = html.UnescapeString(text)
	text = freshdeskBlankLines.ReplaceAllString(text, "\n\n")
	return strings.TrimSpace(text), attachments
}

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
		return fmt.Errorf("freshdesk %s failed: not found: %w", op, ticket.ErrNotFound)
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
// raw is the full source JSON (populated on Get; nil for list rows). agentNames
// resolves ResponderID to a display name when known (see freshdeskAgentNames); a
// nil map or a miss falls back to the raw numeric id, same as before this lookup
// existed.
func freshdeskTicketToModel(config models.TicketConfigurations, ft freshdeskTicket, raw map[string]any, agentNames map[int64]string) models.Ticket {
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
		if name, ok := agentNames[*ft.ResponderID]; ok && name != "" {
			t.Assignee = name
		} else {
			t.Assignee = strconv.FormatInt(*ft.ResponderID, 10)
		}
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

	// Only resolve the assignee's name once the ticket itself is confirmed to
	// exist (the status check above already caught a 404/401) and only when it
	// actually has one - an unassigned ticket skips the extra call entirely.
	agentNames := freshdeskResolveAssigneeNames(ctx, config, []freshdeskTicket{ft})

	t := freshdeskTicketToModel(config, ft, raw, agentNames)
	return &t, nil
}

// GetComments returns the conversation thread (public replies + private notes).
// Private notes are included (useful for internal RCA) and labeled.
func (s *FreshdeskService) GetComments(ctx *gin.Context, config models.TicketConfigurations, ticketID string) ([]models.Comments, error) {
	if err := validateFreshdeskTicketID(ticketID); err != nil {
		return nil, err
	}

	// The conversations endpoint doesn't return a total count, so a short page
	// (fewer than per_page entries) is the only signal that there's no more to
	// fetch. freshdeskMaxConversationPages bounds the loop in case Freshdesk
	// ever returns a full page forever.
	const freshdeskMaxConversationPages = 100
	var convs []freshdeskConversation
	for page := 1; page <= freshdeskMaxConversationPages; page++ {
		body, statusCode, err := freshdeskRequest(ctx, config, http.MethodGet,
			fmt.Sprintf("/tickets/%s/conversations?per_page=%d&page=%d", ticketID, freshdeskListPageSize, page), nil)
		// A failure fetching page 2+ shouldn't hide the conversations that did
		// load (e.g. Freshdesk's undocumented deep-pagination cap on some list
		// endpoints) - surface what we have instead of failing the whole thread.
		if err != nil {
			if page > 1 {
				slog.Error("freshdesk: failed to fetch comments page, returning partial result", "ticketID", ticketID, "page", page, "error", err)
				break
			}
			return nil, err
		}
		if statusCode != http.StatusOK {
			if page > 1 {
				slog.Error("freshdesk: comments page returned non-200, returning partial result", "ticketID", ticketID, "page", page, "statusCode", statusCode)
				break
			}
			return nil, freshdeskStatusError("get comments", statusCode, body)
		}

		var pageConvs []freshdeskConversation
		if err := json.Unmarshal(body, &pageConvs); err != nil {
			return nil, fmt.Errorf("freshdesk: unmarshal conversations: %w", err)
		}
		convs = append(convs, pageConvs...)
		if len(pageConvs) < freshdeskListPageSize {
			break
		}
	}

	comments := make([]models.Comments, 0, len(convs))
	for _, c := range convs {
		author := c.FromEmail
		if author == "" && c.UserID != nil {
			author = strconv.FormatInt(*c.UserID, 10)
		}
		text, attachments := freshdeskBodyToTextWithMarkers(c.Body)
		if text == "" {
			text = c.BodyText
			if text == "" {
				text = c.Body
			}
		}
		if c.Private {
			text = "[private note] " + text
		}
		for _, a := range c.Attachments {
			attachments = append(attachments, models.CommentAttachment{
				ID:          strconv.FormatInt(a.ID, 10),
				Name:        a.Name,
				ContentType: a.ContentType,
				URL:         a.AttachmentURL,
			})
		}
		comments = append(comments, models.Comments{
			Author:      author,
			Comment:     text,
			Created:     c.CreatedAt,
			Updated:     c.UpdatedAt,
			Attachments: attachments,
		})
	}
	return comments, nil
}

// GetAttachment is unimplemented: Freshdesk conversation attachments already carry
// a direct, pre-signed AttachmentURL (see GetComments) - no proxy fetch is needed.
func (s *FreshdeskService) GetAttachment(ctx *gin.Context, config models.TicketConfigurations, ticketID, attachmentID string) (string, io.ReadCloser, error) {
	return "", nil, ticket.ErrNotSupported
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

	// Per-form custom fields (see GetCreateMeta): any additional_fields key not
	// already consumed above is a Freshdesk ticket field keyed by its wire name
	// and goes into custom_fields, except "type" (the native ticket type),
	// which Freshdesk expects as a top-level ticket attribute.
	if additionalFields, ok := t.AdditionalFields.(map[string]interface{}); ok {
		customFields := make(map[string]interface{}, len(additionalFields))
		for key, val := range additionalFields {
			switch key {
			case "requester", "email", "requester_id", "assignee", "priority", "labels", "summary", "description":
				// requester/email/requester_id consumed by resolveFreshdeskRequester
				// above; assignee/priority are extracted into Ticket.Assignee/
				// Ticket.Severity before Create() is even called (see
				// TicketCreatePopupForm.jsx's buildCreateTicketPayload) and handled
				// via responder_id/priority above - dropped here defensively in case
				// a caller (e.g. a workflow payload) still sends one inline; labels/
				// summary/description not applicable to Freshdesk's custom_fields.
			case "group":
				// Freshdesk's Group is the top-level project selector (Ticket.ProjectKey,
				// see above) - not a create-meta field (freshdeskBaseFields). Dropped
				// defensively in case a caller still sends one.
			case "type":
				if s, ok := val.(string); ok && s != "" {
					body["type"] = s
				}
			default:
				customFields[key] = val
			}
		}
		if len(customFields) > 0 {
			body["custom_fields"] = customFields
		}
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
	// Resolve the assignee's name before returning - the caller persists this
	// Ticket to the tickets table (ticket_service.go's CreateIssue), so a raw
	// responder_id here would be stored permanently instead of a name. Mirrors
	// Jira's CreateJiraIssue, which already returns DisplayName rather than an
	// account id. Reads created.ResponderID (what Freshdesk actually set)
	// rather than trusting the input t.Assignee.
	if created.ResponderID != nil {
		agentNames := freshdeskAgentNames(fetchFreshdeskAgentList(ctx, config))
		if name, ok := agentNames[*created.ResponderID]; ok && name != "" {
			t.Assignee = name
		} else {
			t.Assignee = strconv.FormatInt(*created.ResponderID, 10)
		}
	}
	return t, nil
}

// resolveFreshdeskRequester determines the requester field for a create call.
// Prefers Ticket.Reporter (set by callers that bypass the UI, e.g. workflows);
// falls back to additional_fields {email|requester_id}, and then to
// additional_fields.requester - the dynamic "requester" field every Freshdesk
// form carries (see freshdeskBaseFields / freshdeskFieldToInfo), which the UI
// now renders like any other create-meta field rather than a dedicated input.
// Returns the Freshdesk body key ("email" or "requester_id") and its value.
func resolveFreshdeskRequester(t models.Ticket) (string, any, error) {
	if reporter := strings.TrimSpace(t.Reporter); reporter != "" {
		key, val, ok := freshdeskParseRequesterValue(reporter)
		if !ok {
			return "", nil, fmt.Errorf("freshdesk: reporter must be a requester email or numeric requester_id, got %q", reporter)
		}
		return key, val, nil
	}

	if fields, ok := t.AdditionalFields.(map[string]interface{}); ok {
		if email, ok := freshdeskFieldString(fields["email"]); ok && email != "" {
			return "email", email, nil
		}
		if rid, ok := freshdeskFieldInt(fields["requester_id"]); ok {
			return "requester_id", rid, nil
		}
		if requester, ok := freshdeskFieldString(fields["requester"]); ok && requester != "" {
			if key, val, ok := freshdeskParseRequesterValue(requester); ok {
				return key, val, nil
			}
		}
	}

	return "", nil, fmt.Errorf("freshdesk: a requester is required to create a ticket — set reporter (or additional_fields.email / additional_fields.requester_id / additional_fields.requester) to a requester email or numeric requester_id")
}

// freshdeskParseRequesterValue classifies a free-text requester value (from
// Reporter or the dynamic "requester" field) as an email or a numeric
// requester_id, the two forms Freshdesk's create API accepts. Trims its own
// input rather than relying on every caller to have done so already.
func freshdeskParseRequesterValue(v string) (string, any, bool) {
	v = strings.TrimSpace(v)
	if strings.Contains(v, "@") {
		return "email", v, true
	}
	if id, err := strconv.ParseInt(v, 10, 64); err == nil {
		return "requester_id", id, true
	}
	return "", nil, false
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

	agentNames := freshdeskResolveAssigneeNames(ctx, config, raw)
	tickets := make([]models.Ticket, 0, len(raw))
	for _, ft := range raw {
		tickets = append(tickets, freshdeskTicketToModel(config, ft, nil, agentNames))
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
	agentNames := freshdeskResolveAssigneeNames(ctx, config, results)
	tickets := make([]models.Ticket, 0, len(results))
	for _, ft := range results {
		tickets = append(tickets, freshdeskTicketToModel(config, ft, nil, agentNames))
	}

	return &models.ListResult{
		Tickets: tickets,
		Total:   searchResp.Total,
		Limit:   limit,
		Offset:  params.Offset,
	}, nil
}

// GetCreateMeta returns one create-ticket Template per Freshdesk Ticket Form
// (Admin > Ticket Forms), each carrying the base fields plus that form's own
// custom fields - mirroring how Jira ships one Template per issue type
// (sanitizeJiraMeta). Ticket Forms is unavailable on some Freshdesk plans, so
// any failure to fetch the forms list degrades to a single base-fields
// template rather than erroring the whole create flow.
func (s *FreshdeskService) GetCreateMeta(ctx *gin.Context, config models.TicketConfigurations, projectKey string) (interface{}, error) {
	base := freshdeskBaseFields(ctx, config)

	forms, err := fetchFreshdeskTicketForms(ctx, config)
	if err != nil {
		slog.Warn("Freshdesk: failed to fetch ticket forms, falling back to single base template", "error", err)
		return freshdeskSingleTemplateMeta(base), nil
	}
	if len(forms) == 0 {
		return freshdeskSingleTemplateMeta(base), nil
	}

	fieldDefs, err := fetchFreshdeskTicketFieldDefs(ctx, config)
	if err != nil {
		slog.Warn("Freshdesk: failed to fetch ticket field definitions, forms will carry only base fields", "error", err)
	}
	defsByID := make(map[int64]freshdeskTicketFieldDef, len(fieldDefs))
	for _, d := range fieldDefs {
		defsByID[d.ID] = d
	}

	templates := make([]Template, 0, len(forms))
	for _, form := range forms {
		detail, err := fetchFreshdeskTicketFormDetail(ctx, config, form.ID)
		if err != nil {
			slog.Warn("Freshdesk: failed to fetch ticket form detail, skipping form", "formID", form.ID, "title", form.Title, "error", err)
			continue
		}

		fields := make(map[string]FieldInfo, len(base)+len(detail.Fields))
		for k, v := range base {
			fields[k] = v
		}
		for _, ff := range detail.Fields {
			def, ok := defsByID[ff.ID]
			if !ok {
				continue
			}
			if fi, ok := freshdeskFieldToInfo(def, ff.RequiredForAgents); ok {
				fields[fi.Key] = fi
			}
		}

		templates = append(templates, Template{Name: form.Title, Fields: fields})
	}

	if len(templates) == 0 {
		// Every form's detail call failed; degrade rather than return an empty
		// issue-type dropdown.
		return freshdeskSingleTemplateMeta(base), nil
	}
	return map[string]interface{}{"data": templates}, nil
}

// freshdeskSingleTemplateMeta wraps the base field set as the pre-Ticket-Forms
// single-template response (the historical shape, kept as the fallback).
func freshdeskSingleTemplateMeta(base map[string]FieldInfo) map[string]interface{} {
	return map[string]interface{}{"data": []Template{{Name: "Freshdesk Ticket", Fields: base}}}
}

// freshdeskBaseFields returns the fields every Freshdesk create template
// carries regardless of form: requester/assignee/priority/subject/
// description. requester is a fallback default (string, always required) for
// when a form's own fields array can't be fetched; when it can, its
// default_requester entry (every real Freshdesk form includes one - see
// freshdeskFieldToInfo) overwrites this with that form's actual label and
// required_for_agents flag, the same override-by-key mechanism forms use for
// any other field. Rendered generically by TicketFormComponent - no dedicated
// frontend input - its value reaches Create() via additional_fields.requester
// (see resolveFreshdeskRequester).
//
// Group is deliberately NOT one of these fields: Freshdesk's Group is already
// modeled as the top-level "project" selector (TicketFormSection.jsx's
// "Select Freshdesk Group", backed by FetchFreshdeskGroups and sent as
// Ticket.ProjectKey - see Create()'s group_id handling). A second "group"
// FieldInfo here would render as a duplicate picker whose value Create()
// never reads (same dead-field class as the old "requester" entry).
func freshdeskBaseFields(ctx context.Context, config models.TicketConfigurations) map[string]FieldInfo {
	return map[string]FieldInfo{
		"requester": {
			Key:      "requester",
			Name:     "Requester Email",
			Required: true,
			Type:     "string",
		},
		// Key is "assignee", not Freshdesk's own "responder" term: the generic
		// create-ticket payload builder (TicketCreatePopupForm.jsx) special-cases
		// a field keyed "assignee" into Ticket.Assignee (the same convention every
		// other tool's FieldInfo uses, e.g. Jira's), which Create() below already
		// reads to set responder_id. Keying this "responder" instead silently
		// stranded every selection in additional_fields, where Create() dropped it.
		"assignee": {
			AllowedValues: fetchFreshdeskAgents(ctx, config),
			Key:           "assignee",
			Name:          "Assignee",
			Type:          "select",
		},
		// Priority is the severity source (int values 1..4 -> Low..Urgent).
		"priority": {
			AllowedValues: freshdeskPriorityAllowedValues(),
			Key:           "priority",
			Name:          "Priority",
			Type:          "select",
			Group:         FieldGroupSeverity,
		},
		"summary": {
			Key:      "summary",
			Name:     "Subject",
			Required: true,
			Type:     "string",
			Group:    FieldGroupTitle,
		},
		"description": {
			Key:   "description",
			Name:  "Description",
			Type:  "string",
			Group: FieldGroupDescription,
		},
	}
}

// freshdeskTicketForm is one row of GET /ticket-forms.
type freshdeskTicketForm struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
}

// freshdeskTicketFormField is one entry in a form's `fields` array
// (GET /ticket-forms/{id}), referencing an account-wide ticket field by ID.
type freshdeskTicketFormField struct {
	ID                int64 `json:"id"`
	RequiredForAgents bool  `json:"required_for_agents"`
}

// freshdeskTicketFormDetail is the response of GET /ticket-forms/{id}.
type freshdeskTicketFormDetail struct {
	ID     int64                      `json:"id"`
	Title  string                     `json:"title"`
	Fields []freshdeskTicketFormField `json:"fields"`
}

// freshdeskTicketFieldDef is one entry from the account-wide GET /ticket_fields.
// Name is the wire key Freshdesk expects back inside `custom_fields` on create.
type freshdeskTicketFieldDef struct {
	ID      int64       `json:"id"`
	Name    string      `json:"name"`
	Label   string      `json:"label"`
	Type    string      `json:"type"`
	Choices interface{} `json:"choices"`
}

// fetchFreshdeskTicketForms lists the account's configured Ticket Forms
// (Admin > Ticket Forms). Ticket Forms is a plan-tier feature; callers treat
// a fetch error as "unavailable" and fall back to the base template. Paginated
// and bounded like every other list call in this file, with a generous page
// cap since a real account has at most a handful of forms.
func fetchFreshdeskTicketForms(ctx context.Context, config models.TicketConfigurations) ([]freshdeskTicketForm, error) {
	const maxPages = 20
	var all []freshdeskTicketForm
	for page := 1; page <= maxPages; page++ {
		body, statusCode, err := freshdeskRequest(ctx, config, http.MethodGet,
			fmt.Sprintf("/ticket-forms?per_page=%d&page=%d", freshdeskAdminListPageSize, page), nil)
		if err != nil {
			return nil, err
		}
		if statusCode != http.StatusOK {
			return nil, freshdeskStatusError("list ticket forms", statusCode, body)
		}
		var forms []freshdeskTicketForm
		if err := json.Unmarshal(body, &forms); err != nil {
			return nil, fmt.Errorf("freshdesk: unmarshal ticket forms: %w", err)
		}
		all = append(all, forms...)
		if len(forms) < freshdeskAdminListPageSize {
			break
		}
	}
	return all, nil
}

// fetchFreshdeskTicketFormDetail fetches one form's field layout.
func fetchFreshdeskTicketFormDetail(ctx context.Context, config models.TicketConfigurations, formID int64) (*freshdeskTicketFormDetail, error) {
	body, statusCode, err := freshdeskRequest(ctx, config, http.MethodGet, fmt.Sprintf("/ticket-forms/%d", formID), nil)
	if err != nil {
		return nil, err
	}
	if statusCode != http.StatusOK {
		return nil, freshdeskStatusError("get ticket form", statusCode, body)
	}
	var detail freshdeskTicketFormDetail
	if err := json.Unmarshal(body, &detail); err != nil {
		return nil, fmt.Errorf("freshdesk: unmarshal ticket form detail: %w", err)
	}
	return &detail, nil
}

// fetchFreshdeskTicketFieldDefs lists the account-wide ticket field
// definitions (default + custom), joined against each form's field ID list to
// build that form's schema. Paginated and bounded like fetchFreshdeskTicketForms
// - a mature account can have well over 100 custom fields across its history.
func fetchFreshdeskTicketFieldDefs(ctx context.Context, config models.TicketConfigurations) ([]freshdeskTicketFieldDef, error) {
	const maxPages = 20
	var all []freshdeskTicketFieldDef
	for page := 1; page <= maxPages; page++ {
		body, statusCode, err := freshdeskRequest(ctx, config, http.MethodGet,
			fmt.Sprintf("/ticket_fields?per_page=%d&page=%d", freshdeskAdminListPageSize, page), nil)
		if err != nil {
			return nil, err
		}
		if statusCode != http.StatusOK {
			return nil, freshdeskStatusError("list ticket fields", statusCode, body)
		}
		var defs []freshdeskTicketFieldDef
		if err := json.Unmarshal(body, &defs); err != nil {
			return nil, fmt.Errorf("freshdesk: unmarshal ticket fields: %w", err)
		}
		all = append(all, defs...)
		if len(defs) < freshdeskAdminListPageSize {
			break
		}
	}
	return all, nil
}

// freshdeskFieldToInfo converts an account-wide field definition plus its
// per-form required flag into a create-meta FieldInfo. Returns ok=false for
// field types outside the frontend renderer's vocabulary (nested/dependent
// dropdowns, file uploads) - dropped rather than emitted, mirroring Jira's
// create-meta sanitizer (sanitizeJiraMeta).
func freshdeskFieldToInfo(def freshdeskTicketFieldDef, requiredForAgents bool) (FieldInfo, bool) {
	// Freshdesk's native ticket type (e.g. Question/Incident/Problem) is a
	// top-level `type` string on the ticket, not a custom field - force the key
	// so Create() can route it there instead of into custom_fields.
	if def.Type == "default_ticket_type" {
		return FieldInfo{
			AllowedValues: freshdeskChoicesToAllowedValues(def.Choices),
			Key:           "type",
			Name:          firstNonEmpty(def.Label, "Type"),
			Required:      requiredForAgents,
			Type:          "select",
		}, true
	}
	// Every Freshdesk form includes its own requester field - force the key to
	// overwrite freshdeskBaseFields' fallback entry with this form's real
	// required_for_agents flag. Name is fixed (not def.Label): Freshdesk's own
	// label ("Search a requester") describes a live contact-search widget in
	// their UI, which TicketFormComponent's "string" type doesn't replicate -
	// it's a plain text box that takes an email or numeric requester_id (see
	// resolveFreshdeskRequester), so the label says that instead.
	if def.Type == "default_requester" {
		return FieldInfo{
			Key:      "requester",
			Name:     "Requester Email",
			Required: requiredForAgents,
			Type:     "string",
		}, true
	}

	uiType, supported := freshdeskFieldTypeToUIType(def.Type)
	if !supported || def.Name == "" {
		return FieldInfo{}, false
	}

	fi := FieldInfo{
		Key:      def.Name,
		Name:     firstNonEmpty(def.Label, def.Name),
		Required: requiredForAgents,
		Type:     uiType,
	}
	switch def.Type {
	case "custom_checkbox":
		// Freshdesk ships no choices for a checkbox field; synthesize a
		// boolean select since the frontend renderer has no checkbox type.
		fi.AllowedValues = []interface{}{
			map[string]interface{}{"id": "true", "name": "Yes", "value": true},
			map[string]interface{}{"id": "false", "name": "No", "value": false},
		}
	case "custom_dropdown":
		fi.AllowedValues = freshdeskChoicesToAllowedValues(def.Choices)
	}
	return fi, true
}

// EncodeFreshdeskAdditionalFields coerces "number" fields to real JSON numbers
// and "select" fields' chosen id to its AllowedValues entry's value, mirroring
// Jira's EncodeJiraAdditionalFields (same ticket_service.go pre-create hook).
func EncodeFreshdeskAdditionalFields(fields map[string]FieldInfo, additionalFields map[string]interface{}) map[string]interface{} {
	if additionalFields == nil {
		return nil
	}
	out := make(map[string]interface{}, len(additionalFields))
	for key, value := range additionalFields {
		if value != nil {
			if fieldInfo, known := fields[key]; known {
				switch fieldInfo.Type {
				case "number":
					value = freshdeskNumber(value)
				case "select":
					value = freshdeskSelectValue(fieldInfo.AllowedValues, value)
				}
			}
		}
		out[key] = value
	}
	return out
}

// freshdeskSelectValue resolves a select field's chosen id to its
// AllowedValues entry's value, falling back to the original value on no match.
func freshdeskSelectValue(allowedValues []interface{}, selected interface{}) interface{} {
	selectedID, ok := selected.(string)
	if !ok {
		return selected
	}
	for _, av := range allowedValues {
		entry, ok := av.(map[string]interface{})
		if !ok {
			continue
		}
		if id, ok := entry["id"].(string); ok && id == selectedID {
			return entry["value"]
		}
	}
	return selected
}

// freshdeskNumber coerces a text-input string to a JSON number; an
// already-numeric value passes through unchanged. Falls back to the original
// value when it doesn't parse, so Freshdesk's own validation error stays the
// signal rather than this silently dropping or mangling the data.
func freshdeskNumber(v interface{}) interface{} {
	switch n := v.(type) {
	case float64, int, int64:
		return v
	case string:
		if f, err := strconv.ParseFloat(strings.TrimSpace(n), 64); err == nil {
			return f
		}
	}
	return v
}

// freshdeskFieldTypeToUIType maps a Freshdesk ticket_fields `type` to the
// frontend's known FieldInfo.Type vocabulary (TicketFormComponent.jsx's
// renderFields switch: string/text/number/select/array/multiselect/datetime/
// datepicker). Unsupported types return ok=false.
func freshdeskFieldTypeToUIType(fdType string) (string, bool) {
	switch fdType {
	case "custom_text", "custom_phone_number", "custom_url":
		return "string", true
	case "custom_paragraph":
		return "text", true
	case "custom_number", "custom_decimal":
		return "number", true
	case "custom_dropdown", "custom_checkbox":
		return "select", true
	case "custom_date":
		return "datepicker", true
	default:
		return "", false
	}
}

// freshdeskChoicesToAllowedValues normalizes a dropdown field's `choices`
// (a plain string array, confirmed live; or an object map, shape unconfirmed
// for a real custom_dropdown - every map-shaped `choices` actually observed
// belongs to a different field type, key=label/value=id, the reverse of the
// usual assumption, so the map branch deliberately keeps using the key).
func freshdeskChoicesToAllowedValues(choices interface{}) []interface{} {
	switch c := choices.(type) {
	case []interface{}:
		vals := make([]interface{}, 0, len(c))
		for _, v := range c {
			s, _ := v.(string)
			vals = append(vals, map[string]interface{}{"id": s, "name": s, "value": s})
		}
		return vals
	case map[string]interface{}:
		vals := make([]interface{}, 0, len(c))
		for k := range c {
			vals = append(vals, map[string]interface{}{"id": k, "name": k, "value": k})
		}
		// A map has no stable JSON key order; sort for deterministic rendering.
		sort.Slice(vals, func(i, j int) bool {
			return vals[i].(map[string]interface{})["id"].(string) < vals[j].(map[string]interface{})["id"].(string)
		})
		return vals
	default:
		return nil
	}
}

// firstNonEmpty returns a if non-empty, else b.
func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
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

// freshdeskAgent is a parsed /agents row, shared by the create-meta option list
// (fetchFreshdeskAgents, order-sensitive) and the Assignee-name resolver
// (freshdeskAgentNames) so both are built from a single HTTP call.
type freshdeskAgent struct {
	ID   int64
	Name string
}

// fetchFreshdeskAgentList fetches assignable Freshdesk agents in API order
// (display name falls back to email, then the stringified id, when Freshdesk's
// contact name is empty). Best-effort: on error it logs and returns nil.
func fetchFreshdeskAgentList(ctx context.Context, config models.TicketConfigurations) []freshdeskAgent {
	body, statusCode, err := freshdeskRequest(ctx, config, http.MethodGet, fmt.Sprintf("/agents?per_page=%d", freshdeskListPageSize), nil)
	if err != nil || statusCode != http.StatusOK {
		slog.Warn("Freshdesk: failed to fetch agents", "status", statusCode, "error", err)
		return nil
	}

	var raw []struct {
		ID      int64 `json:"id"`
		Contact struct {
			Name  string `json:"name"`
			Email string `json:"email"`
		} `json:"contact"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		slog.Warn("Freshdesk: failed to unmarshal agents", "error", err)
		return nil
	}

	agents := make([]freshdeskAgent, 0, len(raw))
	for _, a := range raw {
		name := a.Contact.Name
		if name == "" {
			name = a.Contact.Email
		}
		if name == "" {
			name = strconv.FormatInt(a.ID, 10)
		}
		agents = append(agents, freshdeskAgent{ID: a.ID, Name: name})
	}
	return agents
}

// fetchFreshdeskAgents fetches assignable Freshdesk agents as create-meta options
// ({id, name, value: agent_id}), in API order.
func fetchFreshdeskAgents(ctx context.Context, config models.TicketConfigurations) []interface{} {
	agents := fetchFreshdeskAgentList(ctx, config)
	vals := make([]interface{}, 0, len(agents))
	for _, a := range agents {
		idStr := strconv.FormatInt(a.ID, 10)
		vals = append(vals, map[string]interface{}{"id": idStr, "name": a.Name, "value": idStr})
	}
	return vals
}

// freshdeskAgentNames builds an ID -> display name lookup from an agent list, used
// to resolve Ticket.Assignee on Get/List: Freshdesk's /tickets, /tickets/{id} and
// /search/tickets responses carry only the numeric responder_id, never a name
// (unlike Jira, whose issue payload embeds the assignee's display name directly).
func freshdeskAgentNames(agents []freshdeskAgent) map[int64]string {
	names := make(map[int64]string, len(agents))
	for _, a := range agents {
		names[a.ID] = a.Name
	}
	return names
}

// freshdeskResolveAssigneeNames fetches an agent-name lookup only if at least
// one of the given tickets actually has a responder_id, so an all-unassigned
// page (or single ticket) skips the extra call entirely.
func freshdeskResolveAssigneeNames(ctx context.Context, config models.TicketConfigurations, tickets []freshdeskTicket) map[int64]string {
	needsLookup := false
	for _, ft := range tickets {
		if ft.ResponderID != nil {
			needsLookup = true
			break
		}
	}
	if !needsLookup {
		return nil
	}
	return freshdeskAgentNames(fetchFreshdeskAgentList(ctx, config))
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
