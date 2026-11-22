package tasks

import (
	"context"
	"fmt"
	"strings"

	"nudgebee/runbook/common"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

func enabledConfig(params map[string]any, key string) (map[string]any, bool) {
	cfg, ok := params[key].(map[string]any)
	if !ok {
		return nil, false
	}
	enabled, _ := cfg["enabled"].(bool)
	return cfg, enabled
}

func stringParam(params map[string]any, key string) string {
	s, _ := params[key].(string)
	return strings.TrimSpace(s)
}

// integrationTypes is the stored types ref may resolve to in the tenant: the
// type of the integration with that id (whatever its type, so a grant for the
// type it really has is needed), or every type, among accepted, of an enabled
// integration with that name, as a name can match more than one. Name lookup
// matches the tasks' own (integrations.ResolveIntegrationID). A variable so
// tests need no metastore.
var integrationTypes = func(ctx context.Context, tenantID, ref string, accepted []string) ([]string, error) {
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, err
	}
	var query string
	args := []any{tenantID, ref}
	if _, err := uuid.Parse(ref); err == nil {
		query = `SELECT type FROM integrations WHERE tenant_id = $1 AND id = $2::uuid`
	} else {
		query = `SELECT DISTINCT type FROM integrations
			WHERE tenant_id = $1 AND LOWER(name) = LOWER($2) AND status = 'enabled'
			  AND (COALESCE(cardinality($3::text[]), 0) = 0 OR type = ANY($3))`
		args = append(args, pq.Array(accepted))
	}
	rows, err := dbManager.Db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var found []string
	for rows.Next() {
		var typ string
		if err := rows.Scan(&typ); err != nil {
			return nil, err
		}
		found = append(found, typ)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(found) == 0 {
		return nil, fmt.Errorf("%w: %q", ErrIntegrationNotFound, ref)
	}
	return found, nil
}
