import React, { useEffect, useState } from 'react';
import { Box, Typography, LinearProgress, Tooltip, Chip } from '@mui/material';
import SummarySkeletonLoader from '@components1/common/SummarySkeletonLoader';
import { formatMemory } from '@lib/formatter';
import apiCloudAccount from '@api1/cloud-account';

import BoxLayout2 from '@components1/common/BoxLayout2';
import { getLast7Days } from '@lib/datetime';
import Charts from '@components1/common/charts/LineCharts';
import { formatMetricName } from '@utils/common';

// ─── Primitives ───────────────────────────────────────────────────────────────

const Card = ({ children, sx = {} }: any) => (
  <Box
    sx={{
      backgroundColor: '#fff',
      borderRadius: '8px',
      boxShadow: '0px 1px 3px rgba(0,0,0,0.08)',
      border: '1px solid #F3F4F6',
      padding: '16px',
      ...sx,
    }}
  >
    {children}
  </Box>
);

const SectionLabel = ({ children }: any) => (
  <Typography fontSize='13px' fontWeight={600} color='#374151' mb='10px'>
    {children}
  </Typography>
);

const parseResource = (r: any) => ({
  ...r,
  meta: typeof r.meta === 'string' ? JSON.parse(r.meta || '{}') : r.meta || {},
  tags: typeof r.tags === 'string' ? JSON.parse(r.tags || '{}') : r.tags || {},
});

const getOrgName = (r: any) => {
  const t = r.tags?.org;
  return Array.isArray(t) ? t[0] : t || '-';
};
const getSpaceName = (r: any) => {
  const t = r.tags?.space;
  return Array.isArray(t) ? t[0] : t || '-';
};

// ─── App Health Breakdown ─────────────────────────────────────────────────────
// Horizontal segmented bar showing running/stopped/crashed distribution

const AppHealthBreakdown = ({ apps }: { apps: any[] }) => {
  const total = apps.length;
  if (total === 0) return null;

  const running = apps.filter((a) => a.status?.toLowerCase() === 'active').length;
  const stopped = total - running;

  // Check for crashed instances
  let crashedApps = 0;
  apps.forEach((a) => {
    const stats = a.meta?.instance_stats;
    if (Array.isArray(stats) && stats.some((s: any) => s.state === 'CRASHED')) crashedApps++;
  });

  const segments = [
    { label: 'Running', count: running - crashedApps, color: '#22C55E' },
    { label: 'Degraded', count: crashedApps, color: '#F59E0B' },
    { label: 'Stopped', count: stopped, color: '#D1D5DB' },
  ].filter((s) => s.count > 0);

  return (
    <Card>
      <SectionLabel>App Health</SectionLabel>
      {/* Segmented bar */}
      <Box sx={{ display: 'flex', height: '8px', borderRadius: '4px', overflow: 'hidden', mb: '10px' }}>
        {segments.map((seg) => (
          <Tooltip key={seg.label} title={`${seg.label}: ${seg.count}`} arrow>
            <Box sx={{ width: `${(seg.count / total) * 100}%`, backgroundColor: seg.color, minWidth: seg.count > 0 ? '4px' : 0 }} />
          </Tooltip>
        ))}
      </Box>
      {/* Legend */}
      <Box sx={{ display: 'flex', gap: '16px', flexWrap: 'wrap' }}>
        {segments.map((seg) => (
          <Box key={seg.label} sx={{ display: 'flex', alignItems: 'center', gap: '5px' }}>
            <Box sx={{ width: 8, height: 8, borderRadius: '50%', backgroundColor: seg.color }} />
            <Typography fontSize='11px' color='#6B7280'>
              {seg.label}
            </Typography>
            <Typography fontSize='11px' fontWeight={600} color='#374151'>
              {seg.count}
            </Typography>
          </Box>
        ))}
      </Box>
    </Card>
  );
};

// ─── Top Consumers ────────────────────────────────────────────────────────────
// Shows top 5 apps by memory allocation — the ones using most resources

const TopConsumers = ({ apps }: { apps: any[] }) => {
  const appsWithMem = apps
    .map((a) => ({
      name: a.name || '-',
      org: getOrgName(a),
      space: getSpaceName(a),
      memMB: (a.meta?.memory_in_mb || 0) * (a.meta?.instances || 0),
      instances: a.meta?.instances || 0,
      isActive: a.status?.toLowerCase() === 'active',
    }))
    .filter((a) => a.memMB > 0)
    .sort((a, b) => b.memMB - a.memMB)
    .slice(0, 5);

  const maxMem = appsWithMem[0]?.memMB || 1;

  if (appsWithMem.length === 0) return null;

  return (
    <Card>
      <SectionLabel>Top Resource Consumers</SectionLabel>
      {appsWithMem.map((app, idx) => (
        <Box
          key={app.name}
          sx={{
            display: 'flex',
            alignItems: 'center',
            gap: '10px',
            py: '6px',
            borderBottom: idx < appsWithMem.length - 1 ? '1px solid #F9FAFB' : 'none',
          }}
        >
          <Box sx={{ width: 6, height: 6, borderRadius: '50%', backgroundColor: app.isActive ? '#22C55E' : '#D1D5DB', flexShrink: 0 }} />
          <Box sx={{ flex: 1, minWidth: 0 }}>
            <Box sx={{ display: 'flex', justifyContent: 'space-between', mb: '2px' }}>
              <Typography fontSize='11px' fontWeight={500} color='#374151' noWrap>
                {app.name}
              </Typography>
              <Typography fontSize='10px' color='#6B7280' flexShrink={0}>
                {app.memMB} MB &middot; {app.instances}i
              </Typography>
            </Box>
            <LinearProgress
              variant='determinate'
              value={(app.memMB / maxMem) * 100}
              sx={{
                height: 3,
                borderRadius: 2,
                backgroundColor: '#F3F4F6',
                '& .MuiLinearProgress-bar': { backgroundColor: '#3B82F6', borderRadius: 2 },
              }}
            />
          </Box>
        </Box>
      ))}
    </Card>
  );
};

// ─── Spaces Overview ──────────────────────────────────────────────────────────

const SpacesOverview = ({ apps }: { apps: any[] }) => {
  const spaceMap: Record<string, { org: string; appCount: number; memMB: number; running: number }> = {};
  apps.forEach((a) => {
    const space = getSpaceName(a);
    const org = getOrgName(a);
    if (space === '-') return;
    if (!spaceMap[space]) spaceMap[space] = { org, appCount: 0, memMB: 0, running: 0 };
    spaceMap[space].appCount++;
    spaceMap[space].memMB += (a.meta?.memory_in_mb || 0) * (a.meta?.instances || 0);
    if (a.status?.toLowerCase() === 'active') spaceMap[space].running++;
  });

  const entries = Object.entries(spaceMap).sort((a, b) => b[1].appCount - a[1].appCount);
  if (entries.length === 0) return null;

  return (
    <Card>
      <SectionLabel>Spaces</SectionLabel>
      {entries.map(([name, data], idx) => (
        <Box
          key={name}
          sx={{
            display: 'flex',
            justifyContent: 'space-between',
            alignItems: 'center',
            py: '6px',
            borderBottom: idx < entries.length - 1 ? '1px solid #F9FAFB' : 'none',
          }}
        >
          <Box>
            <Typography fontSize='12px' fontWeight={500} color='#374151'>
              {name}
            </Typography>
            <Typography fontSize='10px' color='#9CA3AF'>
              {data.org}
            </Typography>
          </Box>
          <Box sx={{ display: 'flex', gap: '8px', alignItems: 'center' }}>
            <Chip label={`${data.running}/${data.appCount} apps`} size='small' variant='outlined' sx={{ fontSize: '10px', height: '20px' }} />
            {data.memMB > 0 && (
              <Typography fontSize='10px' color='#6B7280'>
                {data.memMB} MB
              </Typography>
            )}
          </Box>
        </Box>
      ))}
    </Card>
  );
};

// ═══════════════════════════════════════════════════════════════════════════════
// CFSummaryDetails — CF-specific sections shown below generic CloudAccountSummary
// ═══════════════════════════════════════════════════════════════════════════════

export const CFSummaryDetails = ({ accountId = '' }: { accountId: string }) => {
  const [loading, setLoading] = useState(true);
  const [apps, setApps] = useState<any[]>([]);

  useEffect(() => {
    if (!accountId) return;
    setLoading(true);

    const fetchActive = apiCloudAccount.getCloudResource({ account_id: accountId, serviceName: 'apps', type: [], status: 'Active' }, 500, 0);
    const fetchInactive = apiCloudAccount.getCloudResource({ account_id: accountId, serviceName: 'apps', type: [], status: 'Inactive' }, 500, 0);

    Promise.all([fetchActive, fetchInactive])
      .then(([activeRes, inactiveRes]: any[]) => {
        const activeApps = (activeRes?.data?.data?.cloud_resourses || []).map(parseResource);
        const inactiveApps = (inactiveRes?.data?.data?.cloud_resourses || []).map(parseResource);
        setApps([...activeApps, ...inactiveApps]);
        setLoading(false);
      })
      .catch((err: any) => {
        console.error('CFSummaryDetails fetch error:', err);
        setLoading(false);
      });
  }, [accountId]);

  if (loading) return <SummarySkeletonLoader />;

  const buildpackCounts: Record<string, number> = {};
  apps.forEach((a) => {
    const bp = a.meta?.lifecycle_type || a.meta?.stack || 'unknown';
    buildpackCounts[bp] = (buildpackCounts[bp] || 0) + 1;
  });

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: '12px', mt: '12px', mb: '20px' }}>
      {/* Row 1: Health + Top Consumers + Spaces */}
      <Box sx={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', gap: '12px' }}>
        <AppHealthBreakdown apps={apps} />
        <TopConsumers apps={apps} />
        <SpacesOverview apps={apps} />
      </Box>

      {/* Row 2: Buildpacks (compact) */}
      {Object.keys(buildpackCounts).length > 0 && (
        <Box sx={{ display: 'flex', gap: '6px', alignItems: 'center', px: '4px' }}>
          <Typography fontSize='11px' color='#9CA3AF'>
            Buildpacks:
          </Typography>
          {Object.entries(buildpackCounts).map(([bp, count]) => (
            <Chip key={bp} label={`${bp} (${count})`} size='small' variant='outlined' sx={{ fontSize: '10px', height: '22px' }} />
          ))}
        </Box>
      )}
    </Box>
  );
};

export default CFSummaryDetails;

// ═══════════════════════════════════════════════════════════════════════════════
// OptimizeSummary — used in expandable row monitoring tab
// ═══════════════════════════════════════════════════════════════════════════════

export const OptimizeSummary = ({ accountId = '', serviceName = '', resourceId = '', showSummary: _showSummary = false }) => {
  const [loadingTrend, setLoadingTrend] = useState(false);
  const [renderMetricsData, setRenderMetricsData] = useState<any>({});
  const [selectedDateRange, setSelectedDateRange] = useState({
    startDate: getLast7Days().getTime(),
    endDate: new Date().getTime(),
  });

  useEffect(() => {
    if (!accountId) return;
    setLoadingTrend(true);
    apiCloudAccount
      .getCloudResourceMetrics({
        account_id: accountId,
        serviceName,
        resourceId,
        startDate: new Date(selectedDateRange.startDate),
        endDate: new Date(selectedDateRange.endDate),
      })
      .then((res: any) => {
        setLoadingTrend(false);
        const metricsData = res?.data?.data?.cloud_metric_groupings_v2?.rows || [];
        if (metricsData?.length > 0) {
          const grouped = metricsData.reduce((acc: any, curr: any) => {
            if (!acc[curr.metric]) acc[curr.metric] = [];
            acc[curr.metric].push(curr);
            return acc;
          }, {});
          setRenderMetricsData(grouped);
        }
      })
      .catch((error: any) => {
        setLoadingTrend(false);
        console.error(error);
      });
  }, [accountId, selectedDateRange]);

  const handleDateRangeChange = (dt: any) => {
    setSelectedDateRange({ startDate: dt.startTime, endDate: dt.endTime });
  };

  const renderMetricsSummary = () => {
    if (renderMetricsData && Object.keys(renderMetricsData).length > 0) {
      return Object.keys(renderMetricsData).map((g: string) => {
        const label = renderMetricsData[g].map((h: any) => new Date(h.timestamp).toLocaleString());
        const isCpu = g.replace(/[_\s]/g, '').toLowerCase() === 'cpuutilization';
        const values = renderMetricsData[g].map((item: any) => (isCpu ? item.avg_value : formatMemory(item.avg_value, 'bytes', 'gb', false)));
        return (
          <Box
            key={g}
            sx={{
              mb: '24px',
              background: 'white',
              borderRadius: '8px',
              border: '1px solid #EBEBEB',
              boxShadow: '0px 4px 6px -1px rgba(0,0,0,0.05)',
              p: '20px',
            }}
          >
            <Charts
              chartTitle={formatMetricName(g)}
              dataset={[{ label: 'Utilization', data: values }]}
              labels={label}
              data={[]}
              loading={loadingTrend}
            />
          </Box>
        );
      });
    }
    return <Charts dataset={[]} labels={[]} data={[]} loading={loadingTrend} />;
  };

  return (
    <BoxLayout2
      id=''
      heading='Metrics'
      sharingOptions={{ sharing: { enabled: false, onClick: null }, download: { enabled: false, onClick: () => ({ tableId: '' }) } }}
      dateTimeRange={{
        enabled: true,
        onChange: handleDateRangeChange,
        passedSelectedDateTime: { startTime: selectedDateRange.startDate, endTime: selectedDateRange.endDate, shortcutClickTime: 0 },
      }}
    >
      {renderMetricsSummary()}
    </BoxLayout2>
  );
};
