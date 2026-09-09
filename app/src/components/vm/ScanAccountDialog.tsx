import React, { useState } from 'react';
import { Box, Typography } from '@mui/material';
import PropTypes from 'prop-types';
import { Modal } from '@ui/Modal';
import { Button } from '@ui/Button';
import { toast as snackbar } from '@ui/Toast';
import apiVm from '@api1/vm';
import { ds } from '@utils/colors';

interface ScanAccountDialogProps {
  open: boolean;
  accountId: string;
  onClose: (started: boolean) => void;
}

/**
 * Confirms and triggers an account-wide scan. Unlike ScanVmDialog, there's no
 * per-VM datasource to pick — the backend resolves every discovery
 * datasource that targets this account and queues a scan for each; it errors
 * clearly if none is configured, so no precheck is needed here.
 */
const ScanAccountDialog = ({ open, accountId, onClose }: ScanAccountDialogProps) => {
  const [submitting, setSubmitting] = useState(false);

  const handleSubmit = () => {
    setSubmitting(true);
    apiVm
      .scanVmAccount({ accountId })
      .then(() => {
        // The RPC only acknowledges the start — sweep, inventory and CVE match
        // run detached server-side, so don't promise results here.
        snackbar.success('Account scan started. Results appear as each instance finishes.');
        onClose(true);
      })
      .catch((error) => {
        snackbar.error(error?.message || 'Failed to start account scan');
      })
      .finally(() => {
        setSubmitting(false);
      });
  };

  return (
    <Modal
      width='sm'
      open={open}
      handleClose={submitting ? () => {} : () => onClose(false)}
      title='Scan account for vulnerabilities'
      loader={submitting}
    >
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[4] }}>
        <Typography sx={{ fontSize: ds.text.small, color: ds.gray[600] }}>
          Collects the installed package inventory from every reachable instance in this account through its proxy agent(s) and matches it against the
          vulnerability database. The scan runs in the background.
        </Typography>

        <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: ds.space[2] }}>
          <Button id='vm-scan-account-cancel' tone='secondary' size='md' onClick={() => onClose(false)} disabled={submitting}>
            Cancel
          </Button>
          <Button id='vm-scan-account-submit' tone='primary' size='md' onClick={handleSubmit} disabled={submitting}>
            Start scan
          </Button>
        </Box>
      </Box>
    </Modal>
  );
};

ScanAccountDialog.propTypes = {
  open: PropTypes.bool.isRequired,
  accountId: PropTypes.string.isRequired,
  onClose: PropTypes.func.isRequired,
};

export default ScanAccountDialog;
