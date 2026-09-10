import { Box, Typography, CircularProgress } from '@mui/material';
import { useState, useEffect, useMemo } from 'react';
import { ds } from 'src/utils/colors';
import { formatMemory } from '@lib/formatter';
import ArrowForwardIcon from '@mui/icons-material/ArrowForward';
import DragHandleIcon from '@mui/icons-material/DragHandle';
import Chart from '@ui/Chart';
import { Card } from '@ui/Card';
import k8sApi from '@api1/kubernetes';
import { SavingsFooter } from './evidencePrimitives';
import { safeParseJSON } from '@components/optimise-new/utils';
import MetricQueryInfo, { K8S_METRIC_QUERY_LABELS } from '@shared/MetricQueryInfo';
import CustomTable from '@shared/tables/CustomTable';
import { replicaWindowOf } from '../rightSizingData';
import { perPodCaption, toPerPodTrend } from './perPodTrend';

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

const RightSizingEvidence = ({ recommendation, estimatedSavings, fullRecommendation }: RightSizingEvidenceProps) => {
  const rec = useMemo(() => safeParseJSON(recommendation), [recommendation]);
  const containers = useMemo(() => extractContainerData(rec), [rec]);

  // Fetch CPU/Memory trend data
  const [trendData, setTrendData] = useState<any[]>([]);
  const [trendLoading, setTrendLoading] = useState(false);
  // Executed metric queries that produced the trend (only the prometheus datasource returns them).
  const [cpuQueries, setCpuQueries] = useState<Record<string, string>>({});
  const [memoryQueries, setMemoryQueries] = useState<Record<string, string>>({});

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

    let cancelled = false;

    setTrendLoading(true);
    setCpuQueries({});
    setMemoryQueries({});
    const startDate = new Date();
    startDate.setDate(startDate.getDate() - 7);

    // Match the existing KubernetesUtilization page: use 'prometheus' datasource with explicit metrics list
    // The recommendation line is the first container's target, so usage is
    // scoped to that container and asked for per pod: the running pod count
    // and the busiest pod ride along with the workload totals.
    const query: any = {
      accountId,
      namespaceName,
      workloadName,
      workloadType: workloadType?.toLowerCase(),
      startDate,
      endDate: new Date(),
      metrics: [
        'cpu_usage',
        'memory_usage',
        'cpu_limit',
        'cpu_request',
        'memory_limit',
        'memory_request',
        'pod_count',
        'cpu_usage_max_pod',
        'memory_usage_max_pod',
      ],
    };
    if (effectivePodName) query.podName = effectivePodName;
    // The legacy {notifications: [...]} shape names no container; its 'default'
    // placeholder must not become a PromQL filter that matches nothing.
    const containerName = containers[0]?.containerName;
    if (containerName && containerName !== 'default') query.containerName = containerName;

    const groupBy = ['tenant_id', 'account_id', 'timestamp'];
    if (namespaceName) groupBy.push('namespace_name');
    if (workloadName) groupBy.push('workload_name');
    if (effectivePodName) groupBy.push('pod_name');

    // Split the executed queries (keyed by metric, e.g. cpu_usage / memory_limit) into CPU and memory groups.
    const applyQueries = (res: any) => {
      const promQueries: Record<string, string> = res?.data?.promQueries || {};
      const cpuQ: Record<string, string> = {};
      const memQ: Record<string, string> = {};
      Object.entries(promQueries).forEach(([key, q]) => {
        if (key.startsWith('cpu_')) {
          cpuQ[key] = q;
        } else if (key.startsWith('memory_')) {
          memQ[key] = q;
        } else if (key === 'pod_count') {
          // The divisor behind both charts.
          cpuQ[key] = q;
          memQ[key] = q;
        }
      });
      setCpuQueries(cpuQ);
      setMemoryQueries(memQ);
    };

    k8sApi
      .getK8sPodGroupings2(500, query, groupBy, 'prometheus')
      .then((res: any) => {
        if (cancelled) return undefined;
        const rows = res?.data?.k8s_pod_groupings || [];
        if (rows.length > 0) {
          setTrendData(rows);
          applyQueries(res);
          return undefined;
        }
        // Fallback: try 'nb' (RPC) datasource for historical data
        return k8sApi.getK8sPodGroupings2(500, query, groupBy, 'nb').then((res2: any) => {
          if (cancelled) return;
          setTrendData(res2?.data?.k8s_pod_groupings || []);
        });
      })
      .catch((err: any) => {
        if (cancelled) return;
        console.error('[RightSizingEvidence] Failed to fetch pod trend data:', err);
      })
      .finally(() => {
        if (!cancelled) setTrendLoading(false);
      });

    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [fullRecommendation, containers[0]?.containerName]);

  // Per pod, like the recommendation line: totals are divided by the running
  // pod count, or by the replica average the producers stamped on the row.
  const livePods = fullRecommendation?.cloud_resourse?.meta?.total_pods;
  const perPod = useMemo(
    () => toPerPodTrend(trendData, replicaWindowOf(containers[0] ? [containers[0].cpu, containers[0].memory] : [])?.avg, livePods),
    [trendData, containers, livePods]
  );
  const perPodCaptionText = perPodCaption(perPod);

  // Prepare trend chart data — match existing KubernetesRecommendationCharts format
  // Shows: Usage, Request, Limit, Recommendation as separate lines
  const { trendLabels, cpuUsage, cpuRequest, cpuLimit, memUsage, memRequest, memLimit, hasCpuLimit, hasMemLimit, cpuBusiest, memBusiest } =
    useMemo(() => {
      const rows = perPod.rows;
      const limitsCpu = rows.map((r: any) => r.avg_cpu_limit);
      const limitsMem = rows.map((r: any) => (r.avg_memory_limit != null ? r.avg_memory_limit / (1024 * 1024) : null));
      return {
        trendLabels: rows.map((r: any) =>
          new Date(r.timestamp).toLocaleDateString('en-US', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })
        ),
        cpuUsage: rows.map((r: any) => r.avg_cpu_used),
        cpuRequest: rows.map((r: any) => r.avg_cpu_request),
        cpuLimit: limitsCpu,
        memUsage: rows.map((r: any) => (r.avg_memory_used != null ? r.avg_memory_used / (1024 * 1024) : null)),
        memRequest: rows.map((r: any) => (r.avg_memory_request != null ? r.avg_memory_request / (1024 * 1024) : null)),
        memLimit: limitsMem,
        hasCpuLimit: limitsCpu.some((v: any) => v != null),
        hasMemLimit: limitsMem.some((v: any) => v != null),
        cpuBusiest: perPod.hasBusiestPod ? rows.map((r: any) => r.max_pod_cpu_used ?? null) : null,
        memBusiest: perPod.hasBusiestPod
          ? rows.map((r: any) => (r.max_pod_memory_used != null ? r.max_pod_memory_used / (1024 * 1024) : null))
          : null,
      };
    }, [perPod]);

  // Get recommended values from recommendation JSONB to draw horizontal recommendation line
  const firstContainer = containers[0];
  const cpuReccValue = firstContainer?.cpu?.recommended?.request;
  const memReccValue = firstContainer?.memory?.recommended?.request;
  const cpuReccLine = useMemo(() => (cpuReccValue != null ? perPod.rows.map(() => cpuReccValue) : null), [perPod.rows, cpuReccValue]);
  const memReccLine = useMemo(
    () => (memReccValue != null ? perPod.rows.map(() => Number(formatMemory(memReccValue, 'bytes', 'mb', false))) : null),
    [perPod.rows, memReccValue]
  );
  const basis = perPod.source ? 'per pod' : 'all replicas';

  if (containers.length === 0) {
    return (
      <Box sx={{ p: ds.space.mul(0, 7) }}>
        <Typography sx={{ fontSize: ds.text.body, color: ds.gray[500], fontStyle: 'italic' }}>No right-sizing data available.</Typography>
        {estimatedSavings != null && estimatedSavings !== 0 && <SavingsFooter savings={estimatedSavings} />}
      </Box>
    );
  }

  const hasTrendData = trendData.length > 0;

  return (
    <Box sx={{ p: ds.space.mul(0, 7) }}>
      {/* CPU/Memory Trend Charts */}
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
            No trend data available for the last 7 days
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
                <span>CPU (cores) {basis} — 7 day trend</span>
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
                <span>Memory (MB) {basis} — 7 day trend</span>
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
