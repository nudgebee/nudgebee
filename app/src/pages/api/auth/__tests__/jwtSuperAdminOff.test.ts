// Drives the real NextAuth jwt callback to prove that, with super admin off,
// a session minted as super admin is ended on its next refresh. The
// callback throws: NextAuth's session route then clears the cookie and answers
// with no session, which is what sends the browser to sign-in.
jest.mock('@lib/UserService', () => ({
  getUserByUsername: jest.fn(),
  getTenantAttributes: jest.fn(),
  onboardUser: jest.fn(),
  createUserAuthAccount: jest.fn(),
  getUserSuperAdminRole: jest.fn(),
  getTenantIdByName: jest.fn(),
  listUserTenantRoles: jest.fn(),
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
jest.mock('@lib/sessionRevocation', () => ({ isSessionRevoked: jest.fn(async () => false), revokeAllUserSessions: jest.fn() }));

import type { User } from 'next-auth';
import { authOptions } from '@pages/api/auth/[...nextauth]';
import { getTenantIdByName, getUserSuperAdminRole, listUserTenantRoles } from '@lib/UserService';

const jwt = authOptions.callbacks!.jwt! as unknown as (p: {
  token: Record<string, unknown>;
  trigger?: string;
  session?: Record<string, unknown>;
}) => Promise<Record<string, unknown>>;
const signIn = authOptions.events!.signIn! as unknown as (m: { user: User }) => Promise<void>;

// Existing session of a super admin parked in a customer workspace.
const PARKED = {
  id: 'staff-1',
  email: 'staff@nudgebee.com',
  isSuperAdmin: true,
  tenant: { id: 'customer', name: 'customer' },
  roles: ['tenant_admin_readonly'],
};

describe('NextAuth jwt: super admin switched off', () => {
  let savedEnv: NodeJS.ProcessEnv;
  beforeEach(() => {
    savedEnv = { ...process.env };
    delete process.env.SUPER_ADMIN_ENABLED;
    jest.clearAllMocks();
  });
  afterEach(() => {
    process.env = savedEnv;
  });

  it('ends an existing super-admin session', async () => {
    await expect(jwt({ token: { ...PARKED } })).rejects.toThrow('super admin is disabled');
  });

  it('ends an existing read-only super-admin session', async () => {
    await expect(jwt({ token: { ...PARKED, isSuperAdmin: false, isSuperAdminReadonly: true } })).rejects.toThrow('super admin is disabled');
  });

  it('ends it before a workspace switch runs any lookup for it', async () => {
    await expect(jwt({ token: { ...PARKED }, trigger: 'update', session: { tenantName: 'other-customer' } })).rejects.toThrow();
    expect(getTenantIdByName).not.toHaveBeenCalled();
    expect(listUserTenantRoles).not.toHaveBeenCalled();
  });

  it('drops a super_admin role string and keeps the session', async () => {
    const token = await jwt({ token: { id: 'u1', email: 'a@example.com', tenant: { id: 't1' }, roles: ['tenant_admin', 'super_admin'] } });
    expect(token.id).toBe('u1');
    expect(token.roles).toEqual(['tenant_admin']);
  });

  it('keeps the session while super admin is on', async () => {
    process.env.SUPER_ADMIN_ENABLED = 'true';
    const token = await jwt({ token: { ...PARKED } });
    expect(token.isSuperAdmin).toBe(true);
    expect(token.tenant).toEqual(PARKED.tenant);
  });

  it('does not look up the super-admin role on sign-in while off', async () => {
    await signIn({ user: { id: 'staff-1' } as User });
    expect(getUserSuperAdminRole).not.toHaveBeenCalled();
  });
});
