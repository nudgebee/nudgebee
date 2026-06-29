package user

import (
	"nudgebee/services/common"
	"nudgebee/services/security"
	"strconv"
	"time"
)

// Server-side session JWT denylist, backed by the shared cache namespace
// (`common/cache` — Redis in prod, bigcache in single-node). NextAuth uses a
// stateless JWT strategy, so a captured token survives logout until natural
// expiry unless we record the revocation server-side. Two revocation shapes
// share the namespace, keyed differently:
//
//   - per-jti key (`jti:<jti>`): revokes exactly one token. Written by
//     /api/auth/revoke for Bearer callers that know the token they're
//     killing. Value is unused ("1"); presence == revoked.
//   - user-wide key (`user:<user_id>`): revokes every token whose iat is
//     earlier than `revoked_at` for that user. Written by NextAuth's signOut
//     event. Value is the revocation's unix-epoch timestamp so the read
//     path can compare it against the token's iat — that's how we
//     distinguish "this session was revoked" from "a fresh login after the
//     revocation issued a new token, allow it".
//
// Per-key TTL handled by the cache layer — no manual prune, no migration.
// The trade-off vs a Postgres table: a Redis flush or non-persistent restart
// wipes the denylist, so revoked tokens become valid again until natural
// expiry. With JWT TTL pinned to 1h that's an acceptable worst case.

const sessionRevocationNamespace = "session_revocations"

// Max lifetime any single revocation row needs to outlive — covers the
// largest possible (NEXTAUTH_SESSION_EXPIRATION_HOURS) override. The cache
// layer enforces per-key TTL passed at Set time; this just bounds how long
// the namespace's default eviction policy keeps unset-expiration entries.
const sessionRevocationMaxLifetime = 24 * time.Hour

func init() {
	common.CacheCreateNamespace(
		sessionRevocationNamespace,
		common.CacheNamespaceWithExpiration(sessionRevocationMaxLifetime),
	)
}

type AuthDeleteSessionRequest struct {
	UserId string `json:"user_id" mapstructure:"user_id" validate:"required"`
	// Empty = user-wide revocation. Non-empty = revoke exactly this jti.
	Jti string `json:"jti" mapstructure:"jti"`
	// Unix seconds. Token's `exp` claim for per-jti, or now+max-session-TTL
	// for user-wide. Past this point the token would naturally expire, so
	// the revocation row can be evicted.
	ExpiresAt int64 `json:"expires_at" mapstructure:"expires_at" validate:"required"`
}

type AuthDeleteSessionResponse struct {
	Revoked bool `json:"revoked"`
}

type AuthCheckSessionRequest struct {
	UserId string `json:"user_id" mapstructure:"user_id" validate:"required"`
	// Optional. If empty, only user-wide revocations are considered.
	Jti string `json:"jti" mapstructure:"jti"`
	// Unix seconds — the token's `iat` claim. Compared against the stored
	// user-wide `revoked_at` to decide whether the token predates a logout.
	Iat int64 `json:"iat" mapstructure:"iat" validate:"required"`
}

type AuthCheckSessionResponse struct {
	Revoked bool `json:"revoked"`
}

func DeleteSession(ctx *security.RequestContext, request AuthDeleteSessionRequest) (AuthDeleteSessionResponse, error) {
	if err := common.ValidateStruct(request); err != nil {
		return AuthDeleteSessionResponse{}, err
	}

	now := time.Now().Unix()
	// Clamp TTL: if expires_at is already in the past (clock skew, bad
	// input), give the entry a token short lifetime rather than failing
	// silently. 1 minute is enough for the in-flight request to still be
	// caught by an immediate replay.
	ttl := time.Until(time.Unix(request.ExpiresAt, 0))
	if ttl < time.Minute {
		ttl = time.Minute
	}

	if request.Jti != "" {
		// Per-jti revocation. Value is presence-only.
		if err := common.CacheSet(sessionRevocationNamespace,
			"jti:"+request.Jti, []byte("1"),
			common.CacheSetWithExpiration(ttl),
		); err != nil {
			return AuthDeleteSessionResponse{}, err
		}
	} else {
		// User-wide revocation. Value is the unix-epoch revocation time so
		// the read path can compare against the token's iat. If the user
		// already had a recent user-wide revocation, the newer (greater)
		// value wins automatically — store.WithExpiration replaces the key.
		if err := common.CacheSet(sessionRevocationNamespace,
			"user:"+request.UserId, []byte(strconv.FormatInt(now, 10)),
			common.CacheSetWithExpiration(ttl),
		); err != nil {
			return AuthDeleteSessionResponse{}, err
		}
	}

	return AuthDeleteSessionResponse{Revoked: true}, nil
}

func CheckSession(ctx *security.RequestContext, request AuthCheckSessionRequest) (AuthCheckSessionResponse, error) {
	if err := common.ValidateStruct(request); err != nil {
		return AuthCheckSessionResponse{}, err
	}

	// User-wide check first: cheap presence + integer compare. Covers the
	// NextAuth signOut path (which always writes a user-wide key, never a
	// jti) so this is the hot path.
	if raw, ok := common.CacheGet(sessionRevocationNamespace, "user:"+request.UserId); ok {
		revokedAt, err := strconv.ParseInt(string(raw), 10, 64)
		if err == nil && revokedAt > request.Iat {
			return AuthCheckSessionResponse{Revoked: true}, nil
		}
	}

	// Per-jti check second. Skipped for pre-jti tokens (empty jti) — those
	// rely on the user-wide check above.
	if request.Jti != "" {
		if _, ok := common.CacheGet(sessionRevocationNamespace, "jti:"+request.Jti); ok {
			return AuthCheckSessionResponse{Revoked: true}, nil
		}
	}

	return AuthCheckSessionResponse{Revoked: false}, nil
}
