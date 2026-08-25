import React from 'react';
import { render, waitFor } from '@testing-library/react';

// auth.tsx pulls in HttpService (network) and Loader (UI) — stub both so the
// module loads in isolation.
jest.mock('@lib/HttpService', () => ({ queryGraphQL: jest.fn() }));
jest.mock('@shared/Loader', () => () => <div data-testid='loader' />);

const mockGetSession = jest.fn();
let capturedOptions: any = null;
let sessionState: { data: any; status: string } = { data: null, status: 'unauthenticated' };

jest.mock('next-auth/react', () => ({
  getSession: (...args: any[]) => mockGetSession(...args),
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

beforeEach(() => {
  // Restore first: several cases spy on Storage.prototype to make it throw.
  jest.restoreAllMocks();
  capturedOptions = null;
  sessionState = { data: null, status: 'unauthenticated' };
  mockGetSession.mockReset();
  window.sessionStorage.clear();
});

// jsdom (v26) makes window.location fully unforgeable — href, reload and the
// property itself are all non-configurable — so the navigation calls
// themselves can't be observed here. resolveUnauthenticatedSession() is the
// decision this change adds; the two one-line navigations it selects between
// are covered by the E2E suite.
describe('resolveUnauthenticatedSession — one empty session read must not sign the user out (#33323)', () => {
  it('recovers in place when the retry finds the session still there', async () => {
    mockGetSession.mockResolvedValue({ user: { email: 'a@b.com' }, expires: '2026-08-01T00:00:00Z' });

    await expect(resolveUnauthenticatedSession()).resolves.toBe('reload');
    expect(window.sessionStorage.getItem(SESSION_RECOVERY_FLAG)).toBe('1');
  });

  it('signs the user out when the retry also comes back empty', async () => {
    mockGetSession.mockResolvedValue(null);

    await expect(resolveUnauthenticatedSession()).resolves.toBe('signin');
    expect(window.sessionStorage.getItem(SESSION_RECOVERY_FLAG)).toBeNull();
  });

  it('signs the user out when the retry itself fails', async () => {
    mockGetSession.mockRejectedValue(new Error('network down'));

    await expect(resolveUnauthenticatedSession()).resolves.toBe('signin');
    expect(window.sessionStorage.getItem(SESSION_RECOVERY_FLAG)).toBeNull();
  });

  it('does not spend a second recovery reload in the same tab', async () => {
    window.sessionStorage.setItem(SESSION_RECOVERY_FLAG, '1');

    await expect(resolveUnauthenticatedSession()).resolves.toBe('signin');
    expect(mockGetSession).not.toHaveBeenCalled();
  });

  // Browsers that block site data throw a SecurityError from sessionStorage
  // itself. The function must still settle — the caller has no catch, so a
  // rejection would leave the user on <Loader /> forever.
  it('signs the user out when reading the recovery flag throws', async () => {
    jest.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new DOMException('access denied', 'SecurityError');
    });

    await expect(resolveUnauthenticatedSession()).resolves.toBe('signin');
    expect(mockGetSession).not.toHaveBeenCalled();
  });

  // Must be 'signin', not 'reload': with no storage we can't record that the
  // recovery reload was spent, so reloading would loop forever.
  it('signs the user out when the recovery flag cannot be persisted', async () => {
    mockGetSession.mockResolvedValue({ user: { email: 'a@b.com' } });
    jest.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new DOMException('access denied', 'SecurityError');
    });

    await expect(resolveUnauthenticatedSession()).resolves.toBe('signin');
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
    mockGetSession.mockResolvedValue(null);
    const Probe = withAuth(() => <div data-testid='page' />);
    render(<Probe />);

    capturedOptions.onUnauthenticated();
    capturedOptions.onUnauthenticated();
    capturedOptions.onUnauthenticated();

    await waitFor(() => expect(mockGetSession).toHaveBeenCalledTimes(1));
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
