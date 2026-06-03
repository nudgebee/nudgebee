import { Box, Typography, Table, TableBody, TableCell, TableContainer, TableHead, TableRow, CircularProgress } from '@mui/material';
import { useState, useEffect } from 'react';
import { ds } from 'src/utils/colors';
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
      <Typography sx={{ fontSize: ds.text.small, color: ds.gray[700], fontWeight: ds.weight.regular, whiteSpace: 'nowrap' }}>
        {currentDisplay}
      </Typography>
      {isChanged ? (
        <ArrowForwardIcon sx={{ fontSize: '14px', color: ds.green[600], flexShrink: 0 }} />
      ) : (
        <DragHandleIcon sx={{ fontSize: '14px', color: ds.gray[500], flexShrink: 0 }} />
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
        // Fallback: try 'nb' (RPC) datasource for historical data
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
        <Typography sx={{ fontSize: ds.text.body, color: ds.gray[500], fontStyle: 'italic' }}>No right-sizing data available.</Typography>
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
            backgroundColor: ds.gray[100],
            borderRadius: ds.radius.lg,
            p: '10px',
            mb: ds.space[3],
            border: `1px solid ${ds.gray[200]}`,
          }}
        >
          <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500], fontStyle: 'italic', textAlign: 'center' }}>
            No trend data available for the last 7 days
          </Typography>
        </Box>
      )}
      {hasTrendData && (
        <>
          <SectionTitle title='CPU (cores) — 7 day trend' muiIcon={<TimelineIcon sx={{ fontSize: '16px' }} />} />
          <Box
            sx={{
              backgroundColor: ds.gray[100],
              borderRadius: ds.radius.lg,
              p: '10px',
              mb: ds.space[3],
              border: `1px solid ${ds.gray[200]}`,
            }}
          >
            <LineChart
              data={[cpuUsage, ...(cpuReccLine ? [cpuReccLine] : []), cpuRequest, ...(cpuLimit.some((v: any) => v != null) ? [cpuLimit] : [])]}
              labels={trendLabels}
              colors={[
                ds.blue[500],
                ...(cpuReccLine ? [ds.green[600]] : []),
                ds.gray[400],
                ...(cpuLimit.some((v: any) => v != null) ? [ds.red[500]] : []),
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
              backgroundColor: ds.gray[100],
              borderRadius: ds.radius.lg,
              p: '10px',
              mb: ds.space[3],
              border: `1px solid ${ds.gray[200]}`,
            }}
          >
            <LineChart
              data={[memUsage, ...(memReccLine ? [memReccLine] : []), memRequest, ...(memLimit.some((v: any) => v != null) ? [memLimit] : [])]}
              labels={trendLabels}
              colors={[
                ds.purple[500],
                ...(memReccLine ? [ds.green[600]] : []),
                ds.gray[400],
                ...(memLimit.some((v: any) => v != null) ? [ds.red[500]] : []),
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
          borderRadius: ds.radius.lg,
          border: `1px solid ${ds.gray[200]}`,
          mb: ds.space[3],
          '& .MuiTableCell-root': { px: '10px', py: '7px', fontSize: ds.text.small, borderColor: ds.gray[100] },
        }}
      >
        <Table size='small'>
          <TableHead>
            <TableRow sx={{ backgroundColor: ds.blue[100] }}>
              <TableCell sx={{ fontWeight: ds.weight.semibold, color: ds.gray[700], fontSize: '11px !important' }}>Container</TableCell>
              <TableCell sx={{ fontWeight: ds.weight.semibold, color: ds.gray[700], fontSize: '11px !important' }}>CPU Request (Core)</TableCell>
              <TableCell sx={{ fontWeight: ds.weight.semibold, color: ds.gray[700], fontSize: '11px !important' }}>Memory Request (MB)</TableCell>
            </TableRow>
          </TableHead>
          <TableBody>
            {containers.map(({ containerName, cpu, memory }) => (
              <TableRow key={containerName} sx={{ '&:last-child td': { borderBottom: 'none' } }}>
                <TableCell>
                  <Typography sx={{ fontSize: ds.text.small, color: ds.gray[700], fontWeight: ds.weight.medium, fontStyle: 'italic' }}>
                    {containerName}
                  </Typography>
                </TableCell>
                <TableCell>
                  {cpu ? (
                    <ValueArrow current={cpu.allocated?.request} recommended={cpu.recommended?.request} unit='cores' />
                  ) : (
                    <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500] }}>—</Typography>
                  )}
                </TableCell>
                <TableCell>
                  {memory ? (
                    <ValueArrow current={memory.allocated?.request} recommended={memory.recommended?.request} unit='MB' isMem />
                  ) : (
                    <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500] }}>—</Typography>
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
              borderRadius: ds.radius.lg,
              border: `1px solid ${ds.gray[200]}`,
              mb: ds.space[3],
              '& .MuiTableCell-root': { px: '10px', py: '7px', fontSize: ds.text.small, borderColor: ds.gray[100] },
            }}
          >
            <Table size='small'>
              <TableHead>
                <TableRow sx={{ backgroundColor: ds.blue[100] }}>
                  <TableCell sx={{ fontWeight: ds.weight.semibold, color: ds.gray[700], fontSize: '11px !important' }}>Container</TableCell>
                  <TableCell sx={{ fontWeight: ds.weight.semibold, color: ds.gray[700], fontSize: '11px !important' }}>CPU Limit (Core)</TableCell>
                  <TableCell sx={{ fontWeight: ds.weight.semibold, color: ds.gray[700], fontSize: '11px !important' }}>Memory Limit (MB)</TableCell>
                </TableRow>
              </TableHead>
              <TableBody>
                {containers.map(({ containerName, cpu, memory }) => (
                  <TableRow key={containerName} sx={{ '&:last-child td': { borderBottom: 'none' } }}>
                    <TableCell>
                      <Typography sx={{ fontSize: ds.text.small, color: ds.gray[700], fontWeight: ds.weight.medium, fontStyle: 'italic' }}>
                        {containerName}
                      </Typography>
                    </TableCell>
                    <TableCell>
                      {cpu?.allocated?.limit != null || cpu?.recommended?.limit != null ? (
                        <ValueArrow current={cpu?.allocated?.limit} recommended={cpu?.recommended?.limit} unit='cores' />
                      ) : (
                        <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500] }}>—</Typography>
                      )}
                    </TableCell>
                    <TableCell>
                      {memory?.allocated?.limit != null || memory?.recommended?.limit != null ? (
                        <ValueArrow current={memory?.allocated?.limit} recommended={memory?.recommended?.limit} unit='MB' isMem />
                      ) : (
                        <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500] }}>—</Typography>
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
                backgroundColor: ds.gray[100],
                borderRadius: ds.radius.lg,
                p: ds.space[3],
                border: `1px solid ${ds.gray[200]}`,
                display: 'flex',
                flexDirection: 'column',
                gap: ds.space[3],
              }}
            >
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
                  backgroundColor: ds.gray[100],
                  borderRadius: ds.radius.lg,
                  p: '10px',
                  mb: ds.space[2],
                  border: `1px solid ${ds.gray[200]}`,
                }}
              >
                <Typography sx={{ fontSize: ds.text.caption, fontWeight: ds.weight.semibold, color: ds.gray[500], mb: '6px', fontStyle: 'italic' }}>
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
    <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500] }}>{label}</Typography>
    <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[700], fontWeight: ds.weight.medium, fontFamily: 'monospace' }}>{value}</Typography>
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
        <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500], fontFamily: 'monospace' }}>
          {fmt(usage)} / {fmt(allocated)} {unit} ({pct.toFixed(0)}%)
        </Typography>
      </Box>
      <Box sx={{ position: 'relative', height: '16px', backgroundColor: ds.gray[200], borderRadius: ds.radius.sm, overflow: 'visible' }}>
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
              height: '20px',
              width: '2px',
              backgroundColor: ds.green[600],
              borderRadius: '1px',
              zIndex: 1,
            }}
            title={`Recommended: ${fmt(recommended!)} ${unit}`}
          />
        )}
      </Box>
      <Box sx={{ display: 'flex', justifyContent: 'space-between', mt: '2px' }}>
        <Typography sx={{ fontSize: '9px', color: ds.gray[500] }}>0</Typography>
        {recPct != null && (
          <Typography sx={{ fontSize: '9px', color: ds.green[600], fontWeight: ds.weight.semibold }}>
            Rec: {fmt(recommended!)} {unit}
          </Typography>
        )}
        <Typography sx={{ fontSize: '9px', color: ds.gray[500] }}>
          {fmt(allocated)} {unit}
        </Typography>
      </Box>
    </Box>
  );
};

export default RightSizingEvidence;
