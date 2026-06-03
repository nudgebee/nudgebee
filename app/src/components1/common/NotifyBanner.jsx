import { Box, IconButton, Typography } from '@mui/material';
import CustomButton from '@components1/common/NewCustomButton';
import { colors } from 'src/utils/colors';

/**
 * A dismissible banner that prompts the user to enable browser notifications.
 *
 * @param {Object} props
 * @param {boolean} props.visible - Whether to show the banner
 * @param {() => void} props.onEnable - Called when user clicks "Notify"
 * @param {() => void} props.onDismiss - Called when user dismisses the banner
 * @param {string} [props.message] - Custom prompt message
 */
const NotifyBanner = ({ visible, onEnable, onDismiss, message = 'Want to be notified when your investigation completes?' }) => {
  if (!visible) return null;

  return (
    <Box
      data-testid='notify-banner'
      sx={{
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'space-between',
        backgroundColor: colors.background.tertiaryLightest || '#f5f5f5',
        borderRadius: 'var(--ds-radius-xl)',
        padding: 'var(--ds-space-2) var(--ds-space-4)',
        mb: 'var(--ds-space-2)',
      }}
    >
      <Typography sx={{ fontSize: 'var(--ds-text-body-lg)', color: colors.text.secondary, fontFamily: 'Roboto' }}>{message}</Typography>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-1)', ml: 'var(--ds-space-3)', flexShrink: 0 }}>
        <CustomButton
          data-testid='notify-btn'
          variant='primary'
          text='Notify'
          size='xSmall'
          sx={{ height: '32px', borderRadius: 'var(--ds-radius-lg)', fontWeight: 'var(--ds-font-weight-medium)' }}
          onClick={onEnable}
        />
        <IconButton size='small' aria-label='Dismiss notification banner' onClick={onDismiss} data-testid='notify-dismiss-btn'>
          <Typography sx={{ fontSize: 'var(--ds-text-title)', color: colors.text.tertiary, lineHeight: 1 }}>×</Typography>
        </IconButton>
      </Box>
    </Box>
  );
};

export default NotifyBanner;
