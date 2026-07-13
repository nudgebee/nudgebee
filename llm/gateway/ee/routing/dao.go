// Package routing is the EE DB-backed routing-rule loader: it reads per-tenant
// routing rules from the metastore so tenant rules can be managed in the DB and
// hot-reloaded, on top of the OSS static config-file rules. It is the DB
// implementation of the routing.LoaderFunc seam; the OSS build serves static rules
// only and never links this package. Registered via routing.RegisterRuleLoader in
// init().
package routing

import (
	"encoding/json"
	"log/slog"

	"nudgebee/llm-gateway/common"
	"nudgebee/llm-gateway/routing"
)

func init() {
	routing.RegisterRuleLoader(DBLoader)
}

// ruleRow is the DB shape; match/target are jsonb read as bytes.
type ruleRow struct {
	ID       string `db:"id"`
	TenantID string `db:"tenant_id"`
	Priority int    `db:"priority"`
	Enabled  bool   `db:"enabled"`
	Match    []byte `db:"match"`
	Target   []byte `db:"target"`
}

// DBLoader returns a LoaderFunc that reads enabled rules from the metastore. An
// individual invalid rule (e.g. cross-provider) is skipped with a warning rather
// than failing the whole load, so one bad tenant rule can't break routing for all.
func DBLoader() routing.LoaderFunc {
	return func() ([]routing.Rule, error) {
		db, err := common.GetDatabaseManager(common.Metastore)
		if err != nil {
			return nil, err
		}
		var rows []ruleRow
		if err := db.QueryAndScan(&rows,
			`SELECT id, tenant_id, priority, enabled, match, target
			   FROM llm_gateway_routing_rules WHERE enabled = true`); err != nil {
			return nil, err
		}
		rules := make([]routing.Rule, 0, len(rows))
		for _, r := range rows {
			rule := routing.Rule{ID: r.ID, TenantID: r.TenantID, Priority: r.Priority, Enabled: r.Enabled}
			if len(r.Match) > 0 {
				if err := json.Unmarshal(r.Match, &rule.Match); err != nil {
					slog.Warn("routing: skipping DB rule with bad match json", "id", r.ID, "error", err)
					continue
				}
			}
			if err := json.Unmarshal(r.Target, &rule.Target); err != nil {
				slog.Warn("routing: skipping DB rule with bad target json", "id", r.ID, "error", err)
				continue
			}
			if err := routing.Validate([]routing.Rule{rule}); err != nil {
				slog.Warn("routing: skipping invalid DB rule", "id", r.ID, "error", err)
				continue
			}
			rules = append(rules, rule)
		}
		return rules, nil
	}
}
