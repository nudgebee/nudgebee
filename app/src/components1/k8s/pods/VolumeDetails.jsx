import { Box, Typography } from '@mui/material';
import { Divider } from '@components1/ds/Divider';
import { ds } from 'src/utils/colors';

const VolumeDetails = ({ volumeItem }) => {
  return (
    <Box paddingLeft={ds.space.mul(1, 7)}>
      <Box>
        <Box sx={{ display: 'flex', flex: 1, marginBottom: 'var(--ds-space-2)' }}>
          <Typography
            width={ds.space.mul(0, 125)}
            sx={{
              fontFamily: 'Roboto',
              fontSize: 'var(--ds-text-body-lg)',
              fontWeight: 'var(--ds-font-weight-medium)',
              lineHeight: '20px',
              color: 'var(--ds-brand-500)',
            }}
          >
            Name:
          </Typography>
          <Typography
            sx={{
              fontFamily: 'Roboto',
              fontSize: 'var(--ds-text-body-lg)',
              fontWeight: 'var(--ds-font-weight-medium)',
              lineHeight: '20px',
              color: 'var(--ds-gray-600)',
              maxWidth: ds.space.mul(0, 285),
            }}
          >
            {volumeItem?.name}
          </Typography>
        </Box>
        <Box sx={{ display: 'flex', flex: 1, marginBottom: 'var(--ds-space-2)' }}>
          <Typography
            width={ds.space.mul(0, 125)}
            sx={{
              fontFamily: 'Roboto',
              fontSize: 'var(--ds-text-body-lg)',
              fontWeight: 'var(--ds-font-weight-medium)',
              lineHeight: '20px',
              color: 'var(--ds-brand-500)',
            }}
          >
            Persistent Volume Claim:
          </Typography>
          <Typography
            sx={{
              fontFamily: 'Roboto',
              fontSize: 'var(--ds-text-body-lg)',
              fontWeight: 'var(--ds-font-weight-medium)',
              lineHeight: '20px',
              color: 'var(--ds-gray-600)',
              maxWidth: ds.space.mul(0, 285),
            }}
          >
            {volumeItem?.persistent_volume_claim?.claim_name ? `Claim name = ${volumeItem?.persistent_volume_claim?.claim_name}` : '-'}
          </Typography>
        </Box>
      </Box>
      <Divider sx={{ margin: 'var(--ds-space-4) 0px' }} />
    </Box>
  );
};

export default VolumeDetails;
