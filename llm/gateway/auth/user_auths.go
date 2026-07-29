package auth

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"log/slog"

	"nudgebee/llm-gateway/common"
)

// tokenHashColumn is the sha256-lookup column added to user_auths. Named to match
// the shared migration; if the real column lands under a different name, align here.
const tokenHashColumn = "token_sha256"

// userAuthsValidator resolves a raw NB Personal Access Token to identity by
// sha256(token) lookup on user_auths (provider='token'). sha256 is the correct
// primitive for a high-entropy random token (see gateway-auth-model); bcrypt in
// the `credential` column stays for the existing username-first login path.
type userAuthsValidator struct {
	// db is injectable for tests; defaults to the shared metastore (GATEWAY_DB_URL).
	db func() (*common.DatabaseManager, error)
}

func newUserAuthsValidator() *userAuthsValidator {
	return &userAuthsValidator{
		db: func() (*common.DatabaseManager, error) {
			return common.GetDatabaseManager(common.Metastore)
		},
	}
}

func (v *userAuthsValidator) RequiresToken() bool { return true }

func (v *userAuthsValidator) Validate(raw string) (Identity, bool) {
	if raw == "" {
		return Identity{}, false
	}
	sum := sha256.Sum256([]byte(raw))
	hash := hex.EncodeToString(sum[:])

	db, err := v.db()
	if err != nil {
		// Fail closed: if we can't reach the token store, reject rather than allow.
		slog.Error("auth: metastore unavailable for token lookup", "error", err)
		return Identity{}, false
	}

	var row struct {
		TenantID string `db:"tenant_id"`
		UserID   string `db:"user"`
		Name     string `db:"name"`
	}
	// uuid columns cast to text so they scan into strings. Only active, unexpired
	// PAT rows match. "user" is quoted (reserved word).
	query := `
		SELECT tenant_id::text AS tenant_id, "user"::text AS "user", name
		FROM user_auths
		WHERE ` + tokenHashColumn + ` = $1
		  AND provider_type = 'credentials'
		  AND provider = 'token'
		  AND status = 'active'
		  AND (expires_at IS NULL OR expires_at > now())
		LIMIT 1`
	if err := db.QueryRowAndScan(&row, query, hash); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Identity{}, false // unknown/expired/revoked token
		}
		slog.Error("auth: user_auths lookup failed", "error", err)
		return Identity{}, false
	}
	// TODO(task 5): cache token→identity (short TTL) — auth is a per-request hot
	// path; and best-effort async UPDATE accessed_at for last-used tracking.
	return Identity{TenantID: row.TenantID, UserID: row.UserID, TokenID: row.Name}, true
}
