package recommendation

import (
	"encoding/json"
	"nudgebee/services/config"
	"nudgebee/services/notification"
	"time"

	"github.com/jmoiron/sqlx"
)

// proactiveAccountLimit caps one account's list, as the tenant list is capped.
const proactiveAccountLimit = 10

func proactiveRecMap(rec proactiveRec) map[string]any {
	ctaURL := config.Config.BaseUrl + "/optimise?id=" + rec.ID + "#cost"
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
		"wasted_since_detected": WastedSinceDetected(rec.EstimatedSavings, rec.CreatedAt, time.Now().UTC()),
	}
	if len(rec.RecommendationRaw) > 0 {
		recMap["recommendation"] = json.RawMessage(rec.RecommendationRaw)
	}
	return recMap
}

// queryProactiveAccountRecommendations selects each account's nudge the way the
// tenant nudge is selected — same filters, order and rule-family cap — but
// within the account, up to proactiveAccountLimit rows per account.
func queryProactiveAccountRecommendations(db *sqlx.DB, tenantID string) ([]proactiveRec, error) {
	var recs []proactiveRec
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
					AND r.finops_band = 'Act Now'
					AND (r.last_nudged_at IS NULL OR r.last_nudged_at < now() - interval '24 hours')
			) ranked
			WHERE rule_rank <= 3
		) per_account
		WHERE account_rank <= $2
		ORDER BY cloud_account_id, finops_score DESC, estimated_savings DESC NULLS LAST, id`, tenantID, proactiveAccountLimit)
	return recs, err
}

// buildProactiveAccountSections is the payload's `accounts`: for every account
// with recommendations, its name, its recommendations and its totals.
func buildProactiveAccountSections(recs []proactiveRec, names map[string]string) map[string]any {
	byAccount := map[string][]proactiveRec{}
	for _, rec := range recs {
		byAccount[rec.CloudAccountID] = append(byAccount[rec.CloudAccountID], rec)
	}
	sections := map[string]any{}
	for accountID, accountRecs := range byAccount {
		var savings float64
		recMaps := make([]map[string]any, 0, len(accountRecs))
		for _, rec := range accountRecs {
			savings += rec.EstimatedSavings
			recMaps = append(recMaps, proactiveRecMap(rec))
		}
		name := names[accountID]
		if name == "" {
			name = accountRecs[0].AccountName
		}
		sections[accountID] = map[string]any{
			"account_name":    name,
			"recommendations": recMaps,
			"totals": map[string]any{
				"total_recommendations":     len(accountRecs),
				"total_recoverable_savings": savings,
			},
		}
	}
	return sections
}

// proactiveRecIDs lists every recommendation in any of the lists, once.
func proactiveRecIDs(lists ...[]proactiveRec) []string {
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
