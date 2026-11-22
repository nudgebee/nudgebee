import React from 'react';
import { render, screen, act, waitFor } from '@testing-library/react';
import '@testing-library/jest-dom';
import { SetupGateProvider } from '@context/SetupGateContext';
import TenantSwitchOverlay from '@components/common/TenantSwitchOverlay';
import { TENANT_SWITCH_FLAG } from '@lib/tenantSwitchOverlay';

jest.mock('src/utils/colors');

const mockReplace = jest.fn();
let pathname = '/home';
jest.mock('next/router', () => ({ useRouter: () => ({ replace: mockReplace, pathname }) }));

let session: unknown = { roles: ['tenant_admin'], tenant: { id: 't1' }, uiFeatures: { setupCenter: true } };
let sessionStatus = 'authenticated';
jest.mock('next-auth/react', () => ({ useSession: () => ({ data: session, status: sessionStatus }) }));

const mockGetAllAccount = jest.fn();
jest.mock('@api1/account', () => ({ __esModule: true, default: { getAllAccount: () => mockGetAllAccount() } }));

const mockListK8sAccAgentHealth = jest.fn();
jest.mock('@api1/kubernetes1', () => ({ __esModule: true, default: { listK8sAccAgentHealth: (ids: string[]) => mockListK8sAccAgentHealth(ids) } }));

const accounts = (all: unknown[]) => ({ data: { all_accounts: all } });
const account = (over: Record<string, unknown> = {}) => ({ id: 'a1', status: 'active', ...over });

const page = () => (
  <SetupGateProvider>
    <TenantSwitchOverlay />
    <div data-testid='page'>page content</div>
  </SetupGateProvider>
);

beforeEach(() => {
  jest.clearAllMocks();
  jest.useRealTimers();
  pathname = '/home';
  session = { roles: ['tenant_admin'], tenant: { id: 't1' }, uiFeatures: { setupCenter: true } };
  sessionStatus = 'authenticated';
  mockGetAllAccount.mockResolvedValue(accounts([]));
  mockListK8sAccAgentHealth.mockResolvedValue({ data: { data: { agent: [] } } });
  sessionStorage.removeItem(TENANT_SWITCH_FLAG);
});

afterEach(() => {
  jest.useRealTimers();
});

it('renders nothing when no tenant switch is in flight', async () => {
  render(page());
  expect(screen.queryByTestId('tenant-switch-overlay')).not.toBeInTheDocument();
  expect(screen.getByTestId('page')).toBeInTheDocument();
});

it('masks the page while the gate is still deciding, then clears itself once an unlocked tenant resolves', async () => {
  sessionStorage.setItem(TENANT_SWITCH_FLAG, '1');
  mockGetAllAccount.mockResolvedValue(accounts([account()])); // unlocked — one real account

  render(page());
  expect(screen.getByTestId('tenant-switch-overlay')).toBeInTheDocument();

  await waitFor(() => expect(screen.queryByTestId('tenant-switch-overlay')).not.toBeInTheDocument());
  expect(sessionStorage.getItem(TENANT_SWITCH_FLAG)).toBeNull();
});

it('stays up through a pending gate redirect — does not reveal the page before the route actually gets to /setup', async () => {
  sessionStorage.setItem(TENANT_SWITCH_FLAG, '1');
  mockGetAllAccount.mockResolvedValue(accounts([])); // gated — no accounts

  const { rerender } = render(page());

  // The gate finishes loading and decides to lock, but the redirect to
  // /setup (SetupGateContext's own effect) hasn't landed in this mocked
  // router yet — the overlay must not drop before that happens.
  await waitFor(() => expect(mockReplace).toHaveBeenCalledWith('/setup'));
  expect(screen.getByTestId('tenant-switch-overlay')).toBeInTheDocument();

  pathname = '/setup';
  rerender(page());

  expect(screen.queryByTestId('tenant-switch-overlay')).not.toBeInTheDocument();
});

it('gives up after the safety timeout instead of masking the page forever', async () => {
  jest.useFakeTimers();
  sessionStorage.setItem(TENANT_SWITCH_FLAG, '1');
  mockGetAllAccount.mockReturnValue(new Promise(() => {})); // never resolves

  render(page());
  expect(screen.getByTestId('tenant-switch-overlay')).toBeInTheDocument();

  act(() => {
    jest.advanceTimersByTime(8000);
  });

  expect(screen.queryByTestId('tenant-switch-overlay')).not.toBeInTheDocument();
  expect(sessionStorage.getItem(TENANT_SWITCH_FLAG)).toBeNull();
});
