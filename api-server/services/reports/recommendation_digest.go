package reports

import (
	"nudgebee/services/account"
	"nudgebee/services/common"
	"nudgebee/services/config"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
	"nudgebee/services/tenant"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

type digestRecommendation struct {
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

// SendRecommendationNudgeDigest queries top recommendations by finops_score
// and publishes a digest message per tenant to RabbitMQ for notification delivery.
func SendRecommendationNudgeDigest(ctx *security.RequestContext) error {
	startTime := time.Now()

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return err
	}

	tenants, err := tenant.ListTenantsWithActiveAccounts(ctx)
	if err != nil {
		ctx.GetLogger().Error("recommendation digest: error listing tenants", "error", err)
		return err
	}

	for _, t := range tenants {
		accounts, err := account.ListActiveAccountsWithConnectedAgents(ctx, t.Id)
		if err != nil {
			ctx.GetLogger().Error("recommendation digest: error listing accounts", "error", err, "tenant", t.Id)
			continue
		}
		if len(accounts) == 0 {
			continue
		}

		accountIDs := make([]string, len(accounts))
		accountNameMap := make(map[string]string)
		for i, acc := range accounts {
			accountIDs[i] = acc.Id
			accountNameMap[acc.Id] = acc.AccountName
		}

		// Query top recommendations by finops_score
		var recs []digestRecommendation
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
				AND r.finops_band IN ('Act Now', 'Critical', 'High')
				AND (
					r.last_nudged_at IS NULL
					OR (r.finops_band = 'Act Now' AND r.last_nudged_at < now() - interval '24 hours')
					OR (r.finops_band = 'Critical' AND r.last_nudged_at < now() - interval '7 days')
					OR (r.finops_band = 'High' AND r.last_nudged_at < now() - interval '30 days')
				)
			ORDER BY r.finops_score DESC NULLS LAST
			LIMIT 20`, t.Id)
		if err != nil {
			ctx.GetLogger().Error("recommendation digest: error querying recommendations", "error", err, "tenant", t.Id)
			continue
		}

		if len(recs) == 0 {
			ctx.GetLogger().Debug("recommendation digest: no high-priority recommendations", "tenant", t.Id)
			continue
		}

		// Header deltas: global tenant aggregates over a rolling 24h window,
		// independent of the top-20 digest body. KNOWN GAP: NEW counts use
		// created_at, so resurfaced recs (status flipped Closed→Open via the
		// scanner UPSERT, which preserves created_at — see
		// scan_orchestrator/persist.go) land in carryover rather than NEW. The
		// alternative requires a last_status_change_at column; deferred.
		// Single cutoff for all three delta queries — anchored in Go so the
		// three statements see a consistent "since" boundary even though they
		// run sequentially without a shared transaction.
		cutoff := time.Now().UTC().Add(-24 * time.Hour)
		newCounts, resolvedCount, resolvedSavings, carryoverCount, err := queryDigestDeltas(dbms.Db, t.Id, cutoff)
		if err != nil {
			ctx.GetLogger().Error("recommendation digest: error querying deltas", "error", err, "tenant", t.Id)
			continue
		}

		digestDate := time.Now().UTC().Format("2006-01-02")

		// Aggregate counts and savings
		var totalSavings float64
		var actNowCount, criticalCount, highCount int
		recsByAccount := make(map[string][]map[string]any)

		for _, rec := range recs {
			totalSavings += rec.EstimatedSavings
			switch rec.FinOpsBand {
			case "Act Now":
				actNowCount++
			case "Critical":
				criticalCount++
			case "High":
				highCount++
			}

			// Platform-neutral utm=digest — the formatter doesn't know which
			// channel(s) this payload will be rendered for. Footer-button URLs
			// are appended per platform in the notifications-server formatters
			// where the platform IS known (utm=slack-digest, teams-digest, etc.).
			ctaURL := config.Config.BaseUrl + "/optimise?id=" + rec.ID + "&utm=digest&d=" + digestDate + "#summary"
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
			"type":      "recommendation_nudge_digest",
			"source":    "optimize",
			"tenant_id": t.Id,
			"parameters": map[string]any{
				"organization_id":            t.Id,
				"organization_name":          t.Name,
				"title":                      "FinOps Daily Brief",
				"total_recoverable_savings":  totalSavings,
				"act_now_count":              actNowCount,
				"critical_count":             criticalCount,
				"high_count":                 highCount,
				"recommendations_by_account": recommendationsByAccount,
				"base_url":                   config.Config.BaseUrl,
				"new_counts":                 newCounts,
				"resolved_count":             resolvedCount,
				"resolved_savings":           resolvedSavings,
				"carryover_count":            carryoverCount,
				"delta_window_hours":         24,
				"digest_date":                digestDate,
			},
		}

		ctx.GetLogger().Info("recommendation digest: publishing",
			"tenant", t.Id,
			"total_recs", len(recs),
			"total_savings", totalSavings)

		err = common.MqPublish(config.Config.RabbitMqNotificationsExchange, config.Config.RabbitMqNotificationsQueue, message)
		if err != nil {
			ctx.GetLogger().Error("recommendation digest: error publishing", "error", err, "tenant", t.Id)
			continue
		}

		// Update last_nudged_at for all included recommendations
		recIDs := make([]string, len(recs))
		for i, rec := range recs {
			recIDs[i] = rec.ID
		}
		_, err = dbms.Db.Exec(`UPDATE recommendation SET last_nudged_at = now() WHERE id = ANY($1::uuid[])`, pq.Array(recIDs))
		if err != nil {
			ctx.GetLogger().Error("recommendation digest: error updating last_nudged_at", "error", err, "tenant", t.Id)
		}
	}

	ctx.GetLogger().Info("recommendation digest: complete", "duration", time.Since(startTime))
	return nil
}

// queryDigestDeltas computes the three header rollups for the daily digest:
// NEW counts by band (Open recs with created_at > cutoff), resolved count +
// savings (Closed/Dismissed with updated_at > cutoff), and carryover (all
// still-Open recs older than the cutoff — unlimited, this is the recipient's
// real backlog size, not the top-20 body's band distribution that today's
// header lies about). The caller supplies cutoff so all three statements
// share one consistent "since" boundary.
func queryDigestDeltas(db *sqlx.DB, tenantID string, cutoff time.Time) (map[string]int, int, float64, int, error) {
	type bandCount struct {
		Band string `db:"band"`
		Cnt  int    `db:"cnt"`
	}
	// `finops_band IN (...)` excludes NULL natively, so no COALESCE in the
	// WHERE clause — leaving it there blocks the finops_band index.
	var newRows []bandCount
	err := db.Select(&newRows, `
		SELECT
			finops_band AS band,
			COUNT(*) AS cnt
		FROM recommendation
		WHERE tenant_id = $1
		  AND status = 'Open'
		  AND created_at > $2
		  AND finops_band IN ('Act Now', 'Critical', 'High')
		GROUP BY finops_band`, tenantID, cutoff)
	if err != nil {
		return nil, 0, 0, 0, err
	}
	newCounts := map[string]int{"act_now": 0, "critical": 0, "high": 0}
	for _, r := range newRows {
		switch r.Band {
		case "Act Now":
			newCounts["act_now"] = r.Cnt
		case "Critical":
			newCounts["critical"] = r.Cnt
		case "High":
			newCounts["high"] = r.Cnt
		}
	}

	// 'Archive' (auto-archival when scanner stops emitting a rule) intentionally
	// excluded so we don't conflate scanner churn with user-driven resolutions.
	type resolvedAgg struct {
		Cnt     int     `db:"cnt"`
		Savings float64 `db:"savings"`
	}
	var resolved resolvedAgg
	err = db.Get(&resolved, `
		SELECT
			COUNT(*) AS cnt,
			COALESCE(SUM(estimated_savings), 0) AS savings
		FROM recommendation
		WHERE tenant_id = $1
		  AND status IN ('Closed', 'Dismissed')
		  AND updated_at > $2`, tenantID, cutoff)
	if err != nil {
		return nil, 0, 0, 0, err
	}

	var carryover int
	err = db.Get(&carryover, `
		SELECT COUNT(*)
		FROM recommendation
		WHERE tenant_id = $1
		  AND status = 'Open'
		  AND created_at <= $2`, tenantID, cutoff)
	if err != nil {
		return nil, 0, 0, 0, err
	}

	return newCounts, resolved.Cnt, resolved.Savings, carryover, nil
}
