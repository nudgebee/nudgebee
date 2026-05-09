import { Box, Button, IconButton, Typography } from '@mui/material';
import CloseIcon from '@mui/icons-material/Close';
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
      color: '#10b981',
    },
    {
      key: 'webhook',
      label: 'Webhook',
      description: 'HTTP endpoint trigger',
      icon: workflowWebhookIcon?.default || workflowWebhookIcon,
      color: '#f97316',
    },
    {
      key: 'schedule',
      label: 'Schedule',
      description: 'Time-based trigger',
      icon: workflowCalendarIcon?.default || workflowCalendarIcon,
      color: '#3b82f6',
    },
    {
      key: 'event',
      label: 'Event Trigger',
      description: 'Event-based trigger',
      icon: null,
      emoji: '⚡',
      color: '#f59e0b',
    },
    {
      key: 'optimization',
      label: 'Optimization',
      description: 'Triggered by new recommendations',
      icon: null,
      emoji: '💡',
      color: '#8b5cf6',
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
          borderRadius: '12px',
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
            padding: '16px 20px',
            borderBottom: '1px solid #e5e7eb',
            backgroundColor: colors.background.primaryLightest,
          }}
        >
          <Box sx={{ display: 'flex', alignItems: 'center', gap: '12px' }}>
            <Typography
              sx={{
                fontSize: '16px',
                fontWeight: 600,
                fontFamily: 'poppins',
                color: colors.text.secondary,
                letterSpacing: '-0.025em',
              }}
            >
              Select Trigger a Node
            </Typography>
          </Box>
          <IconButton
            onClick={onClose}
            sx={{
              color: '#6b7280',
              padding: '4px',
            }}
          >
            <CloseIcon sx={{ fontSize: '18px' }} />
          </IconButton>
        </Box>

        {/* Trigger Options */}
        <Box sx={{ padding: '16px' }}>
          {triggerOptions.map((trigger) => (
            <Button
              key={trigger.key}
              data-testid={`trigger-select-${trigger.key}`}
              onClick={() => onSelectTrigger(trigger.key)}
              fullWidth
              sx={{
                display: 'flex',
                alignItems: 'center',
                padding: '8px 16px',
                marginBottom: '4px',
                backgroundColor: 'white',
                borderRadius: '12px',
                cursor: 'pointer',
                transition: 'all 0.2s',
                textTransform: 'none',
                justifyContent: 'flex-start',
                '&:hover': {
                  backgroundColor: '#f8f9fa',
                },
                '&:last-child': {
                  marginBottom: 0,
                },
              }}
            >
              <Box
                sx={{
                  marginRight: '14px',
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'center',
                  width: '44px',
                  height: '44px',
                  borderRadius: '10px',
                  backgroundColor: `${trigger.color}15`,
                  border: `1px solid ${trigger.color}30`,
                }}
              >
                {trigger.icon ? (
                  <SafeIcon src={trigger.icon} alt={trigger.label} width={24} height={24} style={{ objectFit: 'contain' }} />
                ) : (
                  <span style={{ fontSize: '20px' }}>{trigger.emoji}</span>
                )}
              </Box>
              <Box sx={{ textAlign: 'left' }}>
                <Typography
                  sx={{
                    fontSize: '14px',
                    fontWeight: 600,
                    color: colors.text.secondary,
                    letterSpacing: '-0.015em',
                    fontFamily: 'poppins',
                  }}
                >
                  {trigger.label}
                </Typography>
                <Typography
                  sx={{
                    fontSize: '12px',
                    color: '#6b7280',
                  }}
                >
                  {trigger.description}
                </Typography>
              </Box>
            </Button>
          ))}
        </Box>
      </Box>
    </>
  );
};

export default TriggerSelectorPopup;
