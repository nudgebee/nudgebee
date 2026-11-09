// Drives the real NextAuth signIn callback with its backend calls stubbed, to
// prove the unverified-email gate is wired in front of every email-keyed path
// and does not touch sign-ins of already-linked IdP accounts.
jest.mock('@lib/UserService', () => ({
  getUserByUsername: jest.fn(),
  getTenantAttributes: jest.fn(),
  onboardUser: jest.fn(),
  createUserAuthAccount: jest.fn(),
}));
jest.mock('@lib/authHooks', () => ({
  enrichAuthToken: jest.fn(),
  enrichSession: jest.fn(),
  onReturningOAuthSignIn: jest.fn(),
  onUnknownOAuthSignIn: jest.fn(async () => false),
  resolveLicensedTenantUser: jest.fn(),
}));
// next-auth core and jose ship ESM that jest does not transform; signIn needs neither.
jest.mock('next-auth', () => ({ __esModule: true, default: jest.fn() }));
jest.mock('jose', () => ({ createRemoteJWKSet: jest.fn(), jwtVerify: jest.fn() }));
jest.mock('@lib/sessionRevocation', () => ({ isSessionRevoked: jest.fn(), revokeAllUserSessions: jest.fn() }));

import type { Account, Profile, User } from 'next-auth';
import { authOptions } from '@pages/api/auth/[...nextauth]';
import { getTenantAttributes, getUserByUsername, onboardUser } from '@lib/UserService';
import { onReturningOAuthSignIn, onUnknownOAuthSignIn } from '@lib/authHooks';

const signIn = authOptions.callbacks!.signIn! as unknown as (p: { user: User; account: Account; profile?: Profile }) => Promise<boolean | string>;

const account = { type: 'oauth', provider: 'auth0', providerAccountId: 'auth0|other' } as Account;
const unlinkedUser = { id: 'auth0|other', email: 'Member@Corp.com' } as User;
const linkedUser = { id: 'nb-user-1', email: 'member@corp.com' } as User;
const unverified = { email_verified: false } as unknown as Profile;
const verified = { email_verified: true } as unknown as Profile;

describe('NextAuth signIn: unverified OAuth email', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    (getUserByUsername as jest.Mock).mockResolvedValue({ data: { users: [{ id: 'nb-user-1', status: 'active' }] } });
    (getTenantAttributes as jest.Mock).mockResolvedValue([{ name: 'allowed_domains', value: '["corp.com"]', tenant_id: 't1' }]);
  });

  it('refuses to resolve an unlinked IdP identity to the existing user by an unverified email', async () => {
    await expect(signIn({ user: unlinkedUser, account, profile: unverified })).resolves.toBe(false);
    expect(getUserByUsername).not.toHaveBeenCalled();
    expect(onReturningOAuthSignIn).not.toHaveBeenCalled();
  });

  it('refuses to onboard or bootstrap a new user from an unverified email', async () => {
    (getUserByUsername as jest.Mock).mockResolvedValue({ data: { users: [] } });
    await expect(signIn({ user: unlinkedUser, account, profile: unverified })).resolves.toBe(false);
    expect(onUnknownOAuthSignIn).not.toHaveBeenCalled();
    expect(onboardUser).not.toHaveBeenCalled();
  });

  it('still signs in an already-linked IdP account even if its email claim is unverified', async () => {
    await expect(signIn({ user: linkedUser, account, profile: unverified })).resolves.toBe(true);
    expect(onReturningOAuthSignIn).toHaveBeenCalled();
  });

  it('still links a first sign-in whose email is verified, or carries no claim', async () => {
    await expect(signIn({ user: { ...unlinkedUser }, account, profile: verified })).resolves.toBe(true);
    await expect(signIn({ user: { ...unlinkedUser }, account, profile: {} as Profile })).resolves.toBe(true);
  });
});
