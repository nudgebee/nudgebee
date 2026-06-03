import { Box, LinearProgress, Typography } from '@mui/material';
import Tooltip from '@components1/ds/Tooltip';
import React, { useCallback, useEffect, useState } from 'react';
import MoreVertIcon from '@mui/icons-material/MoreVert';
import apiCloudAccount from '@api1/cloud-account';
import CloudAccountTable from '@components1/cloudaccount/CloudAccountTable';
import { Label } from '@components1/ds/Label';
import { OptimizeSummary } from './Summary';
import Datetime from '@common-new/format/Datetime';
import CloudAccountEvents from '@components1/cloudaccount/CloudAccountEvents';
import { MENU_ITEMS, DataBlock, CustomText } from '@components1/cloudaccount/common';
import { usePagination } from '@hooks/usePagination';
import TagsCell from '@components1/cloudaccount/TagsCell';
import type { ICustomTable2Row } from '../ec2/Instances';
import { buildStateApiParams, getStateDropdownOptions } from '@components1/cloudaccount/stateFilter';
import { ListingLayout } from '@components1/ds/ListingLayout';
import FilterDropdown from '@components1/ds/FilterDropdown';
import CustomSearch from '@common-new/CustomSearch';
import { Button as DsButton } from '@components1/ds/Button';
import { DropdownMenu as DsDropdownMenu } from '@components1/ds/DropdownMenu';
import DownloadButton from '@common-new/DownloadButton';
import { ds } from '@utils/colors';

const CF_INSTANCE_HEADER = ['App Name', 'Org / Space', 'Status', 'Instances', 'Memory', 'Disk', 'Buildpack', 'Created At', ''];

// ─── Helper functions ───

const getStateFromStatus = (status: string) => {
  if (status === 'Active') {
    return 'STARTED';
  }
  if (status === 'Inactive') {
    return 'STOPPED';
  }
  return status || '-';
};

const formatUptime = (seconds: number): string => {
  if (!seconds || seconds <= 0) {
    return '-';
  }
  const days = Math.floor(seconds / 86400);
  const hours = Math.floor((seconds % 86400) / 3600);
  const mins = Math.floor((seconds % 3600) / 60);
  const secs = Math.floor(seconds % 60);
  const parts: string[] = [];
  if (days > 0) {
    parts.push(`${days}d`);
  }
  if (hours > 0) {
    parts.push(`${hours}h`);
  }
  if (mins > 0) {
    parts.push(`${mins}m`);
  }
  if (secs > 0 || parts.length === 0) {
    parts.push(`${secs}s`);
  }
  return parts.join(' ');
};

const formatBytes = (bytes: number): string => {
  if (!bytes || bytes <= 0) {
    return '0 MB';
  }
  const mb = bytes / (1024 * 1024);
  if (mb >= 1024) {
    return `${(mb / 1024).toFixed(1)} GB`;
  }
  return `${Math.round(mb)} MB`;
};

const getInstanceStateColor = (state: string): string => {
  if (state === 'RUNNING') {
    return 'green';
  }
  if (state === 'CRASHED' || state === 'DOWN') {
    return 'red';
  }
  if (state === 'STARTING') {
    return 'orange';
  }
  return '';
};

/** Compute running/total instance counts from instance_stats */
const getInstanceCounts = (item: any): { running: number; total: number } | null => {
  const stats: any[] = item.meta?.instance_stats;
  if (!stats || stats.length === 0) {
    return null;
  }
  const running = stats.filter((s: any) => s.state === 'RUNNING').length;
  return { running, total: stats.length };
};

/** Compute average uptime across running instances */
const getAverageUptime = (item: any): number => {
  const stats: any[] = item.meta?.instance_stats;
  if (!stats || stats.length === 0) {
    return 0;
  }
  const running = stats.filter((s: any) => s.state === 'RUNNING' && s.uptime > 0);
  if (running.length === 0) {
    return 0;
  }
  return running.reduce((sum: number, s: any) => sum + s.uptime, 0) / running.length;
};

/** Compute total memory usage across all instances */
const getTotalMemUsage = (item: any): { used: number; quota: number } | null => {
  const stats: any[] = item.meta?.instance_stats;
  if (!stats || stats.length === 0) {
    return null;
  }
  const used = stats.reduce((sum: number, s: any) => sum + (s.mem || 0), 0);
  const quota = stats.reduce((sum: number, s: any) => sum + (s.mem_quota || 0), 0);
  return { used, quota };
};

/** Compute total disk usage across all instances */
const getTotalDiskUsage = (item: any): { used: number; quota: number } | null => {
  const stats: any[] = item.meta?.instance_stats;
  if (!stats || stats.length === 0) {
    return null;
  }
  const used = stats.reduce((sum: number, s: any) => sum + (s.disk || 0), 0);
  const quota = stats.reduce((sum: number, s: any) => sum + (s.disk_quota || 0), 0);
  return { used, quota };
};

/** Status display with colored dot and "Deployed - Online" style */
const getStatusDotColor = (state: string): string => {
  if (state === 'STARTED') {
    return ds.green[500];
  }
  if (state === 'STOPPED') {
    return ds.red[500];
  }
  return ds.gray[400];
};

const getStatusLabel = (state: string): string => {
  if (state === 'STARTED') {
    return 'Deployed - Online';
  }
  if (state === 'STOPPED') {
    return 'Stopped';
  }
  return state || '-';
};

const AppStatusCell = ({ state }: { state: string }) => {
  const dotColor = getStatusDotColor(state);
  const label = getStatusLabel(state);
  return (
    <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2] }}>
      <Box sx={{ width: 8, height: 8, borderRadius: '50%', backgroundColor: dotColor, flexShrink: 0 }} />
      <Typography sx={{ fontSize: ds.text.body, color: ds.gray[700], fontWeight: ds.weight.regular }}>{label}</Typography>
    </Box>
  );
};

/** Compact usage bar for table columns */
const CompactUsageBar = ({ used, quota }: { used: number; quota: number }) => {
  const pct = quota > 0 ? Math.min((used / quota) * 100, 100) : 0;
  const barColor = pct > 80 ? ds.amber[500] : ds.blue[500];
  return (
    <Tooltip title={`${formatBytes(used)} / ${formatBytes(quota)} (${pct.toFixed(1)}%)`} arrow>
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[1], minWidth: ds.space.mul(0, 40) }}>
        <LinearProgress
          variant='determinate'
          value={pct}
          sx={{
            height: ds.space.mul(0, 3),
            borderRadius: ds.radius.lg,
            backgroundColor: ds.gray[200],
            '& .MuiLinearProgress-bar': { backgroundColor: barColor, borderRadius: ds.radius.lg },
          }}
        />
        <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500], whiteSpace: 'nowrap' }}>
          {formatBytes(used)} / {formatBytes(quota)}
        </Typography>
      </Box>
    </Tooltip>
  );
};

/** Full-size usage bar for instance stats panel */
const UsageBar = ({ used, quota, color }: { used: number; quota: number; color: string }) => {
  const pct = quota > 0 ? Math.min((used / quota) * 100, 100) : 0;
  return (
    <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2], minWidth: ds.space.mul(0, 90) }}>
      <LinearProgress
        variant='determinate'
        value={pct}
        sx={{
          flex: 1,
          height: ds.space[2],
          borderRadius: ds.radius.xl,
          backgroundColor: ds.gray[200],
          '& .MuiLinearProgress-bar': { backgroundColor: color, borderRadius: ds.radius.xl },
        }}
      />
      <Typography sx={{ fontSize: ds.text.small, color: ds.gray[700], whiteSpace: 'nowrap' }}>
        {formatBytes(used)} / {formatBytes(quota)}
      </Typography>
    </Box>
  );
};

/** Instance count cell: "running / total" with color indicator */
const InstanceCountCell = ({ item }: { item: any }) => {
  const counts = getInstanceCounts(item);
  const allocated = item.meta?.instances || item.meta?.instance_count;

  if (counts) {
    // We have live instance_stats — show running/total
    const allHealthy = counts.running === counts.total;
    let color: string = ds.red[500];
    if (allHealthy) {
      color = ds.green[500];
    } else if (counts.running > 0) {
      color = ds.amber[500];
    }
    return (
      <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2] }}>
        <Box sx={{ width: 6, height: 6, borderRadius: '50%', backgroundColor: color, flexShrink: 0 }} />
        <Typography sx={{ fontSize: ds.text.body, color: ds.gray[700], fontWeight: ds.weight.medium }}>
          {counts.running} / {counts.total}
        </Typography>
      </Box>
    );
  }

  // Fallback: show allocated count from meta
  if (allocated) {
    return <CustomText text1={String(allocated)} />;
  }
  return <CustomText text1='-' />;
};

// ─── Expandable panel: Details ───

const CFAppDetails = ({ drilldownQuery }: { drilldownQuery: any }) => {
  const meta = drilldownQuery?.meta || {};
  const tags = drilldownQuery?.tags || {};
  const org = Array.isArray(tags.org) ? tags.org[0] : tags.org;
  const space = Array.isArray(tags.space) ? tags.space[0] : tags.space;
  const instanceCounts = getInstanceCounts(drilldownQuery);
  const avgUptime = getAverageUptime(drilldownQuery);
  const statusDotColor = getStatusDotColor(meta.state || '');
  const statusLabel = getStatusLabel(meta.state || '');

  const getInstancesDisplay = (): string => {
    if (instanceCounts) {
      return `${instanceCounts.running} / ${instanceCounts.total}`;
    }
    if (meta.instances) {
      return String(meta.instances);
    }
    return '-';
  };

  return (
    <Box sx={{ backgroundColor: ds.background[100], borderRadius: ds.radius.md, p: ds.space[5] }}>
      {/* Top summary cards (like Stratos header) */}
      <Box sx={{ display: 'flex', gap: ds.space[3], mb: ds.space[5] }}>
        <Box sx={{ flex: 1, p: ds.space[3], backgroundColor: ds.background[200], borderRadius: ds.radius.md, border: `1px solid ${ds.gray[200]}` }}>
          <Typography
            sx={{ fontSize: ds.text.caption, color: ds.gray[600], fontWeight: ds.weight.semibold, mb: ds.space[1], textTransform: 'uppercase' }}
          >
            Status
          </Typography>
          <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2] }}>
            <Box
              sx={{
                width: ds.space.mul(0, 5),
                height: ds.space.mul(0, 5),
                borderRadius: ds.radius.pill,
                backgroundColor: statusDotColor,
              }}
            />
            <Typography sx={{ fontSize: ds.text.bodyLg, fontWeight: ds.weight.semibold, color: ds.gray[700] }}>{statusLabel}</Typography>
          </Box>
        </Box>
        <Box sx={{ flex: 1, p: ds.space[3], backgroundColor: ds.background[200], borderRadius: ds.radius.md, border: `1px solid ${ds.gray[200]}` }}>
          <Typography
            sx={{ fontSize: ds.text.caption, color: ds.gray[600], fontWeight: ds.weight.semibold, mb: ds.space[1], textTransform: 'uppercase' }}
          >
            Instances
          </Typography>
          <Typography sx={{ fontSize: ds.text.bodyLg, fontWeight: ds.weight.semibold, color: ds.gray[700] }}>{getInstancesDisplay()}</Typography>
        </Box>
        <Box sx={{ flex: 1, p: ds.space[3], backgroundColor: ds.background[200], borderRadius: ds.radius.md, border: `1px solid ${ds.gray[200]}` }}>
          <Typography
            sx={{ fontSize: ds.text.caption, color: ds.gray[600], fontWeight: ds.weight.semibold, mb: ds.space[1], textTransform: 'uppercase' }}
          >
            Uptime
          </Typography>
          <Typography sx={{ fontSize: ds.text.bodyLg, fontWeight: ds.weight.semibold, color: ds.gray[700] }}>
            {avgUptime > 0 ? formatUptime(avgUptime) : '-'}
          </Typography>
          {avgUptime > 0 && <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[400] }}>Average across instances</Typography>}
        </Box>
      </Box>

      {/* Application Info section */}
      <Typography
        sx={{
          fontSize: ds.text.body,
          fontWeight: ds.weight.semibold,
          color: ds.gray[700],
          mb: ds.space[3],
          borderBottom: `1px solid ${ds.gray[200]}`,
          pb: ds.space[2],
        }}
      >
        Application Info
      </Typography>
      <Box sx={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', columnGap: ds.space[4], rowGap: ds.space[4], mb: ds.space[5] }}>
        {meta.memory_in_mb !== null && meta.memory_in_mb !== undefined && <DataBlock title='Memory Quota' data={`${meta.memory_in_mb} MB`} />}
        {meta.disk_in_mb !== null && meta.disk_in_mb !== undefined && <DataBlock title='Disk Quota' data={`${meta.disk_in_mb} MB`} />}
        {meta.state && <DataBlock title='App State' data={meta.state} />}
        {drilldownQuery.resourse_created_on && <DataBlock title='Created' data={new Date(drilldownQuery.resourse_created_on).toLocaleString()} />}
        {meta.updated_at && <DataBlock title='Modified' data={new Date(meta.updated_at).toLocaleString()} />}
        {meta.instances !== null && meta.instances !== undefined && <DataBlock title='Allocated Instances' data={String(meta.instances)} />}
      </Box>

      {/* Build Info section */}
      <Typography
        sx={{
          fontSize: ds.text.body,
          fontWeight: ds.weight.semibold,
          color: ds.gray[700],
          mb: ds.space[3],
          borderBottom: `1px solid ${ds.gray[200]}`,
          pb: ds.space[2],
        }}
      >
        Build Info
      </Typography>
      <Box sx={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', columnGap: ds.space[4], rowGap: ds.space[4], mb: ds.space[5] }}>
        {meta.lifecycle_type && <DataBlock title='Lifecycle Type' data={meta.lifecycle_type} />}
        {meta.stack && <DataBlock title='Stack' data={meta.stack} />}
        {(meta.buildpacks || meta.lifecycle_buildpacks) && (meta.buildpacks || meta.lifecycle_buildpacks).length > 0 && (
          <DataBlock title='Buildpacks' data={(meta.buildpacks || meta.lifecycle_buildpacks).join(', ')} />
        )}
      </Box>

      {/* Health Check section */}
      {meta.health_check_type && (
        <>
          <Typography
            sx={{
              fontSize: ds.text.body,
              fontWeight: ds.weight.semibold,
              color: ds.gray[700],
              mb: ds.space[3],
              borderBottom: `1px solid ${ds.gray[200]}`,
              pb: ds.space[2],
            }}
          >
            Health Check Configuration
          </Typography>
          <Box sx={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', columnGap: ds.space[4], rowGap: ds.space[4], mb: ds.space[5] }}>
            <DataBlock title='Health Check Type' data={meta.health_check_type} />
            {meta.health_check_timeout !== null && meta.health_check_timeout !== undefined && (
              <DataBlock title='Health Check Timeout' data={String(meta.health_check_timeout)} />
            )}
            {meta.health_check_endpoint && <DataBlock title='Health Check Endpoint' data={meta.health_check_endpoint} />}
          </Box>
        </>
      )}

      {/* Additional Info section */}
      <Typography
        sx={{
          fontSize: ds.text.body,
          fontWeight: ds.weight.semibold,
          color: ds.gray[700],
          mb: ds.space[3],
          borderBottom: `1px solid ${ds.gray[200]}`,
          pb: ds.space[2],
        }}
      >
        Additional Info
      </Typography>
      <Box sx={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', columnGap: ds.space[4], rowGap: ds.space[4], mb: ds.space[5] }}>
        {drilldownQuery.resourse_id && <DataBlock title='Application GUID' data={drilldownQuery.resourse_id} />}
        {org && <DataBlock title='Organization' data={org} />}
        {space && <DataBlock title='Space' data={space} />}
        {meta.command && <DataBlock title='Command' data={meta.command} />}
        {drilldownQuery.arn && <DataBlock title='CF URI' data={drilldownQuery.arn} />}
      </Box>

      {/* Tags */}
      {tags && Object.keys(tags).length > 0 && (
        <Box>
          <Typography fontSize={ds.text.small} fontWeight={ds.weight.semibold} color={ds.gray[600]} mb={ds.space[1]}>
            Tags
          </Typography>
          <TagsCell tags={tags} />
        </Box>
      )}
    </Box>
  );
};

// ─── Expandable panel: Instance Stats ───

const CFInstanceStats = ({ drilldownQuery }: { drilldownQuery: any }) => {
  const instanceStats: any[] = drilldownQuery?.meta?.instance_stats || [];

  if (instanceStats.length === 0) {
    return (
      <Box sx={{ p: ds.space[5], backgroundColor: ds.background[100], borderRadius: ds.radius.md }}>
        <Typography sx={{ color: ds.gray[600], fontSize: ds.text.body }}>
          No instance data available. Instance stats are collected during the next sync cycle.
        </Typography>
      </Box>
    );
  }

  return (
    <Box sx={{ backgroundColor: ds.background[100], borderRadius: ds.radius.md, p: ds.space[4] }}>
      {/* Summary cards */}
      <Box sx={{ display: 'flex', gap: ds.space[4], mb: ds.space[4] }}>
        <Box sx={{ flex: 1, p: ds.space[3], backgroundColor: ds.background[200], borderRadius: ds.radius.md, border: `1px solid ${ds.gray[200]}` }}>
          <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[600], fontWeight: ds.weight.semibold, mb: ds.space[1] }}>STATUS</Typography>
          <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2] }}>
            <Box sx={{ width: ds.space[2], height: ds.space[2], borderRadius: ds.radius.pill, backgroundColor: ds.green[500] }} />
            <Typography sx={{ fontSize: ds.text.bodyLg, fontWeight: ds.weight.semibold, color: ds.gray[700] }}>
              {drilldownQuery?.meta?.state === 'STARTED' ? 'Deployed - Online' : drilldownQuery?.meta?.state || '-'}
            </Typography>
          </Box>
        </Box>
        <Box sx={{ flex: 1, p: ds.space[3], backgroundColor: ds.background[200], borderRadius: ds.radius.md, border: `1px solid ${ds.gray[200]}` }}>
          <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[600], fontWeight: ds.weight.semibold, mb: ds.space[1] }}>INSTANCES</Typography>
          <Typography sx={{ fontSize: ds.text.bodyLg, fontWeight: ds.weight.semibold, color: ds.gray[700] }}>
            {instanceStats.filter((i: any) => i.state === 'RUNNING').length} / {instanceStats.length}
          </Typography>
        </Box>
        <Box sx={{ flex: 1, p: ds.space[3], backgroundColor: ds.background[200], borderRadius: ds.radius.md, border: `1px solid ${ds.gray[200]}` }}>
          <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[600], fontWeight: ds.weight.semibold, mb: ds.space[1] }}>
            MEMORY QUOTA
          </Typography>
          <Typography sx={{ fontSize: ds.text.bodyLg, fontWeight: ds.weight.semibold, color: ds.gray[700] }}>
            {drilldownQuery?.meta?.memory_in_mb ? `${drilldownQuery.meta.memory_in_mb} MB` : '-'}
          </Typography>
        </Box>
        <Box sx={{ flex: 1, p: ds.space[3], backgroundColor: ds.background[200], borderRadius: ds.radius.md, border: `1px solid ${ds.gray[200]}` }}>
          <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[600], fontWeight: ds.weight.semibold, mb: ds.space[1] }}>DISK QUOTA</Typography>
          <Typography sx={{ fontSize: ds.text.bodyLg, fontWeight: ds.weight.semibold, color: ds.gray[700] }}>
            {drilldownQuery?.meta?.disk_in_mb ? `${drilldownQuery.meta.disk_in_mb} MB` : '-'}
          </Typography>
        </Box>
      </Box>

      {/* Instance table */}
      <Box sx={{ border: `1px solid ${ds.gray[200]}`, borderRadius: ds.radius.md, overflow: 'hidden' }}>
        {/* Header */}
        <Box
          sx={{
            display: 'grid',
            gridTemplateColumns: '60px 90px 1fr 1fr 80px 120px',
            gap: ds.space[2],
            p: 'var(--ds-space-2) var(--ds-space-4)',
            backgroundColor: ds.background[200],
            borderBottom: `1px solid ${ds.gray[200]}`,
          }}
        >
          {['Index', 'State', 'Memory', 'Disk', 'CPU', 'Uptime'].map((h) => (
            <Typography key={h} sx={{ fontSize: ds.text.caption, fontWeight: ds.weight.semibold, color: ds.gray[600], textTransform: 'uppercase' }}>
              {h}
            </Typography>
          ))}
        </Box>
        {/* Rows */}
        {instanceStats.map((inst: any, idx: number) => {
          const memColor = inst.mem_quota > 0 && inst.mem / inst.mem_quota > 0.8 ? ds.amber[500] : ds.blue[500];
          const diskColor = inst.disk_quota > 0 && inst.disk / inst.disk_quota > 0.8 ? ds.amber[500] : ds.blue[500];
          return (
            <Box
              key={inst.index ?? idx}
              sx={{
                display: 'grid',
                gridTemplateColumns: '60px 90px 1fr 1fr 80px 120px',
                gap: ds.space[2],
                p: 'var(--ds-space-2) var(--ds-space-4)',
                alignItems: 'center',
                borderBottom: idx < instanceStats.length - 1 ? `1px solid ${ds.gray[100]}` : 'none',
                '&:hover': { backgroundColor: ds.background[200] },
              }}
            >
              <Typography sx={{ fontSize: ds.text.body, color: ds.gray[700], fontWeight: ds.weight.medium }}>{inst.index ?? idx}</Typography>
              <Label variant={getInstanceStateColor(inst.state)} text={inst.state || '-'} />
              <UsageBar used={inst.mem || 0} quota={inst.mem_quota || 0} color={memColor} />
              <UsageBar used={inst.disk || 0} quota={inst.disk_quota || 0} color={diskColor} />
              <Typography sx={{ fontSize: ds.text.body, color: ds.gray[700] }}>
                {typeof inst.cpu === 'number' ? `${inst.cpu.toFixed(2)}%` : '-'}
              </Typography>
              <Typography sx={{ fontSize: ds.text.body, color: ds.gray[700] }}>{formatUptime(inst.uptime)}</Typography>
            </Box>
          );
        })}
      </Box>
    </Box>
  );
};

// ─── Render functions for expandable tabs ───

const renderCFAppDetails = (_opt: any, drilldownQuery: any) => <CFAppDetails drilldownQuery={drilldownQuery} />;

const renderCFInstanceStats = (_opt: any, drilldownQuery: any) => <CFInstanceStats drilldownQuery={drilldownQuery} />;

const createMonitoringComponentFn = (accountId: string, serviceName: string) => (_opt: any, drilldownQuery: any) =>
  <OptimizeSummary accountId={accountId} serviceName={serviceName} resourceId={drilldownQuery?.resourse_id || ''} showSummary={false} />;

const createEventsComponentFn = (accountId: string, serviceName: string) => (_opt: any, drilldownQuery: any) =>
  <CloudAccountEvents accountId={accountId} serviceName={serviceName} subjectName={drilldownQuery?.name || ''} />;

// ─── Main component ───

const CFInstancesView = (props: {
  accountId: string | undefined;
  heading?: string;
  serviceName: string;
  stickyColumnIndex?: any;
  tableHeadingCenter?: any;
}) => {
  const [instances, setInstances] = useState([]);
  const [instancesCount, setInstancesCount] = useState(0);
  const [loading, setLoading] = useState(false);
  // Typing state + applied state per ManualInvestigated.jsx — fetch fires only
  // on Enter or Clear, not on every keystroke.
  const [selectedInstanceIdName, setSelectedInstanceIdName] = useState('');
  const [appliedInstanceIdName, setAppliedInstanceIdName] = useState('');
  const [selectedTagKey, setSelectedTagKey] = useState<string | null>(null);
  const [selectedTagValue, setSelectedTagValue] = useState<string | null>(null);
  const [availableTagKeys, setAvailableTagKeys] = useState<{ label: string; value: string }[]>([]);
  const [availableTagValues, setAvailableTagValues] = useState<{ label: string; value: string }[]>([]);
  const [selectedState, setSelectedState] = useState<string>('all');
  const stateOptions = getStateDropdownOptions(props?.serviceName);

  const { page, rowsPerPage, changePage, setPage } = usePagination(10);
  const cfInstancesTable = 'cfInstancesTable';

  const onSearchEnter = () => {
    setAppliedInstanceIdName(selectedInstanceIdName);
    setPage(0);
  };

  const onSearchClear = () => {
    setSelectedInstanceIdName('');
    setAppliedInstanceIdName('');
    setPage(0);
  };

  const onMenuClick = (_menuItem: { id: number }, _data: any) => undefined;

  useEffect(() => {
    if (props?.accountId) {
      apiCloudAccount.getDistinctTagKeys(props.accountId, props?.serviceName, []).then(setAvailableTagKeys);
    }
  }, [props?.accountId, props?.serviceName]);

  useEffect(() => {
    if (props?.accountId && selectedTagKey) {
      apiCloudAccount.getDistinctTagValues(props.accountId, selectedTagKey, props?.serviceName, []).then(setAvailableTagValues);
    } else {
      setAvailableTagValues([]);
    }
  }, [props?.accountId, props?.serviceName, selectedTagKey]);

  const listCFInstances = useCallback(() => {
    if (!props?.accountId) {
      return;
    }
    setLoading(true);
    apiCloudAccount
      .getCloudResource(
        {
          account_id: props?.accountId,
          serviceName: props?.serviceName,
          type: [],
          metric: ['cpu_usage', 'memory_usage_bytes', 'disk_usage_bytes', 'instance_count'],
          nameFilter: appliedInstanceIdName,
          tagFilterKey: selectedTagKey || undefined,
          tagFilterValue: selectedTagValue || undefined,
          ...buildStateApiParams(props?.serviceName, selectedState),
        },
        rowsPerPage,
        page * rowsPerPage
      )
      .then((res: any) => {
        setLoading(false);
        const resourceCount = res.data?.data?.cloud_resourses_aggregate?.aggregate?.count || 0;
        const resourceData = res.data?.data?.cloud_resourses?.map((rawItem: any) => {
          // cloud_resources_list_v2 returns meta/tags as JSON strings — parse them
          const item = {
            ...rawItem,
            meta: typeof rawItem.meta === 'string' ? JSON.parse(rawItem.meta || '{}') : rawItem.meta || {},
            tags: typeof rawItem.tags === 'string' ? JSON.parse(rawItem.tags || '{}') : rawItem.tags || {},
          };
          const data: ICustomTable2Row[] = [];

          // App state from meta or status
          const state = item?.meta?.state || getStateFromStatus(item?.status);

          // App Name
          data.push({
            component: <CustomText text1={item.name || item.resourse_id || '-'} subtext1={item.resourse_id} />,
            drilldownQuery: item,
          });

          // Org / Space
          const orgTag = item.tags?.org;
          const org = Array.isArray(orgTag) ? orgTag[0] : orgTag || item.region || '-';
          const spaceTag = item.tags?.space;
          const space = Array.isArray(spaceTag) ? spaceTag[0] : spaceTag || '-';
          data.push({
            component: <CustomText text1={org} subtext1={space} />,
          });

          // Status (Deployed - Online style)
          data.push({
            component: <AppStatusCell state={state} />,
          });

          // Instances (running/total with live data, or allocated count)
          data.push({
            component: <InstanceCountCell item={item} />,
          });

          // Memory (usage bar if live stats available, else quota)
          const memUsage = getTotalMemUsage(item);
          const memoryMB = item.meta?.memory_in_mb;
          data.push({
            component:
              memUsage && memUsage.quota > 0 ? (
                <CompactUsageBar used={memUsage.used} quota={memUsage.quota} />
              ) : (
                <CustomText text1={memoryMB ? `${memoryMB} MB` : '-'} />
              ),
          });

          // Disk (usage bar if live stats available, else quota)
          const diskUsage = getTotalDiskUsage(item);
          const diskMB = item.meta?.disk_in_mb;
          data.push({
            component:
              diskUsage && diskUsage.quota > 0 ? (
                <CompactUsageBar used={diskUsage.used} quota={diskUsage.quota} />
              ) : (
                <CustomText text1={diskMB ? `${diskMB} MB` : '-'} />
              ),
          });

          // Buildpack
          const buildpacks = item.meta?.buildpacks || item.meta?.lifecycle_buildpacks;
          const stack = item.meta?.stack;
          const lifecycleType = item.meta?.lifecycle_type;
          const buildpackDisplay = buildpacks && buildpacks.length > 0 ? buildpacks.join(', ') : lifecycleType || stack || '-';
          data.push({
            component: <CustomText text1={buildpackDisplay} />,
          });

          // Created At
          data.push({ component: <Datetime value={item?.resourse_created_on} /> });

          // Actions
          data.push({
            component: (
              <Box display='flex' flexDirection='row' alignItems='center' gap={ds.space[1]}>
                <DsDropdownMenu
                  align='end'
                  size='sm'
                  items={MENU_ITEMS.map((m) => ({
                    id: `cf-action-${item.resourse_id}-${m.id}`,
                    label: m.label,
                    disabled: m.disabled,
                    onSelect: () => onMenuClick({ id: m.id }, item),
                  }))}
                  trigger={
                    <DsButton tone='ghost' size='xs' composition='icon-only' aria-label='More actions' icon={<MoreVertIcon fontSize='small' />} />
                  }
                />
              </Box>
            ),
          });

          return data;
        });
        setInstances(resourceData);
        setInstancesCount(resourceCount);
      })
      .catch((error: any) => {
        setLoading(false);
        console.error('Failed to fetch CF instances:', error);
      });
  }, [props?.accountId, props?.serviceName, appliedInstanceIdName, selectedTagKey, selectedTagValue, selectedState, rowsPerPage, page]);

  useEffect(() => {
    listCFInstances();
  }, [listCFInstances]);

  return (
    <ListingLayout id='cf-instances'>
      <ListingLayout.Toolbar
        title={props.heading || undefined}
        actions={<DownloadButton id={`${cfInstancesTable}-download`} onClick={() => ({ tableId: cfInstancesTable })} />}
      >
        <CustomSearch
          id='cf-instances-search'
          label='Search By App Name'
          value={selectedInstanceIdName}
          onChange={(next) => {
            if (selectedInstanceIdName !== '' && next === '') {
              setAppliedInstanceIdName('');
              setPage(0);
            }
            setSelectedInstanceIdName(next);
          }}
          onEnterPress={onSearchEnter}
          onClear={onSearchClear}
        />
        <FilterDropdown
          id='cf-filter-state'
          label='State'
          value={selectedState}
          options={stateOptions}
          onSelect={(e: any) => {
            setSelectedState(e.target.value || 'all');
          }}
        />
        <FilterDropdown
          id='cf-filter-tag-key'
          label='Tag Key'
          value={selectedTagKey}
          options={availableTagKeys}
          onSelect={(e: any) => {
            setPage(0);
            setSelectedTagKey(e.target.value || null);
            if (!e.target.value) {
              setSelectedTagValue(null);
            }
          }}
        />
        <FilterDropdown
          id='cf-filter-tag-value'
          label='Tag Value'
          value={selectedTagValue}
          options={availableTagValues}
          disabled={!selectedTagKey}
          onSelect={(e: any) => {
            setPage(0);
            setSelectedTagValue(e.target.value || null);
          }}
        />
      </ListingLayout.Toolbar>

      <ListingLayout.Body>
        <CloudAccountTable
          id={cfInstancesTable}
          headers={CF_INSTANCE_HEADER}
          data={instances}
          rowsPerPage={rowsPerPage}
          onPageChange={changePage}
          totalRows={instancesCount}
          loading={loading}
          showExpandable={true}
          pageNumber={page + 1}
          expandable={{
            tabs: [
              {
                text: 'Details',
                value: 0,
                key: 'cf-details',
                componentFn: renderCFAppDetails,
              },
              {
                text: 'Instances',
                value: 1,
                key: 'cf-instance-stats',
                componentFn: renderCFInstanceStats,
              },
              {
                text: 'Monitoring',
                value: 2,
                key: 'cf-monitoring',
                componentFn: createMonitoringComponentFn(props?.accountId || '', props?.serviceName),
              },
              {
                text: 'Events',
                value: 3,
                key: 'cf-events',
                componentFn: createEventsComponentFn(props?.accountId || '', props?.serviceName),
              },
            ],
          }}
          tableHeadingCenter={props?.tableHeadingCenter || []}
          stickyColumnIndex={props?.stickyColumnIndex || ''}
        />
      </ListingLayout.Body>
    </ListingLayout>
  );
};

export default CFInstancesView;
