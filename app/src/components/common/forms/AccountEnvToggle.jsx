import PropTypes from 'prop-types';
import { Box, Typography, IconButton, RadioGroup, FormControlLabel, Radio } from '@mui/material';
import InfoOutlinedIcon from '@mui/icons-material/InfoOutlined';
import Tooltip from '@ui/Tooltip';

export const ACCOUNT_ENV_PROD = 'prod';
export const ACCOUNT_ENV_NON_PROD = 'non_prod';

/** Matches the cloud_accounts.account_env column default. */
export const DEFAULT_ACCOUNT_ENV = ACCOUNT_ENV_NON_PROD;

/**
 * Environment picker shared by every account onboarding flow (K8s and cloud).
 * The value drives triage scoring — production accounts score their alerts at
 * full weight, non-production ones are damped — so it is surfaced identically
 * everywhere rather than reimplemented per provider.
 */
export default function AccountEnvToggle({ value, onChange, disabled, id, label }) {
  return (
    <Box>
      <Box display='flex' alignItems='center'>
        <Typography variant='subtitle2'>{label}</Typography>
        <Tooltip
          title='Determines how NudgeBee prioritises alerts, recommendations and incidents for this account. Production accounts are scored at full weight. You can change this anytime later.'
          placement='right'
        >
          <IconButton id={`${id}-info-btn`} size='small' sx={{ p: 0.5 }}>
            <InfoOutlinedIcon fontSize='small' />
          </IconButton>
        </Tooltip>
      </Box>
      <RadioGroup row id={id} aria-label='Account environment type' value={value} onChange={(e) => onChange(e.target.value)}>
        <FormControlLabel value={ACCOUNT_ENV_PROD} control={<Radio size='small' />} label='Production' disabled={disabled} />
        <FormControlLabel value={ACCOUNT_ENV_NON_PROD} control={<Radio size='small' />} label='Non-production' disabled={disabled} />
      </RadioGroup>
    </Box>
  );
}

AccountEnvToggle.propTypes = {
  value: PropTypes.oneOf([ACCOUNT_ENV_PROD, ACCOUNT_ENV_NON_PROD]).isRequired,
  onChange: PropTypes.func.isRequired,
  disabled: PropTypes.bool,
  id: PropTypes.string,
  label: PropTypes.string,
};

AccountEnvToggle.defaultProps = {
  disabled: false,
  id: 'account-env',
  label: 'Account Type',
};
