package tools

import (
	"fmt"
	"log/slog"
	"time"

	"nudgebee/llm/common"
	"nudgebee/llm/tools/core"

	"github.com/lib/pq"
)

// ClusterConnection is whether a Kubernetes cluster's Nudgebee agent is
// connected, as recorded in the agent table.
type ClusterConnection struct {
	Connected bool
	LastSeen  *time.Time
}

// ClusterConnections looks up agent connection status for the given accounts.
// An account with several agent rows counts as connected when any of them is.
// Accounts with no agent row are absent from the result. It is a variable so
// tests can replace the database lookup.
var ClusterConnections = func(accountIDs []string) (map[string]ClusterConnection, error) {
	result := map[string]ClusterConnection{}
	if len(accountIDs) == 0 {
		return result, nil
	}
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, fmt.Errorf("cluster connections: %w", err)
	}
	rows, err := dbms.Db.Query(`SELECT cloud_account_id::text, bool_or(status = 'CONNECTED'), max(last_connected_at)
		FROM agent WHERE cloud_account_id::text = ANY($1::text[]) GROUP BY cloud_account_id`, pq.Array(accountIDs))
	if err != nil {
		return nil, fmt.Errorf("cluster connections: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id string
		var connected bool
		var lastSeen *time.Time
		if err := rows.Scan(&id, &connected, &lastSeen); err != nil {
			return nil, fmt.Errorf("cluster connections: %w", err)
		}
		result[id] = ClusterConnection{Connected: connected, LastSeen: lastSeen}
	}
	return result, rows.Err()
}

// ClusterConnectionsFor looks up the connection status of the given clusters,
// keyed by account id. A failed lookup returns an empty map, so callers show
// clusters without a status rather than failing.
func ClusterConnectionsFor(clusters []core.ToolConfig) map[string]ClusterConnection {
	ids := make([]string, 0, len(clusters))
	for _, c := range clusters {
		if id := KubernetesTargetAccountID(c); id != "" {
			ids = append(ids, id)
		}
	}
	connections, err := ClusterConnections(ids)
	if err != nil {
		slog.Warn("cluster connections: lookup failed; listing clusters without a status", "error", err)
		return map[string]ClusterConnection{}
	}
	return connections
}

// ClusterStatusColumns renders the STATUS and LAST-SEEN columns of the virtual
// `kubectl config get-contexts` table. Values contain no spaces, so the table
// stays splittable on whitespace.
func ClusterStatusColumns(connection ClusterConnection, known bool) (string, string) {
	if !known {
		return "Unknown", "-"
	}
	status := "Disconnected"
	if connection.Connected {
		status = "Connected"
	}
	lastSeen := "never"
	if connection.LastSeen != nil {
		lastSeen = connection.LastSeen.UTC().Format("2006-01-02T15:04Z")
	}
	return status, lastSeen
}
