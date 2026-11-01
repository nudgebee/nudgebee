/**
 * forwardAction must stop waiting on an upstream once the caller is gone.
 * Before this, a browser that cancelled a slow panel left the gateway
 * buffering the full answer anyway; a burst of those OOM-killed the pod.
 */
jest.mock('next-auth/jwt', () => ({ getToken: jest.fn() }));
jest.mock('@lib/internal', () => ({ decodeSessionJWT: jest.fn(), decrypt: jest.fn() }));
jest.mock('@lib/sessionRevocation', () => ({ isSessionRevoked: jest.fn() }));

import { EventEmitter } from 'events';
import { forwardAction } from '@lib/rpcGateway';
import { clientGone } from '@lib/graphqlGatewayHandler';

const base = {
  method: 'metrics_list',
  params: {},
  sessionVariables: { role: 'super_admin', allowed_roles: ['super_admin'], permissions: [], user_id: 'u1', tenant_id: 't1' },
  tenantId: 't1',
  userId: 'u1',
  traceparent: '00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01',
  requestId: 'r1',
};

// An upstream that never answers until the fetch signal aborts it, the way
// undici rejects both the connect and the body read on abort.
function hangingFetch() {
  return jest.fn((_url: string, init: RequestInit) => {
    return new Promise<Response>((_resolve, reject) => {
      init.signal?.addEventListener('abort', () => reject(new DOMException('This operation was aborted', 'AbortError')));
    });
  });
}

describe('forwardAction with a client abort signal', () => {
  const originalFetch = global.fetch;
  beforeEach(() => {
    process.env.SERVICE_API_SERVER_URL = 'http://api-server';
  });
  afterEach(() => {
    global.fetch = originalFetch;
  });

  it('passes the signal to fetch and reports client_disconnected once it aborts', async () => {
    const fetchMock = hangingFetch();
    global.fetch = fetchMock as unknown as typeof fetch;
    const controller = new AbortController();

    const pending = forwardAction({ ...base, signal: controller.signal });
    await Promise.resolve(); // let forwardAction reach fetch
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(fetchMock.mock.calls[0][1].signal).toBe(controller.signal);

    controller.abort();
    const result = await pending;
    expect(result).toEqual({ ok: false, error: { kind: 'client_disconnected', method: 'metrics_list', url: 'http://api-server/rpc/metrics' } });
  });

  it('reports a timeout signal as upstream_unreachable, not client_disconnected', async () => {
    global.fetch = hangingFetch() as unknown as typeof fetch;
    const result = await forwardAction({ ...base, signal: AbortSignal.timeout(10) });
    expect(result).toEqual({
      ok: false,
      error: { kind: 'upstream_unreachable', method: 'metrics_list', url: 'http://api-server/rpc/metrics', detail: 'timed out' },
    });
  });

  it('still reports upstream_unreachable when fetch fails without an abort', async () => {
    global.fetch = jest.fn(() => Promise.reject(new Error('ECONNREFUSED'))) as unknown as typeof fetch;
    const result = await forwardAction({ ...base, signal: new AbortController().signal });
    expect(result.ok).toBe(false);
    if (!result.ok) expect(result.error.kind).toBe('upstream_unreachable');
  });
});

describe('clientGone', () => {
  function fakeRes(writableFinished: boolean, destroyed = false) {
    const res = new EventEmitter() as EventEmitter & { writableFinished: boolean; destroyed: boolean };
    res.writableFinished = writableFinished;
    res.destroyed = destroyed;
    return res;
  }

  it('aborts at once when the response is already destroyed', () => {
    const signal = clientGone(fakeRes(false, true) as never);
    expect(signal.aborted).toBe(true);
  });

  it('aborts when the response closes before anything was sent', () => {
    const res = fakeRes(false);
    const signal = clientGone(res as never);
    expect(signal.aborted).toBe(false);
    res.emit('close');
    expect(signal.aborted).toBe(true);
  });

  it('does not abort on the close that follows a sent response', () => {
    const res = fakeRes(true);
    const signal = clientGone(res as never);
    res.emit('close');
    expect(signal.aborted).toBe(false);
  });
});
