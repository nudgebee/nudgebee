import React from 'react';
import { Box, Typography } from '@mui/material';
import { Accordion } from '@ui/Accordion';
import SafeIcon from '@shared/icons/SafeIcon';
import { getBrandingAsset } from '@hooks/useTenantBranding';
import ChatIcon from '@assets/chat-icon.svg';
import PhoneCallIcon from '@assets/phone-call.svg';
import VideoCallIcon from '@assets/video-call-icon.svg';
import { Modal } from '@ui/Modal';
import { ds } from '@utils/colors';

const HelpBeeModal = ({ isModalVisible, onClose }) => {
  return (
    <Modal
      handleClose={onClose}
      title={
        <Typography component='h2' variant='h5' fontWeight={600} color={ds.gray[700]}>
          {'How Can We Help You?'}
        </Typography>
      }
      open={isModalVisible}
      width='md'
      sx={{ maxWidth: ds.space.mul(0, 450), minWidth: ds.space.mul(0, 450) }}
      isConfirmRequired={false}
    >
      <Box
        display='flex'
        flexDirection='column'
        height={ds.space.mul(0, 175)}
        justifyContent='space-between'
        alignItems='left'
        mt={ds.space.mul(0, 7)}
      >
        <Box>
          <Box>
            <Accordion
              items={[
                {
                  id: 'chat-with-us',
                  icon: <SafeIcon src={ChatIcon} width={22} alt={'Chat Icon'} />,
                  label: 'Chat with us',
                  body: null,
                },
                {
                  id: 'get-a-call',
                  icon: <SafeIcon src={PhoneCallIcon} width={22} alt={'Phone Icon'} />,
                  label: 'Get a Call',
                  body: null,
                },
                {
                  id: 'book-an-appointment',
                  icon: <SafeIcon src={VideoCallIcon} width={22} alt={'Video Call Icon'} />,
                  label: 'Book an Appointment',
                  body: null,
                },
              ]}
            />
          </Box>
        </Box>
        <Box sx={{ display: 'flex', flexDirection: 'column' }}>
          <Box sx={{ display: 'flex', flexDirection: 'row', justifyContent: 'center' }}>
            <Box marginRight={ds.space[2]}>
              <SafeIcon src={getBrandingAsset('helpbeeIcon')} alt={'HelpBee Icon'} width={22} height={21} />
            </Box>
            <Typography sx={{ color: ds.gray[700], fontWeight: 'var(--ds-font-weight-semibold)', fontSize: 'var(--ds-text-title)' }}>
              {'HelpBee'}
            </Typography>
          </Box>
        </Box>
      </Box>
    </Modal>
  );
};

export default HelpBeeModal;
