// Drives the real NextAuth jwt callback on a workspace switch with its backend
// calls stubbed, to prove the switch is looked up for the signed-in user and not
// for whatever email the browser put in the update() payload.
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
import { getTenantIdByName, listUserTenantRoles, updateTenantUser } from '@lib/UserService';

const jwt = authOptions.callbacks!.jwt! as unknown as (p: {
  token: Record<string, unknown>;
  trigger?: string;
  session?: Record<string, unknown>;
}) => Promise<Record<string, unknown>>;

const signedIn = () => ({ id: 'u1', email: 'ada@example.com', tenant: { id: 't1', name: 'Home' }, roles: ['tenant_admin'] });

describe('NextAuth jwt: workspace switch', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    (listUserTenantRoles as jest.Mock).mockResolvedValue({ tenantName: 'Other', data: [{ entity_type: 'tenant', role: 'tenant_admin' }] });
    (updateTenantUser as jest.Mock).mockResolvedValue({ data: { data: { users_update_default_tenant: { updated: 1 } } } });
    (getTenantIdByName as jest.Mock).mockResolvedValue('t2');
  });

  it('looks up the signed-in user, not the email in the update payload', async () => {
    await jwt({ token: signedIn(), trigger: 'update', session: { tenantId: 't2', user: { email: 'eve@example.com' } } });
    expect(listUserTenantRoles).toHaveBeenCalledWith('ada@example.com', 't2');
    expect(updateTenantUser).toHaveBeenCalledWith('t2', 'ada@example.com');
  });

  it('resolves a workspace name for the signed-in user', async () => {
    await jwt({ token: signedIn(), trigger: 'update', session: { tenantName: 'Other', user: { email: 'eve@example.com' } } });
    expect(getTenantIdByName).toHaveBeenCalledWith('Other', 'ada@example.com', false);
  });

  it('switches the signed-in user to a workspace they belong to', async () => {
    const token = await jwt({ token: signedIn(), trigger: 'update', session: { tenantId: 't2', user: { email: 'ada@example.com' } } });
    expect(token.tenant).toEqual({ id: 't2', name: 'Other' });
    expect(token.roles).toEqual(['tenant_admin']);
  });

  it('stays put when the token carries no email, whatever the payload says', async () => {
    const { email: _email, ...noEmail } = signedIn();
    const token = await jwt({ token: noEmail, trigger: 'update', session: { tenantId: 't2', user: { email: 'eve@example.com' } } });
    expect(listUserTenantRoles).not.toHaveBeenCalled();
    expect(updateTenantUser).not.toHaveBeenCalled();
    expect(token.tenant).toEqual({ id: 't1', name: 'Home' });
  });

  it('stays put when the signed-in user has no access to the workspace', async () => {
    (listUserTenantRoles as jest.Mock).mockResolvedValue({ tenantName: 'Other', data: [] });
    (updateTenantUser as jest.Mock).mockResolvedValue({ data: { data: { users_update_default_tenant: { updated: 0 } } } });
    const token = await jwt({ token: signedIn(), trigger: 'update', session: { tenantId: 't2', user: { email: 'eve@example.com' } } });
    expect(token.tenant).toEqual({ id: 't1', name: 'Home' });
    expect(token.roles).toEqual(['tenant_admin']);
  });
});
