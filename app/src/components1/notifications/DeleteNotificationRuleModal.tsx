import React, { useState } from 'react';
import { Box, Typography } from '@mui/material';
import apiNotifications from '@api1/notification';
import { colors } from 'src/utils/colors';
import { Modal } from '@components1/common/modal';
import CustomButton from '@components1/common/NewCustomButton';
import { snackbar } from '@components1/common/snackbarService';
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
        contentStyles={{ padding: '0px' }}
        rightComponentOnTitle={''}
        loader={isDeleting}
      >
        <Box sx={{ m: '24px 24px 12px 40px' }}>
          <Typography sx={{ fontSize: '14px', fontWeight: 500, color: colors.text.tertiary }}>
            Are you sure you want to Delete Notification?
          </Typography>
          <Typography sx={{ fontSize: '14px', fontWeight: 600, color: colors.text.dark }}>{ruleData.name}</Typography>
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
          <CustomButton id={'cancel'} size='Medium' onClick={() => handleClose()} variant='secondary' text={'Cancel'} disabled={isDeleting} />

          <CustomButton id={'submit'} size='Medium' onClick={() => handleConfirmDelete()} text={'Delete'} loading={isDeleting} />
        </Box>
      </Modal>
    </>
  );
};

export default DeleteNotificationRuleModal;
