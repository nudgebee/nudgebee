import { Box, Typography, Table, TableBody, TableCell, TableContainer, TableHead, TableRow, CircularProgress } from '@mui/material';
import { useState, useEffect } from 'react';
import { colors } from 'src/utils/colors';
import { formatMemory } from '@lib/formatter';
import ArrowForwardIcon from '@mui/icons-material/ArrowForward';
import DragHandleIcon from '@mui/icons-material/DragHandle';
import BoltIcon from '@mui/icons-material/Bolt';
import BarChartIcon from '@mui/icons-material/BarChart';
import TrendingUpIcon from '@mui/icons-material/TrendingUp';
import TimelineIcon from '@mui/icons-material/Timeline';
import LineChart from '@components1/common/charts/LineCharts';
import k8sApi from '@api1/kubernetes';
import { SavingsFooter, SectionTitle } from '@components1/optimise-new/EvidencePanel';
import { safeParseJSON } from '@components1/optimise-new/utils';

interface RightSizingEvidenceProps {
  recommendation: any;
  estimatedSavings?: number;
  fullRecommendation?: any;
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
    <Box sx={{ display: 'flex', alignItems: 'center', gap: '6px', flexWrap: 'nowrap' }}>
      <Typography sx={{ fontSize: '12px', color: colors.text.secondary, fontWeight: 400, whiteSpace: 'nowrap' }}>{currentDisplay}</Typography>
      {isChanged ? (
        <ArrowForwardIcon sx={{ fontSize: '14px', color: '#16A34A', flexShrink: 0 }} />
      ) : (
        <DragHandleIcon sx={{ fontSize: '14px', color: colors.text.tertiary, flexShrink: 0 }} />
      )}
      <Typography
        sx={{
          fontSize: '12px',
          fontWeight: isChanged ? 600 : 400,
          color: isChanged ? '#16A34A' : colors.text.secondary,
          whiteSpace: 'nowrap',
        }}
      >
        {recDisplay}
      </Typography>
      {pct && (
        <Typography
          sx={{
            fontSize: '10px',
            color: pct.startsWith('-') ? '#16A34A' : '#DC2626',
            fontWeight: 500,
            whiteSpace: 'nowrap',
          }}
        >
          {pct}
        </Typography>
      )}
    </Box>
  );
};

const RightSizingEvidence = ({ recommendation, estimatedSavings, fullRecommendation }: RightSizingEvidenceProps) => {
  const rec = safeParseJSON(recommendation);
  const containers = extractContainerData(rec);

  // Fetch CPU/Memory trend data
  const [trendData, setTrendData] = useState<any[]>([]);
  const [trendLoading, setTrendLoading] = useState(false);

  useEffect(() => {
    if (!fullRecommendation) return;
    const accountId = fullRecommendation.account_id;
    const meta = fullRecommendation.cloud_resourse?.meta || {};
    const resourceType = fullRecommendation.cloud_resourse?.type || fullRecommendation.resource_type || '';
    const namespaceName = fullRecommendation.resource_k8s_namespace || meta?.config?.namespace || meta?.namespace || meta?.namespaceName || '';
    const isPod = resourceType === 'Pod';
    const workloadName = isPod
      ? meta?.controller || meta?.config?.controller || fullRecommendation.resource_name
      : fullRecommendation.cloud_resourse?.name || fullRecommendation.resource_name;
    const workloadType = isPod ? meta?.controllerKind || meta?.config?.controllerKind || 'Deployment' : resourceType || 'Deployment';
    // For Pod-level: only pass podName if different from workloadName (matches existing page behavior)
    const podName = isPod ? fullRecommendation.cloud_resourse?.name || fullRecommendation.resource_name : undefined;
    const effectivePodName = podName && podName !== workloadName ? podName : undefined;

    if (!accountId || !namespaceName || !workloadName) return;

    setTrendLoading(true);
    const startDate = new Date();
    startDate.setDate(startDate.getDate() - 7);

    // Match the existing KubernetesUtilization page: use 'prometheus' datasource with explicit metrics list
    const query: any = {
      accountId,
      namespaceName,
      workloadName,
      workloadType: workloadType?.toLowerCase(),
      startDate,
      endDate: new Date(),
      metrics: ['cpu_usage', 'memory_usage', 'cpu_limit', 'cpu_request', 'memory_limit', 'memory_request'],
    };
    if (effectivePodName) query.podName = effectivePodName;

    const groupBy = ['tenant_id', 'account_id', 'timestamp'];
    if (namespaceName) groupBy.push('namespace_name');
    if (workloadName) groupBy.push('workload_name');
    if (effectivePodName) groupBy.push('pod_name');

    k8sApi
      .getK8sPodGroupings2(500, query, groupBy, 'prometheus')
      .then((res: any) => {
        const rows = res?.data?.k8s_pod_groupings || [];
        if (rows.length > 0) {
          setTrendData(rows);
          return;
        }
        // Fallback: try 'nb' (Hasura) datasource for historical data
        return k8sApi.getK8sPodGroupings2(500, query, groupBy, 'nb').then((res2: any) => {
          setTrendData(res2?.data?.k8s_pod_groupings || []);
        });
      })
      .catch((err: any) => {
        console.error('[RightSizingEvidence] Failed to fetch pod trend data:', err);
      })
      .finally(() => setTrendLoading(false));
  }, [fullRecommendation]);

  if (containers.length === 0) {
    return (
      <Box sx={{ p: '14px' }}>
        <Typography sx={{ fontSize: '13px', color: colors.text.tertiary, fontStyle: 'italic' }}>No right-sizing data available.</Typography>
        {estimatedSavings != null && estimatedSavings !== 0 && <SavingsFooter savings={estimatedSavings} />}
      </Box>
    );
  }

  // Prepare trend chart data — match existing KubernetesRecommendationCharts format
  // Shows: Usage, Request, Limit, Recommendation as separate lines
  const hasTrendData = trendData.length > 0;
  const trendLabels = trendData.map((r: any) => {
    const d = new Date(r.timestamp);
    return d.toLocaleDateString('en-US', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });
  });
  const cpuUsage = trendData.map((r: any) => r.avg_cpu_used);
  const cpuRequest = trendData.map((r: any) => r.avg_cpu_request);
  const cpuLimit = trendData.map((r: any) => r.avg_cpu_limit);
  const memUsage = trendData.map((r: any) => (r.avg_memory_used != null ? r.avg_memory_used / (1024 * 1024) : null));
  const memRequest = trendData.map((r: any) => (r.avg_memory_request != null ? r.avg_memory_request / (1024 * 1024) : null));
  const memLimit = trendData.map((r: any) => (r.avg_memory_limit != null ? r.avg_memory_limit / (1024 * 1024) : null));

  // Get recommended values from recommendation JSONB to draw horizontal recommendation line
  const firstContainer = containers[0];
  const cpuReccValue = firstContainer?.cpu?.recommended?.request;
  const memReccValue = firstContainer?.memory?.recommended?.request;
  const cpuReccLine = cpuReccValue != null ? trendData.map(() => cpuReccValue) : null;
  const memReccLine = memReccValue != null ? trendData.map(() => Number(formatMemory(memReccValue, 'bytes', 'mb', false))) : null;

  return (
    <Box sx={{ p: '14px' }}>
      <SectionTitle title='Resource Right-Sizing' muiIcon={<BoltIcon sx={{ fontSize: '16px' }} />} />

      {/* CPU/Memory Trend Charts */}
      {trendLoading && (
        <Box sx={{ display: 'flex', justifyContent: 'center', py: '16px' }}>
          <CircularProgress size={24} />
        </Box>
      )}
      {!trendLoading && !hasTrendData && fullRecommendation && (
        <Box
          sx={{
            backgroundColor: colors.background.tertiaryLightestestest,
            borderRadius: '8px',
            p: '10px',
            mb: '12px',
            border: `1px solid ${colors.border.secondaryLight}`,
          }}
        >
          <Typography sx={{ fontSize: '11px', color: colors.text.tertiary, fontStyle: 'italic', textAlign: 'center' }}>
            No trend data available for the last 7 days
          </Typography>
        </Box>
      )}
      {hasTrendData && (
        <>
          <SectionTitle title='CPU (cores) — 7 day trend' muiIcon={<TimelineIcon sx={{ fontSize: '16px' }} />} />
          <Box
            sx={{
              backgroundColor: colors.background.tertiaryLightestestest,
              borderRadius: '8px',
              p: '10px',
              mb: '12px',
              border: `1px solid ${colors.border.secondaryLight}`,
            }}
          >
            <LineChart
              data={[cpuUsage, ...(cpuReccLine ? [cpuReccLine] : []), cpuRequest, ...(cpuLimit.some((v: any) => v != null) ? [cpuLimit] : [])]}
              labels={trendLabels}
              colors={[
                colors.border?.cpuUsage || '#3B82F6',
                ...(cpuReccLine ? [colors.border?.cpuRecommendation || '#16A34A'] : []),
                colors.border?.cpuRequested || '#9CA3AF',
                ...(cpuLimit.some((v: any) => v != null) ? [colors.border?.cpuLimit || '#EF4444'] : []),
              ]}
              chartLabel={[
                'Usage',
                ...(cpuReccLine ? ['Recommendation'] : []),
                'Requested',
                ...(cpuLimit.some((v: any) => v != null) ? ['Limit'] : []),
              ]}
              minHeight={180}
              dynamicHeight={false}
            />
          </Box>
          <SectionTitle title='Memory (MB) — 7 day trend' muiIcon={<TimelineIcon sx={{ fontSize: '16px' }} />} />
          <Box
            sx={{
              backgroundColor: colors.background.tertiaryLightestestest,
              borderRadius: '8px',
              p: '10px',
              mb: '12px',
              border: `1px solid ${colors.border.secondaryLight}`,
            }}
          >
            <LineChart
              data={[memUsage, ...(memReccLine ? [memReccLine] : []), memRequest, ...(memLimit.some((v: any) => v != null) ? [memLimit] : [])]}
              labels={trendLabels}
              colors={[
                colors.border?.memoryUsage || '#8B5CF6',
                ...(memReccLine ? [colors.border?.memoryRecommendation || '#16A34A'] : []),
                colors.border?.memoryRequested || '#9CA3AF',
                ...(memLimit.some((v: any) => v != null) ? [colors.border?.memoryLimit || '#EF4444'] : []),
              ]}
              chartLabel={[
                'Usage',
                ...(memReccLine ? ['Recommendation'] : []),
                'Requested',
                ...(memLimit.some((v: any) => v != null) ? ['Limit'] : []),
              ]}
              minHeight={180}
              dynamicHeight={false}
            />
          </Box>
        </>
      )}

      <TableContainer
        sx={{
          borderRadius: '8px',
          border: `1px solid ${colors.border.secondaryLight}`,
          mb: '12px',
          '& .MuiTableCell-root': { px: '10px', py: '7px', fontSize: '12px', borderColor: colors.background.tertiaryLight },
        }}
      >
        <Table size='small'>
          <TableHead>
            <TableRow sx={{ backgroundColor: colors.background.tableHeader }}>
              <TableCell sx={{ fontWeight: 600, color: colors.text.secondary, fontSize: '11px !important' }}>Container</TableCell>
              <TableCell sx={{ fontWeight: 600, color: colors.text.secondary, fontSize: '11px !important' }}>CPU Request (Core)</TableCell>
              <TableCell sx={{ fontWeight: 600, color: colors.text.secondary, fontSize: '11px !important' }}>Memory Request (MB)</TableCell>
            </TableRow>
          </TableHead>
          <TableBody>
            {containers.map(({ containerName, cpu, memory }) => (
              <TableRow key={containerName} sx={{ '&:last-child td': { borderBottom: 'none' } }}>
                <TableCell>
                  <Typography sx={{ fontSize: '12px', color: colors.text.secondary, fontWeight: 500, fontStyle: 'italic' }}>
                    {containerName}
                  </Typography>
                </TableCell>
                <TableCell>
                  {cpu ? (
                    <ValueArrow current={cpu.allocated?.request} recommended={cpu.recommended?.request} unit='cores' />
                  ) : (
                    <Typography sx={{ fontSize: '12px', color: colors.text.tertiary }}>—</Typography>
                  )}
                </TableCell>
                <TableCell>
                  {memory ? (
                    <ValueArrow current={memory.allocated?.request} recommended={memory.recommended?.request} unit='MB' isMem />
                  ) : (
                    <Typography sx={{ fontSize: '12px', color: colors.text.tertiary }}>—</Typography>
                  )}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </TableContainer>

      {/* Limits table */}
      {containers.some(({ cpu, memory }) => cpu?.allocated?.limit || memory?.allocated?.limit) && (
        <>
          <SectionTitle title='Limits' muiIcon={<BarChartIcon sx={{ fontSize: '16px' }} />} />
          <TableContainer
            sx={{
              borderRadius: '8px',
              border: `1px solid ${colors.border.secondaryLight}`,
              mb: '12px',
              '& .MuiTableCell-root': { px: '10px', py: '7px', fontSize: '12px', borderColor: colors.background.tertiaryLight },
            }}
          >
            <Table size='small'>
              <TableHead>
                <TableRow sx={{ backgroundColor: colors.background.tableHeader }}>
                  <TableCell sx={{ fontWeight: 600, color: colors.text.secondary, fontSize: '11px !important' }}>Container</TableCell>
                  <TableCell sx={{ fontWeight: 600, color: colors.text.secondary, fontSize: '11px !important' }}>CPU Limit (Core)</TableCell>
                  <TableCell sx={{ fontWeight: 600, color: colors.text.secondary, fontSize: '11px !important' }}>Memory Limit (MB)</TableCell>
                </TableRow>
              </TableHead>
              <TableBody>
                {containers.map(({ containerName, cpu, memory }) => (
                  <TableRow key={containerName} sx={{ '&:last-child td': { borderBottom: 'none' } }}>
                    <TableCell>
                      <Typography sx={{ fontSize: '12px', color: colors.text.secondary, fontWeight: 500, fontStyle: 'italic' }}>
                        {containerName}
                      </Typography>
                    </TableCell>
                    <TableCell>
                      {cpu?.allocated?.limit != null || cpu?.recommended?.limit != null ? (
                        <ValueArrow current={cpu?.allocated?.limit} recommended={cpu?.recommended?.limit} unit='cores' />
                      ) : (
                        <Typography sx={{ fontSize: '12px', color: colors.text.tertiary }}>—</Typography>
                      )}
                    </TableCell>
                    <TableCell>
                      {memory?.allocated?.limit != null || memory?.recommended?.limit != null ? (
                        <ValueArrow current={memory?.allocated?.limit} recommended={memory?.recommended?.limit} unit='MB' isMem />
                      ) : (
                        <Typography sx={{ fontSize: '12px', color: colors.text.tertiary }}>—</Typography>
                      )}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </TableContainer>
        </>
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
          <Box key={`util-${containerName}`} sx={{ mb: '10px' }}>
            <SectionTitle title={`Utilization — ${containerName}`} muiIcon={<TrendingUpIcon sx={{ fontSize: '16px' }} />} />
            <Box
              sx={{
                backgroundColor: colors.background.tertiaryLightestestest,
                borderRadius: '8px',
                p: '12px',
                border: `1px solid ${colors.border.secondaryLight}`,
                display: 'flex',
                flexDirection: 'column',
                gap: '12px',
              }}
            >
              {cpuP99 != null && cpuAllocated != null && cpuAllocated > 0 && (
                <UtilizationBar label='CPU' unit='cores' allocated={cpuAllocated} usage={cpuP99} recommended={cpuRecommended} color='#3B82F6' />
              )}
              {memP99 != null && memAllocated != null && memAllocated > 0 && (
                <UtilizationBar label='Memory' unit='MB' allocated={memAllocated} usage={memP99} recommended={memRecommended} color='#8B5CF6' isMem />
              )}
            </Box>
          </Box>
        );
      })}

      {/* Detailed percentile data */}
      {containers.some(({ cpu, memory }) => cpu?.add_info || memory?.add_info) && (
        <>
          <SectionTitle title='Usage Percentiles' muiIcon={<TrendingUpIcon sx={{ fontSize: '16px' }} />} />
          {containers.map(({ containerName, cpu, memory }) => {
            const hasPercentiles = cpu?.add_info || memory?.add_info;
            if (!hasPercentiles) return null;
            return (
              <Box
                key={containerName}
                sx={{
                  backgroundColor: colors.background.tertiaryLightestestest,
                  borderRadius: '8px',
                  p: '10px',
                  mb: '8px',
                  border: `1px solid ${colors.border.secondaryLight}`,
                }}
              >
                <Typography sx={{ fontSize: '11px', fontWeight: 600, color: colors.text.tertiary, mb: '6px', fontStyle: 'italic' }}>
                  {containerName}
                </Typography>
                <Box sx={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '4px' }}>
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
        </>
      )}

      {estimatedSavings != null && estimatedSavings !== 0 && <SavingsFooter savings={estimatedSavings} />}
    </Box>
  );
};

const PercentileItem = ({ label, value }: { label: string; value: string }) => (
  <Box sx={{ display: 'flex', justifyContent: 'space-between', py: '3px' }}>
    <Typography sx={{ fontSize: '11px', color: colors.text.tertiary }}>{label}</Typography>
    <Typography sx={{ fontSize: '11px', color: colors.text.secondary, fontWeight: 500, fontFamily: 'monospace' }}>{value}</Typography>
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
    if (pct > 90) return '#DC2626';
    if (pct > 70) return '#EAB308';
    return color;
  };
  const usageColor = getUsageColor();

  return (
    <Box>
      <Box sx={{ display: 'flex', justifyContent: 'space-between', mb: '4px' }}>
        <Typography sx={{ fontSize: '11px', fontWeight: 600, color: colors.text.secondary }}>{label} Usage (P99)</Typography>
        <Typography sx={{ fontSize: '11px', color: colors.text.tertiary, fontFamily: 'monospace' }}>
          {fmt(usage)} / {fmt(allocated)} {unit} ({pct.toFixed(0)}%)
        </Typography>
      </Box>
      <Box sx={{ position: 'relative', height: '16px', backgroundColor: colors.border.secondaryLightest, borderRadius: '4px', overflow: 'visible' }}>
        {/* Usage fill */}
        <Box
          sx={{
            position: 'absolute',
            left: 0,
            top: 0,
            height: '100%',
            width: `${pct}%`,
            backgroundColor: usageColor,
            borderRadius: '4px',
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
              height: '20px',
              width: '2px',
              backgroundColor: '#16A34A',
              borderRadius: '1px',
              zIndex: 1,
            }}
            title={`Recommended: ${fmt(recommended!)} ${unit}`}
          />
        )}
      </Box>
      <Box sx={{ display: 'flex', justifyContent: 'space-between', mt: '2px' }}>
        <Typography sx={{ fontSize: '9px', color: colors.text.tertiary }}>0</Typography>
        {recPct != null && (
          <Typography sx={{ fontSize: '9px', color: '#16A34A', fontWeight: 600 }}>
            Rec: {fmt(recommended!)} {unit}
          </Typography>
        )}
        <Typography sx={{ fontSize: '9px', color: colors.text.tertiary }}>
          {fmt(allocated)} {unit}
        </Typography>
      </Box>
    </Box>
  );
};

export default RightSizingEvidence;
