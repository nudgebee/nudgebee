import { Box, IconButton } from '@mui/material';
import VisibilityOffOutlinedIcon from '@mui/icons-material/VisibilityOffOutlined';
import ConfirmationNumberOutlinedIcon from '@mui/icons-material/ConfirmationNumberOutlined';
import OptimizeIcon from 'src/assets/images/home/optimize-icon-button.svg';
import { getNubiIconUrl, useTenantBranding } from '@hooks/useTenantBranding';
import ContentCopyOutlinedIcon from '@mui/icons-material/ContentCopyOutlined';
import CustomTooltip from '@components1/common/CustomTooltip';
import SafeIcon from '@components1/common/SafeIcon';
import { colors } from 'src/utils/colors';
import { action } from 'src/utils/actionStyles';

interface ActionButtonsProps {
  recommendationId: string;
  onDismiss?: (id: string) => void;
  onCreateTicket?: (id: string) => void;
  onResolve?: (id: string) => void;
  onCopyCli?: (id: string) => void;
  onAskNubi?: (id: string) => void;
  showResolve?: boolean;
  showCopyCli?: boolean;
  existingTicketId?: string;
}

const ActionButtons = ({
  recommendationId,
  onDismiss,
  onCreateTicket,
  onResolve,
  onCopyCli,
  onAskNubi,
  showResolve = true,
  showCopyCli = true,
  existingTicketId,
}: ActionButtonsProps) => {
  const { assistantName } = useTenantBranding();
  const handleClick = (e: React.MouseEvent, callback?: (id: string) => void) => {
    e.stopPropagation();
    callback?.(recommendationId);
  };

  return (
    <Box sx={{ display: 'flex', gap: '2px', alignItems: 'center' }}>
      {showResolve && (
        <CustomTooltip title='Optimize' placement='top'>
          <IconButton
            size='small'
            data-testid={`action-resolve-${recommendationId}`}
            onClick={(e) => handleClick(e, onResolve)}
            sx={{
              p: '4px',
              color: colors.text.tertiary,
              '&:hover': { backgroundColor: colors.background.tertiaryLightest },
            }}
          >
            <SafeIcon src={OptimizeIcon} alt='Optimize' width={16} height={16} />
          </IconButton>
        </CustomTooltip>
      )}

      <CustomTooltip title={existingTicketId ? `Ticket already created: ${existingTicketId}` : 'Create ticket'} placement='top'>
        <Box component='span' sx={{ display: 'inline-flex' }}>
          <IconButton
            size='small'
            data-testid={`action-ticket-${recommendationId}`}
            onClick={(e) => handleClick(e, onCreateTicket)}
            disabled={!!existingTicketId}
            sx={{
              p: '4px',
              color: colors.text.tertiary,
              '&:hover': { backgroundColor: colors.background.primaryLightest },
              '&.Mui-disabled': { color: colors.text.disabled },
            }}
          >
            <ConfirmationNumberOutlinedIcon sx={{ fontSize: '16px' }} />
          </IconButton>
        </Box>
      </CustomTooltip>

      {showCopyCli && (
        <CustomTooltip title='Copy CLI command' placement='top'>
          <IconButton
            size='small'
            data-testid={`action-copy-cli-${recommendationId}`}
            onClick={(e) => handleClick(e, onCopyCli)}
            sx={{
              p: '4px',
              color: colors.text.tertiary,
              '&:hover': { color: '#16A34A', backgroundColor: colors.background.costBlock },
            }}
          >
            <ContentCopyOutlinedIcon sx={{ fontSize: '16px' }} />
          </IconButton>
        </CustomTooltip>
      )}

      <CustomTooltip title={`Ask ${assistantName}`} placement='top'>
        <IconButton
          size='small'
          data-testid={`action-ask-nubi-${recommendationId}`}
          onClick={(e) => handleClick(e, onAskNubi)}
          sx={{ ...action.nubi }}
        >
          <SafeIcon src={getNubiIconUrl()} alt={`Ask ${assistantName}`} width={16} height={16} />
        </IconButton>
      </CustomTooltip>

      <CustomTooltip title='Dismiss' placement='top'>
        <IconButton
          size='small'
          data-testid={`action-dismiss-${recommendationId}`}
          onClick={(e) => handleClick(e, onDismiss)}
          sx={{
            p: '4px',
            color: colors.text.tertiary,
            '&:hover': { backgroundColor: colors.background.accordionSummay },
          }}
        >
          <VisibilityOffOutlinedIcon sx={{ fontSize: '16px' }} />
        </IconButton>
      </CustomTooltip>
    </Box>
  );
};

export default ActionButtons;
