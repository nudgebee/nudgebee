import React, { useEffect, useState } from 'react';
import { Box, Typography } from '@mui/material';
import PropTypes from 'prop-types';
import { Modal } from '@ui/Modal';
import { Button } from '@ui/Button';
import FilterDropdown from '@ui/FilterDropdown';
import { toast as snackbar } from '@ui/Toast';
import apiVm, { VmResource, VmSshTarget } from '@api1/vm';
import { ds } from '@utils/colors';

interface ScanVmDialogProps {
  open: boolean;
  accountId: string;
  vm: VmResource | null;
  onClose: (started: boolean) => void;
}

/**
 * Picks the forager-reachable SSH target to run a VM's inventory scan through.
 *
 * The scan RPC needs a `datasource_id` on top of the VM's `cloud_resource_id`:
 * the resource row says *which asset* to record findings against, the datasource
 * says *how to reach it*. Nothing in the data model links the two yet — VM
 * identity/merge is the open part of the discovery epic — so the operator picks.
 */
const ScanVmDialog = ({ open, accountId, vm, onClose }: ScanVmDialogProps) => {
  const [targets, setTargets] = useState<VmSshTarget[]>([]);
  const [loadingTargets, setLoadingTargets] = useState(false);
  const [selectedTarget, setSelectedTarget] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    if (!open || !accountId) return undefined;
    let cancelled = false;
    setLoadingTargets(true);
    setSelectedTarget(null);
    apiVm
      .listSshTargets(accountId)
      .then((rows) => {
        if (cancelled) return;
        setTargets(rows);
        // One target is the common case for a small fleet — preselect it so the
        // dialog is a single confirm click.
        if (rows.length === 1) setSelectedTarget(rows[0].id);
      })
      .catch((error) => {
        if (cancelled) return;
        console.error('Failed to list SSH targets:', error);
        setTargets([]);
      })
      .finally(() => {
        if (!cancelled) setLoadingTargets(false);
      });
    return () => {
      cancelled = true;
    };
  }, [open, accountId]);

  const handleSubmit = () => {
    if (!vm || !selectedTarget) return;
    setSubmitting(true);
    apiVm
      .scanVm({ accountId, datasourceId: selectedTarget, cloudResourceId: vm.id })
      .then(() => {
        // The RPC only acknowledges the start — the pull, parse and CVE match
        // run detached server-side, so don't promise results here.
        snackbar.success(`Scan started for ${vm.name || vm.resourse_id}. Results appear once it finishes.`);
        onClose(true);
      })
      .catch((error) => {
        snackbar.error(error?.message || 'Failed to start VM scan');
      })
      .finally(() => {
        setSubmitting(false);
      });
  };

  const options = targets.map((target) => ({
    label: target.host ? `${target.name} (${target.host})` : target.name,
    value: target.id,
  }));

  return (
    <Modal width='sm' open={open} handleClose={submitting ? () => {} : () => onClose(false)} title='Scan VM for vulnerabilities' loader={submitting}>
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[4] }}>
        <Typography sx={{ fontSize: ds.text.small, color: ds.gray[600] }}>
          Collects the installed package inventory from <strong>{vm?.name || vm?.resourse_id || 'this VM'}</strong> through a proxy agent and matches
          it against the vulnerability database. The scan runs in the background.
        </Typography>

        <FilterDropdown
          id='vm-scan-datasource'
          label='Reach through'
          value={selectedTarget}
          options={options}
          isOptionsLoading={loadingTargets}
          required
          onSelect={(event: any) => setSelectedTarget(event?.target?.value || null)}
        />

        {!loadingTargets && targets.length === 0 && (
          <Typography sx={{ fontSize: ds.text.caption, color: ds.red[600] }}>
            No agent-reachable SSH connection is configured for this account. Add an SSH integration in <em>VM agent</em> connection mode first.
          </Typography>
        )}

        <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: ds.space[2] }}>
          <Button id='vm-scan-cancel' tone='secondary' size='md' onClick={() => onClose(false)} disabled={submitting}>
            Cancel
          </Button>
          <Button id='vm-scan-submit' tone='primary' size='md' onClick={handleSubmit} disabled={submitting || !selectedTarget}>
            Start scan
          </Button>
        </Box>
      </Box>
    </Modal>
  );
};

ScanVmDialog.propTypes = {
  open: PropTypes.bool.isRequired,
  accountId: PropTypes.string.isRequired,
  vm: PropTypes.object,
  onClose: PropTypes.func.isRequired,
};

export default ScanVmDialog;
