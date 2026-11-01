import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import '@testing-library/jest-dom';
import SetupProgressBanner from '@components/onboarding/SetupProgressBanner';

jest.mock('src/utils/colors');

const mockPush = jest.fn();
jest.mock('next/router', () => ({ useRouter: () => ({ push: mockPush }) }));

jest.mock('@hooks/useTenantBranding', () => ({
  useBrandingConfig: () => ({ title: mockBrandTitle() }),
}));
const mockBrandTitle = jest.fn(() => 'Acme');

let session: unknown = { tenant: { id: 'tenant-1' } };
jest.mock('next-auth/react', () => ({ useSession: () => ({ data: session }) }));

const mockGetSetupBannerHiddenUntil = jest.fn();
const mockSetSetupBannerHiddenUntil = jest.fn();
jest.mock('@api1/user', () => ({
  __esModule: true,
  default: {
    getSetupBannerHiddenUntil: (...args: unknown[]) => mockGetSetupBannerHiddenUntil(...args),
    setSetupBannerHiddenUntil: (...args: unknown[]) => mockSetSetupBannerHiddenUntil(...args),
  },
}));

let gateState: { loading: boolean; locked: boolean; canManageSetup: boolean; setupCenterEnabled: boolean };
jest.mock('@context/SetupGateContext', () => ({ useSetupGate: () => gateState }));

// `readiness` comes from the same hook SetupCenter itself uses (that's the
// whole point of the fix — the two can never disagree), so it's mocked
// separately from the gate rather than bundled into `gateState` the way a
// single `useSetupGate().readiness` used to be.
let readinessState: {
  readiness: { done: number; total: number; percent: number; outstandingRequired: Array<{ id: string; name: string }> } | null;
};
const mockUseOnboardingReadiness = jest.fn(() => readinessState);
jest.mock('@hooks/useOnboardingReadiness', () => ({ useOnboardingReadiness: () => mockUseOnboardingReadiness() }));

const item = (name: string, id = name) => ({ id, name });

const eligibleGate = () => ({ loading: false, locked: false, canManageSetup: true, setupCenterEnabled: true });
const eligibleReadiness = () => ({
  readiness: {
    done: 3,
    total: 7,
    percent: 43,
    outstandingRequired: [item('Logs'), item('Traces'), item('Alerts'), item('LLM gateway')],
  },
});

beforeEach(() => {
  jest.clearAllMocks();
  mockBrandTitle.mockReturnValue('Acme');
  mockGetSetupBannerHiddenUntil.mockReturnValue(undefined);
  session = { tenant: { id: 'tenant-1' } };
  gateState = eligibleGate();
  readinessState = eligibleReadiness();
});

describe('who sees it', () => {
  it('renders nothing while the gate is still resolving', () => {
    gateState = { ...eligibleGate(), loading: true };
    const { container } = render(<SetupProgressBanner />);
    expect(container).toBeEmptyDOMElement();
  });

  // `UI_ENABLE_SETUP_CENTER` off: inert along with the rest of the gate, see
  // `SetupGateContext.tsx`'s own doc comment. Inert means the readiness
  // lookups never fire either, not just that nothing renders.
  it('renders nothing and never reads readiness while the UI_ENABLE_SETUP_CENTER flag is off', () => {
    gateState = { ...eligibleGate(), setupCenterEnabled: false };
    const { container } = render(<SetupProgressBanner />);
    expect(container).toBeEmptyDOMElement();
    expect(mockUseOnboardingReadiness).not.toHaveBeenCalled();
  });

  it('renders nothing without a readiness snapshot yet', () => {
    readinessState = { readiness: null };
    const { container } = render(<SetupProgressBanner />);
    expect(container).toBeEmptyDOMElement();
  });

  it('renders nothing while the gate is still locked — /setup is the only surface then', () => {
    gateState = { ...eligibleGate(), locked: true };
    const { container } = render(<SetupProgressBanner />);
    expect(container).toBeEmptyDOMElement();
  });

  // A recurring instruction to perform a task they cannot perform: every Add
  // button on the card this would send them to is hidden for them.
  it('renders nothing for a read-only viewer', () => {
    gateState = { ...eligibleGate(), canManageSetup: false };
    const { container } = render(<SetupProgressBanner />);
    expect(container).toBeEmptyDOMElement();
  });

  it('renders nothing once every required item is connected', () => {
    readinessState = { readiness: { done: 7, total: 7, percent: 100, outstandingRequired: [] } };
    const { container } = render(<SetupProgressBanner />);
    expect(container).toBeEmptyDOMElement();
  });

  it('renders for an admin with outstanding items and an open gate', () => {
    render(<SetupProgressBanner />);
    expect(screen.getByTestId('setup-progress-banner')).toBeInTheDocument();
  });
});

describe('what it says', () => {
  it('names the done/total count on the progress bar', () => {
    render(<SetupProgressBanner />);
    expect(screen.getByText('3 of 7')).toBeInTheDocument();
  });

  it('recommends completing what is outstanding, across the tenant', () => {
    render(<SetupProgressBanner />);
    expect(screen.getByText(/we recommend connecting what's still outstanding across your tenant/)).toBeInTheDocument();
  });

  it('names the first three outstanding items as tags, then collapses the rest into a count', () => {
    render(<SetupProgressBanner />);
    expect(screen.getByText('Logs')).toBeInTheDocument();
    expect(screen.getByText('Traces')).toBeInTheDocument();
    expect(screen.getByText('Alerts')).toBeInTheDocument();
    expect(screen.queryByText('LLM gateway')).not.toBeInTheDocument();
    expect(screen.getByText('+1 more')).toBeInTheDocument();
  });

  it('lists every outstanding item as its own tag when there are three or fewer, with no "+more" tag', () => {
    readinessState = { readiness: { done: 5, total: 7, percent: 71, outstandingRequired: [item('Logs'), item('Traces')] } };
    render(<SetupProgressBanner />);
    expect(screen.getByText('Logs')).toBeInTheDocument();
    expect(screen.getByText('Traces')).toBeInTheDocument();
    expect(screen.queryByText(/more/)).not.toBeInTheDocument();
  });

  it('reads the brand name from branding, not a hardcoded product name', () => {
    mockBrandTitle.mockReturnValue('Acme');
    render(<SetupProgressBanner />);
    expect(screen.getByText('Finish setting up Acme')).toBeInTheDocument();
  });

  // CLAUDE.md: a sentence must survive brand resolution not having landed
  // yet, rather than interpolating "undefined" into the title.
  it('falls back to brand-free copy while branding has not resolved', () => {
    mockBrandTitle.mockReturnValue(undefined);
    render(<SetupProgressBanner />);
    expect(screen.getByText('Finish setting up your workspace')).toBeInTheDocument();
  });
});

describe('actions', () => {
  it('View connections navigates to the admin Overview Connections sub-view', () => {
    render(<SetupProgressBanner />);
    fireEvent.click(screen.getByRole('button', { name: 'View connections' }));
    expect(mockPush).toHaveBeenCalledWith('/user-management?action=overview-connections#overview');
  });

  it('snoozing hides the banner immediately, without a reload', () => {
    render(<SetupProgressBanner />);
    fireEvent.click(screen.getByRole('button', { name: 'Snooze' }));
    expect(screen.queryByTestId('setup-progress-banner')).not.toBeInTheDocument();
  });

  it('snoozing holds for roughly 24h, not a shorter or longer window, scoped to the current tenant', () => {
    render(<SetupProgressBanner />);
    fireEvent.click(screen.getByRole('button', { name: 'Snooze' }));
    expect(mockSetSetupBannerHiddenUntil).toHaveBeenCalledWith(expect.any(String), 'tenant-1');
    const stored = new Date(mockSetSetupBannerHiddenUntil.mock.calls[0][0]).getTime();
    const hoursAhead = (stored - Date.now()) / (60 * 60 * 1000);
    expect(hoursAhead).toBeGreaterThan(23.9);
    expect(hoursAhead).toBeLessThan(24.1);
  });

  // Best-effort: losing persistence must not also lose the in-session dismiss.
  it('still hides for this session even if storing the snooze throws', () => {
    mockSetSetupBannerHiddenUntil.mockImplementation(() => {
      throw new Error('storage blocked');
    });
    render(<SetupProgressBanner />);
    fireEvent.click(screen.getByRole('button', { name: 'Snooze' }));
    expect(screen.queryByTestId('setup-progress-banner')).not.toBeInTheDocument();
  });
});

// "Not needed" hides this banner indefinitely — a far-future snooze, not a
// change to what the Setup Center card tracks (setup-center-build-spec.md
// §6.4: no Skip control on the checklist itself, ever).
describe('"Not needed"', () => {
  it('hides the banner immediately', () => {
    render(<SetupProgressBanner />);
    fireEvent.click(screen.getByRole('button', { name: 'Not needed…' }));
    expect(screen.queryByTestId('setup-progress-banner')).not.toBeInTheDocument();
  });

  it('persists a far-future hidden-until, not a 24h one, scoped to the current tenant', () => {
    render(<SetupProgressBanner />);
    fireEvent.click(screen.getByRole('button', { name: 'Not needed…' }));
    expect(mockSetSetupBannerHiddenUntil).toHaveBeenCalledWith(expect.any(String), 'tenant-1');
    const stored = new Date(mockSetSetupBannerHiddenUntil.mock.calls[0][0]).getTime();
    const daysAhead = (stored - Date.now()) / (24 * 60 * 60 * 1000);
    expect(daysAhead).toBeGreaterThan(365 * 100);
  });

  it('still hides for this session even if storing throws', () => {
    mockSetSetupBannerHiddenUntil.mockImplementation(() => {
      throw new Error('storage blocked');
    });
    render(<SetupProgressBanner />);
    fireEvent.click(screen.getByRole('button', { name: 'Not needed…' }));
    expect(screen.queryByTestId('setup-progress-banner')).not.toBeInTheDocument();
  });
});

describe('the snooze', () => {
  it('stays hidden on a later mount while the stored snooze has not expired', () => {
    mockGetSetupBannerHiddenUntil.mockReturnValue(new Date(Date.now() + 60 * 60 * 1000).toISOString());
    const { container } = render(<SetupProgressBanner />);
    expect(container).toBeEmptyDOMElement();
  });

  it('returns once the stored snooze has passed — not a permanent dismissal', () => {
    mockGetSetupBannerHiddenUntil.mockReturnValue(new Date(Date.now() - 60 * 60 * 1000).toISOString());
    render(<SetupProgressBanner />);
    expect(screen.getByTestId('setup-progress-banner')).toBeInTheDocument();
  });

  it('renders when reading the stored preference throws — never a permanent silent suppression', () => {
    mockGetSetupBannerHiddenUntil.mockImplementation(() => {
      throw new Error('storage blocked');
    });
    render(<SetupProgressBanner />);
    expect(screen.getByTestId('setup-progress-banner')).toBeInTheDocument();
  });

  // Known issue #18: the old flat, unscoped key meant a snooze clicked while
  // viewing one tenant silently suppressed the banner on any other tenant
  // sharing the same browser. The lookup must be keyed on the tenant actually
  // being viewed, not whichever tenant happened to write the flag.
  it('does not apply a snooze recorded for a different tenant', () => {
    mockGetSetupBannerHiddenUntil.mockImplementation((tenantId: string) =>
      tenantId === 'tenant-A' ? new Date(Date.now() + 60 * 60 * 1000).toISOString() : undefined
    );
    session = { tenant: { id: 'tenant-B' } };
    render(<SetupProgressBanner />);
    expect(screen.getByTestId('setup-progress-banner')).toBeInTheDocument();
    expect(mockGetSetupBannerHiddenUntil).toHaveBeenCalledWith('tenant-B');
  });

  it('never looks up a snooze before the tenant id is known', () => {
    session = null;
    render(<SetupProgressBanner />);
    expect(mockGetSetupBannerHiddenUntil).not.toHaveBeenCalled();
  });
});

// Known issues #7/#18's bug class, one level up: the banner and the card
// used to read readiness from two different fetches (this hook vs the
// gate's own cheaper one), so a K8s account still waiting on its agent could
// read "connected" on one surface and not the other. Both now call
// `useOnboardingReadiness`, so there is only one fetch to ever be wrong.
describe('reading from the same hook SetupCenter uses', () => {
  it('shows exactly the numbers the shared hook reports, not a separately-derived count', () => {
    readinessState = {
      readiness: { done: 1, total: 8, percent: 13, outstandingRequired: [item('Metrics'), item('Logs'), item('Traces'), item('Alerts')] },
    };
    render(<SetupProgressBanner />);
    expect(screen.getByText('1 of 8')).toBeInTheDocument();
  });

  it('has no readiness of its own to disagree with the card — stays hidden the moment the shared hook reports none outstanding', () => {
    readinessState = { readiness: { done: 8, total: 8, percent: 100, outstandingRequired: [] } };
    const { container } = render(<SetupProgressBanner />);
    expect(container).toBeEmptyDOMElement();
  });
});
