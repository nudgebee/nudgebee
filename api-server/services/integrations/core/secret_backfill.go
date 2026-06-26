package core

import (
	"context"
	"fmt"
	"log/slog"

	"nudgebee/services/common"
	"nudgebee/services/internal/database"
)

// SecretBackfillResult summarizes a re-encryption backfill run.
type SecretBackfillResult struct {
	Encrypted int      `json:"encrypted"`
	Failed    int      `json:"failed"`
	Errors    []string `json:"errors,omitempty"`
}

// BackfillConfluenceTokenEncryption is a one-off, idempotent migration that
// encrypts Confluence integration tokens stored as plaintext before the token
// field was marked IsEncrypted (see confluence.go, issue #31697). It selects
// every integration_config_values row for type='confluence', name='token' still
// flagged is_encrypted=false, encrypts the value with the shared
// NUDGEBEE_ENCRYPTION_KEY (common.Encrypt — byte-compatible with the llm-server
// and rag-server decrypt paths), and rewrites it with is_encrypted=true.
//
// The is_encrypted=false predicate makes it idempotent: already-encrypted rows
// are never re-touched, so it is safe to re-run. Rows are processed
// independently — a single failure is recorded and does not abort the rest.
func BackfillConfluenceTokenEncryption(ctx context.Context, logger *slog.Logger) (SecretBackfillResult, error) {
	result := SecretBackfillResult{}
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return result, err
	}

	type tokenRow struct {
		id    string
		value string
	}

	rows, err := dbms.Db.QueryContext(ctx, `
		SELECT icv.id::text, icv.value
		  FROM integration_config_values icv
		  JOIN integrations i ON i.id = icv.integration_id
		 WHERE i.type = 'confluence'
		   AND icv.name = 'token'
		   AND icv.is_encrypted = false
		   AND icv.value <> ''`)
	if err != nil {
		return result, err
	}

	var collected []tokenRow
	for rows.Next() {
		var r tokenRow
		if scanErr := rows.Scan(&r.id, &r.value); scanErr != nil {
			logger.Error("confluence token backfill: scan failed", "error", scanErr)
			continue
		}
		collected = append(collected, r)
	}
	if closeErr := rows.Close(); closeErr != nil {
		logger.Warn("confluence token backfill: failed to close rows", "error", closeErr)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return result, rowsErr
	}

	for _, r := range collected {
		encrypted, encErr := common.Encrypt(r.value)
		if encErr != nil {
			result.Failed++
			result.Errors = append(result.Errors, fmt.Sprintf("%s: encrypt failed: %v", r.id, encErr))
			continue
		}
		res, updErr := dbms.Db.ExecContext(ctx,
			`UPDATE integration_config_values
			    SET value = $1, is_encrypted = true, updated_at = now()
			  WHERE id = $2 AND is_encrypted = false`,
			encrypted, r.id)
		if updErr != nil {
			result.Failed++
			result.Errors = append(result.Errors, fmt.Sprintf("%s: update failed: %v", r.id, updErr))
			continue
		}
		// Count only rows this run actually flipped. A concurrent backfill on
		// another replica may have already encrypted the row (the is_encrypted
		// guard makes its UPDATE a no-op), so RowsAffected keeps the tally honest.
		if affected, raErr := res.RowsAffected(); raErr == nil && affected > 0 {
			result.Encrypted++
		}
	}

	logger.Info("confluence token backfill complete",
		"encrypted", result.Encrypted, "failed", result.Failed)
	return result, nil
}
