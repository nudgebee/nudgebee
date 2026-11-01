import { ONBOARDING_CLOUD_PROVIDERS, PANELS, evaluateOnboarding } from '@lib/onboardingReadiness';
import { SECTIONS_CONFIG, DISABLED_PROVIDERS } from '@components/accounts/integration';

const account = (cloud_provider: string, id = `${cloud_provider}-1`, status = 'active') => ({ id, cloud_provider, status, account_name: id });
const integration = (type: string, status = 'enabled') => ({ type, status, name: type });
const panelOf = (r: ReturnType<typeof evaluateOnboarding>, id: string) => r.panels.find((p) => p.id === id);
const itemOf = (r: ReturnType<typeof evaluateOnboarding>, panelId: string, itemId: string) => panelOf(r, panelId)?.items.find((i) => i.id === itemId);

describe('evaluateOnboarding', () => {
  it('reports a fresh tenant when nothing is connected', () => {
    const r = evaluateOnboarding({});
    expect(r.isFreshTenant).toBe(true);
    expect(r.hasCloudAccount).toBe(false);
    expect(r.done).toBe(0);
    expect(r.percent).toBe(0);
    expect(r.panels).toHaveLength(6);
    // Eight required items with every module selected: account, metrics, logs,
    // traces, alerts, repo, llm, skills.
    expect(r.total).toBe(8);
    expect(r.panels.every((p) => p.state === 'blank')).toBe(true);
  });

  // The old home page used "exactly one account" as its first-login signal
  // because the demo account is injected client-side. The model must not count it.
  it('does not count the injected demo account as a real connection', () => {
    const r = evaluateOnboarding({ accounts: [{ id: 'demo', cloud_provider: 'K8s', status: 'active' }] });
    expect(r.hasCloudAccount).toBe(false);
    expect(r.connectedProviders).toEqual([]);
  });

  it('counts a real cloud account and names the provider', () => {
    const r = evaluateOnboarding({ accounts: [account('AWS'), account('K8s')] });
    expect(r.hasCloudAccount).toBe(true);
    expect(r.connectedProviders).toEqual(expect.arrayContaining(['AWS', 'K8s']));
    expect(panelOf(r, 'accounts')?.state).toBe('done');
  });

  // K8s and Self-hosted's cloud_provider code ('K8s', 'SelfHosted') and display
  // label ('Kubernetes', 'Self-hosted') are different strings — unlike AWS/GCP/
  // Azure, where code and label are identical. The account item's own tile
  // picker compares connectedBy against each provider's *label*
  // (`connectedLabels.includes(p.label)` in SetupCenter's ProviderTile), so
  // connectedBy must carry labels, not raw codes, or the Kubernetes and
  // Self-hosted tiles never read as connected even when they are.
  it('names the account item’s connectedBy with display labels, not cloud_provider codes', () => {
    const r = evaluateOnboarding({ accounts: [account('K8s'), account('SelfHosted'), account('AWS')] });
    const connectedBy = itemOf(r, 'accounts', 'account')?.connectedBy;
    expect(connectedBy).toEqual(expect.arrayContaining(['Kubernetes', 'Self-hosted', 'AWS']));
    expect(connectedBy).not.toEqual(expect.arrayContaining(['K8s', 'SelfHosted']));
  });

  // The complaint that started this work: an account gets added and every other
  // capability stays dark, with nothing on screen saying so.
  it('leaves telemetry and AI outstanding when only an account exists', () => {
    const r = evaluateOnboarding({ accounts: [account('AWS')] });
    expect(panelOf(r, 'accounts')?.state).toBe('done');
    expect(panelOf(r, 'telemetry')?.state).toBe('blank');
    expect(panelOf(r, 'ai')?.state).toBe('blank');
    expect(r.essentialDone).toBe(1);
    // Four scorable essentials: accounts, telemetry, AI, and "Make your AI
    // agents smarter" — Skills is no longer planned, so that panel now has a
    // scorable item too (Preferences and Rules still are, and stay excluded).
    expect(r.essentialTotal).toBe(4);
  });
});

describe('panel state resolution', () => {
  it('treats an anyOf panel as done once any one required item is connected', () => {
    const r = evaluateOnboarding({ integrations: [integration('loki')] });
    // Telemetry is anyOf: logs alone satisfies it, metrics still blank.
    expect(panelOf(r, 'telemetry')?.state).toBe('done');
    expect(itemOf(r, 'telemetry', 'logs')?.state).toBe('done');
    expect(itemOf(r, 'telemetry', 'metrics')?.state).toBe('blank');
  });

  it('needs every required item before an all-of panel is done', () => {
    const chatOnly = evaluateOnboarding({ integrations: [integration('slack')] });
    expect(panelOf(chatOnly, 'delivery')?.state).toBe('partial');

    const both = evaluateOnboarding({ integrations: [integration('slack'), integration('jira')] });
    expect(panelOf(both, 'delivery')?.state).toBe('done');
  });

  // The bar could never fill for a team that collects traces but not logs if an
  // optional extra counted towards the denominator.
  it('never lets an optional item drag a panel backwards or into its count', () => {
    const r = evaluateOnboarding({ integrations: [integration('prometheus'), integration('loki'), integration('jaeger')] });
    const telemetry = panelOf(r, 'telemetry');
    expect(itemOf(r, 'telemetry', 'traces')?.optional).toBe(true);
    expect(itemOf(r, 'telemetry', 'traces')?.state).toBe('done');
    // metrics + logs are required; traces is not counted either way.
    expect(telemetry?.requiredTotal).toBe(2);
    expect(telemetry?.requiredDone).toBe(2);
    expect(telemetry?.state).toBe('done');
  });

  it('hides Preferences and Rules entirely, leaving Skills as the panel’s only visible item', () => {
    const r = evaluateOnboarding({});
    const smarter = panelOf(r, 'smarter');
    // Preferences and Rules are `hidden` — not rendered as Planned, not
    // present at all. Skills has shipped and is neither planned nor hidden.
    expect(itemOf(r, 'smarter', 'prefs')).toBeUndefined();
    expect(itemOf(r, 'smarter', 'rules')).toBeUndefined();
    expect(itemOf(r, 'smarter', 'skills')?.planned).toBeUndefined();
    expect(smarter?.items.map((i) => i.id)).toEqual(['skills']);
    // Skills alone is scorable: it is the only item in this panel with
    // `required: true` — Preferences and Rules were never `required` either
    // way, so this panel's denominator was never really "all three".
    expect(smarter?.requiredTotal).toBe(1);
    expect(smarter?.state).toBe('blank');
    // Counting items rather than panels retires the `scorable` special case
    // this used to need: a wholly planned panel would contribute nothing to
    // the denominator on its own, because planned items are never counted —
    // this panel just no longer is one.
    expect(smarter?.items.filter((i) => i.need === 'req' && !i.planned).map((i) => i.id)).toEqual(['skills']);
  });

  it('marks a panel blocked until its prerequisite is done', () => {
    const blocked = evaluateOnboarding({});
    expect(panelOf(blocked, 'smarter')?.blocked).toBe(true);

    const unblocked = evaluateOnboarding({ integrations: [integration('llm')] });
    expect(panelOf(unblocked, 'ai')?.state).toBe('done');
    expect(panelOf(unblocked, 'smarter')?.blocked).toBe(false);
  });

  // Skills is Knowledge Base under different copy (itemEvaluators.ts). It has
  // no integration row to match, so its only signal is `hasKnowledgeBase`.
  describe('skills reads the knowledge-base lookup, never the AI panel', () => {
    it('reads blank while the per-account lookup has not resolved', () => {
      const r = evaluateOnboarding({ integrations: [integration('llm')] });
      expect(itemOf(r, 'smarter', 'skills')?.state).toBe('blank');
      expect(itemOf(r, 'smarter', 'skills')?.connectedBy).toEqual([]);
    });

    it('reads done once a knowledge base exists, even with no LLM connected', () => {
      // Deliberately no `llm`/`gateway`/`mcp` integration: this item must never
      // infer itself from its own prerequisite panel being connected.
      const r = evaluateOnboarding({ hasKnowledgeBase: true });
      expect(itemOf(r, 'smarter', 'skills')?.state).toBe('done');
      expect(itemOf(r, 'smarter', 'skills')?.connectedBy).toEqual(['Knowledge base']);
      expect(itemOf(r, 'smarter', 'skills')?.autoDetected).toBe(false);
    });

    it('reads blank, not done, once the lookup resolves to zero knowledge bases', () => {
      const r = evaluateOnboarding({ integrations: [integration('llm')], hasKnowledgeBase: false });
      expect(panelOf(r, 'ai')?.state).toBe('done');
      expect(itemOf(r, 'smarter', 'skills')?.state).toBe('blank');
    });

    it('never lets a non-boolean input pass as a resolved answer', () => {
      const r = evaluateOnboarding({ hasKnowledgeBase: 'yes' as never });
      expect(itemOf(r, 'smarter', 'skills')?.state).toBe('blank');
    });
  });

  it('lets a gateway satisfy model access on its own', () => {
    const r = evaluateOnboarding({ integrations: [integration('llm_gateway')] });
    expect(panelOf(r, 'ai')?.state).toBe('done');
    expect(itemOf(r, 'ai', 'llm')?.state).toBe('blank');
  });

  // Worst state wins, so one failing connection is never hidden by a healthy sibling.
  it('surfaces a failing item over a healthy one', () => {
    const r = evaluateOnboarding({ integrations: [integration('slack'), integration('jira', 'error')] });
    expect(itemOf(r, 'delivery', 'ticket')?.state).toBe('error');
    expect(panelOf(r, 'delivery')?.state).toBe('error');
  });

  it('reads a pending account as in progress, not as connected', () => {
    const r = evaluateOnboarding({ accounts: [account('K8s', 'k8s-1', 'pending')] });
    expect(panelOf(r, 'accounts')?.state).toBe('partial');
    // `hasCloudAccount` is existence, same as always — the row is real, it's
    // just not done yet. Not the same question `SetupGateContext` asks of
    // its own, separate lookup, which now requires the row be actually
    // connecting-or-connected, not merely present.
    expect(r.hasCloudAccount).toBe(true);
  });

  it('reads a failed account as an error', () => {
    const r = evaluateOnboarding({ accounts: [account('AWS', 'aws-1', 'failed')] });
    expect(panelOf(r, 'accounts')?.state).toBe('error');
  });
});

describe('telemetry auto-detection', () => {
  // An agent-discovered Prometheus has no integration row, so a fully
  // instrumented Kubernetes tenant read as "not connected" before this.
  it('counts an agent-detected signal as done and flags it as auto-detected', () => {
    const r = evaluateOnboarding({ accounts: [account('K8s')], detectedSignals: { metrics: 'prometheus' } });
    const metrics = itemOf(r, 'telemetry', 'metrics');
    expect(metrics?.state).toBe('done');
    expect(metrics?.autoDetected).toBe(true);
    expect(metrics?.connectedBy).toEqual(['prometheus']);
  });

  // Auto-detected is not a fifth state. It is `done` carrying a chip.
  it('keeps auto-detected inside the four states', () => {
    const r = evaluateOnboarding({ detectedSignals: { logs: 'loki' } });
    expect(['done', 'partial', 'error', 'blank']).toContain(itemOf(r, 'telemetry', 'logs')?.state);
  });

  it('ignores a detection payload that names no provider', () => {
    const r = evaluateOnboarding({ detectedSignals: { metrics: undefined } });
    expect(itemOf(r, 'telemetry', 'metrics')?.state).toBe('blank');
  });

  // Known issue #20: a broken manually-added integration must not vanish
  // behind a healthy auto-detected signal just because they share an item.
  it('does not let an auto-detected signal mask a broken manual sibling', () => {
    const r = evaluateOnboarding({
      integrations: [integration('datadog', 'error')],
      detectedSignals: { metrics: 'prometheus' },
    });
    const metrics = itemOf(r, 'telemetry', 'metrics');
    expect(metrics?.state).toBe('error');
    expect(metrics?.autoDetected).toBe(false);
    expect(metrics?.connectedBy).toEqual(expect.arrayContaining(['Datadog', 'prometheus']));
  });

  it('keeps the auto-detected badge when a matched sibling is healthy too', () => {
    const r = evaluateOnboarding({
      integrations: [integration('datadog')],
      detectedSignals: { metrics: 'prometheus' },
    });
    const metrics = itemOf(r, 'telemetry', 'metrics');
    expect(metrics?.state).toBe('done');
    expect(metrics?.autoDetected).toBe(true);
    expect(metrics?.connectedBy).toEqual(['prometheus']);
  });
});

// The K8s add-account wizard writes the `cloud_accounts` row on step 1, well
// before the agent has checked in on step 2. `status` alone cannot see that
// gap, so the model has to be told separately.
// The item's own `state` answers "was an account added", not "is its agent
// healthy" — that second question is `PendingAgentInstallBanner`'s, and
// capping this item at `partial` for it made the setup checklist and that
// banner re-litigate the same fact two different ways. Agent health still
// gates the *per-account* row below, which is what the per-account chip and
// the provider tile's "Connected" badge actually read.
describe('accounts item state answers "was one actually connected", not "was one added"', () => {
  it('does not mark the item (or its panel) done while a K8s account exists but its agent has not reported connected', () => {
    const r = evaluateOnboarding({ accounts: [account('K8s', 'k8s-1')], k8sAgentConnected: { 'k8s-1': false } });
    expect(itemOf(r, 'accounts', 'account')?.state).toBe('partial');
    expect(panelOf(r, 'accounts')?.state).toBe('partial');
  });

  it('still holds the per-account row at partial until the agent reports connected', () => {
    const r = evaluateOnboarding({ accounts: [account('K8s', 'k8s-1')], k8sAgentConnected: { 'k8s-1': false } });
    expect(itemOf(r, 'accounts', 'account')?.accounts?.[0]?.state).toBe('partial');
  });

  it('marks the item and the per-account row done once the agent reports connected', () => {
    const r = evaluateOnboarding({ accounts: [account('K8s', 'k8s-1')], k8sAgentConnected: { 'k8s-1': true } });
    expect(itemOf(r, 'accounts', 'account')?.state).toBe('done');
    expect(itemOf(r, 'accounts', 'account')?.accounts?.[0]?.state).toBe('done');
  });

  it('falls back to status alone for both the item and the per-account row when the health lookup never resolved', () => {
    // No k8sAgentConnected at all — the same as before this lookup existed.
    const r = evaluateOnboarding({ accounts: [account('K8s', 'k8s-1')] });
    expect(itemOf(r, 'accounts', 'account')?.state).toBe('done');
    expect(itemOf(r, 'accounts', 'account')?.accounts?.[0]?.state).toBe('done');
  });

  it('holds both the item and the per-account row back on an id the health lookup never mentioned', () => {
    // The map resolved, but names a different account — a real gap, not "unknown".
    const r = evaluateOnboarding({ accounts: [account('K8s', 'k8s-1')], k8sAgentConnected: {} });
    expect(itemOf(r, 'accounts', 'account')?.state).toBe('partial');
    expect(itemOf(r, 'accounts', 'account')?.accounts?.[0]?.state).toBe('partial');
  });

  it('never applies agent health to a non-K8s account, item or row', () => {
    const r = evaluateOnboarding({ accounts: [account('AWS', 'aws-1')], k8sAgentConnected: { 'aws-1': false } });
    expect(itemOf(r, 'accounts', 'account')?.state).toBe('done');
    expect(itemOf(r, 'accounts', 'account')?.accounts?.[0]?.state).toBe('done');
  });

  it('marks the item done once at least one K8s account connects, even while a sibling account is still waiting', () => {
    const r = evaluateOnboarding({
      accounts: [account('K8s', 'k8s-1'), account('K8s', 'k8s-2')],
      k8sAgentConnected: { 'k8s-1': true, 'k8s-2': false },
    });
    expect(itemOf(r, 'accounts', 'account')?.state).toBe('done');
    const accounts = itemOf(r, 'accounts', 'account')?.accounts;
    expect(accounts?.find((a) => a.id === 'k8s-1')?.state).toBe('done');
    expect(accounts?.find((a) => a.id === 'k8s-2')?.state).toBe('partial');
  });

  // The bug as reported: several K8s accounts, none of their agents connected
  // yet, and the item read done anyway.
  it('does not mark the item done while every K8s account among several is still waiting on its agent', () => {
    const r = evaluateOnboarding({
      accounts: [account('K8s', 'k8s-1'), account('K8s', 'k8s-2'), account('K8s', 'k8s-3')],
      k8sAgentConnected: { 'k8s-1': false, 'k8s-2': false, 'k8s-3': false },
    });
    expect(itemOf(r, 'accounts', 'account')?.state).toBe('partial');
  });
});

// `accounts` on the account item's own status — the per-row rundown a
// per-account chip renders from. `connectedBy` (tested above) only ever names
// a provider once no matter how many accounts share it, so it can't tell two
// AWS accounts apart or say which one is still pending; this can.
describe('per-account rows for the accounts item', () => {
  it('names every real account, not just an aggregate provider label', () => {
    const r = evaluateOnboarding({ accounts: [account('AWS', 'aws-1'), account('K8s', 'k8s-1')] });
    const accounts = itemOf(r, 'accounts', 'account')?.accounts;
    expect(accounts).toEqual(
      expect.arrayContaining([
        { id: 'aws-1', label: 'aws-1', cloudProvider: 'AWS', state: 'done' },
        { id: 'k8s-1', label: 'k8s-1', cloudProvider: 'K8s', state: 'done' },
      ])
    );
  });

  it('caps only the pending K8s row at partial, leaving its connected sibling done', () => {
    const r = evaluateOnboarding({
      accounts: [account('K8s', 'k8s-1'), account('K8s', 'k8s-2')],
      k8sAgentConnected: { 'k8s-1': true, 'k8s-2': false },
    });
    const accounts = itemOf(r, 'accounts', 'account')?.accounts;
    expect(accounts?.find((a) => a.id === 'k8s-1')?.state).toBe('done');
    expect(accounts?.find((a) => a.id === 'k8s-2')?.state).toBe('partial');
    // k8s-1 alone is enough to mark the item done — see "accounts item state
    // answers..." above.
    expect(itemOf(r, 'accounts', 'account')?.state).toBe('done');
  });

  it('is left undefined when there are no real accounts, same as an unpopulated item', () => {
    const r = evaluateOnboarding({});
    expect(itemOf(r, 'accounts', 'account')?.accounts).toBeUndefined();
  });
});

describe('integration matching', () => {
  it('matches integration types case-insensitively', () => {
    // `ES` is upper-case in integrations_list while every sibling type is lower-case.
    const r = evaluateOnboarding({ integrations: [integration('ES')] });
    expect(itemOf(r, 'telemetry', 'logs')?.connectedBy).toEqual(['Elasticsearch']);
  });

  // Known issue #23: a healthy provider's chip must not read critical just
  // because a sibling on the same item is broken — each label gets its own
  // state, and the item's own aggregate (worst-wins, for the panel/progress
  // math) is untouched by this.
  it('gives each connected provider its own state, not the item aggregate', () => {
    const r = evaluateOnboarding({ integrations: [integration('loki'), integration('es', 'error')] });
    const logs = itemOf(r, 'telemetry', 'logs');
    expect(logs?.state).toBe('error');
    expect(logs?.connectedByState).toEqual({ Loki: 'done', Elasticsearch: 'error' });
  });

  it('ignores disabled integrations but keeps rows with no status', () => {
    const disabled = evaluateOnboarding({ integrations: [integration('prometheus', 'disabled')] });
    expect(itemOf(disabled, 'telemetry', 'metrics')?.state).toBe('blank');

    const noStatus = evaluateOnboarding({ integrations: [{ type: 'prometheus' }] });
    expect(itemOf(noStatus, 'telemetry', 'metrics')?.state).toBe('done');
  });

  it('accepts messaging from either the platform list or the integration list', () => {
    const viaPlatform = evaluateOnboarding({ messagingPlatforms: [{ platform: 'slack' }] });
    expect(itemOf(viaPlatform, 'delivery', 'chat')?.state).toBe('done');

    const viaIntegration = evaluateOnboarding({ integrations: [integration('ms_teams')] });
    expect(itemOf(viaIntegration, 'delivery', 'chat')?.connectedBy).toEqual(['Microsoft Teams']);
  });

  it('lets one integration satisfy two different panels', () => {
    const r = evaluateOnboarding({ integrations: [integration('github')] });
    expect(itemOf(r, 'delivery', 'ticket')?.connectedBy).toEqual(['GitHub Issues']);
    expect(itemOf(r, 'triggers', 'repo')?.connectedBy).toEqual(['GitHub']);
  });

  // Each item matches independently against the same tenant-wide `stateByType`
  // map — there is no per-item "claim" on an integration row. So a provider
  // registered under more than one item (Chronosphere: metrics, logs, and
  // traces all list it) reads as connected on every one of them from a
  // single integration, with no extra wiring needed per item.
  it('lets one integration satisfy every sibling item that lists the same provider', () => {
    const r = evaluateOnboarding({ integrations: [integration('chronosphere')] });
    expect(itemOf(r, 'telemetry', 'metrics')?.connectedBy).toEqual(['Chronosphere']);
    expect(itemOf(r, 'telemetry', 'logs')?.connectedBy).toEqual([]);
    expect(itemOf(r, 'telemetry', 'traces')?.connectedBy).toEqual(['Chronosphere']);
    expect(itemOf(r, 'telemetry', 'metrics')?.state).toBe('done');
    expect(itemOf(r, 'telemetry', 'traces')?.state).toBe('done');
  });

  it('computes percentage over required items, not panels', () => {
    const r = evaluateOnboarding({ accounts: [account('AWS')] });
    expect(r.total).toBe(8);
    expect(r.done).toBe(1);
    expect(r.percent).toBe(13);
  });

  it('survives malformed input without throwing', () => {
    expect(() => evaluateOnboarding({ accounts: null, integrations: undefined, messagingPlatforms: null })).not.toThrow();
    const r = evaluateOnboarding({
      accounts: [null, undefined, {}] as never,
      integrations: [null, { type: null }] as never,
      detectedSignals: null,
    });
    expect(r.done).toBe(0);
  });

  it('skips accounts that are not cloud providers', () => {
    const r = evaluateOnboarding({ accounts: [account('NotAProvider')] });
    expect(r.hasCloudAccount).toBe(false);
  });

  it('skips inactive accounts', () => {
    const r = evaluateOnboarding({ accounts: [account('AWS', 'aws-1', 'inactive')] });
    expect(r.hasCloudAccount).toBe(false);
  });
});

describe('finishing', () => {
  // Only an account gates anything. Everything else can be added later from the
  // integrations page, so holding the product for it would misrepresent what it
  // needs — and the gate reads exactly this predicate.
  it('is past the gate with one account and nothing else', () => {
    const r = evaluateOnboarding({ accounts: [account('K8s')] });
    expect(r.hasCloudAccount).toBe(true);
    expect(r.done).toBe(1);
    expect(r.total).toBe(8);
  });

  // Integrations without an account are not a set-up tenant: every one of them
  // describes a product that still has no infrastructure to point at.
  it('is not past the gate on integrations alone', () => {
    const r = evaluateOnboarding({ integrations: [integration('llm'), integration('slack'), integration('jira')] });
    expect(r.hasCloudAccount).toBe(false);
  });
});

// Drift guards. The home page previously hard-coded Kubernetes as the only
// connectable provider and silently fell years behind the catalog; these turn
// that class of drift into a failing test rather than a support ticket.
describe('catalog drift', () => {
  const catalogProviders = new Set(SECTIONS_CONFIG.flatMap((s: { providers: string[] }) => s.providers));
  const allRoutes = PANELS.flatMap((p) => p.items.flatMap((i) => i.providers.map((o) => o.route)));

  /**
   * Cloud Foundry is in the catalog and deliberately absent from onboarding —
   * a settled design decision, not drift. Listing it here rather than relaxing
   * the assertion keeps the guard's teeth: a *new* catalog provider still fails
   * this test, so somebody decides whether onboarding should offer it.
   */
  const DELIBERATELY_OMITTED = new Set(['CLOUDFOUNDRY']);

  /**
   * Routes that deliberately name something other than an "Add integration"
   * form. Skills has no catalog entry to drift from — its route is a deep
   * link to the Knowledge Base tab already shipped behind AI & Tools (see
   * `openIntegration`'s special case in `SetupCenter.tsx`), not a provider
   * `IntegrationDynamicFormModal` could open. Listed here, rather than
   * relaxing the assertion, so a *new* non-catalog route still fails this
   * test until someone deliberately adds it.
   */
  const NON_CATALOG_ROUTES = new Set(['KNOWLEDGE_BASE']);

  it('offers every cloud provider the catalog offers, minus the ones we chose to omit', () => {
    const cloudSection = SECTIONS_CONFIG.find((s: { id: string }) => s.id === 'cloud');
    expect(cloudSection).toBeDefined();
    const expected = (cloudSection?.providers || []).filter((p: string) => !DELIBERATELY_OMITTED.has(p));
    expect(new Set(ONBOARDING_CLOUD_PROVIDERS.map((p) => p.route))).toEqual(new Set(expected));
  });

  it('only routes to providers that exist in the catalog', () => {
    expect(allRoutes.filter((r) => !catalogProviders.has(r) && !NON_CATALOG_ROUTES.has(r))).toEqual([]);
  });

  it('keeps OpenObserve available in the observability integrations section', () => {
    const observabilitySection = SECTIONS_CONFIG.find((section: { id: string }) => section.id === 'observability');
    expect(observabilitySection?.providers).toContain('OPENOBSERVE');
    expect(DISABLED_PROVIDERS.has('OPENOBSERVE')).toBe(false);
  });

  it('never routes to a provider the catalog has disabled', () => {
    expect(allRoutes.filter((r) => DISABLED_PROVIDERS.has(r))).toEqual([]);
  });
});

describe('panel copy', () => {
  // app/CLAUDE.md: user-visible copy never spells the product or assistant name —
  // this table evaluates at import, before /api/public/app_config resolves, so a
  // literal here would latch the house brand on every white-label tenant. Nothing
  // in lint2 or type-check can see that, which is why it is a test.
  it('carries brand placeholders instead of product names', () => {
    const copy = PANELS.flatMap((p) => [
      p.title,
      p.sub,
      ...p.items.flatMap((i) => [i.name, i.purpose, i.why, ...(i.unlocks || []), ...i.meta.flat(), ...i.impact.flat()]),
    ]).join(' | ');
    expect(copy).not.toMatch(/Nudgebee|Nubi/);
  });

  it('gives every item the copy the UI renders', () => {
    PANELS.forEach((panel) => {
      expect(panel.title.length).toBeGreaterThan(0);
      expect(panel.sub.length).toBeGreaterThan(0);
      expect(panel.items.length).toBeGreaterThan(0);
      panel.items.forEach((item) => {
        expect(item.purpose.length).toBeGreaterThan(0);
        expect(item.why.length).toBeGreaterThan(0);
        expect(item.meta.length).toBeGreaterThan(0);
        expect(item.impact.length).toBeGreaterThan(0);
        // A planned item has nothing to connect to yet; everything else must
        // offer at least one provider, or its Add button leads nowhere.
        if (!item.planned) expect(item.providers.length).toBeGreaterThan(0);
      });
    });
  });

  it('labels exactly one panel as essential on screen', () => {
    expect(PANELS.filter((p) => p.tagTier).map((p) => p.id)).toEqual(['accounts']);
  });
});

describe('a tenant who has done everything they can', () => {
  // Regression: "Make your AI agents smarter" is entirely planned, so counting
  // it capped progress at 83% and kept the setup card on the home page forever
  // for a tenant with nothing left to connect.
  it('reaches 100% once every required item is connected', () => {
    const r = evaluateOnboarding({
      accounts: [account('K8s')],
      integrations: [
        integration('prometheus'),
        integration('loki'),
        // traces is `req` for Troubleshoot and Automation under the matrix, even
        // though it is structurally optional in its own panel.
        integration('jaeger'),
        integration('llm'),
        integration('slack'),
        integration('jira'),
        integration('prometheus_alertmanager_webhook'),
        integration('github'),
      ],
      // Skills has no integration row — it is satisfied by a knowledge base
      // existing, not by anything in `integrations`.
      hasKnowledgeBase: true,
    });
    expect(r.done).toBe(r.total);
    expect(r.percent).toBe(100);
    expect(r.essentialDone).toBe(r.essentialTotal);
    expect(r.panels).toHaveLength(6);
    // Preferences and Rules are still planned — still rendered, still
    // blocked-free, just contributing nothing to count. Skills has shipped:
    // it is the one item in this panel that counts, and with a knowledge base
    // connected above, it is what makes the panel reach 100% rather than
    // capping just under it.
    expect(panelOf(r, 'smarter')?.items.filter((i) => i.id !== 'skills' && i.need === 'req' && !i.planned)).toEqual([]);
    expect(itemOf(r, 'smarter', 'skills')?.state).toBe('done');
  });
});

describe('module selection', () => {
  const idsOf = (r: ReturnType<typeof evaluateOnboarding>, level: string) =>
    r.panels
      .flatMap((p) => p.items)
      .filter((i) => i.need === level && !i.planned)
      .map((i) => i.id);

  it('narrows what counts to the modules the customer picked', () => {
    // Optimize reads metrics and nothing else in telemetry, and never alerts.
    const r = evaluateOnboarding({ selectedModules: ['opt'] });
    expect(idsOf(r, 'req')).toEqual(['account', 'metrics']);
    expect(r.total).toBe(2);
    expect(idsOf(r, 'na')).toEqual(expect.arrayContaining(['logs', 'traces', 'alerts', 'mcp']));
  });

  // Strongest, not weakest: picking two modules must surface everything either
  // one needs, or the card would hide something the customer actually uses.
  it('takes the strongest level across the selected modules', () => {
    const optOnly = evaluateOnboarding({ selectedModules: ['opt'] });
    const both = evaluateOnboarding({ selectedModules: ['opt', 'ts'] });

    // logs is `na` for Optimize and `req` for Troubleshoot.
    expect(idsOf(optOnly, 'na')).toContain('logs');
    expect(idsOf(both, 'req')).toContain('logs');
  });

  it('treats no selection as every module selected', () => {
    const none = evaluateOnboarding({});
    const all = evaluateOnboarding({ selectedModules: ['ts', 'opt', 'k8s', 'auto'] });
    expect(none.total).toBe(all.total);
    expect(idsOf(none, 'req')).toEqual(idsOf(all, 'req'));
  });

  it('ignores a module id it does not recognise', () => {
    const r = evaluateOnboarding({ selectedModules: ['nonsense'] });
    // No row matches, so nothing is required — but the card still renders
    // every visible item rather than throwing or emptying out. 14 registry
    // items minus Preferences and Rules, which are `hidden`.
    expect(r.panels.flatMap((p) => p.items)).toHaveLength(12);
  });

  it('resolves a need level onto every item', () => {
    const r = evaluateOnboarding({ selectedModules: ['k8s'] });
    const levels = r.panels.flatMap((p) => p.items).map((i) => i.need);
    expect(levels).toHaveLength(12);
    expect(levels.every((l) => ['req', 'opt', 'na'].includes(l))).toBe(true);
  });

  // `required` and `need` answer different questions and legitimately disagree:
  // required is what this panel needs to read done, need is what the customer's
  // modules actually use.
  it('keeps structural requirement separate from module need', () => {
    const r = evaluateOnboarding({ selectedModules: ['ts'] });
    const items = r.panels.flatMap((p) => p.items);
    const chat = items.find((i) => i.id === 'chat');
    const traces = items.find((i) => i.id === 'traces');

    expect(chat?.required).toBe(true); // delivery is done only when both connect
    expect(chat?.need).toBe('opt'); // but no module actually requires it
    expect(traces?.optional).toBe(true); // structurally optional in telemetry
    expect(traces?.need).toBe('req'); // yet Troubleshoot needs it
  });
});
