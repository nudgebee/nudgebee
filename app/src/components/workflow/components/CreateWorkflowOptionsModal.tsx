import React from 'react';
import { Box } from '@mui/material';
import Tooltip from '@ui/Tooltip';
import DataObjectIcon from '@mui/icons-material/DataObject';
import { Modal } from '@ui/Modal';
import { Select, type SelectOption } from '@ui/Select';
import Text from '@shared/format/Text';
import WidgetCard from '@ui/WidgetCard';
import { workflowAddIcon, workflowAiIcon, workflowTemplateIcon } from '@assets';
import SafeIcon from '@shared/icons/SafeIcon';
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
  /** Accounts the user can create automations in (write access only). */
  accountOptions: SelectOption[];
  selectedAccountId: string;
  onAccountChange: (accountId: string) => void;
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
  accountOptions,
  selectedAccountId,
  onAccountChange,
}) => {
  const { assistantName } = useTenantBranding();
  // The listing is tenant-level, so an automation's account is no longer
  // implied by the page — it has to be chosen here before any path can start.
  const accountChosen = !!selectedAccountId;
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
      <Box sx={{ padding: 'var(--ds-space-2) var(--ds-space-3) 0 var(--ds-space-3)', maxWidth: '360px' }}>
        <Select
          id='wf-create-account-select'
          label='Account'
          placeholder='Select an account'
          options={accountOptions}
          value={selectedAccountId || null}
          onChange={(next) => onAccountChange(next || '')}
          help={accountChosen ? undefined : 'Pick the account this automation will run in.'}
          required
          searchable
        />
      </Box>
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
            cursor: accountChosen ? 'pointer' : 'default',
            padding: 'var(--ds-space-4)',
            height: '240px',
            display: 'flex',
            flexDirection: 'column',
            gap: 'var(--ds-space-4)',
            opacity: accountChosen ? 1 : 0.5,
            ...(accountChosen
              ? {
                  '&:hover': {
                    transform: 'translateY(-2px)',
                    boxShadow:
                      '0 var(--ds-space-1) calc(var(--ds-space-0) * 10) -1px rgba(229, 229, 229, 8), 0 var(--ds-space-0) calc(var(--ds-space-0) * 10) 0 rgb(233, 233, 233)',
                    border: '1px solid var(--ds-purple-300)',
                  },
                }
              : {}),
            transition: 'all 0.2s ease',
          }}
          onClick={accountChosen ? onCreateFromScratch : undefined}
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

        <Tooltip title={!templateFeatureEnabled ? 'Coming Soon' : !accountChosen ? 'Select an account first' : ''} arrow placement='top'>
          <Box>
            <WidgetCard
              sx={{
                mt: 0,
                cursor: templateFeatureEnabled && accountChosen ? 'pointer' : 'default',
                padding: 'var(--ds-space-4)',
                height: '240px',
                display: 'flex',
                flexDirection: 'column',
                gap: 'var(--ds-space-4)',
                opacity: templateFeatureEnabled && accountChosen ? 1 : 0.5,
                ...(templateFeatureEnabled && accountChosen
                  ? {
                      '&:hover': {
                        transform: 'translateY(-2px)',
                        boxShadow:
                          '0 var(--ds-space-1) calc(var(--ds-space-0) * 10) -1px rgba(229, 229, 229, 8), 0 var(--ds-space-0) calc(var(--ds-space-0) * 10) 0 rgb(233, 233, 233)',
                        border: '1px solid var(--ds-purple-300)',
                      },
                    }
                  : {}),
                transition: 'all 0.2s ease',
              }}
              onClick={templateFeatureEnabled && accountChosen ? onUseTemplate : undefined}
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

        <Tooltip title={!aiFeatureEnabled ? 'Coming Soon' : !accountChosen ? 'Select an account first' : ''} arrow placement='top'>
          <Box>
            <WidgetCard
              sx={{
                mt: 0,
                cursor: aiFeatureEnabled && accountChosen ? 'pointer' : 'default',
                padding: 'var(--ds-space-4)',
                height: '240px',
                display: 'flex',
                flexDirection: 'column',
                gap: 'var(--ds-space-4)',
                opacity: aiFeatureEnabled && accountChosen ? 1 : 0.5,
                ...(aiFeatureEnabled && accountChosen
                  ? {
                      '&:hover': {
                        transform: 'translateY(-2px)',
                        boxShadow:
                          '0 var(--ds-space-1) calc(var(--ds-space-0) * 10) -1px rgba(229, 229, 229, 8), 0 var(--ds-space-0) calc(var(--ds-space-0) * 10) 0 rgb(233, 233, 233)',
                        border: '1px solid var(--ds-purple-300)',
                      },
                    }
                  : {}),
                transition: 'all 0.2s ease',
              }}
              onClick={aiFeatureEnabled && accountChosen ? onAskAI : undefined}
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
            cursor: accountChosen ? 'pointer' : 'default',
            padding: 'var(--ds-space-4)',
            height: '240px',
            display: 'flex',
            flexDirection: 'column',
            gap: 'var(--ds-space-4)',
            opacity: accountChosen ? 1 : 0.5,
            ...(accountChosen
              ? {
                  '&:hover': {
                    transform: 'translateY(-2px)',
                    boxShadow:
                      '0 var(--ds-space-1) calc(var(--ds-space-0) * 10) -1px rgba(229, 229, 229, 8), 0 var(--ds-space-0) calc(var(--ds-space-0) * 10) 0 rgb(233, 233, 233)',
                    border: '1px solid var(--ds-purple-300)',
                  },
                }
              : {}),
            transition: 'all 0.2s ease',
          }}
          onClick={accountChosen ? onCreateFromCode : undefined}
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
