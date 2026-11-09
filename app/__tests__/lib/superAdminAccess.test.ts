// Super admin is off unless SUPER_ADMIN_ENABLED=true. These pin what
// "off" means for a session read from a cookie or minted from an API key, and
// that the server's own synthetic admin is unaffected.

import type { NextApiRequest } from 'next';

jest.mock('next-auth/jwt', () => ({ getToken: jest.fn() }));
jest.mock('@lib/apiToken', () => ({ isApiToken: jest.fn(), authenticateApiToken: jest.fn() }));
jest.mock('@lib/sessionRevocation', () => ({ isSessionRevoked: jest.fn(async () => false) }));

import { getToken, type JWT } from 'next-auth/jwt';
import { authenticateApiToken, isApiToken } from '@lib/apiToken';
import { authenticateRequest, buildSessionVariables } from '@lib/rpcGateway';
import { readSessionToken } from '@lib/sessionCookie';
import { isSuperAdminAccessEnabled, restrictSuperAdmin } from '@lib/superAdminAccess';

const mockGetToken = getToken as jest.Mock;
const mockIsApiToken = isApiToken as jest.Mock;
const mockAuthenticateApiToken = authenticateApiToken as jest.Mock;

const req = { headers: {} } as unknown as NextApiRequest;
const patReq = { headers: { authorization: 'Bearer sk-nb-secret' } } as unknown as NextApiRequest;

// A super admin who switched into a customer workspace: flagged, and holding
// that workspace's read-only admin role.
const PARKED_SUPER_ADMIN = {
  id: 'staff-1',
  sub: 'staff-1',
  iat: 1700000000,
  isSuperAdmin: true,
  tenant: { id: 'customer' },
  roles: ['tenant_admin_readonly'],
};
const READONLY_SUPER_ADMIN = { id: 'staff-2', sub: 'staff-2', iat: 1700000000, isSuperAdminReadonly: true, roles: ['tenant_admin_readonly'] };
const MEMBER = { id: 'u1', sub: 'u1', iat: 1700000000, tenant: { id: 't1' }, roles: ['tenant_admin'] };

let savedEnv: NodeJS.ProcessEnv;

beforeEach(() => {
  savedEnv = { ...process.env };
  delete process.env.SUPER_ADMIN_ENABLED;
  jest.clearAllMocks();
  mockGetToken.mockResolvedValue(null);
  mockIsApiToken.mockReturnValue(false);
});

afterEach(() => {
  process.env = savedEnv;
});

describe('isSuperAdminAccessEnabled', () => {
  it.each([undefined, '', 'false', '1', 'TRUE', 'yes'])('is off for SUPER_ADMIN_ENABLED=%p', (value) => {
    if (value === undefined) delete process.env.SUPER_ADMIN_ENABLED;
    else process.env.SUPER_ADMIN_ENABLED = value;
    expect(isSuperAdminAccessEnabled()).toBe(false);
  });

  it('is on only for exactly "true"', () => {
    process.env.SUPER_ADMIN_ENABLED = 'true';
    expect(isSuperAdminAccessEnabled()).toBe(true);
  });
});

describe('restrictSuperAdmin while off', () => {
  it('refuses a session minted as super admin, full or read-only', () => {
    expect(restrictSuperAdmin(PARKED_SUPER_ADMIN)).toBeNull();
    expect(restrictSuperAdmin(READONLY_SUPER_ADMIN)).toBeNull();
  });

  it('drops super-admin role strings, in any case, and keeps the rest', () => {
    const token = { ...MEMBER, roles: ['tenant_admin', 'super_admin', 'SUPER_ADMIN_READONLY'] };
    expect(restrictSuperAdmin(token)).toEqual({ ...MEMBER, roles: ['tenant_admin'] });
    expect(token.roles).toEqual(['tenant_admin', 'super_admin', 'SUPER_ADMIN_READONLY']);
  });

  it('returns an ordinary session unchanged', () => {
    expect(restrictSuperAdmin(MEMBER)).toBe(MEMBER);
  });
});

describe('restrictSuperAdmin while on', () => {
  it('leaves super-admin sessions alone', () => {
    process.env.SUPER_ADMIN_ENABLED = 'true';
    expect(restrictSuperAdmin(PARKED_SUPER_ADMIN)).toBe(PARKED_SUPER_ADMIN);
  });
});

describe('readSessionToken', () => {
  it('treats a super-admin cookie as no session while off', async () => {
    mockGetToken.mockResolvedValue(PARKED_SUPER_ADMIN);
    await expect(readSessionToken(req)).resolves.toBeNull();
  });

  it('returns it while on', async () => {
    process.env.SUPER_ADMIN_ENABLED = 'true';
    mockGetToken.mockResolvedValue(PARKED_SUPER_ADMIN);
    await expect(readSessionToken(req)).resolves.toBe(PARKED_SUPER_ADMIN);
  });
});

describe('authenticateRequest while off', () => {
  it('rejects a super-admin cookie, so no RPC is forwarded for it', async () => {
    mockGetToken.mockResolvedValue(PARKED_SUPER_ADMIN);
    await expect(authenticateRequest(req)).resolves.toBeNull();
  });

  it('strips a super_admin role row from an API key session before it reaches allowed_roles', async () => {
    mockIsApiToken.mockReturnValue(true);
    mockAuthenticateApiToken.mockResolvedValue({ token: 'signed', jwt: { ...MEMBER, roles: ['tenant_admin', 'super_admin'] } });
    const auth = await authenticateRequest(patReq);
    expect(auth?.jwt.roles).toEqual(['tenant_admin']);
    expect(buildSessionVariables(auth!.jwt).allowed_roles).not.toContain('super_admin');
  });

  it('still admits an ordinary cookie session', async () => {
    mockGetToken.mockResolvedValue(MEMBER);
    await expect(authenticateRequest(req)).resolves.toEqual({ token: '', jwt: MEMBER });
  });
});

describe('the server identity', () => {
  // SERVER_ADMIN_JWT goes straight to buildSessionVariables, never through the
  // cookie or an API key, so the switch does not reach it.
  it('keeps super_admin in allowed_roles while off', () => {
    const vars = buildSessionVariables({ isSuperAdmin: true, roles: ['admin'], tenant: { id: '' }, id: '', sub: '' } as unknown as JWT);
    expect(vars.allowed_roles).toContain('super_admin');
  });
});
