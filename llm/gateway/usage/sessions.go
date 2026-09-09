package usage

import (
	"context"
	"fmt"
	"strings"
	"time"

	"nudgebee/llm-gateway/common"
	"nudgebee/llm-gateway/metering"
)

// SessionRow is one conversation/session, aggregated from its requests — the row of
// the Sessions tab. Costs/tokens sum the session's requests; models/providers list
// the distinct ones it touched (a session can span models, e.g. a main + a helper).
type SessionRow struct {
	SessionID         string    `json:"session_id"`
	SessionSource     string    `json:"session_source"` // header | metadata.session_id | metadata.user_id | inferred
	User              string    `json:"user"`           // resolved display name (falls back to user id)
	UserID            string    `json:"user_id"`
	Requests          int64     `json:"requests"`
	InputTokens       int64     `json:"input_tokens"`
	OutputTokens      int64     `json:"output_tokens"`
	CachedInputTokens int64     `json:"cached_input_tokens"`
	CostUsd           float64   `json:"cost_usd"`
	Models            []string  `json:"models"`
	Providers         []string  `json:"providers"`
	FirstSeen         time.Time `json:"first_seen"`
	LastSeen          time.Time `json:"last_seen"`
	// FirstMessage is a short preview of the session's opening user message, extracted
	// from the earliest captured request body. Empty unless body-capture is on AND the
	// viewer may see it (their own session, or a tenant admin) — same policy as the
	// body view. PHI-gated, so never shown for another user's session to a non-admin.
	FirstMessage string `json:"first_message"`
}

// SessionList is a page of sessions plus the unpaged total (distinct sessions).
type SessionList struct {
	Rows   []SessionRow `json:"rows"`
	Total  int64        `json:"total"`
	Limit  int          `json:"limit"`
	Offset int          `json:"offset"`
}

// ListSessionsRequest is the query for the Sessions tab (newest-active first).
type ListSessionsRequest struct {
	TenantID  string
	StartDate time.Time
	EndDate   time.Time
	UserID    string // optional; scope to one user
	Search    string // optional; session_id contains (case-insensitive)
	Model     string // optional; sessions that touched this model (full session totals kept)
	// Sort column + direction for the page. Sort is an allowlist key (last_seen |
	// first_seen | cost | requests | tokens); anything else falls back to last_seen.
	// Order is "asc" | "desc"; empty defaults to desc (newest / largest first).
	Sort  string
	Order string
	// Body-view policy for the first-message preview: a caller sees their own sessions'
	// opening message; a tenant admin sees any within the tenant.
	CallerUserID  string
	CallerIsAdmin bool
	Limit         int
	Offset        int
}

type sessScan struct {
	SessionID     string    `db:"session_id"`
	UserID        string    `db:"user_id"`
	DisplayName   string    `db:"display_name"`
	Username      string    `db:"username"`
	SessionSource string    `db:"session_source"`
	Requests      int64     `db:"requests"`
	Input         int64     `db:"input_tokens"`
	Output        int64     `db:"output_tokens"`
	CacheRead     int64     `db:"cache_read_tokens"`
	Cost          float64   `db:"cost_usd"`
	Models        string    `db:"models"`    // comma-joined DISTINCT
	Providers     string    `db:"providers"` // comma-joined DISTINCT
	FirstSeen     time.Time `db:"first_seen"`
	LastSeen      time.Time `db:"last_seen"`
	FirstMessage  string    `db:"first_message"`
}

// sessionsFilter builds the WHERE (and optional HAVING) for the session aggregation:
// tenant + window + only rows that carry a session id, plus optional user scope and an
// id search. A model filter is a HAVING (not a WHERE) so a filtered session still shows
// its FULL totals — we want "sessions that touched model X", not "the X-only slice of
// each session". Returns the growing arg list so callers append their own params after.
func sessionsFilter(req ListSessionsRequest) (where, having string, args []any) {
	where = "g.tenant_id = $1 AND g.created_at >= $2 AND g.created_at < $3 AND g.session_id <> ''"
	args = []any{req.TenantID, req.StartDate, req.EndDate}
	if req.UserID != "" {
		where += fmt.Sprintf(" AND g.user_id = $%d", len(args)+1)
		args = append(args, req.UserID)
	}
	if req.Search != "" {
		where += fmt.Sprintf(" AND g.session_id ILIKE $%d", len(args)+1)
		args = append(args, "%"+req.Search+"%")
	}
	if req.Model != "" {
		having = fmt.Sprintf("bool_or(g.model = $%d)", len(args)+1)
		args = append(args, req.Model)
	}
	return where, having, args
}

// sessionsOrderBy maps a sort key (allowlist) to a safe ORDER BY, always with a
// session_id tiebreaker so paging is stable when the sort column ties. Defaults to
// last_seen; direction is desc unless "asc" is asked. The column expressions match the
// SELECT aggregates so they can be referenced by alias (tokens is the summed pair).
func sessionsOrderBy(sort, order string) string {
	var col string
	switch sort {
	case "first_seen":
		col = "first_seen"
	case "cost":
		col = "cost_usd"
	case "requests":
		col = "requests"
	case "tokens":
		col = "(COALESCE(sum(g.input_tokens),0)+COALESCE(sum(g.output_tokens),0))"
	default: // "last_seen" or anything unrecognized
		col = "last_seen"
	}
	dir := "DESC"
	if order == "asc" {
		dir = "ASC"
	}
	return fmt.Sprintf("ORDER BY %s %s, g.session_id DESC", col, dir)
}

func splitNonEmpty(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}

// cleanSnippet collapses whitespace/newlines in a first-message preview to a single
// line so it renders tidily in the table (it is already length-capped in SQL).
func cleanSnippet(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// ListSessions aggregates the tenant's usage rows into one row per session, newest
// active first. Session-source is max()'d (constant per session in practice), models/
// providers are the distinct set touched.
func ListSessions(ctx context.Context, db *common.DatabaseManager, req ListSessionsRequest) (*SessionList, error) {
	if req.EndDate.Before(req.StartDate) {
		return nil, fmt.Errorf("usage: end_date must be >= start_date")
	}
	limit := req.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	offset := max(0, req.Offset)

	where, having, args := sessionsFilter(req)

	// With a model filter the count must also respect the HAVING, so count the grouped
	// sessions in a subquery; without it, the plain DISTINCT count is cheaper.
	countQ := "SELECT count(DISTINCT g.session_id) FROM llm_gateway_usage g WHERE " + where
	if having != "" {
		countQ = "SELECT count(*) FROM (SELECT g.session_id FROM llm_gateway_usage g WHERE " + where +
			" GROUP BY g.session_id HAVING " + having + ") t"
	}
	var total int64
	if err := db.QueryRowAndScan(&total, countQ, args...); err != nil {
		return nil, fmt.Errorf("usage: session count: %w", err)
	}

	// First-message preview: read the precomputed first_user_message column of the
	// session's EARLIEST captured request (extracted at capture; indexed by
	// (session_id, created_at) — a cheap column read, no body parsing per query). Only
	// when body-capture is on, and gated PER ROW to the body-view policy: the caller's
	// own session, or a tenant admin (same tenant).
	firstMsgSelect, firstMsgJoin := "'' AS first_message", ""
	if metering.BodyLoggingEnabled() {
		args = append(args, req.CallerUserID)
		firstMsgSelect = fmt.Sprintf(
			`CASE WHEN (%t OR max(g.user_id) = $%d) THEN max(fb.first_user_message) ELSE '' END AS first_message`,
			req.CallerIsAdmin, len(args))
		firstMsgJoin = `LEFT JOIN LATERAL (
			SELECT rl.first_user_message
			FROM llm_gateway_request_log rl
			WHERE rl.session_id = g.session_id AND rl.tenant_id = g.tenant_id
			  AND rl.deleted_at IS NULL AND rl.expires_at > now()
			ORDER BY rl.created_at LIMIT 1
		) fb ON true`
	}

	// A model filter narrows to sessions that touched it, applied post-aggregation (HAVING)
	// so the row keeps the whole session's totals. Its param already sits in args (before the
	// first-message param appended above), so the $N numbering stays correct wherever it appears.
	havingClause := ""
	if having != "" {
		havingClause = "HAVING " + having
	}

	// GROUP BY session_id ONLY (with max() on the user fields), so a session is exactly
	// one row even in the unlikely case its rows carry differing user details — that
	// keeps the row count consistent with the total (count(DISTINCT session_id)) and the
	// pager correct. A session maps to one user in practice (ids are identity-scoped).
	q := fmt.Sprintf(`
		SELECT g.session_id,
		       COALESCE(max(g.user_id),'') AS user_id,
		       COALESCE(max(u.display_name),'') AS display_name,
		       COALESCE(max(u.username::text),'') AS username,
		       COALESCE(max(NULLIF(g.attributes,'')::jsonb #>> '{derived,session_source}'),'') AS session_source,
		       count(*) AS requests,
		       COALESCE(sum(g.input_tokens),0) AS input_tokens,
		       COALESCE(sum(g.output_tokens),0) AS output_tokens,
		       COALESCE(sum(g.cache_read_tokens),0) AS cache_read_tokens,
		       COALESCE(sum(g.cost_usd),0) AS cost_usd,
		       COALESCE(string_agg(DISTINCT g.model, ','),'') AS models,
		       COALESCE(string_agg(DISTINCT g.provider, ','),'') AS providers,
		       min(g.created_at) AS first_seen, max(g.created_at) AS last_seen,
		       %s
		FROM llm_gateway_usage g
		LEFT JOIN users u ON u.id::text = g.user_id
		%s
		WHERE %s
		GROUP BY g.session_id
		%s
		%s
		LIMIT %d OFFSET %d`, firstMsgSelect, firstMsgJoin, where, havingClause, sessionsOrderBy(req.Sort, req.Order), limit, offset)

	var rows []sessScan
	if err := db.QueryAndScan(&rows, q, args...); err != nil {
		return nil, fmt.Errorf("usage: session list: %w", err)
	}

	out := make([]SessionRow, 0, len(rows))
	for _, r := range rows {
		user := r.DisplayName
		if user == "" {
			user = r.Username
		}
		if user == "" {
			user = r.UserID
		}
		out = append(out, SessionRow{
			SessionID: r.SessionID, SessionSource: r.SessionSource,
			User: user, UserID: r.UserID,
			Requests: r.Requests, InputTokens: r.Input, OutputTokens: r.Output,
			CachedInputTokens: r.CacheRead, CostUsd: r.Cost,
			FirstMessage: cleanSnippet(r.FirstMessage),
			Models:       splitNonEmpty(r.Models), Providers: splitNonEmpty(r.Providers),
			FirstSeen: r.FirstSeen, LastSeen: r.LastSeen,
		})
	}
	// Never report a total below what this page already shows, so the pager can't break
	// if the count and list momentarily disagree.
	if got := int64(offset + len(out)); total < got {
		total = got
	}
	return &SessionList{Rows: out, Total: total, Limit: limit, Offset: offset}, nil
}
