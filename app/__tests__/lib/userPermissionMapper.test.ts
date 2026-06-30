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
