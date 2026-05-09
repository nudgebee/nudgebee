import React from 'react';
import { Box, Tooltip } from '@mui/material';
import { Modal } from '@components1/common/modal';
import { Text } from '@components1/common';
import WidgetCard from '@components1/common/WidgetCard';
import { workflowAddIcon, workflowAiIcon, workflowTemplateIcon } from '@assets';
import SafeIcon from '@components1/common/SafeIcon';
import { useTenantBranding } from '@hooks/useTenantBranding';
import { colors } from 'src/utils/colors';

interface CreateWorkflowOptionsModalProps {
  open: boolean;
  onClose: () => void;
  onCreateFromScratch: () => void;
  onUseTemplate: () => void;
  onAskAI: () => void;
  aiFeatureEnabled: boolean;
  templateFeatureEnabled: boolean;
}

const CreateWorkflowOptionsModal: React.FC<CreateWorkflowOptionsModalProps> = ({
  open,
  onClose,
  onCreateFromScratch,
  onUseTemplate,
  onAskAI,
  aiFeatureEnabled,
  templateFeatureEnabled,
}) => {
  const { assistantName } = useTenantBranding();
  return (
    <Modal
      open={open}
      handleClose={onClose}
      width='md'
      hideTitleBackground={true}
      title='Create a New Automation'
      subtitle={'Choose how you would like to get started'}
    >
      {' '}
      <Box
        sx={{
          display: 'grid',
          gridTemplateColumns: 'repeat(3, 1fr)',
          height: '300px',
          padding: '0px 12px 36px 12px',
          alignItems: 'center',
          gap: '20px',
        }}
      >
        <WidgetCard
          sx={{
            mt: 0,
            cursor: 'pointer',
            padding: '16px',
            height: '240px',
            display: 'flex',
            flexDirection: 'column',
            gap: '18px',
            '&:hover': {
              transform: 'translateY(-2px)',
              boxShadow: '0px 4px 20px -1px rgba(229, 229, 229, 8), 0px 2px 20px 0px rgb(233, 233, 233)',
              border: '1px solid #C5AFFF',
            },
            transition: 'all 0.2s ease',
          }}
          onClick={onCreateFromScratch}
        >
          <Box
            sx={{
              display: 'flex',
              flexDirection: 'column',
              justifyContent: 'center',
              alignItems: 'center',
              borderRadius: '12px',
              background: 'radial-gradient(circle, white 0%, #EBF4FF 100%)',
              height: '55%',
              width: '100%',
            }}
          >
            <SafeIcon style={{ marginTop: '0px' }} src={workflowAddIcon} alt='Create from scratch' width={220} />
          </Box>
          <Box sx={{ display: 'flex', flexDirection: 'column', justifyContent: 'center', height: '36%', px: '8px', gap: '4px' }}>
            <Text
              value='Make an Automation'
              sx={{
                fontSize: '16px',
                fontWeight: 600,
              }}
            />
            <Text
              value='Start with an empty canvas and add steps manually'
              sx={{
                fontSize: '13px',
                fontWeight: 400,
                lineHeight: '16px',
                color: colors.text.secondaryDark,
              }}
            />
          </Box>
        </WidgetCard>

        <Tooltip title={!templateFeatureEnabled ? 'Coming Soon' : ''} arrow placement='top'>
          <Box>
            <WidgetCard
              sx={{
                mt: 0,
                cursor: templateFeatureEnabled ? 'pointer' : 'default',
                padding: '16px',
                height: '240px',
                display: 'flex',
                flexDirection: 'column',
                gap: '18px',
                opacity: templateFeatureEnabled ? 1 : 0.5,
                ...(templateFeatureEnabled
                  ? {
                      '&:hover': {
                        transform: 'translateY(-2px)',
                        boxShadow: '0px 4px 20px -1px rgba(229, 229, 229, 8), 0px 2px 20px 0px rgb(233, 233, 233)',
                        border: '1px solid #C5AFFF',
                      },
                    }
                  : {}),
                transition: 'all 0.2s ease',
              }}
              onClick={templateFeatureEnabled ? onUseTemplate : undefined}
            >
              <Box
                sx={{
                  display: 'flex',
                  flexDirection: 'column',
                  justifyContent: 'center',
                  alignItems: 'center',
                  borderRadius: '12px',
                  background: 'radial-gradient(circle, white 0%, #EBF4FF 100%)',
                  height: '55%',
                  width: '100%',
                }}
              >
                <SafeIcon style={{ marginTop: '0px' }} src={workflowTemplateIcon} alt='Start from template' width={220} />
              </Box>
              <Box sx={{ display: 'flex', flexDirection: 'column', justifyContent: 'center', px: '8px', gap: '4px', height: '36%' }}>
                <Text
                  value='Start from Template'
                  sx={{
                    fontSize: '16px',
                    fontWeight: 600,
                  }}
                />
                <Text
                  value={templateFeatureEnabled ? 'Pre-built automations for common infra tasks' : 'Coming Soon'}
                  sx={{
                    fontSize: '13px',
                    fontWeight: 400,
                    lineHeight: '16px',
                    color: colors.text.secondaryDark,
                  }}
                />
              </Box>
            </WidgetCard>
          </Box>
        </Tooltip>

        <Tooltip title={!aiFeatureEnabled ? 'Coming Soon' : ''} arrow placement='top'>
          <Box>
            <WidgetCard
              sx={{
                mt: 0,
                cursor: aiFeatureEnabled ? 'pointer' : 'default',
                padding: '16px',
                height: '240px',
                display: 'flex',
                flexDirection: 'column',
                gap: '18px',
                opacity: aiFeatureEnabled ? 1 : 0.5,
                ...(aiFeatureEnabled
                  ? {
                      '&:hover': {
                        transform: 'translateY(-2px)',
                        boxShadow: '0px 4px 20px -1px rgba(229, 229, 229, 8), 0px 2px 20px 0px rgb(233, 233, 233)',
                        border: '1px solid #C5AFFF',
                      },
                    }
                  : {}),
                transition: 'all 0.2s ease',
              }}
              onClick={aiFeatureEnabled ? onAskAI : undefined}
            >
              <Box
                sx={{
                  display: 'flex',
                  flexDirection: 'column',
                  justifyContent: 'center',
                  alignItems: 'center',
                  borderRadius: '12px',
                  background: 'radial-gradient(circle, white 0%, #EBF4FF 100%)',
                  height: '55%',
                  width: '100%',
                }}
              >
                <SafeIcon style={{ marginTop: '48px' }} src={workflowAiIcon} alt={`Ask ${assistantName} AI to generate`} width={220} />
              </Box>
              <Box sx={{ display: 'flex', flexDirection: 'column', justifyContent: 'center', px: '8px', gap: '4px', height: '36%' }}>
                <Box sx={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
                  <Text
                    value={`Generate with ${assistantName}`}
                    sx={{
                      fontSize: '16px',
                      fontWeight: 600,
                    }}
                  />
                  {aiFeatureEnabled && (
                    <Box
                      sx={{
                        backgroundColor: '#EEE8FF',
                        color: '#7C3AED',
                        fontSize: '10px',
                        fontWeight: 700,
                        letterSpacing: '0.5px',
                        padding: '2px 8px',
                        borderRadius: '4px',
                        lineHeight: '16px',
                      }}
                    >
                      BETA
                    </Box>
                  )}
                </Box>
                <Text
                  value={aiFeatureEnabled ? 'Describe what you need in plain English' : 'Coming Soon'}
                  sx={{
                    fontSize: '13px',
                    fontWeight: 400,
                    lineHeight: '16px',
                    color: colors.text.secondaryDark,
                  }}
                />
              </Box>
            </WidgetCard>
          </Box>
        </Tooltip>
      </Box>
    </Modal>
  );
};

export default CreateWorkflowOptionsModal;
