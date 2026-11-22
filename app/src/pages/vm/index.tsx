import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { useRouter } from 'next/router';
import { Box, CircularProgress } from '@mui/material';
import AnchorComponent from '@components/common/navigation/AnchorComponent';
import PermissionDeniedContent from '@components/common/navigation/PermissionDeniedContent';
import ErrorBoundary from '@shared/ErrorBoundary';
import { useData } from '@context/DataContext';
import { hasPermission, isGrantsOnlyUser, missingPermissionMessage } from '@lib/auth';
import apiVm from '@api1/vm';
import VmEmptyState from '@components/vm/VmEmptyState';
import VmSummary from '@components/vm/VmSummary';
import VmInventory from '@components/vm/VmInventory';
import VmVulnerabilities from '@components/vm/VmVulnerabilities';
import VmPackages from '@components/vm/VmPackages';
import VmAlerts from '@components/vm/VmAlerts';
import VmTraces from '@components/vm/VmTraces';
import VmServiceMap from '@components/vm/VmServiceMap';
import VmLogs from '@components/vm/VmLogs';
import VmMetricsTab from '@components/vm/VmMetricsTab';
import {
  VmServerIcon,
  ApplicationsIcon,
  RecommendationIcon,
  OptimizeSummaryIcon,
  ServiceMapIcon,
  LogsTracesIcon,
  AllEventsIcon,
  QueryLogIcon,
} from '@assets';
import { ds } from '@utils/colors';

/**
 * Self-hosted VM fleets — the Infra → VM tab.
 *
 * Scoped to one cloud_accounts row with cloud_provider = 'SelfHosted'
 * (account_type = 'vm'). Those accounts have no provider API behind them, so
 * everything here is reported by a proxy agent (forager) rather than discovered:
 * the machines, their installed packages, and the CVEs those packages match.
 *
 * The account in scope comes from the header cluster dropdown (DataContext's
 * selectedCluster), same as the K8s and cloud detail pages — self-hosted
 * accounts are a group in that dropdown. This page owns no account control of
 * its own; it only has to cope with landing while a non-self-hosted account is
 * selected, which is what resolveAccount below does.
 */
const SELF_HOSTED = 'SelfHosted';

const tabOptions = [
  { name: 'Summary', id: 'vm-summary', fragment: 'summary', value: 0, disabled: false, icon: OptimizeSummaryIcon },
  { name: 'Virtual Machines', id: 'vm-instances', fragment: 'instances', value: 1, disabled: false, icon: VmServerIcon },
  {
    name: 'Vulnerabilities',
    id: 'vm-vulnerabilities',
    fragment: 'vulnerabilities',
    value: 2,
    disabled: false,
    icon: RecommendationIcon,
    iconSize: 18,
  },
  { name: 'Packages', id: 'vm-packages', fragment: 'packages', value: 3, disabled: false, icon: ApplicationsIcon },
  { name: 'Service Map', id: 'vm-service-map', fragment: 'service-map', value: 4, disabled: false, icon: ServiceMapIcon },
  { name: 'Traces', id: 'vm-traces', fragment: 'traces', value: 5, disabled: false, icon: LogsTracesIcon },
  { name: 'Events', id: 'vm-alerts', fragment: 'alerts', value: 6, disabled: false, icon: AllEventsIcon },
  { name: 'Metrics', id: 'vm-metrics', fragment: 'metrics', value: 7, disabled: false, icon: OptimizeSummaryIcon },
  { name: 'Logs', id: 'vm-logs', fragment: 'logs', value: 8, disabled: false, icon: QueryLogIcon },
];

const PACKAGES_TAB = 3;
const SERVICE_MAP_TAB = 4;
const TRACES_TAB = 5;
const ALERTS_TAB = 6;
const METRICS_TAB = 7;
const LOGS_TAB = 8;

/**
 * Deep-link params the Summary cards use to open the Vulnerabilities tab already
 * narrowed: one scope (VM / package / CVE) plus an optional severity. Read here
 * rather than inside the tab so the URL stays the single source of truth for
 * what is filtered, and the chip that clears it can just drop the param.
 */
const SCOPE_PARAMS = [
  // VM and Package seed the tab's own dropdowns, which then show and clear them;
  // only the CVE scope has no control of its own, so only it needs the chip.
  { param: 'vmId', prop: 'cloudResourceId', label: 'VM', chip: false },
  { param: 'packageName', prop: 'packageName', label: 'Package', chip: false },
  { param: 'vulnId', prop: 'vulnId', label: 'Vulnerability', chip: true },
] as const;

/** How many VMs to name-resolve for the account-wide Packages tab. */
const VM_NAME_LOOKUP_LIMIT = 500;

const VmPage = () => {
  const router = useRouter();
  const { selectedCluster, setSelectedCluster, allCluster } = useData();
  // Matches the sidebar's own gate on the Infra -> VM row (module: 'cloud',
  // layout/index.jsx) -- without this, a grants-only user the sidebar hides VM
  // from could still reach every tab here by typing the URL.
  const canReadCloud = !isGrantsOnlyUser() || hasPermission('cloud', 'Read');
  const filterOptions = useMemo(
    () =>
      canReadCloud ? tabOptions : tabOptions.map((opt) => ({ ...opt, disabled: true, disabledTooltip: missingPermissionMessage('cloud:Read') })),
    [canReadCloud]
  );
  const [accounts, setAccounts] = useState<any[]>([]);
  const [loadingAccounts, setLoadingAccounts] = useState(true);
  const [selectedTab, setSelectedTab] = useState<number | null>(null);
  const [vmNames, setVmNames] = useState<Record<string, string>>({});

  // `refresh` bypasses the 1h accounts cache — an account created from the empty
  // state a second ago would otherwise not be in it.
  const loadAccounts = useCallback((refresh = false) => {
    setLoadingAccounts(true);
    return apiVm
      .getSelfHostedAccounts(refresh)
      .then(setAccounts)
      .catch((error) => {
        console.error('Failed to list self-hosted accounts:', error);
        setAccounts([]);
      })
      .finally(() => setLoadingAccounts(false));
  }, []);

  useEffect(() => {
    loadAccounts();
  }, [loadAccounts]);

  const accountId = selectedCluster?.cloud_provider === SELF_HOSTED ? selectedCluster.value : null;

  // The header dropdown spans every provider, so /vm can be reached with a K8s
  // or AWS account in scope — from the sidebar, or by a bookmark saved while a
  // cloud account was selected. Adopt the first self-hosted account instead of
  // rendering a page that describes nothing, and push it into the shared
  // selection so the dropdown agrees with what is on screen.
  useEffect(() => {
    if (!router.isReady || !accounts.length) return;
    const fromQuery = typeof router.query.accountId === 'string' ? router.query.accountId : undefined;
    // Wait for `allCluster` before deciding what a URL id means -- it loads
    // independently of (and sometimes slower than) `accounts` above.
    if (fromQuery && allCluster === null) return;
    // `?accountId=` may already name a real, non-self-hosted account (direct
    // link, or ClusterDropdown mid-switch away from /vm) -- route to its own
    // page instead of swapping in an unrelated VM fleet. Checked before the
    // `accountId` guard below so it still redirects even if `selectedCluster`
    // hasn't caught up to the URL yet.
    const knownOtherAccount = fromQuery
      ? allCluster?.find((cluster: any) => cluster.value === fromQuery && cluster.cloud_provider !== SELF_HOSTED)
      : undefined;
    if (knownOtherAccount) {
      const target =
        knownOtherAccount.cloud_provider === 'K8s' ? `/kubernetes/details/${fromQuery}#summary` : `/cloud-account/details/${fromQuery}#summary`;
      router.replace(target);
      return;
    }
    if (accountId) return;
    const match = accounts.find((account) => account.id === fromQuery) || accounts[0];
    setSelectedCluster({
      label: match.account_name,
      value: match.id,
      status: match.status || '',
      cloud_provider: match.cloud_provider,
      account_type: match.account_type || '',
      agent: match.agents?.[0] || {},
    });
  }, [router.isReady, router.query.accountId, accounts, accountId, allCluster, setSelectedCluster, router]);

  // Keep ?accountId= in step with the selection so a copied URL reopens on the
  // same fleet. Shallow: the account is read from context, not from the query.
  // The hash has to be carried through by hand — a UrlObject without one
  // navigates to a fragment-less URL, which would drop the tab a deep link
  // arrived on (and the filters that came with it) on every fresh load.
  useEffect(() => {
    if (!router.isReady || !accountId || router.query.accountId === accountId) return;
    const hash = window.location.hash.replace('#', '');
    router.replace({ pathname: '/vm', query: { ...router.query, accountId }, hash }, undefined, { shallow: true });
  }, [router.isReady, accountId]);

  useEffect(() => {
    // window.location.hash rather than router.asPath — on a statically optimized
    // page asPath can still be the server-rendered value on mount, which would
    // open the first tab on a deep link. Same source AnchorComponent reads.
    const hash = window.location.hash.replace('#', '');
    const tab = tabOptions.find((option) => option.fragment === hash);
    setSelectedTab(tab ? tab.value : 0);
  }, []);

  // Only the account-wide Packages tab needs a resource-id → name map; the
  // per-VM views already know which VM they are showing.
  useEffect(() => {
    if (!accountId || selectedTab !== PACKAGES_TAB) return undefined;
    let cancelled = false;
    apiVm
      .listVms({ accountId, limit: VM_NAME_LOOKUP_LIMIT, offset: 0 })
      .then(({ rows }) => {
        if (cancelled) return;
        setVmNames(Object.fromEntries(rows.map((vm) => [vm.id, vm.name || vm.resourse_id])));
      })
      .catch((error) => {
        if (!cancelled) console.error('Failed to resolve VM names:', error);
      });
    return () => {
      cancelled = true;
    };
  }, [accountId, selectedTab]);

  // First scope param present wins — the cards only ever set one.
  const scope = SCOPE_PARAMS.map((entry) => ({ entry, value: router.query[entry.param] })).find(
    (candidate) => typeof candidate.value === 'string' && candidate.value
  );
  const scopeProps = scope ? { [scope.entry.prop]: scope.value as string } : {};
  const scopeLabel = scope?.entry.chip ? `${scope.entry.label}: ${scope.value}` : undefined;
  // The Instances tab only understands a vulnId scope (which VMs a CVE sits on) — vmId/packageName have no meaning there.
  const vulnScope = scope?.entry.param === 'vulnId' ? (scope.value as string) : undefined;
  const severityParam = typeof router.query.severity === 'string' ? router.query.severity : undefined;

  // Drops the scope only. Severity stays with its dropdown, which owns it once
  // the tab is mounted.
  const clearScope = () => {
    const query = { ...router.query };
    SCOPE_PARAMS.forEach((entry) => delete query[entry.param]);
    const fragment = tabOptions.find((option) => option.value === selectedTab)?.fragment || 'vulnerabilities';
    router.replace({ pathname: '/vm', query, hash: fragment }, undefined, { shallow: true });
  };

  if (loadingAccounts) {
    return (
      <Box sx={{ display: 'flex', justifyContent: 'center', alignItems: 'center', minHeight: '70vh' }}>
        <CircularProgress />
      </Box>
    );
  }

  if (canReadCloud && !accounts.length) {
    return (
      <Box sx={{ p: ds.space[6] }}>
        <VmEmptyState onAccountCreated={() => loadAccounts(true)} />
      </Box>
    );
  }

  return (
    <>
      <AnchorComponent manageRoute={true} filterOptions={filterOptions} onChangeFilter={(val: number) => setSelectedTab(val)} />
      <ErrorBoundary key={`${accountId}-${selectedTab ?? 'none'}`}>
        {!canReadCloud && <PermissionDeniedContent message={missingPermissionMessage('cloud:Read')} />}
        {canReadCloud && accountId && selectedTab === 0 && <VmSummary accountId={accountId} />}
        {canReadCloud && accountId && selectedTab === 1 && (
          <VmInventory
            accountId={accountId}
            vulnId={vulnScope}
            scopeLabel={vulnScope ? scopeLabel : undefined}
            onClearScope={vulnScope ? clearScope : undefined}
          />
        )}
        {canReadCloud && accountId && selectedTab === 2 && (
          <VmVulnerabilities
            accountId={accountId}
            {...scopeProps}
            scopeLabel={scopeLabel}
            onClearScope={scopeLabel ? clearScope : undefined}
            initialSeverity={severityParam}
          />
        )}
        {canReadCloud && accountId && selectedTab === PACKAGES_TAB && <VmPackages accountId={accountId} vmNames={vmNames} />}
        {canReadCloud && accountId && selectedTab === SERVICE_MAP_TAB && <VmServiceMap accountId={accountId} />}
        {canReadCloud && accountId && selectedTab === TRACES_TAB && <VmTraces accountId={accountId} />}
        {canReadCloud && accountId && selectedTab === ALERTS_TAB && <VmAlerts accountId={accountId} />}
        {canReadCloud && accountId && selectedTab === METRICS_TAB && <VmMetricsTab accountId={accountId} />}
        {canReadCloud && accountId && selectedTab === LOGS_TAB && <VmLogs accountId={accountId} />}
      </ErrorBoundary>
    </>
  );
};

export default VmPage;
