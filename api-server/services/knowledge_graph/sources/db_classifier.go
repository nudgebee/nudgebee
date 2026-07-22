package sources

import (
	"fmt"
	"regexp"
	"strings"

	"nudgebee/services/internal/database"
)

// In-cluster datastores (databases, caches, message queues) run as ordinary K8s
// Deployments/StatefulSets, so the K8s source types them as Workload. This file
// classifies which of those workloads are actually datastores and what engine/role
// they play, so the node can carry a database "facet" (properties.role + engine)
// WITHOUT changing node_type — the workload stays a Workload (keeping its topology,
// scaling, and edges) and merely gains the role it plays. See the KG service guide.

// Datastore roles a workload can play. Empty = not a datastore.
const (
	roleDatabase     = "database"
	roleCache        = "cache"
	roleMessageQueue = "messagequeue"
)

// engineRole maps a canonical engine name to its datastore role. etcd/zookeeper are
// intentionally absent — they are coordination/KV stores we leave unclassified for now.
var engineRole = map[string]string{
	// databases
	"postgres":      roleDatabase,
	"mysql":         roleDatabase,
	"mariadb":       roleDatabase,
	"mongodb":       roleDatabase,
	"clickhouse":    roleDatabase,
	"cassandra":     roleDatabase,
	"scylla":        roleDatabase,
	"cockroach":     roleDatabase,
	"elasticsearch": roleDatabase,
	"opensearch":    roleDatabase,
	"influxdb":      roleDatabase,
	"neo4j":         roleDatabase,
	"couchdb":       roleDatabase,
	// caches
	"redis":     roleCache,
	"valkey":    roleCache,
	"keydb":     roleCache,
	"memcached": roleCache,
	"dragonfly": roleCache,
	// message queues
	"rabbitmq": roleMessageQueue,
	"kafka":    roleMessageQueue,
	"nats":     roleMessageQueue,
	"pulsar":   roleMessageQueue,
	"activemq": roleMessageQueue,
}

// tokenEngine maps an image/framework token to a canonical engine key in engineRole.
// Variants (postgresql→postgres, mongo→mongodb, percona→mysql) normalize here.
var tokenEngine = map[string]string{
	"postgres":      "postgres",
	"postgresql":    "postgres",
	"mysql":         "mysql",
	"mariadb":       "mariadb",
	"percona":       "mysql",
	"mongo":         "mongodb",
	"mongodb":       "mongodb",
	"clickhouse":    "clickhouse",
	"cassandra":     "cassandra",
	"scylla":        "scylla",
	"scylladb":      "scylla",
	"cockroach":     "cockroach",
	"cockroachdb":   "cockroach",
	"elasticsearch": "elasticsearch",
	"opensearch":    "opensearch",
	"influxdb":      "influxdb",
	"neo4j":         "neo4j",
	"couchdb":       "couchdb",
	"redis":         "redis",
	"valkey":        "valkey",
	"keydb":         "keydb",
	"memcached":     "memcached",
	"dragonfly":     "dragonfly",
	"rabbitmq":      "rabbitmq",
	"kafka":         "kafka",
	"nats":          "nats",
	"pulsar":        "pulsar",
	"activemq":      "activemq",
}

var tokenSplitRe = regexp.MustCompile(`[^a-z0-9]+`)

// tokenizeEngine splits s on non-alphanumerics and returns the first token that maps
// to a canonical engine. Token-based (not substring) matching avoids false hits like
// "nats" inside "automation".
func tokenizeEngine(s string) string {
	for _, tok := range tokenSplitRe.Split(strings.ToLower(s), -1) {
		if eng, ok := tokenEngine[tok]; ok {
			return eng
		}
	}
	return ""
}

// classifyDatastore decides whether a workload is an in-cluster datastore based solely
// on the runtime framework signal (cloud_resource_attributes) — the same protocol-verified
// source of truth as the DBMS view. Returns the canonical engine and its role.
// framework is the value from cloud_resource_attributes (may be ""); it has zero false
// positives because a workload only gets a framework row once its wire protocol is observed.
func classifyDatastore(framework string) (engine, role string, ok bool) {
	eng := tokenizeEngine(framework)
	if eng == "" {
		return "", "", false
	}
	r, found := engineRole[eng]
	if !found {
		return "", "", false
	}
	return eng, r, true
}

// GetWorkloadFrameworks returns a map of K8s workload resource_id -> canonical engine
// for the account, derived from the runtime framework attributes the discovery service
// writes to cloud_resource_attributes (the same source of truth as the DBMS view).
// A generic `framework` row wins over a per-container `framework/<container>` row.
func GetWorkloadFrameworks(accountID string) (map[string]string, error) {
	// Fail fast on an empty account to avoid an unscoped full-table scan.
	if accountID == "" {
		return nil, fmt.Errorf("accountID cannot be empty")
	}

	dbManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, fmt.Errorf("failed to get database manager: %w", err)
	}

	const query = `
		select resource_id, name, value
		from cloud_resource_attributes
		where account_id = $1
		  and name like 'framework%'
	`
	rows, err := dbManager.Db.Queryx(query, accountID)
	if err != nil {
		return nil, fmt.Errorf("failed to query cloud_resource_attributes: %w", err)
	}
	defer func() { _ = rows.Close() }()

	frameworks := make(map[string]string)
	hasGeneric := make(map[string]bool)
	for rows.Next() {
		var resourceID, name, value string
		if err := rows.Scan(&resourceID, &name, &value); err != nil {
			return nil, fmt.Errorf("failed to scan cloud_resource_attribute: %w", err)
		}
		eng := tokenizeEngine(value)
		if eng == "" {
			continue
		}
		generic := name == "framework"
		// Prefer the generic `framework` row; don't let a per-container row override it.
		if hasGeneric[resourceID] && !generic {
			continue
		}
		frameworks[resourceID] = eng
		if generic {
			hasGeneric[resourceID] = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating cloud_resource_attributes rows: %w", err)
	}
	return frameworks, nil
}
