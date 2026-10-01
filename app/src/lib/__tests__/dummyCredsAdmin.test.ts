import { dummyCredsAdminEmail } from '@lib/dummyCredsAdmin';

describe('dummyCredsAdminEmail', () => {
  // The function falls back to process.env.ADMIN_EMAIL, so an ambient value
  // (a developer's shell, a CI runner) would leak into every case.
  let originalAdminEmail: string | undefined;

  beforeEach(() => {
    originalAdminEmail = process.env.ADMIN_EMAIL;
    delete process.env.ADMIN_EMAIL;
  });

  afterEach(() => {
    if (originalAdminEmail === undefined) delete process.env.ADMIN_EMAIL;
    else process.env.ADMIN_EMAIL = originalAdminEmail;
  });

  it('pins to admin.email on an unlicensed deployment', () => {
    expect(dummyCredsAdminEmail('', 'Admin@Example.com')).toBe('admin@example.com');
  });

  it('lets the licence address win over a conflicting admin.email', () => {
    // provision.go ignores admin.email when the licence names an address, so
    // the licence's address is the admin that actually exists.
    expect(dummyCredsAdminEmail('owner@corp.io', 'admin@example.com')).toBe('owner@corp.io');
  });

  it('pins nothing when neither is set, keeping the any-email behavior', () => {
    expect(dummyCredsAdminEmail('', '')).toBe('');
    expect(dummyCredsAdminEmail(undefined, undefined)).toBe('');
    expect(dummyCredsAdminEmail('  ', '  ')).toBe('');
  });

  it('reads ADMIN_EMAIL from the environment by default', () => {
    process.env.ADMIN_EMAIL = ' you@example.com ';
    expect(dummyCredsAdminEmail('')).toBe('you@example.com');
  });
});
