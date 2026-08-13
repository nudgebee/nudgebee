import { render, screen, fireEvent, act } from '@testing-library/react';
import GatewayUsage from '@components/llm/gateway-usage/GatewayUsage';

// The sub-tab of the AI Gateway screen is encoded in the URL hash as
// `#ai-gateway/<sub-tab>` so a shared link lands on the exact sub-tab and it
// survives reload. These tests lock in that read (deep-link → tab) and write
// (tab change → hash) contract.

// `router.replace` returns a promise — it must be mocked as one, because the
// component chains `.catch()` onto it to absorb Next's `cancelled` rejection.
const replaceMock = jest.fn(() => Promise.resolve(true));
let currentAsPath = '/optimise#ai-gateway';

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
    pathname: '/optimise',
    query: {},
    get asPath() {
      return currentAsPath;
    },
    replace: replaceMock,
  })),
}));

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
jest.mock('@components/llm/gateway-usage/views/RequestsView', () => ({ __esModule: true, default: () => <div>requests</div> }));

// The filter bar pulls in heavy pickers; stub to keep the render focused on tabs.
jest.mock('@shared/widgets/CustomDateTimeRangePicker', () => ({ __esModule: true, default: () => <div>date-picker</div> }));
jest.mock('@ui/ToggleGroup', () => ({ ToggleGroup: () => <div>granularity</div> }));

function setHash(hash: string) {
  window.location.hash = hash;
  currentAsPath = `/optimise${hash}`;
}

describe('GatewayUsage sub-tab URL routing', () => {
  beforeEach(() => {
    replaceMock.mockClear();
    replaceMock.mockReturnValue(Promise.resolve(true));
    setHash('#ai-gateway');
  });

  it('opens on the sub-tab named in the hash (deep link)', () => {
    setHash('#ai-gateway/users');
    render(<GatewayUsage />);
    expect(screen.getByTestId('tabs')).toHaveAttribute('data-value', 'users');
    expect(screen.getByTestId('drill-user')).toBeInTheDocument();
  });

  it('falls back to overview when the hash has no sub-tab', () => {
    setHash('#ai-gateway');
    render(<GatewayUsage />);
    expect(screen.getByTestId('tabs')).toHaveAttribute('data-value', 'overview');
  });

  it('ignores an unknown sub-tab and falls back to overview', () => {
    setHash('#ai-gateway/bogus');
    render(<GatewayUsage />);
    expect(screen.getByTestId('tabs')).toHaveAttribute('data-value', 'overview');
  });

  // Mount must never navigate: `tab` is seeded from the hash, so the two already
  // agree. This is what keeps a deep link from being overwritten, and it removes
  // the mount-time navigation that Next could cancel mid-flight.
  it.each([['#ai-gateway'], ['#ai-gateway/models'], ['#ai-gateway/bogus'], ['#summary']])('does not navigate on mount for %s', (hash) => {
    setHash(hash);
    render(<GatewayUsage />);
    expect(replaceMock).not.toHaveBeenCalled();
  });

  it('writes the hash when the user clicks a sub-tab', () => {
    setHash('#ai-gateway/overview');
    render(<GatewayUsage />);
    replaceMock.mockClear();
    // The tab list is keyed by string TabId, so the Models tab is `tab-models`.
    act(() => {
      fireEvent.click(screen.getByTestId('tab-models'));
    });
    expect(replaceMock).toHaveBeenCalledWith(expect.objectContaining({ hash: '#ai-gateway/models' }), undefined, { shallow: true });
  });

  it('writes the hash on the Users → Requests drill-in', () => {
    setHash('#ai-gateway/users');
    render(<GatewayUsage />);
    replaceMock.mockClear();
    act(() => {
      fireEvent.click(screen.getByTestId('drill-user'));
    });
    expect(replaceMock).toHaveBeenCalledWith(expect.objectContaining({ hash: '#ai-gateway/requests' }), undefined, { shallow: true });
  });

  // The hash is the source of truth: when it changes underneath us (browser
  // back/forward, or the top-level tab's own link) the screen must follow it.
  describe('URL → tab', () => {
    it('follows the hash to another sub-tab (browser back/forward)', () => {
      setHash('#ai-gateway/users');
      const { rerender } = render(<GatewayUsage />);
      act(() => {
        setHash('#ai-gateway/models');
        rerender(<GatewayUsage />);
      });
      expect(screen.getByTestId('tabs')).toHaveAttribute('data-value', 'models');
    });

    // Regression: the AI Gateway top-level tab links to a bare `#ai-gateway`
    // (AnchorComponent's getTabUrl), so clicking it while on Requests used to leave
    // the screen on Requests with a URL that no longer named it — sharing that link
    // then landed the recipient on Overview.
    it('resets to overview when the hash drops to a bare #ai-gateway', () => {
      setHash('#ai-gateway/requests');
      const { rerender } = render(<GatewayUsage />);
      expect(screen.getByTestId('tabs')).toHaveAttribute('data-value', 'requests');
      act(() => {
        setHash('#ai-gateway');
        rerender(<GatewayUsage />);
      });
      expect(screen.getByTestId('tabs')).toHaveAttribute('data-value', 'overview');
    });

    // While navigating to another top-level tab this component can still render a
    // tick before it unmounts; it must not claim a hash that isn't its own.
    it('ignores a hash belonging to another top-level tab', () => {
      setHash('#ai-gateway/requests');
      const { rerender } = render(<GatewayUsage />);
      replaceMock.mockClear();
      act(() => {
        setHash('#summary');
        rerender(<GatewayUsage />);
      });
      expect(screen.getByTestId('tabs')).toHaveAttribute('data-value', 'requests');
      expect(replaceMock).not.toHaveBeenCalled();
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
      setHash('#ai-gateway/overview');
      render(<GatewayUsage />);
      act(() => {
        fireEvent.click(screen.getByTestId('tab-requests'));
      });
      await flush();
      expect(replaceMock).toHaveBeenCalled();
      expect(unhandled).not.toHaveBeenCalled();
    });

    it('keeps the tab usable after a cancelled hash write', async () => {
      replaceMock.mockReturnValueOnce(Promise.reject(cancelledError()));
      setHash('#ai-gateway/overview');
      render(<GatewayUsage />);
      act(() => {
        fireEvent.click(screen.getByTestId('tab-requests'));
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
