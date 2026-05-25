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
        borderRadius: '12px',
        padding: '10px 16px',
        mb: '8px',
      }}
    >
      <Typography sx={{ fontSize: '14px', color: colors.text.secondary, fontFamily: 'Roboto' }}>{message}</Typography>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: '4px', ml: '12px', flexShrink: 0 }}>
        <CustomButton
          data-testid='notify-btn'
          variant='primary'
          text='Notify'
          size='xSmall'
          sx={{ height: '32px', borderRadius: '8px', fontWeight: 500 }}
          onClick={onEnable}
        />
        <IconButton size='small' aria-label='Dismiss notification banner' onClick={onDismiss} data-testid='notify-dismiss-btn'>
          <Typography sx={{ fontSize: '18px', color: colors.text.tertiary, lineHeight: 1 }}>×</Typography>
        </IconButton>
      </Box>
    </Box>
  );
};

export default NotifyBanner;
