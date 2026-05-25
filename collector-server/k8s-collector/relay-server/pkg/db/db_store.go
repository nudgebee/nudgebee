package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"nudgebee/relay-server/pkg/cache"
	"nudgebee/relay-server/pkg/utils"
	"strings"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"golang.org/x/crypto/bcrypt"
)

// AgentType constants for agent type filtering.
const (
	AgentTypeK8s   = "k8s"
	AgentTypeProxy = "proxy"
)

// WorkspaceConfigValue is a single key/value pair from an integration's config.
type WorkspaceConfigValue struct {
	Name  string
	Value string
}

// AgentDatasource describes a datasource reported by a proxy agent for auto-registration.
type AgentDatasource struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	ProxyType string `json:"proxy_type"`
	Name      string `json:"name"`
}

// ProxyDatasource holds the datasource config for pushing to proxy agents on connect.
type ProxyDatasource struct {
	ID               string            `json:"id"`
	Type             string            `json:"type"`
	ProxyType        string            `json:"proxy_type"`
	Name             string            `json:"name,omitempty"`
	Config           map[string]any    `json:"config"`
	Credentials      map[string]string `json:"credentials,omitempty"`
	CredentialSource string            `json:"credential_source"`
	CredentialRef    string            `json:"credential_ref,omitempty"`
}

// AgentStore defines what handlers need from the DB layer.
type AgentStore interface {
	ValidateAgent(ctx context.Context, accessKey, secret string) (bool, string, string, error)
	IsWSEnabled(ctx context.Context, accountID, agentType string) (bool, string, error)
	IsAgentConnected(ctx context.Context, accountID, agentType string) (bool, error)
	GetAgentStatus(ctx context.Context, accountID, agentType string) (connected bool, wsEnabled bool, fallbackURL string, prometheusAdditionalLabel string, err error)
	UpdateRelayConnectionStatus(ctx context.Context, accountID, agentType string, relayConnected bool, sessionStart time.Time) error
	UpdateAgentVersion(ctx context.Context, accountID, agentType, version, commit, buildTime, protocolVersion string) error
	UpdateDatasourceHealth(ctx context.Context, accountID, agentType string, datasources map[string]any) error
	UpsertAgentDatasources(ctx context.Context, accountID, agentType string, datasources []AgentDatasource) error
	UpdateDatasourceMetadata(ctx context.Context, accountID, agentType string, metadata map[string]map[string]any) error
	QueryProxyDatasources(ctx context.Context, accountID string) ([]ProxyDatasource, error)
	// GetWorkspaceToolConfig returns integration config values for the given account + integration type.
	// If configName is non-empty, only configs whose name matches (case-insensitive) are returned.
	GetWorkspaceToolConfig(ctx context.Context, accountID, integrationType, configName string) ([]WorkspaceConfigValue, error)
}

// pgStore is a PostgreSQL implementation of AgentStore.
type pgStore struct {
	db            *sqlx.DB
	encryptionKey string

	agentCache sync.Map    // map[accessKey]agentCacheEntry
	wsCache    cache.Cache // shared cache (Redis or in-memory) for agent connection status
}

type agentCacheEntry struct {
	Valid     bool
	AccountID string
	AgentType string
	Expires   time.Time
}

type wsCacheEntry struct {
	Allowed          bool           `json:"allowed"`
	Fallback         string         `json:"fallback"`
	Status           string         `json:"status"`
	ConnectionStatus map[string]any `json:"connection_status"`
}

const (
	agentCacheTTL          = 30 * time.Minute
	wsCacheTTL             = 30 * time.Minute
	wsNotConnectedCacheTTL = 2 * time.Minute
)

// NewPostgresStore opens the DB and returns a pgStore.
func NewPostgresStore(
	dsn, encryptionKey string,
	maxOpenConns, maxIdleConns int,
	connMaxLifetime time.Duration,
	wsCache cache.Cache,
) (AgentStore, error) {
	db, err := sqlx.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening db: %w", err)
	}
	db.SetMaxOpenConns(maxOpenConns)
	db.SetMaxIdleConns(maxIdleConns)
	db.SetConnMaxLifetime(connMaxLifetime)
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("pinging db: %w", err)
	}
	return &pgStore{db: db, encryptionKey: encryptionKey, wsCache: wsCache}, nil
}

func (p *pgStore) ValidateAgent(ctx context.Context, accessKey, secret string) (bool, string, string, error) {
	// 1) cache hit?
	if v, ok := p.agentCache.Load(accessKey); ok {
		ace := v.(agentCacheEntry)
		if time.Now().Before(ace.Expires) {
			return ace.Valid, ace.AccountID, ace.AgentType, nil
		}
		p.agentCache.Delete(accessKey)
	}

	// 2) query DB
	var row struct {
		AccountID       string         `db:"cloud_account_id"`
		AgentType       string         `db:"type"`
		SecretEncrypted sql.NullString `db:"access_secret"`
		SecretHashed    sql.NullString `db:"access_secret_v2"`
	}
	err := p.db.GetContext(ctx, &row, `
        SELECT cloud_account_id, "type", access_secret, access_secret_v2
          FROM agent
         WHERE access_key = $1
    `, accessKey)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// cache failure short‐term
			p.agentCache.Store(accessKey, agentCacheEntry{false, "", "", time.Now().Add(1 * time.Minute)})
			return false, "", "", nil
		}
		return false, "", "", err
	}

	// 3) verify secret
	ok := false
	if row.SecretHashed.Valid {
		if bcrypt.CompareHashAndPassword([]byte(row.SecretHashed.String), []byte(secret)) == nil {
			ok = true
		}
	} else if row.SecretEncrypted.Valid {
		dec, derr := utils.Decrypt(p.encryptionKey, row.SecretEncrypted.String)
		if derr == nil && dec == secret {
			ok = true
		}
	}
	if !ok {
		// cache failure short‐term
		p.agentCache.Store(accessKey, agentCacheEntry{false, "", "", time.Now().Add(1 * time.Minute)})
		return false, "", "", nil
	}

	// 4) cache success
	p.agentCache.Store(accessKey, agentCacheEntry{true, row.AccountID, row.AgentType, time.Now().Add(agentCacheTTL)})
	return true, row.AccountID, row.AgentType, nil
}

// wsCacheKey builds a composite cache key for account + agent type.
func wsCacheKey(accountID, agentType string) string {
	return accountID + ":" + agentType
}

// getWSEntry does the common cache check + DB query + cache store.
func (p *pgStore) getWSEntry(ctx context.Context, accountID, agentType string) (*wsCacheEntry, error) {
	cacheKey := wsCacheKey(accountID, agentType)

	// 1) Try cache
	if data, ok := p.wsCache.Get(ctx, cacheKey); ok {
		var wce wsCacheEntry
		if err := json.Unmarshal(data, &wce); err == nil {
			return &wce, nil
		}
	}

	// 2) Query both fields in one go
	var row struct {
		ConnStatus sql.NullString `db:"connection_status"`
		Status     sql.NullString `db:"status"`
	}
	err := p.db.GetContext(ctx, &row, `
        SELECT connection_status, status
          FROM agent
         WHERE cloud_account_id = $1
           AND "type" = $2
    `, accountID, agentType)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	// 3) Build the entry
	entry := wsCacheEntry{
		Allowed:          true, // default
		Fallback:         "",   // default
		Status:           "NOT_CONNECTED",
		ConnectionStatus: map[string]any{},
	}

	if row.Status.Valid {
		entry.Status = row.Status.String
	}

	if row.ConnStatus.Valid {
		var csMap map[string]any
		if jerr := json.Unmarshal([]byte(row.ConnStatus.String), &csMap); jerr == nil {
			if val, ok := csMap["agentWSEnabled"].(bool); ok {
				entry.Allowed = val
			}
			if urlVal, ok := csMap["agentUrl"].(string); ok {
				entry.Fallback = urlVal
			}
			entry.ConnectionStatus = csMap
		}
	}

	// 4) Cache with appropriate TTL based on status
	ttl := wsCacheTTL
	if entry.Status != "CONNECTED" {
		ttl = wsNotConnectedCacheTTL
	}
	if data, err := json.Marshal(entry); err != nil {
		slog.Warn("failed to marshal wsCacheEntry for caching", "key", cacheKey, "error", err)
	} else if err := p.wsCache.Set(ctx, cacheKey, data, ttl); err != nil {
		slog.Warn("failed to set wsCacheEntry in cache", "key", cacheKey, "error", err)
	}
	return &entry, nil
}

// IsWSEnabled now just calls getWSEntry and returns the two fields.
func (p *pgStore) IsWSEnabled(ctx context.Context, accountID, agentType string) (bool, string, error) {
	entry, err := p.getWSEntry(ctx, accountID, agentType)
	if err != nil {
		// no rows → fall back to defaults baked into entry
		if errors.Is(err, sql.ErrNoRows) {
			return true, "", nil
		}
		return false, "", err
	}
	return entry.Allowed, entry.Fallback, nil
}

// IsAgentConnected likewise calls getWSEntry and inspects Status.
func (p *pgStore) IsAgentConnected(ctx context.Context, accountID, agentType string) (bool, error) {
	entry, err := p.getWSEntry(ctx, accountID, agentType)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return entry.Status == "CONNECTED", nil
}

// GetAgentStatus returns all agent status info in a single call to avoid double DB queries
func (p *pgStore) GetAgentStatus(ctx context.Context, accountID, agentType string) (connected bool, wsEnabled bool, fallbackURL string, prometheusAdditionalLabel string, err error) {
	entry, err := p.getWSEntry(ctx, accountID, agentType)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, true, "", "", nil // Default: disconnected but WS enabled
		}
		return false, false, "", "", err
	}

	if entry.ConnectionStatus["prometheusAdditionalLabels"] != nil {
		switch labels := entry.ConnectionStatus["prometheusAdditionalLabels"].(type) {
		case string:
			prometheusAdditionalLabel = labels
		case map[string]any:
			sb := strings.Builder{}
			for k, v := range labels {
				sb.WriteString(k)
				sb.WriteString("=")
				fmt.Fprintf(&sb, "\"%v\"", v)
				sb.WriteString(",")
			}
			if sb.Len() > 0 {
				prometheusAdditionalLabel = sb.String()
			}
		}
		if prometheusAdditionalLabel != "" {
			prometheusAdditionalLabel = strings.TrimSuffix(prometheusAdditionalLabel, ",")
		}
	}

	return entry.Status == "CONNECTED", entry.Allowed, entry.Fallback, prometheusAdditionalLabel, nil
}

// UpdateRelayConnectionStatus updates the relayConnection field in the agent's connection_status JSON.
// sessionStart is the time this session began. When disconnecting, the update is skipped if a newer
// session has already connected (i.e. last_connected_at > sessionStart), preventing stale sessions
// from clobbering the status of active ones.
func (p *pgStore) UpdateRelayConnectionStatus(ctx context.Context, accountID, agentType string, relayConnected bool, sessionStart time.Time) error {
	// Update the connection_status JSON, agent status, and last_connected_at
	status := "NOT_CONNECTED"
	if relayConnected {
		status = "CONNECTED"
	}

	var query string
	var err error
	if relayConnected {
		// On connect: always update
		query = `
			UPDATE agent
			SET connection_status = COALESCE(connection_status, '{}'::jsonb) || jsonb_build_object('relayConnection', $2::boolean),
			    status = $4,
			    last_connected_at = NOW()
			WHERE cloud_account_id = $1 AND "type" = $3
		`
		_, err = p.db.ExecContext(ctx, query, accountID, relayConnected, agentType, status)
	} else {
		// On disconnect: only update if no newer session has connected since this one started.
		// This prevents a stale session's cleanup from overwriting a newer session's CONNECTED status.
		query = `
			UPDATE agent
			SET connection_status = COALESCE(connection_status, '{}'::jsonb) || jsonb_build_object('relayConnection', $2::boolean),
			    status = $4,
			    last_connected_at = NOW()
			WHERE cloud_account_id = $1 AND "type" = $3
			  AND (last_connected_at IS NULL OR last_connected_at <= $5)
		`
		_, err = p.db.ExecContext(ctx, query, accountID, relayConnected, agentType, status, sessionStart)
	}

	if err != nil {
		return fmt.Errorf("failed to update relay connection status for account %s (type=%s): %w", accountID, agentType, err)
	}

	// Invalidate cache after successful DB write to prevent a concurrent read
	// from repopulating stale data between Delete and ExecContext.
	if err := p.wsCache.Delete(ctx, wsCacheKey(accountID, agentType)); err != nil {
		slog.Warn("failed to invalidate wsCache after status update", "account_id", accountID, "agent_type", agentType, "error", err)
	}

	return nil
}

// UpdateAgentVersion persists the running agent build info to the agent row.
// - version is written to the first-class agent.version column (already surfaced via Hasura).
// - commit/buildTime/protocolVersion nest under connection_status.agentBuild.
// Empty fields are preserved so partial reports (e.g. health ticks that only carry version+commit)
// don't clobber values already recorded from the initial greeting.
func (p *pgStore) UpdateAgentVersion(ctx context.Context, accountID, agentType, version, commit, buildTime, protocolVersion string) error {
	if version == "" && commit == "" && buildTime == "" && protocolVersion == "" {
		return nil
	}

	build := map[string]string{}
	if commit != "" {
		build["commit"] = commit
	}
	if buildTime != "" {
		build["buildTime"] = buildTime
	}
	if protocolVersion != "" {
		build["protocolVersion"] = protocolVersion
	}

	buildJSON, err := json.Marshal(build)
	if err != nil {
		return fmt.Errorf("failed to marshal agent build info: %w", err)
	}

	// The trailing AND clause short-circuits the UPDATE when nothing changed
	// (same version, same build fields). Reconnects on network flaps are common
	// and would otherwise rewrite the row with identical data on every session,
	// producing dead tuples and WAL for no reason.
	query := `
		UPDATE agent
		SET version = CASE WHEN $2 <> '' THEN $2 ELSE version END,
		    connection_status = COALESCE(connection_status, '{}'::jsonb)
		                        || jsonb_build_object(
		                             'agentBuild',
		                             COALESCE(connection_status->'agentBuild', '{}'::jsonb) || $3::jsonb
		                           )
		WHERE cloud_account_id = $1 AND "type" = $4
		  AND (
		        ($2 <> '' AND version IS DISTINCT FROM $2)
		     OR NOT (COALESCE(connection_status->'agentBuild', '{}'::jsonb) @> $3::jsonb)
		      )
	`
	if _, err := p.db.ExecContext(ctx, query, accountID, version, string(buildJSON), agentType); err != nil {
		return fmt.Errorf("failed to update agent version for account %s (type=%s): %w", accountID, agentType, err)
	}

	if err := p.wsCache.Delete(ctx, wsCacheKey(accountID, agentType)); err != nil {
		slog.Warn("failed to invalidate wsCache after agent version update", "account_id", accountID, "agent_type", agentType, "error", err)
	}
	return nil
}

// UpdateDatasourceHealth merges per-datasource health status into the agent's connection_status JSONB.
// The datasources map is keyed by datasource ID with status objects as values.
// Example: {"ds-uuid-1": {"type": "postgresql", "status": "healthy", "last_check": "..."}}
func (p *pgStore) UpdateDatasourceHealth(ctx context.Context, accountID, agentType string, datasources map[string]any) error {
	dsJSON, err := json.Marshal(datasources)
	if err != nil {
		return fmt.Errorf("failed to marshal datasource health: %w", err)
	}

	query := `
		UPDATE agent
		SET connection_status = COALESCE(connection_status, '{}'::jsonb) || jsonb_build_object('datasources', $2::jsonb),
		    last_connected_at = NOW()
		WHERE cloud_account_id = $1 AND "type" = $3
	`

	_, err = p.db.ExecContext(ctx, query, accountID, string(dsJSON), agentType)
	if err != nil {
		return fmt.Errorf("failed to update datasource health for account %s (type=%s): %w", accountID, agentType, err)
	}

	// Invalidate cache after successful DB write
	if err := p.wsCache.Delete(ctx, wsCacheKey(accountID, agentType)); err != nil {
		slog.Warn("failed to invalidate wsCache after datasource health update", "account_id", accountID, "agent_type", agentType, "error", err)
	}

	return nil
}

// UpsertAgentDatasources auto-registers locally configured datasources as integrations.
// This follows the same pattern as k8s-collector's _upsert_integration in telemetry_handler.py.
func (p *pgStore) UpsertAgentDatasources(ctx context.Context, accountID, agentType string, datasources []AgentDatasource) error {
	if len(datasources) == 0 {
		return nil
	}

	// Look up tenant_id from agent table
	var tenantID string
	err := p.db.GetContext(ctx, &tenantID, `
		SELECT tenant FROM agent WHERE cloud_account_id = $1 AND "type" = $2
	`, accountID, agentType)
	if err != nil {
		return fmt.Errorf("failed to get tenant for account %s: %w", accountID, err)
	}

	// Track upserted integration IDs so we can clean up stale ones afterwards.
	upsertedIDs := make(map[string]bool)

	var errs []error
	for _, ds := range datasources {
		// Map proxy type to integration type name
		integrationType := proxyTypeToIntegrationType(ds.ProxyType, ds.Type)
		if integrationType == "" {
			continue
		}

		// Upsert integration
		var integrationID string
		err := p.db.GetContext(ctx, &integrationID, `
			INSERT INTO integrations (id, tenant_id, type, source, name, status, created_at, updated_at, labels)
			VALUES (gen_random_uuid(), $1, $2, 'agent', $3, 'enabled', NOW(), NOW(), '{}')
			ON CONFLICT (source, type, name, tenant_id) DO UPDATE
			SET status = 'enabled', updated_at = NOW()
			RETURNING id
		`, tenantID, integrationType, ds.Name)
		if err != nil {
			slog.Error("failed to upsert integration, skipping datasource", "datasource", ds.Name, "type", integrationType, "err", err)
			errs = append(errs, fmt.Errorf("datasource %s: %w", ds.Name, err))
			continue
		}

		upsertedIDs[integrationID] = true

		// Upsert integration-account mapping
		_, err = p.db.ExecContext(ctx, `
			INSERT INTO integrations_cloud_accounts (integration_id, cloud_account_id, tenant_id)
			VALUES ($1, $2, $3)
			ON CONFLICT (integration_id, cloud_account_id, tenant_id) DO NOTHING
		`, integrationID, accountID, tenantID)
		if err != nil {
			slog.Error("failed to upsert integration mapping, skipping datasource", "datasource", ds.Name, "err", err)
			errs = append(errs, fmt.Errorf("datasource %s mapping: %w", ds.Name, err))
			continue
		}

		// For dual-mode types (db-proxy, redis-proxy), set config values
		// so the llm-server and runbook-server can route requests to the correct agent.
		if isDualModeProxy(ds.ProxyType) {
			for _, cv := range []struct{ name, value string }{
				{"connection_mode", "vm_agent"},
				{"agent_type", agentType},
				{"datasource_key", ds.ID},
			} {
				_, err = p.db.ExecContext(ctx, `
					INSERT INTO integration_config_values (id, integration_id, name, value, is_encrypted, created_at, updated_at)
					VALUES (gen_random_uuid(), $1, $2, $3, false, NOW(), NOW())
					ON CONFLICT (integration_id, name) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()
				`, integrationID, cv.name, cv.value)
				if err != nil {
					slog.Error("failed to upsert config value, skipping", "config", cv.name, "datasource", ds.Name, "err", err)
				}
			}
		}
	}

	// Remove stale agent-sourced integrations for this account that are no longer
	// in the agent's inventory. This prevents orphaned entries from accumulating
	// when customers rename or remove local datasources.
	if err := p.removeStaleAgentDatasources(ctx, accountID, tenantID, upsertedIDs); err != nil {
		slog.Error("failed to remove stale agent datasources", "account_id", accountID, "err", err)
	}

	return errors.Join(errs...)
}

// removeStaleAgentDatasources deletes agent-sourced integrations for this account
// that are no longer reported in the agent's datasource inventory.
func (p *pgStore) removeStaleAgentDatasources(ctx context.Context, accountID, tenantID string, currentIDs map[string]bool) error {
	rows, err := p.db.QueryxContext(ctx, `
		SELECT i.id::text, i.name::text, i.type::text
		FROM integrations i
		JOIN integrations_cloud_accounts ica ON i.id = ica.integration_id
		WHERE ica.cloud_account_id = $1
		AND i.tenant_id = $2
		AND i.source = 'agent'
	`, accountID, tenantID)
	if err != nil {
		return fmt.Errorf("query stale agent datasources: %w", err)
	}
	defer rows.Close() // nolint:errcheck

	for rows.Next() {
		var id, name, intType string
		if err := rows.Scan(&id, &name, &intType); err != nil {
			continue
		}
		if currentIDs[id] {
			continue
		}

		// Delete config values first (no cascade FK)
		if _, err := p.db.ExecContext(ctx, `DELETE FROM integration_config_values WHERE integration_id = $1`, id); err != nil {
			slog.Error("failed to delete config values for stale agent datasource", "integration_id", id, "err", err)
			continue
		}
		// Delete integration (cascades to integrations_cloud_accounts)
		if _, err := p.db.ExecContext(ctx, `DELETE FROM integrations WHERE id = $1`, id); err != nil {
			slog.Error("failed to delete stale agent datasource", "integration_id", id, "err", err)
			continue
		}
		slog.Info("removed stale agent datasource", "integration_id", id, "name", name, "type", intType, "account_id", accountID)
	}
	return rows.Err()
}

// UpdateDatasourceMetadata persists version and metadata from proxy agent datasources
// into the integrations.labels JSONB column.
func (p *pgStore) UpdateDatasourceMetadata(ctx context.Context, accountID, agentType string, metadata map[string]map[string]any) error {
	if len(metadata) == 0 {
		return nil
	}

	// Look up tenant_id from agent table
	var tenantID string
	err := p.db.GetContext(ctx, &tenantID, `
		SELECT tenant FROM agent WHERE cloud_account_id = $1 AND "type" = $2
	`, accountID, agentType)
	if err != nil {
		return fmt.Errorf("failed to get tenant for account %s: %w", accountID, err)
	}

	for dsID, meta := range metadata {
		// Extract datasource name from ID (e.g., "local:my-postgres" → "my-postgres")
		dsName := dsID
		if idx := strings.Index(dsID, ":"); idx >= 0 {
			dsName = dsID[idx+1:]
		}

		metaJSON, err := json.Marshal(meta)
		if err != nil {
			continue
		}

		_, err = p.db.ExecContext(ctx, `
			UPDATE integrations
			SET labels = $1::jsonb, updated_at = NOW()
			WHERE name = $2 AND source = 'agent' AND tenant_id = $3
		`, string(metaJSON), dsName, tenantID)
		if err != nil {
			return fmt.Errorf("failed to update metadata for datasource %s: %w", dsName, err)
		}
	}

	return nil
}

// proxyTypeToIntegrationType maps proxy type + datasource type to the integration type name
// used in the integrations table. For dual-mode types (db-proxy, redis-proxy) it returns
// the actual datasource type (e.g. "postgresql", "mysql", "redis") so they appear alongside
// UI-configured integrations of the same type.
func proxyTypeToIntegrationType(proxyType, dsType string) string {
	switch proxyType {
	case "db-proxy":
		// Use actual DB type so it matches the registered integration type
		// (e.g. "postgresql", "mysql", "clickhouse", "mssql", "oracle")
		if dsType != "" {
			return dsType
		}
		return ""
	case "redis-proxy":
		return "redis"
	case "http-proxy":
		// Use actual datasource type if provided (e.g. "elastic_search", "rabbitmq", "prometheus")
		// so it matches the registered integration type.
		if dsType != "" && dsType != "http" {
			return dsType
		}
		return ""
	case "mongo-proxy":
		return "mongodb_proxy"
	case "kafka-proxy":
		return "kafka_proxy"
	case "ssh-proxy":
		return "ssh"
	case "mcp-proxy":
		return "mcp"
	default:
		return ""
	}
}

// GetWorkspaceToolConfig fetches integration_config_values for the given account and
// integration type. Uses the same tables as UpsertAgentDatasources.
// configName filters to a specific integration by name (case-insensitive); empty means any.
func (p *pgStore) GetWorkspaceToolConfig(ctx context.Context, accountID, integrationType, configName string) ([]WorkspaceConfigValue, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT icv.name, icv.value, icv.is_encrypted
		FROM integrations i
		JOIN integrations_cloud_accounts ica ON i.id = ica.integration_id
		JOIN integration_config_values icv ON i.id = icv.integration_id
		WHERE ica.cloud_account_id = $1
		  AND i.type = $2
		  AND i.status = 'enabled'
		  AND ($3 = '' OR lower(i.name) = lower($3))
	`, accountID, integrationType, configName)
	if err != nil {
		return nil, fmt.Errorf("GetWorkspaceToolConfig: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var result []WorkspaceConfigValue
	for rows.Next() {
		var name, value string
		var isEncrypted bool
		if err := rows.Scan(&name, &value, &isEncrypted); err != nil {
			return nil, fmt.Errorf("GetWorkspaceToolConfig scan: %w", err)
		}
		if isEncrypted && p.encryptionKey != "" {
			decrypted, derr := utils.Decrypt(p.encryptionKey, value)
			if derr != nil {
				slog.Error("GetWorkspaceToolConfig: failed to decrypt config value, skipping", "name", name, "err", derr)
				continue // do not expose encrypted ciphertext as plaintext
			}
			value = decrypted
		}
		result = append(result, WorkspaceConfigValue{Name: name, Value: value})
	}
	return result, rows.Err()
}

// isDualModeProxy returns true for proxy types that should have connection_mode,
// agent_type, and datasource_key config values set so the platform can route
// requests through the proxy agent.
func isDualModeProxy(proxyType string) bool {
	switch proxyType {
	case "db-proxy", "redis-proxy", "http-proxy", "ssh-proxy", "mcp-proxy":
		return true
	default:
		return false
	}
}
