import * as React from 'react';
import DialogActions from '@mui/material/DialogActions';
import DialogContent from '@mui/material/DialogContent';
import DialogContentText from '@mui/material/DialogContentText';
import { Box } from '@mui/material';
import CustomButton from '@common/NewCustomButton';
import { Modal } from './index';

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
          <DialogContent sx={{ padding: '24px' }}>
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
          <DialogActions sx={{ px: '24px', my: '20px', button: { minWidth: '140px' } }}>
            {isCancelRequired && (
              <CustomButton variant='secondary' text='Cancel' onClick={handleClose} size='Medium' id='cancel' type='button' disabled={loading} />
            )}
            {isSubmitRequired && (
              <CustomButton text={buttonText} onClick={handleSubmit} disabled={disabled || loading} size='Medium' id='submit' type='button' />
            )}
          </DialogActions>
        )}
      </Modal>
    </React.Fragment>
  );
}
