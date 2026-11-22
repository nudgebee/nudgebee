import React, { useEffect, useMemo, useState } from 'react';
import { useRouter } from 'next/router';
import { ds } from '@utils/colors';
import { Box, Typography } from '@mui/material';
import { Skeleton } from '@ui/Skeleton';
import { Button as DsButton } from '@ui/Button';
import { Accordion } from '@ui/Accordion';
import { Label } from '@ui/Label';
import { StatusIndicator } from '@ui/StatusIndicator';
import SearchInput from '@ui/SearchInput';
import ErrorBoundary from '@shared/ErrorBoundary';
import apiKubernetes from '@api1/kubernetes';
import apiHome from '@api1/home';
import { isCloudProvider, isSelfHostedProvider } from '@components/overview/providers';
import { checkConnections } from '@shared/widgets/ClusterStatusIndicator';
import { logsServedBy, CLICKHOUSE_LOGS_CHECK } from '@utils/logsServedBy';

// The "Connections" sub-view of the admin landing tab. Every connected account,
// grouped by its cloud provider into one accordion each — AWS / Azure / GCP /
// Kubernetes — the same grouping the header's account picker uses, so the two
// read alike.
//
// What a row shows depends on the account kind:
//   - Kubernetes clusters carry real per-signal logs/metrics/traces coverage
//     (connection_status.{logsConnection, prometheusConnection, tracesEnabled}
//     plus the server-stamped providerStatus), so their rows show L/M/T.
//   - Cloud accounts (AWS/GCP/Azure) have no such telemetry surface — the
//     provider-status check only ever stamps K8s accounts, and the agent-details
//     page itself omits its Observability tab for cloud accounts for the same
//     reason. So a cloud-account row shows its name and connection status only.
//
// Two sources, fetched in parallel and fail-soft (one failing still renders the
// other): listk8ClusterData() for K8s clusters and getCloudAccounts() for cloud
// accounts (filtered to real infra providers via isCloudProvider).

// Cluster status maps to the same DS Label tones the Agent Health → Observability
// page uses for its Connected/Disconnected chips, so the two views read alike.
const STATUS_TONE = { healthy: 'success', degraded: 'warning', disconnected: 'critical' };
const STATUS_LABEL = { healthy: 'Healthy', degraded: 'Degraded', disconnected: 'Disconnected' };

const SIGNALS = [
  { key: 'logs', label: 'Logs', flag: 'logsConnection' },
  { key: 'metrics', label: 'Metrics', flag: 'prometheusConnection' },
  { key: 'traces', label: 'Traces', flag: 'tracesEnabled' },
];

// Provider name the agent reports for a signal — mirrors agentReportedProvider
// in agentHealth.jsx (and the api-server's provider_status.go). Two gotchas kept
// faithful: (1) logs/metrics report their provider whenever a source is set
// (logsConnectionProvider / prometheusUrl), even while currently down — that is
// what makes "configured but disconnected" a real state; (2) traces reports a
// provider ONLY when the agent is live AND traces is enabled (mirrors
// isTracesManagerConnected = isAgentActive && tracesEnabled), so a stale
// traceProvider on a dead agent doesn't read as configured.
const agentReportedProvider = (cs, agentActive, key) => {
  const isChronosphere = (cs.prometheusUrl || '').includes('chronosphere');
  if (key === 'logs') return cs.logsConnectionProvider || '';
  if (key === 'metrics') return isChronosphere ? 'chronosphere' : cs.prometheusUrl ? 'prometheus' : '';
  if (key === 'traces') return agentActive && cs.tracesEnabled ? (isChronosphere ? 'chronosphere' : cs.traceProvider || '') : '';
  return '';
};

// Resolve a signal exactly as the Agent Health → Observability cards do, so the
// two views never disagree: an integration that serves the signal is probed
// directly (status independent of the agent); an agent-served signal's connected
// flag is gated on the agent being live (isAgentActive && flag, exactly as
// agentHealth.jsx builds isLogs/Prometheus/TracesConnected). `configured` (a
// provider exists at all) separates "set up but currently down" (provider +
// Disconnected) from "never set up" (offer + Add) — only the latter gets + Add.
//
// Logs go through logsServedBy, which also knows logs served by the agent's ClickHouse.
const resolveSignal = (cs, agentActive, signal) => {
  const ps = cs?.providerStatus?.[signal.key];
  const provider = ps?.provider || agentReportedProvider(cs, agentActive, signal.key);
  if (signal.key === 'logs') {
    const logs = logsServedBy(cs);
    const connected = logs.by === 'integration' ? logs.connected : agentActive && logs.connected;
    // The ClickHouse logs check says the logs table is present, not that logs arrive.
    return { configured: !!provider, connected, provider, ...(logs.by === 'clickhouse' && { check: CLICKHOUSE_LOGS_CHECK }) };
  }
  const viaIntegration = ps?.integration_id ? ps : null;
  const connected = viaIntegration ? !!viaIntegration.connected : agentActive && !!cs?.[signal.flag];
  return { configured: !!provider, connected, provider };
};

const toClusterRow = (cluster) => {
  const agent = cluster?.agent || {};
  const cs = agent.connection_status || {};
  const agentActive = agent.status === 'CONNECTED';
  const signals = SIGNALS.map((s) => ({ ...s, ...resolveSignal(cs, agentActive, s) }));
  // Force the k8s branch of checkConnections — these rows are all K8s clusters,
  // but listk8ClusterData doesn't select a cloud_provider field, and without it
  // checkConnections would fall through to its cloud branch and read amber forever.
  const status = !agentActive ? 'disconnected' : checkConnections({ agent, cloud_provider: 'k8s' }) ? 'healthy' : 'degraded';
  return {
    id: cluster?.account_id,
    name: cluster?.account_name || 'Unnamed cluster',
    kind: 'k8s',
    // Every K8s cluster groups under one "Kubernetes" accordion; its distro
    // (EKS / GKE / AKS) becomes a per-row badge, not a top-level group.
    groupKey: 'Kubernetes',
    badge: agent.k8s_provider || '',
    agentActive,
    status,
    signals,
  };
};

// Non-K8s accounts — cloud providers (AWS / Azure / GCP) and self-hosted VM
// fleets — have no L/M/T surface, so a row is just its connection health: the
// same healthy/degraded/disconnected the K8s rows use, resolved by
// checkConnections' provider branch (fresh cloud sync services, or healthy VM
// datasources = healthy). groupKey is the raw cloud_provider so each provider
// lands in its own accordion.
const toCloudRow = (account) => {
  const agent = account?.agents?.[0] || {};
  const agentActive = agent.status === 'CONNECTED';
  const status = !agentActive ? 'disconnected' : checkConnections({ agent, cloud_provider: account?.cloud_provider }) ? 'healthy' : 'degraded';
  return {
    id: account?.id,
    name: account?.account_name || 'Unnamed account',
    kind: 'cloud',
    groupKey: account?.cloud_provider || 'Cloud',
    status,
  };
};

const StatusPill = ({ status }) => (
  <Box sx={{ flexShrink: 0 }}>
    <Label dot size='sm' tone={STATUS_TONE[status] || 'critical'} text={STATUS_LABEL[status] || 'Disconnected'} />
  </Box>
);

// A signal is in one of four states:
//   configured + connected      → provider name + green dot
//   configured + disconnected   → provider name + red dot + "Disconnected"
//                                 (set up but the backend/agent isn't serving it
//                                 right now — NOT an invitation to add it again)
//   not configured, agent up    → an actionable "+ Add <signal>" into Integrations
//   not configured, agent down  → plain "Not connected" (reconnecting the agent
//                                 is the fix, not adding a provider)
const SignalCell = ({ signal, agentActive, onAdd }) => {
  const renderStatus = () => {
    if (signal.configured) {
      // Signal-level state via StatusIndicator, deliberately a different DS
      // primitive from the cluster's agent-status Label pill above: the same
      // Label chip for both would conflate "the agent is disconnected" with
      // "this signal is disconnected". The dot color carries the state
      // (connected → healthy, disconnected → failed), the provider stays the
      // label, and "Disconnected" is the subtext only when the signal is down.
      return (
        <StatusIndicator
          size='sm'
          tone={signal.connected ? 'healthy' : 'failed'}
          label={signal.provider}
          subtext={signal.connected ? signal.check : 'Disconnected'}
        />
      );
    }
    if (agentActive) {
      return (
        <Box
          role='button'
          tabIndex={0}
          onClick={onAdd}
          onKeyDown={(e) => {
            if (e.key === 'Enter' || e.key === ' ') {
              e.preventDefault();
              onAdd();
            }
          }}
          id={`admin-connections-add-${signal.key}`}
          sx={{
            display: 'inline-flex',
            alignItems: 'center',
            gap: ds.space[0],
            cursor: 'pointer',
            fontSize: 'var(--ds-text-small)',
            fontWeight: 'var(--ds-font-weight-medium)',
            color: 'var(--ds-amber-600)',
            outline: 'none',
            '&:hover, &:focus-visible': { textDecoration: 'underline' },
          }}
        >
          + Add {signal.label.toLowerCase()}
        </Box>
      );
    }
    return (
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-1)' }}>
        <Box sx={{ width: 6, height: 6, borderRadius: '50%', flexShrink: 0, backgroundColor: 'var(--ds-gray-300)' }} />
        <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-500)' }}>Not connected</Typography>
      </Box>
    );
  };
  return (
    <Box sx={{ minWidth: 0, flex: '1 1 150px' }}>
      <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)', mb: '3px' }}>{signal.label}</Typography>
      {renderStatus()}
    </Box>
  );
};

// Renders one account. A K8s cluster carries an EKS/GKE badge and its L/M/T
// signal cells; a cloud account is just its name + status (no signal row).
const AccountRow = ({ row, onAddSignal }) => (
  <Box
    sx={{
      px: 'var(--ds-space-4)',
      py: 'var(--ds-space-3)',
      border: '1px solid var(--ds-gray-200)',
      borderRadius: 'var(--ds-radius-md)',
      backgroundColor: 'var(--ds-background-100)',
    }}
  >
    <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)', mb: row.kind === 'k8s' ? 'var(--ds-space-2)' : 0 }}>
      {row.badge ? <Label size='sm' tone='neutral' text={row.badge} /> : null}
      <Typography
        sx={{
          fontSize: 'var(--ds-text-body)',
          fontWeight: 'var(--ds-font-weight-medium)',
          color: 'var(--ds-foreground)',
          flex: 1,
          minWidth: 0,
          overflow: 'hidden',
          textOverflow: 'ellipsis',
          whiteSpace: 'nowrap',
        }}
      >
        {row.name}
      </Typography>
      <StatusPill status={row.status} />
    </Box>
    {row.kind === 'k8s' && (
      <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--ds-space-5)', pl: 'var(--ds-space-1)' }}>
        {row.signals.map((s) => (
          <SignalCell key={s.key} signal={s} agentActive={row.agentActive} onAdd={() => onAddSignal(s)} />
        ))}
      </Box>
    )}
  </Box>
);

const FILTERS = [
  { key: 'all', label: 'All' },
  { key: 'attention', label: 'Needs attention' },
  { key: 'disconnected', label: 'Disconnected' },
];

// Problems first inside each provider group.
const STATUS_ORDER = { disconnected: 0, degraded: 1, healthy: 2 };

// Group order: Kubernetes first (the richest, L/M/T-bearing group), then the
// cloud providers, self-hosted VMs last. Anything unmapped sorts last, alpha.
const PROVIDER_ORDER = { Kubernetes: 0, AWS: 1, Azure: 2, GCP: 3, CloudFoundry: 4, SelfHosted: 5 };

// Display label for a group. groupKey is the raw cloud_provider value (kept for
// grouping/ordering); these two just read nicer than the wire form.
const PROVIDER_LABEL = { SelfHosted: 'Self-hosted', CloudFoundry: 'Cloud Foundry' };

const AdminConnections = () => {
  const router = useRouter();
  const [accounts, setAccounts] = useState(null); // null = loading, [] = loaded-empty
  const [hasError, setHasError] = useState(false);
  const [q, setQ] = useState('');
  const [filter, setFilter] = useState('all');
  // Provider groups the user has explicitly collapsed. Empty = all open (the
  // default, and correct even before accounts load since expandedIds is derived
  // from whatever groups currently exist minus this set).
  const [collapsed, setCollapsed] = useState(() => new Set());

  useEffect(() => {
    let cancelled = false;
    // Two independent sources, fetched together and fail-soft: if one rejects the
    // other still renders (allSettled never short-circuits). hasError is set only
    // when BOTH fail — otherwise a missing source would blank accounts that loaded.
    Promise.allSettled([apiKubernetes.listk8ClusterData(), apiHome.getCloudAccounts()]).then(([k8sRes, cloudRes]) => {
      if (cancelled) return;
      const k8sRows = k8sRes.status === 'fulfilled' ? (k8sRes.value?.cloudaccount_k8s_aggregate || []).map(toClusterRow) : [];
      const cloudRows =
        cloudRes.status === 'fulfilled'
          ? (cloudRes.value || []).filter((a) => isCloudProvider(a?.cloud_provider) || isSelfHostedProvider(a?.cloud_provider)).map(toCloudRow)
          : [];
      // Log a failed source instead of silently swallowing it; the other still renders.
      if (k8sRes.status === 'rejected') console.error('Failed to fetch Kubernetes cluster data', k8sRes.reason);
      if (cloudRes.status === 'rejected') console.error('Failed to fetch cloud accounts', cloudRes.reason);
      setAccounts([...k8sRows, ...cloudRows]);
      setHasError(k8sRes.status === 'rejected' && cloudRes.status === 'rejected');
    });
    return () => {
      cancelled = true;
    };
  }, []);

  const summary = useMemo(() => {
    const rows = accounts || [];
    return {
      total: rows.length,
      healthy: rows.filter((r) => r.status === 'healthy').length,
      degraded: rows.filter((r) => r.status === 'degraded').length,
      disconnected: rows.filter((r) => r.status === 'disconnected').length,
    };
  }, [accounts]);

  // Filter by search + status, then group by cloud provider (Kubernetes first,
  // then the cloud providers) — the same grouping the header account picker uses.
  // Attention-first within each group.
  const groups = useMemo(() => {
    const rows = (accounts || []).filter((r) => {
      const matchesSearch = !q.trim() || r.name.toLowerCase().includes(q.trim().toLowerCase());
      const matchesFilter =
        filter === 'all' || (filter === 'attention' && r.status !== 'healthy') || (filter === 'disconnected' && r.status === 'disconnected');
      return matchesSearch && matchesFilter;
    });
    const byGroup = {};
    rows.forEach((r) => {
      (byGroup[r.groupKey] = byGroup[r.groupKey] || []).push(r);
    });
    return Object.keys(byGroup)
      .sort((a, b) => (PROVIDER_ORDER[a] ?? 99) - (PROVIDER_ORDER[b] ?? 99) || a.localeCompare(b))
      .map((groupKey) => ({
        groupKey,
        kind: byGroup[groupKey][0].kind,
        attention: byGroup[groupKey].filter((r) => r.status !== 'healthy').length,
        rows: byGroup[groupKey].sort((a, b) => STATUS_ORDER[a.status] - STATUS_ORDER[b.status] || a.name.localeCompare(b.name)),
      }));
  }, [accounts, q, filter]);

  // Missing signals are added on the Integrations tab's observability category.
  const goAddObservability = () => router.push({ pathname: '/user-management', query: { integration: 'observability' }, hash: 'integrations' });

  // Accordion expansion. While a search is active, force every matching group
  // open — a collapsed group hiding a match would make the search look empty.
  // Otherwise honour the user's collapse state (all open by default).
  const searching = q.trim().length > 0;
  const groupIds = groups.map((g) => g.groupKey);
  const expandedIds = searching ? groupIds : groupIds.filter((p) => !collapsed.has(p));
  const handleExpandedChange = (next) => {
    // Don't record toggles while searching: the force-open above would fight the
    // user's click and corrupt their real (pre-search) preference.
    if (searching) return;
    // Merge, don't overwrite: groupIds is only the *currently visible* (filtered)
    // set, so rebuilding collapsed from it alone would drop the collapse state of
    // groups a filter is hiding. Update only the visible ids against the prior set.
    setCollapsed((prev) => {
      const nextCollapsed = new Set(prev);
      groupIds.forEach((id) => (next.includes(id) ? nextCollapsed.delete(id) : nextCollapsed.add(id)));
      return nextCollapsed;
    });
  };

  if (accounts === null) {
    return (
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-4)', pt: 'var(--ds-space-2)' }}>
        <Skeleton shape='rect' width='340px' height='18px' />
        <Skeleton shape='rect' width='100%' height='84px' />
        <Skeleton shape='rect' width='100%' height='84px' />
      </Box>
    );
  }

  if (hasError) {
    return (
      <Box sx={{ py: 'var(--ds-space-7)', textAlign: 'center' }}>
        <Typography
          sx={{
            fontSize: 'var(--ds-text-body-lg)',
            fontWeight: 'var(--ds-font-weight-medium)',
            color: 'var(--ds-foreground)',
            mb: 'var(--ds-space-1)',
          }}
        >
          Couldn&apos;t load accounts
        </Typography>
        <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-600)' }}>
          Something went wrong fetching your connected accounts. Please try again in a moment.
        </Typography>
      </Box>
    );
  }

  if (accounts.length === 0) {
    return (
      <Box sx={{ py: 'var(--ds-space-7)', textAlign: 'center' }}>
        <Typography
          sx={{
            fontSize: 'var(--ds-text-body-lg)',
            fontWeight: 'var(--ds-font-weight-medium)',
            color: 'var(--ds-foreground)',
            mb: 'var(--ds-space-1)',
          }}
        >
          No accounts connected
        </Typography>
        <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-600)', mb: 'var(--ds-space-4)' }}>
          Connect a Kubernetes cluster or cloud account to see it here.
        </Typography>
        <DsButton
          id='admin-connections-add-cluster'
          tone='secondary'
          size='sm'
          onClick={() => router.push({ pathname: '/user-management', query: { integration: 'account' }, hash: 'integrations' })}
        >
          Connect an account
        </DsButton>
      </Box>
    );
  }

  return (
    <Box>
      {/* Toolbar, one line: counts + status pills (left), search + segmented filter (right) */}
      <Box
        sx={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          flexWrap: 'wrap',
          gap: 'var(--ds-space-3)',
          mb: 'var(--ds-space-3)',
        }}
      >
        {/* Left — total count + status pills (colored, not faint text) */}
        <Box sx={{ display: 'flex', alignItems: 'center', flexWrap: 'wrap', columnGap: 'var(--ds-space-3)', rowGap: 'var(--ds-space-2)' }}>
          <Typography sx={{ fontSize: 'var(--ds-text-body-lg)', color: 'var(--ds-gray-700)' }}>
            <Box component='span' sx={{ fontWeight: 'var(--ds-font-weight-semibold)', color: 'var(--ds-foreground)' }}>
              {summary.total}
            </Box>{' '}
            {summary.total === 1 ? 'account' : 'accounts'}
          </Typography>
          <Label dot size='sm' tone='success' text={`${summary.healthy} Healthy`} />
          {summary.degraded > 0 && <Label dot size='sm' tone='warning' text={`${summary.degraded} Degraded`} />}
          {summary.disconnected > 0 && <Label dot size='sm' tone='critical' text={`${summary.disconnected} Disconnected`} />}
        </Box>

        {/* Right — search + segmented filter, kept side by side on one line
            (flexShrink 0 + no wrap); the outer toolbar wraps this whole group
            below the counts only when the viewport is genuinely too narrow. */}
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-3)', flexShrink: 0 }}>
          <Box sx={{ width: 240 }}>
            <SearchInput
              id='admin-connections-search'
              value={q}
              onChange={(next) => setQ(next)}
              onClear={() => setQ('')}
              label='Search by name'
              maxWidth='100%'
            />
          </Box>
          <Box
            role='tablist'
            sx={{
              display: 'inline-flex',
              gap: ds.space[0],
              p: '3px',
              backgroundColor: 'var(--ds-background-200)',
              border: '1px solid var(--ds-gray-200)',
              borderRadius: 'var(--ds-radius-md)',
            }}
          >
            {FILTERS.map((f) => {
              const active = filter === f.key;
              return (
                <Box
                  key={f.key}
                  role='tab'
                  aria-selected={active}
                  tabIndex={0}
                  onClick={() => setFilter(f.key)}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter' || e.key === ' ') {
                      e.preventDefault();
                      setFilter(f.key);
                    }
                  }}
                  id={`admin-connections-filter-${f.key}`}
                  sx={{
                    fontSize: 'var(--ds-text-small)',
                    fontWeight: 'var(--ds-font-weight-semibold)',
                    px: 'var(--ds-space-3)',
                    py: 'var(--ds-space-1)',
                    borderRadius: 'var(--ds-radius-sm)',
                    cursor: 'pointer',
                    outline: 'none',
                    whiteSpace: 'nowrap',
                    color: active ? 'var(--ds-foreground)' : 'var(--ds-gray-500)',
                    backgroundColor: active ? 'var(--ds-background-100)' : 'transparent',
                    boxShadow: active ? '0 1px 2px rgba(16, 24, 40, 0.06)' : 'none',
                    transition: 'background-color 120ms ease, color 120ms ease',
                    '&:hover': active ? {} : { color: 'var(--ds-gray-700)' },
                    '&:focus-visible': { outline: '2px solid var(--ds-blue-500)', outlineOffset: 1 },
                  }}
                >
                  {f.label}
                </Box>
              );
            })}
          </Box>
        </Box>
      </Box>

      {/* Provider groups — accordion (all open by default); drill down to accounts */}
      {groups.length === 0 ? (
        <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-500)', textAlign: 'center', py: 'var(--ds-space-6)' }}>
          No accounts match that filter.
        </Typography>
      ) : (
        <Box
          sx={{
            '& > div': { display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-3)' },
            '& .MuiAccordion-root': {
              border: '1px solid var(--ds-gray-200)',
              borderRadius: 'var(--ds-radius-lg)',
              overflow: 'hidden',
            },
            '& .MuiAccordionSummary-root:hover': { backgroundColor: 'var(--ds-background-200)' },
          }}
        >
          <Accordion
            selection='multi'
            density='md'
            expandedIds={expandedIds}
            onExpandedChange={handleExpandedChange}
            items={groups.map((group) => {
              const noun = group.kind === 'k8s' ? 'cluster' : 'account';
              return {
                id: group.groupKey,
                label: PROVIDER_LABEL[group.groupKey] || group.groupKey,
                description: `${group.rows.length} ${group.rows.length === 1 ? noun : `${noun}s`}`,
                meta:
                  group.attention > 0 ? (
                    <Label dot size='sm' tone='warning' text={`${group.attention} need attention`} />
                  ) : (
                    <Label dot size='sm' tone='success' text={`${group.rows.length} Healthy`} />
                  ),
                body: (
                  <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-2)' }}>
                    {group.rows.map((row) => (
                      <ErrorBoundary key={row.id}>
                        <AccountRow row={row} onAddSignal={goAddObservability} />
                      </ErrorBoundary>
                    ))}
                  </Box>
                ),
              };
            })}
          />
        </Box>
      )}
    </Box>
  );
};

export default AdminConnections;
