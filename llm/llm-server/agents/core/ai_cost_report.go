package core

import (
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"nudgebee/llm/common"
	"nudgebee/llm/security"

	"github.com/lib/pq"
)

// ai_cost_report.go computes the daily + month-to-date AI cost report,
// consolidated per account for a tenant. One shared computation feeds two
// surfaces: the Slack digest (published via publishAiCostAccountReport
// below) and the Cost Analyser dashboard's Accounts tab
// (HandleAiCostAccountReportApi) — so both reconcile with the Overview
// screen's numbers. Reuses perCallCostExpr / usageBaseFrom from
// usage_analytics.go so cost figures never drift between the two files.

// --- API handler (dashboard Accounts tab) -----------------------------------

// AiCostAccountReportRequest is the request for ai_aggregate_account_cost_report —
// the Cost Analyser dashboard's Accounts tab. account_ids empty = every
// account the caller may read (same convention as UsageMetricsRequest).
type AiCostAccountReportRequest struct {
	AccountIds    []string `json:"account_ids"`
	ReferenceDate string   `json:"reference_date" validate:"required"` // YYYY-MM-DD
}

// HandleAiCostAccountReportApi backs ai_aggregate_account_cost_report. Enforces
// AiCostReportFeatureFlag server-side — the frontend also hides the Accounts
// tab behind the flag, but a flag-off tenant could otherwise still reach this
// handler directly (e.g. a stale Slack deep link from a demo tenant). Reuses
// resolveAccessibleAccounts (usage_analytics.go) so the dashboard's account
// scope matches every other Cost Analyser screen.
func HandleAiCostAccountReportApi(ctx *security.RequestContext, request AiCostAccountReportRequest) (AiCostAccountReport, error) {
	tenantID := ctx.GetSecurityContext().GetTenantId()
	enabled, err := common.IsFeatureEnabled(AiCostReportFeatureFlag, tenantID)
	if err != nil {
		return AiCostAccountReport{}, fmt.Errorf("HandleAiCostAccountReportApi: failed to check %s: %w", AiCostReportFeatureFlag, err)
	}
	if !enabled {
		return AiCostAccountReport{}, fmt.Errorf("HandleAiCostAccountReportApi: %s is not enabled for this tenant", AiCostReportFeatureFlag)
	}

	referenceDate, err := time.Parse("2006-01-02", request.ReferenceDate)
	if err != nil {
		return AiCostAccountReport{}, fmt.Errorf("HandleAiCostAccountReportApi: invalid reference_date: %w", err)
	}
	accountIDs, err := resolveAccessibleAccounts(ctx, request.AccountIds)
	if err != nil {
		return AiCostAccountReport{}, err
	}
	return GetConversationDao().GetAiCostAccountReport(accountIDs, referenceDate)
}

// AiCostReportFeatureFlag gates both the daily digest cron (this file — the
// bulk listAiCostReportEnabledTenantIDs join, not a per-tenant
// common.IsFeatureEnabled call) and the dashboard's Accounts tab, which is
// checked twice: the frontend hides the tab (hasFeatureAccess('AI_COST_REPORT'))
// and HandleAiCostAccountReportApi re-checks it server-side via
// common.IsFeatureEnabled. One per-tenant flag, a gradual opt-in rollout
// rather than always-on. No feature_flag row = disabled
// (common.IsFeatureEnabled's default), matching how new flags in this repo
// ship (e.g. EVENT_AUTO_RAISE_PR_ENABLED). Registering the flag key itself
// (INSERT INTO feature) and enabling it per tenant are separate, deliberately
// out of scope here — see api-server/migrations for that shape.
const AiCostReportFeatureFlag = "AI_COST_REPORT"

// topDriversPerAccount caps each per-account "top drivers" list.
const topDriversPerAccount = 5

// AiCostDriver is one line of a top-N "what drove the cost" breakdown
// (e.g. one model, or one source) within a single account.
type AiCostDriver struct {
	Key     string  `json:"key"`
	CostUsd float64 `json:"cost_usd"`
}

// AiCostAccountRow is one account's line in the consolidated report.
type AiCostAccountRow struct {
	AccountID         string  `json:"account_id"`
	AccountName       string  `json:"account_name"`
	DailyCostUsd      float64 `json:"daily_cost_usd"`
	MtdCostUsd        float64 `json:"mtd_cost_usd"`
	PrevMonthCostUsd  float64 `json:"prev_month_cost_usd"`
	AvgDailyThisMonth float64 `json:"avg_daily_this_month_usd"`
	AvgDailyPrevMonth float64 `json:"avg_daily_prev_month_usd"`
	// PctDeltaAvgDaily compares AvgDailyThisMonth to AvgDailyPrevMonth (0 when
	// AvgDailyPrevMonth is below negligibleCostUsd — no meaningful prior
	// baseline to compare against; dividing by a near-zero baseline would
	// otherwise blow up into a huge, meaningless percentage).
	PctDeltaAvgDaily float64        `json:"pct_delta_avg_daily"`
	TopDailyByModel  []AiCostDriver `json:"top_daily_by_model"`
	TopDailyBySource []AiCostDriver `json:"top_daily_by_source"`
	TopMtdByModel    []AiCostDriver `json:"top_mtd_by_model"`
	TopMtdBySource   []AiCostDriver `json:"top_mtd_by_source"`
}

// ModelCostBreakdownRow is one model's tenant-wide month-to-date cost/call
// breakdown (across every account in accountIDs, not one account) — backs
// the "top models" table alongside the per-account Accounts breakdown.
type ModelCostBreakdownRow struct {
	Model             string  `json:"model"`
	MtdCostUsd        float64 `json:"mtd_cost_usd"`
	CallCount         int64   `json:"call_count"`
	P95CostPerCallUsd float64 `json:"p95_cost_per_call_usd"`
	P99CostPerCallUsd float64 `json:"p99_cost_per_call_usd"`
}

// SourceCostBreakdownRow is the same shape as ModelCostBreakdownRow, grouped
// by source (e.g. Investigation, Automation) instead of model.
type SourceCostBreakdownRow struct {
	Source            string  `json:"source"`
	MtdCostUsd        float64 `json:"mtd_cost_usd"`
	CallCount         int64   `json:"call_count"`
	P95CostPerCallUsd float64 `json:"p95_cost_per_call_usd"`
	P99CostPerCallUsd float64 `json:"p99_cost_per_call_usd"`
}

// AiCostAccountReport is the full per-tenant, per-account report for one
// reference date (the day being reported on — typically "yesterday" for the
// daily digest, since the report is generated the following morning).
type AiCostAccountReport struct {
	TenantID      string             `json:"tenant_id"`
	ReferenceDate string             `json:"reference_date"` // YYYY-MM-DD, UTC
	Accounts      []AiCostAccountRow `json:"accounts"`
	// TopModels / TopSources are tenant-wide (summed across every account in
	// accountIDs), not per-account like Accounts above — the top N by MTD
	// cost (topModelsLimit / topSourcesLimit), with p95/p99 cost-per-call to
	// surface outlier-expensive calls that a mean cost/call would hide.
	TopModels  []ModelCostBreakdownRow  `json:"top_models"`
	TopSources []SourceCostBreakdownRow `json:"top_sources"`
}

// accountDimScan is one (account, dimension-key) cost row from a 2-column
// GROUP BY. Shared by the account×model and account×source breakdowns.
type accountDimScan struct {
	AccountID   string  `db:"account_id"`
	AccountName string  `db:"account_name"`
	DimKey      string  `db:"dim_key"`
	CostUsd     float64 `db:"cost_usd"`
}

// accountTotalScan is one account's cost total with no dimension breakdown.
type accountTotalScan struct {
	AccountID   string  `db:"account_id"`
	AccountName string  `db:"account_name"`
	CostUsd     float64 `db:"cost_usd"`
}

// accountReportWhere renders the account+date-window WHERE, mirroring
// UsageMetricsFilter.buildWhere's account-scope + Optimize self-run exclusion
// (spec: the report must reconcile with the Overview screen's numbers).
func accountReportWhere(accountIDs []string, start, end time.Time) (string, []any) {
	return "c.account_id = ANY($1::uuid[]) AND c.created_at >= $2 AND c.created_at <= $3 AND c.source IS DISTINCT FROM 'Optimize'",
		[]any{pq.Array(accountIDs), start, end}
}

// runAccountDimensionCost aggregates cost by (account, dimExpr) over a
// window. dimExpr is a trusted SQL expression (never caller input) — only
// "t.llm_model" and "c.source" are passed by callers below.
func (chat *ConversationDao) runAccountDimensionCost(accountIDs []string, start, end time.Time, dimExpr string) ([]accountDimScan, error) {
	if len(accountIDs) == 0 {
		return nil, nil
	}
	where, args := accountReportWhere(accountIDs, start, end)
	query := fmt.Sprintf(`
		SELECT
			c.account_id::text                            AS account_id,
			COALESCE(ca.account_name, c.account_id::text)  AS account_name,
			COALESCE(%s::text, 'unknown')                  AS dim_key,
			COALESCE(SUM(%s), 0)                           AS cost_usd
		%s
		LEFT JOIN cloud_accounts ca ON ca.id = c.account_id
		WHERE %s
		GROUP BY 1, 2, 3`,
		dimExpr, perCallCostExpr, usageBaseFrom, where)

	var scans []accountDimScan
	if err := chat.dbManager.Db.Select(&scans, query, args...); err != nil {
		slog.Error("runAccountDimensionCost: query failed", "error", err, "dim", dimExpr)
		return nil, fmt.Errorf("runAccountDimensionCost(%s): %w", dimExpr, err)
	}
	return scans, nil
}

// runAccountTotalCost aggregates cost per account over a window, with no
// dimension breakdown — used for the prev-month total (no drivers needed).
func (chat *ConversationDao) runAccountTotalCost(accountIDs []string, start, end time.Time) ([]accountTotalScan, error) {
	if len(accountIDs) == 0 {
		return nil, nil
	}
	where, args := accountReportWhere(accountIDs, start, end)
	query := fmt.Sprintf(`
		SELECT
			c.account_id::text                            AS account_id,
			COALESCE(ca.account_name, c.account_id::text)  AS account_name,
			COALESCE(SUM(%s), 0)                           AS cost_usd
		%s
		LEFT JOIN cloud_accounts ca ON ca.id = c.account_id
		WHERE %s
		GROUP BY 1, 2`,
		perCallCostExpr, usageBaseFrom, where)

	var scans []accountTotalScan
	if err := chat.dbManager.Db.Select(&scans, query, args...); err != nil {
		slog.Error("runAccountTotalCost: query failed", "error", err)
		return nil, fmt.Errorf("runAccountTotalCost: %w", err)
	}
	return scans, nil
}

// topModelsLimit / topSourcesLimit cap the tenant-wide "top models"/"top
// sources" tables at this many rows.
const topModelsLimit = 10
const topSourcesLimit = 5

// dimCostBreakdownScan is one dimension-key's (model or source) tenant-wide
// MTD cost/call breakdown row, straight off the percentile_cont aggregate
// query below.
type dimCostBreakdownScan struct {
	DimKey            string  `db:"dim_key"`
	MtdCostUsd        float64 `db:"mtd_cost_usd"`
	CallCount         int64   `db:"call_count"`
	P95CostPerCallUsd float64 `db:"p95_cost_per_call_usd"`
	P99CostPerCallUsd float64 `db:"p99_cost_per_call_usd"`
}

// runTopDimensionCostBreakdown aggregates cost/calls/percentiles by dimExpr
// (e.g. "t.llm_model" or "c.source") across every account in accountIDs
// (tenant-wide, not per-account) over a window, sorted by MTD cost desc and
// capped at limit. percentile_cont computes the percentile across individual
// calls' perCallCostExpr within each dimension value's group — a mean
// cost/call hides a handful of outlier-expensive calls (e.g. one huge
// context dump) that p95/p99 surface.
//
// The dim_key column is deliberately phrased "COALESCE(dimExpr, 'unknown')::text"
// rather than runAccountDimensionCost's "COALESCE(dimExpr::text, 'unknown')" —
// identical result, but the two must NOT share that literal substring: tests
// mock several other queries with sqlmock regexes matched against raw query
// text, and this query's WHERE/args are otherwise indistinguishable from the
// daily/MTD-by-dimension scans', so an accidental substring match would
// route this query's response into the wrong mock (and the wrong Go struct).
func (chat *ConversationDao) runTopDimensionCostBreakdown(accountIDs []string, start, end time.Time, dimExpr string, limit int) ([]dimCostBreakdownScan, error) {
	if len(accountIDs) == 0 {
		return nil, nil
	}
	where, args := accountReportWhere(accountIDs, start, end)
	query := fmt.Sprintf(`
		SELECT
			COALESCE(%s, 'unknown')::text                                   AS dim_key,
			COALESCE(SUM(%s), 0)                                            AS mtd_cost_usd,
			COUNT(*)                                                        AS call_count,
			COALESCE(percentile_cont(0.95) WITHIN GROUP (ORDER BY %s), 0)   AS p95_cost_per_call_usd,
			COALESCE(percentile_cont(0.99) WITHIN GROUP (ORDER BY %s), 0)   AS p99_cost_per_call_usd
		%s
		WHERE %s
		GROUP BY 1
		ORDER BY mtd_cost_usd DESC
		LIMIT %d`,
		dimExpr, perCallCostExpr, perCallCostExpr, perCallCostExpr, usageBaseFrom, where, limit)

	var scans []dimCostBreakdownScan
	if err := chat.dbManager.Db.Select(&scans, query, args...); err != nil {
		slog.Error("runTopDimensionCostBreakdown: query failed", "error", err, "dim", dimExpr)
		return nil, fmt.Errorf("runTopDimensionCostBreakdown(%s): %w", dimExpr, err)
	}
	return scans, nil
}

// negligibleCostUsd is the display-rounding boundary the Slack/dashboard
// surfaces format at (2 decimal places): anything below this still shows as
// "$0.00" even though it isn't exactly zero, so it must be treated the same
// way exact-zero values are — an exact `== 0` check alone lets e.g. $0.003
// through, reproducing the same "$0.00" confusion (a driver row that's
// really noise, or a trend % computed against a baseline that's really
// nothing).
const negligibleCostUsd = 0.005

// topDrivers reduces the dim rows for one account to the top-N by cost.
func topDrivers(rows []accountDimScan, accountID string, n int) []AiCostDriver {
	out := make([]AiCostDriver, 0, n)
	for _, r := range rows {
		if r.AccountID != accountID || r.CostUsd < negligibleCostUsd {
			continue
		}
		out = append(out, AiCostDriver{Key: r.DimKey, CostUsd: r.CostUsd})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CostUsd > out[j].CostUsd })
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// sumForAccount sums the dim rows belonging to one account — used to derive
// the per-account total from the model breakdown (which, being unlimited,
// captures 100% of the account's cost) rather than running a third query.
func sumForAccount(rows []accountDimScan, accountID string) float64 {
	var total float64
	for _, r := range rows {
		if r.AccountID == accountID {
			total += r.CostUsd
		}
	}
	return total
}

// GetAiCostAccountReport computes the consolidated per-account report for
// referenceDate (the day being reported on, UTC). accountIDs scopes which
// accounts to include — typically every active account in a tenant
// (security.GetAccountsForTenant). Account names are resolved from
// cloud_accounts inside the aggregation queries themselves, not passed in.
// Accounts with zero cost across the daily, month-to-date, AND previous-month
// windows are omitted — a zero-cost row adds no signal to a cost digest. An
// account with prior-month spend but nothing since IS kept even though daily
// and MTD are both zero, so a cost drop-to-zero (something got turned off, or
// billing/ingestion broke) stays visible instead of silently disappearing.
func (chat *ConversationDao) GetAiCostAccountReport(accountIDs []string, referenceDate time.Time) (AiCostAccountReport, error) {
	report := AiCostAccountReport{ReferenceDate: referenceDate.UTC().Format("2006-01-02"), Accounts: []AiCostAccountRow{}}
	if len(accountIDs) == 0 {
		return report, nil
	}

	day := referenceDate.UTC()
	dayStart := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
	dayEnd := dayStart.Add(24*time.Hour - time.Nanosecond)
	monthStart := time.Date(day.Year(), day.Month(), 1, 0, 0, 0, 0, time.UTC)
	prevMonthStart := monthStart.AddDate(0, -1, 0)
	prevMonthEnd := monthStart.Add(-time.Nanosecond)
	daysElapsedThisMonth := float64(day.Day())
	daysInPrevMonth := float64(prevMonthEnd.Day())

	var (
		dailyByModel, dailyBySource, mtdByModel, mtdBySource []accountDimScan
		prevMonthTotals                                      []accountTotalScan
		topModels, topSources                                []dimCostBreakdownScan
		mu                                                   sync.Mutex // guards firstErr only — each goroutine below owns a distinct result variable, and wg.Wait() happens-after every Done(), so those writes need no lock.
		firstErr                                             error
		wg                                                   sync.WaitGroup
	)
	fail := func(err error) {
		mu.Lock()
		if firstErr == nil {
			firstErr = err
		}
		mu.Unlock()
	}

	wg.Add(7)
	go func() {
		defer wg.Done()
		rows, err := chat.runAccountDimensionCost(accountIDs, dayStart, dayEnd, "t.llm_model")
		if err != nil {
			fail(err)
			return
		}
		dailyByModel = rows
	}()
	go func() {
		defer wg.Done()
		rows, err := chat.runAccountDimensionCost(accountIDs, dayStart, dayEnd, "c.source")
		if err != nil {
			fail(err)
			return
		}
		dailyBySource = rows
	}()
	go func() {
		defer wg.Done()
		rows, err := chat.runAccountDimensionCost(accountIDs, monthStart, dayEnd, "t.llm_model")
		if err != nil {
			fail(err)
			return
		}
		mtdByModel = rows
	}()
	go func() {
		defer wg.Done()
		rows, err := chat.runAccountDimensionCost(accountIDs, monthStart, dayEnd, "c.source")
		if err != nil {
			fail(err)
			return
		}
		mtdBySource = rows
	}()
	go func() {
		defer wg.Done()
		rows, err := chat.runAccountTotalCost(accountIDs, prevMonthStart, prevMonthEnd)
		if err != nil {
			fail(err)
			return
		}
		prevMonthTotals = rows
	}()
	go func() {
		defer wg.Done()
		rows, err := chat.runTopDimensionCostBreakdown(accountIDs, monthStart, dayEnd, "t.llm_model", topModelsLimit)
		if err != nil {
			fail(err)
			return
		}
		topModels = rows
	}()
	go func() {
		defer wg.Done()
		rows, err := chat.runTopDimensionCostBreakdown(accountIDs, monthStart, dayEnd, "c.source", topSourcesLimit)
		if err != nil {
			fail(err)
			return
		}
		topSources = rows
	}()
	wg.Wait()
	if firstErr != nil {
		return AiCostAccountReport{}, firstErr
	}

	prevByAccount := make(map[string]accountTotalScan, len(prevMonthTotals))
	for _, r := range prevMonthTotals {
		prevByAccount[r.AccountID] = r
	}

	// Union every account that appears anywhere (daily, MTD, or prev-month) —
	// an account with prev-month spend but nothing yet today still belongs in
	// the report so a cost drop-to-zero is visible, not silently dropped.
	seen := map[string]string{} // account_id -> account_name
	collectNames := func(rows []accountDimScan) {
		for _, r := range rows {
			seen[r.AccountID] = r.AccountName
		}
	}
	collectNames(dailyByModel)
	collectNames(mtdByModel)
	for _, r := range prevMonthTotals {
		seen[r.AccountID] = r.AccountName
	}
	// Accounts requested but with zero activity anywhere carry no rows in any
	// scan and are intentionally left out (see doc comment).

	rows := make([]AiCostAccountRow, 0, len(seen))
	for accountID, name := range seen {
		daily := sumForAccount(dailyByModel, accountID)
		mtd := sumForAccount(mtdByModel, accountID)
		prevMonth := prevByAccount[accountID].CostUsd
		if daily == 0 && mtd == 0 && prevMonth == 0 {
			continue
		}

		avgThisMonth := mtd / daysElapsedThisMonth
		avgPrevMonth := prevMonth / daysInPrevMonth
		pctDelta := 0.0
		if avgPrevMonth >= negligibleCostUsd {
			pctDelta = (avgThisMonth - avgPrevMonth) / avgPrevMonth * 100
		}

		rows = append(rows, AiCostAccountRow{
			AccountID:         accountID,
			AccountName:       name,
			DailyCostUsd:      daily,
			MtdCostUsd:        mtd,
			PrevMonthCostUsd:  prevMonth,
			AvgDailyThisMonth: avgThisMonth,
			AvgDailyPrevMonth: avgPrevMonth,
			PctDeltaAvgDaily:  pctDelta,
			TopDailyByModel:   topDrivers(dailyByModel, accountID, topDriversPerAccount),
			TopDailyBySource:  topDrivers(dailyBySource, accountID, topDriversPerAccount),
			TopMtdByModel:     topDrivers(mtdByModel, accountID, topDriversPerAccount),
			TopMtdBySource:    topDrivers(mtdBySource, accountID, topDriversPerAccount),
		})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].MtdCostUsd > rows[j].MtdCostUsd })
	report.Accounts = rows

	topModelRows := make([]ModelCostBreakdownRow, 0, len(topModels))
	for _, m := range topModels {
		topModelRows = append(topModelRows, ModelCostBreakdownRow{
			Model:             m.DimKey,
			MtdCostUsd:        m.MtdCostUsd,
			CallCount:         m.CallCount,
			P95CostPerCallUsd: m.P95CostPerCallUsd,
			P99CostPerCallUsd: m.P99CostPerCallUsd,
		})
	}
	report.TopModels = topModelRows

	topSourceRows := make([]SourceCostBreakdownRow, 0, len(topSources))
	for _, s := range topSources {
		topSourceRows = append(topSourceRows, SourceCostBreakdownRow{
			Source:            s.DimKey,
			MtdCostUsd:        s.MtdCostUsd,
			CallCount:         s.CallCount,
			P95CostPerCallUsd: s.P95CostPerCallUsd,
			P99CostPerCallUsd: s.P99CostPerCallUsd,
		})
	}
	report.TopSources = topSourceRows

	return report, nil
}

// --- Tenant-wide orchestration (cron entry point) ---------------------------

// listAiCostReportEnabledTenantIDs returns every tenant that both has at
// least one active cloud account AND has AiCostReportFeatureFlag enabled —
// the set the daily digest iterates. Joins the flag check into the same
// query rather than listing every active tenant and then checking the flag
// one tenant at a time: with 100+ active tenants and the flag enabled for
// only a handful during rollout, a per-tenant round trip was ~100+ sequential
// queries to find a few matches. One join replaces all of them.
func listAiCostReportEnabledTenantIDs() ([]string, error) {
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, fmt.Errorf("listAiCostReportEnabledTenantIDs: failed to get database manager: %w", err)
	}
	var tenantIDs []string
	query := `
		SELECT DISTINCT ca.tenant
		FROM cloud_accounts ca
		JOIN feature_flag ff ON ff.tenant_id = ca.tenant
		WHERE ca.status = 'active'
		  AND ff.feature_id = $1
		  AND ff.status = 'enabled'`
	if err := dbManager.Db.Select(&tenantIDs, query, AiCostReportFeatureFlag); err != nil {
		slog.Error("listAiCostReportEnabledTenantIDs: query failed", "error", err)
		return nil, fmt.Errorf("listAiCostReportEnabledTenantIDs: %w", err)
	}
	return tenantIDs, nil
}

// GetAiCostAccountReportForTenant resolves a tenant's active accounts and
// computes its report. Used by the digest cron (RunAiCostDailyDigest) only —
// the dashboard's Accounts tab calls GetConversationDao().GetAiCostAccountReport
// directly (via HandleAiCostAccountReportApi), scoped to the caller's own
// accessible accounts rather than every account in the tenant.
func GetAiCostAccountReportForTenant(tenantID string, referenceDate time.Time) (AiCostAccountReport, error) {
	accounts, err := security.GetAccountsForTenant(tenantID)
	if err != nil {
		return AiCostAccountReport{}, fmt.Errorf("GetAiCostAccountReportForTenant: %w", err)
	}
	accountIDs := make([]string, 0, len(accounts))
	for _, a := range accounts {
		accountIDs = append(accountIDs, a.ID)
	}
	report, err := GetConversationDao().GetAiCostAccountReport(accountIDs, referenceDate)
	if err != nil {
		return AiCostAccountReport{}, err
	}
	report.TenantID = tenantID
	return report, nil
}

// RunAiCostDailyDigest computes and publishes the daily AI cost digest for
// every AI_COST_REPORT-enabled tenant, for the given referenceDate (the day
// being reported on — callers pass "yesterday" so the report covers a full
// day, not a partial one). Errors for one tenant are logged and skipped so a
// single bad tenant doesn't block the rest of the digest run.
func RunAiCostDailyDigest(referenceDate time.Time) error {
	tenantIDs, err := listAiCostReportEnabledTenantIDs()
	if err != nil {
		return err
	}
	for _, tenantID := range tenantIDs {
		report, err := GetAiCostAccountReportForTenant(tenantID, referenceDate)
		if err != nil {
			slog.Error("RunAiCostDailyDigest: failed to compute report", "tenant_id", tenantID, "error", err)
			continue
		}
		if len(report.Accounts) == 0 {
			continue // nothing to report — no AI usage anywhere in the tenant.
		}
		if err := publishAiCostAccountReport(report); err != nil {
			slog.Error("RunAiCostDailyDigest: failed to publish report", "tenant_id", tenantID, "error", err)
		}
	}
	return nil
}

// aiCostNotificationsExchange / aiCostNotificationsQueue mirror api-server's
// rabbit_mq_notifications_exchange/_queue defaults (services/config/config.go)
// — the two producers publish to the same durable exchange/queue that
// notifications-server's consumer binds to.
const (
	aiCostNotificationsExchange = "notifications_exchange"
	aiCostNotificationsQueue    = "notifications"
)

// publishAiCostAccountReport publishes one tenant's report to the shared
// notifications exchange, in the same envelope shape api-server's report
// publishers use (services/reports/recommendation_digest.go) — "kind" +
// "type" select the message_templates dispatch entry in notifications-server;
// "parameters" is the type-specific payload.
func publishAiCostAccountReport(report AiCostAccountReport) error {
	message := map[string]any{
		"kind":       "notification",
		"type":       "ai_cost_daily_report",
		"source":     "ai_cost",
		"tenant_id":  report.TenantID,
		"parameters": report,
	}
	return common.MqPublish(aiCostNotificationsExchange, aiCostNotificationsQueue, message)
}
