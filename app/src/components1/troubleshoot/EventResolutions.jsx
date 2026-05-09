import { useEffect, useState } from 'react';
import apiRecommendations from '@api1/recommendation';
import apiUser from '@api1/user';
import apiHome from '@api1/home';
import { Box, Typography } from '@mui/material';
import Link from 'next/link';
import CustomLabels from '@components1/common/widgets/CustomLabels';
import { BoxLayout2, Text } from '@components1/common';
import CustomTable from '@components1/common/tables/CustomTable2';
import Datetime from '@components1/common/format/Datetime';
import SeverityIcon from '@components1/common/widgets/SeverityIcon';
import { containsLink, snakeToTitleCase } from 'src/utils/common';
import { colors } from 'src/utils/colors';
import CloudProviderIcon from '@components1/common/CloudIcon';

const renderAccountGroupIcon = (provider) => <CloudProviderIcon cloud_provider={provider} width='14px' height='14px' />;

const EventResolutions = () => {
  const [totalCount, setTotalCount] = useState(0);
  const [currentPage, setCurrentPage] = useState(0);
  const [rowsPerPage, setRowsPerPage] = useState(apiUser.getUserPreferencesTablePageSize());
  const [data, setData] = useState([]);
  const [loading, setLoading] = useState(false);
  const [accounts, setAccounts] = useState([]);
  const [selectedAccountId, setSelectedAccountId] = useState('');
  const [selectedStatus, setSelectedStatus] = useState('');
  const [selectedType, setSelectedType] = useState('');
  const [selectedResolver, setSelectedResolver] = useState('');

  useEffect(() => {
    apiHome.getCloudAccounts().then((res) => {
      setAccounts(res);
    });
  }, []);

  const getAccountName = (id) => {
    const filteredAcc = accounts.find((ac) => ac.id == id);
    return filteredAcc?.account_name || id || '-';
  };

  const formatResourceChange = (res) => {
    if (!res || typeof res !== 'object') return null;
    const parts = [];
    if (res.oldRequest != null && res.request != null) parts.push(`req: ${res.oldRequest} \u2192 ${res.request}`);
    else if (res.request != null) parts.push(`req: ${res.request}`);
    if (res.oldLimit != null && res.limit != null) parts.push(`lim: ${res.oldLimit} \u2192 ${res.limit}`);
    else if (res.limit != null) parts.push(`lim: ${res.limit}`);
    return parts.length > 0 ? parts.join(', ') : null;
  };

  const getContainerDetails = (nested) => {
    // nested.data is keyed by container name, each having cpu/memory objects
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
      const lines = [];
      if (containerData.cpu) {
        const cpuStr = formatResourceChange(containerData.cpu);
        if (cpuStr) lines.push(`CPU ${cpuStr}`);
      }
      if (containerData.memory) {
        const memStr = formatResourceChange(containerData.memory);
        if (memStr) lines.push(`Mem ${memStr}`);
      }
      if (lines.length > 0) return { containerName, lines };
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
        <Box display='flex' flexDirection='column'>
          <Text value={containerInfo.containerName} sx={{ fontSize: '13px', fontWeight: 500 }} />
          {containerInfo.lines.map((line, i) => (
            <Text key={i} value={line} secondaryText sx={{ fontSize: '12px' }} />
          ))}
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
        accountId: selectedAccountId || undefined,
        status: selectedStatus || undefined,
        type: selectedType || undefined,
        resolverType: selectedResolver || undefined,
      })
      .then((res) => {
        const resolutions = res?.data?.data?.event_resolution || [];
        const count = res?.data?.data?.event_resolution_aggregate?.aggregate?.count || 0;

        const tableData = resolutions.map((item) => {
          const referenceObj = {};
          const typeLabel = item.type ? item.type.replace(/([a-z])([A-Z])/g, '$1 $2') : '';
          if (containsLink(item.type_reference_id)) {
            referenceObj['component'] = (
              <Link onClick={(e) => e.stopPropagation()} href={item.type_reference_id} target='_blank' style={{ fontSize: '14px', fontWeight: 400 }}>
                {typeLabel}
              </Link>
            );
          } else {
            referenceObj['text'] = <Typography sx={{ fontSize: '14px', fontWeight: 400, color: colors.text.secondary }}>{typeLabel}</Typography>;
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
                      <SeverityIcon severityType={item.event.priority} />
                    </Box>
                  )}
                </Box>
              ),
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
                    variant={
                      item.status === 'Success' ? 'green' : item.status === 'Failed' ? 'red' : item.status === 'InProgress' ? 'yellow' : 'grey'
                    }
                  />
                  {item.status === 'Failed' && item.status_message && (
                    <Text value={item.status_message} secondaryText showAutoEllipsis sx={{ fontSize: '12px' }} />
                  )}
                </Box>
              ),
            },
            {
              component: (() => {
                const resolverName = item.resolver_user?.[0]?.display_name || item.data?.provider_config?.name;
                const resolverLink = item.data?.reference_link;
                return (
                  <Box display='flex' flexDirection='column'>
                    <Text value={item.resolver_type ? snakeToTitleCase(item.resolver_type) : '-'} />
                    {resolverName &&
                      (resolverLink ? (
                        <Link href={resolverLink} onClick={(e) => e.stopPropagation()} style={{ fontSize: '12px', fontWeight: 400 }}>
                          {resolverName}
                        </Link>
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

        setTotalCount(count);
        setData(tableData);
      })
      .finally(() => {
        setLoading(false);
      });
  };

  useEffect(() => {
    fetchEventResolutions();
  }, [selectedAccountId, selectedStatus, selectedType, selectedResolver, rowsPerPage, currentPage]);

  const onPageChange = (page, limit) => {
    setCurrentPage(page - 1);
    setRowsPerPage(limit);
  };

  return (
    <BoxLayout2
      filterOptions={[
        {
          type: 'dropdown',
          enabled: true,
          grouped: true,
          groupIcon: renderAccountGroupIcon,
          options: accounts.map((acc) => ({
            label: acc.label || acc.account_name,
            value: acc.id || acc.value,
            group: acc.cloud_provider || 'Other',
          })),
          onSelect: (e) => {
            setSelectedAccountId(e.target.value);
            setCurrentPage(0);
          },
          label: 'Account',
          value: selectedAccountId,
        },
        {
          type: 'dropdown',
          enabled: true,
          options: ['Success', 'Failed', 'InProgress', 'Configuring'].map((s) => ({
            label: s,
            value: s,
          })),
          onSelect: (e) => {
            setSelectedStatus(e.target.value);
            setCurrentPage(0);
          },
          label: 'Status',
          value: selectedStatus,
        },
        {
          type: 'dropdown',
          enabled: true,
          options: ['PullRequest', 'Ticket', 'DeploymentChange'].map((t) => ({
            label: snakeToTitleCase(t),
            value: t,
          })),
          onSelect: (e) => {
            setSelectedType(e.target.value);
            setCurrentPage(0);
          },
          label: 'Type',
          value: selectedType,
        },
        {
          type: 'dropdown',
          enabled: true,
          options: ['AutoPilot', 'Manual', 'System'].map((r) => ({
            label: snakeToTitleCase(r),
            value: r,
          })),
          onSelect: (e) => {
            setSelectedResolver(e.target.value);
            setCurrentPage(0);
          },
          label: 'Resolver',
          value: selectedResolver,
        },
      ]}
      sharingOptions={{
        sharing: {
          enabled: false,
        },
        download: {
          enabled: false,
        },
      }}
    >
      <CustomTable
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
    </BoxLayout2>
  );
};

export default EventResolutions;
