import React, { useState } from 'react';
import { Box, Typography } from '@mui/material';
import apiNotifications from '@api1/notification';
import { ds } from 'src/utils/colors';
import { Modal } from '@components1/ds/Modal';
import { Button } from '@components1/ds/Button';
import { toast as snackbar } from '@components1/ds/Toast';
interface DeleteNotificationRuleModalProps {
  open: boolean;
  handleClose: () => void;
  ruleData: any;
  listNotificationRules: () => void;
}

const DeleteNotificationRuleModal: React.FC<DeleteNotificationRuleModalProps> = ({ open = false, handleClose, ruleData, listNotificationRules }) => {
  const [isDeleting, setIsDeleting] = useState(false);

  const handleConfirmDelete = () => {
    setIsDeleting(true);
    apiNotifications
      .deleteNotificationRule(ruleData.id)
      .then((res: any) => {
        if (res?.data?.data.notification_rule_delete) {
          snackbar.success('Notification deleted successfully');
          listNotificationRules();
          handleClose();
        } else {
          snackbar.error('Something went wrong while deleting notification');
          handleClose();
        }
      })
      .catch(() => {
        snackbar.error('Something went wrong while deleting');
        handleClose();
      })
      .finally(() => {
        setIsDeleting(false);
      });
  };
  return (
    <>
      <Modal
        width='xs'
        open={open}
        handleClose={handleClose}
        title={`Confirm Delete`}
        contentStyles={{ padding: 'var(--ds-space-1)' }}
        rightComponentOnTitle={''}
        loader={isDeleting}
      >
        <Box sx={{ m: 'var(--ds-space-5) var(--ds-space-5) var(--ds-space-3) var(--ds-space-6)' }}>
          <Typography sx={{ fontSize: 'var(--ds-text-body-lg)', fontWeight: 'var(--ds-font-weight-medium)', color: ds.gray[600] }}>
            Are you sure you want to Delete Notification?
          </Typography>
          <Typography sx={{ fontSize: 'var(--ds-text-body-lg)', fontWeight: 'var(--ds-font-weight-semibold)', color: ds.gray[700] }}>
            {ruleData.name}
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
          <Button id='cancel' size='md' tone='secondary' onClick={() => handleClose()} disabled={isDeleting}>
            Cancel
          </Button>
          <Button id='submit' size='md' onClick={() => handleConfirmDelete()} loading={isDeleting}>
            Delete
          </Button>
        </Box>
      </Modal>
    </>
  );
};

export default DeleteNotificationRuleModal;
