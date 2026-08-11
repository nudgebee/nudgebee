// getAccountByTenant hits the network during session building — stub it so we can
// drive the tenant-account list. extractUserPermissions narrows role-granted account
// ids to the tenant's accounts; an empty list must NOT strip explicit grants (#32887).
const mockGetAccountByTenant = jest.fn();
jest.mock('@lib/UserService', () => ({
  getAccountByTenant: (...args: any[]) => mockGetAccountByTenant(...args),
}));

import { extractUserPermissions } from '@lib/userPermissionMapper';

const TENANT = 'tenant-1';
const ACCOUNT = 'acc-1';

function accountAdminUser() {
  return {
    tenants: [{ id: TENANT, is_default: true }],
    user_roles: [{ entity_type: 'account', role: 'account_admin', entity_id: ACCOUNT }],
    groups: [],
  };
}

describe('extractUserPermissions — tenant-account narrowing (#32887)', () => {
  beforeEach(() => mockGetAccountByTenant.mockReset());

  it('keeps role-granted account ids that belong to the tenant', async () => {
    mockGetAccountByTenant.mockResolvedValue({ errored: false, data: { cloud_accounts: [{ id: ACCOUNT }, { id: 'acc-2' }] } });
    const perms = await extractUserPermissions(accountAdminUser());
    expect(perms.accountIds).toEqual([ACCOUNT]);
  });

  it('drops role-granted account ids NOT in the tenant (multi-tenant hygiene preserved)', async () => {
    mockGetAccountByTenant.mockResolvedValue({ errored: false, data: { cloud_accounts: [{ id: 'acc-other' }] } });
    const perms = await extractUserPermissions(accountAdminUser());
    expect(perms.accountIds).toEqual([]);
  });

  it('preserves grants when the tenant-account lookup ERRORS (transient backend failure)', async () => {
    mockGetAccountByTenant.mockResolvedValue({ errored: true, data: { cloud_accounts: [] } });
    const perms = await extractUserPermissions(accountAdminUser());
    // Regression: a failed lookup must not silently strip explicit grants and lock the
    // account_admin out of Ask-Nudgebee — the backend re-validates every request.
    expect(perms.accountIds).toEqual([ACCOUNT]);
  });

  it('preserves grants when the lookup resolves to no accounts (no positive basis to strip)', async () => {
    mockGetAccountByTenant.mockResolvedValue({ errored: false, data: { cloud_accounts: [] } });
    const perms = await extractUserPermissions(accountAdminUser());
    expect(perms.accountIds).toEqual([ACCOUNT]);
  });

  it('preserves grants when the lookup response is malformed/undefined', async () => {
    mockGetAccountByTenant.mockResolvedValue({ data: undefined });
    const perms = await extractUserPermissions(accountAdminUser());
    expect(perms.accountIds).toEqual([ACCOUNT]);
  });
});

describe('extractUserPermissions — roles are scoped to the logged-in tenant', () => {
  beforeEach(() => {
    mockGetAccountByTenant.mockReset();
    mockGetAccountByTenant.mockResolvedValue({ errored: false, data: { cloud_accounts: [{ id: ACCOUNT }] } });
  });

  it('drops account/namespace roles granted in another tenant', async () => {
    const perms = await extractUserPermissions({
      tenants: [{ id: TENANT, is_default: true }],
      user_roles: [
        { entity_type: 'tenant', role: 'tenant_admin', entity_id: TENANT, tenant_id: TENANT },
        { entity_type: 'account', role: 'account_admin', entity_id: 'acc-other', tenant_id: 'tenant-2' },
        { entity_type: 'k8s_namespace', role: 'k8s_namespace_admin', entity_id: 'acc-other:nudgebee', tenant_id: 'tenant-2' },
      ],
      groups: [],
    });
    // Regression: the session leaked role NAMES from every tenant the user belonged
    // to, even after the account ids themselves were narrowed away.
    expect(perms.roles).toEqual(['tenant_admin']);
    expect(perms.accountIds).toEqual([]);
    expect(perms.namespacedAccountIds).toEqual([]);
    expect(perms.k8sNamespaces).toEqual({});
  });

  it('keeps account/namespace roles granted in the logged-in tenant', async () => {
    const perms = await extractUserPermissions({
      tenants: [{ id: TENANT, is_default: true }],
      user_roles: [
        { entity_type: 'account', role: 'account_admin', entity_id: ACCOUNT, tenant_id: TENANT },
        { entity_type: 'k8s_namespace', role: 'k8s_namespace_admin', entity_id: `${ACCOUNT}:nudgebee`, tenant_id: TENANT },
      ],
      groups: [],
    });
    expect(perms.roles).toEqual(['account_admin', 'k8s_namespace_admin']);
    expect(perms.accountIds).toEqual([ACCOUNT]);
    expect(perms.k8sNamespaces).toEqual({ [ACCOUNT]: ['nudgebee'] });
  });

  it('ignores roles from groups owned by another tenant', async () => {
    const perms = await extractUserPermissions({
      tenants: [{ id: TENANT, is_default: true }],
      user_roles: [{ entity_type: 'tenant', role: 'tenant_admin', entity_id: TENANT, tenant_id: TENANT }],
      groups: [
        { user_group: { id: 'g1', tenant: 'tenant-2', group_roles: [{ entity_type: 'account', role: 'account_admin', entity_id: 'acc-other' }] } },
        { user_group: { id: 'g2', tenant: TENANT, group_roles: [{ entity_type: 'account', role: 'account_admin_readonly', entity_id: ACCOUNT }] } },
      ],
    });
    expect(perms.roles).toEqual(['tenant_admin', 'account_admin_readonly']);
    expect(perms.readonlyAccountIds).toEqual([ACCOUNT]);
  });
});
