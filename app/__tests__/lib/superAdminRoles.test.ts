import { SUPER_ADMIN_ROLES, stripSuperAdminRoles } from '@lib/superAdminRoles';

// The super-admin role strings, pinned. security.IsSuperAdminRole in the
// api-server and the CHECK constraints of migration V1107 carry the same two;
// change all three together.
describe('superAdminRoles', () => {
  it('names exactly the two super-admin roles', () => {
    expect([...SUPER_ADMIN_ROLES]).toEqual(['super_admin', 'super_admin_readonly']);
  });

  it('strips them in any case and keeps every other role in order', () => {
    expect(stripSuperAdminRoles(['tenant_admin', 'super_admin', 'account_admin', 'SUPER_ADMIN_READONLY', 'Super_Admin'])).toEqual([
      'tenant_admin',
      'account_admin',
    ]);
  });

  it('yields no roles for anything that is not an array, instead of throwing', () => {
    expect(stripSuperAdminRoles('super_admin' as unknown as string[])).toEqual([]);
    expect(stripSuperAdminRoles(undefined as unknown as string[])).toEqual([]);
    expect(stripSuperAdminRoles(null as unknown as string[])).toEqual([]);
  });

  it('does not touch the array it is given', () => {
    const roles = ['super_admin', 'tenant_admin'];
    stripSuperAdminRoles(roles);
    expect(roles).toEqual(['super_admin', 'tenant_admin']);
  });
});
