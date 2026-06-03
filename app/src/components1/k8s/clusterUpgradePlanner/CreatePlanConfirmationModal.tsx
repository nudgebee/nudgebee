import React from 'react';
import { Box, Typography } from '@mui/material';
import { Modal } from '@components1/ds/Modal';
import { Button as DsButton } from '@components1/ds/Button';
import { ds } from '@utils/colors';

interface CreatePlanConfirmationModalProps {
  open: boolean;
  handleClose: () => void;
  onConfirm: () => void;
  isLoading?: boolean;
}

const CreatePlanConfirmationModal: React.FC<CreatePlanConfirmationModalProps> = ({ open = false, handleClose, onConfirm, isLoading = false }) => {
  return (
    <Modal width='xs' open={open} handleClose={handleClose} title='Confirm Create Plan' contentStyles={{ padding: '0px' }}>
      <Box sx={{ m: 'var(--ds-space-5) var(--ds-space-5) var(--ds-space-3) var(--ds-space-6)' }}>
        <Typography
          sx={{ fontSize: 'var(--ds-text-body-lg)', fontWeight: 'var(--ds-font-weight-medium)', color: 'var(--ds-gray-600)', mb: ds.space[4] }}
        >
          Creating a new upgrade plan will refresh the task owners and statuses
        </Typography>

        <Typography
          sx={{ fontSize: 'var(--ds-text-body-lg)', fontWeight: 'var(--ds-font-weight-medium)', color: 'var(--ds-gray-600)', mt: ds.space[4] }}
        >
          Are you sure you want to proceed?
        </Typography>
      </Box>
      <Box
        sx={{
          padding: 'var(--ds-space-4) var(--ds-space-5)',
          display: 'flex',
          justifyContent: 'flex-end',
          gap: 'var(--ds-space-3)',
          button: {
            minWidth: ds.space.mul(1, 35),
          },
        }}
      >
        <DsButton id='cancel' tone='secondary' size='md' onClick={handleClose} disabled={isLoading}>
          Cancel
        </DsButton>
        <DsButton id='confirm' tone='primary' size='md' onClick={onConfirm} loading={isLoading}>
          Create Plan
        </DsButton>
      </Box>
    </Modal>
  );
};

export default CreatePlanConfirmationModal;
