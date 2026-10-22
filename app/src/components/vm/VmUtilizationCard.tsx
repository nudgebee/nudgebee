import React, { useEffect, useMemo, useState } from 'react';
import { Box, Typography } from '@mui/material';
import { useRouter } from 'next/router';
import Card from '@ui/Card';
import Heading from '@components/common/Heading';
import { Select } from '@ui/Select';
import { Banner } from '@ui/Banner';
import apiVm, { VmMetricsStatus, VmResource } from '@api1/vm';
import { Gauge } from './VmMetricsGauges';
import { emptyReasons, formatBytes, formatCores, hasAnyData } from './vmMetricsData';
import { OBSERVABILITY_INTEGRATIONS_URL, useLatestRequest } from './common';
import { ds } from '@utils/colors';

/** VMs read per request, and the most pages read: the picker covers up to PAGE_SIZE * MAX_PAGES VMs. */
const PAGE_SIZE = 200;
const MAX_PAGES = 10;

/** Every VM of the account, page by page, so a reporting VM past the first page is still offered. */
export const listAllVms = async (accountId: string): Promise<VmResource[]> => {
  const all: VmResource[] = [];
  for (let page = 0; page < MAX_PAGES; page += 1) {
    const { rows, total } = await apiVm.listVms({ accountId, limit: PAGE_SIZE, offset: page * PAGE_SIZE });
    all.push(...rows);
    if (!rows.length || all.length >= total) break;
  }
  return all;
};

const RANGES = [
  { value: '1h', label: 'Last 1 Hour', ms: 60 * 60 * 1000 },
  { value: '6h', label: 'Last 6 Hours', ms: 6 * 60 * 60 * 1000 },
  { value: '24h', label: 'Last 24 Hours', ms: 24 * 60 * 60 * 1000 },
];

const KEYS = ['cpu_usage_line', 'cpu_total', 'memory_usage_line', 'mem_total'];

const storageKey = (accountId: string) => `vm-summary-instance:${accountId}`;

const readChoice = (accountId: string): string | null => {
  try {
    return window.localStorage.getItem(storageKey(accountId));
  } catch {
    return null;
  }
};

const writeChoice = (accountId: string, id: string) => {
  try {
    window.localStorage.setItem(storageKey(accountId), id);
  } catch {
    // A blocked store only means the choice is not remembered.
  }
};

const percentile = (sorted: number[], p: number): number | undefined => {
  if (!sorted.length) return undefined;
  return sorted[Math.min(sorted.length - 1, Math.max(0, Math.ceil((p / 100) * sorted.length) - 1))];
};

interface Resource {
  total?: number;
  usage?: number;
  p50?: number;
  p90?: number;
  max?: number;
}

const summarise = (values: number[], total?: number): Resource => {
  const sorted = [...values].sort((a, b) => a - b);
  return {
    total,
    usage: values.length ? values[values.length - 1] : undefined,
    p50: percentile(sorted, 50),
    p90: percentile(sorted, 90),
    max: sorted[sorted.length - 1],
  };
};

const seriesValues = (results: Array<{ key: string; series: Array<{ values: number[] }> }>, key: string): number[] =>
  results.find((r) => r.key === key)?.series?.[0]?.values || [];

const lastOf = (values: number[]) => (values.length ? values[values.length - 1] : undefined);

/** Share of `total` that `value` is, rounded; undefined when either is unknown or total is 0. */
const pctOf = (value?: number, total?: number) => (value === undefined || !total ? undefined : Math.round((value / total) * 100));

const STAT_ROWS: Array<{ key: keyof Resource; label: string }> = [
  { key: 'total', label: 'Total' },
  { key: 'usage', label: 'Usage' },
  { key: 'p50', label: 'P50 Usage' },
  { key: 'p90', label: 'P90 Usage' },
  { key: 'max', label: 'Max Usage' },
];

const Column = ({ title, resource, format }: { title: string; resource?: Resource; format: (value?: number) => string }) => (
  <Box sx={{ flex: 1, minWidth: 0 }} data-testid={`vm-utilization-${title.toLowerCase()}`}>
    <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.semibold, color: ds.gray[700], mb: ds.space[2] }}>{title}</Typography>
    <Box sx={{ display: 'flex', justifyContent: 'center', mb: ds.space[3] }}>
      <Gauge label='Usage' value={pctOf(resource?.usage, resource?.total)} scale={2} />
    </Box>
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[1] }}>
      {STAT_ROWS.map(({ key, label }) => (
        <Box key={key} sx={{ display: 'flex', justifyContent: 'space-between', gap: ds.space[3] }}>
          <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500] }}>{label}</Typography>
          <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[700] }}>{format(resource?.[key])}</Typography>
        </Box>
      ))}
    </Box>
  </Box>
);

/**
 * CPU and memory of one VM, laid out like the cluster summary's Utilization & Health.
 *
 * Request and Limit rows are not here: they are Kubernetes scheduling concepts a VM does
 * not have. Only VMs a node-agent is reporting for are offered. The one shown is the one
 * the user picked last, else the first of them — nothing on the proxy agent says which VM
 * it runs on, so the forager host cannot be singled out yet.
 */
const VmUtilizationCard = ({ accountId }: { accountId: string }) => {
  const router = useRouter();
  const [vms, setVms] = useState<VmResource[]>([]);
  const [selected, setSelected] = useState<string>('');
  const [range, setRange] = useState(RANGES[0].value);
  const [loadingVms, setLoadingVms] = useState(true);
  const [loadingMetrics, setLoadingMetrics] = useState(false);
  const [error, setError] = useState<string>();
  const [cpu, setCpu] = useState<Resource>();
  const [memory, setMemory] = useState<Resource>();
  const [noData, setNoData] = useState<string>();
  const [metricsStatus, setMetricsStatus] = useState<VmMetricsStatus>();
  const [statusError, setStatusError] = useState<string>();
  const beginStatus = useLatestRequest();
  const beginVms = useLatestRequest();
  const beginMetrics = useLatestRequest();

  // Whether metrics are set up at all, and whether the store answers. The check goes
  // through a forager, so it runs beside the VM read rather than gating it.
  useEffect(() => {
    if (!accountId) return;
    const isLatest = beginStatus();
    setMetricsStatus(undefined);
    setStatusError(undefined);
    apiVm
      .getMetricsStatus(accountId)
      .then((status) => isLatest() && setMetricsStatus(status))
      .catch((err) => isLatest() && setStatusError(err?.message || 'Failed to check metrics'));
  }, [accountId, beginStatus]);

  useEffect(() => {
    if (!accountId) return;
    const isLatest = beginVms();
    setLoadingVms(true);
    setVms([]);
    setSelected('');
    (async () => {
      try {
        const rows = await listAllVms(accountId);
        // Only VMs a node-agent is reporting for: a VM with no metrics has nothing to show.
        const fleet = await apiVm.getVmFleetMetrics({ accountId, vms: rows });
        if (!isLatest()) return;
        const reporting = rows.filter((vm) => fleet[vm.id]);
        setVms(reporting);
        const remembered = readChoice(accountId);
        setSelected(reporting.find((vm) => vm.id === remembered)?.id || reporting[0]?.id || '');
      } catch (err: any) {
        if (!isLatest()) return;
        setError(err?.message || 'Failed to load VMs');
      } finally {
        if (isLatest()) setLoadingVms(false);
      }
    })();
  }, [accountId, beginVms]);

  const vm = useMemo(() => vms.find((candidate) => candidate.id === selected), [vms, selected]);

  useEffect(() => {
    if (!vm) return;
    const isLatest = beginMetrics();
    const windowMs = RANGES.find((r) => r.value === range)?.ms ?? RANGES[0].ms;
    const end = Date.now();
    setLoadingMetrics(true);
    setError(undefined);
    setNoData(undefined);
    apiVm
      .getVmMetrics({ accountId, vm, startDate: end - windowMs, endDate: end, metrics: KEYS })
      .then((results) => {
        if (!isLatest()) return;
        if (!hasAnyData(results)) {
          setCpu(undefined);
          setMemory(undefined);
          setNoData(emptyReasons(results)[0] || 'No metrics reported for this VM in this window.');
          return;
        }
        const cpuTotal = lastOf(seriesValues(results, 'cpu_total'));
        const memTotal = lastOf(seriesValues(results, 'mem_total'));
        setCpu(summarise(seriesValues(results, 'cpu_usage_line'), cpuTotal));
        setMemory(summarise(seriesValues(results, 'memory_usage_line'), memTotal));
      })
      .catch((err) => {
        if (!isLatest()) return;
        setCpu(undefined);
        setMemory(undefined);
        setError(err?.message || 'Failed to load metrics');
      })
      .finally(() => {
        if (isLatest()) setLoadingMetrics(false);
      });
  }, [accountId, vm, range, beginMetrics]);

  const notConfigured = metricsStatus?.state === 'not_configured';
  const metricsFailure =
    statusError ||
    (metricsStatus && ['unreachable', 'check_failed'].includes(metricsStatus.state)
      ? metricsStatus.error || 'The metrics store could not be reached.'
      : undefined);

  const options = vms.map((candidate) => ({ value: candidate.id, label: candidate.name || candidate.resourse_id }));

  return (
    <Card size='md' id='vm-summary-utilization'>
      <Box sx={{ display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: ds.space[3], mb: ds.space[4] }}>
        <Heading value='Utilization & Health' borderWidth='md' />
        {/* The pickers stay on one line and share what the heading leaves; they only drop
            under it when the card is too narrow for both. */}
        <Box sx={{ display: 'flex', flex: '1 1 280px', gap: ds.space[2], minWidth: 0 }}>
          <Box sx={{ flex: 2, minWidth: 0 }}>
            <Select
              size='sm'
              clearable={false}
              placeholder={loadingVms ? 'Loading VMs…' : 'Select a VM'}
              loading={loadingVms}
              options={options}
              value={selected}
              onChange={(value: string) => {
                setSelected(value);
                writeChoice(accountId, value);
              }}
            />
          </Box>
          <Box sx={{ flex: 1, minWidth: 0 }}>
            <Select size='sm' clearable={false} options={RANGES} value={range} onChange={(value: string) => setRange(value)} />
          </Box>
        </Box>
      </Box>
      {notConfigured ? (
        <Banner
          id='vm-summary-metrics-not-connected'
          tone='info'
          surface='section'
          title='Metrics are not connected'
          message='Connect a Prometheus metrics store to see CPU and memory for this account’s VMs.'
          actions={[
            {
              id: 'vm-summary-metrics-connect',
              label: 'Connect metrics',
              tone: 'primary',
              onClick: () => router.push(OBSERVABILITY_INTEGRATIONS_URL),
            },
          ]}
        />
      ) : metricsFailure ? (
        <Banner id='vm-summary-metrics-failure' tone='critical' surface='section' title='Metrics unavailable' message={metricsFailure} />
      ) : error || noData || (!loadingVms && !vm) ? (
        <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500] }}>
          {error || noData || 'No VM is reporting metrics yet. Install the node agent on a VM and point its metrics at the connected store.'}
        </Typography>
      ) : (
        <Box sx={{ display: 'flex', gap: ds.space[4], opacity: loadingMetrics ? 0.6 : 1 }}>
          <Column title='CPU' resource={cpu} format={(value) => (value === undefined ? '—' : `${formatCores(value)} cores`)} />
          <Column title='Memory' resource={memory} format={formatBytes} />
        </Box>
      )}
    </Card>
  );
};

export default VmUtilizationCard;
