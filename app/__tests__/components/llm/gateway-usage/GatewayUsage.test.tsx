import { render, screen, fireEvent, act } from '@testing-library/react';
import GatewayUsage from '@components/llm/gateway-usage/GatewayUsage';

// The AI Gateway page's tabs are grouped into sections, and the open tab is
// encoded in the URL hash as `#<section>` (its default tab) or
// `#<section>/<tab>`, so a shared link lands on the exact tab and survives
// reload. These tests lock in that read (deep-link → tab) and write (tab change
// → hash) contract, including push vs replace when a drill-in crosses sections.

// `router.replace`/`push` return promises — they must be mocked as such, because
// the component chains `.catch()` onto them to absorb Next's `cancelled` rejection.
const replaceMock = jest.fn(() => Promise.resolve(true));
const pushMock = jest.fn(() => Promise.resolve(true));
let currentAsPath = '/ai-gateway#monitor';
let mockCanViewUsers = true;

// Next rejects an in-flight navigation that a newer one supersedes with this
// error shape (next/dist/client/index.js — `error.cancelled = true`).
function cancelledError() {
  return Object.assign(new Error('Cancel rendering route'), { cancelled: true });
}

// Drains the microtask queue so a promise rejection has a chance to surface as
// an unhandledRejection before we assert on it.
async function flush() {
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
}

jest.mock('next/router', () => ({
  useRouter: jest.fn(() => ({
    pathname: '/ai-gateway',
    query: {},
    get asPath() {
      return currentAsPath;
    },
    replace: replaceMock,
    push: pushMock,
  })),
}));

jest.mock('@lib/auth', () => ({ canViewGatewayUserAccess: () => mockCanViewUsers }));

// The data fetch is irrelevant to routing — stub it out.
jest.mock('@components/llm/gateway-usage/useGatewayData', () => ({
  useGatewayData: () => ({ loading: false, error: null, metrics: null }),
}));

// Replace CustomTabs with a minimal harness that exposes a click-per-tab and
// reflects the current value, so tests can switch tabs without the real DOM.
jest.mock('@shared/navigation/Tabs', () => ({
  __esModule: true,
  default: ({ options, value, onChange }: any) => (
    <div data-testid='tabs' data-value={value}>
      {options?.tabOptions?.map((opt: any) => (
        <button key={opt.value} data-testid={`tab-${opt.value}`} onClick={() => onChange(opt.value)}>
          {opt.text}
        </button>
      ))}
    </div>
  ),
}));

// Views are stubbed; the Users view exposes its drill-in callback so we can
// assert the drill-in path also writes the hash.
jest.mock('@components/llm/gateway-usage/views/ConnectView', () => ({ __esModule: true, default: () => <div>connect</div> }));
jest.mock('@components/llm/gateway-usage/views/OverviewView', () => ({ __esModule: true, default: () => <div>overview</div> }));
jest.mock('@components/llm/gateway-usage/views/ModelsView', () => ({ __esModule: true, default: () => <div>models</div> }));
jest.mock('@components/llm/gateway-usage/views/UsersView', () => ({
  __esModule: true,
  default: ({ onSelectUser }: any) => (
    <button data-testid='drill-user' onClick={() => onSelectUser('u1', 'Alice')}>
      users
    </button>
  ),
}));
jest.mock('@components/llm/gateway-usage/views/KeysView', () => ({ __esModule: true, default: () => <div>keys</div> }));
jest.mock('@components/llm/gateway-usage/views/RequestsView', () => ({ __esModule: true, default: () => <div>requests</div> }));
jest.mock('@components/llm/gateway-usage/views/SessionsView', () => ({ __esModule: true, default: () => <div>sessions</div> }));
jest.mock('@components/llm/gateway-usage/views/ToolsView', () => ({ __esModule: true, default: () => <div>tools</div> }));
jest.mock('@components/llm/gateway-usage/views/GovernanceView', () => ({ __esModule: true, default: () => <div>governance</div> }));

// The filter bar pulls in heavy pickers; stub to keep the render focused on tabs.
jest.mock('@shared/widgets/CustomDateTimeRangePicker', () => ({ __esModule: true, default: () => <div>date-picker</div> }));
jest.mock('@ui/ToggleGroup', () => ({ ToggleGroup: () => <div>granularity</div> }));

function setHash(hash: string) {
  window.location.hash = hash;
  currentAsPath = `/ai-gateway${hash}`;
}

describe('GatewayUsage tab URL routing', () => {
  beforeEach(() => {
    replaceMock.mockClear();
    replaceMock.mockReturnValue(Promise.resolve(true));
    pushMock.mockClear();
    mockCanViewUsers = true;
    setHash('#monitor');
  });

  it.each([
    ['#monitor/users', 'users'],
    ['#monitor', 'overview'],
    ['#monitor/bogus', 'overview'],
    ['#activity', 'requests'],
    ['#activity/tools', 'tools'],
  ])('opens %s on %s', (hash, tab) => {
    setHash(hash);
    render(<GatewayUsage />);
    expect(screen.getByTestId('tabs')).toHaveAttribute('data-value', tab);
  });

  it('shows only the open section’s tabs', () => {
    setHash('#activity');
    render(<GatewayUsage />);
    expect(screen.getByTestId('tab-sessions')).toBeInTheDocument();
    expect(screen.queryByTestId('tab-models')).not.toBeInTheDocument();
  });

  it.each([
    ['#governance', 'governance'],
    ['#connect', 'connect'],
  ])('shows no tab strip for the one-tab section %s', (hash, view) => {
    setHash(hash);
    render(<GatewayUsage />);
    expect(screen.queryByTestId('tabs')).not.toBeInTheDocument();
    expect(screen.getByText(view)).toBeInTheDocument();
  });

  it('hides the Users tab, and its deep link, from a non-admin', () => {
    mockCanViewUsers = false;
    setHash('#monitor/users');
    render(<GatewayUsage />);
    expect(screen.getByTestId('tabs')).toHaveAttribute('data-value', 'overview');
    expect(screen.queryByTestId('tab-users')).not.toBeInTheDocument();
  });

  // Mount must not navigate while the hash already selects the tab: that's what
  // keeps a deep link from being overwritten.
  it.each([['#monitor'], ['#monitor/overview'], ['#monitor/models'], ['#activity/sessions'], ['#governance']])(
    'does not navigate on mount for %s',
    (hash) => {
      setHash(hash);
      render(<GatewayUsage />);
      expect(replaceMock).not.toHaveBeenCalled();
      expect(pushMock).not.toHaveBeenCalled();
    }
  );

  // No hash leaves the page's top tab strip on its first section (Connect) while
  // Overview shows; a tab named under the wrong section highlights the wrong one.
  it.each([
    ['', '#monitor'],
    ['#monitor/requests', '#activity'],
  ])('rewrites %s to the tab’s own hash %s on mount', (hash, canonical) => {
    setHash(hash);
    render(<GatewayUsage />);
    expect(replaceMock).toHaveBeenCalledWith(expect.objectContaining({ hash: canonical }), undefined, { shallow: true });
    expect(pushMock).not.toHaveBeenCalled();
  });

  it('replaces the hash when the user clicks a tab in the same section', () => {
    render(<GatewayUsage />);
    act(() => {
      fireEvent.click(screen.getByTestId('tab-models'));
    });
    expect(replaceMock).toHaveBeenCalledWith(expect.objectContaining({ hash: '#monitor/models' }), undefined, { shallow: true });
    expect(pushMock).not.toHaveBeenCalled();
  });

  // Crossing sections is a push, so browser Back returns to the Users list
  // instead of leaving the page.
  it('pushes the hash on the Users → Requests drill-in', () => {
    setHash('#monitor/users');
    render(<GatewayUsage />);
    act(() => {
      fireEvent.click(screen.getByTestId('drill-user'));
    });
    expect(pushMock).toHaveBeenCalledWith(expect.objectContaining({ hash: '#activity' }), undefined, { shallow: true });
    expect(replaceMock).not.toHaveBeenCalled();
  });

  // The hash is the source of truth: when it changes underneath us (browser
  // back/forward, the sidenav, the page's top tab strip) the screen must follow.
  describe('URL → tab', () => {
    it('follows the hash to another tab (browser back/forward)', () => {
      setHash('#monitor/users');
      const { rerender } = render(<GatewayUsage />);
      act(() => {
        setHash('#monitor/models');
        rerender(<GatewayUsage />);
      });
      expect(screen.getByTestId('tabs')).toHaveAttribute('data-value', 'models');
    });

    it('resets to the section default when the hash drops to a bare section', () => {
      setHash('#activity/tools');
      const { rerender } = render(<GatewayUsage />);
      act(() => {
        setHash('#activity');
        rerender(<GatewayUsage />);
      });
      expect(screen.getByTestId('tabs')).toHaveAttribute('data-value', 'requests');
    });

    // A sidenav or top-strip click already put the hash in the URL; writing it
    // again (or pushing a second history entry) would be wrong.
    it('does not write back a hash it was moved to', () => {
      setHash('#monitor/models');
      const { rerender } = render(<GatewayUsage />);
      act(() => {
        setHash('#activity');
        rerender(<GatewayUsage />);
      });
      expect(screen.getByTestId('tabs')).toHaveAttribute('data-value', 'requests');
      expect(replaceMock).not.toHaveBeenCalled();
      expect(pushMock).not.toHaveBeenCalled();
    });

    // Next leaves router.asPath alone for a native hash change (the guided tour
    // sets location.hash), so hashchange alone must move the tab.
    it('follows a native hashchange that Next does not report', () => {
      render(<GatewayUsage />);
      act(() => {
        window.location.hash = '#activity/sessions';
        window.dispatchEvent(new HashChangeEvent('hashchange'));
      });
      expect(screen.getByTestId('tabs')).toHaveAttribute('data-value', 'sessions');
    });
  });

  // Regression: the hash write used to leave Next's rejected navigation promise
  // unhandled, surfacing as "Cancel rendering route" in the dev overlay whenever a
  // newer navigation superseded it.
  describe('superseded navigation', () => {
    let unhandled: jest.Mock;

    beforeEach(() => {
      unhandled = jest.fn();
      process.on('unhandledRejection', unhandled);
    });

    afterEach(() => {
      process.off('unhandledRejection', unhandled);
    });

    it('absorbs a cancelled rejection without an unhandled rejection', async () => {
      replaceMock.mockReturnValueOnce(Promise.reject(cancelledError()));
      render(<GatewayUsage />);
      act(() => {
        fireEvent.click(screen.getByTestId('tab-keys'));
      });
      await flush();
      expect(replaceMock).toHaveBeenCalled();
      expect(unhandled).not.toHaveBeenCalled();
    });

    it('keeps the tab usable after a cancelled hash write', async () => {
      replaceMock.mockReturnValueOnce(Promise.reject(cancelledError()));
      render(<GatewayUsage />);
      act(() => {
        fireEvent.click(screen.getByTestId('tab-keys'));
      });
      await flush();
      // The component must still respond to tab changes after absorbing the reject.
      act(() => {
        fireEvent.click(screen.getByTestId('tab-users'));
      });
      expect(screen.getByTestId('tabs')).toHaveAttribute('data-value', 'users');
      expect(unhandled).not.toHaveBeenCalled();
    });
  });
});
