import { useEffect, useMemo, useState } from 'react';
import { Box, Typography } from '@mui/material';
import apiHome from '@api1/home';
import KubernetesSecurity from '@components/recommendations/KubernetesSecurity';
import KubernetesCisSecurityV2 from '@components/recommendations/KubernetesCisSecurityV2';
import VmVulnerabilities from '@components/vm/VmVulnerabilities';
import CloudPostureView from '@components/recommendations/security/CloudPostureView';
import FilterDropdown from '@ui/FilterDropdown';
import CloudProviderIcon from '@shared/icons/CloudIcon';
import { Skeleton } from '@ui/Skeleton';
import { ds } from '@utils/colors';

const renderAccountGroupIcon = (provider: string) => <CloudProviderIcon cloud_provider={provider} width='14px' height='14px' />;

/**
 * Which accounts a sub-tab can have findings on, by `account_type`.
 *
 * Scoping by type rather than by cloud_provider matters: the k8s tabs used to
 * offer every non-SelfHosted account, so the eleven cloud accounts were
 * selectable and always returned an empty table. Verified against dev — no
 * cloud-typed account carries image-scan or CIS findings, and no k8s-typed
 * account carries an aws_/azure_/gcp_ posture rule — so the split is clean.
 */
const TAB_ACCOUNT_TYPE = ['kubernetes', 'kubernetes', 'vm', 'cloud'] as const;

interface AccountInfo {
  name: string;
  provider: string;
  type: string;
}

/**
 * Cross-account security recommendations on /optimise. Mirrors the per-cluster
 * Security & Tools views (Image Scan, CIS Scan) and the /vm findings list, but
 * spans every account in the tenant. The scope is always an explicit account-id
 * list — never an unfiltered tenant-wide query — so the security queries keep
 * seeking their account-first indexes; the reused components take the list and
 * scope each row's actions to the row's own account.
 */
const SecurityView = ({ subTab = 0 }: { subTab?: number }) => {
  const [accounts, setAccounts] = useState<Record<string, AccountInfo> | null>(null);
  const [selectedAccounts, setSelectedAccounts] = useState<string[]>([]);

  useEffect(() => {
    apiHome
      .getCloudAccounts()
      .then((res: any) => {
        // getCloudAccounts swallows its own failures and resolves to [], but guard
        // the shape anyway so a non-array never reaches .map().
        const list = Array.isArray(res) ? res : [];
        setAccounts(
          Object.fromEntries(
            list.map((a: any) => [a.id, { name: a.account_name || a.id, provider: a.cloud_provider || '', type: a.account_type || '' }])
          )
        );
      })
      .catch((err: any) => {
        // Not reachable today, but leaving `accounts` null would strand the tab on
        // its skeleton forever; an empty map shows the empty state instead.
        console.error('Failed to fetch cloud accounts:', err);
        setAccounts({});
      });
  }, []);

  const isVmTab = subTab === 2;
  const isCloudTab = subTab === 3;
  const accountType = TAB_ACCOUNT_TYPE[subTab] ?? 'kubernetes';

  // Accounts the active sub-tab can have findings on.
  const relevantIds = useMemo(() => {
    if (!accounts) return null;
    return Object.entries(accounts)
      .filter(([, info]) => info.type === accountType)
      .map(([id]) => id);
  }, [accounts, accountType]);

  // Empty selection means "all"; a selection is intersected with the sub-tab's
  // relevant accounts so a picked VM account never leaks into the k8s queries.
  const scopeIds = useMemo(() => {
    if (!relevantIds) return null;
    const chosen = selectedAccounts.filter((id) => relevantIds.includes(id));
    return chosen.length > 0 ? chosen : relevantIds;
  }, [relevantIds, selectedAccounts]);

  const accountsById = useMemo(() => Object.fromEntries(Object.entries(accounts || {}).map(([id, info]) => [id, info.name])), [accounts]);
  const providerById = useMemo(() => Object.fromEntries(Object.entries(accounts || {}).map(([id, info]) => [id, info.provider])), [accounts]);

  const accountFilterOptions = useMemo(
    () =>
      Object.entries(accounts || {})
        .filter(([, info]) => info.type === accountType)
        .map(([id, info]) => ({ label: info.name, value: id, group: info.provider || 'Other' })),
    [accounts, accountType]
  );

  // Stable object identity: the reused components key their fetch effects on
  // kubernetes.id, so a fresh array every render would refetch in a loop.
  const kubernetesScope = useMemo(() => ({ id: scopeIds || [] }), [scopeIds]);

  if (accounts === null) {
    return (
      <Box sx={{ p: ds.space[5] }} data-testid='optimise-security-loading'>
        <Skeleton shape='rect' height={40} />
        <Skeleton shape='rect' height={240} />
      </Box>
    );
  }

  if (!scopeIds || scopeIds.length === 0) {
    return (
      <Box sx={{ p: ds.space[6], textAlign: 'center' }} data-testid='optimise-security-empty'>
        <Typography sx={{ fontSize: ds.text.bodyLg, color: ds.gray[600] }}>
          {isVmTab
            ? 'No self-hosted VM accounts connected. Findings appear here once a VM account is added and scanned.'
            : isCloudTab
            ? 'No cloud accounts connected. Findings appear here once an AWS, Azure or GCP account is added and scanned.'
            : 'No clusters connected. Findings appear here once a cluster is connected and scanned.'}
        </Typography>
      </Box>
    );
  }

  // Rendered first inside each view's own toolbar, so the Account picker sits
  // beside Status the way the Recommendations tab lays its filters out.
  const accountFilter = (
    <FilterDropdown
      id='optimise-security-filter-account'
      label='Account'
      multiple
      grouped
      groupIcon={renderAccountGroupIcon}
      options={accountFilterOptions}
      value={accountFilterOptions.filter((o) => selectedAccounts.includes(o.value))}
      onSelect={(_e: any, items: any) => {
        setSelectedAccounts((Array.isArray(items) ? items : []).map((it: any) => it.value));
      }}
    />
  );

  return (
    // No horizontal padding: each view's own ListingLayout carries the page
    // inset, so adding one here would make these tables narrower than the
    // Recommendations tab's.
    <Box sx={{ pt: ds.space[4] }} data-testid='optimise-security-view'>
      {subTab === 0 && <KubernetesSecurity kubernetes={kubernetesScope} accountsById={accountsById} leadingFilters={accountFilter} heading='' />}
      {subTab === 1 && (
        <KubernetesCisSecurityV2
          kubernetes={kubernetesScope}
          accountsById={accountsById}
          leadingFilters={accountFilter}
          disableInfographic
          heading=''
        />
      )}
      {isVmTab && <VmVulnerabilities accountId={scopeIds} accountsById={accountsById} leadingFilters={accountFilter} hidePageInset />}
      {isCloudTab && <CloudPostureView accountId={scopeIds} accountsById={accountsById} providerById={providerById} leadingFilters={accountFilter} />}
    </Box>
  );
};

export default SecurityView;
