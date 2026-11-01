// The revocation check runs before every cookie request. When services-server
// cannot be reached (a pod that exited while still in the Service's endpoints)
// the check must give up quickly and fail open, not hold the request for
// undici's 10 s connect timeout (#39746).
jest.mock('next-auth/jwt', () => ({ getToken: jest.fn() }));
jest.mock('@lib/internal', () => ({ decodeSessionJWT: jest.fn(), decrypt: jest.fn() }));
jest.mock('@lib/HttpService', () => ({ queryGraphQL: jest.fn() }));

import { queryGraphQL } from '@lib/HttpService';
import { bypassGraphQLAsServer } from '@lib/rpcGateway';
import { isSessionRevoked } from '@lib/sessionRevocation';

const queryGraphQLMock = queryGraphQL as jest.Mock;

describe('isSessionRevoked', () => {
  afterEach(() => {
    queryGraphQLMock.mockReset();
    jest.restoreAllMocks();
  });

  it('bounds the check with a one-second timeout signal', async () => {
    queryGraphQLMock.mockResolvedValue({ data: { data: { auth_check_session: { revoked: false } } } });
    const timeoutSpy = jest.spyOn(AbortSignal, 'timeout');

    await isSessionRevoked('u1', null, 1700000000);

    expect(timeoutSpy).toHaveBeenCalledWith(1000);
    const signal = queryGraphQLMock.mock.calls[0][4];
    expect(signal).toBe(timeoutSpy.mock.results[0].value);
  });

  it('fails open when the check comes back with an error', async () => {
    jest.spyOn(console, 'error').mockImplementation(() => {});
    jest.spyOn(console, 'warn').mockImplementation(() => {});
    queryGraphQLMock.mockResolvedValue({
      data: { data: { auth_check_session: null }, errors: [{ message: 'Upstream unreachable for auth_check_session' }] },
    });

    await expect(isSessionRevoked('u1', null, 1700000000)).resolves.toBe(false);
  });

  it('reports a revoked session', async () => {
    queryGraphQLMock.mockResolvedValue({ data: { data: { auth_check_session: { revoked: true } } } });

    await expect(isSessionRevoked('u1', null, 1700000000)).resolves.toBe(true);
  });
});

// #39801: a skipped check lets one request through unchecked. Each skip must leave
// one line of a fixed shape, so a log query can count skips and say why.
describe('isSessionRevoked logs each skipped check', () => {
  const SKIP_LINE = /^\[sessionRevocation\] session_check_skipped reason=(timeout|unreachable|error) duration_ms=\d+$/;
  let warn: jest.SpyInstance;

  beforeEach(() => {
    jest.spyOn(console, 'error').mockImplementation(() => {});
    warn = jest.spyOn(console, 'warn').mockImplementation(() => {});
  });
  afterEach(() => {
    queryGraphQLMock.mockReset();
    jest.restoreAllMocks();
  });

  const skipLines = () => warn.mock.calls.map((call) => String(call[0])).filter((line) => line.includes('session_check_skipped'));

  it('says "unreachable" when services-server refuses the connection', async () => {
    queryGraphQLMock.mockResolvedValue({
      data: { data: { auth_check_session: null }, errors: [{ message: 'Upstream unreachable for auth_check_session' }] },
    });

    await isSessionRevoked('u1', null, 1700000000);

    expect(skipLines()).toHaveLength(1);
    expect(skipLines()[0]).toMatch(SKIP_LINE);
    expect(skipLines()[0]).toContain('reason=unreachable');
  });

  it('says "timeout" when the one-second limit fired', async () => {
    jest.spyOn(AbortSignal, 'timeout').mockReturnValue(AbortSignal.abort(new DOMException('timed out', 'TimeoutError')));
    queryGraphQLMock.mockResolvedValue({
      data: { data: { auth_check_session: null }, errors: [{ message: 'Upstream unreachable for auth_check_session' }] },
    });

    await isSessionRevoked('u1', null, 1700000000);

    expect(skipLines()).toHaveLength(1);
    expect(skipLines()[0]).toMatch(SKIP_LINE);
    expect(skipLines()[0]).toContain('reason=timeout');
  });

  it('says "error" for any other failed answer, and when the call throws', async () => {
    queryGraphQLMock.mockResolvedValueOnce({
      data: { data: { auth_check_session: null }, errors: [{ message: 'Service is temporarily unavailable.' }] },
    });
    queryGraphQLMock.mockRejectedValueOnce(new Error('boom'));

    await expect(isSessionRevoked('u1', null, 1700000000)).resolves.toBe(false);
    await expect(isSessionRevoked('u1', null, 1700000000)).resolves.toBe(false);

    expect(skipLines()).toHaveLength(2);
    skipLines().forEach((line) => {
      expect(line).toMatch(SKIP_LINE);
      expect(line).toContain('reason=error');
    });
  });

  it('says "error" when the answer carries no verdict at all', async () => {
    queryGraphQLMock.mockResolvedValueOnce(undefined);
    queryGraphQLMock.mockResolvedValueOnce({ data: { data: { auth_check_session: null } } });

    await expect(isSessionRevoked('u1', null, 1700000000)).resolves.toBe(false);
    await expect(isSessionRevoked('u1', null, 1700000000)).resolves.toBe(false);

    expect(skipLines()).toHaveLength(2);
    skipLines().forEach((line) => {
      expect(line).toMatch(SKIP_LINE);
      expect(line).toContain('reason=error');
    });
  });

  it('logs nothing when the check answers', async () => {
    queryGraphQLMock.mockResolvedValue({ data: { data: { auth_check_session: { revoked: false } } } });

    await isSessionRevoked('u1', null, 1700000000);

    expect(skipLines()).toHaveLength(0);
  });
});

describe('bypassGraphQLAsServer with a timeout signal', () => {
  const originalFetch = global.fetch;
  beforeEach(() => {
    process.env.SERVICE_API_SERVER_URL = 'http://api-server';
  });
  afterEach(() => {
    global.fetch = originalFetch;
    jest.restoreAllMocks();
  });

  it('stops waiting on a blackholed upstream and logs it as a timeout', async () => {
    const errorLog = jest.spyOn(console, 'error').mockImplementation(() => {});
    // Never answers until the fetch signal aborts it, the way undici rejects a
    // blackholed connect on abort.
    const fetchMock = jest.fn(
      (_url: string, init: RequestInit) =>
        new Promise<Response>((_resolve, reject) => {
          init.signal?.addEventListener('abort', () => reject(init.signal?.reason));
        })
    );
    global.fetch = fetchMock as unknown as typeof fetch;

    const result = await bypassGraphQLAsServer({
      query: 'mutation AuthCheckSession($object: auth_check_session_input!) { auth_check_session(object: $object) { revoked } }',
      variables: { object: { user_id: 'u1', jti: '', iat: 1700000000 } },
      traceparent: '00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01',
      requestId: 'r1',
      signal: AbortSignal.timeout(20),
    });

    expect(fetchMock.mock.calls[0][0]).toBe('http://api-server/rpc/user');
    expect(result).toMatchObject({
      handled: true,
      body: { data: { auth_check_session: null }, errors: [{ message: 'Upstream unreachable for auth_check_session' }] },
    });
    expect(errorLog).toHaveBeenCalledWith(
      expect.stringContaining('upstream_unreachable method=auth_check_session url=http://api-server/rpc/user detail=timed out')
    );
  });
});
