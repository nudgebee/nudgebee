import React from 'react';
import { render, waitFor } from '@testing-library/react';

// auth.tsx pulls in HttpService (network) and Loader (UI) — stub both so the
// module loads in isolation.
jest.mock('@lib/HttpService', () => ({ queryGraphQL: jest.fn() }));
jest.mock('@shared/Loader', () => () => <div data-testid='loader' />);

let capturedOptions: any = null;
let sessionState: { data: any; status: string } = { data: null, status: 'unauthenticated' };

jest.mock('next-auth/react', () => ({
  useSession: (options: any) => {
    capturedOptions = options;
    // Mirror next-auth: under `required`, an unauthenticated session is
    // reported as `loading` and the caller's onUnauthenticated handler is what
    // actually drives the exit.
    if (options?.required && sessionState.status === 'unauthenticated') {
      return { data: null, status: 'loading' };
    }
    return sessionState;
  },
}));

import { resolveUnauthenticatedSession, withAuth } from '@lib/auth';

const SESSION_RECOVERY_FLAG = 'nb.session-recovery';

// resolveUnauthenticatedSession reads /api/auth/session with fetch directly, so
// that is what each case scripts.
const mockFetch = jest.fn();
global.fetch = mockFetch;

const LIVE_SESSION = { user: { email: 'a@b.com' }, expires: '2026-08-01T00:00:00Z' };
const sessionResponse = (body: unknown, ok = true): { ok: boolean; json: () => Promise<unknown> } => ({
  ok,
  json: (): Promise<unknown> => Promise.resolve(body),
});
// What fetch throws when the network changes under an in-flight request
// (Chrome: net::ERR_NETWORK_CHANGED).
const networkError = (): Promise<never> => Promise.reject(new TypeError('Failed to fetch'));

beforeEach(() => {
  // Restore first: several cases spy on Storage.prototype to make it throw.
  jest.restoreAllMocks();
  capturedOptions = null;
  sessionState = { data: null, status: 'unauthenticated' };
  mockFetch.mockReset();
  window.sessionStorage.clear();
});

afterEach(() => {
  jest.useRealTimers();
});

// jsdom (v26) makes window.location fully unforgeable — href, reload and the
// property itself are all non-configurable — so the navigation calls
// themselves can't be observed here. resolveUnauthenticatedSession() is the
// decision this change adds; the two one-line navigations it selects between
// are covered by the E2E suite.
describe('resolveUnauthenticatedSession — one empty session read must not sign the user out (#33323)', () => {
  it('recovers in place when the retry finds the session still there', async () => {
    mockFetch.mockResolvedValue(sessionResponse(LIVE_SESSION));

    await expect(resolveUnauthenticatedSession()).resolves.toBe('reload');
    expect(window.sessionStorage.getItem(SESSION_RECOVERY_FLAG)).toBe('1');
  });

  it('signs the user out at once when the server says there is no session', async () => {
    mockFetch.mockResolvedValue(sessionResponse({}));

    await expect(resolveUnauthenticatedSession()).resolves.toBe('signin');
    expect(mockFetch).toHaveBeenCalledTimes(1);
    expect(window.sessionStorage.getItem(SESSION_RECOVERY_FLAG)).toBeNull();
  });

  it('does not spend a second recovery reload in the same tab', async () => {
    window.sessionStorage.setItem(SESSION_RECOVERY_FLAG, '1');

    await expect(resolveUnauthenticatedSession()).resolves.toBe('signin');
    expect(mockFetch).not.toHaveBeenCalled();
  });

  // Browsers that block site data throw a SecurityError from sessionStorage
  // itself. The function must still settle — the caller has no catch, so a
  // rejection would leave the user on <Loader /> forever.
  it('signs the user out when reading the recovery flag throws', async () => {
    jest.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new DOMException('access denied', 'SecurityError');
    });

    await expect(resolveUnauthenticatedSession()).resolves.toBe('signin');
    expect(mockFetch).not.toHaveBeenCalled();
  });

  // Must be 'signin', not 'reload': with no storage we can't record that the
  // recovery reload was spent, so reloading would loop forever.
  it('signs the user out when the recovery flag cannot be persisted', async () => {
    mockFetch.mockResolvedValue(sessionResponse(LIVE_SESSION));
    jest.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new DOMException('access denied', 'SecurityError');
    });

    await expect(resolveUnauthenticatedSession()).resolves.toBe('signin');
  });
});

describe('resolveUnauthenticatedSession — a read that never reached the server is retried, not a sign-out', () => {
  beforeEach(() => {
    jest.useFakeTimers();
  });

  it('keeps the user signed in when the network comes back mid-retry', async () => {
    mockFetch.mockImplementationOnce(networkError).mockImplementationOnce(networkError).mockResolvedValue(sessionResponse(LIVE_SESSION));

    const result = resolveUnauthenticatedSession();
    await jest.runAllTimersAsync();

    await expect(result).resolves.toBe('reload');
    expect(mockFetch).toHaveBeenCalledTimes(3);
  });

  it('gives up on a read that hangs and retries it', async () => {
    // A request stranded on the old network: never settles until aborted.
    const hangingRead = (_url: string, init: RequestInit): Promise<never> =>
      new Promise((_resolve, reject) => init.signal?.addEventListener('abort', () => reject(init.signal?.reason)));
    mockFetch.mockImplementationOnce(hangingRead).mockResolvedValue(sessionResponse(LIVE_SESSION));

    const result = resolveUnauthenticatedSession();
    await jest.runAllTimersAsync();

    await expect(result).resolves.toBe('reload');
    expect(mockFetch).toHaveBeenCalledTimes(2);
  });

  it('retries a server error or a non-JSON body, then honours the real answer', async () => {
    mockFetch
      .mockResolvedValueOnce(sessionResponse(null, false))
      .mockResolvedValueOnce({ ok: true, json: () => Promise.reject(new SyntaxError('Unexpected token <')) })
      .mockResolvedValue(sessionResponse({}));

    const result = resolveUnauthenticatedSession();
    await jest.runAllTimersAsync();

    await expect(result).resolves.toBe('signin');
    expect(mockFetch).toHaveBeenCalledTimes(3);
  });

  it('signs the user out after three failed retries', async () => {
    mockFetch.mockImplementation(networkError);

    const result = resolveUnauthenticatedSession();
    await jest.runAllTimersAsync();

    await expect(result).resolves.toBe('signin');
    // The first read plus three retries.
    expect(mockFetch).toHaveBeenCalledTimes(4);
    expect(window.sessionStorage.getItem(SESSION_RECOVERY_FLAG)).toBeNull();
  });

  it('waits for the browser to come back online without using up retries', async () => {
    const onLine = jest.spyOn(navigator, 'onLine', 'get').mockReturnValue(false);
    mockFetch.mockImplementationOnce(networkError).mockResolvedValue(sessionResponse(LIVE_SESSION));

    const result = resolveUnauthenticatedSession();
    await jest.runAllTimersAsync();
    // Far past the whole retry budget: still parked on the one failed read.
    await jest.advanceTimersByTimeAsync(10 * 60 * 1000);
    expect(mockFetch).toHaveBeenCalledTimes(1);

    onLine.mockReturnValue(true);
    window.dispatchEvent(new Event('online'));

    await expect(result).resolves.toBe('reload');
    expect(mockFetch).toHaveBeenCalledTimes(2);
  });
});

describe('withAuth — wiring', () => {
  it('hands next-auth an onUnauthenticated handler instead of letting it redirect', () => {
    const Probe = withAuth(() => <div data-testid='page' />);
    render(<Probe />);

    expect(capturedOptions.required).toBe(true);
    expect(typeof capturedOptions.onUnauthenticated).toBe('function');
  });

  it('retries at most once per mount however often next-auth calls back', async () => {
    mockFetch.mockResolvedValue(sessionResponse({}));
    const Probe = withAuth(() => <div data-testid='page' />);
    render(<Probe />);

    capturedOptions.onUnauthenticated();
    capturedOptions.onUnauthenticated();
    capturedOptions.onUnauthenticated();

    await waitFor(() => expect(mockFetch).toHaveBeenCalledTimes(1));
  });

  it('renders the page and clears a stale recovery flag once a session is present', async () => {
    window.sessionStorage.setItem(SESSION_RECOVERY_FLAG, '1');
    sessionState = { data: { roles: ['tenant_admin'] }, status: 'authenticated' };

    const Probe = withAuth(() => <div data-testid='page' />);
    const { getByTestId } = render(<Probe />);

    expect(getByTestId('page')).toBeTruthy();
    await waitFor(() => expect(window.sessionStorage.getItem(SESSION_RECOVERY_FLAG)).toBeNull());
  });

  // The flag clear runs in a commit-phase effect, so an unguarded throw there
  // takes down the whole page rather than just skipping the cleanup.
  it('still renders when clearing the recovery flag throws', () => {
    sessionState = { data: { roles: ['tenant_admin'] }, status: 'authenticated' };
    jest.spyOn(Storage.prototype, 'removeItem').mockImplementation(() => {
      throw new DOMException('access denied', 'SecurityError');
    });

    const Probe = withAuth(() => <div data-testid='page' />);
    const { getByTestId } = render(<Probe />);

    expect(getByTestId('page')).toBeTruthy();
  });
});
