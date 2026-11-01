package alertrule

import (
	"fmt"
	"net/url"
	"strings"

	"nudgebee/services/internal/database"
	"nudgebee/services/security"
)

type openObserveAlertConfig struct {
	URL, OrgID, Username, Password, LogStream, TraceStream string
}

// Like the other alert providers, resolve locally to avoid integrations -> eventrule -> alertrule cycles.
func getOpenObserveAlertConfig(ctx *security.RequestContext, accountID string) (openObserveAlertConfig, error) {
	cfg := openObserveAlertConfig{LogStream: "default", TraceStream: "default"}
	if accountID == "" {
		return cfg, fmt.Errorf("account_id is required to resolve an OpenObserve integration")
	}
	db, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return cfg, fmt.Errorf("get OpenObserve config database: %w", err)
	}
	rows, err := db.Db.Queryx(`
 SELECT DISTINCT i.id::text, v.name::text, v.value::text, v.is_encrypted
 FROM integrations i
 JOIN integrations_cloud_accounts ica ON i.id=ica.integration_id AND ica.tenant_id=i.tenant_id
 JOIN cloud_accounts ca ON ca.id=ica.cloud_account_id AND ca.tenant=i.tenant_id
 JOIN integration_config_values v ON v.integration_id=i.id
 WHERE i.type='openobserve' AND i.source='user' AND i.status != 'disabled'
 AND i.tenant_id=$1 AND ica.cloud_account_id=$2`, ctx.GetSecurityContext().GetTenantId(), accountID)
	if err != nil {
		return cfg, fmt.Errorf("list OpenObserve alert configs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	integrationID := ""
	for rows.Next() {
		var id string
		var value integrationConfigValue
		if err := rows.Scan(&id, &value.Name, &value.Value, &value.IsEncrypted); err != nil {
			return cfg, fmt.Errorf("read OpenObserve alert config: %w", err)
		}
		if integrationID != "" && integrationID != id {
			return cfg, fmt.Errorf("multiple active OpenObserve integrations for account %s", accountID)
		}
		integrationID = id
		decoded, err := decryptConfigValue(value)
		if err != nil {
			return cfg, fmt.Errorf("decrypt OpenObserve config %s: %w", value.Name, err)
		}
		switch value.Name {
		case "openobserve_url":
			cfg.URL = strings.TrimSpace(decoded)
		case "openobserve_org_id":
			cfg.OrgID = strings.TrimSpace(decoded)
		case "openobserve_username":
			cfg.Username = decoded
		case "openobserve_password":
			cfg.Password = decoded
		case "openobserve_log_stream":
			if strings.TrimSpace(decoded) != "" {
				cfg.LogStream = strings.TrimSpace(decoded)
			}
		case "openobserve_trace_stream":
			if strings.TrimSpace(decoded) != "" {
				cfg.TraceStream = strings.TrimSpace(decoded)
			}
		}
	}
	if err := rows.Err(); err != nil {
		return cfg, fmt.Errorf("read OpenObserve alert configs: %w", err)
	}
	if integrationID == "" {
		return cfg, fmt.Errorf("active OpenObserve integration not found for account %s", accountID)
	}
	parsed, err := url.Parse(cfg.URL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil {
		return cfg, fmt.Errorf("OpenObserve URL must be an HTTP(S) base URL without embedded credentials")
	}
	cfg.URL = parsed.Scheme + "://" + parsed.Host
	if cfg.OrgID == "" || cfg.Username == "" || cfg.Password == "" {
		return cfg, fmt.Errorf("OpenObserve organization, username and password are required")
	}
	if !openObserveAlertStreamName(cfg.LogStream) || !openObserveAlertStreamName(cfg.TraceStream) {
		return cfg, fmt.Errorf("invalid OpenObserve log or trace stream name")
	}
	return cfg, nil
}

func openObserveAlertStreamName(name string) bool {
	if name == "" || len(name) > 255 {
		return false
	}
	for _, r := range name {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' {
			return false
		}
	}
	return true
}
