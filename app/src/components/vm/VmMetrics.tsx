import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Box, Typography } from '@mui/material';
import Chart from '@ui/Chart';
import { Button } from '@ui/Button';
import { ProgressLinear } from '@ui/ProgressLinear';
import { Skeleton } from '@ui/Skeleton';
import apiVm, { VmMetricResult, VmResource } from '@api1/vm';
import { CellText, useLatestRequest } from './common';
import { useVmServiceRepos, VmServiceGitModal } from './VmServiceGit';
import {
  cpuChart,
  diskIoChart,
  emptyReasons,
  formatBytes,
  formatCores,
  hasAnyData,
  latest,
  memoryChart,
  ServiceRow,
  serviceRows,
} from './vmMetricsData';
import { ds } from '@utils/colors';

const RANGES = [
  { label: '1h', ms: 60 * 60 * 1000 },
  { label: '6h', ms: 6 * 60 * 60 * 1000 },
  { label: '24h', ms: 24 * 60 * 60 * 1000 },
];

interface VmMetricsProps {
  accountId: string;
  vm: VmResource;
}

const Panel = ({ title, subtitle, children }: { title: string; subtitle?: string; children: React.ReactNode }) => (
  <Box sx={{ border: `1px solid ${ds.gray[200]}`, borderRadius: ds.radius.md, padding: ds.space[4], minWidth: 0 }}>
    <Typography sx={{ fontSize: ds.text.small, fontWeight: ds.weight.semibold, color: ds.gray[700] }}>{title}</Typography>
    {subtitle ? <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500], marginBottom: ds.space[2] }}>{subtitle}</Typography> : null}
    {children}
  </Box>
);

/**
 * What the dialog shows on first open, before any data has arrived: placeholders the shape of the
 * charts and the service table. The queries go through a forager and take a few seconds, so an empty
 * dialog reads as broken.
 */
const LoadingPlaceholder = ({ vmName }: { vmName: string }) => (
  <Box role='status' aria-busy='true' aria-label='Loading metrics' sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[4] }}>
    <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500] }}>Loading metrics for {vmName}…</Typography>
    <Box sx={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(320px, 1fr))', gap: ds.space[4] }}>
      {['CPU', 'Memory', 'Disk I/O'].map((title) => (
        <Panel key={title} title={title}>
          <Skeleton shape='rect' width='100%' height={180} />
        </Panel>
      ))}
    </Box>
    <Panel title='Services' subtitle='Latest value per service'>
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[2] }}>
        <Skeleton shape='text' size='text' width='100%' />
        <Skeleton shape='text' size='text' width='100%' />
        <Skeleton shape='text' size='text' width='100%' />
      </Box>
    </Panel>
  </Box>
);

/** CPU, memory, disk and per-service metrics for one self-hosted VM, shown in the VM row's expandable area. */
const VmMetrics = ({ accountId, vm }: VmMetricsProps) => {
  const [rangeMs, setRangeMs] = useState(RANGES[0].ms);
  const [results, setResults] = useState<VmMetricResult[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>();
  const latestRequest = useLatestRequest();

  // The parent can hand us a fresh `vm` object on every render (polling, unrelated
  // state). Read it through a ref and refetch only when the VM itself or the range
  // changes, so a re-render does not become a metrics query.
  const vmRef = useRef(vm);
  useEffect(() => {
    vmRef.current = vm;
  }, [vm]);

  const load = useCallback(async () => {
    const isCurrent = latestRequest();
    setLoading(true);
    setError(undefined);
    try {
      const end = Date.now();
      const data = await apiVm.getVmMetrics({ accountId, vm: vmRef.current, startDate: end - rangeMs, endDate: end });
      if (!isCurrent()) return;
      setResults(data);
    } catch (err: any) {
      if (!isCurrent()) return;
      setResults([]);
      setError(err?.message || 'Failed to load VM metrics');
    } finally {
      if (isCurrent()) setLoading(false);
    }
  }, [accountId, vm.id, rangeMs, latestRequest]);

  useEffect(() => {
    load();
  }, [load]);

  const cpu = useMemo(() => cpuChart(results), [results]);
  const memory = useMemo(() => memoryChart(results), [results]);
  const diskIo = useMemo(() => diskIoChart(results), [results]);
  const services = useMemo(() => serviceRows(results), [results]);
  const { links, reload: reloadLinks } = useVmServiceRepos(vm.id);
  const [repoService, setRepoService] = useState<ServiceRow | null>(null);
  // A repository set here wins over the one the service declares itself.
  const repoFor = (row: ServiceRow) =>
    links[row.containerId]?.repo ? links[row.containerId] : row.agentRepo ? { repo: row.agentRepo, branch: row.agentBranch || '' } : undefined;
  const reasons = useMemo(() => emptyReasons(results), [results]);

  const cores = latest(results, 'cpu_total');
  const memTotal = latest(results, 'mem_total');
  const diskUsed = latest(results, 'disk_used');
  const diskTotal = latest(results, 'disk_total');

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[4], padding: ds.space[4] }}>
      <Box sx={{ display: 'flex', gap: ds.space[2], alignItems: 'center' }}>
        {RANGES.map((r) => (
          <Button
            key={r.label}
            id={`vm-metrics-range-${r.label}-${vm.id}`}
            size='sm'
            tone={r.ms === rangeMs ? 'primary' : 'secondary'}
            onClick={() => setRangeMs(r.ms)}
          >
            {r.label}
          </Button>
        ))}
        <Button id={`vm-metrics-refresh-${vm.id}`} size='sm' tone='secondary' onClick={load} disabled={loading}>
          Refresh
        </Button>
      </Box>

      {/* Changing the range or pressing Refresh keeps the last charts on screen under a progress bar; the
          placeholder is for the first load only, when there is nothing else to show. */}
      {loading && hasAnyData(results) ? <ProgressLinear mode='indeterminate' tone='info' surface='section' aria-label='Refreshing metrics' /> : null}
      {loading && !hasAnyData(results) && !error ? <LoadingPlaceholder vmName={vm.name} /> : null}

      {error ? (
        <Typography role='alert' sx={{ fontSize: ds.text.small, color: ds.gray[700] }}>
          Could not load metrics for this VM: {error}
        </Typography>
      ) : null}

      {!loading && !error && !hasAnyData(results) ? (
        <Box>
          <Typography sx={{ fontSize: ds.text.small, color: ds.gray[700] }}>
            No metrics for this VM yet. Check that the VM agent is running, that its metrics store is connected to this account, and that the store is
            reachable from a forager.
          </Typography>
          {reasons.map((reason) => (
            <Typography key={reason} sx={{ fontSize: ds.text.caption, color: ds.gray[500] }}>
              {reason}
            </Typography>
          ))}
        </Box>
      ) : null}

      {hasAnyData(results) ? (
        <Box
          aria-busy={loading}
          sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[4], opacity: loading ? 0.55 : 1, transition: 'opacity 150ms' }}
        >
          <Box sx={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(320px, 1fr))', gap: ds.space[4] }}>
            <Panel title='CPU' subtitle={cores !== undefined ? `${cores} cores` : undefined}>
              <Chart.TimeSeries {...cpu} shape='line' format={(v) => v.toFixed(2)} id={`vm-cpu-${vm.id}`} height={180} />
            </Panel>
            <Panel title='Memory' subtitle={memTotal !== undefined ? `${formatBytes(memTotal)} total` : undefined}>
              <Chart.TimeSeries {...memory} shape='line' format={(v) => v.toFixed(2)} id={`vm-mem-${vm.id}`} height={180} />
            </Panel>
            <Panel title='Disk I/O'>
              <Chart.TimeSeries {...diskIo} shape='line' format={(v) => v.toFixed(2)} id={`vm-disk-${vm.id}`} height={180} />
            </Panel>
            <Panel title='Disk space' subtitle='Used and total across disks'>
              <Typography sx={{ fontSize: ds.text.small, color: ds.gray[700] }}>
                {diskUsed === undefined ? '—' : `${formatBytes(diskUsed)} used${diskTotal !== undefined ? ` of ${formatBytes(diskTotal)}` : ''}`}
              </Typography>
            </Panel>
          </Box>

          <Panel title='Services' subtitle='Latest value per service'>
            {services.length ? (
              <Box component='table' sx={{ width: '100%', borderCollapse: 'collapse', fontSize: ds.text.small }}>
                <thead>
                  <tr>
                    {['Service', 'CPU (cores)', 'Memory', 'Disk used', 'Repository'].map((h) => (
                      <Box component='th' key={h} sx={{ textAlign: 'left', color: ds.gray[500], fontWeight: ds.weight.medium, padding: ds.space[1] }}>
                        {h}
                      </Box>
                    ))}
                  </tr>
                </thead>
                <tbody>
                  {services.map((row) => (
                    <tr key={row.containerId}>
                      <Box component='td' sx={{ padding: ds.space[1] }}>
                        <CellText text={row.service} subtext={row.service !== row.containerId ? row.containerId : undefined} mono />
                      </Box>
                      <Box component='td' sx={{ padding: ds.space[1] }}>
                        {formatCores(row.cpuCores)}
                      </Box>
                      <Box component='td' sx={{ padding: ds.space[1] }}>
                        {formatBytes(row.memoryBytes)}
                      </Box>
                      <Box component='td' sx={{ padding: ds.space[1] }}>
                        {formatBytes(row.diskBytes)}
                      </Box>
                      <Box component='td' sx={{ padding: ds.space[1] }}>
                        <Button id={`vm-service-repo-btn-${row.service}`} tone='secondary' size='sm' onClick={() => setRepoService(row)}>
                          {repoFor(row)?.repo.replace(/^https?:\/\//, '') || 'Link repository'}
                        </Button>
                      </Box>
                    </tr>
                  ))}
                </tbody>
              </Box>
            ) : (
              <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500] }}>No per-service data in this range.</Typography>
            )}
          </Panel>
        </Box>
      ) : null}
      {repoService ? (
        <VmServiceGitModal
          accountId={accountId}
          vmId={vm.id}
          containerId={repoService.containerId}
          serviceName={repoService.service}
          current={repoFor(repoService)}
          onClose={() => setRepoService(null)}
          onSaved={reloadLinks}
        />
      ) : null}
    </Box>
  );
};

export default VmMetrics;
