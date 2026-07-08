import PropTypes from 'prop-types';
import { Box, Typography } from '@mui/material';
import { Modal } from '@ui/Modal';
import { Button } from '@ui/Button';
import SafeIcon from '@shared/icons/SafeIcon';
import { useTenantBranding } from '@hooks/useTenantBranding';
import { ds } from 'src/utils/colors';

const capitalize = (s) => (s ? s.charAt(0).toUpperCase() + s.slice(1) : s);

/**
 * Friendly intro card shown before a tour starts (assistant persona header,
 * title, body, and a Snooze / "Let's go" footer). Brand-aware: the persona and
 * avatar come from the tenant's branding (assistant name, brand title, nubi
 * icon), so it matches white-label deployments automatically.
 */
const TourWelcomeDialog = ({ open, title, description, totalSteps, onStart, onSnooze }) => {
  // Reactive branding: re-renders when the async /api/public/app_config fetch
  // resolves, so white-label partners don't get stuck on Nudgebee defaults when
  // this dialog mounts before the branding cache is populated.
  const { assistantName, baseTitle, nubiIconUrl } = useTenantBranding();
  const persona = `${capitalize(assistantName)} from ${baseTitle}`;

  return (
    <Modal
      open={open}
      handleClose={onSnooze}
      width='sm'
      actionButtonsFullBleed
      title={
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)' }}>
          <Box
            sx={{
              width: 28,
              height: 28,
              borderRadius: 'var(--ds-radius-pill)',
              overflow: 'hidden',
              background: ds.background[300],
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              flexShrink: 0,
            }}
          >
            <SafeIcon src={nubiIconUrl} alt='' width={20} height={20} />
          </Box>
          <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500], fontWeight: 500 }}>{persona}</Typography>
        </Box>
      }
      actionButtons={
        <Box
          sx={{
            width: '100%',
            boxSizing: 'border-box',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'space-between',
            gap: 'var(--ds-space-2)',
            padding: 'var(--ds-space-3) var(--ds-space-5)',
            background: ds.background[200],
          }}
        >
          <Button id='tour-welcome-snooze' tone='secondary' size='md' onClick={onSnooze}>
            Snooze
          </Button>
          {totalSteps > 1 && <Typography sx={{ fontSize: ds.text.small, color: ds.gray[400] }}>{`1 of ${totalSteps}`}</Typography>}
          <Button id='tour-welcome-start' tone='primary' size='md' onClick={onStart}>
            Let’s go
          </Button>
        </Box>
      }
    >
      <Box
        sx={{
          px: 'var(--ds-space-5)',
          py: 'var(--ds-space-4)',
          textAlign: 'center',
          display: 'flex',
          flexDirection: 'column',
          gap: 'var(--ds-space-3)',
        }}
      >
        <Typography sx={{ fontSize: ds.text.title, fontWeight: 700, color: ds.gray[700] }}>{title}</Typography>
        <Typography sx={{ fontSize: ds.text.bodyLg, color: ds.gray[600], lineHeight: 1.6 }}>{description}</Typography>
      </Box>
    </Modal>
  );
};

TourWelcomeDialog.propTypes = {
  open: PropTypes.bool.isRequired,
  title: PropTypes.string,
  description: PropTypes.string,
  totalSteps: PropTypes.number,
  onStart: PropTypes.func.isRequired,
  onSnooze: PropTypes.func.isRequired,
};

export default TourWelcomeDialog;
