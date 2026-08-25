package core

import (
	"testing"
	"time"

	"nudgebee/llm/common"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// dimBreakdownCols matches dimCostBreakdownScan's db tags — shared by both
// the top-models and top-sources percentile_cont mocks below.
var dimBreakdownCols = []string{"dim_key", "mtd_cost_usd", "call_count", "p95_cost_per_call_usd", "p99_cost_per_call_usd"}

// expectEmptyTopBreakdowns registers no-op mock responses for the top-models
// and top-sources concurrent queries, for tests that don't care about them.
// Anchored on the dim-expression substring (not "percentile_cont" alone —
// both queries contain that), since the model/source queries are otherwise
// textually indistinguishable to sqlmock's regex matcher.
func expectEmptyTopBreakdowns(mock sqlmock.Sqlmock) {
	mock.ExpectQuery("COALESCE\\(t.llm_model, 'unknown'\\)::text").
		WillReturnRows(sqlmock.NewRows(dimBreakdownCols))
	mock.ExpectQuery("COALESCE\\(c.source, 'unknown'\\)::text").
		WillReturnRows(sqlmock.NewRows(dimBreakdownCols))
}

// TestTopDrivers_FiltersSortsAndCaps verifies the pure reduction that powers
// each account's "top drivers" lists: only that account's rows, sorted cost
// desc, capped to n.
func TestTopDrivers_FiltersSortsAndCaps(t *testing.T) {
	rows := []accountDimScan{
		{AccountID: "acc-1", DimKey: "gpt-4o", CostUsd: 1.0},
		{AccountID: "acc-2", DimKey: "claude-opus", CostUsd: 99.0}, // different account, must be excluded
		{AccountID: "acc-1", DimKey: "claude-haiku", CostUsd: 5.0},
		{AccountID: "acc-1", DimKey: "gemini", CostUsd: 3.0},
	}
	got := topDrivers(rows, "acc-1", 2)
	require.Len(t, got, 2)
	assert.Equal(t, "claude-haiku", got[0].Key)
	assert.Equal(t, 5.0, got[0].CostUsd)
	assert.Equal(t, "gemini", got[1].Key)
}

// TestTopDrivers_DropsZeroCostRows verifies a model/source that was called
// but billed nothing (a cache-only hit, a free-tier stub model) is excluded
// — it isn't a cost driver, and including it just pads the list with noise.
func TestTopDrivers_DropsZeroCostRows(t *testing.T) {
	rows := []accountDimScan{
		{AccountID: "acc-1", DimKey: "gemini-3.1-pro-preview", CostUsd: 1.38},
		{AccountID: "acc-1", DimKey: "stub-model", CostUsd: 0},
	}
	got := topDrivers(rows, "acc-1", 5)
	require.Len(t, got, 1)
	assert.Equal(t, "gemini-3.1-pro-preview", got[0].Key)
}

// TestTopDrivers_DropsRowsThatRoundToZeroDisplay verifies a nonzero but tiny
// cost (e.g. $0.003) is excluded too — Slack/dashboard format at 2 decimal
// places, so an exact `== 0` check alone would let it through and still show
// as a confusing "$0.00" driver.
func TestTopDrivers_DropsRowsThatRoundToZeroDisplay(t *testing.T) {
	rows := []accountDimScan{
		{AccountID: "acc-1", DimKey: "gemini-3.1-pro-preview", CostUsd: 1.38},
		{AccountID: "acc-1", DimKey: "tiny-call", CostUsd: 0.003},
		{AccountID: "acc-1", DimKey: "just-over-boundary", CostUsd: 0.005},
	}
	got := topDrivers(rows, "acc-1", 5)
	require.Len(t, got, 2)
	assert.Equal(t, "gemini-3.1-pro-preview", got[0].Key)
	assert.Equal(t, "just-over-boundary", got[1].Key)
}

// TestSumForAccount_OnlyMatchingAccount verifies the derived-total helper
// sums only the requested account's rows — this is what GetAiCostAccountReport
// relies on instead of a dedicated totals query.
func TestSumForAccount_OnlyMatchingAccount(t *testing.T) {
	rows := []accountDimScan{
		{AccountID: "acc-1", CostUsd: 1.5},
		{AccountID: "acc-2", CostUsd: 100.0},
		{AccountID: "acc-1", CostUsd: 2.5},
	}
	assert.Equal(t, 4.0, sumForAccount(rows, "acc-1"))
	assert.Equal(t, 100.0, sumForAccount(rows, "acc-2"))
	assert.Equal(t, 0.0, sumForAccount(rows, "acc-missing"))
}

// TestGetAiCostAccountReport_ComputesRowsAndOmitsZeroCost is an end-to-end DAO
// test: mocks the seven concurrent scans and verifies the arithmetic (MTD/day
// count, prev-month/day count, pct delta) and that an account with no cost
// anywhere in the window is left out of the report.
func TestGetAiCostAccountReport_ComputesRowsAndOmitsZeroCost(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	// The seven scans run as concurrent goroutines — order is non-deterministic.
	mock.MatchExpectationsInOrder(false)

	dao := &ConversationDao{dbManager: &common.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}}

	// referenceDate = 2026-08-10 -> 10 days elapsed this month.
	referenceDate := time.Date(2026, 8, 10, 15, 0, 0, 0, time.UTC)
	// Previous month (July) has 31 days.
	dayStart := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	dayEnd := dayStart.Add(24*time.Hour - time.Nanosecond)
	monthStart := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)

	dimCols := []string{"account_id", "account_name", "dim_key", "cost_usd"}
	totalCols := []string{"account_id", "account_name", "cost_usd"}

	// The daily and MTD scans for a given dimension share IDENTICAL SQL text
	// (only the bound start/end args differ), so they must be disambiguated by
	// .WithArgs() — matching by query-text regex alone lets sqlmock pair either
	// expectation with either concurrent call, non-deterministically.

	// Daily by model: acc-1 spent $10 today across two models.
	mock.ExpectQuery("COALESCE\\(t.llm_model::text, 'unknown'\\)").
		WithArgs(sqlmock.AnyArg(), dayStart, dayEnd).
		WillReturnRows(sqlmock.NewRows(dimCols).
			AddRow("acc-1", "Acme", "gpt-4o", 7.0).
			AddRow("acc-1", "Acme", "claude-haiku", 3.0))

	// Daily by source: same $10 total for acc-1, split by source.
	mock.ExpectQuery("COALESCE\\(c.source::text, 'unknown'\\)").
		WithArgs(sqlmock.AnyArg(), dayStart, dayEnd).
		WillReturnRows(sqlmock.NewRows(dimCols).
			AddRow("acc-1", "Acme", "user_chat", 6.0).
			AddRow("acc-1", "Acme", "auto_event", 4.0))

	// MTD by model: acc-1 $100 MTD, acc-2 has MTD spend but nothing today or
	// prior month (must still appear), acc-3 never appears anywhere (must be
	// omitted).
	mock.ExpectQuery("COALESCE\\(t.llm_model::text, 'unknown'\\)").
		WithArgs(sqlmock.AnyArg(), monthStart, dayEnd).
		WillReturnRows(sqlmock.NewRows(dimCols).
			AddRow("acc-1", "Acme", "gpt-4o", 60.0).
			AddRow("acc-1", "Acme", "claude-haiku", 40.0).
			AddRow("acc-2", "Beta", "gpt-4o", 20.0))

	// MTD by source.
	mock.ExpectQuery("COALESCE\\(c.source::text, 'unknown'\\)").
		WithArgs(sqlmock.AnyArg(), monthStart, dayEnd).
		WillReturnRows(sqlmock.NewRows(dimCols).
			AddRow("acc-1", "Acme", "user_chat", 100.0).
			AddRow("acc-2", "Beta", "user_chat", 20.0))

	// Prev-month totals: acc-1 spent $310 over 31 days -> $10/day avg. Anchored
	// at end-of-string ("GROUP BY 1, 2$") since the dimension queries above all
	// end in "GROUP BY 1, 2, 3" — a bare "GROUP BY 1, 2" substring match would
	// ambiguously match those too.
	mock.ExpectQuery("GROUP BY 1, 2$").
		WillReturnRows(sqlmock.NewRows(totalCols).
			AddRow("acc-1", "Acme", 310.0))

	// Tenant-wide top-models / top-sources breakdowns — not under test here.
	expectEmptyTopBreakdowns(mock)

	report, err := dao.GetAiCostAccountReport([]string{"acc-1", "acc-2", "acc-3"}, referenceDate)
	require.NoError(t, err)
	require.Len(t, report.Accounts, 2, "acc-3 has zero cost everywhere and must be omitted")

	// Sorted by MTD cost desc -> acc-1 (100) before acc-2 (20).
	acc1 := report.Accounts[0]
	assert.Equal(t, "acc-1", acc1.AccountID)
	assert.Equal(t, "Acme", acc1.AccountName)
	assert.Equal(t, 10.0, acc1.DailyCostUsd)
	assert.Equal(t, 100.0, acc1.MtdCostUsd)
	assert.Equal(t, 310.0, acc1.PrevMonthCostUsd)
	assert.InDelta(t, 10.0, acc1.AvgDailyThisMonth, 0.001, "100 MTD / 10 days elapsed")
	assert.InDelta(t, 10.0, acc1.AvgDailyPrevMonth, 0.001, "310 / 31 days in July")
	assert.InDelta(t, 0.0, acc1.PctDeltaAvgDaily, 0.001, "avg daily unchanged month over month")
	require.Len(t, acc1.TopDailyByModel, 2)
	assert.Equal(t, "gpt-4o", acc1.TopDailyByModel[0].Key)

	acc2 := report.Accounts[1]
	assert.Equal(t, "acc-2", acc2.AccountID)
	assert.Equal(t, 0.0, acc2.DailyCostUsd, "no cost today, only MTD")
	assert.Equal(t, 20.0, acc2.MtdCostUsd)
	assert.Equal(t, 0.0, acc2.PrevMonthCostUsd, "no prior-month baseline")
	assert.Equal(t, 0.0, acc2.PctDeltaAvgDaily, "no baseline to compare against")

	require.NoError(t, mock.ExpectationsWereMet())
}

// TestGetAiCostAccountReport_NegligiblePrevMonthBaselineYieldsZeroDelta
// verifies that a previous-month average below the negligibleCostUsd
// display-rounding boundary (nonzero, but rounds to "$0.00") is treated like
// "no baseline" for the trend %, not divided into — otherwise a near-zero
// denominator blows up into a meaningless six-figure percentage even though
// the displayed baseline reads as $0.00.
func TestGetAiCostAccountReport_NegligiblePrevMonthBaselineYieldsZeroDelta(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.MatchExpectationsInOrder(false)

	dao := &ConversationDao{dbManager: &common.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}}

	referenceDate := time.Date(2026, 8, 10, 15, 0, 0, 0, time.UTC)
	dayStart := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	dayEnd := dayStart.Add(24*time.Hour - time.Nanosecond)
	monthStart := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)

	dimCols := []string{"account_id", "account_name", "dim_key", "cost_usd"}
	totalCols := []string{"account_id", "account_name", "cost_usd"}

	mock.ExpectQuery("COALESCE\\(t.llm_model::text, 'unknown'\\)").
		WithArgs(sqlmock.AnyArg(), dayStart, dayEnd).
		WillReturnRows(sqlmock.NewRows(dimCols).AddRow("acc-1", "Acme", "gpt-4o", 5.0))

	mock.ExpectQuery("COALESCE\\(c.source::text, 'unknown'\\)").
		WithArgs(sqlmock.AnyArg(), dayStart, dayEnd).
		WillReturnRows(sqlmock.NewRows(dimCols).AddRow("acc-1", "Acme", "user_chat", 5.0))

	mock.ExpectQuery("COALESCE\\(t.llm_model::text, 'unknown'\\)").
		WithArgs(sqlmock.AnyArg(), monthStart, dayEnd).
		WillReturnRows(sqlmock.NewRows(dimCols).AddRow("acc-1", "Acme", "gpt-4o", 50.0))

	mock.ExpectQuery("COALESCE\\(c.source::text, 'unknown'\\)").
		WithArgs(sqlmock.AnyArg(), monthStart, dayEnd).
		WillReturnRows(sqlmock.NewRows(dimCols).AddRow("acc-1", "Acme", "user_chat", 50.0))

	// Prev month (July, 31 days): $0.03 total -> ~$0.001/day avg — below the
	// $0.005 negligible-cost boundary but not exactly zero.
	mock.ExpectQuery("GROUP BY 1, 2$").
		WillReturnRows(sqlmock.NewRows(totalCols).AddRow("acc-1", "Acme", 0.03))

	// Tenant-wide top-models / top-sources breakdowns — not under test here.
	expectEmptyTopBreakdowns(mock)

	report, err := dao.GetAiCostAccountReport([]string{"acc-1"}, referenceDate)
	require.NoError(t, err)
	require.Len(t, report.Accounts, 1)

	acc := report.Accounts[0]
	assert.InDelta(t, 5.0, acc.AvgDailyThisMonth, 0.001, "50 MTD / 10 days elapsed")
	assert.Greater(t, acc.AvgDailyPrevMonth, 0.0, "baseline isn't exactly zero")
	assert.Less(t, acc.AvgDailyPrevMonth, negligibleCostUsd, "baseline rounds to $0.00 on display")
	assert.Equal(t, 0.0, acc.PctDeltaAvgDaily, "negligible baseline treated as no baseline, not divided into")

	require.NoError(t, mock.ExpectationsWereMet())
}

// TestGetAiCostAccountReport_KeepsPrevMonthOnlyDropToZero verifies an account
// with prior-month spend but nothing today or this month is still included
// (daily == mtd == 0, prevMonth > 0) rather than omitted — a cost drop-to-zero
// (something got turned off, or billing/ingestion broke) is high-signal for a
// cost digest and must stay visible, not be silently dropped by the
// omission filter.
func TestGetAiCostAccountReport_KeepsPrevMonthOnlyDropToZero(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.MatchExpectationsInOrder(false)

	dao := &ConversationDao{dbManager: &common.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}}

	referenceDate := time.Date(2026, 8, 10, 15, 0, 0, 0, time.UTC)
	dayStart := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	dayEnd := dayStart.Add(24*time.Hour - time.Nanosecond)
	monthStart := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)

	dimCols := []string{"account_id", "account_name", "dim_key", "cost_usd"}
	totalCols := []string{"account_id", "account_name", "cost_usd"}

	// No daily or MTD activity anywhere for acc-1.
	mock.ExpectQuery("COALESCE\\(t.llm_model::text, 'unknown'\\)").
		WithArgs(sqlmock.AnyArg(), dayStart, dayEnd).
		WillReturnRows(sqlmock.NewRows(dimCols))
	mock.ExpectQuery("COALESCE\\(c.source::text, 'unknown'\\)").
		WithArgs(sqlmock.AnyArg(), dayStart, dayEnd).
		WillReturnRows(sqlmock.NewRows(dimCols))
	mock.ExpectQuery("COALESCE\\(t.llm_model::text, 'unknown'\\)").
		WithArgs(sqlmock.AnyArg(), monthStart, dayEnd).
		WillReturnRows(sqlmock.NewRows(dimCols))
	mock.ExpectQuery("COALESCE\\(c.source::text, 'unknown'\\)").
		WithArgs(sqlmock.AnyArg(), monthStart, dayEnd).
		WillReturnRows(sqlmock.NewRows(dimCols))

	// Prev month (July, 31 days): acc-1 spent $150 — the only place it appears.
	mock.ExpectQuery("GROUP BY 1, 2$").
		WillReturnRows(sqlmock.NewRows(totalCols).AddRow("acc-1", "Acme", 150.0))

	// Tenant-wide top-models / top-sources breakdowns — not under test here.
	expectEmptyTopBreakdowns(mock)

	report, err := dao.GetAiCostAccountReport([]string{"acc-1"}, referenceDate)
	require.NoError(t, err)
	require.Len(t, report.Accounts, 1, "prev-month-only account must not be omitted")

	acc := report.Accounts[0]
	assert.Equal(t, "acc-1", acc.AccountID)
	assert.Equal(t, 0.0, acc.DailyCostUsd)
	assert.Equal(t, 0.0, acc.MtdCostUsd)
	assert.Equal(t, 150.0, acc.PrevMonthCostUsd)
	assert.InDelta(t, -100.0, acc.PctDeltaAvgDaily, 0.01, "spend went from a real baseline to nothing")

	require.NoError(t, mock.ExpectationsWereMet())
}

// TestGetAiCostAccountReport_PopulatesTopModels verifies the tenant-wide
// top-models breakdown (Model/MtdCostUsd/CallCount/p95/p99) passes through
// from the percentile_cont query into the report untouched — the SQL's own
// ORDER BY mtd_cost_usd DESC / LIMIT already does the sorting and capping, so
// there's no Go-side re-sort to verify beyond preserving row order.
func TestGetAiCostAccountReport_PopulatesTopModels(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.MatchExpectationsInOrder(false)

	dao := &ConversationDao{dbManager: &common.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}}

	referenceDate := time.Date(2026, 8, 10, 15, 0, 0, 0, time.UTC)
	dayStart := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	dayEnd := dayStart.Add(24*time.Hour - time.Nanosecond)
	monthStart := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)

	dimCols := []string{"account_id", "account_name", "dim_key", "cost_usd"}
	totalCols := []string{"account_id", "account_name", "cost_usd"}

	mock.ExpectQuery("COALESCE\\(t.llm_model::text, 'unknown'\\)").
		WithArgs(sqlmock.AnyArg(), dayStart, dayEnd).
		WillReturnRows(sqlmock.NewRows(dimCols))
	mock.ExpectQuery("COALESCE\\(c.source::text, 'unknown'\\)").
		WithArgs(sqlmock.AnyArg(), dayStart, dayEnd).
		WillReturnRows(sqlmock.NewRows(dimCols))
	mock.ExpectQuery("COALESCE\\(t.llm_model::text, 'unknown'\\)").
		WithArgs(sqlmock.AnyArg(), monthStart, dayEnd).
		WillReturnRows(sqlmock.NewRows(dimCols).AddRow("acc-1", "Acme", "gpt-4o", 100.0))
	mock.ExpectQuery("COALESCE\\(c.source::text, 'unknown'\\)").
		WithArgs(sqlmock.AnyArg(), monthStart, dayEnd).
		WillReturnRows(sqlmock.NewRows(dimCols).AddRow("acc-1", "Acme", "user_chat", 100.0))
	mock.ExpectQuery("GROUP BY 1, 2$").
		WillReturnRows(sqlmock.NewRows(totalCols))

	mock.ExpectQuery("COALESCE\\(t.llm_model, 'unknown'\\)::text").
		WillReturnRows(sqlmock.NewRows(dimBreakdownCols).
			AddRow("gpt-4o", 100.0, 50, 3.5, 9.2).
			AddRow("claude-haiku", 20.0, 200, 0.15, 0.4))
	mock.ExpectQuery("COALESCE\\(c.source, 'unknown'\\)::text").
		WillReturnRows(sqlmock.NewRows(dimBreakdownCols))

	report, err := dao.GetAiCostAccountReport([]string{"acc-1"}, referenceDate)
	require.NoError(t, err)
	require.Len(t, report.TopModels, 2)

	m0 := report.TopModels[0]
	assert.Equal(t, "gpt-4o", m0.Model)
	assert.Equal(t, 100.0, m0.MtdCostUsd)
	assert.Equal(t, int64(50), m0.CallCount)
	assert.Equal(t, 3.5, m0.P95CostPerCallUsd)
	assert.Equal(t, 9.2, m0.P99CostPerCallUsd)
	assert.Equal(t, "claude-haiku", report.TopModels[1].Model)

	require.NoError(t, mock.ExpectationsWereMet())
}

// TestGetAiCostAccountReport_PopulatesTopSources mirrors
// TestGetAiCostAccountReport_PopulatesTopModels for the source breakdown —
// same query shape, grouped by c.source instead of t.llm_model, capped at
// topSourcesLimit (5) instead of topModelsLimit (10).
func TestGetAiCostAccountReport_PopulatesTopSources(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.MatchExpectationsInOrder(false)

	dao := &ConversationDao{dbManager: &common.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}}

	referenceDate := time.Date(2026, 8, 10, 15, 0, 0, 0, time.UTC)
	dayStart := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	dayEnd := dayStart.Add(24*time.Hour - time.Nanosecond)
	monthStart := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)

	dimCols := []string{"account_id", "account_name", "dim_key", "cost_usd"}
	totalCols := []string{"account_id", "account_name", "cost_usd"}

	mock.ExpectQuery("COALESCE\\(t.llm_model::text, 'unknown'\\)").
		WithArgs(sqlmock.AnyArg(), dayStart, dayEnd).
		WillReturnRows(sqlmock.NewRows(dimCols))
	mock.ExpectQuery("COALESCE\\(c.source::text, 'unknown'\\)").
		WithArgs(sqlmock.AnyArg(), dayStart, dayEnd).
		WillReturnRows(sqlmock.NewRows(dimCols))
	mock.ExpectQuery("COALESCE\\(t.llm_model::text, 'unknown'\\)").
		WithArgs(sqlmock.AnyArg(), monthStart, dayEnd).
		WillReturnRows(sqlmock.NewRows(dimCols).AddRow("acc-1", "Acme", "gpt-4o", 100.0))
	mock.ExpectQuery("COALESCE\\(c.source::text, 'unknown'\\)").
		WithArgs(sqlmock.AnyArg(), monthStart, dayEnd).
		WillReturnRows(sqlmock.NewRows(dimCols).AddRow("acc-1", "Acme", "user_chat", 100.0))
	mock.ExpectQuery("GROUP BY 1, 2$").
		WillReturnRows(sqlmock.NewRows(totalCols))

	mock.ExpectQuery("COALESCE\\(t.llm_model, 'unknown'\\)::text").
		WillReturnRows(sqlmock.NewRows(dimBreakdownCols))
	mock.ExpectQuery("COALESCE\\(c.source, 'unknown'\\)::text").
		WillReturnRows(sqlmock.NewRows(dimBreakdownCols).
			AddRow("Investigation", 80.0, 40, 2.5, 6.0).
			AddRow("Automation", 20.0, 10, 1.0, 1.5))

	report, err := dao.GetAiCostAccountReport([]string{"acc-1"}, referenceDate)
	require.NoError(t, err)
	require.Len(t, report.TopSources, 2)

	s0 := report.TopSources[0]
	assert.Equal(t, "Investigation", s0.Source)
	assert.Equal(t, 80.0, s0.MtdCostUsd)
	assert.Equal(t, int64(40), s0.CallCount)
	assert.Equal(t, 2.5, s0.P95CostPerCallUsd)
	assert.Equal(t, 6.0, s0.P99CostPerCallUsd)
	assert.Equal(t, "Automation", report.TopSources[1].Source)

	require.NoError(t, mock.ExpectationsWereMet())
}

// TestGetAiCostAccountReport_EmptyAccountIDs short-circuits without touching
// the DB — mirrors GetUsageMetrics' empty-scope guard.
func TestGetAiCostAccountReport_EmptyAccountIDs(t *testing.T) {
	dao := &ConversationDao{}
	report, err := dao.GetAiCostAccountReport(nil, time.Now())
	require.NoError(t, err)
	assert.Empty(t, report.Accounts)
}
