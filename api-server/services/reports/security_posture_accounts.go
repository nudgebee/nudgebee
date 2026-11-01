package reports

import (
	"nudgebee/services/account"
	"nudgebee/services/config"

	"github.com/jmoiron/sqlx"
)

// securityPostureAccountLimit caps one account's list, as the tenant list is capped.
const securityPostureAccountLimit = 20

// securityFindingURL is the one place that maps a security finding's account
// to a real destination. Only kubernetes accounts have a per-account security
// page; vm and cloud accounts have no such page, so they land on the
// cross-account /optimise Security tab's matching sub-tab instead (unscoped —
// there's no accountId param for it to read).
func securityFindingURL(accountID, accountType string) string {
	base := config.Config.BaseUrl
	switch accountType {
	case account.AccountTypeVM:
		return base + "/optimise?utm=security-alert#security/vm-vulnerabilities"
	case "cloud":
		return base + "/optimise?utm=security-alert#security/cloud-posture"
	default: // "kubernetes" or unset
		return base + "/kubernetes/details/" + accountID +
			"?accountId=" + accountID + "&utm=security-alert#security/image-scan"
	}
}

func securityPostureRecMap(rec digestRecommendation, accountType string) map[string]any {
	return map[string]any{
		"id":                rec.ID,
		"rule_name":         rec.RuleName,
		"resource_name":     rec.ResourceName,
		"finops_score":      rec.FinOpsScore,
		"finops_band":       rec.FinOpsBand,
		"estimated_savings": rec.EstimatedSavings,
		"severity":          rec.Severity,
		"category":          rec.Category,
		"cta_url":           securityFindingURL(rec.CloudAccountID, accountType),
	}
}

// querySecurityPostureAccountFindings selects each account's due findings the
// way the tenant alert selects them — same filters, order and rule-family cap —
// but within the account, up to securityPostureAccountLimit rows per account.
func querySecurityPostureAccountFindings(db *sqlx.DB, tenantID string) ([]digestRecommendation, error) {
	var recs []digestRecommendation
	err := db.Select(&recs, `
		SELECT id, rule_name, resource_name, finops_score, finops_band,
			estimated_savings, severity, category, cloud_account_id, account_name, created_at
		FROM (
			SELECT *,
				ROW_NUMBER() OVER (
					PARTITION BY cloud_account_id
					ORDER BY CASE severity WHEN 'Critical' THEN 0 ELSE 1 END, created_at DESC, id
				) AS account_rank
			FROM (
				SELECT r.id, r.rule_name,
					COALESCE(r.account_object_id, r.id::varchar) AS resource_name,
					COALESCE(r.finops_score, 0) AS finops_score,
					COALESCE(r.finops_band, 'Low') AS finops_band,
					COALESCE(r.estimated_savings, 0) AS estimated_savings,
					r.severity,
					r.category,
					r.cloud_account_id::varchar AS cloud_account_id,
					COALESCE(ca.account_name, r.cloud_account_id::varchar) AS account_name,
					r.created_at,
					ROW_NUMBER() OVER (
						PARTITION BY r.cloud_account_id, regexp_replace(r.rule_name, '^.+_misconfigurations$', 'misconfigurations')
						ORDER BY CASE r.severity WHEN 'Critical' THEN 0 ELSE 1 END, r.created_at DESC, r.id
					) AS rule_rank
				FROM recommendation r
				LEFT JOIN cloud_accounts ca ON ca.id = r.cloud_account_id
				WHERE r.tenant_id = $1
					AND r.category = 'Security'
					AND r.status = 'Open'
					AND r.severity IN ('Critical', 'High')
					AND (
						r.last_nudged_at IS NULL
						OR (r.severity = 'Critical' AND r.last_nudged_at < now() - interval '24 hours')
						OR (r.severity = 'High' AND r.last_nudged_at < now() - interval '7 days')
					)
			) ranked
			WHERE rule_rank <= 3
		) per_account
		WHERE account_rank <= $2
		ORDER BY cloud_account_id, CASE severity WHEN 'Critical' THEN 0 ELSE 1 END, created_at DESC, id`,
		tenantID, securityPostureAccountLimit)
	return recs, err
}

// securityPostureCounts is one account's open Critical and High findings,
// independent of reminder windows and caps.
type securityPostureCounts struct {
	Critical int
	High     int
}

func querySecurityPostureCountsByAccount(db *sqlx.DB, tenantID string) (map[string]securityPostureCounts, error) {
	var rows []struct {
		AccountID string `db:"account_id"`
		Severity  string `db:"severity"`
		Cnt       int    `db:"cnt"`
	}
	err := db.Select(&rows, `
		SELECT COALESCE(cloud_account_id::text, '') AS account_id, severity, COUNT(*) AS cnt
		FROM recommendation
		WHERE tenant_id = $1
		  AND category = 'Security'
		  AND status = 'Open'
		  AND severity IN ('Critical', 'High')
		GROUP BY 1, 2`, tenantID)
	if err != nil {
		return nil, err
	}
	counts := map[string]securityPostureCounts{}
	for _, row := range rows {
		c := counts[row.AccountID]
		if row.Severity == "Critical" {
			c.Critical = row.Cnt
		} else {
			c.High = row.Cnt
		}
		counts[row.AccountID] = c
	}
	return counts, nil
}

// buildSecurityPostureAccountSections is the payload's `accounts`: for every
// account with due or open findings, its name, its due findings and its open
// counts. An account with nothing due still counts in its channel's header.
func buildSecurityPostureAccountSections(
	recs []digestRecommendation,
	counts map[string]securityPostureCounts,
	names map[string]string,
	types map[string]string,
) map[string]any {
	byAccount := map[string][]digestRecommendation{}
	for _, rec := range recs {
		byAccount[rec.CloudAccountID] = append(byAccount[rec.CloudAccountID], rec)
	}
	for accountID := range counts {
		if _, ok := byAccount[accountID]; !ok {
			byAccount[accountID] = nil
		}
	}
	sections := map[string]any{}
	for accountID, accountRecs := range byAccount {
		recMaps := make([]map[string]any, 0, len(accountRecs))
		for _, rec := range accountRecs {
			recMaps = append(recMaps, securityPostureRecMap(rec, types[accountID]))
		}
		c := counts[accountID]
		sections[accountID] = map[string]any{
			"account_name":    accountSectionName(accountID, accountRecs, names),
			"recommendations": recMaps,
			"totals":          map[string]any{"critical_count": c.Critical, "high_count": c.High},
		}
	}
	return sections
}
