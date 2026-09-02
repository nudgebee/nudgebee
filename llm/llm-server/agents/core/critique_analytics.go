package core

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/lib/pq"
)

// Cross-tenant critique reads. No account/tenant filter — gated by requireTenantReadAdmin.

// judgedDecisions: the two decisions runCritique returns. Others come from
// other critique_type producers and are excluded from every rate here.
const judgedDecisionsClause = "(decision = 'accept' OR decision = 'refine')"

// critiqueThemes are keyword heuristics over critique feedback text — not a
// maintained taxonomy. See ThemesView's banner.
var critiqueThemes = []struct {
	Key      string
	Patterns []string // ILIKE patterns, OR'd together
}{
	{"root_cause_not_verified", []string{"%root cause%"}},
	{"incomplete", []string{"%incomplete%"}},
	{"manual_action", []string{"%manual%", "%yourself%"}},
	{"evidence", []string{"%evidence%"}},
	{"verify", []string{"%verify%", "%confirm%"}},
	{"guessing", []string{"%guess%", "%assum%", "%speculat%"}},
	{"hallucination", []string{"%hallucinat%"}},
	{"symptom_not_cause", []string{"%symptom%"}},
	{"schema_validation", []string{"%schema%", "%validation%"}},
}

// CritiqueFilter scopes every critique-analytics query. Cross-tenant by design.
type CritiqueFilter struct {
	StartDate  time.Time
	EndDate    time.Time
	AgentNames []string
	Decisions  []string
	// Theme, when set, overrides Decisions — refine rows matching the theme's patterns.
	Theme string
}

func (f CritiqueFilter) buildWhere() (string, []any) {
	args := []any{f.StartDate, f.EndDate}
	clauses := []string{"created_at >= $1", "created_at <= $2"}
	n := 3
	if len(f.AgentNames) > 0 {
		clauses = append(clauses, fmt.Sprintf("agent_name = ANY($%d)", n))
		args = append(args, pq.Array(f.AgentNames))
		n++
	}
	if f.Theme != "" {
		if patterns, ok := themePatterns(f.Theme); ok {
			ors := make([]string, 0, len(patterns))
			for _, p := range patterns {
				ors = append(ors, fmt.Sprintf("feedback ILIKE $%d", n))
				args = append(args, p)
				n++
			}
			clauses = append(clauses, fmt.Sprintf("decision = 'refine' AND (%s)", strings.Join(ors, " OR ")))
		}
	} else if len(f.Decisions) > 0 {
		clauses = append(clauses, fmt.Sprintf("decision = ANY($%d)", n))
		args = append(args, pq.Array(f.Decisions))
	}
	return strings.Join(clauses, " AND "), args
}

// themePatterns looks up a theme's ILIKE patterns by key.
func themePatterns(key string) ([]string, bool) {
	for _, t := range critiqueThemes {
		if t.Key == key {
			return t.Patterns, true
		}
	}
	return nil, false
}

func refinePct(refined, judged int64) float64 {
	if judged <= 0 {
		return 0
	}
	return float64(refined) / float64(judged) * 100
}

// CritiqueTotals is the filter-wide KPI block.
type CritiqueTotals struct {
	Judged    int64   `json:"judged"`
	Refined   int64   `json:"refined"`
	RefinePct float64 `json:"refine_pct"`
}

// CritiqueAgentRow is one agent's refine-rate breakdown.
type CritiqueAgentRow struct {
	AgentName string  `json:"agent_name"`
	Judged    int64   `json:"judged"`
	Refined   int64   `json:"refined"`
	RefinePct float64 `json:"refine_pct"`
}

// CritiqueThemeExample is one real critique backing a theme count.
type CritiqueThemeExample struct {
	ID        string `json:"id" db:"id"`
	AgentName string `json:"agent_name" db:"agent_name"`
	Feedback  string `json:"feedback" db:"feedback"`
}

// CritiqueThemeRow is one theme's count plus a few recent matching critiques.
type CritiqueThemeRow struct {
	Theme    string                 `json:"theme"`
	Count    int64                  `json:"count"`
	Examples []CritiqueThemeExample `json:"examples"`
}

// themeExampleLimit caps example critiques per theme in the summary payload.
const themeExampleLimit = 3

// CritiqueSummary backs critiques_aggregate_all.
type CritiqueSummary struct {
	Totals  CritiqueTotals     `json:"totals"`
	ByAgent []CritiqueAgentRow `json:"by_agent"`
	Themes  []CritiqueThemeRow `json:"themes"`
}

type critiqueTotalsScan struct {
	Judged  int64 `db:"judged"`
	Refined int64 `db:"refined"`
}

type critiqueAgentScan struct {
	AgentName string `db:"agent_name"`
	Judged    int64  `db:"judged"`
	Refined   int64  `db:"refined"`
}

// GetCritiqueSummary computes totals, per-agent refine rate, and themes.
func (chat *ConversationDao) GetCritiqueSummary(filter CritiqueFilter) (CritiqueSummary, error) {
	if filter.EndDate.Before(filter.StartDate) {
		return CritiqueSummary{}, fmt.Errorf("GetCritiqueSummary: end_date must be >= start_date")
	}
	where, args := filter.buildWhere()

	totalsQuery := fmt.Sprintf(`
		SELECT
			COUNT(*) FILTER (WHERE %s) AS judged,
			COUNT(*) FILTER (WHERE decision = 'refine') AS refined
		FROM llm_conversation_agent_critiques
		WHERE %s`, judgedDecisionsClause, where)
	var totalsScanResult critiqueTotalsScan
	if err := chat.dbManager.Db.Get(&totalsScanResult, totalsQuery, args...); err != nil {
		slog.Error("GetCritiqueSummary: totals query failed", "error", err)
		return CritiqueSummary{}, fmt.Errorf("GetCritiqueSummary: %w", err)
	}

	agentQuery := fmt.Sprintf(`
		SELECT
			agent_name,
			COUNT(*) FILTER (WHERE %s) AS judged,
			COUNT(*) FILTER (WHERE decision = 'refine') AS refined
		FROM llm_conversation_agent_critiques
		WHERE %s
		GROUP BY agent_name
		HAVING COUNT(*) FILTER (WHERE %s) > 0
		ORDER BY refined DESC`, judgedDecisionsClause, where, judgedDecisionsClause)
	var agentScans []critiqueAgentScan
	if err := chat.dbManager.Db.Select(&agentScans, agentQuery, args...); err != nil {
		slog.Error("GetCritiqueSummary: by-agent query failed", "error", err)
		return CritiqueSummary{}, fmt.Errorf("GetCritiqueSummary: %w", err)
	}

	themes, err := chat.runCritiqueThemes(where, args)
	if err != nil {
		return CritiqueSummary{}, err
	}

	byAgent := make([]CritiqueAgentRow, 0, len(agentScans))
	for _, s := range agentScans {
		byAgent = append(byAgent, CritiqueAgentRow{
			AgentName: s.AgentName,
			Judged:    s.Judged,
			Refined:   s.Refined,
			RefinePct: refinePct(s.Refined, s.Judged),
		})
	}

	return CritiqueSummary{
		Totals: CritiqueTotals{
			Judged:    totalsScanResult.Judged,
			Refined:   totalsScanResult.Refined,
			RefinePct: refinePct(totalsScanResult.Refined, totalsScanResult.Judged),
		},
		ByAgent: byAgent,
		Themes:  themes,
	}, nil
}

// runCritiqueThemes counts refine rows matching each theme's patterns.
func (chat *ConversationDao) runCritiqueThemes(where string, args []any) ([]CritiqueThemeRow, error) {
	n := len(args) + 1
	queryArgs := append([]any{}, args...)
	selects := make([]string, 0, len(critiqueThemes))
	for _, t := range critiqueThemes {
		ors := make([]string, 0, len(t.Patterns))
		for _, p := range t.Patterns {
			ors = append(ors, fmt.Sprintf("feedback ILIKE $%d", n))
			queryArgs = append(queryArgs, p)
			n++
		}
		selects = append(selects, fmt.Sprintf("COUNT(*) FILTER (WHERE decision = 'refine' AND (%s)) AS %s", strings.Join(ors, " OR "), t.Key))
	}
	query := fmt.Sprintf(`SELECT %s FROM llm_conversation_agent_critiques WHERE %s`, strings.Join(selects, ", "), where)

	row := chat.dbManager.Db.QueryRowx(query, queryArgs...)
	scanned := map[string]any{}
	if err := row.MapScan(scanned); err != nil {
		slog.Error("runCritiqueThemes: query failed", "error", err)
		return nil, fmt.Errorf("runCritiqueThemes: %w", err)
	}

	themes := make([]CritiqueThemeRow, 0, len(critiqueThemes))
	for _, t := range critiqueThemes {
		count, _ := scanned[t.Key].(int64)
		row := CritiqueThemeRow{Theme: t.Key, Count: count}
		if count > 0 {
			examples, err := chat.fetchThemeExamples(where, args, t.Patterns)
			if err != nil {
				return nil, err
			}
			row.Examples = examples
		}
		themes = append(themes, row)
	}
	return themes, nil
}

// fetchThemeExamples returns recent critiques matching patterns — the real text behind a theme count.
func (chat *ConversationDao) fetchThemeExamples(where string, args []any, patterns []string) ([]CritiqueThemeExample, error) {
	n := len(args) + 1
	queryArgs := append([]any{}, args...)
	ors := make([]string, 0, len(patterns))
	for _, p := range patterns {
		ors = append(ors, fmt.Sprintf("feedback ILIKE $%d", n))
		queryArgs = append(queryArgs, p)
		n++
	}
	query := fmt.Sprintf(`
		SELECT
			id::text AS id,
			agent_name,
			COALESCE(substring(feedback from '<feedback>(.*?)</feedback>'), feedback, '') AS feedback
		FROM llm_conversation_agent_critiques
		WHERE %s AND decision = 'refine' AND (%s)
		ORDER BY created_at DESC
		LIMIT $%d`, where, strings.Join(ors, " OR "), n)
	queryArgs = append(queryArgs, themeExampleLimit)

	var examples []CritiqueThemeExample
	if err := chat.dbManager.Db.Select(&examples, query, queryArgs...); err != nil {
		slog.Error("fetchThemeExamples: query failed", "error", err)
		return nil, fmt.Errorf("fetchThemeExamples: %w", err)
	}
	return examples, nil
}

// CritiqueTrendPoint is one time bucket's judged/refined counts.
type CritiqueTrendPoint struct {
	Bucket    time.Time `json:"bucket" db:"bucket"`
	Judged    int64     `json:"judged" db:"judged"`
	Refined   int64     `json:"refined" db:"refined"`
	RefinePct float64   `json:"refine_pct" db:"-"`
}

// CritiqueTrend is the Overview rate-over-time series.
type CritiqueTrend struct {
	Granularity string               `json:"granularity"`
	Points      []CritiqueTrendPoint `json:"points"`
}

// GetCritiqueTrend buckets judged/refined counts by day/week/month.
func (chat *ConversationDao) GetCritiqueTrend(filter CritiqueFilter, granularity string) (CritiqueTrend, error) {
	if filter.EndDate.Before(filter.StartDate) {
		return CritiqueTrend{}, fmt.Errorf("GetCritiqueTrend: end_date must be >= start_date")
	}
	if !usageGranularities[granularity] {
		return CritiqueTrend{}, fmt.Errorf("GetCritiqueTrend: invalid granularity %q", granularity)
	}
	where, args := filter.buildWhere()
	bucketExpr := fmt.Sprintf("date_trunc('%s', created_at)", granularity)
	query := fmt.Sprintf(`
		SELECT
			%s AS bucket,
			COUNT(*) FILTER (WHERE %s) AS judged,
			COUNT(*) FILTER (WHERE decision = 'refine') AS refined
		FROM llm_conversation_agent_critiques
		WHERE %s
		GROUP BY bucket
		ORDER BY bucket`, bucketExpr, judgedDecisionsClause, where)

	var points []CritiqueTrendPoint
	if err := chat.dbManager.Db.Select(&points, query, args...); err != nil {
		slog.Error("GetCritiqueTrend: query failed", "error", err)
		return CritiqueTrend{}, fmt.Errorf("GetCritiqueTrend: %w", err)
	}
	for i := range points {
		points[i].RefinePct = refinePct(points[i].Refined, points[i].Judged)
	}
	return CritiqueTrend{Granularity: granularity, Points: points}, nil
}

// CritiqueListRow is one raw critique record for the Browse view.
type CritiqueListRow struct {
	ID               string    `json:"id" db:"id"`
	AgentName        string    `json:"agent_name" db:"agent_name"`
	Decision         string    `json:"decision" db:"decision"`
	Input            string    `json:"input" db:"input"`
	CritiquedContent string    `json:"critiqued_content" db:"critiqued_content"`
	Feedback         string    `json:"feedback" db:"feedback"`
	CreatedAt        time.Time `json:"created_at" db:"created_at"`
	// AccountID/ConversationID/MessageID were already stored on this table but
	// not selected; SessionID is looked up from llm_conversations. Together
	// they back the Browse view's "Go to conversation" deep-link (#35815).
	AccountID      string `json:"account_id" db:"account_id"`
	ConversationID string `json:"conversation_id" db:"conversation_id"`
	MessageID      string `json:"message_id" db:"message_id"`
	SessionID      string `json:"session_id" db:"session_id"`
}

// CritiqueList is a paginated page of rows plus the total match count.
type CritiqueList struct {
	Rows  []CritiqueListRow `json:"rows"`
	Total int64             `json:"total"`
}

const (
	defaultCritiqueListLimit = 50
	maxCritiqueListLimit     = 200
)

// GetCritiqueList returns a paginated, filtered page of raw critique rows.
func (chat *ConversationDao) GetCritiqueList(filter CritiqueFilter, limit, offset int) (CritiqueList, error) {
	if filter.EndDate.Before(filter.StartDate) {
		return CritiqueList{}, fmt.Errorf("GetCritiqueList: end_date must be >= start_date")
	}
	if limit <= 0 {
		limit = defaultCritiqueListLimit
	}
	if limit > maxCritiqueListLimit {
		limit = maxCritiqueListLimit
	}
	if offset < 0 {
		offset = 0
	}

	where, args := filter.buildWhere()

	var total int64
	countQuery := fmt.Sprintf(`SELECT COUNT(*) FROM llm_conversation_agent_critiques WHERE %s`, where)
	if err := chat.dbManager.Db.Get(&total, countQuery, args...); err != nil {
		slog.Error("GetCritiqueList: count query failed", "error", err)
		return CritiqueList{}, fmt.Errorf("GetCritiqueList: %w", err)
	}

	n := len(args)
	query := fmt.Sprintf(`
		SELECT
			id::text AS id,
			agent_name,
			decision,
			input,
			critiqued_content,
			COALESCE(substring(feedback from '<feedback>(.*?)</feedback>'), feedback, '') AS feedback,
			created_at,
			account_id::text AS account_id,
			conversation_id::text AS conversation_id,
			message_id::text AS message_id,
			COALESCE((SELECT session_id FROM llm_conversations WHERE id = llm_conversation_agent_critiques.conversation_id), '') AS session_id
		FROM llm_conversation_agent_critiques
		WHERE %s
		ORDER BY created_at DESC
		LIMIT $%d OFFSET $%d`, where, n+1, n+2)

	rowArgs := append(append([]any{}, args...), limit, offset)
	var rows []CritiqueListRow
	if err := chat.dbManager.Db.Select(&rows, query, rowArgs...); err != nil {
		slog.Error("GetCritiqueList: query failed", "error", err)
		return CritiqueList{}, fmt.Errorf("GetCritiqueList: %w", err)
	}
	return CritiqueList{Rows: rows, Total: total}, nil
}

// --- API request/response DTOs + handlers -----------------------------------

// CritiqueSummaryRequest is the request for critiques_aggregate_all.
type CritiqueSummaryRequest struct {
	StartDate string   `json:"start_date" validate:"required"`
	EndDate   string   `json:"end_date" validate:"required"`
	Agents    []string `json:"agents,omitempty"`
}

// HandleCritiqueSummaryApi backs critiques_aggregate_all. Caller must
// already have passed requireTenantReadAdmin (api/chains.go).
func HandleCritiqueSummaryApi(request CritiqueSummaryRequest) (CritiqueSummary, error) {
	startDate, err := time.Parse(time.RFC3339, request.StartDate)
	if err != nil {
		return CritiqueSummary{}, fmt.Errorf("HandleCritiqueSummaryApi: invalid start_date: %w", err)
	}
	endDate, err := time.Parse(time.RFC3339, request.EndDate)
	if err != nil {
		return CritiqueSummary{}, fmt.Errorf("HandleCritiqueSummaryApi: invalid end_date: %w", err)
	}
	return GetConversationDao().GetCritiqueSummary(CritiqueFilter{
		StartDate:  startDate,
		EndDate:    endDate,
		AgentNames: request.Agents,
	})
}

// CritiqueTrendRequest is the request for critiques_trend_all.
type CritiqueTrendRequest struct {
	StartDate   string   `json:"start_date" validate:"required"`
	EndDate     string   `json:"end_date" validate:"required"`
	Agents      []string `json:"agents,omitempty"`
	Granularity string   `json:"granularity"` // day|week|month, default "day"
}

// HandleCritiqueTrendApi backs critiques_trend_all.
func HandleCritiqueTrendApi(request CritiqueTrendRequest) (CritiqueTrend, error) {
	startDate, err := time.Parse(time.RFC3339, request.StartDate)
	if err != nil {
		return CritiqueTrend{}, fmt.Errorf("HandleCritiqueTrendApi: invalid start_date: %w", err)
	}
	endDate, err := time.Parse(time.RFC3339, request.EndDate)
	if err != nil {
		return CritiqueTrend{}, fmt.Errorf("HandleCritiqueTrendApi: invalid end_date: %w", err)
	}
	granularity := request.Granularity
	if granularity == "" {
		granularity = "day"
	}
	return GetConversationDao().GetCritiqueTrend(CritiqueFilter{
		StartDate:  startDate,
		EndDate:    endDate,
		AgentNames: request.Agents,
	}, granularity)
}

// CritiqueListRequest is the request for critiques_list_all.
type CritiqueListRequest struct {
	StartDate string   `json:"start_date" validate:"required"`
	EndDate   string   `json:"end_date" validate:"required"`
	Agents    []string `json:"agents,omitempty"`
	Decisions []string `json:"decisions,omitempty"`
	// Theme, when set, overrides Decisions.
	Theme  string `json:"theme,omitempty"`
	Limit  int    `json:"limit,omitempty"`
	Offset int    `json:"offset,omitempty"`
}

// HandleCritiqueListApi backs critiques_list_all.
func HandleCritiqueListApi(request CritiqueListRequest) (CritiqueList, error) {
	startDate, err := time.Parse(time.RFC3339, request.StartDate)
	if err != nil {
		return CritiqueList{}, fmt.Errorf("HandleCritiqueListApi: invalid start_date: %w", err)
	}
	endDate, err := time.Parse(time.RFC3339, request.EndDate)
	if err != nil {
		return CritiqueList{}, fmt.Errorf("HandleCritiqueListApi: invalid end_date: %w", err)
	}
	if request.Theme != "" {
		if _, ok := themePatterns(request.Theme); !ok {
			return CritiqueList{}, fmt.Errorf("HandleCritiqueListApi: unknown theme %q", request.Theme)
		}
	}
	return GetConversationDao().GetCritiqueList(CritiqueFilter{
		StartDate:  startDate,
		EndDate:    endDate,
		AgentNames: request.Agents,
		Decisions:  request.Decisions,
		Theme:      request.Theme,
	}, request.Limit, request.Offset)
}
