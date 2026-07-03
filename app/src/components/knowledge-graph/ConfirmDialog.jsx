// Lightweight confirm dialog — Phase 2.5 (NB-30989).
//
// Wraps the DS Modal with a focused "title + message + Cancel/Confirm bar".
// Used by Manual Dependencies for destructive actions (single-row delete,
// panic-button "Delete all"). Kept local to the knowledge-graph folder so
// the destructive-action UX stays consistent across this feature; if other
// surfaces need the same primitive later, lift to @common.

import { Box, Typography } from '@mui/material';
import PropTypes from 'prop-types';
import { Modal } from '@ui/Modal';
import { Button } from '@ui/Button';
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
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[4], p: ds.space[4] }}>
      <Typography sx={{ fontSize: ds.text.body, color: ds.gray[700], lineHeight: 1.5 }}>{message}</Typography>
      <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: ds.space[2], pt: ds.space[1] }}>
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
