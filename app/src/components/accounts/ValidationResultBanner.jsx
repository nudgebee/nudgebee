import { Alert, Box, Typography } from '@mui/material';
import CheckCircleOutlineIcon from '@mui/icons-material/CheckCircleOutline';
import WarningAmberIcon from '@mui/icons-material/WarningAmber';
import PropTypes from 'prop-types';
import { ds } from '@utils/colors';

// Permission labels the backend uses for the two cost-only checks
// (cloud_credential_validator_aws.go). Kept as a list so a failure in either
// one still reads as "cost is unavailable" rather than a generic permission gap.
const COST_PERMISSIONS = ['Cost & Usage Report (CUR) Discovery', 'CUR S3 Bucket Access'];

const ValidationResultBanner = ({ result }) => {
  if (!result) {
    return null;
  }

  // Hard failure (e.g. invalid credentials JSON)
  if (result.success === false && result.errorMessage) {
    return (
      <Alert severity='error' sx={{ mt: ds.space[2], mb: ds.space[2] }}>
        {result.errorMessage}
      </Alert>
    );
  }

  const details = result.permissionDetails || [];
  if (details.length === 0) {
    return null;
  }

  const failed = details.filter((d) => !d.hasAccess);
  const hasMissing = failed.length > 0;
  // A cost-only gap has a specific, actionable consequence, so name it instead
  // of the generic "certain features may not work". Everything except spend
  // keeps working, and the CUR can be attached later without re-onboarding.
  const costOnly = hasMissing && failed.every((d) => COST_PERMISSIONS.includes(d.permission));
  const severity = hasMissing ? 'warning' : 'success';
  let title = 'All permission checks passed.';
  if (costOnly) {
    title =
      'No usable Cost & Usage Report was found. You can still create the account — spend, rightsizing and cost recommendations will stay empty until a CUR is attached via Edit Billing Config.';
  } else if (hasMissing) {
    title = 'Some permission checks failed. You can still create the account, but certain features may not work until resolved.';
  }

  return (
    <Alert severity={severity} sx={{ mt: ds.space[2], mb: ds.space[2] }}>
      <Typography variant='body2' sx={{ mb: ds.space[1] }}>
        {title}
      </Typography>
      {details.map((detail) => (
        <Box key={detail.permission} sx={{ display: 'flex', alignItems: 'flex-start', gap: ds.space[1], mt: ds.space[1] }}>
          {detail.hasAccess ? (
            <CheckCircleOutlineIcon sx={{ fontSize: ds.text.title, color: 'success.main', mt: 'var(--ds-space-1)' }} />
          ) : (
            <WarningAmberIcon sx={{ fontSize: ds.text.title, color: 'warning.main', mt: 'var(--ds-space-1)' }} />
          )}
          <Box>
            <Typography variant='body2' sx={{ fontWeight: 'var(--ds-font-weight-medium)' }}>
              {detail.permission}
            </Typography>
            {detail.errorDetail && (
              <Typography variant='caption' color='text.secondary'>
                {detail.errorDetail}
              </Typography>
            )}
          </Box>
        </Box>
      ))}
    </Alert>
  );
};

ValidationResultBanner.propTypes = {
  result: PropTypes.shape({
    success: PropTypes.bool,
    errorMessage: PropTypes.string,
    permissionDetails: PropTypes.arrayOf(
      PropTypes.shape({
        permission: PropTypes.string,
        hasAccess: PropTypes.bool,
        errorDetail: PropTypes.string,
      })
    ),
  }),
};

export default ValidationResultBanner;
