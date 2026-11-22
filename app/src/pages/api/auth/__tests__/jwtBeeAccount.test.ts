// Drives the real NextAuth jwt callback with its backend calls stubbed, to prove
// a bee account (an AI worker's user) never gets a session from any provider.
jest.mock('@lib/UserService', () => ({
  getUserByUsername: jest.fn(),
  getTenantAttributes: jest.fn(),
  onboardUser: jest.fn(),
  createUserAuthAccount: jest.fn(),
  resolveUserCustomPermissions: jest.fn(async () => ({ permissions: [], enabled: false })),
}));
jest.mock('@lib/authHooks', () => ({
  enrichAuthToken: jest.fn(),
  enrichSession: jest.fn(),
  onReturningOAuthSignIn: jest.fn(),
  onUnknownOAuthSignIn: jest.fn(async () => false),
  resolveLicensedTenantUser: jest.fn(),
}));
// next-auth core and jose ship ESM that jest does not transform; jwt needs neither.
jest.mock('next-auth', () => ({ __esModule: true, default: jest.fn() }));
jest.mock('jose', () => ({ createRemoteJWKSet: jest.fn(), jwtVerify: jest.fn() }));
jest.mock('@lib/sessionRevocation', () => ({ isSessionRevoked: jest.fn(), revokeAllUserSessions: jest.fn() }));

import type { Account, User } from 'next-auth';
import { authOptions } from '@pages/api/auth/[...nextauth]';
import { getUserByUsername } from '@lib/UserService';
import { enrichAuthToken } from '@lib/authHooks';

const jwt = authOptions.callbacks!.jwt! as unknown as (p: {
  token: Record<string, unknown>;
  user?: User;
  account?: Account | null;
}) => Promise<Record<string, unknown>>;

describe('NextAuth jwt: bee accounts', () => {
  beforeEach(() => jest.clearAllMocks());

  it.each(['email', 'credentials', 'ldap', 'teleport', 'google'])('gives a bee no session through the %s provider', async (provider) => {
    (getUserByUsername as jest.Mock).mockResolvedValue({ data: { users: [{ id: 'bee-1', status: 'active', kind: 'bee' }] } });
    const account = { type: provider === 'google' ? 'oauth' : 'email', provider, providerAccountId: 'x' } as Account;
    const token = await jwt({
      token: { email: 'bee:bad_rollout.0a1b2c3d' },
      user: { id: 'bee-1', email: 'bee:bad_rollout.0a1b2c3d' } as User,
      account,
    });
    expect(token).toEqual({});
    expect(enrichAuthToken).not.toHaveBeenCalled();
  });

  it('still builds a session for a person', async () => {
    (getUserByUsername as jest.Mock).mockResolvedValue({ data: { users: [{ id: 'u1', status: 'active', kind: 'person' }] } });
    const token = await jwt({ token: { email: 'ada@example.com' }, user: { id: 'u1', email: 'ada@example.com' } as User, account: null });
    expect(token.id).toBe('u1');
  });

  it('does not look users up on an existing session', async () => {
    await jwt({ token: { id: 'u1', email: 'ada@example.com' } });
    expect(getUserByUsername).not.toHaveBeenCalled();
  });
});
