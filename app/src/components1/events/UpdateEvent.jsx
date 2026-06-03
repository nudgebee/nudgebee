import apiKubernetes1 from '@api1/kubernetes1';
import CustomDropdown from '@components1/common/CustomDropdown';
import CustomButton from '@components1/common/NewCustomButton';
import { Modal } from '@components1/common/modal';
import { snackbar } from '@components1/common/snackbarService';
import { Box, Typography } from '@mui/material';
import { useState } from 'react';
import { colors } from 'src/utils/colors';
import PropTypes from 'prop-types';

const UpdateEvent = ({ selectedEvent, handlePopupClose, isUpdateEvent }) => {
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
        } else {
          snackbar.error(`Failed to update event: ${res.message || 'An unknown error occurred'}`);
        }
      })
      .catch((err) => {
        snackbar.error(`Failed to update event: ${err.message}`);
      })
      .finally(() => {
        setUpdateEventLoading(false);
        handlePopupClose();
      });
  };

  return (
    <Modal
      width='md'
      open={isUpdateEvent}
      handleClose={() => {
        handlePopupClose();
      }}
      title={`Update the event "${selectedEvent.title}"`}
      contentStyles={{ padding: '0px' }}
      rightComponentOnTitle={undefined}
      loader={updateEventLoading}
    >
      <Box p={5}>
        <Box key={'1'} display='flex' alignItems='center' width='100%' mb={3} gap={'20px'}>
          <Typography>Urgency</Typography>
          <CustomDropdown
            value={updatedUrgency}
            options={['HIGH', 'MEDIUM', 'LOW', 'DEBUG', 'INFO']}
            onChange={(_, v) => {
              setUpdatedUrgency(v);
            }}
          />
        </Box>
      </Box>

      <Box
        display='flex'
        alignItems='center'
        justifyContent='flex-end'
        gap='12px'
        p='16px 24px'
        sx={{
          borderTop: `0.5px solid ${colors.border.vertical}`,
          button: {
            minWidth: '140px',
          },
        }}
      >
        <CustomButton
          type='button'
          id='cancel'
          text={'Cancel'}
          size='Medium'
          variant='secondary'
          onClick={handlePopupClose}
          disabled={updateEventLoading}
        />
        <CustomButton type='button' id='submit' text={'Submit'} size='Medium' onClick={handleSubmit} disabled={updateEventLoading} />
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
};

export default UpdateEvent;
