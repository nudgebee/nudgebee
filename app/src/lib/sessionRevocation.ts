import { queryGraphQL } from '@lib/HttpService';

// Server-side session JWT denylist client. Backs the fix for the F03
// pentest finding (session does not expire after logout): NextAuth uses a
// stateless JWT strategy, so a captured token (cookie or Bearer) survived
// logout until its natural expiry. These calls insert / look up revocation
// rows in the `session_revocations` Postgres table so decodeSessionJWT can
// refuse a revoked token before its `exp`.
//
// All three functions invoke server-only actions (auth_delete_session /
// auth_check_session) registered without a `permissions:` block in
// actions.yaml — only super_admin can call them. That's intentional:
// a user must not be able to forge a logout for someone else, and the
// `decodeSessionJWT` check itself runs at request-authentication time
// (before any user role is established), so it has to bypass the user-auth
// gate. Both call sites reach the gateway via `bypassGraphQLAsServer`,
// which synthesises a super_admin session.

const REVOKE_MUTATION = `
mutation AuthDeleteSession($object: auth_delete_session_input!) {
  auth_delete_session(object: $object) {
    revoked
  }
}`;

const IS_REVOKED_MUTATION = `
mutation AuthCheckSession($object: auth_check_session_input!) {
  auth_check_session(object: $object) {
    revoked
  }
}`;

/**
 * Revoke every JWT for `userId` whose `iat` is earlier than now. Used by the
 * NextAuth signOut event. `expiresAtSec` should be the maximum possible
 * lifetime of any in-flight token (= now + JWT TTL) so the row can be pruned
 * once it can't possibly matter anymore.
 *
 * jti is intentionally omitted: even though we know the *current* token, the
 * 5-minute rotation-overlap window in jwtGenerateHasuraTokenAndUpdate means
 * an older sibling token may also be live. User-wide revocation closes both
 * atomically.
 */
export async function revokeAllUserSessions(userId: string, expiresAtSec: number): Promise<void> {
  const response = await queryGraphQL(REVOKE_MUTATION, 'AuthDeleteSession', {
    object: { user_id: userId, expires_at: expiresAtSec },
  });
  if (response?.data?.errors?.length) {
    console.error('[sessionRevocation] revokeAllUserSessions failed', JSON.stringify(response.data.errors));
  }
}

/**
 * Revoke exactly one token by its jti. Used by POST /api/auth/revoke for
 * Bearer callers (nbctl, automation) that know which token to kill without
 * touching the user's browser session. `expiresAtSec` should be the token's
 * own `exp` claim — the row becomes purgeable when the token would have
 * expired anyway.
 */
export async function revokeBearerSession(userId: string, jti: string, expiresAtSec: number): Promise<void> {
  const response = await queryGraphQL(REVOKE_MUTATION, 'AuthDeleteSession', {
    object: { user_id: userId, jti, expires_at: expiresAtSec },
  });
  if (response?.data?.errors?.length) {
    console.error('[sessionRevocation] revokeBearerSession failed', JSON.stringify(response.data.errors));
  }
}

/**
 * Check the denylist for a decoded JWT. Returns true iff the token is
 * revoked. Called from decodeSessionJWT after signature/expiry verification
 * (Bearer flow) and from authenticateRequest after the cookie's hasuraIdToken
 * is extracted. Failing closed on errors would lock everyone out if the DB
 * blips, so a backend error logs and returns false — the existing
 * signature+expiry checks remain authoritative.
 */
export async function isSessionRevoked(userId: string, jti: string | null, iatSec: number): Promise<boolean> {
  try {
    const response = await queryGraphQL(IS_REVOKED_MUTATION, 'AuthCheckSession', {
      object: { user_id: userId, jti: jti ?? '', iat: iatSec },
    });
    if (response?.data?.errors?.length) {
      console.error('[sessionRevocation] isSessionRevoked errors', JSON.stringify(response.data.errors));
      return false;
    }
    return Boolean(response?.data?.data?.auth_check_session?.revoked);
  } catch (err) {
    console.error('[sessionRevocation] isSessionRevoked threw', err);
    return false;
  }
}
