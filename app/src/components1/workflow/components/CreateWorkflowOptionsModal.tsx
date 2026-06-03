import React from 'react';
import { Box } from '@mui/material';
import Tooltip from '@components1/ds/Tooltip';
import DataObjectIcon from '@mui/icons-material/DataObject';
import { Modal } from '@components1/common/modal';
import Text from '@common-new/format/Text';
import WidgetCard from '@components1/ds/WidgetCard';
import { workflowAddIcon, workflowAiIcon, workflowTemplateIcon } from '@assets';
import SafeIcon from '@components1/common/SafeIcon';
import { useTenantBranding } from '@hooks/useTenantBranding';

interface CreateWorkflowOptionsModalProps {
  open: boolean;
  onClose: () => void;
  onCreateFromScratch: () => void;
  onUseTemplate: () => void;
  onAskAI: () => void;
  onCreateFromCode: () => void;
  aiFeatureEnabled: boolean;
  templateFeatureEnabled: boolean;
}

const CreateWorkflowOptionsModal: React.FC<CreateWorkflowOptionsModalProps> = ({
  open,
  onClose,
  onCreateFromScratch,
  onUseTemplate,
  onAskAI,
  onCreateFromCode,
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
          gridTemplateColumns: 'repeat(2, 1fr)',
          padding: 'var(--ds-space-2) var(--ds-space-3) var(--ds-space-4) var(--ds-space-3)',
          alignItems: 'center',
          gap: 'var(--ds-space-4)',
        }}
      >
        <WidgetCard
          sx={{
            mt: 0,
            cursor: 'pointer',
            padding: 'var(--ds-space-4)',
            height: '240px',
            display: 'flex',
            flexDirection: 'column',
            gap: 'var(--ds-space-4)',
            '&:hover': {
              transform: 'translateY(-2px)',
              boxShadow: '0px 4px 20px -1px rgba(229, 229, 229, 8), 0px 2px 20px 0px rgb(233, 233, 233)',
              border: '1px solid var(--ds-purple-300)',
            },
            transition: 'all 0.2s ease',
          }}
          onClick={onCreateFromScratch}
          id='wf-create-from-scratch-card'
        >
          <Box
            sx={{
              display: 'flex',
              flexDirection: 'column',
              justifyContent: 'center',
              alignItems: 'center',
              borderRadius: 'var(--ds-radius-xl)',
              background: 'radial-gradient(circle, var(--ds-background-100) 0%, var(--ds-blue-100) 100%)',
              height: '55%',
              width: '100%',
            }}
          >
            <SafeIcon style={{ marginTop: '0px' }} src={workflowAddIcon} alt='Create from scratch' width={220} />
          </Box>
          <Box
            sx={{
              display: 'flex',
              flexDirection: 'column',
              justifyContent: 'center',
              height: '36%',
              px: 'var(--ds-space-2)',
              gap: 'var(--ds-space-1)',
            }}
          >
            <Text
              value='Make an Automation'
              sx={{
                fontSize: 'var(--ds-text-title)',
                fontWeight: 'var(--ds-font-weight-semibold)',
              }}
            />
            <Text
              value='Start with an empty canvas and add steps manually'
              sx={{
                fontSize: 'var(--ds-text-body)',
                fontWeight: 'var(--ds-font-weight-regular)',
                lineHeight: 'var(--ds-text-body-lh)',
                color: 'var(--ds-gray-500)',
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
                padding: 'var(--ds-space-4)',
                height: '240px',
                display: 'flex',
                flexDirection: 'column',
                gap: 'var(--ds-space-4)',
                opacity: templateFeatureEnabled ? 1 : 0.5,
                ...(templateFeatureEnabled
                  ? {
                      '&:hover': {
                        transform: 'translateY(-2px)',
                        boxShadow: '0px 4px 20px -1px rgba(229, 229, 229, 8), 0px 2px 20px 0px rgb(233, 233, 233)',
                        border: '1px solid var(--ds-purple-300)',
                      },
                    }
                  : {}),
                transition: 'all 0.2s ease',
              }}
              onClick={templateFeatureEnabled ? onUseTemplate : undefined}
              id='wf-create-from-template-card'
            >
              <Box
                sx={{
                  display: 'flex',
                  flexDirection: 'column',
                  justifyContent: 'center',
                  alignItems: 'center',
                  borderRadius: 'var(--ds-radius-xl)',
                  background: 'radial-gradient(circle, var(--ds-background-100) 0%, var(--ds-blue-100) 100%)',
                  height: '55%',
                  width: '100%',
                }}
              >
                <SafeIcon style={{ marginTop: '0px' }} src={workflowTemplateIcon} alt='Start from template' width={220} />
              </Box>
              <Box
                sx={{
                  display: 'flex',
                  flexDirection: 'column',
                  justifyContent: 'center',
                  px: 'var(--ds-space-2)',
                  gap: 'var(--ds-space-1)',
                  height: '36%',
                }}
              >
                <Text
                  value='Start from Template'
                  sx={{
                    fontSize: 'var(--ds-text-title)',
                    fontWeight: 'var(--ds-font-weight-semibold)',
                  }}
                />
                <Text
                  value={templateFeatureEnabled ? 'Pre-built automations for common infra tasks' : 'Coming Soon'}
                  sx={{
                    fontSize: 'var(--ds-text-body)',
                    fontWeight: 'var(--ds-font-weight-regular)',
                    lineHeight: 'var(--ds-text-body-lh)',
                    color: 'var(--ds-gray-500)',
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
                padding: 'var(--ds-space-4)',
                height: '240px',
                display: 'flex',
                flexDirection: 'column',
                gap: 'var(--ds-space-4)',
                opacity: aiFeatureEnabled ? 1 : 0.5,
                ...(aiFeatureEnabled
                  ? {
                      '&:hover': {
                        transform: 'translateY(-2px)',
                        boxShadow: '0px 4px 20px -1px rgba(229, 229, 229, 8), 0px 2px 20px 0px rgb(233, 233, 233)',
                        border: '1px solid var(--ds-purple-300)',
                      },
                    }
                  : {}),
                transition: 'all 0.2s ease',
              }}
              onClick={aiFeatureEnabled ? onAskAI : undefined}
              id='wf-create-from-ai-card'
            >
              <Box
                sx={{
                  display: 'flex',
                  flexDirection: 'column',
                  justifyContent: 'center',
                  alignItems: 'center',
                  borderRadius: 'var(--ds-radius-xl)',
                  background: 'radial-gradient(circle, var(--ds-background-100) 0%, var(--ds-blue-100) 100%)',
                  height: '55%',
                  width: '100%',
                }}
              >
                <SafeIcon style={{ marginTop: 'var(--ds-space-7)' }} src={workflowAiIcon} alt={`Ask ${assistantName} AI to generate`} width={220} />
              </Box>
              <Box
                sx={{
                  display: 'flex',
                  flexDirection: 'column',
                  justifyContent: 'center',
                  px: 'var(--ds-space-2)',
                  gap: 'var(--ds-space-1)',
                  height: '36%',
                }}
              >
                <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)' }}>
                  <Text
                    value={`Generate with ${assistantName}`}
                    sx={{
                      fontSize: 'var(--ds-text-title)',
                      fontWeight: 'var(--ds-font-weight-semibold)',
                    }}
                  />
                  {aiFeatureEnabled && (
                    <Box
                      sx={{
                        backgroundColor: 'var(--ds-purple-100)',
                        color: 'var(--ds-purple-400)',
                        fontSize: 'var(--ds-text-caption)',
                        fontWeight: 'var(--ds-font-weight-semibold)',
                        letterSpacing: '0.5px',
                        padding: 'var(--ds-space-1) var(--ds-space-2)',
                        borderRadius: 'var(--ds-radius-sm)',
                        lineHeight: 'var(--ds-text-body-lh)',
                      }}
                    >
                      BETA
                    </Box>
                  )}
                </Box>
                <Text
                  value={aiFeatureEnabled ? 'Describe what you need in plain English' : 'Coming Soon'}
                  sx={{
                    fontSize: 'var(--ds-text-body)',
                    fontWeight: 'var(--ds-font-weight-regular)',
                    lineHeight: 'var(--ds-text-body-lh)',
                    color: 'var(--ds-gray-500)',
                  }}
                />
              </Box>
            </WidgetCard>
          </Box>
        </Tooltip>

        <WidgetCard
          sx={{
            mt: 0,
            cursor: 'pointer',
            padding: 'var(--ds-space-4)',
            height: '240px',
            display: 'flex',
            flexDirection: 'column',
            gap: 'var(--ds-space-4)',
            '&:hover': {
              transform: 'translateY(-2px)',
              boxShadow: '0px 4px 20px -1px rgba(229, 229, 229, 8), 0px 2px 20px 0px rgb(233, 233, 233)',
              border: '1px solid var(--ds-purple-300)',
            },
            transition: 'all 0.2s ease',
          }}
          onClick={onCreateFromCode}
          id='wf-create-from-code-card'
          data-testid='create-workflow-from-code-card'
        >
          <Box
            sx={{
              display: 'flex',
              flexDirection: 'column',
              justifyContent: 'center',
              alignItems: 'center',
              borderRadius: 'var(--ds-radius-xl)',
              background: 'radial-gradient(circle, var(--ds-background-100) 0%, var(--ds-blue-100) 100%)',
              height: '55%',
              width: '100%',
            }}
          >
            <DataObjectIcon sx={{ fontSize: 'var(--ds-text-display)', color: 'var(--ds-purple-400)' }} />
          </Box>
          <Box
            sx={{
              display: 'flex',
              flexDirection: 'column',
              justifyContent: 'center',
              height: '36%',
              px: 'var(--ds-space-2)',
              gap: 'var(--ds-space-1)',
            }}
          >
            <Text
              value='Create from Code'
              sx={{
                fontSize: 'var(--ds-text-title)',
                fontWeight: 'var(--ds-font-weight-semibold)',
              }}
            />
            <Text
              value='Paste an automation JSON or YAML definition to create'
              sx={{
                fontSize: 'var(--ds-text-body)',
                fontWeight: 'var(--ds-font-weight-regular)',
                lineHeight: 'var(--ds-text-body-lh)',
                color: 'var(--ds-gray-500)',
              }}
            />
          </Box>
        </WidgetCard>
      </Box>
    </Modal>
  );
};

export default CreateWorkflowOptionsModal;
