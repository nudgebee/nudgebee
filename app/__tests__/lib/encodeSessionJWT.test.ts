/**
 * @jest-environment node
 */
// The allowed_roles claim of a session token the app signs (API keys)
// never carries a super-admin role read from the user's tenant roles.
const mockSignedClaims: Record<string, unknown>[] = [];
jest.mock('jose', () => ({
  SignJWT: class {
    constructor(claims: Record<string, unknown>) {
      mockSignedClaims.push(claims);
    }
    setProtectedHeader() {
      return this;
    }
    setJti() {
      return this;
    }
    setIssuedAt() {
      return this;
    }
    setIssuer() {
      return this;
    }
    setAudience() {
      return this;
    }
    setExpirationTime() {
      return this;
    }
    async sign() {
      return 'signed';
    }
  },
  jwtVerify: jest.fn(),
}));
jest.mock('bcrypt', () => ({}));
jest.mock('@lib/sessionRevocation', () => ({ isSessionRevoked: jest.fn() }));

let encodeSessionJWT: (user: any, claims: any, exp: number, iat: number) => Promise<string>;

beforeAll(async () => {
  // internal.ts reads both at module load.
  process.env.NUDGEBEE_ENCRYPTION_KEY = 'a'.repeat(64);
  process.env.NEXTAUTH_SECRET = 'encode-session-jwt-test-secret-0123456789';
  ({ encodeSessionJWT } = await import('@lib/internal'));
});

beforeEach(() => {
  mockSignedClaims.length = 0;
});

describe('encodeSessionJWT', () => {
  it('drops super-admin roles from the signed allowed_roles', async () => {
    const user = { id: 'u1', tenant: { id: 't1' }, roles: ['tenant_admin', 'super_admin', 'super_admin_readonly'] };
    await encodeSessionJWT(user, {}, 0, 0);
    expect(mockSignedClaims[0].allowed_roles).toEqual(['tenant_admin']);
    expect(mockSignedClaims[0].default_role).toBe('tenant_admin');
    expect(user.roles).toEqual(['tenant_admin']);
  });

  it('still adds the tenant_usage marker, on the user too, when nothing else is left', async () => {
    const user = { id: 'u1', tenant: { id: 't1' }, roles: ['super_admin'] };
    await encodeSessionJWT(user, {}, 0, 0);
    expect(mockSignedClaims[0].allowed_roles).toEqual(['tenant_usage']);
    // createBearerSession reads the roles back off the user object.
    expect(user.roles).toEqual(['tenant_usage']);
  });
});
