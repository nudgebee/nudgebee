// Drives the real NextAuth jwt callback on an ordinary session poll to prove the
// built-in roles baked into the token are re-read from the database on the
// grants' cadence, so a demotion stops working without a re-login.
jest.mock('@lib/UserService', () => ({
  getUserByUsername: jest.fn(),
  getTenantAttributes: jest.fn(),
  onboardUser: jest.fn(),
  createUserAuthAccount: jest.fn(),
  listUserTenantRoles: jest.fn(),
  updateTenantUser: jest.fn(),
  getTenantIdByName: jest.fn(),
  resolveUserCustomPermissions: jest.fn(async () => ({ permissions: [], enabled: true })),
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
jest.mock('@lib/sessionRevocation', () => ({ isSessionRevoked: jest.fn(async () => false), revokeAllUserSessions: jest.fn() }));

import { authOptions } from '@pages/api/auth/[...nextauth]';
import { listUserTenantRoles } from '@lib/UserService';

const jwt = authOptions.callbacks!.jwt! as unknown as (p: { token: Record<string, unknown>; trigger?: string }) => Promise<Record<string, unknown>>;

// A tenant admin whose grants (and so roles) were last read six minutes ago.
const staleAdmin = (extra: Record<string, unknown> = {}) => ({
  id: 'u1',
  email: 'ada@example.com',
  iat: Math.floor(Date.now() / 1000) - 60,
  tenant: { id: 't1', name: 'Home' },
  roles: ['tenant_admin'],
  accountIds: [],
  permissionsResolvedAt: Date.now() - 6 * 60 * 1000,
  ...extra,
});

describe('NextAuth jwt: built-in role refresh', () => {
  beforeEach(() => jest.clearAllMocks());

  it('drops a role the user no longer holds', async () => {
    (listUserTenantRoles as jest.Mock).mockResolvedValue({
      errored: false,
      tenantName: 'Home',
      data: [{ entity_type: 'account', entity_id: 'acc-1', role: 'account_admin_readonly' }],
    });
    const token = await jwt({ token: staleAdmin() });
    expect(listUserTenantRoles).toHaveBeenCalledWith('ada@example.com', 't1');
    expect(token.roles).toEqual(['account_admin_readonly']);
    expect(token.readOnlyAccountIds).toEqual(['acc-1']);
    expect(token.accountIds).toEqual([]);
  });

  it('keeps tenant roles, whose entity_id the lookup blanks', async () => {
    (listUserTenantRoles as jest.Mock).mockResolvedValue({
      errored: false,
      data: [{ entity_type: 'tenant', entity_id: '', role: 'tenant_admin_readonly' }],
    });
    const token = await jwt({ token: staleAdmin() });
    expect(token.roles).toEqual(['tenant_admin_readonly']);
  });

  it('leaves a user with no role rows (custom roles only) with none', async () => {
    (listUserTenantRoles as jest.Mock).mockResolvedValue({ errored: false, data: [] });
    const token = await jwt({ token: staleAdmin() });
    expect(token.roles).toEqual([]);
  });

  it('keeps the current roles when the lookup fails', async () => {
    (listUserTenantRoles as jest.Mock).mockResolvedValue({ errored: true, data: [] });
    const token = await jwt({ token: staleAdmin() });
    expect(token.roles).toEqual(['tenant_admin']);
    expect(token.id).toBe('u1');
  });

  it('keeps the session and its roles when the lookup throws', async () => {
    (listUserTenantRoles as jest.Mock).mockRejectedValue(new Error('socket hang up'));
    const token = await jwt({ token: staleAdmin() });
    expect(token.id).toBe('u1');
    expect(token.roles).toEqual(['tenant_admin']);
  });

  it('does not look again inside the refresh window', async () => {
    await jwt({ token: staleAdmin({ permissionsResolvedAt: Date.now() - 60 * 1000 }) });
    expect(listUserTenantRoles).not.toHaveBeenCalled();
  });

  it('leaves a super-admin session alone', async () => {
    const original = process.env.SUPER_ADMIN_ENABLED;
    process.env.SUPER_ADMIN_ENABLED = 'true';
    try {
      const token = await jwt({ token: staleAdmin({ isSuperAdminReadonly: true, roles: ['tenant_admin_readonly'] }) });
      expect(listUserTenantRoles).not.toHaveBeenCalled();
      expect(token.roles).toEqual(['tenant_admin_readonly']);
    } finally {
      if (original === undefined) delete process.env.SUPER_ADMIN_ENABLED;
      else process.env.SUPER_ADMIN_ENABLED = original;
    }
  });
});
