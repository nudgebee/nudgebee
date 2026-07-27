package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"nudgebee/tickets-server/clients"
	"nudgebee/tickets-server/common"
	"nudgebee/tickets-server/database"
	"nudgebee/tickets-server/models"
	"nudgebee/tickets-server/services/tools"
	"strings"
	"time"

	"github.com/andygrunwald/go-jira"
)

// fetchConfigurationList loads all enabled ticketing integrations with their
// config values in a single JOIN query instead of 2N+1 round-trips (1 listing
// + N metadata re-fetches + N config-value fetches).
func fetchConfigurationList() ([]models.TicketConfigurations, error) {
	dbManager, err := database.GetDatabaseManager()
	if err != nil {
		slog.Error("Unable to get database manager:", "error", slog.AnyValue(err))
		return nil, err
	}

	query := `
		SELECT
			i.id,
			i.tenant_id,
			i.type,
			i.name,
			i.status,
			i.created_by,
			COALESCE(icv.name, '')                AS config_name,
			COALESCE(icv.value, '')               AS config_value,
			COALESCE(icv.is_encrypted, false)     AS is_encrypted
		FROM integrations i
		LEFT JOIN integration_config_values icv ON i.id = icv.integration_id
		WHERE i.type IN ('jira', 'github', 'gitlab', 'servicenow', 'pagerduty', 'zenduty', 'freshdesk')
		  AND i.status = 'enabled'
		ORDER BY i.created_at DESC, i.id
	`

	type joinRow struct {
		ID          string  `db:"id"`
		TenantID    string  `db:"tenant_id"`
		Type        string  `db:"type"`
		Name        string  `db:"name"`
		Status      string  `db:"status"`
		CreatedBy   *string `db:"created_by"`
		ConfigName  string  `db:"config_name"`
		ConfigValue string  `db:"config_value"`
		IsEncrypted bool    `db:"is_encrypted"`
	}

	var rows []joinRow
	if err = dbManager.Select(&rows, query); err != nil {
		slog.Error("Unable to fetch integrations with config values:", "error", slog.AnyValue(err))
		return nil, err
	}

	seen := make(map[string]int, len(rows))
	configurations := make([]models.TicketConfigurations, 0, len(rows))
	failedIntegrations := make(map[string]bool)

	for _, r := range rows {
		if failedIntegrations[r.ID] {
			continue
		}

		idx, exists := seen[r.ID]
		if !exists {
			idx = len(configurations)
			seen[r.ID] = idx
			configurations = append(configurations, models.TicketConfigurations{
				ID:        r.ID,
				Tenant:    r.TenantID,
				Tool:      r.Type,
				Name:      r.Name,
				Status:    r.Status,
				CreatedBy: r.CreatedBy,
				IsActive:  r.Status == "enabled",
			})
		}

		if r.ConfigName == "" {
			continue
		}

		value := r.ConfigValue
		if r.IsEncrypted && value != "" {
			decrypted, decErr := common.Decrypt(value)
			if decErr != nil {
				slog.Error("Failed to decrypt config value:", "name", r.ConfigName, "error", decErr, "integration_id", r.ID)
				failedIntegrations[r.ID] = true
				continue
			}
			value = decrypted
		}

		cfg := &configurations[idx]
		switch r.ConfigName {
		case "url":
			cfg.URL = value
		case "username":
			cfg.Username = value
		case "password":
			cfg.Password = value
		case "auth_type":
			cfg.AuthType = value
		case "projects":
			if value != "" {
				if err := json.Unmarshal([]byte(value), &cfg.Projects); err != nil {
					slog.Warn("Failed to unmarshal projects config value", "integration_id", r.ID, "error", err)
				}
			}
		case "priorities":
			if value != "" {
				if err := json.Unmarshal([]byte(value), &cfg.Priorities); err != nil {
					slog.Warn("Failed to unmarshal priorities config value", "integration_id", r.ID, "error", err)
				}
			}
		case "users":
			if value != "" {
				if err := json.Unmarshal([]byte(value), &cfg.Users); err != nil {
					slog.Warn("Failed to unmarshal users config value", "integration_id", r.ID, "error", err)
				}
			}
		case "last_connected":
			if value != "" {
				lastConnected, parseErr := time.Parse(time.RFC3339, value)
				if parseErr == nil {
					cfg.LastConnected = &lastConnected
				}
			}
		default:
			slog.Debug("Unhandled integration config key", "name", r.ConfigName, "integration_id", r.ID)
		}
	}

	// Remove integrations that failed decryption, preserving order.
	n := 0
	for _, cfg := range configurations {
		if !failedIntegrations[cfg.ID] {
			configurations[n] = cfg
			n++
		}
	}
	for i := n; i < len(configurations); i++ {
		configurations[i] = models.TicketConfigurations{}
	}
	configurations = configurations[:n]

	return configurations, nil
}

func ListTickets(configurationId string) ([]models.Ticket, error) {
	dbManager, err := database.GetDatabaseManager()
	if err != nil {
		return nil, fmt.Errorf("database unavailable: %v", err)
	}

	var tickets []models.Ticket
	err = dbManager.Select(&tickets, `
		SELECT id, integration_id,
		       COALESCE(assignee, '') as assignee,
		       COALESCE(severity, '') as severity,
		       COALESCE(status, '') as status,
		       ticket_id,
		       COALESCE(url, '') as url
		FROM tickets
		WHERE integration_id = $1`, configurationId)
	if err != nil {
		slog.Error("Error querying tickets", "integrationID", configurationId, "error", err)
		return nil, err
	}
	return tickets, nil
}

func SyncTickets() {
	configurations, err := fetchConfigurationList()
	if err != nil {
		slog.Error("Unable to fetch configuration list:", "error", slog.AnyValue(err))
		return
	}

	dbManager, err := database.GetDatabaseManager()
	if err != nil {
		slog.Error("Unable to get database manager for ticket sync:", "error", slog.AnyValue(err))
		return
	}

	for _, configuration := range configurations {
		if configuration.Tool != "jira" {
			continue
		}

		tickets, err := ListTickets(configuration.ID)
		if err != nil {
			slog.Error("Unable to list tickets", "error", err, "configuration_id", configuration.ID)
			continue
		}

		if len(tickets) == 0 {
			continue
		}

		jiraClient, err := clients.CreateJiraClient(configuration.Username, configuration.Password, configuration.URL)
		if err != nil {
			slog.Error("Unable to create Jira client", "error", err, "configuration_id", configuration.ID)
			continue
		}

		for _, ticket := range tickets {
			issue, err := tools.FetchFullIssueDetails(jiraClient, ticket.TicketID)
			if err != nil {
				var jiraErr *jira.Error
				if errors.As(err, &jiraErr) && jiraErr.HTTPError != nil && strings.Contains(jiraErr.HTTPError.Error(), "Status code: 404") {
					slog.Debug("Ticket not found or inaccessible, skipping", "ticketID", ticket.TicketID)
					continue
				}
				slog.Error("Error:", "error", slog.AnyValue(err))
				continue
			}

			if issue == nil || issue.Fields == nil {
				slog.Error("Issue or its fields are empty", "ticketID", ticket.TicketID)
				continue
			}
			update := false
			updateFields := models.UpdateFields{}
			if issue.Fields.Status != nil && ticket.Status != issue.Fields.Status.Name {
				updateFields.Status = issue.Fields.Status.Name
				update = true
			}
			if issue.Fields.Priority != nil && ticket.Severity != issue.Fields.Priority.Name {
				updateFields.Severity = issue.Fields.Priority.Name
				update = true
			}
			if issue.Fields.Assignee != nil && ticket.Assignee != issue.Fields.Assignee.DisplayName {
				updateFields.Assignee = issue.Fields.Assignee.DisplayName
				update = true
			}

			if update {
				slog.Debug("Updating ticket fields", "ticketID", ticket.TicketID, "updateFields", updateFields)
				setClauses, args := buildUpdateClauses(updateFields)
				if len(setClauses) > 0 {
					argIdx := len(args) + 1
					query := fmt.Sprintf("UPDATE tickets SET %s WHERE id = $%d",
						strings.Join(setClauses, ", "), argIdx)
					args = append(args, ticket.ID)
					_, err = dbManager.Exec(query, args...)
					if err != nil {
						slog.Info("Unable to update ticket:", "error", slog.AnyValue(err))
					}
				}
			}
		}
	}

}

// SyncConfigurations refreshes metadata for all enabled ticketing integrations.
// Uses context.Background() since it runs as a background goroutine detached from any HTTP request.
func SyncConfigurations() {
	ctx := context.Background()

	configurationTools := map[string]bool{
		"jira":       true,
		"github":     true,
		"gitlab":     true,
		"servicenow": true,
		"pagerduty":  true,
		"zenduty":    true,
		"freshdesk":  true,
	}

	configurations, err := fetchConfigurationList()
	if err != nil {
		slog.Error("Unable to fetch configuration list:", "error", slog.AnyValue(err))
		return
	}

	dbManager, err := database.GetDatabaseManager()
	if err != nil {
		slog.Error("Unable to get database manager:", "error", slog.AnyValue(err))
		return
	}

	for _, configuration := range configurations {
		if !configurationTools[configuration.Tool] {
			continue
		}

		metadata, err := ValidateAndGetMetadataWithContext(ctx, configuration)
		if err != nil {
			// Mark integration as disabled/expired
			_, dbErr := dbManager.Exec(`
				UPDATE integrations
				SET status = $1, updated_at = NOW()
				WHERE id = $2
			`, "disabled", configuration.ID)
			if dbErr != nil {
				slog.Error("Unable to update integration status:", "error", slog.AnyValue(dbErr), "id", configuration.ID)
			}
			slog.Error("Unable to validate configuration:", "error", slog.AnyValue(err), "id", configuration.ID)
			continue
		}

		if err := updateMetadataConfigValues(configuration.ID, metadata, configuration.CreatedBy); err != nil {
			slog.Error("Failed to update metadata config values during sync",
				"integration_id", configuration.ID, "tool", configuration.Tool, "error", err)
		}
	}
}
