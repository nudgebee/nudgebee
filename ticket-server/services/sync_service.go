package services

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"nudgebee/tickets-server/clients"
	"nudgebee/tickets-server/database"
	"nudgebee/tickets-server/models"
	"nudgebee/tickets-server/services/tools"
	"strings"

	"github.com/andygrunwald/go-jira"
)

func fetchConfigurationList() ([]models.TicketConfigurations, error) {
	dbManager, err := database.GetDatabaseManager()
	if err != nil {
		slog.Error("Unable to get database manager:", "error", slog.AnyValue(err))
		return nil, err
	}

	// Query all enabled ticketing integrations
	query := `
		SELECT
			i.id,
			i.tenant_id,
			i.type,
			i.name,
			i.status
		FROM integrations i
		WHERE i.type IN ('jira', 'github', 'gitlab', 'servicenow', 'pagerduty', 'zenduty')
		  AND i.status = 'enabled'
		ORDER BY i.created_at DESC
	`

	type IntegrationRow struct {
		ID       string `db:"id"`
		TenantID string `db:"tenant_id"`
		Type     string `db:"type"`
		Name     string `db:"name"`
		Status   string `db:"status"`
	}

	var integrationRows []IntegrationRow
	err = dbManager.Select(&integrationRows, query)
	if err != nil {
		slog.Error("Unable to fetch integrations:", "error", slog.AnyValue(err))
		return nil, err
	}

	// Fetch config values for each integration
	var configurations []models.TicketConfigurations
	for _, row := range integrationRows {
		config, err := fetchIntegrationWithConfigValues(dbManager, row.ID)
		if err != nil {
			slog.Error("Unable to fetch config values for integration:", "error", slog.AnyValue(err), "integration_id", row.ID)
			continue
		}

		configurations = append(configurations, config)
	}

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
