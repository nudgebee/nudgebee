package core

import (
	"database/sql"
	"errors"
	"fmt"
)

// defaultWriter is the part of a transaction writeAccountDefault needs.
type defaultWriter interface {
	Exec(query string, args ...any) (sql.Result, error)
	QueryRow(query string, args ...any) *sql.Row
}

// writeAccountDefault sets or clears the default column on an account's link to
// an integration, from a save in the Integrations UI. column comes from the
// caller's allowlist of default_*_provider columns.
//
// Turning a default on also marks the link in user_set_defaults, which tells the
// k8s collector's heartbeat to leave it alone (without the mark, a default the
// user put on an agent-created integration, such as the ClickHouse row traces
// created, was taken back by the next heartbeat of the agent's own provider).
// Only a link that already existed with the column off is marked: a value the
// request re-sends as already on, or a link the request creates, records no
// choice by the user. Every clear of the column also clears its mark.
func writeAccountDefault(tx defaultWriter, tenantId, integrationId, accountId, column string, on bool) error {
	if !on {
		if _, err := tx.Exec(fmt.Sprintf(`
			UPDATE integrations_cloud_accounts
			SET %s = false, user_set_defaults = array_remove(user_set_defaults, $3)
			WHERE integration_id = $1 AND cloud_account_id = $2`,
			column),
			integrationId, accountId, column,
		); err != nil {
			return fmt.Errorf("failed to update %s=false: %w", column, err)
		}
		return nil
	}

	// The LLM default has its own tenant-level handling and no heartbeat
	// claims it, so there is nothing for a mark to protect.
	markable := column != "default_llm_provider"
	var wasOn bool
	linked := false
	if markable {
		linked = true
		err := tx.QueryRow(fmt.Sprintf(`
			SELECT %s FROM integrations_cloud_accounts
			WHERE integration_id = $1 AND cloud_account_id = $2 AND tenant_id = $3 AND link_role = 'own'`,
			column),
			integrationId, accountId, tenantId,
		).Scan(&wasOn)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			linked = false
		case err != nil:
			return fmt.Errorf("failed to read %s: %w", column, err)
		}
	}

	if _, err := tx.Exec(fmt.Sprintf(`
		INSERT INTO integrations_cloud_accounts (
			integration_id, cloud_account_id, tenant_id, link_role, %s
		)
		VALUES ($1,$2,$3,'own',true)
		ON CONFLICT (integration_id, cloud_account_id, tenant_id, link_role)
		DO UPDATE SET %s=EXCLUDED.%s`,
		column, column, column),
		integrationId, accountId, tenantId,
	); err != nil {
		return fmt.Errorf("failed to upsert %s=true: %w", column, err)
	}

	if linked && !wasOn {
		if _, err := tx.Exec(`
			UPDATE integrations_cloud_accounts
			SET user_set_defaults = array_append(array_remove(user_set_defaults, $4), $4)
			WHERE integration_id = $1 AND cloud_account_id = $2 AND tenant_id = $3 AND link_role = 'own'`,
			integrationId, accountId, tenantId, column,
		); err != nil {
			return fmt.Errorf("failed to mark %s as user-set: %w", column, err)
		}
	}

	// One default per account: the others lose it, and with it any mark.
	if _, err := tx.Exec(fmt.Sprintf(`
		UPDATE integrations_cloud_accounts
		SET %s = false, user_set_defaults = array_remove(user_set_defaults, $3)
		WHERE cloud_account_id = $1 AND integration_id != $2`,
		column),
		accountId, integrationId, column,
	); err != nil {
		return fmt.Errorf("failed to reset others for %s: %w", column, err)
	}
	return nil
}
