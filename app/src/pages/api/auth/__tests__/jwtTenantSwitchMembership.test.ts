// Drives the real NextAuth jwt callback on a workspace switch with its backend
// calls stubbed, to prove role rows alone do not switch anyone into a workspace
// they are not a member of.
jest.mock('@lib/UserService', () => ({
  getUserByUsername: jest.fn(),
  getTenantAttributes: jest.fn(),
  onboardUser: jest.fn(),
  createUserAuthAccount: jest.fn(),
  listUserTenantRoles: jest.fn(),
  updateTenantUser: jest.fn(),
  getTenantIdByName: jest.fn(),
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

import { authOptions } from '@pages/api/auth/[...nextauth]';
import { listUserTenantRoles, updateTenantUser } from '@lib/UserService';

const jwt = authOptions.callbacks!.jwt! as unknown as (p: {
  token: Record<string, unknown>;
  trigger?: string;
  session?: Record<string, unknown>;
}) => Promise<Record<string, unknown>>;

const signedIn = (extra: Record<string, unknown> = {}) => ({
  id: 'u1',
  email: 'ada@example.com',
  tenant: { id: 't1', name: 'Home' },
  roles: ['tenant_admin'],
  ...extra,
});

const notAMember = () => (updateTenantUser as jest.Mock).mockResolvedValue({ data: { data: { users_update_default_tenant: { updated: 0 } } } });

describe('NextAuth jwt: workspace switch needs membership', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    // Role rows exist in the target workspace, as if written there by someone else.
    (listUserTenantRoles as jest.Mock).mockResolvedValue({ tenantName: 'Other', data: [{ entity_type: 'tenant', role: 'tenant_admin' }] });
  });

  it('stays put when the user holds role rows in a workspace they are not a member of', async () => {
    notAMember();
    const token = await jwt({ token: signedIn(), trigger: 'update', session: { tenantId: 't2' } });
    expect(token.tenant).toEqual({ id: 't1', name: 'Home' });
    expect(token.roles).toEqual(['tenant_admin']);
  });

  it('still lets a super admin in read-only where they are not a member', async () => {
    // Super admin is off unless the deployment turns it on.
    process.env.SUPER_ADMIN_ENABLED = 'true';
    notAMember();
    const token = await jwt({ token: signedIn({ isSuperAdmin: true }), trigger: 'update', session: { tenantId: 't2' } });
    expect(token.tenant).toEqual({ id: 't2', name: 'Other' });
    expect(token.roles).toEqual(['tenant_admin_readonly']);
    delete process.env.SUPER_ADMIN_ENABLED;
  });

  it('switches a member with their roles in the workspace', async () => {
    (updateTenantUser as jest.Mock).mockResolvedValue({ data: { data: { users_update_default_tenant: { updated: 1 } } } });
    const token = await jwt({ token: signedIn(), trigger: 'update', session: { tenantId: 't2' } });
    expect(token.tenant).toEqual({ id: 't2', name: 'Other' });
    expect(token.roles).toEqual(['tenant_admin']);
  });
});
