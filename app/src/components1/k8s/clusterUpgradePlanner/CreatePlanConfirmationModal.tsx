import React from 'react';
import { Box, Typography } from '@mui/material';
import { colors } from 'src/utils/colors';
import { Modal } from '@common-new/modal';
import { Button as DsButton } from '@components1/ds/Button';

interface CreatePlanConfirmationModalProps {
  open: boolean;
  handleClose: () => void;
  onConfirm: () => void;
  isLoading?: boolean;
}

const CreatePlanConfirmationModal: React.FC<CreatePlanConfirmationModalProps> = ({ open = false, handleClose, onConfirm, isLoading = false }) => {
  return (
    <Modal width='xs' open={open} handleClose={handleClose} title='Confirm Create Plan' contentStyles={{ padding: '0px' }}>
      <Box sx={{ m: '24px 24px 12px 40px' }}>
        <Typography sx={{ fontSize: '14px', fontWeight: 500, color: colors.text.tertiary, mb: 2 }}>
          Creating a new upgrade plan will refresh the task owners and statuses
        </Typography>

        <Typography sx={{ fontSize: '14px', fontWeight: 500, color: colors.text.tertiary, mt: 2 }}>Are you sure you want to proceed?</Typography>
      </Box>
      <Box
        sx={{
          padding: '16px 24px',
          display: 'flex',
          justifyContent: 'flex-end',
          gap: '12px',
          button: {
            minWidth: '140px',
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
