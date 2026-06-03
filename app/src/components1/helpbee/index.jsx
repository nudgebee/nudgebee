import React from 'react';
import { Accordion, AccordionSummary, AccordionDetails, Box, Typography } from '@mui/material';
import ExpandMoreIcon from '@mui/icons-material/ExpandMore';
import SafeIcon from '@components1/common/SafeIcon';
import { getBrandingAsset } from '@hooks/useTenantBranding';
import ChatIcon from '@assets/chat-icon.svg';
import PhoneCallIcon from '@assets/phone-call.svg';
import VideoCallIcon from '@assets/video-call-icon.svg';
import NDialog from '@components1/common/modal/NDialog';
import { ds } from '@utils/colors';

const HelpBeeModal = ({ isModalVisible, onClose }) => {
  return (
    <NDialog
      buttonText={
        <Box sx={{ display: 'flex', flexDirection: 'row', justifyContent: 'center' }}>
          <Box marginRight={ds.space[2]}>
            <SafeIcon src={getBrandingAsset('helpbeeIcon')} alt={'HelpBee Icon'} width={22} height={21} />
          </Box>
          <Typography sx={{ color: ds.gray[700], fontWeight: 'var(--ds-font-weight-semibold)', fontSize: 'var(--ds-text-title)' }}>
            {'HelpBee'}
          </Typography>
        </Box>
      }
      handleClose={onClose}
      dialogTitle={
        <Typography component='h2' variant='h5' fontWeight={600} color={ds.gray[700]}>
          {'How Can We Help You?'}
        </Typography>
      }
      open={isModalVisible}
      sx={{ maxWidth: ds.space.mul(0, 450), minWidth: ds.space.mul(0, 450) }}
      dialogContent={
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
              <Accordion sx={{ background: ds.background[100], marginBottom: 'var(--ds-space-2)', borderRadius: 'var(--ds-radius-lg)' }}>
                <AccordionSummary expandIcon={<ExpandMoreIcon />}>
                  <Box sx={{ display: 'flex', flexDirection: 'row', width: '100%', justifyContent: 'flex-start' }}>
                    <Box sx={{ marginRight: 'var(--ds-space-3)' }}>
                      <SafeIcon src={ChatIcon} width={22} alt={'Chat Icon'} />
                    </Box>
                    <Typography sx={{ color: ds.gray[700], fontWeight: 'var(--ds-font-weight-semibold)', fontSize: 'var(--ds-text-title)' }}>
                      {'Chat with us'}
                    </Typography>
                  </Box>
                </AccordionSummary>
                <AccordionDetails />
              </Accordion>
              <Accordion sx={{ background: ds.background[100], marginBottom: 'var(--ds-space-2)', borderRadius: 'var(--ds-radius-lg)' }}>
                <AccordionSummary expandIcon={<ExpandMoreIcon />}>
                  <Box sx={{ display: 'flex', flexDirection: 'row', width: '100%', justifyContent: 'flex-start' }}>
                    <Box sx={{ marginRight: 'var(--ds-space-3)' }}>
                      <SafeIcon src={PhoneCallIcon} width={22} alt={'Phone Icon'} />
                    </Box>
                    <Typography sx={{ color: ds.gray[700], fontWeight: 'var(--ds-font-weight-semibold)', fontSize: 'var(--ds-text-title)' }}>
                      {'Get a Call'}
                    </Typography>
                  </Box>
                </AccordionSummary>
                <AccordionDetails />
              </Accordion>
              <Accordion sx={{ background: ds.background[100], marginBottom: 'var(--ds-space-2)', borderRadius: 'var(--ds-radius-lg)' }}>
                <AccordionSummary expandIcon={<ExpandMoreIcon />}>
                  <Box sx={{ display: 'flex', flexDirection: 'row', width: '100%', justifyContent: 'flex-start' }}>
                    <Box sx={{ marginRight: 'var(--ds-space-3)' }}>
                      <SafeIcon src={VideoCallIcon} width={22} alt={'Video Call Icon'} />
                    </Box>
                    <Typography sx={{ color: ds.gray[700], fontWeight: 'var(--ds-font-weight-semibold)', fontSize: 'var(--ds-text-title)' }}>
                      {'Book an Appointment'}
                    </Typography>
                  </Box>
                </AccordionSummary>
                <AccordionDetails />
              </Accordion>
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
      }
      additionalComponent={undefined}
      isSubmitRequired={false}
    />
  );
};

export default HelpBeeModal;
