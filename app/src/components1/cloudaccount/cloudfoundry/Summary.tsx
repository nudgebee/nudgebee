import React, { useEffect, useState } from 'react';
import { Box, Typography, LinearProgress } from '@mui/material';
import SummarySkeletonLoader from '@components1/common/SummarySkeletonLoader';
import { formatMemory } from '@lib/formatter';
import apiCloudAccount from '@api1/cloud-account';
import { getLast7Days } from '@lib/datetime';
import Charts from '@components1/common/charts/LineCharts';
import { formatMetricName } from '@utils/common';
import { ListingLayout } from '@components1/ds/ListingLayout';
import CustomDateTimeRangePicker from '@common-new/widgets/CustomDateTimeRangePicker';
import DSCard from '@components1/ds/Card';
import DsTooltip from '@components1/ds/Tooltip';
import Chip from '@components1/ds/Chip';
import { ds } from '@utils/colors';

// ─── Primitives ───────────────────────────────────────────────────────────────

// Card-level section label — `bodyLg + medium + gray[700]`. Rendered inside
// each DSCard's `header` slot; DSCard owns the divider + spacing below the heading.
const SectionLabel = ({ children }: any) => (
  <Typography sx={{ fontSize: ds.text.bodyLg, fontWeight: ds.weight.medium, color: ds.gray[700] }}>{children}</Typography>
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
// Horizontal segmented bar showing running / degraded / stopped distribution.
//
// Colors are intentionally hardcoded traffic-light values (green / amber / gray)
// rather than ds.green/amber/gray — DS palette tones are status-axis pill
// colors and don't carry the saturation/contrast needed for a thin 8px bar.
// If the DS later ships a Distribution / SegmentedBar primitive, swap then.

const AppHealthBreakdown = ({ apps }: { apps: any[] }) => {
  const total = apps.length;
  if (total === 0) return null;

  const running = apps.filter((a) => a.status?.toLowerCase() === 'active').length;
  const stopped = total - running;

  let crashedApps = 0;
  apps.forEach((a) => {
    const stats = a.meta?.instance_stats;
    if (Array.isArray(stats) && stats.some((s: any) => s.state === 'CRASHED')) crashedApps++;
  });

  const segments = [
    { label: 'Running', count: running - crashedApps, color: ds.green[500] },
    { label: 'Degraded', count: crashedApps, color: ds.amber[500] },
    { label: 'Stopped', count: stopped, color: ds.gray[300] },
  ].filter((s) => s.count > 0);

  return (
    <DSCard size='md' elevation='flat' header={<SectionLabel>App Health</SectionLabel>}>
      <Box sx={{ display: 'flex', height: ds.space[2], borderRadius: ds.radius.sm, overflow: 'hidden', mb: ds.space[2] }}>
        {segments.map((seg) => (
          <DsTooltip key={seg.label} title={`${seg.label}: ${seg.count}`} arrow>
            <Box sx={{ width: `${(seg.count / total) * 100}%`, backgroundColor: seg.color, minWidth: seg.count > 0 ? ds.space[1] : 0 }} />
          </DsTooltip>
        ))}
      </Box>
      <Box sx={{ display: 'flex', gap: ds.space[4], flexWrap: 'wrap' }}>
        {segments.map((seg) => (
          <Box key={seg.label} sx={{ display: 'flex', alignItems: 'center', gap: ds.space[1] }}>
            <Box sx={{ width: 8, height: 8, borderRadius: '50%', backgroundColor: seg.color }} />
            <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500] }}>{seg.label}</Typography>
            <Typography sx={{ fontSize: ds.text.small, fontWeight: ds.weight.semibold, color: ds.gray[700] }}>{seg.count}</Typography>
          </Box>
        ))}
      </Box>
    </DSCard>
  );
};

// ─── Top Consumers ────────────────────────────────────────────────────────────
// Top 5 apps by memory allocation (memory_in_mb × instance count).

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
    <DSCard size='md' elevation='flat' header={<SectionLabel>Top Resource Consumers</SectionLabel>}>
      {appsWithMem.map((app, idx) => (
        <Box
          key={app.name}
          sx={{
            display: 'flex',
            alignItems: 'center',
            gap: ds.space[2],
            py: ds.space[1],
            borderBottom: idx < appsWithMem.length - 1 ? `1px solid ${ds.gray[100]}` : 'none',
          }}
        >
          <Box sx={{ width: 6, height: 6, borderRadius: '50%', backgroundColor: app.isActive ? ds.green[500] : ds.gray[300], flexShrink: 0 }} />
          <Box sx={{ flex: 1, minWidth: 0 }}>
            <Box sx={{ display: 'flex', justifyContent: 'space-between', mb: ds.space[1] }}>
              <Typography sx={{ fontSize: ds.text.small, fontWeight: ds.weight.medium, color: ds.gray[700] }} noWrap>
                {app.name}
              </Typography>
              <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500], flexShrink: 0 }}>
                {app.memMB} MB &middot; {app.instances}i
              </Typography>
            </Box>
            <LinearProgress
              variant='determinate'
              value={(app.memMB / maxMem) * 100}
              sx={{
                height: 3,
                borderRadius: ds.radius.md,
                backgroundColor: ds.gray[100],
                '& .MuiLinearProgress-bar': { backgroundColor: ds.blue[500], borderRadius: ds.radius.md },
              }}
            />
          </Box>
        </Box>
      ))}
    </DSCard>
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
    <DSCard size='md' elevation='flat' header={<SectionLabel>Spaces</SectionLabel>}>
      {entries.map(([name, data], idx) => (
        <Box
          key={name}
          sx={{
            display: 'flex',
            justifyContent: 'space-between',
            alignItems: 'center',
            py: ds.space[1],
            borderBottom: idx < entries.length - 1 ? `1px solid ${ds.gray[100]}` : 'none',
          }}
        >
          <Box>
            <Typography sx={{ fontSize: ds.text.small, fontWeight: ds.weight.medium, color: ds.gray[700] }}>{name}</Typography>
            <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[400] }}>{data.org}</Typography>
          </Box>
          <Box sx={{ display: 'flex', gap: ds.space[2], alignItems: 'center' }}>
            <Chip variant='tag' tone='neutral' size='sm'>
              {`${data.running}/${data.appCount} apps`}
            </Chip>
            {data.memMB > 0 && <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500] }}>{data.memMB} MB</Typography>}
          </Box>
        </Box>
      ))}
    </DSCard>
  );
};

// ═══════════════════════════════════════════════════════════════════════════════
// CFSummaryDetails — CF-specific sections shown below the generic CloudAccount
// Summary header. Renders App Health + Top Consumers + Spaces in a 3-col grid,
// with a compact Buildpacks chip row below.
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
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[3], mt: ds.space[3], mb: ds.space[5] }}>
      {/* Row 1: Health + Top Consumers + Spaces */}
      <Box sx={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', gap: ds.space[3] }}>
        <AppHealthBreakdown apps={apps} />
        <TopConsumers apps={apps} />
        <SpacesOverview apps={apps} />
      </Box>

      {/* Row 2: Buildpacks (compact chip row) */}
      {Object.keys(buildpackCounts).length > 0 && (
        <Box sx={{ display: 'flex', gap: ds.space[1], alignItems: 'center', px: ds.space[1], flexWrap: 'wrap' }}>
          <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500] }}>Buildpacks:</Typography>
          {Object.entries(buildpackCounts).map(([bp, count]) => (
            <Chip key={bp} variant='tag' tone='neutral' size='sm'>
              {`${bp} (${count})`}
            </Chip>
          ))}
        </Box>
      )}
    </Box>
  );
};

export default CFSummaryDetails;

// ═══════════════════════════════════════════════════════════════════════════════
// OptimizeSummary — used in the expandable-row monitoring tab. Renders a
// single Metrics panel (ListingLayout with date picker) over `getCloudResource
// Metrics` data.
//
// Note: this branch uses `getCloudResourceMetrics` (non-Direct) — different
// from EC2/RDS/S3/ECS which all use `…MetricsDirect`. Preserved verbatim — the
// CF endpoint behavior or shape may differ; switching is out of migration
// scope.
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
  }, [accountId, selectedDateRange, serviceName, resourceId]);

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
          <DSCard size='md' elevation='flat' key={g} sx={{ mb: ds.space[4], padding: ds.space[5] }}>
            <Charts
              chartTitle={formatMetricName(g)}
              dataset={[{ label: 'Utilization', data: values }]}
              labels={label}
              data={[]}
              loading={loadingTrend}
            />
          </DSCard>
        );
      });
    }
    return <Charts dataset={[]} labels={[]} data={[]} loading={loadingTrend} />;
  };

  return (
    <ListingLayout id='cf-metrics'>
      <ListingLayout.Toolbar
        title='Metrics'
        actions={
          <CustomDateTimeRangePicker
            passedSelectedDateTime={{
              startTime: selectedDateRange.startDate,
              endTime: selectedDateRange.endDate,
              shortcutClickTime: 0,
            }}
            onChange={(result: any) => {
              const val = result?.selection ?? result;
              if (val) handleDateRangeChange(val);
            }}
          />
        }
      />
      <ListingLayout.Body padding={`${ds.space[4]} ${ds.space[5]}`}>{renderMetricsSummary()}</ListingLayout.Body>
    </ListingLayout>
  );
};
