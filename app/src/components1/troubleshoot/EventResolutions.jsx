import { useEffect, useMemo, useState } from 'react';
import { useRouter } from 'next/router';
import apiRecommendations from '@api1/recommendation';
import apiUser from '@api1/user';
import apiHome from '@api1/home';
import { applyFiltersOnRouter } from '@lib/router';
import { Box, Typography } from '@mui/material';
import FileDownloadOutlinedIcon from '@mui/icons-material/FileDownloadOutlined';
import CustomLabels from '@common-new/widgets/CustomLabels';
import CustomLink from '@components1/common/CustomLink';
import { Text } from '@components1/common';
import { snackbar } from '@components1/common/snackbarService';
import CustomTable from '@common-new/tables/CustomTable2';
import Datetime from '@components1/common/format/Datetime';
import SeverityIcon from '@components1/ds/SeverityIcon';
import { Comparison as DsComparison, ComparisonGroup as DsComparisonGroup } from '@components1/ds/Comparison';
import ListingLayout from '@components1/ds/ListingLayout';
import FilterDropdown from '@components1/ds/FilterDropdown';
import { Button as DsButton } from '@components1/ds/Button';
import { containsLink, snakeToTitleCase, toSeverityLevel } from 'src/utils/common';
import { colors, ds } from 'src/utils/colors';
import CloudProviderIcon from '@components1/common/CloudIcon';

const renderAccountGroupIcon = (provider) => <CloudProviderIcon cloud_provider={provider} width='14px' height='14px' />;

const EventResolutions = () => {
  const router = useRouter();
  const [totalCount, setTotalCount] = useState(0);
  const [currentPage, setCurrentPage] = useState(0);
  const [rowsPerPage, setRowsPerPage] = useState(apiUser.getUserPreferencesTablePageSize());
  const [resolutions, setResolutions] = useState([]);
  const [loading, setLoading] = useState(false);
  const [accounts, setAccounts] = useState([]);
  const [selectedAccountId, setSelectedAccountId] = useState(() => {
    const raw = router.query.accountId;
    return raw ? String(raw).split(',').filter(Boolean) : [];
  });

  useEffect(() => {
    const raw = router.query.accountId;
    setSelectedAccountId(raw ? String(raw).split(',').filter(Boolean) : []);
  }, [router.query.accountId]);
  const [selectedStatus, setSelectedStatus] = useState('');
  const [selectedType, setSelectedType] = useState('');
  const [selectedResolver, setSelectedResolver] = useState('');

  const tableId = 'eventResolutionsTable';

  useEffect(() => {
    apiHome.getCloudAccounts().then((res) => {
      setAccounts(res);
    });
  }, []);

  const getAccountName = (id) => {
    const filteredAcc = accounts.find((ac) => ac.id == id);
    return filteredAcc?.account_name || id || '-';
  };

  const collectResourceComparisons = (res, resourceLabel) => {
    if (!res || typeof res !== 'object') return [];
    const items = [];
    if (res.oldRequest != null || res.request != null) {
      items.push({ label: `${resourceLabel} req`, before: res.oldRequest, after: res.request });
    }
    if (res.oldLimit != null || res.limit != null) {
      items.push({ label: `${resourceLabel} lim`, before: res.oldLimit, after: res.limit });
    }
    return items;
  };

  const getContainerDetails = (nested) => {
    // nested is keyed by container name, each having cpu/memory objects
    const containerEntries = Object.entries(nested).filter(
      ([key]) =>
        key !== 'restart' &&
        key !== 'raisePR' &&
        key !== 'size' &&
        key !== 'increase_replicas' &&
        key !== 'imageNameWithTag' &&
        key !== 'imageChangeContainerName' &&
        key !== 'container_name'
    );
    for (const [containerName, containerData] of containerEntries) {
      if (!containerData || typeof containerData !== 'object') continue;
      const comparisons = [
        ...(containerData.cpu ? collectResourceComparisons(containerData.cpu, 'CPU') : []),
        ...(containerData.memory ? collectResourceComparisons(containerData.memory, 'Mem') : []),
      ];
      if (comparisons.length > 0) return { containerName, comparisons };
    }
    return null;
  };

  const getResolutionDetails = (item) => {
    const data = item.data;
    if (!data || typeof data !== 'object') return '-';

    // nested holds action-specific params
    const nested = data.data && typeof data.data === 'object' ? data.data : {};

    // Check for container-level cpu/memory resource changes
    const containerInfo = getContainerDetails(nested);
    if (containerInfo) {
      return (
        <Box display='flex' flexDirection='column' gap='2px'>
          <Text value={containerInfo.containerName} sx={{ fontSize: '13px', fontWeight: 500 }} />
          <DsComparisonGroup spacing='xs'>
            {containerInfo.comparisons.map((c, i) => (
              <DsComparison key={i} label={c.label} size='sm' polarity='neutral' before={{ value: c.before }} after={{ value: c.after }} />
            ))}
          </DsComparisonGroup>
        </Box>
      );
    }

    // PRraiseRequest with change_type
    const changeType = data.change_type;
    if (changeType) {
      const parts = [snakeToTitleCase(changeType)];
      if (nested.replica_count) parts.push(`replicas: ${nested.replica_count}`);
      return parts.join(' - ');
    }

    // Other action types
    if (nested.restart) return `Pod Restart${nested.container_name ? ` (${nested.container_name})` : ''}`;
    if (nested.raisePR) return `Raise PR${data.provider ? ` via ${data.provider}` : ''}`;
    if (nested.size) return `PVC Resize: ${nested.size}`;
    if (nested.increase_replicas) return `Scale Replicas: ${nested.increase_replicas}`;
    if (nested.imageNameWithTag) return `Image Update: ${nested.imageNameWithTag}`;

    if (data.provider) return snakeToTitleCase(data.provider);
    return '-';
  };

  const fetchEventResolutions = () => {
    setLoading(true);
    apiRecommendations
      .listAllEventResolutions({
        limit: Math.min(rowsPerPage, 100),
        offset: Math.min(rowsPerPage, 100) * currentPage,
        accountId: selectedAccountId.length ? selectedAccountId : undefined,
        status: selectedStatus || undefined,
        type: selectedType || undefined,
        resolverType: selectedResolver || undefined,
      })
      .then((res) => {
        const resolutions = res?.data?.data?.event_resolution || [];
        const count = res?.data?.data?.event_resolution_aggregate?.aggregate?.count || 0;

        setTotalCount(count);
        setResolutions(resolutions);
      })
      .finally(() => {
        setLoading(false);
      });
  };

  useEffect(() => {
    fetchEventResolutions();
  }, [selectedAccountId, selectedStatus, selectedType, selectedResolver, rowsPerPage, currentPage]);

  const accountsKey = accounts.map((a) => a.id || a.value).join(',');

  const data = useMemo(() => {
    return resolutions.map((item) => {
      const referenceObj = {};
      const typeLabel = item.type ? item.type.replace(/([a-z])([A-Z])/g, '$1 $2') : '';
      if (containsLink(item.type_reference_id)) {
        referenceObj['component'] = (
          <CustomLink href={item.type_reference_id} openInNew style={{ fontSize: '14px' }}>
            {typeLabel}
          </CustomLink>
        );
        referenceObj['data'] = item.type_reference_id;
      } else {
        referenceObj['text'] = <Typography sx={{ fontSize: '14px', fontWeight: 400, color: colors.text.secondary }}>{typeLabel}</Typography>;
        referenceObj['data'] = typeLabel;
      }

      return [
        {
          component: (
            <Box display='flex' flexDirection='column'>
              <Text value={item.event?.subject_name || '-'} showAutoEllipsis />
              {item.event?.subject_namespace && <Text value={`ns: ${item.event.subject_namespace}`} secondaryText />}
              {item.event?.cloud_account_id && <Text value={`acc: ${getAccountName(item.event.cloud_account_id)}`} secondaryText />}
            </Box>
          ),
        },
        {
          component: (
            <Box display='flex' alignItems='center' gap='6px'>
              {item.event?.priority && (
                <Box sx={{ display: 'flex', alignItems: 'center' }}>
                  <SeverityIcon level={toSeverityLevel(item.event.priority)} aria-label={`${item.event.priority || 'unknown'}`} />
                </Box>
              )}
            </Box>
          ),
          data: item.event?.priority || '',
        },
        referenceObj,
        {
          component: (() => {
            const details = getResolutionDetails(item);
            if (typeof details === 'string') return <Text value={details} showAutoEllipsis sx={{ fontSize: '13px' }} />;
            return details || <Text value='-' />;
          })(),
        },
        {
          component: (
            <Box display='flex' flexDirection='column' gap='4px'>
              <CustomLabels
                margin='0'
                text={item.status}
                variant={item.status === 'Success' ? 'green' : item.status === 'Failed' ? 'red' : item.status === 'InProgress' ? 'yellow' : 'grey'}
              />
              {item.status === 'Failed' && item.status_message && (
                <Text value={item.status_message} secondaryText showAutoEllipsis sx={{ fontSize: '12px' }} />
              )}
            </Box>
          ),
        },
        {
          component: (() => {
            const resolverName = item.resolver_user?.display_name || item.data?.provider_config?.name;
            const resolverLink = item.data?.reference_link;
            return (
              <Box display='flex' flexDirection='column'>
                <Text value={item.resolver_type ? snakeToTitleCase(item.resolver_type) : '-'} />
                {resolverName &&
                  (resolverLink ? (
                    <CustomLink href={resolverLink} style={{ fontSize: '12px' }}>
                      {resolverName}
                    </CustomLink>
                  ) : (
                    <Text value={resolverName} secondaryText />
                  ))}
              </Box>
            );
          })(),
        },
        {
          component: <Datetime value={item.updated_at} />,
        },
      ];
    });
  }, [resolutions, accountsKey]);

  const onPageChange = (page, limit) => {
    setCurrentPage(page - 1);
    setRowsPerPage(limit);
  };

  // DOM-scrape CSV download — mirrors KubernetesTable2's `data-export-enabled` /
  // `data-export-data` contract, the same way other ListingLayout consumers do it.
  const handleDownloadCsv = () => {
    const oTable = document.getElementById(tableId);
    if (!oTable) {
      snackbar.error('Nothing to export — table not ready.');
      return;
    }
    const escape = (s) => `"${(s == null ? '' : String(s)).replace(/"/g, '""').replace(/[\r\n]+/g, ' ')}"`;
    let csv = '';
    const headerRows = oTable.querySelectorAll('thead tr');
    const headerRow = headerRows?.[headerRows.length - 1];
    if (headerRow) {
      csv +=
        [...headerRow.children]
          .filter((th) => th.getAttribute('data-export-enabled') !== 'false')
          .map((th) => escape(th.innerText))
          .join(',') + '\r\n';
    }
    const bodyRows = oTable.querySelectorAll('tbody tr') || [];
    for (const tr of Array.from(bodyRows)) {
      const cells = [...tr.children].filter((td) => td.getAttribute('data-export-enabled') === 'true');
      if (cells.length === 0) continue;
      csv += cells.map((td) => escape(td.getAttribute('data-export-data') ?? td.innerText)).join(',') + '\r\n';
    }
    const blob = new Blob([csv], { type: 'text/csv;charset=utf-8' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = `${tableId}.csv`;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    URL.revokeObjectURL(url);
  };

  const filterDropdowns = [
    {
      type: 'multi-dropdown',
      grouped: true,
      groupIcon: renderAccountGroupIcon,
      options: accounts.map((acc) => ({
        label: acc.label || acc.account_name,
        value: acc.id || acc.value,
        group: acc.cloud_provider || 'Other',
      })),
      onSelect: (_e, value) => {
        const ids = (value || []).map((v) => v.value);
        setSelectedAccountId(ids);
        setCurrentPage(0);
        applyFiltersOnRouter(router, { accountId: ids.join(',') });
      },
      label: 'Account',
      value: accounts
        .filter((acc) => selectedAccountId.includes(acc.id || acc.value))
        .map((acc) => ({
          label: acc.label || acc.account_name,
          value: acc.id || acc.value,
          group: acc.cloud_provider || 'Other',
        })),
    },
    {
      type: 'dropdown',
      options: ['Success', 'Failed', 'InProgress', 'Configuring'].map((s) => ({ label: s, value: s })),
      onSelect: (e) => {
        setSelectedStatus(e.target.value);
        setCurrentPage(0);
      },
      label: 'Status',
      value: selectedStatus,
    },
    {
      type: 'dropdown',
      options: ['PullRequest', 'Ticket', 'DeploymentChange'].map((t) => ({ label: snakeToTitleCase(t), value: t })),
      onSelect: (e) => {
        setSelectedType(e.target.value);
        setCurrentPage(0);
      },
      label: 'Type',
      value: selectedType,
    },
    {
      type: 'dropdown',
      options: ['AutoPilot', 'Manual', 'System'].map((r) => ({ label: snakeToTitleCase(r), value: r })),
      onSelect: (e) => {
        setSelectedResolver(e.target.value);
        setCurrentPage(0);
      },
      label: 'Resolver',
      value: selectedResolver,
    },
  ];

  return (
    <ListingLayout id='event-resolutions'>
      <ListingLayout.Toolbar
        actions={
          <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2] }}>
            <DsButton
              tone='secondary'
              size='sm'
              composition='icon-only'
              icon={<FileDownloadOutlinedIcon />}
              aria-label='Download event resolutions as CSV'
              tooltip='Download as CSV'
              id='event-resolutions-download'
              onClick={handleDownloadCsv}
            />
          </Box>
        }
      >
        {filterDropdowns.map((opt, idx) => (
          <FilterDropdown
            key={`${opt.label || 'filter'}-${idx}`}
            id={`filter-${opt.label.toString().replace(/\s+/g, '-').toLowerCase()}`}
            label={opt.label}
            multiple={opt.type === 'multi-dropdown'}
            grouped={!!opt.grouped}
            groupIcon={opt.groupIcon}
            options={opt.options || []}
            value={opt.value}
            onSelect={opt.onSelect}
          />
        ))}
      </ListingLayout.Toolbar>
      <ListingLayout.Body>
        <CustomTable
          id={tableId}
          tableData={data}
          headers={[
            { name: 'Event Subject', width: '16%' },
            { name: 'Severity', width: '10%' },
            { name: 'Resolution', width: '10%' },
            { name: 'Resolution Details', width: '16%' },
            { name: 'Status', width: '14%' },
            { name: 'Resolver', width: '8%' },
            { name: 'Updated', width: '10%' },
          ]}
          rowsPerPage={rowsPerPage}
          totalRows={totalCount}
          onPageChange={onPageChange}
          pageNumber={currentPage + 1}
          loading={loading}
        />
      </ListingLayout.Body>
    </ListingLayout>
  );
};

export default EventResolutions;
