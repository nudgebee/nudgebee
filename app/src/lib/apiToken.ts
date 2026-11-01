import { createHash } from 'crypto';
import type { JWT } from 'next-auth/jwt';
import { encodeSessionJWT } from '@lib/internal';
import { getUserAuthByTokenSha256, updateUserAccountAccessed } from '@lib/UserService';
import { roleBelongsToTenant } from '@lib/userPermissionMapper';
import cache from '@lib/cache';

// Prefix stamped on every Nudgebee Personal Access Token when the api-server
// mints it (services/user/service.go: TokenPrefix). Two jobs: secret scanners
// can recognise a leaked key, and the gateway can tell a static PAT from an
// encrypted session bearer before spending a hash. Keep in sync with Go — the
// prefix is part of the token value, so both hashes cover it.
export const API_TOKEN_PREFIX = 'sk-nb-';

// Session JWTs minted for token callers live one hour, matching what
// the gateway mints for a PAT holder.
export const SESSION_JWT_TTL_SEC = 60 * 60;

// Resolved PATs are cached briefly because auth is a per-request hot path and
// the alternative is one metastore round-trip per API call. The cost is that a
// deleted or suspended token keeps working for up to this long — still far
// tighter than the hour an already-exchanged JWT survives today.
const PAT_CACHE_TTL_SEC = 60;

export function isApiToken(bearer: string): boolean {
  return bearer.startsWith(API_TOKEN_PREFIX);
}

// Lowercase hex sha256 — the exact encoding user_auths.token_sha256 stores
// (common.HashTextFast in Go) and the AI Gateway looks up by.
export function hashApiToken(rawToken: string): string {
  return createHash('sha256').update(rawToken).digest('hex');
}

/**
 * The token's own expiry as epoch seconds, or null when it never expires.
 *
 * `user_auths.expires_at` is NULL for every token `user_token_create` mints
 * today, so this is dormant in practice — but the column is real, the AI
 * Gateway already honours it, and a token that outlives its own expiry is the
 * kind of gap nobody notices until it matters.
 *
 * An unparseable value is treated as "no expiry" rather than "expired": the
 * realistic way this goes wrong is a change in how the gateway serialises
 * timestamps, and failing closed on that would lock every token holder out at
 * once. It logs instead.
 */
export function userAuthExpiresAtSec(userAccount: any): number | null {
  if (!userAccount?.expires_at) {
    return null;
  }
  const ms = new Date(userAccount.expires_at).getTime();
  if (Number.isNaN(ms)) {
    console.warn('[apiToken] unparseable expires_at, treating token as non-expiring', userAccount.id);
    return null;
  }
  return Math.floor(ms / 1000);
}

export function isUserAuthExpired(userAccount: any, nowSec = Math.floor(Date.now() / 1000)): boolean {
  const expiresAtSec = userAuthExpiresAtSec(userAccount);
  return expiresAtSec !== null && expiresAtSec <= nowSec;
}

// The `user` half of a session: which tenant the credential is bound to and
// which grants it carries there.
export type SessionUser = {
  id: string;
  roles: string[];
  tenant: { id: string };
  accountIds: string[];
  readOnlyAccountIds: string[];
  namespacedAccountIds: string[];
  namespacedReadOnlyAccountIds: string[];
};

/**
 * Derive the session grants from a user_auths row + its user's role rows.
 *
 * Kept as its own function so the session a PAT holder gets has exactly one
 * definition. If a second copy ever appears it will drift on RBAC, and the
 * drift will only show up as one flow being able to do something the other
 * cannot.
 */
export function buildSessionUser(userAccount: any): SessionUser {
  const accountIds: string[] = [];
  const readOnlyAccountIds: string[] = [];
  const namespacedAccountIds: string[] = [];
  const namespacedReadOnlyAccountIds: string[] = [];
  const roles: string[] = [];

  for (const ur of userAccount.user?.user_roles ?? []) {
    // The token is bound to one tenant — role rows granted in a different
    // tenant must not widen it (see roleBelongsToTenant for the fail-open
    // rationale).
    if (!roleBelongsToTenant(ur, userAccount.tenant_id)) {
      continue;
    }
    if (ur.entity_type && ur.entity_type == 'tenant' && ur.entity_id == userAccount.tenant_id) {
      roles.push(ur.role);
    } else if (ur.entity_type && ur.entity_type == 'account') {
      if (ur.role == 'account_admin_readonly') {
        readOnlyAccountIds.push(ur.entity_id);
      } else if (ur.role == 'account_admin') {
        accountIds.push(ur.entity_id);
      }
    }
  }

  return {
    id: userAccount.user?.id,
    roles,
    tenant: { id: userAccount.tenant_id },
    accountIds,
    readOnlyAccountIds,
    namespacedAccountIds,
    namespacedReadOnlyAccountIds,
  };
}

export function buildSessionClaims(userAccount: any) {
  return {
    name: userAccount.user?.display_name,
    email: userAccount.user?.username,
    sub: userAccount.user?.id,
    given_name: userAccount.user?.display_name,
  };
}

export type BearerSession = {
  // HS256-signed session JWT. This is what upstream handlers receive as
  // `Authorization: Bearer ...` when an action declares forward_client_headers.
  signedJwt: string;
  // JWT-shaped object for buildSessionVariables, built from the same inputs as
  // signedJwt so the direct-PAT path needs no verify round-trip.
  jwt: JWT;
  expiresInSec: number;
};

export async function createBearerSession(userAccount: any): Promise<BearerSession> {
  const sessionUser = buildSessionUser(userAccount);
  const claims = buildSessionClaims(userAccount);
  const nowSec = Math.floor(Date.now() / 1000);
  // A session must never outlive the credential that produced it. Without this
  // cap, a token expiring in a minute would still mint a session good for the
  // full hour, and the expiry check above would be the only thing that ever
  // enforced the expiry at all.
  const tokenExpiresAtSec = userAuthExpiresAtSec(userAccount);
  const expiresAtSec = Math.min(nowSec + SESSION_JWT_TTL_SEC, tokenExpiresAtSec ?? Number.POSITIVE_INFINITY);
  // A scoped key carries its scopes in the session so the gateway can hold every call
  // to them (rpcGateway actionAllowedByTokenScopes). Absence (NULL) is what means full
  // access; an empty array is carried as-is and allows nothing.
  const tokenScopes: string[] | null = Array.isArray(userAccount.scopes) ? userAccount.scopes : null;
  if (tokenScopes) {
    (claims as Record<string, unknown>).token_scopes = tokenScopes;
  }
  const signedJwt = await encodeSessionJWT(sessionUser, claims, expiresAtSec, nowSec);
  // Read roles back off sessionUser rather than from the local list: for a user
  // holding no roles at all, encodeSessionJWT appends the `tenant_usage` marker
  // in place. Reading it back keeps this object identical to what
  // synthesizeJwtFromBearer would derive from signedJwt.
  const jwt = {
    id: sessionUser.id,
    sub: sessionUser.id,
    tenant: { id: sessionUser.tenant.id },
    roles: sessionUser.roles,
    // encodeSessionJWT sets no `permissions` claim, so the bearer flow has
    // always presented an empty grant list here. Dynamic-RBAC grants are
    // re-resolved from the DB by the upstream Go handlers.
    permissions: [],
    accountIds: sessionUser.accountIds,
    readOnlyAccountIds: sessionUser.readOnlyAccountIds,
    namespacedAccountIds: sessionUser.namespacedAccountIds,
    namespacedReadOnlyAccountIds: sessionUser.namespacedReadOnlyAccountIds,
    ...(tokenScopes ? { tokenScopes } : {}),
  } as unknown as JWT;

  return {
    signedJwt,
    jwt,
    expiresInSec: expiresAtSec - nowSec,
  };
}

export type ApiTokenAuth = {
  // Signed session JWT to forward upstream. Never the PAT itself: upstream
  // handlers decode this bearer, and a raw PAT would fail that decode.
  token: string;
  jwt: JWT;
};

/**
 * Authenticate a raw `sk-nb-…` Personal Access Token presented as a Bearer.
 *
 * Resolves identity by sha256 lookup, then mints the session the gateway
 * forwards upstream. Returns null for anything unresolvable — unknown,
 * expired, suspended token, or a suspended user.
 */
export async function authenticateApiToken(rawToken: string): Promise<ApiTokenAuth | null> {
  const tokenSha256 = hashApiToken(rawToken);
  const cacheKey = `api_token_session_${tokenSha256}`;
  const cached = cache.get(cacheKey) as ApiTokenAuth | null;
  if (cached) {
    return cached;
  }

  const lookup = await getUserAuthByTokenSha256(tokenSha256);
  if (lookup?.errors) {
    // Fail closed: a metastore we cannot reach must not authenticate anyone.
    console.error('[apiToken] token lookup failed', JSON.stringify(lookup.errors));
    return null;
  }

  const userAccount = lookup?.data?.user_auths?.[0];
  if (!userAccount || !userAccount.user) {
    return null;
  }
  if (userAccount.status !== 'active' || userAccount.user.status !== 'active') {
    console.warn('[apiToken] token or user is not active', userAccount.id);
    return null;
  }
  // A gateway key authenticates LLM traffic only. Accepting it here would hand
  // app-API access to a credential minted for model calls.
  if (userAccount.kind === 'gateway') {
    console.warn('[apiToken] AI Gateway key presented to the app API', userAccount.id);
    return null;
  }
  const nowSec = Math.floor(Date.now() / 1000);
  if (isUserAuthExpired(userAccount, nowSec)) {
    return null;
  }

  const session = await createBearerSession(userAccount);
  const auth: ApiTokenAuth = { token: session.signedJwt, jwt: session.jwt };
  // Never cache a token past its own expiry: the cache is the one place a
  // credential can keep working after the row that authorises it stopped
  // being valid, so an expiring token gets a correspondingly shorter entry.
  const tokenExpiresAtSec = userAuthExpiresAtSec(userAccount);
  // Math.max(1, …) is not cosmetic: @lib/cache treats a TTL of 0 as "never
  // expire", so a token with under a second left would be cached for a year.
  const cacheTtlSec = tokenExpiresAtSec === null ? PAT_CACHE_TTL_SEC : Math.max(1, Math.min(PAT_CACHE_TTL_SEC, tokenExpiresAtSec - nowSec));
  cache.set(cacheKey, auth, cacheTtlSec);

  // Last-used tracking, best effort. Runs on cache misses only, so a hot token
  // costs at most one write a minute, and a failure here must not fail the
  // request the caller actually made.
  updateUserAccountAccessed(userAccount.id, userAccount.tenant_id).catch((err) => console.error('[apiToken] accessed_at update failed', err));

  return auth;
}
