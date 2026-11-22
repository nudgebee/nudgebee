package core

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"nudgebee/services/common"
	"nudgebee/services/internal/database"

	"github.com/lib/pq"
)

// SecretBackfillResult summarizes a re-encryption backfill run.
type SecretBackfillResult struct {
	Encrypted int `json:"encrypted"`
	// Flagged counts rows that already held ciphertext but were marked
	// is_encrypted=false, so readers used the ciphertext as the credential.
	Flagged int `json:"flagged,omitempty"`
	// Skipped counts rows left alone because their value is shaped like
	// ciphertext this key cannot decrypt (another key, or a hex secret).
	Skipped int      `json:"skipped,omitempty"`
	Failed  int      `json:"failed"`
	Errors  []string `json:"errors,omitempty"`
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

	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			logger.Warn("confluence token backfill: failed to close rows", "error", closeErr)
		}
	}()
	var collected []tokenRow
	for rows.Next() {
		var r tokenRow
		if scanErr := rows.Scan(&r.id, &r.value); scanErr != nil {
			logger.Error("confluence token backfill: scan failed", "error", scanErr)
			continue
		}
		collected = append(collected, r)
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

// secretRowAction is what BackfillSecretEncryption does with one stored secret.
type secretRowAction int

const (
	secretRowKeep secretRowAction = iota
	// secretRowFlag: the value is ciphertext under the current key; set the flag.
	secretRowFlag
	// secretRowEncrypt: the value is plaintext; encrypt it and set the flag.
	secretRowEncrypt
	// secretRowSkip: the value looks like ciphertext but does not decrypt.
	secretRowSkip
)

// ciphertextShape matches common.Encrypt output: hex of a 12-byte nonce, the
// sealed message and a 16-byte tag, so at least 56 hex characters.
var ciphertextShape = regexp.MustCompile(`^(?:[0-9a-f]{2}){28,}$`)

// classifySecretRow decides from the value itself, not the is_encrypted flag,
// because legacy rows carry both ciphertext under false and plaintext under true.
// AES-GCM authenticates, so a value that decrypts is ciphertext. A value that
// does not decrypt but has the shape of ciphertext is left alone: encrypting it
// again would bury a secret written under another key.
func classifySecretRow(value string, isEncrypted bool, decrypt func(string) (string, error)) secretRowAction {
	if value == "" {
		return secretRowKeep
	}
	if _, err := decrypt(value); err == nil {
		if isEncrypted {
			return secretRowKeep
		}
		return secretRowFlag
	}
	if ciphertextShape.MatchString(value) {
		return secretRowSkip
	}
	return secretRowEncrypt
}

// secretConfigFieldNames lists every field name some integration schema marks
// IsEncrypted, to narrow the scan; isSecretConfigField decides per row.
func secretConfigFieldNames() []string {
	seen := map[string]bool{}
	for _, integration := range nbSystemTools {
		for name, prop := range integration.ConfigSchema().Properties {
			if prop.IsEncrypted {
				seen[name] = true
			}
		}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	return names
}

// BackfillSecretEncryption makes every stored secret config value ciphertext
// flagged is_encrypted=true, where secret means isSecretConfigField: the field's
// schema IsEncrypted, or an LLM key. It covers rows written before a field was
// marked encrypted, and rows whose flag disagrees with the value.
//
// It is idempotent and safe to run on several replicas at once: each UPDATE only
// applies while the row still holds the value that was read, so a concurrent
// run or a user's save is never overwritten. Readers decrypt by the flag
// (api-server, ticket-server, llm-server), so after the run a legacy plaintext
// row reads exactly as before.
func BackfillSecretEncryption(ctx context.Context, logger *slog.Logger) (SecretBackfillResult, error) {
	result := SecretBackfillResult{}
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return result, err
	}

	type secretRow struct {
		id, integrationType, source, name, value string
		isEncrypted                              bool
	}
	rows, err := dbms.Db.QueryContext(ctx, `
		SELECT icv.id::text, i.type, COALESCE(i.source, ''), icv.name, icv.value, COALESCE(icv.is_encrypted, false)
		  FROM integration_config_values icv
		  JOIN integrations i ON i.id = icv.integration_id
		 WHERE COALESCE(icv.value, '') <> ''
		   AND (icv.name = ANY($1) OR i.type = 'llm')`,
		pq.Array(secretConfigFieldNames()))
	if err != nil {
		return result, err
	}
	defer func() { _ = rows.Close() }()

	var candidates []secretRow
	schemas := map[string]IntegrationSchema{}
	for rows.Next() {
		var r secretRow
		if err := rows.Scan(&r.id, &r.integrationType, &r.source, &r.name, &r.value, &r.isEncrypted); err != nil {
			return result, err
		}
		key := strings.ToLower(r.integrationType) + ":" + strings.ToLower(r.source)
		schema, ok := schemas[key]
		if !ok {
			if integration, found := GetIntegrationBySource(r.integrationType, r.source); found {
				schema = integration.ConfigSchema()
			}
			schemas[key] = schema
		}
		if isSecretConfigField(r.integrationType, schema, r.name) {
			candidates = append(candidates, r)
		}
	}
	if err := rows.Err(); err != nil {
		return result, err
	}

	// applied reports whether the UPDATE changed the row; it is a no-op once the
	// row no longer holds the value read above.
	applied := func(query string, args ...any) (bool, error) {
		res, err := dbms.Db.ExecContext(ctx, query, args...)
		if err != nil {
			return false, err
		}
		n, err := res.RowsAffected()
		return n > 0, err
	}
	for _, r := range candidates {
		// Stop at shutdown rather than fail every remaining update.
		if err := ctx.Err(); err != nil {
			return result, err
		}
		var changed bool
		var updErr error
		switch classifySecretRow(r.value, r.isEncrypted, common.Decrypt) {
		case secretRowFlag:
			changed, updErr = applied(`UPDATE integration_config_values SET is_encrypted = true, updated_at = now()
				WHERE id = $1 AND value = $2`, r.id, r.value)
			if changed {
				result.Flagged++
			}
		case secretRowEncrypt:
			encrypted, encErr := common.Encrypt(r.value)
			if encErr != nil {
				result.Failed++
				result.Errors = append(result.Errors, fmt.Sprintf("%s: encrypt failed: %v", r.id, encErr))
				continue
			}
			changed, updErr = applied(`UPDATE integration_config_values SET value = $1, is_encrypted = true, updated_at = now()
				WHERE id = $2 AND value = $3`, encrypted, r.id, r.value)
			if changed {
				result.Encrypted++
			}
		case secretRowSkip:
			result.Skipped++
			logger.Debug("secret backfill: value looks like ciphertext but does not decrypt; left as is",
				"config_value_id", r.id, "type", r.integrationType, "name", r.name)
		}
		if updErr != nil {
			result.Failed++
			result.Errors = append(result.Errors, fmt.Sprintf("%s: update failed: %v", r.id, updErr))
		}
	}

	level := slog.LevelInfo
	if result.Skipped > 0 || result.Failed > 0 {
		// Skipped rows stay as they are on every boot: ciphertext under another
		// key, or a hex secret this run cannot tell from one. Fix those by hand.
		level = slog.LevelWarn
	}
	logger.Log(ctx, level, "secret backfill complete", "scanned", len(candidates), "encrypted", result.Encrypted,
		"flagged", result.Flagged, "skipped", result.Skipped, "failed", result.Failed)
	return result, nil
}
