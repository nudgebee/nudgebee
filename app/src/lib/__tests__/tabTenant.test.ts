/**
 * A tab pins the tenant it loaded with; the gateway refuses a request whose pin
 * no longer matches the cookie, and the tab learns about a switch made in
 * another tab.
 */
jest.mock('next-auth/jwt', () => ({ getToken: jest.fn() }));
jest.mock('@lib/internal', () => ({ decodeSessionJWT: jest.fn(), decrypt: jest.fn() }));
jest.mock('@lib/sessionRevocation', () => ({ isSessionRevoked: jest.fn() }));

import type { NextApiRequest } from 'next';
import type { JWT } from 'next-auth/jwt';

const jwt = { tenant: { id: 't-a', name: 'Acme' } } as unknown as JWT;
const req = (headers: Record<string, string | string[]>) => ({ headers } as unknown as NextApiRequest);

// The pin is module state, set once per page load; isolate each test's copy.
function freshTabTenant(): typeof import('@lib/tabTenant') {
  let mod!: typeof import('@lib/tabTenant');
  jest.isolateModules(() => {
    mod = require('@lib/tabTenant');
  });
  return mod;
}

describe('staleTabTenant', () => {
  const { staleTabTenant } = require('@lib/rpcGateway') as typeof import('@lib/rpcGateway');

  it('lets a request through when the tab sent no pin (API token, nbctl, server bypass)', () => {
    expect(staleTabTenant(req({}), jwt)).toBeNull();
  });

  it('lets a request through when the pin matches the session tenant', () => {
    expect(staleTabTenant(req({ 'x-nb-tab-tenant': 't-a' }), jwt)).toBeNull();
  });

  it('refuses a tab pinned to another tenant and names the session tenant', () => {
    expect(staleTabTenant(req({ 'x-nb-tab-tenant': 't-b' }), jwt)).toEqual({ id: 't-a', name: 'Acme' });
  });

  it('refuses a pin when the session has no tenant at all', () => {
    expect(staleTabTenant(req({ 'x-nb-tab-tenant': 't-b' }), {} as JWT)).toEqual({ id: '', name: '' });
  });
});

describe('tab tenant pin', () => {
  it('pins only the first tenant it sees', () => {
    const t = freshTabTenant();
    expect(t.getTabTenantId()).toBeNull();
    t.pinTabTenant('t-a');
    t.pinTabTenant('t-b');
    expect(t.getTabTenantId()).toBe('t-a');
  });

  it('sends no pin while this tab is switching tenant', () => {
    const t = freshTabTenant();
    t.pinTabTenant('t-a');
    t.beginTenantSwitch();
    expect(t.getTabTenantId()).toBeNull();
  });

  it('reports a session tenant that moved away from the pin, and only that', () => {
    const t = freshTabTenant();
    t.pinTabTenant('t-a');
    const seen: unknown[] = [];
    const off = t.subscribeTenantChange((tenant) => seen.push(tenant));
    t.reportSessionTenant({ id: 't-a', name: 'Acme' });
    t.reportSessionTenant({ id: 't-b', name: 'Beta' });
    off();
    t.reportSessionTenant({ id: 't-c', name: 'Gamma' });
    expect(seen).toEqual([{ id: 't-b', name: 'Beta' }]);
  });

  it('does not report on the tab that is doing the switch', () => {
    const t = freshTabTenant();
    t.pinTabTenant('t-a');
    const seen: unknown[] = [];
    const off = t.subscribeTenantChange((tenant) => seen.push(tenant));
    t.beginTenantSwitch();
    t.reportSessionTenant({ id: 't-b', name: 'Beta' });
    off();
    expect(seen).toEqual([]);
  });

  it('hears a switch announced by another tab through the storage fallback', () => {
    const t = freshTabTenant();
    t.pinTabTenant('t-a');
    const seen: unknown[] = [];
    const off = t.subscribeTenantChange((tenant) => seen.push(tenant));
    window.dispatchEvent(new StorageEvent('storage', { key: 'nb.tenant-switch', newValue: JSON.stringify({ id: 't-b', name: 'Beta', at: 1 }) }));
    off();
    expect(seen).toEqual([{ id: 't-b', name: 'Beta', at: 1 }]);
  });

  it('recognises the refusal in both gateway envelopes', () => {
    const t = freshTabTenant();
    const gql = { errors: [{ message: 'x', extensions: { code: 'TENANT_CHANGED', tenant: { id: 't-a' } } }] };
    const rpc = { error: { code: -32004, message: 'x', data: { code: 'TENANT_CHANGED', tenant: { id: 't-a' } } } };
    expect(t.isTenantChangedResponse(409, gql)).toBe(true);
    expect(t.isTenantChangedResponse(409, rpc)).toBe(true);
    expect(t.isTenantChangedResponse(403, gql)).toBe(false);
    expect(t.isTenantChangedResponse(409, { errors: [{ message: 'conflict' }] })).toBe(false);
    expect(t.tenantFromChangedResponse(rpc)).toEqual({ id: 't-a' });
  });
});
