package auth

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"log/slog"
	"sync"
	"time"

	"nudgebee/llm-gateway/common"

	"github.com/lib/pq"
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
	// recordAccess is called on valid token; injectable for tests.
	recordAccess func(db *common.DatabaseManager, hash string)

	mu          sync.Mutex
	lastUpdated map[string]time.Time
	lastSweep   time.Time
	wg          *sync.WaitGroup // Used to synchronize async updates in tests
}

func newUserAuthsValidator() *userAuthsValidator {
	return &userAuthsValidator{
		db: func() (*common.DatabaseManager, error) {
			return common.GetDatabaseManager(common.Metastore)
		},
		lastUpdated: make(map[string]time.Time),
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
		APIKeyID string `db:"api_key_id"`
		// Read in the same lookup so a policy edit binds on the key's next request. NULL when
		// the key has no rows for that dimension, which leaves it unrestricted.
		Providers pq.StringArray `db:"providers"`
		Models    pq.StringArray `db:"models"`
		Endpoints pq.StringArray `db:"endpoints"`
	}
	// uuid columns cast to text so they scan into strings; "user" is a reserved word.
	// kind excludes platform keys: an app-API credential must not authenticate LLM traffic.
	// Only key-scope policy rows apply (user- and tenant-scope rows wait for #38963), matched by
	// key and owner but not tenant: a key's tenant_id follows its owner's current tenant.
	query := `
		SELECT ua.tenant_id::text AS tenant_id, ua."user"::text AS "user", ua.name, ua.id::text AS api_key_id,
		       pol.providers, pol.models, pol.endpoints
		FROM user_auths ua
		LEFT JOIN LATERAL (
			SELECT array_agg(p.value) FILTER (WHERE p.dimension = 'provider') AS providers,
			       array_agg(p.value) FILTER (WHERE p.dimension = 'model') AS models,
			       array_agg(p.value) FILTER (WHERE p.dimension = 'endpoint') AS endpoints
			FROM llm_gateway_access_policies p
			WHERE p.api_key_id = ua.id::text AND p.user_id = ua."user"::text
		) pol ON true
		WHERE ua.` + tokenHashColumn + ` = $1
		  AND ua.provider_type = 'credentials'
		  AND ua.provider = 'token'
		  AND ua.kind IN ('gateway', 'all')
		  AND ua.status = 'active'
		  AND (ua.expires_at IS NULL OR ua.expires_at > now())
		LIMIT 1`
	if err := db.QueryRowAndScan(&row, query, hash); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Identity{}, false // unknown/expired/revoked token
		}
		slog.Error("auth: user_auths lookup failed", "error", err)
		return Identity{}, false
	}
	v.recordTokenAccess(db, hash)
	return Identity{
		TenantID: row.TenantID, UserID: row.UserID, TokenID: row.Name, APIKeyID: row.APIKeyID,
		Policy: KeyPolicy{Providers: row.Providers, Models: row.Models, Endpoints: row.Endpoints},
	}, true
}

func (v *userAuthsValidator) recordTokenAccess(db *common.DatabaseManager, hash string) {
	if v.recordAccess != nil {
		v.recordAccess(db, hash)
		return
	}

	v.mu.Lock()
	if v.lastUpdated == nil {
		v.lastUpdated = make(map[string]time.Time)
	}

	now := time.Now()
	if lastUpdate, ok := v.lastUpdated[hash]; ok {
		if now.Sub(lastUpdate) < 1*time.Minute {
			v.mu.Unlock()
			return
		}
	}

	v.lastUpdated[hash] = now

	// Amortized sweep: prune expired entries when the map grows to keep memory bounded.
	if len(v.lastUpdated) > 1000 && now.Sub(v.lastSweep) > 1*time.Minute {
		v.lastSweep = now
		for k, t := range v.lastUpdated {
			if now.Sub(t) >= 1*time.Minute {
				delete(v.lastUpdated, k)
			}
		}
	}
	v.mu.Unlock()

	// Best-effort async UPDATE accessed_at for last-used tracking.
	// Throttled: in-memory check above avoids goroutine/pool churn; DB check guards cross-instance concurrency.
	wg := v.wg
	if wg != nil {
		wg.Add(1)
	}
	go func() {
		if wg != nil {
			defer wg.Done()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		updateQuery := `
			UPDATE user_auths
			SET accessed_at = now()
			WHERE ` + tokenHashColumn + ` = $1
			  AND (accessed_at IS NULL OR accessed_at < now() - interval '1 minute')`
		if _, err := db.Db.ExecContext(ctx, updateQuery, hash); err != nil {
			slog.Warn("auth: failed to update token accessed_at", "error", err, "hash", hash)
		}
	}()
}
