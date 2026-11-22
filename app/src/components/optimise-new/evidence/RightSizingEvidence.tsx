import { Box, Typography, CircularProgress } from '@mui/material';
import { useMemo } from 'react';
import { ds } from 'src/utils/colors';
import { formatMemory } from '@lib/formatter';
import ArrowForwardIcon from '@mui/icons-material/ArrowForward';
import DragHandleIcon from '@mui/icons-material/DragHandle';
import Chart from '@ui/Chart';
import { Card } from '@ui/Card';
import { SavingsFooter } from './evidencePrimitives';
import { safeParseJSON } from '@components/optimise-new/utils';
import MetricQueryInfo, { K8S_METRIC_QUERY_LABELS } from '@shared/MetricQueryInfo';
import CustomTable from '@shared/tables/CustomTable';
import { perPodCaption } from './perPodTrend';
import { buildTrendSeries } from './trendSeries';
import { TrendRangeToggle } from './TrendRange';
import type { RightSizingTrend } from '@hooks/useRightSizingTrend';

interface RightSizingEvidenceProps {
  recommendation: any;
  estimatedSavings?: number;
  fullRecommendation?: any;
  /** The usage trend, fetched once by the drawer and shared with the Details tab. */
  trend: RightSizingTrend;
}

// Extract notifications from either format:
// Format 1: { notifications: [{resource, allocated, recommended}] }
// Format 2: { "container-name": [{resource, allocated, recommended}] }
const extractContainerData = (data: any): { containerName: string; cpu: any; memory: any }[] => {
  if (!data) return [];

  // Format 1: notifications array (flat, no container names)
  if (data.notifications && Array.isArray(data.notifications)) {
    const cpu = data.notifications.find((n: any) => n.resource === 'cpu');
    const mem = data.notifications.find((n: any) => n.resource === 'memory');
    return [{ containerName: 'default', cpu, memory: mem }];
  }

  // Format 2: container-keyed object
  const containers: { containerName: string; cpu: any; memory: any }[] = [];
  for (const [key, value] of Object.entries(data)) {
    if (Array.isArray(value) && value.length > 0 && value[0]?.resource) {
      const cpu = value.find((v: any) => v.resource === 'cpu');
      const mem = value.find((v: any) => v.resource === 'memory');
      containers.push({ containerName: key, cpu, memory: mem });
    }
  }
  return containers;
};

// Memory values from the K8s collector are always in bytes
const formatMem = (val: number | null | undefined): string => {
  if (val == null) return '—';
  return formatMemory(val, 'bytes', 'mb', false);
};

const calculatePercentage = (recommended: number, allocated: number): string => {
  const epsilon = 1e-10;
  if (!isNaN(recommended) && !isNaN(allocated) && Math.abs(allocated) > epsilon) {
    const pct = ((allocated - recommended) / allocated) * 100;
    const sign = pct > 0 ? '-' : '+';
    return `${sign}${Math.abs(pct).toFixed(0)}%`;
  }
  return '';
};

const ValueArrow = ({
  current,
  recommended,
  unit: _unit,
  isMem,
}: {
  current: number | null;
  recommended: number | null;
  unit: string;
  isMem?: boolean;
}) => {
  const formatValue = (v: number | null) => (v != null ? String(Number(v).toFixed(3)) : '—');
  const currentDisplay = isMem ? formatMem(current) : formatValue(current);
  const recDisplay = isMem ? formatMem(recommended) : formatValue(recommended);
  const isChanged = current != null && recommended != null && current !== recommended;
  const pct = current != null && recommended != null ? calculatePercentage(recommended, current) : '';

  return (
    <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space.mul(0, 3), flexWrap: 'nowrap' }}>
      <Typography sx={{ fontSize: ds.text.small, color: ds.gray[700], fontWeight: ds.weight.regular, whiteSpace: 'nowrap' }}>
        {currentDisplay}
      </Typography>
      {isChanged ? (
        <ArrowForwardIcon sx={{ fontSize: ds.text.bodyLg, color: ds.green[600], flexShrink: 0 }} />
      ) : (
        <DragHandleIcon sx={{ fontSize: ds.text.bodyLg, color: ds.gray[500], flexShrink: 0 }} />
      )}
      <Typography
        sx={{
          fontSize: ds.text.small,
          fontWeight: isChanged ? ds.weight.semibold : ds.weight.regular,
          color: isChanged ? ds.green[600] : ds.gray[700],
          whiteSpace: 'nowrap',
        }}
      >
        {recDisplay}
      </Typography>
      {pct && (
        <Typography
          sx={{
            fontSize: ds.text.caption,
            color: pct.startsWith('-') ? ds.green[600] : ds.red[600],
            fontWeight: ds.weight.medium,
            whiteSpace: 'nowrap',
          }}
        >
          {pct}
        </Typography>
      )}
    </Box>
  );
};

const PerPodCaption = ({ text }: { text: string }) => (
  <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500], mb: ds.space[2] }}>{text}</Typography>
);

const RightSizingEvidence = ({ recommendation, estimatedSavings, fullRecommendation, trend }: RightSizingEvidenceProps) => {
  const rec = useMemo(() => safeParseJSON(recommendation), [recommendation]);
  const containers = useMemo(() => extractContainerData(rec), [rec]);

  // The drawer owns the fetch (useRightSizingTrend); this tab draws it in full.
  const { perPod, loading: trendLoading, hasData: hasTrendData, cpuQueries, memoryQueries, rangeDays, setRangeDays } = trend;
  const perPodCaptionText = perPodCaption(perPod);

  // The recommended values from the recommendation JSONB draw the flat
  // recommendation line, and the same builder feeds the compact Details chart,
  // so both tabs plot identical numbers.
  const firstContainer = containers[0];
  const series = useMemo(
    () => buildTrendSeries(perPod, firstContainer?.cpu?.recommended?.request, firstContainer?.memory?.recommended?.request),
    [perPod, firstContainer]
  );
  const { labels: trendLabels, cpu, memory } = series;
  const { usage: cpuUsage, request: cpuRequest, limit: cpuLimit, hasLimit: hasCpuLimit, busiest: cpuBusiest, recommended: cpuReccLine } = cpu;
  const { usage: memUsage, request: memRequest, limit: memLimit, hasLimit: hasMemLimit, busiest: memBusiest, recommended: memReccLine } = memory;
  const basis = perPod.source ? 'per pod' : 'all replicas';

  if (containers.length === 0) {
    return (
      <Box sx={{ p: ds.space.mul(0, 7) }}>
        <Typography sx={{ fontSize: ds.text.body, color: ds.gray[500], fontStyle: 'italic' }}>No right-sizing data available.</Typography>
        {estimatedSavings != null && estimatedSavings !== 0 && <SavingsFooter savings={estimatedSavings} />}
      </Box>
    );
  }

  return (
    <Box sx={{ p: ds.space.mul(0, 7) }}>
      {/* CPU/Memory Trend Charts */}
      {fullRecommendation && (
        <Box sx={{ mb: ds.space[2] }}>
          <TrendRangeToggle value={rangeDays} onChange={setRangeDays} />
        </Box>
      )}
      {trendLoading && (
        <Box sx={{ display: 'flex', justifyContent: 'center', py: ds.space[4] }}>
          <CircularProgress size={24} />
        </Box>
      )}
      {!trendLoading && !hasTrendData && fullRecommendation && (
        <Box
          sx={{
            backgroundColor: ds.gray[100],
            borderRadius: ds.radius.lg,
            p: ds.space.mul(0, 5),
            mb: ds.space[3],
            border: `1px solid ${ds.gray[200]}`,
          }}
        >
          <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500], fontStyle: 'italic', textAlign: 'center' }}>
            {`No trend data available for the last ${rangeDays} days`}
          </Typography>
        </Box>
      )}
      {hasTrendData && (
        <>
          <Card
            variant='outlined'
            elevation='flat'
            size='sm'
            header={
              <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: ds.space[2] }}>
                <span>
                  CPU (cores) {basis} — {rangeDays} day trend
                </span>
                <MetricQueryInfo queries={cpuQueries} labelMap={K8S_METRIC_QUERY_LABELS} />
              </Box>
            }
            sx={{ mb: ds.space[3] }}
            data-testid='cpu-trend-card'
          >
            {perPodCaptionText && <PerPodCaption text={perPodCaptionText} />}
            <Chart.Line
              data={[
                cpuUsage,
                ...(cpuBusiest ? [cpuBusiest] : []),
                ...(cpuReccLine ? [cpuReccLine] : []),
                cpuRequest,
                ...(hasCpuLimit ? [cpuLimit] : []),
              ]}
              labels={trendLabels}
              colors={[
                ds.blue[500],
                ...(cpuBusiest ? [ds.blue[200]] : []),
                ...(cpuReccLine ? [ds.green[600]] : []),
                ds.gray[400],
                ...(hasCpuLimit ? [ds.red[500]] : []),
              ]}
              chartLabel={[
                'Usage',
                ...(cpuBusiest ? ['Busiest pod'] : []),
                ...(cpuReccLine ? ['Recommendation'] : []),
                'Requested',
                ...(hasCpuLimit ? ['Limit'] : []),
              ]}
              minHeight={180}
              dynamicHeight={false}
            />
          </Card>
          <Card
            variant='outlined'
            elevation='flat'
            size='sm'
            header={
              <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: ds.space[2] }}>
                <span>
                  Memory (MB) {basis} — {rangeDays} day trend
                </span>
                <MetricQueryInfo queries={memoryQueries} labelMap={K8S_METRIC_QUERY_LABELS} />
              </Box>
            }
            sx={{ mb: ds.space[3] }}
            data-testid='memory-trend-card'
          >
            {perPodCaptionText && <PerPodCaption text={perPodCaptionText} />}
            <Chart.Line
              data={[
                memUsage,
                ...(memBusiest ? [memBusiest] : []),
                ...(memReccLine ? [memReccLine] : []),
                memRequest,
                ...(hasMemLimit ? [memLimit] : []),
              ]}
              labels={trendLabels}
              colors={[
                ds.purple[500],
                ...(memBusiest ? [ds.purple[200]] : []),
                ...(memReccLine ? [ds.green[600]] : []),
                ds.gray[400],
                ...(hasMemLimit ? [ds.red[500]] : []),
              ]}
              chartLabel={[
                'Usage',
                ...(memBusiest ? ['Busiest pod'] : []),
                ...(memReccLine ? ['Recommendation'] : []),
                'Requested',
                ...(hasMemLimit ? ['Limit'] : []),
              ]}
              minHeight={180}
              dynamicHeight={false}
            />
          </Card>
        </>
      )}

      {/* Limits table */}
      {containers.some(({ cpu, memory }) => cpu?.allocated?.limit || memory?.allocated?.limit) && (
        <Card variant='outlined' elevation='flat' size='sm' header='Limits' sx={{ mb: ds.space[3] }} data-testid='limits-card'>
          <CustomTable
            headers={['Container', 'CPU Limit (Core)', 'Memory Limit (MB)']}
            tableData={containers.map(({ containerName, cpu, memory }) => [
              {
                component: (
                  <Typography sx={{ fontSize: ds.text.small, color: ds.gray[700], fontWeight: ds.weight.medium, fontStyle: 'italic' }}>
                    {containerName}
                  </Typography>
                ),
                data: containerName,
              },
              {
                component:
                  cpu?.allocated?.limit != null || cpu?.recommended?.limit != null ? (
                    <ValueArrow current={cpu?.allocated?.limit} recommended={cpu?.recommended?.limit} unit='cores' />
                  ) : (
                    <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500] }}>—</Typography>
                  ),
              },
              {
                component:
                  memory?.allocated?.limit != null || memory?.recommended?.limit != null ? (
                    <ValueArrow current={memory?.allocated?.limit} recommended={memory?.recommended?.limit} unit='MB' isMem />
                  ) : (
                    <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500] }}>—</Typography>
                  ),
              },
            ])}
          />
        </Card>
      )}

      {/* Percentile usage data */}
      {/* CPU/Memory utilization bars per container */}
      {containers.map(({ containerName, cpu, memory }) => {
        const cpuAllocated = cpu?.allocated?.request;
        const cpuP99 = cpu?.add_info?.cpu_percentile_99;
        const cpuRecommended = cpu?.recommended?.request;
        const memAllocated = memory?.allocated?.request;
        const memP99 = memory?.add_info?.memory_percentile_p99;
        const memRecommended = memory?.recommended?.request;
        const hasUtilization = cpuP99 != null || memP99 != null;
        if (!hasUtilization) return null;
        return (
          <Card
            key={`util-${containerName}`}
            variant='outlined'
            elevation='flat'
            size='sm'
            header={`Utilization — ${containerName}`}
            sx={{ mb: ds.space[3] }}
            data-testid={`utilization-card-${containerName}`}
          >
            <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[3] }}>
              {cpuP99 != null && cpuAllocated != null && cpuAllocated > 0 && (
                <UtilizationBar label='CPU' unit='cores' allocated={cpuAllocated} usage={cpuP99} recommended={cpuRecommended} color={ds.blue[500]} />
              )}
              {memP99 != null && memAllocated != null && memAllocated > 0 && (
                <UtilizationBar
                  label='Memory'
                  unit='MB'
                  allocated={memAllocated}
                  usage={memP99}
                  recommended={memRecommended}
                  color={ds.purple[500]}
                  isMem
                />
              )}
            </Box>
          </Card>
        );
      })}

      {/* Detailed percentile data */}
      {containers.some(({ cpu, memory }) => cpu?.add_info || memory?.add_info) && (
        <Card variant='outlined' elevation='flat' size='sm' header='Usage Percentiles' sx={{ mb: ds.space[3] }} data-testid='usage-percentiles-card'>
          {containers.map(({ containerName, cpu, memory }, idx) => {
            const hasPercentiles = cpu?.add_info || memory?.add_info;
            if (!hasPercentiles) return null;
            return (
              <Box key={containerName} sx={{ mt: idx > 0 ? ds.space[3] : 0 }}>
                <Typography
                  sx={{ fontSize: ds.text.caption, fontWeight: ds.weight.semibold, color: ds.gray[500], mb: ds.space.mul(0, 3), fontStyle: 'italic' }}
                >
                  {containerName}
                </Typography>
                <Box sx={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: ds.space[1] }}>
                  {cpu?.add_info?.cpu_percentile_99 != null && (
                    <PercentileItem label='CPU P99' value={`${Number(cpu.add_info.cpu_percentile_99).toFixed(4)} cores`} />
                  )}
                  {cpu?.add_info?.cpu_percentile_97 != null && (
                    <PercentileItem label='CPU P97' value={`${Number(cpu.add_info.cpu_percentile_97).toFixed(4)} cores`} />
                  )}
                  {cpu?.add_info?.cpu_percentile_95 != null && (
                    <PercentileItem label='CPU P95' value={`${Number(cpu.add_info.cpu_percentile_95).toFixed(4)} cores`} />
                  )}
                  {memory?.add_info?.memory_percentile_p99 != null && (
                    <PercentileItem label='Mem P99' value={`${formatMem(memory.add_info.memory_percentile_p99)} MB`} />
                  )}
                  {memory?.add_info?.actual_recommended_request != null && (
                    <PercentileItem label='Actual Rec Req' value={`${formatMem(memory.add_info.actual_recommended_request)} MB`} />
                  )}
                  {memory?.add_info?.actual_recommended_limit != null && (
                    <PercentileItem label='Actual Rec Limit' value={`${formatMem(memory.add_info.actual_recommended_limit)} MB`} />
                  )}
                </Box>
              </Box>
            );
          })}
        </Card>
      )}

      {estimatedSavings != null && estimatedSavings !== 0 && <SavingsFooter savings={estimatedSavings} />}
    </Box>
  );
};

const PercentileItem = ({ label, value }: { label: string; value: string }) => (
  <Box sx={{ display: 'flex', justifyContent: 'space-between', py: ds.space[1] }}>
    <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500] }}>{label}</Typography>
    <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[700], fontWeight: ds.weight.medium, fontFamily: ds.font.mono }}>{value}</Typography>
  </Box>
);

/** Visual utilization bar: shows usage vs allocated with recommended line marker */
const UtilizationBar = ({
  label,
  unit,
  allocated,
  usage,
  recommended,
  color,
  isMem,
}: {
  label: string;
  unit: string;
  allocated: number;
  usage: number;
  recommended?: number;
  color: string;
  isMem?: boolean;
}) => {
  const fmt = (v: number) => (isMem ? formatMem(v) : Number(v).toFixed(3));
  const pct = Math.min((usage / allocated) * 100, 100);
  const recPct = recommended != null && allocated > 0 ? Math.min((recommended / allocated) * 100, 100) : null;
  const getUsageColor = () => {
    if (pct > 90) return ds.red[600];
    if (pct > 70) return ds.amber[500];
    return color;
  };
  const usageColor = getUsageColor();

  return (
    <Box>
      <Box sx={{ display: 'flex', justifyContent: 'space-between', mb: ds.space[1] }}>
        <Typography sx={{ fontSize: ds.text.caption, fontWeight: ds.weight.semibold, color: ds.gray[700] }}>{label} Usage (P99)</Typography>
        <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500], fontFamily: ds.font.mono }}>
          {fmt(usage)} / {fmt(allocated)} {unit} ({pct.toFixed(0)}%)
        </Typography>
      </Box>
      <Box sx={{ position: 'relative', height: ds.space[4], backgroundColor: ds.gray[200], borderRadius: ds.radius.sm, overflow: 'visible' }}>
        {/* Usage fill */}
        <Box
          sx={{
            position: 'absolute',
            left: 0,
            top: 0,
            height: '100%',
            width: `${pct}%`,
            backgroundColor: usageColor,
            borderRadius: ds.radius.sm,
            transition: 'width 0.3s ease',
          }}
        />
        {/* Recommended marker line */}
        {recPct != null && (
          <Box
            sx={{
              position: 'absolute',
              left: `${recPct}%`,
              top: '-2px',
              height: ds.space.mul(1, 5),
              width: ds.space[0],
              backgroundColor: ds.green[600],
              borderRadius: 'calc(var(--ds-radius-sm) / 2)',
              zIndex: 1,
            }}
            title={`Recommended: ${fmt(recommended!)} ${unit}`}
          />
        )}
      </Box>
      <Box sx={{ display: 'flex', justifyContent: 'space-between', mt: ds.space[0] }}>
        <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500] }}>0</Typography>
        {recPct != null && (
          <Typography sx={{ fontSize: ds.text.caption, color: ds.green[600], fontWeight: ds.weight.semibold }}>
            Rec: {fmt(recommended!)} {unit}
          </Typography>
        )}
        <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500] }}>
          {fmt(allocated)} {unit}
        </Typography>
      </Box>
    </Box>
  );
};

export default RightSizingEvidence;
