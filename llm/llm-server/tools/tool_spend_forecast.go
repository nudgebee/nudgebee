package tools

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"time"

	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
)

func init() {
	core.RegisterNBToolFactory(ToolSpendForecast, func(accountId string) (core.NBTool, error) {
		return SpendForecastTool{}, nil
	})
}

const ToolSpendForecast = "spend_forecast"

// SpendForecastTool projects current-month cloud spend to month-end from the
// daily run-rate and compares it to the previous full month. Like
// SpendSummaryTool it runs fixed parameterized queries over the daily `spends`
// table (Metastore) — no LLM-generated SQL. Read-only.
type SpendForecastTool struct{}

func (t SpendForecastTool) Name() string             { return ToolSpendForecast }
func (t SpendForecastTool) GetType() core.NBToolType { return core.NBToolTypeTool }

func (t SpendForecastTool) Description() string {
	return "Projects this month's cloud spend to month-end from the recent daily run-rate and compares it to the previous full month. " +
		"Use for 'are we on track to overspend?', 'what will the bill be this month?', forecast/projection questions. " +
		"Optional account_id parameter: UUID of a specific cloud account (defaults to the current account). " +
		"Returns month-to-date spend, daily run-rate, projected month-end total, previous-month total, and the projected percentage change."
}

func (t SpendForecastTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"account_id": {
				Type:        core.ToolSchemaTypeString,
				Description: "UUID of a specific cloud account to scope the forecast to. Defaults to the current account.",
			},
		},
		Required: []string{},
	}
}

// InferToolRequestType classifies this tool as read-only so it can be parallelized safely.
func (t SpendForecastTool) InferToolRequestType(_ *security.RequestContext, _, _ string) (core.ToolRequestType, error) {
	return core.ToolRequestTypeRead, nil
}

func (t SpendForecastTool) Call(nbCtx core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	accountId := ""
	if v, ok := input.Arguments["account_id"].(string); ok && v != "" {
		accountId = v
	}
	// Default to the requesting user's account to avoid cross-account
	// double-counting (mirrors spend_summary scoping rationale).
	if accountId == "" {
		accountId = nbCtx.AccountId
	}

	tenantId, err := security.GetTenantIdFromAccountId(nbCtx.AccountId)
	if err != nil {
		return core.NBToolResponse{
			Data:   fmt.Sprintf("Error resolving tenant: %s", err.Error()),
			Status: core.NBToolResponseStatusError,
		}, nil
	}
	if tenantId == "" {
		return core.NBToolResponse{
			Data:   "No tenant found for this account.",
			Status: core.NBToolResponseStatusError,
		}, nil
	}

	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return core.NBToolResponse{
			Data:   fmt.Sprintf("Database error: %s", err.Error()),
			Status: core.NBToolResponseStatusError,
		}, nil
	}

	// Pull daily totals from the first of the previous month through start-of-today.
	// This single window covers the previous full month, the current month-to-date,
	// and the trailing run-rate; everything else is computed in Go.
	now := time.Now().UTC()
	today := now.Truncate(24 * time.Hour)
	firstOfMonth := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC)
	firstOfPrevMonth := firstOfMonth.AddDate(0, -1, 0)

	daily, err := queryDailySpend(dbManager, tenantId, accountId, firstOfPrevMonth, today)
	if err != nil {
		slog.Error("spend_forecast: query failed", "error", err, "account_id", accountId)
		return core.NBToolResponse{
			Data:   fmt.Sprintf("Error querying spend data: %s", err.Error()),
			Status: core.NBToolResponseStatusError,
		}, nil
	}

	f := computeForecast(daily, now)
	f.AccountID = accountId
	f.Currency = "USD"

	jsonBytes, err := json.Marshal(f)
	if err != nil {
		return core.NBToolResponse{
			Data:   fmt.Sprintf("Error formatting response: %s", err.Error()),
			Status: core.NBToolResponseStatusError,
		}, nil
	}

	return core.NBToolResponse{
		Data:   string(jsonBytes),
		Type:   core.NBToolResponseTypeJson,
		Status: core.NBToolResponseStatusSuccess,
	}, nil
}

type dailySpend struct {
	Day    time.Time `db:"day"`
	Amount float64   `db:"amount"`
}

type forecastResult struct {
	AccountID              string  `json:"account_id,omitempty"`
	Currency               string  `json:"currency"`
	Month                  string  `json:"month"`        // current month, e.g. "2026-05"
	AsOfDate               string  `json:"as_of_date"`   // last complete day included
	DaysElapsed            int     `json:"days_elapsed"` // complete days so far this month
	DaysInMonth            int     `json:"days_in_month"`
	DaysRemaining          int     `json:"days_remaining"`
	MonthToDate            float64 `json:"month_to_date"`
	AvgDailyLast7d         float64 `json:"avg_daily_last_7d"`
	AvgDailyMtd            float64 `json:"avg_daily_mtd"`
	ProjectedMonthTotal    float64 `json:"projected_month_total"`
	PreviousMonthTotal     float64 `json:"previous_month_total"`
	ProjectedVsPreviousPct float64 `json:"projected_vs_previous_month_pct"`
	Note                   string  `json:"note"`
}

// computeForecast derives the month-end projection from a daily spend series.
// It is pure (no DB/clock side effects beyond the passed `now`) so the run-rate
// math can be unit-tested directly. The projection is:
//
//	projected = month_to_date + daily_run_rate × days_remaining
//
// where daily_run_rate is the average spend over the trailing 7 days, computed
// over the days that actually have data so a newly onboarded account (whose
// trailing window is padded with pre-onboarding zeros) is not under-projected.
func computeForecast(daily []dailySpend, now time.Time) forecastResult {
	today := now.UTC().Truncate(24 * time.Hour)
	firstOfMonth := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC)
	firstOfPrevMonth := firstOfMonth.AddDate(0, -1, 0)
	sevenDaysAgo := today.AddDate(0, 0, -7)

	dim := daysInMonth(today)
	elapsed := today.Day() - 1 // complete days this month (today is excluded — partial)
	remaining := dim - elapsed

	var mtd, prevMonth, last7 float64
	trailingDays := 0 // distinct days with data in the trailing window (one row per day)
	for _, d := range daily {
		day := d.Day.UTC().Truncate(24 * time.Hour)
		switch {
		case !day.Before(firstOfMonth) && day.Before(today):
			mtd += d.Amount
		case !day.Before(firstOfPrevMonth) && day.Before(firstOfMonth):
			prevMonth += d.Amount
		}
		// trailing window can overlap the current month boundary, so check it independently
		if !day.Before(sevenDaysAgo) && day.Before(today) {
			last7 += d.Amount
			trailingDays++
		}
	}

	// Divide the trailing spend by the days that actually have data, not a
	// hardcoded 7. For a mature account this is 7; for an account with only a
	// few days of history it avoids diluting the run-rate with pre-onboarding
	// zeros (which would otherwise under-project the month).
	avgDaily7 := 0.0
	if trailingDays > 0 {
		avgDaily7 = last7 / float64(trailingDays)
	}
	avgDailyMtd := 0.0
	if elapsed > 0 {
		avgDailyMtd = mtd / float64(elapsed)
	}

	// Prefer the responsive trailing rate; fall back to MTD average if the
	// trailing window has no data at all.
	rate := avgDaily7
	if trailingDays == 0 && avgDailyMtd > 0 {
		rate = avgDailyMtd
	}

	projected := mtd + rate*float64(remaining)

	pct := 0.0
	if prevMonth > 0 {
		pct = (projected - prevMonth) / prevMonth * 100
	}

	return forecastResult{
		Month:                  firstOfMonth.Format("2006-01"),
		AsOfDate:               today.AddDate(0, 0, -1).Format("2006-01-02"),
		DaysElapsed:            elapsed,
		DaysInMonth:            dim,
		DaysRemaining:          remaining,
		MonthToDate:            round2(mtd),
		AvgDailyLast7d:         round2(avgDaily7),
		AvgDailyMtd:            round2(avgDailyMtd),
		ProjectedMonthTotal:    round2(projected),
		PreviousMonthTotal:     round2(prevMonth),
		ProjectedVsPreviousPct: round2(pct),
		Note:                   "Projection = month-to-date + (trailing 7-day daily run-rate × days remaining). Recent days may be partial due to cloud billing lag.",
	}
}

// daysInMonth returns the number of days in t's calendar month.
func daysInMonth(t time.Time) int {
	firstOfNext := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location()).AddDate(0, 1, 0)
	return firstOfNext.AddDate(0, 0, -1).Day()
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

func queryDailySpend(dbManager *common.DatabaseManager, tenantId, accountId string, start, end time.Time) ([]dailySpend, error) {
	accountFilter := ""
	args := []any{tenantId, start, end}
	if accountId != "" {
		accountFilter = " AND cloud_account = $4"
		args = append(args, accountId)
	}

	query := fmt.Sprintf(`
		SELECT date_trunc('day', date)::date AS day,
		       ROUND(SUM(amount)::numeric, 2)::float AS amount
		FROM spends
		WHERE tenant = $1 AND date >= $2 AND date < $3%s
		GROUP BY day
		ORDER BY day`, accountFilter)

	rows := []dailySpend{}
	err := dbManager.Db.Select(&rows, query, args...)
	return rows, err
}
