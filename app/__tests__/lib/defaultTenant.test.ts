import { pickDefaultTenant, userHasRoleInTenant, type UserForTenantPick } from '@lib/defaultTenant';

// Tenant UUIDs mirroring the issue #32594 repro: A = the roleless default,
// B = the tenant where the user is actually an admin (via a group).
const A = 'fd12f18e-3df8-41fb-80d9-4a4601a6d2a5'; // "Iteration-2" — default, no role
const B = '890cad87-c452-4aa7-b84a-742cee0454a1'; // "Nudgebee" — tenant_admin via group

const tenantRole = (tenantId: string) => ({ entity_type: 'tenant', entity_id: tenantId, role: 'tenant_admin' });
const groupWithTenantRole = (tenantId: string) => ({ user_group: { group_roles: [tenantRole(tenantId)] } });

describe('userHasRoleInTenant', () => {
  it('is true for a direct role scoped to the tenant (user_roles carry tenant_id)', () => {
    const user: UserForTenantPick = { user_roles: [{ entity_type: 'account', entity_id: 'acc-1', tenant_id: A, role: 'account_admin' }] };
    expect(userHasRoleInTenant(user, A)).toBe(true);
    expect(userHasRoleInTenant(user, B)).toBe(false);
  });

  it('is true for a group-derived tenant role whose entity_id is the tenant', () => {
    const user: UserForTenantPick = { groups: [groupWithTenantRole(B)] };
    expect(userHasRoleInTenant(user, B)).toBe(true);
    expect(userHasRoleInTenant(user, A)).toBe(false);
  });

  it('does NOT count a group account role as tenant access (entity_id is an account id, not a tenant)', () => {
    const user: UserForTenantPick = {
      groups: [{ user_group: { group_roles: [{ entity_type: 'account', entity_id: 'acc-9', role: 'account_admin' }] } }],
    };
    // acc-9 is an account id; it must not be mistaken for a tenant id.
    expect(userHasRoleInTenant(user, 'acc-9')).toBe(false);
    expect(userHasRoleInTenant(user, B)).toBe(false);
  });

  it('is false when the user has no roles at all', () => {
    expect(userHasRoleInTenant({ user_roles: [], groups: [] }, A)).toBe(false);
    expect(userHasRoleInTenant({}, A)).toBe(false);
  });
});

describe('pickDefaultTenant', () => {
  it('issue #32594: lands the user in the tenant where their group grants a role, not the roleless default', () => {
    const user: UserForTenantPick = {
      tenants: [
        { id: A, name: 'Iteration-2', is_default: true },
        { id: B, name: 'Nudgebee', is_default: false },
      ],
      user_roles: [], // no direct roles anywhere
      groups: [groupWithTenantRole(B)], // tenant_admin in B via group
    };
    expect(pickDefaultTenant(user)?.id).toBe(B);
  });

  it('keeps the is_default tenant when the user DOES have a role there', () => {
    const user: UserForTenantPick = {
      tenants: [
        { id: A, name: 'Iteration-2', is_default: true },
        { id: B, name: 'Nudgebee', is_default: false },
      ],
      user_roles: [{ entity_type: 'tenant', entity_id: A, tenant_id: A, role: 'tenant_admin' }],
      groups: [groupWithTenantRole(B)], // also has a role in B, but A is the explicit default
    };
    expect(pickDefaultTenant(user)?.id).toBe(A);
  });

  it('falls back to the first tenant with a role when none is marked default', () => {
    const user: UserForTenantPick = {
      tenants: [
        { id: A, name: 'Iteration-2' },
        { id: B, name: 'Nudgebee' },
      ],
      groups: [groupWithTenantRole(B)],
    };
    expect(pickDefaultTenant(user)?.id).toBe(B);
  });

  it('preserves prior behavior (marked default) when the user has no role anywhere', () => {
    const user: UserForTenantPick = {
      tenants: [
        { id: A, name: 'Iteration-2', is_default: true },
        { id: B, name: 'Nudgebee', is_default: false },
      ],
      user_roles: [],
      groups: [],
    };
    expect(pickDefaultTenant(user)?.id).toBe(A);
  });

  it('falls back to the first tenant when no role and no marked default', () => {
    const user: UserForTenantPick = {
      tenants: [{ id: A }, { id: B }],
    };
    expect(pickDefaultTenant(user)?.id).toBe(A);
  });

  it('returns the only tenant regardless of roles', () => {
    expect(pickDefaultTenant({ tenants: [{ id: A, is_default: true }] })?.id).toBe(A);
    expect(pickDefaultTenant({ tenants: [{ id: A }] })?.id).toBe(A);
  });

  it('returns undefined when the user belongs to no tenant', () => {
    expect(pickDefaultTenant({ tenants: [] })).toBeUndefined();
    expect(pickDefaultTenant({})).toBeUndefined();
  });
});
