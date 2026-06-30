import React from 'react';
import { Box, Typography } from '@mui/material';
import CheckCircleOutlineIcon from '@mui/icons-material/CheckCircleOutline';
import ErrorOutlineIcon from '@mui/icons-material/ErrorOutline';
import { Modal } from '@ui/Modal';
import { ds } from '@utils/colors';

export interface SsmCommandResult {
  success: boolean;
  message: string;
}

interface SsmCommandResultDialogProps {
  open: boolean;
  result: SsmCommandResult | null;
  onClose: () => void;
}

// Displays the output returned by an SSM Run Command execution. The backend
// (aws_ssm.go, with wait_for_results) aggregates per-instance stdout/stderr into
// the `message` field; a snackbar can't render that, so we show it here in a
// scrollable monospace block.
const SsmCommandResultDialog: React.FC<SsmCommandResultDialogProps> = ({ open, result, onClose }) => {
  if (!result) return null;

  const { success, message } = result;

  return (
    <Modal open={open} onClose={onClose} width='md' title='SSM Command Result' confirmText='Close' onConfirm={onClose} isCancelRequired={false}>
      <Box>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2], mb: ds.space[3] }}>
          {success ? (
            <CheckCircleOutlineIcon sx={{ color: ds.green[600], fontSize: 20 }} />
          ) : (
            <ErrorOutlineIcon sx={{ color: ds.red[600], fontSize: 20 }} />
          )}
          <Typography sx={{ fontWeight: ds.weight.semibold, color: success ? ds.green[600] : ds.red[600] }}>
            {success ? 'Command completed' : 'Command failed'}
          </Typography>
        </Box>

        <Box
          data-testid='ssm-command-output'
          sx={{
            fontFamily: ds.font.mono,
            fontSize: ds.text.small,
            whiteSpace: 'pre-wrap',
            wordBreak: 'break-word',
            backgroundColor: ds.background[200],
            border: `1px solid ${ds.gray[300]}`,
            borderRadius: ds.radius.md,
            p: ds.space[3],
            maxHeight: '420px',
            overflow: 'auto',
          }}
        >
          {message || 'No output returned.'}
        </Box>
      </Box>
    </Modal>
  );
};

export default SsmCommandResultDialog;
