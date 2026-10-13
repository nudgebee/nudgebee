package core

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"runtime/debug"
	"sync"

	"nudgebee/llm/common"
)

// Check now (#40994): an admin asks for an MCP integration's health to be
// checked immediately instead of waiting for Nubi's next use of it. It runs the
// same discovery Nubi does, through the relay, and records the result as an
// explicit check, which skips the hold.

var (
	ErrMCPIntegrationNotFound   = errors.New("MCP integration not found")
	ErrMCPIntegrationNotEnabled = errors.New("MCP integration is not enabled")
	ErrMCPIntegrationNoAccounts = errors.New("MCP integration is not linked to any account")
	ErrMCPCheckInconclusive     = errors.New("could not reach the relay to check the MCP integration; try again")
)

// MCPCheckTarget is an MCP integration loaded for Check now.
type MCPCheckTarget struct {
	TenantID string
	cfg      mcpIntegrationConfig
	enabled  bool
	accounts []string
}

// loadMCPCheckTarget is a variable so tests can supply an integration without
// a database.
var loadMCPCheckTarget = loadMCPCheckTargetFromDB

// LoadMCPCheckTarget reads an MCP integration, its config and its linked
// accounts. The caller authorizes against TenantID before CheckMCPIntegration.
func LoadMCPCheckTarget(integrationID string) (MCPCheckTarget, error) {
	return loadMCPCheckTarget(integrationID)
}

func loadMCPCheckTargetFromDB(integrationID string) (MCPCheckTarget, error) {
	var t MCPCheckTarget
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return t, err
	}

	var status string
	var accounts []byte
	err = dbms.Db.QueryRow(`
		SELECT i.tenant_id::text, i.status::text,
			COALESCE((SELECT json_agg(ica.cloud_account_id::text ORDER BY ica.cloud_account_id)
				FROM integrations_cloud_accounts ica WHERE ica.integration_id = i.id), '[]'::json)
		FROM integrations i
		WHERE i.id = $1::uuid AND i.type = 'mcp'
	`, integrationID).Scan(&t.TenantID, &status, &accounts)
	if errors.Is(err, sql.ErrNoRows) {
		return t, ErrMCPIntegrationNotFound
	}
	if err != nil {
		return t, err
	}
	if err := json.Unmarshal(accounts, &t.accounts); err != nil {
		return t, err
	}
	t.enabled = status == "enabled"

	rows, err := dbms.Query(`
		SELECT
			i.id::text,
			i.name::text,
			icv.name as config_name,
			icv.value as config_value,
			icv.is_encrypted as config_encrypted
		FROM integrations i
		LEFT JOIN integration_config_values icv ON i.id = icv.integration_id
		WHERE i.id = $1::uuid
	`, integrationID)
	if err != nil {
		return t, err
	}
	defer func() { _ = rows.Close() }()
	configs, err := scanMCPIntegrationConfigs(rows)
	if err != nil {
		return t, err
	}
	if len(configs) == 0 {
		return t, ErrMCPIntegrationNotFound
	}
	t.cfg = configs[0]
	return t, nil
}

// CheckMCPIntegration runs discovery for every scope of the integration now: one
// for a direct integration (through any linked account, since the relay calls
// the server itself), one per linked account for a vm_agent integration. It
// returns the integration's stored health afterwards.
//
// Every caller admitted in v1 is tenant-wide (integrations:Execute is not on
// the gateway's ACCOUNT_ENFORCED_ACTIONS), so every linked account is checked.
func CheckMCPIntegration(t MCPCheckTarget) (json.RawMessage, error) {
	if !t.enabled {
		return nil, ErrMCPIntegrationNotEnabled
	}
	if len(t.accounts) == 0 {
		return nil, ErrMCPIntegrationNoAccounts
	}
	accounts := t.accounts
	if t.cfg.ConnectionMode != "vm_agent" {
		accounts = accounts[:1]
	}

	var mu sync.Mutex
	recorded := false
	var wg sync.WaitGroup
	sem := make(chan struct{}, 10)
	for _, accountId := range accounts {
		wg.Add(1)
		go func(accountId string) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					slog.Error("mcp-integration: health check panicked", "integration_id", t.cfg.IntegrationID, "account_id", accountId, "panic", r, "stack", string(debug.Stack()))
				}
			}()
			sem <- struct{}{}
			defer func() { <-sem }()
			_, o := runMCPDiscovery(accountId, t.cfg, true)
			if o.kind != mcpOutcomeIgnore {
				mu.Lock()
				recorded = true
				mu.Unlock()
			}
		}(accountId)
	}
	wg.Wait()
	if !recorded {
		return nil, ErrMCPCheckInconclusive
	}

	stored, err := mcpHealthStoreInstance.load([]string{t.cfg.IntegrationID})
	if err != nil {
		return nil, err
	}
	// Absent when a save reset the health (or a disable stopped the write)
	// while the check ran: no verdict.
	health := stored[t.cfg.IntegrationID]
	if health == nil {
		health = mcpHealth{}
	}
	return json.Marshal(health)
}
