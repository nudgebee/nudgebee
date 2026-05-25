package migration

import (
	"crypto/sha256"
	"fmt"
	"log/slog"
	"nudgebee/services/internal/database"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

func DeduplicateIntegrations() error {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return err
	}

	// Step 1: fetch all integrations
	rows, err := dbms.Db.Queryx(`SELECT id, tenant_id, account_id, type, name 
		FROM integrations 
		WHERE source = $1`, "user")
	if err != nil {
		return err
	}
	defer func() {
		err := rows.Close()
		if err != nil {
			slog.Error("Error closing response body", "error", err)
		}
	}()

	type Integration struct {
		ID        string `db:"id"`
		TenantID  string `db:"tenant_id"`
		AccountID string `db:"account_id"`
		Type      string `db:"type"`
		Name      string `db:"name"`
	}

	var integrations []Integration
	for rows.Next() {
		var i Integration
		if err := rows.StructScan(&i); err != nil {
			return err
		}
		integrations = append(integrations, i)
	}

	// Step 2: load config values for all integrations at once
	configs := make(map[string]map[string]string)
	ids := make([]string, 0, len(integrations))
	for _, i := range integrations {
		ids = append(ids, i.ID)
	}

	cvRows, err := dbms.Db.Queryx(`
		SELECT integration_id, name, value 
		FROM integration_config_values 
		WHERE integration_id = ANY($1)`, pq.Array(ids))
	if err != nil {
		return err
	}
	defer func() {
		err := cvRows.Close()
		if err != nil {
			slog.Error("Error closing response body", "error", err)
		}
	}()

	for cvRows.Next() {
		var integrationID, name, value string
		if err := cvRows.Scan(&integrationID, &name, &value); err != nil {
			return err
		}
		if configs[integrationID] == nil {
			configs[integrationID] = make(map[string]string)
		}
		configs[integrationID][name] = value
	}

	// Step 3: detect duplicates (hash of config values)
	type Key struct {
		Type string
		Hash string
	}
	seen := make(map[Key]string) // key -> canonical integration_id

	for _, i := range integrations {
		configMap := configs[i.ID]
		keys := make([]string, 0, len(configMap))
		for k := range configMap {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		var builder strings.Builder
		for _, k := range keys {
			builder.WriteString(k)
			builder.WriteString(configMap[k])
		}

		normalized := builder.String()
		hash := fmt.Sprintf("%x", sha256.Sum256([]byte(normalized)))
		key := Key{Type: i.Type, Hash: hash}

		// Extract defaults
		defaultLogs := configs[i.ID]["default_log_provider"] == "true"
		defaultTraces := configs[i.ID]["default_traces_provider"] == "true"
		defaultMetrics := configs[i.ID]["default_metrics_provider"] == "true"

		targetIntegration := i.ID
		if canonical, exists := seen[key]; exists {
			// Duplicate → merge into canonical
			targetIntegration = canonical
		} else {
			// First time → mark as canonical
			seen[key] = i.ID
		}

		// Insert mapping into integrations_cloud_accounts with defaults
		_, err := dbms.Exec(`
			INSERT INTO integrations_cloud_accounts (
				id, integration_id, cloud_account_id, tenant_id,
				default_log_provider, default_traces_provider, default_metrics_provider
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (integration_id, cloud_account_id)
			DO UPDATE SET 
				default_log_provider = EXCLUDED.default_log_provider,
				default_traces_provider = EXCLUDED.default_traces_provider,
				default_metrics_provider = EXCLUDED.default_metrics_provider
		`, uuid.NewString(), targetIntegration, i.AccountID, i.TenantID,
			defaultLogs, defaultTraces, defaultMetrics)
		if err != nil {
			return err
		}

		// If duplicate, delete old integration + values
		if targetIntegration != i.ID {
			_, err = dbms.Exec(`DELETE FROM integration_config_values WHERE integration_id = $1`, i.ID)
			if err != nil {
				return err
			}
			_, err = dbms.Exec(`DELETE FROM integrations WHERE id = $1`, i.ID)
			if err != nil {
				return err
			}
		}
	}

	return nil
}
