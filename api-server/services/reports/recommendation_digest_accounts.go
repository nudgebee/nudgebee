package reports

import (
	"encoding/json"
	"nudgebee/services/config"
	"nudgebee/services/notification"
	"nudgebee/services/recommendation"
	"time"

	"github.com/jmoiron/sqlx"
)

// digestAccountLimit caps one account's list, as the tenant list is capped.
const digestAccountLimit = 20

func digestRecMap(rec digestRecommendation, digestDate string) map[string]any {
	// Platform-neutral utm=digest — the formatter doesn't know which
	// channel(s) this payload will be rendered for. Footer-button URLs
	// are appended per platform in the notifications-server formatters
	// where the platform IS known (utm=slack-digest, teams-digest, etc.).
	ctaURL := config.Config.BaseUrl + "/optimise?id=" + rec.ID + "&utm=digest&d=" + digestDate + "#cost"
	recMap := map[string]any{
		"id":                    rec.ID,
		"rule_name":             rec.RuleName,
		"resource_name":         rec.ResourceName,
		"finops_score":          rec.FinOpsScore,
		"finops_band":           rec.FinOpsBand,
		"estimated_savings":     rec.EstimatedSavings,
		"severity":              rec.Severity,
		"category":              rec.Category,
		"cta_url":               ctaURL,
		"wasted_since_detected": recommendation.WastedSinceDetected(rec.EstimatedSavings, rec.CreatedAt, time.Now().UTC()),
	}
	if len(rec.RecommendationRaw) > 0 {
		recMap["recommendation"] = json.RawMessage(rec.RecommendationRaw)
	}
	return recMap
}

// queryDigestAccountRecommendations selects each account's digest the way the
// tenant digest is selected — same filters, order and rule-family cap — but
// within the account, up to digestAccountLimit rows per account.
func queryDigestAccountRecommendations(db *sqlx.DB, tenantID string) ([]digestRecommendation, error) {
	var recs []digestRecommendation
	err := db.Select(&recs, `
		SELECT id, rule_name, resource_name, finops_score, finops_band,
			estimated_savings, severity, category, cloud_account_id, account_name, created_at, recommendation
		FROM (
			SELECT *,
				ROW_NUMBER() OVER (
					PARTITION BY cloud_account_id
					ORDER BY finops_score DESC, estimated_savings DESC NULLS LAST, id
				) AS account_rank
			FROM (
				SELECT r.id, r.rule_name,
					COALESCE(r.account_object_id, r.id::varchar) AS resource_name,
					COALESCE(r.finops_score, 0) AS finops_score,
					COALESCE(r.finops_band, 'Low') AS finops_band,
					r.estimated_savings,
					COALESCE(r.severity, 'Medium') AS severity,
					r.category,
					r.cloud_account_id::varchar AS cloud_account_id,
					COALESCE(ca.account_name, r.cloud_account_id::varchar) AS account_name,
					r.created_at,
					r.recommendation,
					ROW_NUMBER() OVER (
						PARTITION BY r.cloud_account_id, regexp_replace(r.rule_name, '^.+_misconfigurations$', 'misconfigurations')
						ORDER BY r.finops_score DESC NULLS LAST, r.estimated_savings DESC NULLS LAST, r.created_at DESC, r.id
					) AS rule_rank
				FROM recommendation r
				LEFT JOIN cloud_accounts ca ON ca.id = r.cloud_account_id
				WHERE r.tenant_id = $1
					AND `+notification.NotOwnRecommendationSQL+`
					AND r.status = 'Open'
					AND r.category <> 'Security'
					AND r.finops_band IN ('Act Now', 'Critical', 'High')
					AND (
						r.last_nudged_at IS NULL
						OR (r.finops_band = 'Act Now' AND r.last_nudged_at < now() - interval '24 hours')
						OR (r.finops_band = 'Critical' AND r.last_nudged_at < now() - interval '7 days')
						OR (r.finops_band = 'High' AND r.last_nudged_at < now() - interval '30 days')
					)
			) ranked
			WHERE rule_rank <= 3
		) per_account
		WHERE account_rank <= $2
		ORDER BY cloud_account_id, finops_score DESC, estimated_savings DESC NULLS LAST, id`, tenantID, digestAccountLimit)
	return recs, err
}

// digestDeltas is one account's header numbers; see queryDigestDeltas.
type digestDeltas struct {
	NewCounts       map[string]int
	ResolvedCount   int
	ResolvedSavings float64
	CarryoverCount  int
	CategorySavings map[string]float64
}

func newDigestDeltas() *digestDeltas {
	return &digestDeltas{
		NewCounts:       map[string]int{"act_now": 0, "critical": 0, "high": 0},
		CategorySavings: map[string]float64{},
	}
}

// queryDigestDeltasByAccount computes queryDigestDeltas' rollups and the
// category savings per account, so the numbers a channel shows are the sum over
// its accounts.
func queryDigestDeltasByAccount(db *sqlx.DB, tenantID string, cutoff time.Time) (map[string]*digestDeltas, error) {
	deltas := map[string]*digestDeltas{}
	get := func(accountID string) *digestDeltas {
		if deltas[accountID] == nil {
			deltas[accountID] = newDigestDeltas()
		}
		return deltas[accountID]
	}

	var newRows []struct {
		AccountID string `db:"account_id"`
		Band      string `db:"band"`
		Cnt       int    `db:"cnt"`
	}
	err := db.Select(&newRows, `
		SELECT COALESCE(cloud_account_id::text, '') AS account_id, finops_band AS band, COUNT(*) AS cnt
		FROM recommendation r
		WHERE tenant_id = $1
		  AND `+notification.NotOwnRecommendationSQL+`
		  AND status = 'Open'
		  AND category <> 'Security'
		  AND created_at > $2
		  AND finops_band IN ('Act Now', 'Critical', 'High')
		GROUP BY 1, 2`, tenantID, cutoff)
	if err != nil {
		return nil, err
	}
	bandKeys := map[string]string{"Act Now": "act_now", "Critical": "critical", "High": "high"}
	for _, row := range newRows {
		get(row.AccountID).NewCounts[bandKeys[row.Band]] = row.Cnt
	}

	var resolvedRows []struct {
		AccountID string  `db:"account_id"`
		Cnt       int     `db:"cnt"`
		Savings   float64 `db:"savings"`
	}
	err = db.Select(&resolvedRows, `
		SELECT COALESCE(cloud_account_id::text, '') AS account_id, COUNT(*) AS cnt,
			COALESCE(SUM(estimated_savings), 0) AS savings
		FROM recommendation r
		WHERE tenant_id = $1
		  AND `+notification.NotOwnRecommendationSQL+`
		  AND status IN ('Closed', 'Dismissed')
		  AND category <> 'Security'
		  AND updated_at > $2
		GROUP BY 1`, tenantID, cutoff)
	if err != nil {
		return nil, err
	}
	for _, row := range resolvedRows {
		d := get(row.AccountID)
		d.ResolvedCount, d.ResolvedSavings = row.Cnt, row.Savings
	}

	var carryoverRows []struct {
		AccountID string `db:"account_id"`
		Cnt       int    `db:"cnt"`
	}
	err = db.Select(&carryoverRows, `
		SELECT COALESCE(cloud_account_id::text, '') AS account_id, COUNT(*) AS cnt
		FROM recommendation r
		WHERE tenant_id = $1
		  AND `+notification.NotOwnRecommendationSQL+`
		  AND status = 'Open'
		  AND category <> 'Security'
		  AND created_at <= $2
		GROUP BY 1`, tenantID, cutoff)
	if err != nil {
		return nil, err
	}
	for _, row := range carryoverRows {
		get(row.AccountID).CarryoverCount = row.Cnt
	}

	var categoryRows []struct {
		AccountID string  `db:"account_id"`
		Category  string  `db:"category"`
		Savings   float64 `db:"savings"`
	}
	err = db.Select(&categoryRows, `
		SELECT COALESCE(cloud_account_id::text, '') AS account_id, category,
			COALESCE(SUM(estimated_savings), 0) AS savings
		FROM recommendation r
		WHERE tenant_id = $1
		  AND `+notification.NotOwnRecommendationSQL+`
		  AND status = 'Open'
		  AND category <> 'Security'
		  AND estimated_savings > 0
		GROUP BY 1, 2`, tenantID)
	if err != nil {
		return nil, err
	}
	for _, row := range categoryRows {
		get(row.AccountID).CategorySavings[row.Category] = row.Savings
	}
	return deltas, nil
}

// buildDigestAccountSections is the payload's `accounts`: for every account
// with recommendations or header numbers, its name, its recommendations and its
// totals. An account with nothing to send still counts in its channel's header.
func buildDigestAccountSections(
	recs []digestRecommendation,
	deltas map[string]*digestDeltas,
	names map[string]string,
	digestDate string,
) map[string]any {
	sections := map[string]any{}
	byAccount := map[string][]digestRecommendation{}
	for _, rec := range recs {
		byAccount[rec.CloudAccountID] = append(byAccount[rec.CloudAccountID], rec)
	}
	for accountID := range deltas {
		if _, ok := byAccount[accountID]; !ok {
			byAccount[accountID] = nil
		}
	}
	for accountID, accountRecs := range byAccount {
		var savings float64
		var actNow, critical, high int
		recMaps := make([]map[string]any, 0, len(accountRecs))
		for _, rec := range accountRecs {
			savings += rec.EstimatedSavings
			switch rec.FinOpsBand {
			case "Act Now":
				actNow++
			case "Critical":
				critical++
			case "High":
				high++
			}
			recMaps = append(recMaps, digestRecMap(rec, digestDate))
		}
		d := deltas[accountID]
		if d == nil {
			d = newDigestDeltas()
		}
		sections[accountID] = map[string]any{
			"account_name":    accountSectionName(accountID, accountRecs, names),
			"recommendations": recMaps,
			"totals": map[string]any{
				"total_recoverable_savings": savings,
				"act_now_count":             actNow,
				"critical_count":            critical,
				"high_count":                high,
				"new_counts":                d.NewCounts,
				"resolved_count":            d.ResolvedCount,
				"resolved_savings":          d.ResolvedSavings,
				"carryover_count":           d.CarryoverCount,
				"category_savings":          d.CategorySavings,
			},
		}
	}
	return sections
}

func accountSectionName(accountID string, recs []digestRecommendation, names map[string]string) string {
	if name := names[accountID]; name != "" {
		return name
	}
	if len(recs) > 0 {
		return recs[0].AccountName
	}
	return accountID
}

// digestRecommendationIDs lists every recommendation in any of the lists, once.
func digestRecommendationIDs(lists ...[]digestRecommendation) []string {
	seen := map[string]bool{}
	ids := []string{}
	for _, list := range lists {
		for _, rec := range list {
			if !seen[rec.ID] {
				seen[rec.ID] = true
				ids = append(ids, rec.ID)
			}
		}
	}
	return ids
}
