import { Alert, Box, Typography } from '@mui/material';
import CheckCircleOutlineIcon from '@mui/icons-material/CheckCircleOutline';
import WarningAmberIcon from '@mui/icons-material/WarningAmber';
import PropTypes from 'prop-types';
import { ds } from '@utils/colors';

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

  const hasMissing = details.some((d) => !d.hasAccess);
  const severity = hasMissing ? 'warning' : 'success';
  const title = hasMissing
    ? 'Some permission checks failed. You can still create the account, but certain features may not work until resolved.'
    : 'All permission checks passed.';

  return (
    <Alert severity={severity} sx={{ mt: ds.space[2], mb: ds.space[2] }}>
      <Typography variant='body2' sx={{ mb: ds.space[1] }}>
        {title}
      </Typography>
      {details.map((detail) => (
        <Box key={detail.permission} sx={{ display: 'flex', alignItems: 'flex-start', gap: ds.space[1], mt: ds.space[1] }}>
          {detail.hasAccess ? (
            <CheckCircleOutlineIcon sx={{ fontSize: 16, color: 'success.main', mt: 'var(--ds-space-1)' }} />
          ) : (
            <WarningAmberIcon sx={{ fontSize: 16, color: 'warning.main', mt: 'var(--ds-space-1)' }} />
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
