import * as React from 'react';
import { useRouter } from 'next/router';
import { useSession } from 'next-auth/react';
import apiAccount from '@api1/account';
import apiKubernetes1 from '@api1/kubernetes1';
import { AGENT_CONNECTED } from '@components/integrations/pendingAgentInstall';
import { accountRowState, type OnboardingAccount } from '@lib/onboardingReadiness';

/**
 * Holds a tenant with no *actually connecting-or-connected* account on the
 * Setup Center, and lets everyone else straight through. Mounted once in
 * `_app.tsx`, above `PageLayout`, so the lookup runs once per session
 * rather than once per page.
 *
 * **The rule: locked while every real account row is still `partial`
 * (actively connecting), open the moment any row isn't.** `partial` is
 * `accountRowState`'s own answer — the exact same per-row check
 * `accountEvaluator` uses for the Setup Center card and `Go to dashboard`'s
 * own enabled state, so there is exactly one definition of "still
 * connecting" across the whole feature. A K8s row whose agent has not
 * checked in is `partial`; a `done` row obviously isn't; neither is a
 * `blank` one (never connected, or deliberately deactivated — an
 * established tenant who paused their only cluster is not back in
 * first-run) or an `error` one (a connection attempt that is over, not one
 * still in flight). Nothing is stored — no finished flag, no cutoff date.
 *
 * **Update — this used to be existence alone** ("a K8s row exists before
 * the install command runs, so gating on agent health would leave a failed
 * install trapping the user here indefinitely"). Reversed deliberately,
 * from watching it live: a newly-added, still-`partial` account unlocked
 * the entire nav, which reads as far more "done" than it is. The
 * `partial`-only lock (not "must be exactly `done`") is what keeps the
 * original concern from coming back — a K8s install that fails outright
 * lands on `error`, which still opens the gate; only a genuinely stuck,
 * still-in-progress row holds it. What this still doesn't close: an agent
 * that hangs at "connecting" forever, rather than ever reaching `error`,
 * still holds the gate indefinitely — a real, accepted trade-off, not an
 * oversight.
 *
 * Two properties still matter most when editing this file:
 *
 * - **Locking requires positive evidence.** Anything unknown — a failed
 *   lookup, a session that has not resolved — leaves the product open.
 *   Wrongly unlocking shows someone an empty dashboard they can navigate
 *   out of; wrongly locking takes a paying customer's product away. A K8s
 *   agent-health lookup that itself fails falls back to trusting `status`
 *   alone (`accountRowState`'s own contract), the same as everywhere else
 *   that lookup is used, rather than stranding every K8s row at `partial`.
 * - **A non-admin reaches a page here too now, but never a checklist.**
 *   `locked` used to also require `canManageSetup`, on the theory that
 *   holding someone on a page whose only exit is an action they lack
 *   permission for is a trap with no exit. Reversed after a live report: a
 *   non-admin on a genuinely unconfigured tenant landed on an ordinary page
 *   (the demo account's mock data, same as anyone else) with nothing telling
 *   them the tenant had never been set up, or who to ask. `SetupCenter`
 *   itself is what keeps the original trap concern from actually recurring —
 *   it branches on `canManageSetup` (still exposed below) and gives a
 *   non-admin one line saying to ask an admin, never the checklist an admin
 *   sees, so nothing here implies an action that isn't theirs to take.
 *   `hasConnectedAccount` is exposed for exactly that branch, so the page's
 *   content and the redirect that sent them there read the same signal.
 *   What this does not fix: once locked, a non-admin has no self-service way
 *   out, and nothing re-checks in the background — if an admin fixes the
 *   tenant a minute later, this non-admin's own tab won't notice until they
 *   reload. Accepted, not fixed here.
 *
 * **`UI_ENABLE_SETUP_CENTER` wraps all of it.** Off: `locked` is always
 * `false` — no redirect, no dimmed rail, no Home progress banner, no account
 * lookup even attempted (nothing to spend a request on). The feature is
 * inert, not merely hidden, so a deployment can run with it off exactly as
 * if it had never shipped. Read straight off the session
 * (`session.uiFeatures.setupCenter`), the same way `canManageSetup` reads
 * `session.roles` directly — not through `isUiFeatureEnabled()`, whose
 * `userData` dependency is populated by `withAuth`, which wraps neither
 * `/setup` nor `/home` (see `canManageSetup`'s own comment below for the
 * identical reasoning). The flag living on the session rather than behind
 * that helper is what makes it readable on the two pages this file actually
 * needs it to work on.
 */

/** The one route a gated tenant may open. */
export const SETUP_ROUTE = '/setup';

interface SetupGateValue {
  /** True until the account lookup (and, when there is a K8s row to ask
   * about, the agent-health lookup too) and the session have both
   * resolved. `locked` stays false meanwhile, so a returning user never
   * sees a flash of locked nav before the far more common unlocked case
   * confirms itself. */
  loading: boolean;
  locked: boolean;
  /** Whether this user may connect accounts and integrations. Read from the
   * session rather than `hasWriteAccess()`, which resolves against a module
   * global only `withAuth` populates — and `withAuth` wraps neither `/setup`
   * nor `/home`, so on the gated page it answers "not an admin" for everyone. */
  canManageSetup: boolean;
  /** `null` while unknown (never a confident zero). The same signal `locked`
   * is built from — exposed so `/setup` can tell a genuinely unconfigured
   * tenant apart from a non-admin just read-only browsing one that already
   * has an account, without computing that a second way. */
  hasConnectedAccount: boolean | null;
  /** The `UI_ENABLE_SETUP_CENTER` deployment flag — see the file doc comment.
   * Exposed so `/setup` and the Home progress banner can stay inert too, not
   * just the redirect. */
  setupCenterEnabled: boolean;
  /** Re-reads accounts (and agent health). Call it after connecting one, or
   * after an agent checks in, so the gate lifts without a page reload. */
  refresh: () => void;
}

const SetupGateContext = React.createContext<SetupGateValue>({
  loading: true,
  locked: false,
  canManageSetup: false,
  hasConnectedAccount: null,
  setupCenterEnabled: false,
  refresh: () => {},
});

type GateResult =
  | {
      data?: {
        all_accounts?: OnboardingAccount[];
      };
    }
  | Error
  | undefined;

type AgentHealthResult =
  | {
      data?: {
        data?: {
          agent?: Array<{ cloud_account_id?: string; status?: string }>;
        };
      };
    }
  | undefined;

/**
 * What counts as an account *row* worth asking about — deliberately more
 * permissive than the Setup Center's own `realAccounts` filter in
 * `onboardingReadiness.ts`, and it has to stay that way. If the card could
 * see an account the gate could not, it would offer a way out on a route
 * the guard bounces straight back — a trap. Keeping this a superset makes
 * that impossible by construction rather than by agreement. Whether a row
 * this admits is actually connecting, connected, or deactivated is
 * `accountRowState`'s question, not this one.
 *
 * - A *deactivated* account still counts as a row to ask about — see the
 *   file doc comment above for why that still opens the gate.
 * - The demo account cannot reach this list — it is a client-side mock
 *   appended by `apiHome.getCloudAccounts`, while these rows come from
 *   `cloud_accounts`, whose `id` is a uuid column. The check is cheap
 *   insurance, not the safeguard; drop it when the demo account goes.
 */
const countsAsAccountRow = (a?: OnboardingAccount | null): boolean => !!a && a.id !== 'demo' && a.status !== 'deleted';

export const SetupGateProvider = ({ children }: { children: React.ReactNode }): React.ReactElement => {
  const router = useRouter();
  const { data: session, status: sessionStatus } = useSession();
  const [accountsLoading, setAccountsLoading] = React.useState(true);
  /** `null` means "we do not know" — a failed lookup, never a confident zero. */
  const [hasConnectedAccount, setHasConnectedAccount] = React.useState<boolean | null>(null);
  const [reloadToken, setReloadToken] = React.useState(0);

  const tenantId = (session as { tenant?: { id?: string } } | null)?.tenant?.id;
  const canManageSetup = Boolean((session as { roles?: string[] } | null)?.roles?.includes('tenant_admin'));
  const setupCenterEnabled = Boolean((session as { uiFeatures?: { setupCenter?: boolean } } | null)?.uiFeatures?.setupCenter);

  // Keyed on the tenant, so switching tenants re-reads rather than carrying the
  // previous tenant's answer across — which would drop an admin into a
  // brand-new tenant's empty product with no checklist and no explanation.
  React.useEffect(() => {
    // Wait for the session before issuing an authenticated query: `tenantId`
    // arrives with it, so firing early costs a second round trip for an answer
    // that is about to be re-fetched anyway.
    if (sessionStatus === 'loading') return undefined;

    // Off: `locked` (below) ignores `hasConnectedAccount` entirely, so there is
    // nothing to spend a request finding out. Every page load for every user
    // would otherwise cost a real account (and possibly agent-health) lookup
    // for an answer nothing reads.
    if (!setupCenterEnabled) {
      setHasConnectedAccount(null);
      setAccountsLoading(false);
      return undefined;
    }

    let cancelled = false;

    // No session to scope the query to. Sign-in takes over from here, and
    // `locked` is already false, so nobody is held anywhere.
    if (sessionStatus === 'unauthenticated') {
      setHasConnectedAccount(null);
      setAccountsLoading(false);
      return undefined;
    }

    setAccountsLoading(true);

    const evaluate = async () => {
      const raw = (await apiAccount.getAllAccount().catch((err: unknown) => err)) as GateResult;
      if (cancelled) return;

      // `getAllAccount` returns its error rather than rejecting, so `.catch`
      // alone would never fire. An unanswered query is unknown, not empty.
      if (raw instanceof Error || !raw?.data) {
        setHasConnectedAccount(null);
        setAccountsLoading(false);
        return;
      }

      const rows = (raw.data.all_accounts || []).filter(countsAsAccountRow);

      // Only K8s rows have an agent-health concept — everyone else's status
      // is the whole answer, so this is skipped entirely (no added request)
      // for a tenant with no K8s accounts at all.
      const k8sIds = rows.filter((a) => (a.cloud_provider || '').toLowerCase() === 'k8s' && a.id).map((a) => a.id as string);
      const agentHealthResult =
        k8sIds.length > 0 ? ((await apiKubernetes1.listK8sAccAgentHealth(k8sIds).catch(() => undefined)) as AgentHealthResult) : undefined;
      if (cancelled) return;

      let k8sAgentConnected: Record<string, boolean> | undefined;
      const agentRows = agentHealthResult?.data?.data?.agent;
      if (Array.isArray(agentRows)) {
        k8sAgentConnected = {};
        agentRows.forEach((row) => {
          if (!row?.cloud_account_id) return;
          k8sAgentConnected![row.cloud_account_id] = k8sAgentConnected![row.cloud_account_id] || row.status === AGENT_CONNECTED;
        });
      }

      setHasConnectedAccount(rows.some((a) => accountRowState(a, k8sAgentConnected) !== 'partial'));
      setAccountsLoading(false);
    };

    evaluate().catch(() => {
      if (cancelled) return;
      setHasConnectedAccount(null);
      setAccountsLoading(false);
    });

    return () => {
      cancelled = true;
    };
  }, [sessionStatus, tenantId, reloadToken, setupCenterEnabled]);

  const refresh = React.useCallback(() => setReloadToken((n) => n + 1), []);

  const loading = accountsLoading || sessionStatus === 'loading';
  // `hasConnectedAccount === false` and not merely falsy: `null` is the
  // unknown case, and unknown must never lock. No `&& canManageSetup` here
  // any more — a non-admin is locked too now, see the file doc comment above
  // for why that stopped being the trap it used to be. `setupCenterEnabled`
  // short-circuits everything else when the deployment flag is off.
  const locked = setupCenterEnabled && !loading && hasConnectedAccount === false;

  // Dimming the nav rail stops the rail and nothing else: a typed URL, a
  // bookmark or browser Back all reach the page regardless. This redirect is the
  // layer that actually enforces the gate.
  //
  // `replace`, not `push`: pushing would leave the blocked route in history, so
  // Back would land on it, redirect again, and trap the user in a loop.
  //
  // Auth routes (signin, signup, verify, no-tenant-access…) render outside this
  // provider in `_app.tsx`, so they can never be caught by it.
  //
  // `router.pathname` is the only part of the router this depends on. Next
  // rebuilds the public router instance on every render — including a fresh
  // closure for `replace` — so listing either the object or the method would
  // re-run this after every paint. Both proxy the same singleton, so holding
  // the method in a ref is safe and keeps the dependency list honest.
  const replaceRef = React.useRef(router.replace);
  replaceRef.current = router.replace;
  const { pathname } = router;

  React.useEffect(() => {
    if (loading || !locked) return;
    if (pathname === SETUP_ROUTE) return;
    replaceRef.current(SETUP_ROUTE);
  }, [loading, locked, pathname]);

  const value = React.useMemo(
    () => ({ loading, locked, canManageSetup, hasConnectedAccount, setupCenterEnabled, refresh }),
    [loading, locked, canManageSetup, hasConnectedAccount, setupCenterEnabled, refresh]
  );

  return <SetupGateContext.Provider value={value}>{children}</SetupGateContext.Provider>;
};

export const useSetupGate = (): SetupGateValue => React.useContext(SetupGateContext);
