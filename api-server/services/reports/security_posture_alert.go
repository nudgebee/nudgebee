package reports

import (
	"nudgebee/services/account"
	"nudgebee/services/common"
	"nudgebee/services/config"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
	"nudgebee/services/tenant"
	"time"

	"github.com/lib/pq"
)

// SendSecurityPostureAlert publishes one tenant-level security alert per the
// alert redesign: critical findings only (top-5 high fallback when there are
// none). Security recommendations are excluded from the FinOps digest/nudge,
// so this producer is the sole writer of last_nudged_at for Security rows —
// reminder windows: Critical 24h, High 7d.
func SendSecurityPostureAlert(ctx *security.RequestContext) error {
	startTime := time.Now()

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return err
	}

	tenants, err := tenant.ListTenantsWithActiveAccounts(ctx)
	if err != nil {
		ctx.GetLogger().Error("security posture alert: error listing tenants", "error", err)
		return err
	}

	for _, t := range tenants {
		accounts, err := account.ListActiveAccountsWithConnectedAgents(ctx, t.Id)
		if err != nil {
			ctx.GetLogger().Error("security posture alert: error listing accounts", "error", err, "tenant", t.Id)
			continue
		}
		if len(accounts) == 0 {
			continue
		}

		accountNameMap := make(map[string]string)
		for _, acc := range accounts {
			accountNameMap[acc.Id] = acc.AccountName
		}

		// True open counts by severity, independent of reminder windows and
		// caps, so the alert headline states the real totals.
		type severityCount struct {
			Severity string `db:"severity"`
			Cnt      int    `db:"cnt"`
		}
		var counts []severityCount
		err = dbms.Db.Select(&counts, `
			SELECT severity, COUNT(*) AS cnt
			FROM recommendation
			WHERE tenant_id = $1
			  AND category = 'Security'
			  AND status = 'Open'
			  AND severity IN ('Critical', 'High')
			GROUP BY severity`, t.Id)
		if err != nil {
			ctx.GetLogger().Error("security posture alert: error counting findings", "error", err, "tenant", t.Id)
			continue
		}
		var criticalCount, highCount int
		for _, c := range counts {
			switch c.Severity {
			case "Critical":
				criticalCount = c.Cnt
			case "High":
				highCount = c.Cnt
			}
		}
		if criticalCount == 0 && highCount == 0 {
			// All-clear lives in the daily digest, never as a standalone alert.
			continue
		}

		// Items due for notification: criticals first, then high. Rule families
		// capped at 3 rows so one CVE cohort can't fill the alert; per-kind
		// *_misconfigurations rules collapse into a single family for the cap.
		var recs []digestRecommendation
		err = dbms.Db.Select(&recs, `
			SELECT id, rule_name, resource_name, finops_score, finops_band,
				estimated_savings, severity, category, cloud_account_id, account_name, created_at
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
						PARTITION BY regexp_replace(r.rule_name, '^.+_misconfigurations$', 'misconfigurations')
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
			ORDER BY CASE severity WHEN 'Critical' THEN 0 ELSE 1 END, created_at DESC, id
			LIMIT 20`, t.Id)
		if err != nil {
			ctx.GetLogger().Error("security posture alert: error querying findings", "error", err, "tenant", t.Id)
			continue
		}
		if len(recs) == 0 {
			// Everything open is within its reminder window.
			continue
		}

		recsByAccount := make(map[string][]map[string]any)
		for _, rec := range recs {
			// No per-finding deep link exists on the security surface; link to
			// the account's security tab.
			ctaURL := config.Config.BaseUrl + "/kubernetes/details/" + rec.CloudAccountID +
				"?accountId=" + rec.CloudAccountID + "&utm=security-alert#security/image-scan"
			recMap := map[string]any{
				"id":                rec.ID,
				"rule_name":         rec.RuleName,
				"resource_name":     rec.ResourceName,
				"finops_score":      rec.FinOpsScore,
				"finops_band":       rec.FinOpsBand,
				"estimated_savings": rec.EstimatedSavings,
				"severity":          rec.Severity,
				"category":          rec.Category,
				"cta_url":           ctaURL,
			}
			accID := rec.CloudAccountID
			recsByAccount[accID] = append(recsByAccount[accID], recMap)
			if rec.AccountName != "" && rec.AccountName != accID {
				accountNameMap[accID] = rec.AccountName
			}
		}

		recommendationsByAccount := make(map[string]any)
		for accID, accRecs := range recsByAccount {
			name := accountNameMap[accID]
			if name == "" {
				name = accID
			}
			recommendationsByAccount[accID] = map[string]any{
				"account_name":    name,
				"recommendations": accRecs,
			}
		}

		message := map[string]any{
			"kind":      "notification",
			"type":      "security_posture_alert",
			"source":    "optimize", // "security" rule category arrives with the rules revisit
			"tenant_id": t.Id,
			"parameters": map[string]any{
				"organization_id":            t.Id,
				"organization_name":          t.Name,
				"critical_count":             criticalCount,
				"high_count":                 highCount,
				"recommendations_by_account": recommendationsByAccount,
				"base_url":                   config.Config.BaseUrl,
			},
		}

		err = common.MqPublish(config.Config.RabbitMqNotificationsExchange, config.Config.RabbitMqNotificationsQueue, message, common.MqPublishWithContext(ctx.GetContext()))
		if err != nil {
			ctx.GetLogger().Error("security posture alert: error publishing", "error", err, "tenant", t.Id)
			continue
		}

		recIDs := make([]string, len(recs))
		for i, rec := range recs {
			recIDs[i] = rec.ID
		}
		_, err = dbms.Db.Exec(`UPDATE recommendation SET last_nudged_at = now() WHERE id = ANY($1::uuid[])`, pq.Array(recIDs))
		if err != nil {
			ctx.GetLogger().Error("security posture alert: error updating last_nudged_at", "error", err, "tenant", t.Id)
		}
	}

	ctx.GetLogger().Info("security posture alert: complete", "duration", time.Since(startTime))
	return nil
}
