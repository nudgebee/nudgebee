import { clearSavedWorkflowFilters, workflowFilterKey } from '../listingFilterPersistence';
import { getCurrentTenant } from '@lib/auth';

jest.mock('@lib/auth', () => ({ getCurrentTenant: jest.fn() }));

const mockTenant = getCurrentTenant as jest.Mock;
const storageKey = (tenantId: string) => `nudgebee:automation:workflows:filters:v1:${tenantId}`;

describe('listingFilterPersistence', () => {
  beforeEach(() => {
    window.localStorage.clear();
    mockTenant.mockReturnValue({ id: 'tenant-1' });
  });

  it('scopes the key by tenant, and has none before a tenant is known', () => {
    expect(workflowFilterKey()).toBe('automation:workflows:filters:v1:tenant-1');
    mockTenant.mockReturnValue(undefined);
    expect(workflowFilterKey()).toBeNull();
  });

  it("forgets the current tenant's saved filters and leaves other tenants' alone", () => {
    window.localStorage.setItem(storageKey('tenant-1'), JSON.stringify({ type: 'webhook', name: 'deploy' }));
    window.localStorage.setItem(storageKey('tenant-2'), JSON.stringify({ type: 'event' }));

    clearSavedWorkflowFilters();

    expect(window.localStorage.getItem(storageKey('tenant-1'))).toBeNull();
    expect(window.localStorage.getItem(storageKey('tenant-2'))).toBe(JSON.stringify({ type: 'event' }));
  });

  it('does nothing when no tenant is known', () => {
    window.localStorage.setItem(storageKey('tenant-1'), JSON.stringify({ type: 'webhook' }));
    mockTenant.mockReturnValue(undefined);

    clearSavedWorkflowFilters();

    expect(window.localStorage.getItem(storageKey('tenant-1'))).not.toBeNull();
  });
});
