import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { Box } from '@mui/material';
import CustomTable from '@shared/tables/CustomTable';
import ListingLayout from '@ui/ListingLayout';
import SearchInput from '@ui/SearchInput';
import FilterDropdown from '@ui/FilterDropdown';
import Datetime from '@shared/format/Datetime';
import DownloadButton from '@shared/buttons/DownloadButton';
import { usePagination } from '@hooks/usePagination';
import apiVm, { VmPackage } from '@api1/vm';
import { CellText, joinVmNames, useLatestRequest } from './common';
import { ds } from '@utils/colors';

// Widths are explicit because CustomTable falls back to 20% per column, which
// on this table starved Last Seen (its relative time wrapped to three lines) to
// pay for a Type column that only ever holds "rpm" or "deb".
const HEADERS = [
  { name: 'Package', width: '16%' },
  { name: 'Version', width: '18%' },
  { name: 'Architecture', width: '10%' },
  { name: 'Type', width: '5%' },
  { name: 'Source', width: '14%' },
  { name: 'Operating System', width: '11%' },
  { name: 'VM', width: '16%' },
  { name: 'Last Seen', width: '10%' },
];
const EMBEDDED_HEADERS = [
  { name: 'Package', width: '22%' },
  { name: 'Version', width: '24%' },
  { name: 'Architecture', width: '16%' },
  { name: 'Type', width: '5%' },
  { name: 'Source', width: '20%' },
  { name: 'Last Seen', width: '13%' },
];

const PKG_TYPE_OPTIONS = [
  { label: 'deb', value: 'deb' },
  { label: 'rpm', value: 'rpm' },
];

interface VmPackagesProps {
  accountId: string;
  cloudResourceId?: string;
  embedded?: boolean;
  /** resource id → display name, so the account-wide view can name each VM. */
  vmNames?: Record<string, string>;
}

/**
 * Installed-package inventory (vm_package), as collected by the last scan.
 * Only active rows are listed — a re-scan archives what it no longer sees, so
 * `is_active = false` means "was installed, isn't any more".
 */
const VmPackages = ({ accountId, cloudResourceId, embedded = false, vmNames = {} }: VmPackagesProps) => {
  const [loading, setLoading] = useState(false);
  const [rows, setRows] = useState<VmPackage[]>([]);
  const [total, setTotal] = useState(0);
  const [searchInput, setSearchInput] = useState('');
  const [appliedSearch, setAppliedSearch] = useState('');
  const [pkgType, setPkgType] = useState<string | null>(null);
  const [vmFilter, setVmFilter] = useState<string | null>(null);
  const { page, rowsPerPage, changePage, setPage } = usePagination(embedded ? 5 : 10);
  const beginRequest = useLatestRequest();

  const tableId = cloudResourceId ? `VM_PACKAGES_${cloudResourceId}` : 'VM_PACKAGES_TABLE';

  // An embedded table is already pinned to one VM; the filter only exists on the
  // account-wide view, which is also the only place vmNames is populated.
  const vmOptions = useMemo(() => Object.entries(vmNames).map(([value, label]) => ({ label, value })), [vmNames]);
  const resourceId = cloudResourceId || vmFilter || undefined;

  const fetchPackages = useCallback(() => {
    if (!accountId) return;
    setLoading(true);
    // Searching or switching a filter mid-flight must not let the older
    // response overwrite the newer one.
    const isLatest = beginRequest();
    apiVm
      .listPackages({
        accountId,
        cloudResourceId: resourceId,
        search: appliedSearch,
        pkgType: pkgType || undefined,
        limit: rowsPerPage,
        offset: page * rowsPerPage,
      })
      .then(({ rows: packages, total: count }) => {
        if (!isLatest()) return;
        setRows(packages);
        setTotal(count);
      })
      .catch((error) => {
        if (!isLatest()) return;
        console.error('Failed to list VM packages:', error);
        setRows([]);
        setTotal(0);
      })
      .finally(() => {
        if (isLatest()) setLoading(false);
      });
  }, [accountId, resourceId, appliedSearch, pkgType, page, rowsPerPage, beginRequest]);

  useEffect(() => {
    fetchPackages();
  }, [fetchPackages]);

  const onSearchEnter = () => {
    setAppliedSearch(searchInput);
    setPage(0);
  };

  const onSearchClear = () => {
    setSearchInput('');
    setAppliedSearch('');
    setPage(0);
  };

  const tableData = rows.map((pkg) => {
    const cells: any[] = [
      { component: <CellText text={pkg.name} /> },
      // Epoch is part of the identity for rpm, and absent (not zero) for most
      // packages — show it only when the package manager actually reported one.
      { component: <CellText text={pkg.version} subtext={pkg.epoch != null ? `epoch ${pkg.epoch}` : undefined} mono /> },
      { component: <CellText text={pkg.arch} /> },
      { component: <CellText text={pkg.pkg_type} /> },
      { component: <CellText text={pkg.source_name} subtext={pkg.source_version} /> },
    ];
    if (!embedded) {
      cells.push({ component: <CellText text={[pkg.os_family, pkg.os_version].filter(Boolean).join(' ')} /> });
      // A row is one package across every VM that carries it — an embedded table
      // is already scoped to one machine, so it drops the column.
      cells.push({ component: <CellText text={joinVmNames(pkg.resource_ids, vmNames)} /> });
    }
    cells.push({ component: <Datetime value={pkg.last_seen_at} /> });
    return cells;
  });

  const table = (
    <CustomTable
      id={tableId}
      headers={embedded ? EMBEDDED_HEADERS : HEADERS}
      tableData={tableData}
      loading={loading}
      rowsPerPage={rowsPerPage}
      pageNumber={page + 1}
      totalRows={total}
      onPageChange={changePage}
      emptyHeading='No package inventory'
      emptySubHeading='Run a scan on a VM to collect its installed packages.'
      showUpdatedEmptyData={true}
    />
  );

  if (embedded) {
    // Same card chrome as the standalone view — the expanding row supplies the
    // heading (its tab strip), so the card carries no toolbar of its own.
    return (
      <Box sx={{ py: ds.space[2] }}>
        <ListingLayout id={`${tableId}-card`}>
          <ListingLayout.Body>{table}</ListingLayout.Body>
        </ListingLayout>
      </Box>
    );
  }

  return (
    <Box sx={{ px: ds.space[5], pb: ds.space[5] }}>
      <ListingLayout id='vm-packages'>
        <ListingLayout.Toolbar actions={<DownloadButton id={`${tableId}-download`} onClick={() => ({ tableId })} />}>
          <SearchInput
            id='vm-packages-search'
            label='Search By Package Name'
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
          <FilterDropdown
            id='vm-packages-type'
            label='Package Type'
            value={pkgType}
            options={PKG_TYPE_OPTIONS}
            onSelect={(event: any) => {
              setPage(0);
              setPkgType(event?.target?.value || null);
            }}
          />
          {vmOptions.length > 0 && (
            <FilterDropdown
              id='vm-packages-vm'
              label='VM'
              value={vmFilter}
              options={vmOptions}
              onSelect={(event: any) => {
                setPage(0);
                setVmFilter(event?.target?.value || null);
              }}
            />
          )}
        </ListingLayout.Toolbar>
        <ListingLayout.Body>{table}</ListingLayout.Body>
      </ListingLayout>
    </Box>
  );
};

export default VmPackages;
