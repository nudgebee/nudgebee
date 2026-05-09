import { Alert, Box, Typography } from '@mui/material';
import CheckCircleOutlineIcon from '@mui/icons-material/CheckCircleOutline';
import WarningAmberIcon from '@mui/icons-material/WarningAmber';
import PropTypes from 'prop-types';

const ValidationResultBanner = ({ result }) => {
  if (!result) {
    return null;
  }

  // Hard failure (e.g. invalid credentials JSON)
  if (result.success === false && result.errorMessage) {
    return (
      <Alert severity='error' sx={{ mt: 1, mb: 1 }}>
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
    <Alert severity={severity} sx={{ mt: 1, mb: 1 }}>
      <Typography variant='body2' sx={{ mb: 0.5 }}>
        {title}
      </Typography>
      {details.map((detail) => (
        <Box key={detail.permission} sx={{ display: 'flex', alignItems: 'flex-start', gap: 0.5, mt: 0.5 }}>
          {detail.hasAccess ? (
            <CheckCircleOutlineIcon sx={{ fontSize: 16, color: 'success.main', mt: '2px' }} />
          ) : (
            <WarningAmberIcon sx={{ fontSize: 16, color: 'warning.main', mt: '2px' }} />
          )}
          <Box>
            <Typography variant='body2' sx={{ fontWeight: 500 }}>
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
