import apiKubernetes1 from '@api1/kubernetes1';
import { Select } from '@ui/Select';
import { Modal } from '@ui/Modal';
import { toast as snackbar } from '@ui/Toast';
import { Box, Typography } from '@mui/material';
import { useState } from 'react';
import { ds } from '@utils/colors';
import PropTypes from 'prop-types';

const UpdateEvent = ({ selectedEvent, handlePopupClose, isUpdateEvent, onUpdated }) => {
  const [updatedUrgency, setUpdatedUrgency] = useState(selectedEvent?.urgency);
  const [updateEventLoading, setUpdateEventLoading] = useState(false);

  const handleSubmit = () => {
    setUpdateEventLoading(true);
    apiKubernetes1
      .updateEvent({
        eventId: selectedEvent.id,
        urgency: updatedUrgency,
      })
      .then((res) => {
        if (res?.data?.errors) {
          snackbar.error(`Failed to update event: ${res.data.errors[0].message}`);
        } else if (res?.data?.data?.event_update) {
          snackbar.success(`Event ${selectedEvent.title} Updated`);
          handlePopupClose();
          onUpdated?.();
        } else {
          snackbar.error(`Failed to update event: ${res.message || 'An unknown error occurred'}`);
        }
      })
      .catch((err) => {
        snackbar.error(`Failed to update event: ${err.message}`);
      })
      .finally(() => {
        setUpdateEventLoading(false);
      });
  };

  return (
    <Modal
      open={isUpdateEvent}
      handleClose={handlePopupClose}
      title='Update event urgency'
      loader={updateEventLoading}
      confirmText='Update'
      onConfirm={handleSubmit}
      confirmDisabled={!updatedUrgency || updatedUrgency === selectedEvent?.urgency}
    >
      <Typography variant='body1' mb={3}>
        Set the urgency of <strong>{selectedEvent.title}</strong>. This helps prioritize the event for further investigation.
      </Typography>
      <Box sx={{ maxWidth: ds.space.mul(0, 100) }}>
        <Select
          label='Urgency'
          required
          value={updatedUrgency}
          options={['HIGH', 'MEDIUM', 'LOW', 'DEBUG', 'INFO']}
          onChange={(v) => {
            setUpdatedUrgency(v);
          }}
        />
      </Box>
    </Modal>
  );
};

UpdateEvent.propTypes = {
  selectedEvent: PropTypes.shape({
    id: PropTypes.string.isRequired,
    title: PropTypes.string.isRequired,
    urgency: PropTypes.string,
  }).isRequired,
  handlePopupClose: PropTypes.func.isRequired,
  isUpdateEvent: PropTypes.bool.isRequired,
  onUpdated: PropTypes.func,
};

export default UpdateEvent;
