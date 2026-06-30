package core

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"

	"nudgebee/services/internal/database"
	"nudgebee/services/security"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// MessagingBackfillResult summarizes a backfill run.
type MessagingBackfillResult struct {
	Created int      `json:"created"`
	Skipped int      `json:"skipped"`
	Failed  int      `json:"failed"`
	Errors  []string `json:"errors,omitempty"`
}

type messagingPlatformRow struct {
	id, tenantId, platform, teamId, teamName, token, refreshToken      string
	botId, appId, clientId, username, scopes, channels, tokenExpiresAt string
}

// BackfillMessagingIntegrations is a one-off, idempotent migration that copies legacy
// messaging_platforms rows (Slack / MS Teams) into the integrations table with the
// OAuth token encrypted at rest (via CreateIntegrationConfig's schema-driven
// encryption). Tenants that already have the integration for that platform are
// skipped, so it is safe to re-run. It is cross-tenant: each integration is created
// under its own tenant's admin context. Legacy rows are left in place (reads dedup to
// the integration); dropping them is a separate cleanup.
func BackfillMessagingIntegrations(logger *slog.Logger, tracer *trace.Tracer, meter *metric.Meter) (MessagingBackfillResult, error) {
	result := MessagingBackfillResult{}
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return result, err
	}

	rows, err := dbms.Db.Query(`
		SELECT id::text, tenant_id::text, platform,
		       COALESCE(team_id, ''), COALESCE(team_name, ''), COALESCE(token, ''),
		       COALESCE(refresh_token, ''), COALESCE(bot_id, ''), COALESCE(app_id, ''),
		       COALESCE(client_id, ''), COALESCE(username, ''), COALESCE(scopes, ''),
		       COALESCE(channels::text, ''),
		       COALESCE(to_char(token_expires_at, 'YYYY-MM-DD"T"HH24:MI:SS'), '')
		FROM messaging_platforms
		WHERE platform IN ('slack', 'ms_teams')`)
	if err != nil {
		return result, err
	}

	var collected []messagingPlatformRow
	for rows.Next() {
		var r messagingPlatformRow
		if scanErr := rows.Scan(&r.id, &r.tenantId, &r.platform, &r.teamId, &r.teamName,
			&r.token, &r.refreshToken, &r.botId, &r.appId, &r.clientId, &r.username,
			&r.scopes, &r.channels, &r.tokenExpiresAt); scanErr != nil {
			slog.Error("messaging backfill: scan failed", "error", scanErr)
			continue
		}
		collected = append(collected, r)
	}
	if closeErr := rows.Close(); closeErr != nil {
		slog.Warn("messaging backfill: failed to close rows", "error", closeErr)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return result, rowsErr
	}

	for _, r := range collected {
		if r.tenantId == "" {
			result.Failed++
			result.Errors = append(result.Errors, fmt.Sprintf("%s: empty tenant_id", r.id))
			continue
		}
		// Idempotent: skip tenants that already have the integration for this platform.
		var existingId string
		checkErr := dbms.Db.QueryRow(
			`SELECT id::text FROM integrations WHERE tenant_id = $1 AND type = $2 LIMIT 1`,
			r.tenantId, r.platform).Scan(&existingId)
		if checkErr == nil {
			result.Skipped++
			continue
		}
		if checkErr != sql.ErrNoRows {
			result.Failed++
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", r.id, checkErr))
			continue
		}

		ctx := security.NewRequestContextForTenantAdmin(r.tenantId, logger, tracer, meter)
		_, createErr := CreateIntegrationConfig(
			ctx, "", r.platform, r.teamId, buildMessagingConfigValues(r.platform, r),
			nil, []string{}, true, "user")
		if createErr != nil {
			result.Failed++
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", r.id, createErr))
			continue
		}
		result.Created++
	}
	return result, nil
}

// buildMessagingConfigValues maps a legacy messaging_platforms row to integration
// config values. Token fields (bot_token/access_token/refresh_token) are encrypted by
// CreateIntegrationConfig per the slack.go / ms_teams.go schema; everything else,
// including the scalar default channel + its display name, is stored plaintext.
func buildMessagingConfigValues(platform string, r messagingPlatformRow) []IntegrationConfigValue {
	var values []IntegrationConfigValue
	add := func(name, val string) {
		if val != "" {
			values = append(values, IntegrationConfigValue{Name: name, Value: val})
		}
	}
	add("token_expires_at", r.tokenExpiresAt)
	add("app_id", r.appId)
	add("client_id", r.clientId)
	add("installed_by", r.username)
	add("scopes", r.scopes)
	add("refresh_token", r.refreshToken)

	if platform == "slack" {
		add("bot_token", r.token)
		add("bot_id", r.botId)
		add("team_name", r.teamName)
		// Legacy Slack default channel shape: {"name": ..., "id": ...}, or in older
		// rows a single-element array of the same.
		if r.channels != "" {
			var ch struct {
				Name string `json:"name"`
				Id   string `json:"id"`
			}
			if json.Unmarshal([]byte(r.channels), &ch) == nil {
				add("default_channel_id", ch.Id)
				add("default_channel_name", ch.Name)
			} else {
				var chs []struct {
					Name string `json:"name"`
					Id   string `json:"id"`
				}
				if json.Unmarshal([]byte(r.channels), &chs) == nil && len(chs) > 0 {
					add("default_channel_id", chs[0].Id)
					add("default_channel_name", chs[0].Name)
				}
			}
		}
		return values
	}

	// ms_teams — legacy default shape: {"team_id", "team_name", "channels": [{"name","id"}]}
	add("access_token", r.token)
	if r.channels != "" {
		var ch struct {
			TeamId   string `json:"team_id"`
			TeamName string `json:"team_name"`
			Channels []struct {
				Name string `json:"name"`
				Id   string `json:"id"`
			} `json:"channels"`
		}
		if json.Unmarshal([]byte(r.channels), &ch) == nil {
			add("default_team_id", ch.TeamId)
			add("default_team_name", ch.TeamName)
			if len(ch.Channels) > 0 {
				add("default_channel_id", ch.Channels[0].Id)
				add("default_channel_name", ch.Channels[0].Name)
			}
		}
	}
	return values
}
