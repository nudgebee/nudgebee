/**
 * @jest-environment node
 */
import type { NextApiRequest, NextApiResponse } from 'next';

// The handler reads its env at import, so load it fresh per test.
function loadHandler(env: Record<string, string | undefined>) {
  for (const [key, value] of Object.entries(env)) {
    // Assigning undefined to process.env stores the string "undefined".
    if (value === undefined) delete process.env[key];
    else process.env[key] = value;
  }
  let handler: (req: NextApiRequest, res: NextApiResponse) => Promise<unknown>;
  jest.isolateModules(() => {
    handler = require('@pages/api/integrations/marketplace/azure/subscribe').default;
  });
  return handler!;
}

const fetchMock = global.fetch as jest.Mock;

function makeRes() {
  const res = {
    statusCode: 0,
    body: undefined as unknown,
    headers: {} as Record<string, string>,
    setHeader(name: string, value: string) {
      res.headers[name] = value;
      return res;
    },
    status(code: number) {
      res.statusCode = code;
      return res;
    },
    send(payload: unknown) {
      res.body = payload;
      return res;
    },
  };
  return res as unknown as NextApiResponse & typeof res;
}

const req = { method: 'GET', query: { token: 'purchase-token' }, headers: {} } as unknown as NextApiRequest;

const configured = {
  AZURE_MARKETPLACE_APP_ID: 'app-id',
  AZURE_MARKETPLACE_CLIENT_SECRET: 'secret',
  AZURE_MARKETPLACE_TENANT_ID: 'tenant-id',
};

beforeEach(() => {
  fetchMock.mockReset();
  jest.spyOn(console, 'error').mockImplementation(() => {});
});

afterEach(() => {
  jest.restoreAllMocks();
  for (const key of Object.keys(configured)) delete process.env[key];
});

describe('GET /api/integrations/marketplace/azure/subscribe', () => {
  it('resolves the purchase as the Partner Center app and stores the purchased plan', async () => {
    fetchMock
      .mockResolvedValueOnce({ json: async () => ({ access_token: 'tok' }) })
      .mockResolvedValueOnce({
        ok: true,
        json: async () => ({ id: 'sub-1', subscription: { offerId: 'offer', planId: 'pro', beneficiary: { tenantId: 'buyer-tenant' } } }),
      })
      .mockResolvedValueOnce({ ok: true, json: async () => ({ tenant_id: 'existing-tenant' }) });

    const res = makeRes();
    await loadHandler(configured)(req, res);

    const [tokenUrl, tokenInit] = fetchMock.mock.calls[0];
    expect(tokenUrl).toBe('https://login.microsoftonline.com/tenant-id/oauth2/v2.0/token');
    const form = new URLSearchParams(tokenInit.body);
    expect(form.get('client_id')).toBe('app-id');
    expect(form.get('client_secret')).toBe('secret');

    const [subscribeUrl, subscribeInit] = fetchMock.mock.calls[2];
    expect(subscribeUrl).toMatch(/\/marketplace\/subscribe$/);
    expect(JSON.parse(subscribeInit.body)).toEqual({
      customer_identifier: 'sub-1',
      provider_account_id: 'buyer-tenant',
      product_code: 'offer',
      pricing_tier: 'pro',
      marketplace: 'azure',
    });
    expect(res.statusCode).toBe(200);
  });

  it.each(Object.keys(configured))('fails with 503 without calling Microsoft when %s is unset', async (name) => {
    const res = makeRes();
    await loadHandler({ ...configured, [name]: undefined })(req, res);
    expect(res.statusCode).toBe(503);
    expect(fetchMock).not.toHaveBeenCalled();
  });
});
