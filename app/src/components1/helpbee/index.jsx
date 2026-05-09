import React from 'react';
import { Accordion, AccordionSummary, AccordionDetails, Box, Typography } from '@mui/material';
import ExpandMoreIcon from '@mui/icons-material/ExpandMore';
import SafeIcon from '@components1/common/SafeIcon';
import { getBrandingAsset } from '@hooks/useTenantBranding';
import ChatIcon from '@assets/chat-icon.svg';
import PhoneCallIcon from '@assets/phone-call.svg';
import VideoCallIcon from '@assets/video-call-icon.svg';
import NDialog from '@components1/common/modal/NDialog';
import { colors } from 'src/utils/colors';

const HelpBeeModal = ({ isModalVisible, onClose }) => {
  return (
    <NDialog
      buttonText={
        <Box sx={{ display: 'flex', flexDirection: 'row', justifyContent: 'center' }}>
          <Box marginRight={'8px'}>
            <SafeIcon src={getBrandingAsset('helpbeeIcon')} alt={'HelpBee Icon'} width={22} height={21} />
          </Box>
          <Typography sx={{ color: colors.text.secondary, fontWeight: 600, fontSize: '16px' }}>{'HelpBee'}</Typography>
        </Box>
      }
      handleClose={onClose}
      dialogTitle={
        <Typography component='h2' variant='h5' fontWeight={600} color={colors.text.secondary}>
          {'How Can We Help You?'}
        </Typography>
      }
      open={isModalVisible}
      sx={{ maxWidth: '900px', minWidth: '900px' }}
      dialogContent={
        <Box display='flex' flexDirection='column' height={'350px'} justifyContent='space-between' alignItems='left' mt='15px'>
          <Box>
            <Box>
              <Accordion sx={{ background: colors.background.white, marginBottom: '8px', borderRadius: '8px' }}>
                <AccordionSummary expandIcon={<ExpandMoreIcon />}>
                  <Box sx={{ display: 'flex', flexDirection: 'row', width: '100%', justifyContent: 'flex-start' }}>
                    <Box sx={{ marginRight: '14px' }}>
                      <SafeIcon src={ChatIcon} width={22} alt={'Chat Icon'} />
                    </Box>
                    <Typography sx={{ color: colors.text.secondary, fontWeight: 600, fontSize: '16px' }}>{'Chat with us'}</Typography>
                  </Box>
                </AccordionSummary>
                <AccordionDetails />
              </Accordion>
              <Accordion sx={{ background: colors.background.white, marginBottom: '8px', borderRadius: '8px' }}>
                <AccordionSummary expandIcon={<ExpandMoreIcon />}>
                  <Box sx={{ display: 'flex', flexDirection: 'row', width: '100%', justifyContent: 'flex-start' }}>
                    <Box sx={{ marginRight: '14px' }}>
                      <SafeIcon src={PhoneCallIcon} width={22} alt={'Phone Icon'} />
                    </Box>
                    <Typography sx={{ color: colors.text.secondary, fontWeight: 600, fontSize: '16px' }}>{'Get a Call'}</Typography>
                  </Box>
                </AccordionSummary>
                <AccordionDetails />
              </Accordion>
              <Accordion sx={{ background: colors.background.white, marginBottom: '8px', borderRadius: '8px' }}>
                <AccordionSummary expandIcon={<ExpandMoreIcon />}>
                  <Box sx={{ display: 'flex', flexDirection: 'row', width: '100%', justifyContent: 'flex-start' }}>
                    <Box sx={{ marginRight: '14px' }}>
                      <SafeIcon src={VideoCallIcon} width={22} alt={'Video Call Icon'} />
                    </Box>
                    <Typography sx={{ color: colors.text.secondary, fontWeight: 600, fontSize: '16px' }}>{'Book an Appointment'}</Typography>
                  </Box>
                </AccordionSummary>
                <AccordionDetails />
              </Accordion>
            </Box>
          </Box>
          <Box sx={{ display: 'flex', flexDirection: 'column' }}>
            <Box sx={{ display: 'flex', flexDirection: 'row', justifyContent: 'center' }}>
              <Box marginRight={'8px'}>
                <SafeIcon src={getBrandingAsset('helpbeeIcon')} alt={'HelpBee Icon'} width={22} height={21} />
              </Box>
              <Typography sx={{ color: colors.text.secondary, fontWeight: 600, fontSize: '16px' }}>{'HelpBee'}</Typography>
            </Box>
          </Box>
        </Box>
      }
      additionalComponent={undefined}
      isSubmitRequired={false}
    />
  );
};

export default HelpBeeModal;
