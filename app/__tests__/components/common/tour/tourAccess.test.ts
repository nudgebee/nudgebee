/**
 * Guards the guide-visibility rule: a guide is offered only to users who can
 * actually complete it, and — just as importantly — is NOT hidden from users who
 * can. Both failure directions have shipped before:
 *   - the automation guides declared 'write' (tenant-scoped) while the button
 *     they drive is gated on hasWriteAccess(accountId), hiding them from the
 *     account_admins who could see the button;
 *   - the knowledge-graph guide declared an OPT-OUT feature flag, which
 *     fail-closed `hasFeatureAccessCached` reads as "off" for every tenant that
 *     never explicitly enabled it — i.e. everyone who has the feature.
 * The registry-contract block at the bottom pins both.
 */
const mockHasWriteAccess = jest.fn();
const mockHasFeatureAccessCached = jest.fn();
const mockGetUserSession = jest.fn();
const mockIsUiFeatureEnabled = jest.fn();

jest.mock('@lib/auth', () => ({
  hasWriteAccess: (...args: any[]) => mockHasWriteAccess(...args),
  hasFeatureAccessCached: (...args: any[]) => mockHasFeatureAccessCached(...args),
  getUserSession: () => mockGetUserSession(),
  isUiFeatureEnabled: (...args: any[]) => mockIsUiFeatureEnabled(...args),
}));

import { canAccessTour } from '@components/common/tour/tourAccess';
import { TOURS, type TourDef } from '@components/common/tour/tours';

const tour = (overrides: Partial<TourDef> = {}): TourDef => ({
  id: 'test-tour',
  title: 'Test tour',
  steps: [{ element: '#x', title: 't', description: 'd' }],
  ...overrides,
});

/**
 * The role simulators below mirror the real semantics of `hasWriteAccess` in
 * lib/auth.tsx — in particular that the NO-ARG call can only return true via its
 * tenant_admin branch (`accountIds.includes(undefined)` is always false), which
 * is the whole reason 'write' and 'account-write' differ.
 */
function asTenantAdmin(): void {
  mockHasWriteAccess.mockImplementation(() => true);
  mockGetUserSession.mockReturnValue({ accountIds: [] });
}

function asAccountAdmin(accountIds: string[] = ['acct-1']): void {
  mockHasWriteAccess.mockImplementation((accountId?: string) => accountId !== undefined && accountIds.includes(accountId));
  mockGetUserSession.mockReturnValue({ accountIds });
}

function asReadOnly(): void {
  mockHasWriteAccess.mockImplementation(() => false);
  mockGetUserSession.mockReturnValue({ accountIds: [] });
}

beforeEach(() => {
  jest.clearAllMocks();
  mockHasFeatureAccessCached.mockReturnValue(false);
  mockIsUiFeatureEnabled.mockReturnValue(false);
  mockGetUserSession.mockReturnValue({});
});

describe('canAccessTour', () => {
  describe('ungated guides', () => {
    it('offers a guide with no requirements to everyone, including read-only users', () => {
      asReadOnly();
      expect(canAccessTour(tour())).toBe(true);
    });
  });

  describe("requires: 'write' (tenant-scoped)", () => {
    const writeTour = tour({ requires: 'write' });

    it('offers it to a tenant admin', () => {
      asTenantAdmin();
      expect(canAccessTour(writeTour)).toBe(true);
    });

    it('hides it from an account admin — a bare hasWriteAccess() gate is tenant-admin-only', () => {
      asAccountAdmin();
      expect(canAccessTour(writeTour)).toBe(false);
    });

    it('hides it from a read-only user', () => {
      asReadOnly();
      expect(canAccessTour(writeTour)).toBe(false);
    });

    it('resolves against the no-arg call, matching the button it mirrors', () => {
      asTenantAdmin();
      canAccessTour(writeTour);
      expect(mockHasWriteAccess).toHaveBeenCalledWith();
    });
  });

  describe("requires: 'account-write' (account-scoped)", () => {
    const accountWriteTour = tour({ requires: 'account-write' });

    it('offers it to a tenant admin', () => {
      asTenantAdmin();
      expect(canAccessTour(accountWriteTour)).toBe(true);
    });

    it('offers it to an account admin — they pass hasWriteAccess(accountId) on their own account', () => {
      asAccountAdmin(['acct-1']);
      expect(canAccessTour(accountWriteTour)).toBe(true);
    });

    it('offers it to an account admin who owns any one of several accounts', () => {
      asAccountAdmin(['acct-1', 'acct-2', 'acct-3']);
      expect(canAccessTour(accountWriteTour)).toBe(true);
    });

    it('hides it from a read-only user with no writable account', () => {
      asReadOnly();
      expect(canAccessTour(accountWriteTour)).toBe(false);
    });

    it('hides it from a user whose session carries no accounts at all', () => {
      mockHasWriteAccess.mockImplementation(() => false);
      mockGetUserSession.mockReturnValue({});
      expect(canAccessTour(accountWriteTour)).toBe(false);
    });

    it('does not throw when the session is unavailable', () => {
      mockHasWriteAccess.mockImplementation(() => false);
      mockGetUserSession.mockReturnValue(undefined);
      expect(() => canAccessTour(accountWriteTour)).not.toThrow();
      expect(canAccessTour(accountWriteTour)).toBe(false);
    });

    it('short-circuits on the tenant-admin check without walking accounts', () => {
      asTenantAdmin();
      canAccessTour(accountWriteTour);
      expect(mockGetUserSession).not.toHaveBeenCalled();
    });
  });

  describe('requiresFeature', () => {
    const featureTour = tour({ requiresFeature: 'SOME_FLAG' });

    it('offers the guide when the flag is enabled', () => {
      asReadOnly();
      mockHasFeatureAccessCached.mockImplementation((flag: string) => flag === 'SOME_FLAG');
      expect(canAccessTour(featureTour)).toBe(true);
    });

    it('hides the guide when the flag is off (or the cache is not yet warm)', () => {
      asReadOnly();
      mockHasFeatureAccessCached.mockReturnValue(false);
      expect(canAccessTour(featureTour)).toBe(false);
    });
  });

  describe('requiresUiFeature (deployment-level UI_ENABLE_* toggle)', () => {
    const uiTour = tour({ requiresUiFeature: 'llmGateway' });

    it('offers the guide when the deployment toggle is on', () => {
      asReadOnly();
      mockIsUiFeatureEnabled.mockImplementation((f: string) => f === 'llmGateway');
      expect(canAccessTour(uiTour)).toBe(true);
    });

    it('hides the guide when the toggle is off — even for a tenant admin', () => {
      asTenantAdmin();
      mockIsUiFeatureEnabled.mockReturnValue(false);
      expect(canAccessTour(uiTour)).toBe(false);
    });

    it('resolves the specific toggle the guide names', () => {
      asReadOnly();
      mockIsUiFeatureEnabled.mockImplementation((f: string) => f === 'llmGateway');
      expect(canAccessTour(uiTour)).toBe(true);
      expect(mockIsUiFeatureEnabled).toHaveBeenCalledWith('llmGateway');
    });

    it('does not consult the tenant feature-flag cache — these are per-deployment, not per-tenant', () => {
      asReadOnly();
      mockIsUiFeatureEnabled.mockReturnValue(true);
      canAccessTour(uiTour);
      expect(mockHasFeatureAccessCached).not.toHaveBeenCalled();
    });
  });

  describe('combined requirements', () => {
    it('requires BOTH the capability and the flag', () => {
      const both = tour({ requires: 'account-write', requiresFeature: 'SOME_FLAG' });

      asAccountAdmin();
      mockHasFeatureAccessCached.mockReturnValue(false);
      expect(canAccessTour(both)).toBe(false);

      mockHasFeatureAccessCached.mockReturnValue(true);
      expect(canAccessTour(both)).toBe(true);

      asReadOnly();
      expect(canAccessTour(both)).toBe(false);
    });
  });
});

describe('TOURS gating contract', () => {
  const AUTOMATION_GUIDES = ['automation-from-code', 'automation-with-ai', 'automation-from-template', 'automation-from-scratch'];

  it.each(AUTOMATION_GUIDES)('gates %s on account-write, matching hasWriteAccess(accountId) on #workflow-listing-create-btn', (id) => {
    expect(TOURS[id].requires).toBe('account-write');
  });

  it('shows the automation guides to an account admin (regression: they were hidden by a tenant-scoped gate)', () => {
    asAccountAdmin();
    mockHasFeatureAccessCached.mockReturnValue(true);
    AUTOMATION_GUIDES.forEach((id) => {
      expect(canAccessTour(TOURS[id])).toBe(true);
    });
  });

  it('leaves knowledge-graph ungated — its flag is opt-out, so the tab always renders', () => {
    expect(TOURS['knowledge-graph'].requiresFeature).toBeUndefined();
    expect(TOURS['knowledge-graph'].requires).toBeUndefined();

    asReadOnly();
    expect(canAccessTour(TOURS['knowledge-graph'])).toBe(true);
  });

  it.each([
    'optimize-overview',
    'optimize-summary',
    'optimize-resolutions',
    'optimize-auto-optimize',
    'tickets-overview',
    'cloud-overview',
    'troubleshoot-overview',
    'app-overview',
    'investigations',
  ])('offers the read-only orientation guide %s to every user', (id) => {
    expect(TOURS[id].requires).toBeUndefined();
    asReadOnly();
    expect(canAccessTour(TOURS[id])).toBe(true);
  });

  it.each(['create-user', 'connect-cluster', 'connect-aws', 'connect-gcp', 'connect-azure'])(
    'keeps %s tenant-scoped, matching its bare hasWriteAccess() button gate',
    (id) => {
      expect(TOURS[id].requires).toBe('write');
    }
  );

  it('only uses requiresFeature for opt-in flags', () => {
    expect(TOURS['automation-with-ai'].requiresFeature).toBe('WORKFLOWS');
    expect(TOURS['automation-from-template'].requiresFeature).toBe('WORKFLOW_TEMPLATES');
    expect(TOURS['llm-analyser'].requiresFeature).toBe('LLM_ANALYSER');
  });

  it('gates ai-gateway on its deployment toggle, not a tenant feature flag', () => {
    // UI_ENABLE_LLM_GATEWAY is a per-deployment env var and never appears in
    // featureflags_list, so requiresFeature could never resolve it.
    expect(TOURS['ai-gateway'].requiresUiFeature).toBe('llmGateway');
    expect(TOURS['ai-gateway'].requiresFeature).toBeUndefined();
  });

  it('gates llm-analyser on the tenant LLM_ANALYSER feature flag, not a deployment toggle', () => {
    expect(TOURS['llm-analyser'].requiresFeature).toBe('LLM_ANALYSER');
    expect(TOURS['llm-analyser'].requiresUiFeature).toBeUndefined();
  });

  it('hides ai-gateway wherever its tab is not deployed', () => {
    asTenantAdmin();
    mockIsUiFeatureEnabled.mockReturnValue(false);
    expect(canAccessTour(TOURS['ai-gateway'])).toBe(false);
  });

  it('hides llm-analyser wherever its tenant flag is not enabled', () => {
    asTenantAdmin();
    mockHasFeatureAccessCached.mockReturnValue(false);
    expect(canAccessTour(TOURS['llm-analyser'])).toBe(false);
  });
});

describe('app-overview', () => {
  it('shows Back rather than a Restart button', () => {
    // The engine derives Back from step index; showRestart no longer exists.
    expect('showRestart' in TOURS['app-overview']).toBe(false);
  });

  it('introduces Nubi between Admin and the cluster filter', () => {
    const elements = TOURS['app-overview'].steps.map((s) => s.element);
    const admin = elements.indexOf('#admin-sidenav');
    const nubi = elements.indexOf('[data-testid="nav-bcortex-btn"]');
    const clusterFilter = elements.indexOf('#global-cluster-filter');

    expect(nubi).toBeGreaterThan(admin);
    expect(nubi).toBeLessThan(clusterFilter);
  });
});

describe('TOURS registry integrity', () => {
  const entries = Object.entries(TOURS);

  it.each(entries)('%s is keyed by its own id', (key, def) => {
    expect(def.id).toBe(key);
  });

  it.each(entries)('%s has at least one step, each with an anchor and copy', (_key, def) => {
    expect(def.steps.length).toBeGreaterThan(0);
    def.steps.forEach((step) => {
      expect(step.element).toBeTruthy();
      expect(step.title).toBeTruthy();
      expect(step.description).toBeTruthy();
    });
  });

  it.each(entries.filter(([, def]) => Boolean(def.module)))('catalog guide %s declares a route to navigate to', (_key, def) => {
    // GuidesMenu can launch a guide from anywhere; without a route,
    // useLaunchGuide starts it on whatever page the user happens to be on.
    expect(def.route).toBeTruthy();
  });

  it.each(entries)('%s uses a selector that querySelector accepts', (_key, def) => {
    def.steps.forEach((step) => {
      // Guards the space-containing ids (e.g. `anchor-tab-All Tickets`), which
      // must be written as [id="…"] rather than `#…`.
      expect(() => document.querySelector(step.element)).not.toThrow();
    });
  });
});
