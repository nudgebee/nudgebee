package vmservicemap

import (
	"errors"
	"fmt"

	"nudgebee/services/common"
	"nudgebee/services/internal/database"
)

// Store is how the account's forager holds its metrics store: the datasource
// key the forager knows it by, and the relay agent type that serves it.
type Store struct {
	DatasourceKey string
	AgentType     string
}

// ErrNoStore means the account has no Prometheus integration reached through a
// forager, so there is nothing to build the map from.
var ErrNoStore = errors.New("vmservicemap: no Prometheus integration reached through a forager is configured for this account")

// ResolveStore finds the account's metrics store held by a forager: an enabled
// Prometheus integration linked to the account as its own, in
// connection_mode=vm_agent. Its datasource key is the row's own datasource_key,
// else the integration id: the same rule the metric-query loader and alert-rule
// listing use, since a user-created row only has the key when the relay wrote it.
// With several, the one marked as the default metrics provider wins; if that does
// not settle it, the map is not built from a guess.
func ResolveStore(accountID, tenantID string) (Store, error) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return Store{}, fmt.Errorf("vmservicemap: database manager: %w", err)
	}
	var rows []struct {
		DatasourceKey string `db:"datasource_key"`
		AgentType     string `db:"agent_type"`
		IsDefault     bool   `db:"is_default"`
	}
	err = dbms.Db.Select(&rows,
		`SELECT COALESCE(NULLIF(ck.value, ''), i.id::text) AS datasource_key,
		        COALESCE(at.value, '') AS agent_type,
		        COALESCE(l.default_metrics_provider, false) AS is_default
		 FROM integrations i
		 JOIN integrations_cloud_accounts l
		   ON l.integration_id = i.id AND l.cloud_account_id = $1 AND l.link_role = 'own'
		 JOIN integration_config_values mode
		   ON mode.integration_id = i.id AND mode.name = 'connection_mode' AND mode.value = 'vm_agent'
		 LEFT JOIN integration_config_values ck
		   ON ck.integration_id = i.id AND ck.name = 'datasource_key'
		 LEFT JOIN integration_config_values at
		   ON at.integration_id = i.id AND at.name = 'agent_type'
		 WHERE i.type = 'prometheus' AND i.status = 'enabled' AND i.tenant_id = $2
		 ORDER BY i.id`,
		accountID, tenantID)
	if err != nil {
		return Store{}, fmt.Errorf("vmservicemap: resolve metrics store for account %s: %w", accountID, err)
	}
	switch len(rows) {
	case 0:
		return Store{}, common.ErrorBadRequest(ErrNoStore.Error())
	case 1:
		return Store{DatasourceKey: rows[0].DatasourceKey, AgentType: rows[0].AgentType}, nil
	}
	var chosen []int
	for i, r := range rows {
		if r.IsDefault {
			chosen = append(chosen, i)
		}
	}
	if len(chosen) != 1 {
		return Store{}, common.ErrorBadRequest(fmt.Sprintf(
			"vmservicemap: %d Prometheus integrations are reached through a forager for this account and none is marked as the default metrics provider", len(rows)))
	}
	r := rows[chosen[0]]
	return Store{DatasourceKey: r.DatasourceKey, AgentType: r.AgentType}, nil
}
