import { createContext, useCallback, useContext, useEffect, useLayoutEffect, useMemo, useState } from 'react';
import { useSession } from 'next-auth/react';
import { Box } from '@mui/material';
import { Banner, type BannerAction } from '@ui/Banner';
import { toast } from '@ui/Toast';
import apiAccount from '@api1/account';
import apiKubernetes1 from '@api1/kubernetes1';
import { hasWriteAccess } from '@lib/auth';
import { buildK8sInstallCommand } from './k8sInstallCommand';
import {
  AGENT_CONNECTED,
  clearPendingAgentInstalls,
  isPendingAgentInstallSnoozed,
  PENDING_AGENT_INSTALL_EVENT,
  pendingAgentInstallKey,
  readPendingAgentInstalls,
  snoozePendingAgentInstalls,
  type PendingAgentInstall,
} from './pendingAgentInstall';

/**
 * Reminder for Kubernetes accounts created in the UI whose install command was
 * never run — the account exists, the agent table has no row for it, so nothing
 * is collecting data. Mounted once in the page layout: the "Add Kubernetes
 * Account" modal is reachable from the header on every page, so the reminder has
 * to follow the user rather than live on one screen.
 *
 * Dismissing snoozes rather than forgets: the entry stays in the store and comes
 * back after SNOOZE_MS, so the only signal that the cluster is half-connected is
 * delayed, never lost. It clears itself for good the moment the agent reports in.
 */
const POLL_MS = 30000;

/**
 * Where the reminder renders. The layout mounts it above the page as a fallback,
 * but a page with a tab strip (AnchorComponent) claims it so it sits below the
 * tabs instead of pushing them down. `claim` returns the release callback.
 */
export const PendingAgentInstallBannerHostContext = createContext<{ claim: () => () => void } | null>(null);

/** Called by a page-level tab strip: take over rendering the reminder while mounted. */
export const useClaimPendingAgentInstallBanner = () => {
  const host = useContext(PendingAgentInstallBannerHostContext);
  // Layout effect so the layout's fallback copy never paints for a frame first.
  useLayoutEffect(() => host?.claim(), [host]);
};

const PendingAgentInstallBanner = () => {
  const [pending, setPending] = useState<PendingAgentInstall[]>([]);
  // The account whose token is being renewed; one at a time, so a double click
  // can't mint (and invalidate) two tokens.
  const [renewingId, setRenewingId] = useState<string | null>(null);
  // The store is keyed by tenant, and the tenant is only known once the session
  // resolves — a read before that hits a different key and finds nothing. Re-read
  // when it lands, and again on a tenant switch.
  const { data: session } = useSession();
  const tenant = (session as { tenant?: { id?: string; name?: string } } | null)?.tenant;
  const tenantKey = tenant?.id || tenant?.name || '';

  const sync = useCallback(() => setPending(readPendingAgentInstalls()), []);

  useEffect(() => {
    sync();
    window.addEventListener(PENDING_AGENT_INSTALL_EVENT, sync);
    // Another tab finishing the install should clear this one too. Only our own
    // key is worth a re-read; `key` is null when a tab calls localStorage.clear(),
    // which does concern us.
    const handleStorage = (event: StorageEvent) => {
      if (event.key === null || event.key === pendingAgentInstallKey()) {
        sync();
      }
    };
    window.addEventListener('storage', handleStorage);
    return () => {
      window.removeEventListener(PENDING_AGENT_INSTALL_EVENT, sync);
      window.removeEventListener('storage', handleStorage);
    };
  }, [sync, tenantKey]);

  // Joined ids rather than the array itself: `sync` hands back a fresh array on
  // every event, and that identity change alone must not restart the poll.
  const pendingIds = useMemo(() => pending.map((entry) => entry.id), [pending]);
  const pendingIdsKey = pendingIds.join(',');

  useEffect(() => {
    if (!pendingIdsKey) {
      return undefined;
    }
    let cancelled = false;
    const check = () => {
      if (document.visibilityState !== 'visible') {
        return;
      }
      // No agent-type filter: this list mixes clusters (type k8s) with self-hosted
      // VM accounts (type proxy), and each account only carries agents of its own
      // kind, so filtering by id alone is exact.
      apiKubernetes1.listK8sAccAgentHealth(pendingIdsKey.split(','), null).then((res: any) => {
        if (cancelled) {
          return;
        }
        // CONNECTED, not the row's existence: an agent row is written when the
        // account is created, so every pending account has one from the start.
        const installed = (res?.data?.data?.agent || [])
          .filter((row: { status?: string }) => row?.status === AGENT_CONNECTED)
          .map((row: { cloud_account_id: string }) => row.cloud_account_id);
        // Writing fires PENDING_AGENT_INSTALL_EVENT, which re-syncs local state.
        clearPendingAgentInstalls(installed);
      });
    };
    check();
    const timer = setInterval(check, POLL_MS);
    // A hidden tab skips its ticks, so re-check on return rather than leaving a
    // stale banner up for the rest of the interval.
    const handleVisibility = () => {
      if (document.visibilityState === 'visible') {
        check();
      }
    };
    document.addEventListener('visibilitychange', handleVisibility);
    return () => {
      cancelled = true;
      clearInterval(timer);
      document.removeEventListener('visibilitychange', handleVisibility);
    };
  }, [pendingIdsKey]);

  // Snoozed entries are still polled above (so a connecting agent clears them),
  // but not shown. Re-read the store when the earliest snooze lapses so the
  // reminder returns without a reload; the next tick re-arms if any remain.
  const nextWakeAt = useMemo(() => {
    const now = Date.now();
    const pendingWakes = pending.filter((entry) => isPendingAgentInstallSnoozed(entry, now)).map((entry) => entry.snoozedUntil as number);
    return pendingWakes.length > 0 ? Math.min(...pendingWakes) : 0;
  }, [pending]);

  useEffect(() => {
    if (!nextWakeAt) {
      return undefined;
    }
    const timer = setTimeout(sync, Math.max(0, nextWakeAt - Date.now()));
    return () => clearTimeout(timer);
  }, [nextWakeAt, sync]);

  const visible = pending.filter((entry) => !isPendingAgentInstallSnoozed(entry));

  if (visible.length === 0) {
    return null;
  }

  // Snooze everything currently shown, not everything tracked: an account added
  // after the dismissal has no snooze and shows up right away.
  const handleDismiss = () => snoozePendingAgentInstalls(visible.map((entry) => entry.id));

  // One banner, not one per kind — the DS allows a single banner per surface, and
  // the next step differs by kind, so each kind gets its own line inside it.
  const clusters = visible.filter((entry) => entry.kind !== 'vm');
  const fleets = visible.filter((entry) => entry.kind === 'vm');

  // Same flow as "Re-new Token" in the account row menu: mint a fresh token, then
  // hand over the install command with it. Minting revokes the old token, so first
  // re-check that the agent hasn't connected since the last poll (up to POLL_MS
  // ago) — otherwise this would cut off a live agent.
  const handleRenew = async (entry: PendingAgentInstall) => {
    if (renewingId) {
      return;
    }
    setRenewingId(entry.id);
    try {
      const health: any = await apiKubernetes1.listK8sAccAgentHealth([entry.id], null);
      if ((health?.data?.data?.agent || []).some((row: { status?: string }) => row?.status === AGENT_CONNECTED)) {
        clearPendingAgentInstalls([entry.id]);
        toast.info('The agent has just connected — no need to renew.');
        return;
      }
      const res: any = await apiAccount.generateAgentToken(entry.id);
      const token = res?.data?.data?.agents_create_token;
      if (!token?.access_key || !token?.access_secret) {
        toast.error('Could not renew the token. Try "Re-new Token" from the account menu in Integrations.');
        return;
      }
      const command = buildK8sInstallCommand(token.access_key, token.access_secret);
      try {
        await navigator.clipboard.writeText(command);
      } catch {
        // The token is already minted (and the old one revoked), so a refused
        // clipboard write — Safari does this after a network wait — must not
        // lose the command. Show it instead.
        toast.error('Could not copy automatically. Copy the command below.', { description: command, duration: 30000 });
        return;
      }
      toast.success('Install command copied', { description: 'Run it on your cluster to finish connecting the agent.' });
    } catch {
      toast.error('Could not renew the token. Try "Re-new Token" from the account menu in Integrations.');
    } finally {
      setRenewingId(null);
    }
  };

  // One button per cluster, and only for a couple of them: a Banner holds two
  // actions at most, and past that the account list is the better place to pick.
  // VM accounts get no button — their command comes from the VM Agent integration.
  const renewActions: BannerAction[] | undefined =
    clusters.length > 0 && clusters.length <= 2
      ? clusters
          .filter((entry) => hasWriteAccess(entry.id))
          .map((entry) => ({
            id: `pending-agent-install-renew-${entry.id}`,
            tone: 'secondary' as const,
            label:
              renewingId === entry.id
                ? 'Renewing…'
                : clusters.length > 1 && entry.name
                ? `Renew and copy command for "${entry.name}"`
                : 'Renew and copy command',
            onClick: () => handleRenew(entry),
          }))
      : undefined;

  const nameList = (entries: PendingAgentInstall[], fallbackNoun: string) => {
    const names = entries.map((entry) => entry.name).filter(Boolean);
    return (
      <Box component='strong' sx={{ fontWeight: 'var(--ds-font-weight-medium)', color: 'var(--ds-gray-700)' }}>
        {names.length > 0 ? names.map((name) => `"${name}"`).join(', ') : `${entries.length} ${fallbackNoun}`}
      </Box>
    );
  };

  return (
    <Box data-testid='pending-agent-install-banner' sx={{ mt: 'var(--ds-space-3)' }}>
      <Banner
        tone='warning'
        surface='page'
        dismissible
        onDismiss={handleDismiss}
        actions={renewActions}
        actionsPlacement='inline'
        title='Finish installing the agent'
        message={
          <>
            {clusters.length > 0 && (
              <Box component='div' data-testid='pending-agent-install-k8s'>
                No agent has connected for {nameList(clusters, 'Kubernetes account(s)')} yet, so nothing is being collected from{' '}
                {clusters.length > 1 ? 'those clusters' : 'that cluster'}. Run the install command on your cluster.
              </Box>
            )}
            {fleets.length > 0 && (
              <Box component='div' data-testid='pending-agent-install-vm' sx={{ mt: clusters.length > 0 ? 'var(--ds-space-1)' : 0 }}>
                No agent has connected for {nameList(fleets, 'self-hosted account(s)')} yet, so no machines are being discovered. Add a VM Agent
                integration for each network segment — that is where the install command comes from.
              </Box>
            )}
            <Box component='div' sx={{ mt: 'var(--ds-space-1)' }}>
              This message clears itself as soon as the agent connects. Dismiss it and we&apos;ll remind you again in 24 hours.
            </Box>
          </>
        }
      />
    </Box>
  );
};

export default PendingAgentInstallBanner;
