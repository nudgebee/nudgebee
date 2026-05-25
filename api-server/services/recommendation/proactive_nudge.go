package recommendation

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

// proactiveRec holds the fields returned by the proactive nudge query.
type proactiveRec struct {
	ID               string  `db:"id"`
	RuleName         string  `db:"rule_name"`
	ResourceName     string  `db:"resource_name"`
	FinOpsScore      int     `db:"finops_score"`
	FinOpsBand       string  `db:"finops_band"`
	EstimatedSavings float64 `db:"estimated_savings"`
	Severity         string  `db:"severity"`
	Category         string  `db:"category"`
	CloudAccountID   string  `db:"cloud_account_id"`
	AccountName      string  `db:"account_name"`
}

// ProcessProactiveNudges scans for "Act Now" recommendations that haven't been
// nudged within the cooldown window and publishes a single bundled notification
// per tenant. Called hourly by the recommendation-proactive-nudge cron.
func ProcessProactiveNudges(ctx *security.RequestContext) error {
	t0 := time.Now()

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return err
	}

	tenants, err := tenant.ListTenantsWithActiveAccounts(ctx)
	if err != nil {
		ctx.GetLogger().Error("proactive nudge: error listing tenants", "error", err)
		return err
	}

	totalPublished := 0

	for _, t := range tenants {
		accounts, err := account.ListActiveAccountsWithConnectedAgents(ctx, t.Id)
		if err != nil {
			ctx.GetLogger().Error("proactive nudge: error listing accounts", "error", err, "tenant", t.Id)
			continue
		}
		if len(accounts) == 0 {
			continue
		}

		accountNameMap := make(map[string]string)
		for _, acc := range accounts {
			accountNameMap[acc.Id] = acc.AccountName
		}

		var recs []proactiveRec
		err = dbms.Db.Select(&recs, `
			SELECT r.id, r.rule_name,
				COALESCE(r.account_object_id, r.id::varchar) AS resource_name,
				COALESCE(r.finops_score, 0) AS finops_score,
				COALESCE(r.finops_band, 'Low') AS finops_band,
				r.estimated_savings,
				COALESCE(r.severity, 'Medium') AS severity,
				r.category,
				r.cloud_account_id::varchar AS cloud_account_id,
				COALESCE(ca.account_name, r.cloud_account_id::varchar) AS account_name
			FROM recommendation r
			LEFT JOIN cloud_accounts ca ON ca.id = r.cloud_account_id
			WHERE r.tenant_id = $1
				AND r.status = 'Open'
				AND r.finops_band = 'Act Now'
				AND (r.last_nudged_at IS NULL OR r.last_nudged_at < now() - interval '24 hours')
			ORDER BY r.finops_score DESC NULLS LAST
			LIMIT 10`, t.Id)
		if err != nil {
			ctx.GetLogger().Error("proactive nudge: error querying recommendations", "error", err, "tenant", t.Id)
			continue
		}

		if len(recs) == 0 {
			continue
		}

		// Aggregate savings and group by account
		var totalSavings float64
		recsByAccount := make(map[string][]map[string]any)

		for _, rec := range recs {
			totalSavings += rec.EstimatedSavings

			ctaURL := config.Config.BaseUrl + "/optimise?id=" + rec.ID + "#summary"
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
		}

		// Build recommendations_by_account with account names
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
			"type":      "recommendation_proactive_nudge",
			"source":    "optimize",
			"tenant_id": t.Id,
			"parameters": map[string]any{
				"organization_id":            t.Id,
				"organization_name":          t.Name,
				"total_recommendations":      len(recs),
				"total_recoverable_savings":  totalSavings,
				"recommendations_by_account": recommendationsByAccount,
				"base_url":                   config.Config.BaseUrl,
			},
		}

		err = common.MqPublish(config.Config.RabbitMqNotificationsExchange, config.Config.RabbitMqNotificationsQueue, message)
		if err != nil {
			ctx.GetLogger().Error("proactive nudge: error publishing", "error", err, "tenant", t.Id)
			continue
		}

		// Update last_nudged_at for all included recommendations
		recIDs := make([]string, len(recs))
		for i, rec := range recs {
			recIDs[i] = rec.ID
		}
		_, err = dbms.Db.Exec(`UPDATE recommendation SET last_nudged_at = now() WHERE id = ANY($1::uuid[])`, pq.Array(recIDs))
		if err != nil {
			ctx.GetLogger().Error("proactive nudge: error updating last_nudged_at", "error", err, "tenant", t.Id)
		}

		totalPublished += len(recs)
	}

	ctx.GetLogger().Info("proactive nudge: complete", "published", totalPublished, "duration", time.Since(t0))
	return nil
}
