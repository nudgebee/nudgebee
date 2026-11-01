// Tests for the direct Personal Access Token auth path — the one that lets a
// caller send `Authorization: Bearer sk-nb-…` without first exchanging the
// token as a Bearer.
//
// @lib/internal transitively loads `jose` as ESM, which Jest's Babel transform
// doesn't handle, so the two crypto primitives apiToken.ts uses are stubbed
// with identifiable fakes.

jest.mock('@lib/internal', () => ({
  encodeSessionJWT: jest.fn(async (user: any) => {
    // Mirror the one mutation the real implementation performs, since
    // createBearerSession reads roles back off the object afterwards.
    if ((user?.roles ?? []).length === 0) {
      user.roles.push('tenant_usage');
    }
    return `signed:${user.id}`;
  }),
  encrypt: jest.fn(async (value: string) => `encrypted:${value}`),
}));

jest.mock('@lib/UserService', () => ({
  getUserAuthByTokenSha256: jest.fn(),
  updateUserAccountAccessed: jest.fn(async () => ({})),
}));

import {
  API_TOKEN_PREFIX,
  authenticateApiToken,
  buildSessionUser,
  createBearerSession,
  hashApiToken,
  isApiToken,
  isUserAuthExpired,
  userAuthExpiresAtSec,
} from '@lib/apiToken';
import { getUserAuthByTokenSha256, updateUserAccountAccessed } from '@lib/UserService';
import { encodeSessionJWT } from '@lib/internal';
import cache from '@lib/cache';

const mockLookup = getUserAuthByTokenSha256 as jest.Mock;
const mockAccessed = updateUserAccountAccessed as jest.Mock;
const mockEncode = encodeSessionJWT as jest.Mock;

// exp / iat as encodeSessionJWT received them, so the tests can assert on the
// lifetime the session was actually signed with.
function signedLifetimeSec() {
  const [, , exp, iat] = mockEncode.mock.calls[mockEncode.mock.calls.length - 1];
  return exp - iat;
}

const TENANT = 'tenant-1';
const OTHER_TENANT = 'tenant-2';
const RAW_TOKEN = `${API_TOKEN_PREFIX}deadbeef`;

function userAuthRow(overrides: Record<string, any> = {}) {
  return {
    id: 'auth-1',
    status: 'active',
    tenant_id: TENANT,
    expires_at: null,
    user: {
      id: 'user-1',
      display_name: 'Ada',
      username: 'ada@example.com',
      status: 'active',
      user_roles: [{ role: 'tenant_admin', entity_type: 'tenant', entity_id: TENANT, tenant_id: TENANT }],
    },
    ...overrides,
  };
}

function lookupReturning(row: any) {
  mockLookup.mockResolvedValue({ data: { user_auths: row ? [row] : [] } });
}

beforeEach(() => {
  jest.clearAllMocks();
  cache.clear();
});

describe('isApiToken', () => {
  it('recognises a raw personal access token by its prefix', () => {
    expect(isApiToken(RAW_TOKEN)).toBe(true);
  });

  it('does not claim an encrypted session bearer', () => {
    // The exchange path hands out hex ciphertext, which must keep falling
    // through to decrypt().
    expect(isApiToken('a1b2c3d4e5f6')).toBe(false);
  });
});

describe('hashApiToken', () => {
  it('produces the lowercase hex sha256 the metastore stores', () => {
    // sha256("sk-nb-deadbeef"), the same value Go's common.HashTextFast writes
    // into user_auths.token_sha256 at token-create time.
    expect(hashApiToken(RAW_TOKEN)).toMatch(/^[0-9a-f]{64}$/);
    expect(hashApiToken(RAW_TOKEN)).toBe(hashApiToken(RAW_TOKEN));
    expect(hashApiToken(RAW_TOKEN)).not.toBe(hashApiToken(`${RAW_TOKEN}x`));
  });
});

describe('buildSessionUser', () => {
  it('splits tenant roles from account grants', () => {
    const session = buildSessionUser(
      userAuthRow({
        user: {
          id: 'user-1',
          user_roles: [
            { role: 'tenant_admin', entity_type: 'tenant', entity_id: TENANT, tenant_id: TENANT },
            { role: 'account_admin', entity_type: 'account', entity_id: 'acct-w', tenant_id: TENANT },
            { role: 'account_admin_readonly', entity_type: 'account', entity_id: 'acct-r', tenant_id: TENANT },
          ],
        },
      })
    );

    expect(session.roles).toEqual(['tenant_admin']);
    expect(session.accountIds).toEqual(['acct-w']);
    expect(session.readOnlyAccountIds).toEqual(['acct-r']);
    expect(session.tenant).toEqual({ id: TENANT });
  });

  it('ignores role rows granted in a different tenant', () => {
    const session = buildSessionUser(
      userAuthRow({
        user: {
          id: 'user-1',
          user_roles: [
            { role: 'tenant_admin', entity_type: 'tenant', entity_id: OTHER_TENANT, tenant_id: OTHER_TENANT },
            { role: 'account_admin', entity_type: 'account', entity_id: 'acct-elsewhere', tenant_id: OTHER_TENANT },
          ],
        },
      })
    );

    expect(session.roles).toEqual([]);
    expect(session.accountIds).toEqual([]);
  });
});

describe('createBearerSession', () => {
  it('returns the signed JWT the gateway forwards upstream', async () => {
    const session = await createBearerSession(userAuthRow());

    expect(session.signedJwt).toBe('signed:user-1');
    expect(session.expiresInSec).toBe(3600);
  });

  it('carries the marker role encodeSessionJWT adds for a user holding none', async () => {
    // Without reading roles back off the mutated session user, this JWT would
    // disagree with the signed token about what the caller is allowed to do.
    const session = await createBearerSession(userAuthRow({ user: { id: 'user-1', user_roles: [] } }));

    expect(session.jwt.roles).toEqual(['tenant_usage']);
  });
});

describe('token expiry', () => {
  it('reports no expiry for a token that never expires', () => {
    expect(userAuthExpiresAtSec(userAuthRow())).toBeNull();
    expect(isUserAuthExpired(userAuthRow())).toBe(false);
  });

  it('treats an unparseable expires_at as non-expiring rather than locking the holder out', () => {
    // The realistic cause is a change in how the gateway serialises timestamps.
    // Failing closed on that would 401 every token holder at once.
    const row = userAuthRow({ expires_at: 'not-a-date' });
    expect(userAuthExpiresAtSec(row)).toBeNull();
    expect(isUserAuthExpired(row)).toBe(false);
  });

  it("caps the minted session at the token's own expiry", async () => {
    const session = await createBearerSession(userAuthRow({ expires_at: new Date(Date.now() + 90_000).toISOString() }));

    // 90s of token life left, so the session gets ~90s — not the full hour.
    expect(session.expiresInSec).toBeLessThanOrEqual(90);
    expect(session.expiresInSec).toBeGreaterThan(60);
    expect(signedLifetimeSec()).toBe(session.expiresInSec);
  });

  it('gives a non-expiring token the full hour', async () => {
    const session = await createBearerSession(userAuthRow());

    expect(session.expiresInSec).toBe(3600);
    expect(signedLifetimeSec()).toBe(3600);
  });
});

describe('authenticateApiToken', () => {
  it('resolves an active token to a forwardable session', async () => {
    lookupReturning(userAuthRow());

    const auth = await authenticateApiToken(RAW_TOKEN);

    expect(auth).not.toBeNull();
    // The PAT itself is never forwarded — upstream handlers decode this bearer.
    expect(auth?.token).toBe('signed:user-1');
    expect(auth?.jwt.roles).toEqual(['tenant_admin']);
    expect(mockLookup).toHaveBeenCalledWith(hashApiToken(RAW_TOKEN));
  });

  it('records last-used without failing the request when the write errors', async () => {
    lookupReturning(userAuthRow());
    mockAccessed.mockRejectedValueOnce(new Error('metastore down'));

    await expect(authenticateApiToken(RAW_TOKEN)).resolves.not.toBeNull();
    expect(mockAccessed).toHaveBeenCalledWith('auth-1', TENANT);
  });

  it('rejects an unknown token', async () => {
    lookupReturning(null);
    expect(await authenticateApiToken(RAW_TOKEN)).toBeNull();
  });

  it('rejects a suspended token', async () => {
    lookupReturning(userAuthRow({ status: 'inactive' }));
    expect(await authenticateApiToken(RAW_TOKEN)).toBeNull();
  });

  it('rejects a token whose user is suspended', async () => {
    lookupReturning(userAuthRow({ user: { ...userAuthRow().user, status: 'inactive' } }));
    expect(await authenticateApiToken(RAW_TOKEN)).toBeNull();
  });

  it('rejects an expired token', async () => {
    lookupReturning(userAuthRow({ expires_at: new Date(Date.now() - 1000).toISOString() }));
    expect(await authenticateApiToken(RAW_TOKEN)).toBeNull();
  });

  it('accepts a token whose expiry is still in the future', async () => {
    lookupReturning(userAuthRow({ expires_at: new Date(Date.now() + 60_000).toISOString() }));
    expect(await authenticateApiToken(RAW_TOKEN)).not.toBeNull();
  });

  it('rejects an AI Gateway key — it authenticates model calls only', async () => {
    lookupReturning(userAuthRow({ kind: 'gateway' }));
    expect(await authenticateApiToken(RAW_TOKEN)).toBeNull();
  });

  // The guard rejects kind === 'gateway' rather than allow-listing 'platform', so an
  // all-access key passing is load-bearing behaviour, not an accident of the condition.
  it.each(['platform', 'all'])('accepts a %s key', async (kind) => {
    lookupReturning(userAuthRow({ kind }));
    expect(await authenticateApiToken(RAW_TOKEN)).not.toBeNull();
  });

  // Scopes reach the gateway through the session: a scoped key's session must carry
  // them, and a full-access key's must not (absence is what means full access).
  it("carries a scoped key's scopes into both the signed claims and the session", async () => {
    lookupReturning(userAuthRow({ scopes: ['tickets:Read', 'tickets:Write'] }));
    const auth = await authenticateApiToken(RAW_TOKEN);
    expect(mockEncode.mock.calls[0][1].token_scopes).toEqual(['tickets:Read', 'tickets:Write']);
    expect(auth?.jwt.tokenScopes).toEqual(['tickets:Read', 'tickets:Write']);
  });

  it('carries no scopes for a full-access key', async () => {
    lookupReturning(userAuthRow({ scopes: null }));
    const auth = await authenticateApiToken(RAW_TOKEN);
    expect(mockEncode.mock.calls[0][1]).not.toHaveProperty('token_scopes');
    expect(auth?.jwt).not.toHaveProperty('tokenScopes');
  });

  it('fails closed when the metastore lookup errors', async () => {
    mockLookup.mockResolvedValue({ errors: [{ message: 'boom' }] });
    expect(await authenticateApiToken(RAW_TOKEN)).toBeNull();
  });

  it('serves a repeat call from cache instead of hitting the metastore again', async () => {
    lookupReturning(userAuthRow());

    const first = await authenticateApiToken(RAW_TOKEN);
    const second = await authenticateApiToken(RAW_TOKEN);

    expect(second).toEqual(first);
    expect(mockLookup).toHaveBeenCalledTimes(1);
  });

  it('caches per token, so a second token does its own lookup', async () => {
    lookupReturning(userAuthRow());
    await authenticateApiToken(RAW_TOKEN);

    lookupReturning(userAuthRow({ id: 'auth-2', user: { ...userAuthRow().user, id: 'user-2' } }));
    const other = await authenticateApiToken(`${API_TOKEN_PREFIX}cafebabe`);

    expect(other?.token).toBe('signed:user-2');
    expect(mockLookup).toHaveBeenCalledTimes(2);
  });
});
