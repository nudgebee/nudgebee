import * as React from 'react';
import DialogActions from '@mui/material/DialogActions';
import DialogContent from '@mui/material/DialogContent';
import DialogContentText from '@mui/material/DialogContentText';
import { Box } from '@mui/material';
import { Button } from '@components1/ds/Button';
import { Modal } from '@components1/common/modal';

interface NDialogProps {
  open: boolean;
  buttonText?: string;
  dialogTitle: React.ReactNode;
  dialogContent: React.ReactNode;
  handleClose?: () => void;
  handleSubmit?: () => void;
  additionalComponent: any;
  disabled?: boolean;
  loading?: boolean;
  isSubmitRequired?: boolean;
  isCancelRequired?: boolean;
  sx?: React.CSSProperties;
  backdropClickClose?: boolean;
  width?: 'xs' | 'sm' | 'md' | 'lg' | 'xl';
}

export default function NDialog({
  open,
  buttonText,
  dialogTitle,
  dialogContent,
  handleClose,
  handleSubmit,
  additionalComponent,
  disabled = false,
  loading = false,
  isSubmitRequired = true,
  isCancelRequired = true,
  backdropClickClose = true,
  width = 'md',
}: NDialogProps) {
  return (
    <React.Fragment>
      <Modal
        open={open}
        handleClose={(_event, reason) => {
          if (!backdropClickClose) {
            if (reason === 'backdropClick' || reason === 'escapeKeyDown') {
              return;
            }
          }
          handleClose?.();
        }}
        width={width}
        title={dialogTitle}
        loader={loading}
      >
        {dialogContent ? (
          <DialogContent sx={{ padding: 'var(--ds-space-5)' }}>
            <DialogContentText id='alert-dialog-description'>{dialogContent}</DialogContentText>
          </DialogContent>
        ) : (
          <></>
        )}
        {!!additionalComponent && (
          <Box
            px='24px'
            sx={{
              '& ::-webkit-scrollbar': {
                display: 'none',
              },
            }}
          >
            {additionalComponent}
          </Box>
        )}

        {(isCancelRequired || isSubmitRequired) && (
          <DialogActions sx={{ px: 'var(--ds-space-5)', my: 'var(--ds-space-4)', gap: 'var(--ds-space-3)', button: { minWidth: '140px' } }}>
            {isCancelRequired && (
              <Button tone='secondary' onClick={handleClose} size='md' id='cancel' type='button' disabled={loading}>
                Cancel
              </Button>
            )}
            {isSubmitRequired && (
              <Button tone='primary' onClick={handleSubmit} disabled={disabled || loading} loading={loading} size='md' id='submit' type='button'>
                {buttonText}
              </Button>
            )}
          </DialogActions>
        )}
      </Modal>
    </React.Fragment>
  );
}
