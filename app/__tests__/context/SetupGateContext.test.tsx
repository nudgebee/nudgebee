import React from 'react';
import { render, screen, waitFor } from '@testing-library/react';
import '@testing-library/jest-dom';
import { SetupGateProvider, useSetupGate } from '@context/SetupGateContext';
import { evaluateOnboarding } from '@lib/onboardingReadiness';

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
// Defaults every requested id to CONNECTED, so existing tests that never asked
// about agent health keep seeing a K8s row resolve to `done`.
const agentHealth = (ids: string[], status = 'CONNECTED') => ({ data: { data: { agent: ids.map((id) => ({ cloud_account_id: id, status })) } } });

// `toHaveTextContent` substring-matches. A probe rendering 'locked'/'unlocked'
// therefore made every `toHaveTextContent('locked')` pass in *both* states,
// because 'unlocked' contains 'locked' — assertions that read fine and tested
// nothing. These tokens share no substring, so they mean what they say.
const state = (loading: boolean, locked: boolean) => (loading ? 'PENDING' : locked ? 'GATED' : 'OPEN');

const Probe = () => {
  const { loading, locked } = useSetupGate();
  return <div data-testid='probe'>{state(loading, locked)}</div>;
};

const renderGate = () =>
  render(
    <SetupGateProvider>
      <Probe />
    </SetupGateProvider>
  );

const accounts = (all: unknown[]) => ({ data: { all_accounts: all } });
const account = (over: Record<string, unknown> = {}) => ({ id: 'a1', status: 'active', ...over });

beforeEach(() => {
  jest.clearAllMocks();
  pathname = '/home';
  session = { roles: ['tenant_admin'], tenant: { id: 't1' }, uiFeatures: { setupCenter: true } };
  sessionStatus = 'authenticated';
  mockGetAllAccount.mockResolvedValue(accounts([]));
  mockListK8sAccAgentHealth.mockResolvedValue(agentHealth([]));
});

describe('the rule', () => {
  it('gates a tenant with no accounts', async () => {
    renderGate();
    await waitFor(() => expect(screen.getByTestId('probe')).toHaveTextContent('GATED'));
  });

  it('lets a tenant with one account through', async () => {
    mockGetAllAccount.mockResolvedValue(accounts([account()]));
    renderGate();
    await waitFor(() => expect(screen.getByTestId('probe')).toHaveTextContent('OPEN'));
  });

  // The whole point of deriving it: setup state belongs to the tenant, so a
  // second admin on a first login inherits it rather than starting over.
  it('does not care which browser or which user is asking', async () => {
    mockGetAllAccount.mockResolvedValue(accounts([account()]));
    session = { roles: ['tenant_admin'], tenant: { id: 't1' }, email: 'second.admin@example.com', uiFeatures: { setupCenter: true } };
    renderGate();
    await waitFor(() => expect(screen.getByTestId('probe')).toHaveTextContent('OPEN'));
  });
});

describe('what counts as an account', () => {
  // Deliberately a superset of the Setup Center's own filter. If the card could
  // see an account the gate could not, it would offer a way out on a route the
  // guard bounces straight back.
  it('counts a deactivated account — a paused tenant is not a new one', async () => {
    mockGetAllAccount.mockResolvedValue(accounts([account({ status: 'inactive' })]));
    renderGate();
    await waitFor(() => expect(screen.getByTestId('probe')).toHaveTextContent('OPEN'));
  });

  it('does not count a deleted account', async () => {
    mockGetAllAccount.mockResolvedValue(accounts([account({ status: 'deleted' })]));
    renderGate();
    await waitFor(() => expect(screen.getByTestId('probe')).toHaveTextContent('GATED'));
  });

  it('does not count the demo account', async () => {
    mockGetAllAccount.mockResolvedValue(accounts([account({ id: 'demo' })]));
    renderGate();
    await waitFor(() => expect(screen.getByTestId('probe')).toHaveTextContent('GATED'));
  });
});

// A K8s row used to unlock the whole nav the instant it existed, before its
// agent ever checked in — seeing that live is what reversed the original
// existence-only rule. The lock now keys on `partial` specifically
// (still connecting), not on anything short of `done`, so it doesn't also
// re-wall an established tenant whose only account happens to be paused or
// errored — see 'what counts as an account' above for that case.
describe('locked while an account is still connecting, not merely added', () => {
  const k8s = (over: Record<string, unknown> = {}) => account({ cloud_provider: 'k8s', ...over });

  it('stays gated for a K8s account whose agent has not checked in yet', async () => {
    mockGetAllAccount.mockResolvedValue(accounts([k8s()]));
    mockListK8sAccAgentHealth.mockResolvedValue(agentHealth(['a1'], 'PENDING'));
    renderGate();
    await waitFor(() => expect(screen.getByTestId('probe')).toHaveTextContent('GATED'));
  });

  it('opens once that same account finishes connecting', async () => {
    mockGetAllAccount.mockResolvedValue(accounts([k8s()]));
    mockListK8sAccAgentHealth.mockResolvedValue(agentHealth(['a1'], 'CONNECTED'));
    renderGate();
    await waitFor(() => expect(screen.getByTestId('probe')).toHaveTextContent('OPEN'));
  });

  it('opens on a second, fully-connected account even while the first is still connecting', async () => {
    const aws = account({ id: 'a2', cloud_provider: 'AWS' });
    mockGetAllAccount.mockResolvedValue(accounts([k8s(), aws]));
    mockListK8sAccAgentHealth.mockResolvedValue(agentHealth(['a1'], 'PENDING'));
    renderGate();
    await waitFor(() => expect(screen.getByTestId('probe')).toHaveTextContent('OPEN'));
  });

  it('does not need to ask about agent health at all for a non-K8s account', async () => {
    mockGetAllAccount.mockResolvedValue(accounts([account({ cloud_provider: 'AWS' })]));
    renderGate();
    await waitFor(() => expect(screen.getByTestId('probe')).toHaveTextContent('OPEN'));
    expect(mockListK8sAccAgentHealth).not.toHaveBeenCalled();
  });

  it('falls back to trusting status alone when the agent-health lookup itself fails', async () => {
    // Same principle as issue #24: a lookup that cannot answer must not
    // strand every K8s account at partial forever.
    mockGetAllAccount.mockResolvedValue(accounts([k8s()]));
    mockListK8sAccAgentHealth.mockRejectedValue(new Error('network'));
    renderGate();
    await waitFor(() => expect(screen.getByTestId('probe')).toHaveTextContent('OPEN'));
  });

  it('opens for a K8s account whose install failed outright — error is not "still connecting"', async () => {
    mockGetAllAccount.mockResolvedValue(accounts([k8s({ status: 'error' })]));
    renderGate();
    await waitFor(() => expect(screen.getByTestId('probe')).toHaveTextContent('OPEN'));
  });
});

describe('locking requires positive evidence', () => {
  // An incorrect unlock shows someone an empty dashboard they can navigate out
  // of. An incorrect lock takes a paying customer's product away.
  it('does not lock when the lookup returns an error', async () => {
    mockGetAllAccount.mockResolvedValue(new Error('upstream exploded'));
    renderGate();
    await waitFor(() => expect(screen.getByTestId('probe')).toHaveTextContent('OPEN'));
    expect(mockReplace).not.toHaveBeenCalled();
  });

  it('does not lock when the lookup returns no payload', async () => {
    mockGetAllAccount.mockResolvedValue(undefined);
    renderGate();
    await waitFor(() => expect(screen.getByTestId('probe')).toHaveTextContent('OPEN'));
    expect(mockReplace).not.toHaveBeenCalled();
  });

  it('does not lock when the lookup rejects', async () => {
    mockGetAllAccount.mockRejectedValue(new Error('network'));
    renderGate();
    await waitFor(() => expect(screen.getByTestId('probe')).toHaveTextContent('OPEN'));
  });

  it('does not lock while the lookup is still resolving', () => {
    mockGetAllAccount.mockReturnValue(new Promise(() => {}));
    renderGate();
    expect(screen.getByTestId('probe')).toHaveTextContent('PENDING');
    expect(mockReplace).not.toHaveBeenCalled();
  });

  it('does not lock, or even ask, while the session is still resolving', () => {
    sessionStatus = 'loading';
    session = undefined;
    renderGate();
    expect(screen.getByTestId('probe')).toHaveTextContent('PENDING');
    // The tenant id arrives with the session, so querying first would only buy
    // an answer that is immediately re-fetched.
    expect(mockGetAllAccount).not.toHaveBeenCalled();
    expect(mockReplace).not.toHaveBeenCalled();
  });

  it('does not lock a signed-out visitor', async () => {
    sessionStatus = 'unauthenticated';
    session = null;
    renderGate();
    await waitFor(() => expect(screen.getByTestId('probe')).toHaveTextContent('OPEN'));
    expect(mockGetAllAccount).not.toHaveBeenCalled();
    expect(mockReplace).not.toHaveBeenCalled();
  });
});

describe('a non-admin is gated too now, but never onto a checklist', () => {
  // Used to also require canManageSetup, on the theory that holding someone on
  // a page whose only exit is an action they lack permission for is a trap
  // with no exit. Reversed after a live report — see the file's own doc
  // comment for why. SetupCenter is what keeps the trap from recurring: it
  // reads `hasConnectedAccount` off this same context and shows a non-admin
  // one line, never the checklist. This file only pins `locked` itself.
  it('gates a read-only user on a tenant with no accounts', async () => {
    session = { roles: ['tenant_admin_readonly'], tenant: { id: 't1' }, uiFeatures: { setupCenter: true } };
    renderGate();
    await waitFor(() => expect(screen.getByTestId('probe')).toHaveTextContent('GATED'));
    expect(mockReplace).toHaveBeenCalledWith('/setup');
  });

  it('gates a user with no roles at all on a tenant with no accounts', async () => {
    session = { roles: [], tenant: { id: 't1' }, uiFeatures: { setupCenter: true } };
    renderGate();
    await waitFor(() => expect(screen.getByTestId('probe')).toHaveTextContent('GATED'));
  });

  it('never gates a non-admin once the tenant has an account — role and account state are independent', async () => {
    session = { roles: [], tenant: { id: 't1' }, uiFeatures: { setupCenter: true } };
    mockGetAllAccount.mockResolvedValue(accounts([account()]));
    renderGate();
    await waitFor(() => expect(screen.getByTestId('probe')).toHaveTextContent('OPEN'));
    expect(mockReplace).not.toHaveBeenCalled();
  });

  it('reports who may act, so the card does not have to ask twice', async () => {
    const Roles = () => {
      const { canManageSetup } = useSetupGate();
      return <div data-testid='roles'>{String(canManageSetup)}</div>;
    };
    render(
      <SetupGateProvider>
        <Roles />
      </SetupGateProvider>
    );
    await waitFor(() => expect(screen.getByTestId('roles')).toHaveTextContent('true'));
  });

  it('reports hasConnectedAccount too, so the page can tell "unconfigured" apart from "just not an admin"', async () => {
    const Signal = () => {
      const { hasConnectedAccount } = useSetupGate();
      return <div data-testid='signal'>{String(hasConnectedAccount)}</div>;
    };
    render(
      <SetupGateProvider>
        <Signal />
      </SetupGateProvider>
    );
    await waitFor(() => expect(screen.getByTestId('signal')).toHaveTextContent('false'));
  });
});

describe('the route guard', () => {
  it('sends a gated tenant to /setup from anywhere else', async () => {
    pathname = '/kubernetes';
    renderGate();
    await waitFor(() => expect(mockReplace).toHaveBeenCalledWith('/setup'));
  });

  // `replace`, not `push`: pushing would leave the blocked route in history, so
  // Back would land on it, redirect again, and trap the user in a loop.
  it('replaces rather than pushes, so Back cannot loop', async () => {
    pathname = '/kubernetes';
    renderGate();
    await waitFor(() => expect(mockReplace).toHaveBeenCalled());
    expect(mockReplace).toHaveBeenCalledTimes(1);
  });

  it('leaves a gated tenant alone once they are on /setup', async () => {
    pathname = '/setup';
    renderGate();
    await waitFor(() => expect(screen.getByTestId('probe')).toHaveTextContent('GATED'));
    expect(mockReplace).not.toHaveBeenCalled();
  });

  it('never redirects a tenant that has an account', async () => {
    mockGetAllAccount.mockResolvedValue(accounts([account()]));
    pathname = '/kubernetes';
    renderGate();
    await waitFor(() => expect(screen.getByTestId('probe')).toHaveTextContent('OPEN'));
    expect(mockReplace).not.toHaveBeenCalled();
  });
});

describe('re-evaluation', () => {
  // Carrying tenant A's answer into tenant B drops an admin into a brand-new
  // tenant's empty product with no checklist and no explanation.
  it('re-reads when the tenant changes', async () => {
    mockGetAllAccount.mockResolvedValue(accounts([account()]));
    const { rerender } = renderGate();
    await waitFor(() => expect(screen.getByTestId('probe')).toHaveTextContent('OPEN'));

    mockGetAllAccount.mockResolvedValue(accounts([]));
    session = { roles: ['tenant_admin'], tenant: { id: 't2' }, uiFeatures: { setupCenter: true } };
    rerender(
      <SetupGateProvider>
        <Probe />
      </SetupGateProvider>
    );
    await waitFor(() => expect(screen.getByTestId('probe')).toHaveTextContent('GATED'));
    expect(mockGetAllAccount).toHaveBeenCalledTimes(2);
  });

  // Without this, connecting the first account leaves the user gated until they
  // reload the page — the card says connected, the product says no.
  it('re-reads on demand, so the gate lifts when an account is added', async () => {
    const Refresher = () => {
      const { locked, loading, refresh } = useSetupGate();
      return (
        <>
          <div data-testid='probe'>{state(loading, locked)}</div>
          <button type='button' onClick={refresh} data-testid='refresh'>
            refresh
          </button>
        </>
      );
    };
    render(
      <SetupGateProvider>
        <Refresher />
      </SetupGateProvider>
    );
    await waitFor(() => expect(screen.getByTestId('probe')).toHaveTextContent('GATED'));

    mockGetAllAccount.mockResolvedValue(accounts([account()]));
    screen.getByTestId('refresh').click();
    await waitFor(() => expect(screen.getByTestId('probe')).toHaveTextContent('OPEN'));
  });
});

// `readiness` used to live on this context — a cheaper, accounts-only
// snapshot for `SetupProgressBanner`. It moved to `useOnboardingReadiness`
// (shared with `SetupCenter`, so the two banner/card numbers can never
// disagree — see that hook's doc comment), so this context is back to just
// deciding the lock. Its own tests moved with it (SetupProgressBanner.test.tsx,
// SetupCenter.test.tsx). What stays worth testing here is the invariant the
// old snapshot used to double as a proof of: the gate's own account count
// must never be *stricter* than whatever the readiness model counts.
describe('the readiness model can disagree with the gate, in only one direction', () => {
  // Deliberate, and already true of SetupCenter today (known issues #10,
  // MITIGATED): the gate's own account count is a superset of the readiness
  // model's — a deactivated account still opens the gate, but the model's
  // stricter filter does not count it as connected. If it were the other way
  // round, the card could show an exit action the guard's own redirect would
  // immediately bounce right back — see `countsAsAccount`'s doc comment.
  it('stays unlocked for an account the stricter readiness model would not count as connected', async () => {
    const inactiveAccount = account({ status: 'inactive' });
    mockGetAllAccount.mockResolvedValue(accounts([inactiveAccount]));
    renderGate();
    await waitFor(() => expect(screen.getByTestId('probe')).toHaveTextContent('OPEN'));
    expect(evaluateOnboarding({ accounts: [inactiveAccount] }).hasCloudAccount).toBe(false);
  });
});

// `UI_ENABLE_SETUP_CENTER` wraps all of it — see the file's own doc comment.
// Off is meant to be indistinguishable from the feature never having shipped,
// not just a hidden lock.
describe('UI_ENABLE_SETUP_CENTER', () => {
  it('never locks a zero-account tenant while the flag is off', async () => {
    session = { roles: ['tenant_admin'], tenant: { id: 't1' }, uiFeatures: { setupCenter: false } };
    renderGate();
    await waitFor(() => expect(screen.getByTestId('probe')).toHaveTextContent('OPEN'));
    expect(mockReplace).not.toHaveBeenCalled();
  });

  it('never locks a non-admin either, while the flag is off', async () => {
    session = { roles: [], tenant: { id: 't1' }, uiFeatures: { setupCenter: false } };
    renderGate();
    await waitFor(() => expect(screen.getByTestId('probe')).toHaveTextContent('OPEN'));
  });

  it('does not spend a request on the account lookup while the flag is off', async () => {
    session = { roles: ['tenant_admin'], tenant: { id: 't1' }, uiFeatures: { setupCenter: false } };
    renderGate();
    await waitFor(() => expect(screen.getByTestId('probe')).toHaveTextContent('OPEN'));
    expect(mockGetAllAccount).not.toHaveBeenCalled();
  });

  it('exposes false through the context so /setup and the banner can stay inert too', async () => {
    const Flag = () => {
      const { setupCenterEnabled } = useSetupGate();
      return <div data-testid='flag'>{String(setupCenterEnabled)}</div>;
    };
    session = { roles: ['tenant_admin'], tenant: { id: 't1' }, uiFeatures: { setupCenter: false } };
    render(
      <SetupGateProvider>
        <Flag />
      </SetupGateProvider>
    );
    await waitFor(() => expect(screen.getByTestId('flag')).toHaveTextContent('false'));
  });

  it('exposes true once the session says so', async () => {
    const Flag = () => {
      const { setupCenterEnabled } = useSetupGate();
      return <div data-testid='flag'>{String(setupCenterEnabled)}</div>;
    };
    render(
      <SetupGateProvider>
        <Flag />
      </SetupGateProvider>
    );
    await waitFor(() => expect(screen.getByTestId('flag')).toHaveTextContent('true'));
  });
});
