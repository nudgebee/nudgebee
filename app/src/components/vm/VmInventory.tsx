import React, { useCallback, useEffect, useState } from 'react';
import { Box } from '@mui/material';
import CustomTable from '@shared/tables/CustomTable';
import ListingLayout from '@ui/ListingLayout';
import SearchInput from '@ui/SearchInput';
import { Label } from '@ui/Label';
import { Chip } from '@ui/Chip';
import { Button } from '@ui/Button';
import Datetime from '@shared/format/Datetime';
import DownloadButton from '@shared/buttons/DownloadButton';
import { usePagination } from '@hooks/usePagination';
import { hasWriteAccess } from '@lib/auth';
import apiVm, { VmResource, VmScanStatus } from '@api1/vm';
import { CellText, ScanStatusIndicator, SeverityCounts, useLatestRequest } from './common';
import ScanVmDialog from './ScanVmDialog';
import ScanAccountDialog from './ScanAccountDialog';
import VmPackages from './VmPackages';
import VmVulnerabilities from './VmVulnerabilities';
import { ds } from '@utils/colors';

const HEADERS = ['Name', 'Host / Resource ID', 'Operating System', 'Status', 'Packages', 'Open Vulnerabilities', 'Last Scan', ''];

const TABLE_ID = 'VM_INVENTORY_TABLE';

const statusTone = (status?: string) => {
  const value = (status || '').toLowerCase();
  if (value === 'active' || value === 'running') return 'success' as const;
  if (value === 'stopped' || value === 'terminated' || value === 'deallocated') return 'critical' as const;
  return 'neutral' as const;
};

interface VmInventoryProps {
  accountId: string;
  /** Scope the list to just the VMs one CVE affects — set when arriving from a CVE deep link (Optimize card, top-CVE summary cards, …). */
  vulnId?: string;
  scopeLabel?: string;
  onClearScope?: () => void;
}

/**
 * The self-hosted fleet's asset list.
 *
 * Rows come from cloud_resourses, but the OS, package count and last-scan time
 * come from vm_package — a VM that has never been scanned shows the resource
 * with those columns blank rather than being hidden, because "known asset, never
 * scanned" is exactly the state this page exists to surface.
 */
const VmInventory = ({ accountId, vulnId, scopeLabel, onClearScope }: VmInventoryProps) => {
  const [loading, setLoading] = useState(false);
  const [vms, setVms] = useState<VmResource[]>([]);
  const [total, setTotal] = useState(0);
  const [packageCounts, setPackageCounts] = useState<Record<string, { count: number; lastSeenAt: string }>>({});
  const [osByVm, setOsByVm] = useState<Record<string, string>>({});
  const [vulnCounts, setVulnCounts] = useState<Record<string, Record<string, number>>>({});
  const [scanStatuses, setScanStatuses] = useState<Record<string, VmScanStatus>>({});
  const [searchInput, setSearchInput] = useState('');
  const [appliedSearch, setAppliedSearch] = useState('');
  const [scanTarget, setScanTarget] = useState<VmResource | null>(null);
  const [showAccountScan, setShowAccountScan] = useState(false);
  const [scopedResourceIds, setScopedResourceIds] = useState<string[] | null>(null);
  const { page, rowsPerPage, changePage, setPage } = usePagination();
  const beginVmsRequest = useLatestRequest();
  const beginEnrichmentRequest = useLatestRequest();
  const beginScopeRequest = useLatestRequest();

  const canScan = hasWriteAccess(accountId);

  // A vulnId scope names a CVE, not a VM — resolve which machines it actually
  // sits on first, then fetch exactly those (below) instead of paging the fleet.
  useEffect(() => {
    if (!accountId || !vulnId) {
      setScopedResourceIds(null);
      return;
    }
    const isLatest = beginScopeRequest();
    apiVm
      .listVulnerabilityGroups({ accountId, grouping: 'vm', vulnId, includeTotal: false, limit: 500 })
      .then(({ rows }) => {
        if (!isLatest()) return;
        setScopedResourceIds(rows.map((row) => row.key).filter(Boolean));
      })
      .catch((error) => {
        if (!isLatest()) return;
        console.error('Failed to resolve VMs for vulnerability scope:', error);
        setScopedResourceIds([]);
      });
  }, [accountId, vulnId, beginScopeRequest]);

  const fetchVms = useCallback(() => {
    if (!accountId) return;
    // Scope resolution above hasn't landed yet — wait for it rather than fetching an empty/unscoped page first.
    if (vulnId && !scopedResourceIds) return;
    // vulnId was just cleared but the scope-resolution effect hasn't reset scopedResourceIds
    // yet — wait for that reset instead of fetching the whole unscoped fleet twice.
    if (!vulnId && scopedResourceIds !== null) return;
    // Resolved and empty (the CVE no longer sits on any VM) — an empty `resourceIds` filter
    // is a no-op in listVms, which would silently fall back to the whole fleet.
    if (vulnId && scopedResourceIds?.length === 0) {
      setVms([]);
      setTotal(0);
      return;
    }
    setLoading(true);
    // Paging or searching faster than the previous response arrives would
    // otherwise let the older result land last and show the wrong page.
    const isLatest = beginVmsRequest();
    const request = vulnId
      ? apiVm.listVms({ accountId, resourceIds: scopedResourceIds || [], limit: (scopedResourceIds || []).length || 1 })
      : apiVm.listVms({ accountId, search: appliedSearch, limit: rowsPerPage, offset: page * rowsPerPage });
    request
      .then(({ rows, total: count }) => {
        if (!isLatest()) return;
        setVms(rows);
        setTotal(count);
      })
      .catch((error) => {
        if (!isLatest()) return;
        console.error('Failed to list VMs:', error);
        setVms([]);
        setTotal(0);
      })
      .finally(() => {
        if (isLatest()) setLoading(false);
      });
  }, [accountId, appliedSearch, page, rowsPerPage, beginVmsRequest, vulnId, scopedResourceIds]);

  // Enrichment is account-wide (one grouping query each), not per page of rows,
  // so it does not refetch as the operator pages through the table.
  const fetchEnrichment = useCallback(() => {
    if (!accountId) return;
    // One stamp covers all three: they are refetched together, so a later round
    // must win on every one of them.
    const isLatest = beginEnrichmentRequest();
    apiVm
      .getPackageCountsByVm(accountId)
      .then((counts) => isLatest() && setPackageCounts(counts))
      .catch((error) => console.error('Failed to load VM package counts:', error));
    apiVm
      .getOsByVm(accountId)
      .then((osByResource) => isLatest() && setOsByVm(osByResource))
      .catch((error) => console.error('Failed to load VM operating systems:', error));
    apiVm
      .getVulnerabilityCountsByVm(accountId)
      .then((counts) => isLatest() && setVulnCounts(counts))
      .catch((error) => console.error('Failed to load VM vulnerability counts:', error));
    apiVm
      .getScanStatuses(accountId)
      .then((rows) => isLatest() && setScanStatuses(Object.fromEntries(rows.map((row) => [row.cloudResourceId, row]))))
      .catch((error) => console.error('Failed to load VM scan statuses:', error));
  }, [accountId, beginEnrichmentRequest]);

  useEffect(() => {
    fetchVms();
  }, [fetchVms]);

  useEffect(() => {
    fetchEnrichment();
  }, [fetchEnrichment]);

  // While any VM is mid-scan, poll so "Scanning…" resolves to a fresh Last
  // Scan time or "Scan failed" without a manual reload — the RPC only acks
  // the start (see #38466). Capped at scanTimeout (server-side) + a margin,
  // so a tab left open doesn't poll forever if a status write is ever lost.
  const [pollDeadline, setPollDeadline] = useState<number | null>(null);
  const hasActiveScan = Object.values(scanStatuses).some((status) => status.status === 'started');

  // extendPoll is also called directly when an account-wide scan is
  // acknowledged (see ScanAccountDialog's onClose below): scanVmAccount only
  // publishes queue messages, so no vm_scan_status row — and therefore no
  // hasActiveScan — exists until the queue consumer picks the work up later.
  // Without this, the page would never start polling for an account scan and
  // would sit on stale data until a manual reload.
  const extendPoll = useCallback(() => setPollDeadline(Date.now() + 16 * 60 * 1000), []);

  useEffect(() => {
    if (hasActiveScan) extendPoll();
  }, [hasActiveScan, extendPoll]);

  useEffect(() => {
    if (!pollDeadline) return undefined;
    const interval = setInterval(() => {
      if (Date.now() > pollDeadline) {
        setPollDeadline(null);
        return;
      }
      fetchEnrichment();
    }, 10000);
    return () => clearInterval(interval);
  }, [pollDeadline, fetchEnrichment]);

  const onSearchEnter = () => {
    // Pasted text often carries a stray space, which the substring match would
    // otherwise treat as part of the name and find nothing.
    setAppliedSearch(searchInput.trim());
    setPage(0);
  };

  const onSearchClear = () => {
    setSearchInput('');
    setAppliedSearch('');
    setPage(0);
  };

  const tableData = vms.map((vm) => {
    const packages = packageCounts[vm.id];
    const severities = vulnCounts[vm.id];
    return [
      { component: <CellText text={vm.name} subtext={vm.type} />, drilldownQuery: vm },
      {
        component: (
          <CellText text={vm.meta?.private_ip || vm.meta?.host || vm.resourse_id} subtext={vm.meta?.private_ip ? vm.resourse_id : undefined} mono />
        ),
      },
      { component: <CellText text={osByVm[vm.id]} /> },
      { component: <Label tone={statusTone(vm.status)} size='sm' text={vm.status || 'Unknown'} /> },
      { component: <CellText text={packages ? String(packages.count) : 'Not scanned'} /> },
      { component: <SeverityCounts counts={severities} /> },
      {
        component: (
          <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2] }}>
            {packages?.lastSeenAt ? <Datetime value={packages.lastSeenAt} /> : <CellText text='—' />}
            <ScanStatusIndicator status={scanStatuses[vm.id]} />
          </Box>
        ),
      },
      {
        component: canScan ? (
          <Button id={`vm-scan-${vm.id}`} tone='secondary' size='sm' onClick={() => setScanTarget(vm)}>
            Scan
          </Button>
        ) : (
          <></>
        ),
      },
    ];
  });

  return (
    <Box sx={{ px: ds.space[5], pb: ds.space[5] }}>
      <ListingLayout id='vm-inventory'>
        <ListingLayout.Toolbar
          actions={
            <Box sx={{ display: 'flex', gap: ds.space[2] }}>
              {canScan && (
                <Button id='vm-scan-account' tone='secondary' size='sm' onClick={() => setShowAccountScan(true)}>
                  Scan
                </Button>
              )}
              <DownloadButton id={`${TABLE_ID}-download`} onClick={() => ({ tableId: TABLE_ID })} />
            </Box>
          }
        >
          {vulnId ? (
            scopeLabel && (
              <Chip variant='filter' size='sm' tone='info' onDismiss={onClearScope} sx={{ alignSelf: 'center' }}>
                {scopeLabel}
              </Chip>
            )
          ) : (
            <SearchInput
              id='vm-inventory-search'
              label='Search By Name Or Resource ID'
              value={searchInput}
              onChange={(next: string) => {
                if (searchInput !== '' && next === '') {
                  setAppliedSearch('');
                  setPage(0);
                }
                setSearchInput(next);
              }}
              onEnterPress={onSearchEnter}
              onClear={onSearchClear}
            />
          )}
        </ListingLayout.Toolbar>

        <ListingLayout.Body>
          <CustomTable
            id={TABLE_ID}
            headers={HEADERS}
            tableData={tableData}
            loading={loading}
            rowsPerPage={rowsPerPage}
            pageNumber={page + 1}
            totalRows={total}
            onPageChange={changePage}
            showExpandable={true}
            expandAllInitially={Boolean(vulnId)}
            emptyHeading='No virtual machines yet'
            emptySubHeading='VMs appear here once a proxy agent has reported them for this account.'
            showUpdatedEmptyData={true}
            expandable={{
              tabs: [
                {
                  text: 'Vulnerabilities',
                  value: 0,
                  key: 'vm-row-vulnerabilities',
                  componentFn: (_opt: any, vm: VmResource) => <VmVulnerabilities accountId={accountId} cloudResourceId={vm.id} embedded />,
                },
                {
                  text: 'Packages',
                  value: 1,
                  key: 'vm-row-packages',
                  componentFn: (_opt: any, vm: VmResource) => <VmPackages accountId={accountId} cloudResourceId={vm.id} embedded />,
                },
              ],
            }}
          />
        </ListingLayout.Body>
      </ListingLayout>

      <ScanVmDialog
        open={Boolean(scanTarget)}
        accountId={accountId}
        vm={scanTarget}
        onClose={(started: boolean) => {
          setScanTarget(null);
          // A scan that just started has not written anything yet; refresh the
          // enrichment anyway so a scan finished a moment ago is picked up.
          if (started) fetchEnrichment();
        }}
      />

      <ScanAccountDialog
        open={showAccountScan}
        accountId={accountId}
        onClose={(started: boolean) => {
          setShowAccountScan(false);
          if (started) {
            fetchEnrichment();
            // The account-wide scan only publishes queue messages here — no
            // vm_scan_status row exists yet, so hasActiveScan alone would
            // never trigger polling. Start it explicitly.
            extendPoll();
          }
        }}
      />
    </Box>
  );
};

export default VmInventory;
