import { Box, ButtonBase, Typography } from '@mui/material';
import CloseIcon from '@mui/icons-material/Close';
import { Button } from '@components1/ds/Button';
import SafeIcon from '@components1/common/SafeIcon';
import { colors } from 'src/utils/colors';
import { workflowUserIcon, workflowWebhookIcon, workflowCalendarIcon } from '@assets';

interface TriggerSelectorPopupProps {
  open: boolean;
  onClose: () => void;
  onSelectTrigger: (triggerKey: string) => void;
}

const TriggerSelectorPopup: React.FC<TriggerSelectorPopupProps> = ({ open, onClose, onSelectTrigger }) => {
  if (!open) {
    return null;
  }

  const triggerOptions = [
    {
      key: 'manual',
      label: 'Manual Trigger',
      description: 'Start automation manually',
      icon: workflowUserIcon?.default || workflowUserIcon,
      color: 'var(--ds-teal-500)',
    },
    {
      key: 'webhook',
      label: 'Webhook',
      description: 'HTTP endpoint trigger',
      icon: workflowWebhookIcon?.default || workflowWebhookIcon,
      color: 'var(--ds-amber-500)',
    },
    {
      key: 'schedule',
      label: 'Schedule',
      description: 'Time-based trigger',
      icon: workflowCalendarIcon?.default || workflowCalendarIcon,
      color: 'var(--ds-blue-500)',
    },
    {
      key: 'event',
      label: 'Event Trigger',
      description: 'Event-based trigger',
      icon: null,
      emoji: '⚡',
      color: 'var(--ds-amber-400)',
    },
    {
      key: 'optimization',
      label: 'Optimization',
      description: 'Triggered by new recommendations',
      icon: null,
      emoji: '💡',
      color: 'var(--ds-purple-400)',
    },
  ];

  return (
    <>
      {/* Overlay */}
      <Box
        onClick={onClose}
        sx={{
          position: 'absolute',
          top: 0,
          left: 0,
          width: '100%',
          height: '100vh',
          backgroundColor: 'rgba(0, 0, 0, 0.3)',
          zIndex: 190,
        }}
      />

      {/* Trigger Selection Popup */}
      <Box
        sx={{
          position: 'absolute',
          top: '50%',
          left: '50%',
          transform: 'translate(-50%, -50%)',
          width: '420px',
          backgroundColor: 'white',
          zIndex: 200,
          boxShadow: '0 20px 25px -5px rgba(0, 0, 0, 0.1), 0 10px 10px -5px rgba(0, 0, 0, 0.04)',
          borderRadius: 'var(--ds-radius-xl)',
          border: '3px solid rgb(170, 144, 235)',
          overflow: 'hidden',
        }}
      >
        {/* Header */}
        <Box
          sx={{
            display: 'flex',
            justifyContent: 'space-between',
            alignItems: 'center',
            padding: 'var(--ds-space-4) var(--ds-space-4)',
            borderBottom: '1px solid var(--ds-brand-150)',
            backgroundColor: colors.background.primaryLightest,
          }}
        >
          <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-3)' }}>
            <Typography
              sx={{
                fontSize: 'var(--ds-text-title)',
                fontWeight: 'var(--ds-font-weight-semibold)',
                fontFamily: 'poppins',
                color: colors.text.secondary,
                letterSpacing: '-0.025em',
              }}
            >
              Select Trigger a Node
            </Typography>
          </Box>
          <Button
            id='wf-trigger-selector-close-btn'
            composition='icon-only'
            tone='ghost'
            size='sm'
            aria-label='Close'
            icon={<CloseIcon sx={{ fontSize: 'var(--ds-text-title)', color: 'var(--ds-gray-600)' }} />}
            onClick={onClose}
          />
        </Box>

        {/* Trigger Options */}
        <Box sx={{ padding: 'var(--ds-space-4)' }}>
          {triggerOptions.map((trigger) => (
            <ButtonBase
              key={trigger.key}
              id={`wf-trigger-selector-${trigger.key}-btn`}
              data-testid={`trigger-select-${trigger.key}`}
              onClick={() => onSelectTrigger(trigger.key)}
              sx={{
                width: '100%',
                display: 'flex',
                alignItems: 'center',
                padding: 'var(--ds-space-2) var(--ds-space-4)',
                marginBottom: 'var(--ds-space-1)',
                backgroundColor: 'white',
                borderRadius: 'var(--ds-radius-xl)',
                cursor: 'pointer',
                transition: 'all 0.2s',
                justifyContent: 'flex-start',
                '&:hover': {
                  backgroundColor: 'var(--ds-background-200)',
                },
                '&:last-child': {
                  marginBottom: 0,
                },
              }}
            >
              <Box
                sx={{
                  marginRight: 'var(--ds-space-3)',
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'center',
                  width: '44px',
                  height: '44px',
                  borderRadius: 'var(--ds-radius-lg)',
                  backgroundColor: `color-mix(in srgb, ${trigger.color} 15%, transparent)`,
                  border: `1px solid color-mix(in srgb, ${trigger.color} 30%, transparent)`,
                }}
              >
                {trigger.icon ? (
                  <SafeIcon src={trigger.icon} alt={trigger.label} width={24} height={24} style={{ objectFit: 'contain' }} />
                ) : (
                  <span style={{ fontSize: 'var(--ds-text-heading)' }}>{trigger.emoji}</span>
                )}
              </Box>
              <Box sx={{ textAlign: 'left' }}>
                <Typography
                  sx={{
                    fontSize: 'var(--ds-text-body-lg)',
                    fontWeight: 'var(--ds-font-weight-semibold)',
                    color: colors.text.secondary,
                    letterSpacing: '-0.015em',
                    fontFamily: 'poppins',
                  }}
                >
                  {trigger.label}
                </Typography>
                <Typography
                  sx={{
                    fontSize: 'var(--ds-text-small)',
                    color: 'var(--ds-gray-600)',
                  }}
                >
                  {trigger.description}
                </Typography>
              </Box>
            </ButtonBase>
          ))}
        </Box>
      </Box>
    </>
  );
};

export default TriggerSelectorPopup;
