// Tests for authenticateRequest's credential selection — which of the two
// ways a caller can identify itself (raw API token, NextAuth cookie) wins,
// and what gets forwarded upstream as a result.
//
// rpcGateway.test.ts deliberately does not exercise this function, so its
// stubs live here instead of being widened there.

import type { NextApiRequest } from 'next';

jest.mock('next-auth/jwt', () => ({ getToken: jest.fn() }));
jest.mock('@lib/apiToken', () => ({ isApiToken: jest.fn(), authenticateApiToken: jest.fn() }));
jest.mock('@lib/sessionRevocation', () => ({ isSessionRevoked: jest.fn(async () => false) }));

import { authenticateRequest } from '@lib/rpcGateway';
import { getToken, type JWT } from 'next-auth/jwt';
import { authenticateApiToken, isApiToken } from '@lib/apiToken';
import { isSessionRevoked } from '@lib/sessionRevocation';

const mockGetToken = getToken as jest.Mock;
const mockIsApiToken = isApiToken as jest.Mock;
const mockAuthenticateApiToken = authenticateApiToken as jest.Mock;
const mockIsSessionRevoked = isSessionRevoked as jest.Mock;

const PAT_JWT = { id: 'pat-user', sub: 'pat-user', tenant: { id: 'tenant-1' }, roles: ['tenant_admin'] } as unknown as JWT;
const COOKIE_JWT = { id: 'cookie-user', sub: 'cookie-user', iat: 1700000000, roles: ['tenant_admin'] } as unknown as JWT;

function requestWithBearer(bearer?: string): NextApiRequest {
  return { headers: bearer ? { authorization: `Bearer ${bearer}` } : {} } as unknown as NextApiRequest;
}

beforeEach(() => {
  jest.clearAllMocks();
  mockGetToken.mockResolvedValue(null);
  mockIsApiToken.mockReturnValue(false);
  mockIsSessionRevoked.mockResolvedValue(false);
});

describe('authenticateRequest — raw API token', () => {
  it('forwards the minted session upstream, not the token itself', async () => {
    mockIsApiToken.mockReturnValue(true);
    mockAuthenticateApiToken.mockResolvedValue({ token: 'signed-session-jwt', jwt: PAT_JWT });

    const auth = await authenticateRequest(requestWithBearer('sk-nb-secret'));

    expect(auth).toEqual({ token: 'signed-session-jwt', jwt: PAT_JWT });
    expect(mockAuthenticateApiToken).toHaveBeenCalledWith('sk-nb-secret');
  });

  it('rejects an unresolvable PAT instead of falling back to the cookie', async () => {
    mockIsApiToken.mockReturnValue(true);
    mockAuthenticateApiToken.mockResolvedValue(null);
    mockGetToken.mockResolvedValue(COOKIE_JWT);

    expect(await authenticateRequest(requestWithBearer('sk-nb-revoked'))).toBeNull();
  });

  it('lets a presented PAT win over a NextAuth cookie on the same request', async () => {
    mockIsApiToken.mockReturnValue(true);
    mockAuthenticateApiToken.mockResolvedValue({ token: 'signed-session-jwt', jwt: PAT_JWT });
    mockGetToken.mockResolvedValue(COOKIE_JWT);

    const auth = await authenticateRequest(requestWithBearer('sk-nb-secret'));

    // Running as the cookie's user while forwarding the PAT's session upstream
    // would split identity across the two hops.
    expect(auth?.jwt).toBe(PAT_JWT);
    expect(mockIsSessionRevoked).not.toHaveBeenCalled();
  });
});

describe('authenticateRequest — cookie flow', () => {
  it('ignores a non-PAT bearer and authenticates on the cookie', async () => {
    // The /api/auth/token exchange is gone, so the encrypted session JWTs it
    // used to mint are no longer a credential. They must not authenticate, and
    // must not break a caller who also holds a valid cookie.
    mockGetToken.mockResolvedValue(COOKIE_JWT);

    const auth = await authenticateRequest(requestWithBearer('deadbeefciphertext'));

    expect(auth).toEqual({ token: '', jwt: COOKIE_JWT });
    expect(mockAuthenticateApiToken).not.toHaveBeenCalled();
  });

  it('rejects a non-PAT bearer outright when there is no cookie', async () => {
    mockGetToken.mockResolvedValue(null);

    expect(await authenticateRequest(requestWithBearer('deadbeefciphertext'))).toBeNull();
  });

  it('still runs the logout denylist check on the cookie-only flow', async () => {
    mockGetToken.mockResolvedValue(COOKIE_JWT);
    mockIsSessionRevoked.mockResolvedValue(true);

    expect(await authenticateRequest(requestWithBearer())).toBeNull();
    expect(mockIsSessionRevoked).toHaveBeenCalledWith('cookie-user', null, 1700000000);
  });
});
