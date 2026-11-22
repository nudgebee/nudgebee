import React from 'react';
import { Modal } from '@ui/Modal';
import type { VmResource } from '@api1/vm';
import VmMetrics from './VmMetrics';
import { vmPrivateIp } from './vmMetricsData';

interface VmMetricsDialogProps {
  accountId: string;
  /** The VM whose history to show; the dialog is closed while this is null. */
  vm: VmResource | null;
  onClose: () => void;
}

/** CPU, memory and disk history plus the per-service table for one VM, opened from its gauges. */
const VmMetricsDialog = ({ accountId, vm, onClose }: VmMetricsDialogProps) => (
  <Modal
    width='lg'
    open={Boolean(vm)}
    handleClose={onClose}
    title={vm ? `${vm.name} metrics` : 'Metrics'}
    subtitle={vmPrivateIp(vm?.meta) || vm?.resourse_id}
  >
    {vm ? <VmMetrics accountId={accountId} vm={vm} /> : null}
  </Modal>
);

export default VmMetricsDialog;
