import { Typography } from '@mui/material';
import { Input } from '@components1/ds/Input';
import Stack from '@mui/material/Stack';
import React, { useState } from 'react';
import Box from '@mui/material/Box';
import apiRecommendations from '@api1/recommendation';
import { colors } from 'src/utils/colors';
import PropTypes from 'prop-types';
import { Modal } from '@components1/common/modal';
import CopyableText from '@components1/common/CopyableText';
import CustomButton from '@components1/common/NewCustomButton';

const KubernetesUnusedVolumePopupForm = ({ open, onClose, onSuccess, onFailure, data = {} }) => {
  const [confirmationText, setConfirmationText] = useState('');
  const [errorText, setErrorText] = useState('');
  const [isDeleting, setIsDeleting] = useState(false);

  const submitRecommendation = () => {
    if (confirmationText === data?.name) {
      setIsDeleting(true);
      apiRecommendations
        .applyRecommendation(data.accountId, data.id, data)
        .then((res) => {
          if (res?.errors) {
            onFailure(res?.errors);
          } else {
            onSuccess(res?.data);
            setConfirmationText('');
          }
        })
        .finally(() => {
          setIsDeleting(false);
        });
    } else {
      setErrorText('Please enter the correct volume name to confirm deletion');
    }
  };

  return (
    <Modal
      open={open}
      handleClose={onClose}
      title={'Are you sure you want to delete this volume?'}
      // actionButtons={<ActionButtons buttons={getButtons()} activeButton={activeButton} setActiveButton={setActiveButton} />}
    >
      <Box display='flex' flexDirection='column' justifyContent='space-between' alignItems='left' m='0px 12px 20px 12px'>
        <Box>
          {data?.name?.length < 15 ? (
            <Box display='flex' flexDirection='row' gap='8px' alignItems='center' mt={1}>
              <Typography sx={{ fontSize: 'var(--ds-text-title)', color: colors.text.secondary, fontWeight: 'var(--ds-font-weight-medium)' }}>
                Volume Name:
              </Typography>
              <Typography sx={{ fontSize: 'var(--ds-text-body-lg)', color: colors.text.secondary, fontWeight: 'var(--ds-font-weight-regular)' }}>
                {data?.name}
              </Typography>
              <CopyableText copyableText={data?.name} />
            </Box>
          ) : (
            <>
              <Typography
                sx={{
                  fontSize: 'var(--ds-text-title)',
                  color: colors.text.secondary,
                  fontWeight: 'var(--ds-font-weight-medium)',
                  marginTop: 'var(--ds-space-3)',
                }}
              >
                Volume Name:
              </Typography>{' '}
              <Box display='flex' flexDirection='row' gap='8px' mt={1}>
                <Typography sx={{ fontSize: 'var(--ds-text-body-lg)', color: colors.text.secondary, fontWeight: 'var(--ds-font-weight-regular)' }}>
                  {data?.name}
                </Typography>
                <CopyableText copyableText={data?.name} />
              </Box>
            </>
          )}
        </Box>
        <Box sx={{ mt: 2 }}>
          <Input
            label='Enter Volume Name'
            value={confirmationText}
            onChange={(value) => {
              setConfirmationText(value);
              setErrorText('');
            }}
            error={errorText || undefined}
            size='sm'
          />
        </Box>
      </Box>
      <Stack spacing={1} direction='row' sx={{ float: 'right' }} mb={2} mx='20px'>
        <CustomButton size='Medium' text={'Cancel'} variant='secondary' onClick={onClose} sx={{ minWidth: '140px' }} />
        <CustomButton
          size='Medium'
          text={'Delete Volume'}
          variant='primary'
          onClick={() => submitRecommendation()}
          sx={{ minWidth: '140px' }}
          loading={isDeleting}
          disabled={isDeleting}
        />
      </Stack>
    </Modal>
  );
};

KubernetesUnusedVolumePopupForm.propTypes = {
  onSuccess: PropTypes.func,
  onFailure: PropTypes.func,
  onClose: PropTypes.func,
  open: PropTypes.bool,
  data: PropTypes.object,
};

export default KubernetesUnusedVolumePopupForm;
