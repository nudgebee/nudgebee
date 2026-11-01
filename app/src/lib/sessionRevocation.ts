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
 * Check the denylist for a decoded JWT. Returns true iff the token is
 * revoked. Called from decodeSessionJWT after signature/expiry verification
 * (relay-proxy bearer flow) and from authenticateRequest for the cookie
 * session. Failing closed on errors would lock everyone out if the DB
 * blips, so a backend error logs and returns false — the existing
 * signature+expiry checks remain authoritative.
 *
 * The check runs before every cookie request, so it is bounded: a
 * services-server pod that exits while still in the Service's endpoints
 * otherwise holds every request for undici's 10 s connect timeout, only to
 * fail open anyway. On dev the 24h p99 was 247 ms and nothing landed between
 * 1 s and 9 s, so the bound only cuts those stalls.
 */
const REVOCATION_CHECK_TIMEOUT_MS = 1000;

type SkipReason = 'timeout' | 'unreachable' | 'error';

// Every skipped check lets one request through without knowing whether the user
// logged out. One fixed-shape line per skip, so a log query can count them and
// alert when skips stop being rare (#39801). The error lines beside it keep the
// detail; this one only has to be countable.
function logSkippedCheck(reason: SkipReason, startedAtMs: number): void {
  console.warn(`[sessionRevocation] session_check_skipped reason=${reason} duration_ms=${Date.now() - startedAtMs}`);
}

function skipReason(signal: AbortSignal, errors?: Array<{ message?: unknown }>): SkipReason {
  if (signal.aborted) return 'timeout';
  const message = errors?.[0]?.message;
  return typeof message === 'string' && message.startsWith('Upstream unreachable') ? 'unreachable' : 'error';
}

export async function isSessionRevoked(userId: string, jti: string | null, iatSec: number): Promise<boolean> {
  const startedAtMs = Date.now();
  const signal = AbortSignal.timeout(REVOCATION_CHECK_TIMEOUT_MS);
  try {
    const response = await queryGraphQL(
      IS_REVOKED_MUTATION,
      'AuthCheckSession',
      { object: { user_id: userId, jti: jti ?? '', iat: iatSec } },
      undefined,
      signal
    );
    const errors = response?.data?.errors;
    const revoked = response?.data?.data?.auth_check_session?.revoked;
    // No verdict is a skipped check too: queryGraphQL resolves to undefined when
    // the gateway call itself throws, and that also lets the request through.
    if (errors?.length || revoked == null) {
      if (errors?.length) {
        console.error('[sessionRevocation] isSessionRevoked errors', JSON.stringify(errors));
      } else {
        console.error('[sessionRevocation] isSessionRevoked got no answer');
      }
      logSkippedCheck(skipReason(signal, errors), startedAtMs);
      return false;
    }
    return Boolean(revoked);
  } catch (err) {
    console.error('[sessionRevocation] isSessionRevoked threw', err);
    logSkippedCheck(signal.aborted ? 'timeout' : 'error', startedAtMs);
    return false;
  }
}
