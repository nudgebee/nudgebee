// Lightweight confirm dialog — Phase 2.5 (NB-30989).
//
// Wraps the DS Modal with a focused "title + message + Cancel/Confirm bar".
// Used by Manual Dependencies for destructive actions (single-row delete,
// panic-button "Delete all"). Kept local to the knowledge-graph folder so
// the destructive-action UX stays consistent across this feature; if other
// surfaces need the same primitive later, lift to @common-new.

import { Box, Typography } from '@mui/material';
import PropTypes from 'prop-types';
import { Modal } from '@components1/ds/Modal';
import { Button } from '@components1/ds/Button';
import { ds } from 'src/utils/colors';

const ConfirmDialog = ({
  open,
  title,
  message,
  confirmLabel = 'Confirm',
  cancelLabel = 'Cancel',
  danger = false,
  submitting = false,
  onConfirm,
  onClose,
}) => (
  <Modal width='sm' title={title} open={open} handleClose={onClose} onClose={onClose}>
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2, p: 2 }}>
      <Typography sx={{ fontSize: '13px', color: ds?.text?.secondary ?? '#374151', lineHeight: 1.5 }}>{message}</Typography>
      <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: 1, pt: 0.5 }}>
        <Button tone='secondary' size='md' onClick={onClose} disabled={submitting}>
          {cancelLabel}
        </Button>
        <Button tone={danger ? 'danger' : 'primary'} size='md' onClick={onConfirm} disabled={submitting} loading={submitting}>
          {confirmLabel}
        </Button>
      </Box>
    </Box>
  </Modal>
);

ConfirmDialog.propTypes = {
  open: PropTypes.bool.isRequired,
  title: PropTypes.string.isRequired,
  message: PropTypes.node.isRequired,
  confirmLabel: PropTypes.string,
  cancelLabel: PropTypes.string,
  danger: PropTypes.bool,
  submitting: PropTypes.bool,
  onConfirm: PropTypes.func.isRequired,
  onClose: PropTypes.func.isRequired,
};

export default ConfirmDialog;
