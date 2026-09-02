import React, { useCallback, useEffect, useState } from 'react';
import { Box } from '@mui/material';
import CustomTable from '@shared/tables/CustomTable';
import ListingLayout from '@ui/ListingLayout';
import SearchInput from '@ui/SearchInput';
import { Label } from '@ui/Label';
import { Button } from '@ui/Button';
import Datetime from '@shared/format/Datetime';
import DownloadButton from '@shared/buttons/DownloadButton';
import { usePagination } from '@hooks/usePagination';
import { hasWriteAccess } from '@lib/auth';
import apiVm, { VmResource } from '@api1/vm';
import { CellText, SeverityCounts, useLatestRequest } from './common';
import ScanVmDialog from './ScanVmDialog';
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
}

/**
 * The self-hosted fleet's asset list.
 *
 * Rows come from cloud_resourses, but the OS, package count and last-scan time
 * come from vm_package — a VM that has never been scanned shows the resource
 * with those columns blank rather than being hidden, because "known asset, never
 * scanned" is exactly the state this page exists to surface.
 */
const VmInventory = ({ accountId }: VmInventoryProps) => {
  const [loading, setLoading] = useState(false);
  const [vms, setVms] = useState<VmResource[]>([]);
  const [total, setTotal] = useState(0);
  const [packageCounts, setPackageCounts] = useState<Record<string, { count: number; lastSeenAt: string }>>({});
  const [osByVm, setOsByVm] = useState<Record<string, string>>({});
  const [vulnCounts, setVulnCounts] = useState<Record<string, Record<string, number>>>({});
  const [searchInput, setSearchInput] = useState('');
  const [appliedSearch, setAppliedSearch] = useState('');
  const [scanTarget, setScanTarget] = useState<VmResource | null>(null);
  const { page, rowsPerPage, changePage, setPage } = usePagination();
  const beginVmsRequest = useLatestRequest();
  const beginEnrichmentRequest = useLatestRequest();

  const canScan = hasWriteAccess(accountId);

  const fetchVms = useCallback(() => {
    if (!accountId) return;
    setLoading(true);
    // Paging or searching faster than the previous response arrives would
    // otherwise let the older result land last and show the wrong page.
    const isLatest = beginVmsRequest();
    apiVm
      .listVms({ accountId, search: appliedSearch, limit: rowsPerPage, offset: page * rowsPerPage })
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
  }, [accountId, appliedSearch, page, rowsPerPage, beginVmsRequest]);

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
  }, [accountId, beginEnrichmentRequest]);

  useEffect(() => {
    fetchVms();
  }, [fetchVms]);

  useEffect(() => {
    fetchEnrichment();
  }, [fetchEnrichment]);

  const onSearchEnter = () => {
    setAppliedSearch(searchInput);
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
      { component: packages?.lastSeenAt ? <Datetime value={packages.lastSeenAt} /> : <CellText text='—' /> },
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
        <ListingLayout.Toolbar actions={<DownloadButton id={`${TABLE_ID}-download`} onClick={() => ({ tableId: TABLE_ID })} />}>
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
    </Box>
  );
};

export default VmInventory;
