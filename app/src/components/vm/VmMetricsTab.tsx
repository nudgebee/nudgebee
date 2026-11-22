import React, { useEffect, useMemo, useState } from 'react';
import { Box, Typography } from '@mui/material';
import { useRouter } from 'next/router';
import { Banner } from '@ui/Banner';
import { EmptyState } from '@ui/EmptyState';
import { Select } from '@ui/Select';
import Loader from '@shared/Loader';
import apiVm, { VmMetricsStatus, VmResource } from '@api1/vm';
import VmMetrics from './VmMetrics';
import { listAllVms } from './VmUtilizationCard';
import { OBSERVABILITY_INTEGRATIONS_URL, useLatestRequest } from './common';
import { ds } from '@utils/colors';

/**
 * CPU, memory, disk I/O and disk usage of one self-hosted VM, as a tab of its own.
 *
 * A VM account has no agent of its own, so metrics come from a store the user
 * connected (Prometheus and friends). Like the Traces tab, this says so when nothing
 * is connected — and keeps "could not check" apart from "not connected", so the user
 * does not go connect a store that is already there.
 */

interface VmMetricsTabProps {
  accountId: string;
}

const storageKey = (accountId: string) => `vm-metrics-tab-instance:${accountId}`;

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

const VmMetricsTab = ({ accountId }: VmMetricsTabProps) => {
  const router = useRouter();
  const [status, setStatus] = useState<VmMetricsStatus>();
  const [statusError, setStatusError] = useState<string>();
  const [vms, setVms] = useState<VmResource[]>([]);
  const [loadingVms, setLoadingVms] = useState(false);
  const [vmsError, setVmsError] = useState<string>();
  const [selected, setSelected] = useState('');
  const beginStatus = useLatestRequest();
  const beginVms = useLatestRequest();

  useEffect(() => {
    const isLatest = beginStatus();
    setStatus(undefined);
    setStatusError(undefined);
    apiVm
      .getMetricsStatus(accountId)
      .then((res) => isLatest() && setStatus(res))
      .catch((err) => isLatest() && setStatusError(err?.message || 'Failed to check metrics'));
  }, [accountId, beginStatus]);

  const connected = !!status && status.state !== 'not_configured' && status.state !== 'check_failed';

  // Only VMs a node-agent is reporting for: a VM with no metrics has nothing to show.
  useEffect(() => {
    if (!connected) return;
    const isLatest = beginVms();
    setLoadingVms(true);
    setVmsError(undefined);
    setVms([]);
    setSelected('');
    (async () => {
      try {
        const rows = await listAllVms(accountId);
        const fleet = await apiVm.getVmFleetMetrics({ accountId, vms: rows });
        if (!isLatest()) return;
        const reporting = rows.filter((vm) => fleet[vm.id]);
        setVms(reporting);
        const remembered = readChoice(accountId);
        setSelected(reporting.find((vm) => vm.id === remembered)?.id || reporting[0]?.id || '');
      } catch (err: any) {
        if (isLatest()) setVmsError(err?.message || 'Failed to load VMs');
      } finally {
        if (isLatest()) setLoadingVms(false);
      }
    })();
  }, [accountId, connected, beginVms]);

  const vm = useMemo(() => vms.find((candidate) => candidate.id === selected), [vms, selected]);
  const options = vms.map((candidate) => ({ value: candidate.id, label: candidate.name || candidate.resourse_id }));

  if (!status && !statusError) {
    return (
      <Box sx={{ display: 'flex', justifyContent: 'center', alignItems: 'center', minHeight: '40vh' }}>
        <Loader />
      </Box>
    );
  }

  if (statusError || status?.state === 'check_failed') {
    return (
      <Box sx={{ p: ds.space[4] }}>
        <Banner
          id='vm-metrics-tab-failure'
          tone='critical'
          surface='section'
          title='Could not check the metrics integration'
          message={statusError || status?.error || 'Failed to check metrics'}
        />
      </Box>
    );
  }

  if (status?.state === 'not_configured') {
    return (
      <Box sx={{ p: ds.space[4] }}>
        <EmptyState
          title='No metrics source connected'
          description='Connect a metrics store (Prometheus, VictoriaMetrics and others) in Integrations to see CPU, memory and disk usage of the VMs in this account.'
          action={{ label: 'Open Integrations', onClick: () => router.push(OBSERVABILITY_INTEGRATIONS_URL) }}
        />
      </Box>
    );
  }

  const picker = (
    <Box sx={{ width: 360, maxWidth: '100%' }}>
      <Select
        id='vm-metrics-tab-vm-picker'
        label='Virtual machine'
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
  );

  return (
    <Box sx={{ p: ds.space[4], display: 'flex', flexDirection: 'column', gap: ds.space[3] }}>
      {status?.state === 'unreachable' ? (
        <Banner
          id='vm-metrics-tab-unreachable'
          tone='warning'
          surface='section'
          title='Metrics store unreachable'
          message={status.error || 'The metrics store could not be reached.'}
        />
      ) : null}
      {vm ? (
        <VmMetrics accountId={accountId} vm={vm} leading={picker} />
      ) : (
        <>
          {picker}
          {!loadingVms ? (
            <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500] }}>
              {vmsError || 'No VM is reporting metrics yet. Install the node agent on a VM and point its metrics at the connected store.'}
            </Typography>
          ) : null}
        </>
      )}
    </Box>
  );
};

export default VmMetricsTab;
