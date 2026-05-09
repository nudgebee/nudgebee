import { Box, Typography, Divider } from '@mui/material';

const VolumeDetails = ({ volumeItem }) => {
  return (
    <Box paddingLeft={'29px'}>
      <Box>
        <Box sx={{ display: 'flex', flex: 1, marginBottom: '8px' }}>
          <Typography width={'250px'} sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '500', lineHeight: '20px', color: '#374151' }}>
            Name:
          </Typography>
          <Typography sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '500', lineHeight: '20px', color: '#737373', maxWidth: '570px' }}>
            {volumeItem?.name}
          </Typography>
        </Box>
        <Box sx={{ display: 'flex', flex: 1, marginBottom: '8px' }}>
          <Typography width={'250px'} sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '500', lineHeight: '20px', color: '#374151' }}>
            Persistent Volume Claim:
          </Typography>
          <Typography sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '500', lineHeight: '20px', color: '#737373', maxWidth: '570px' }}>
            {volumeItem?.persistent_volume_claim?.claim_name ? `Claim name = ${volumeItem?.persistent_volume_claim?.claim_name}` : '-'}
          </Typography>
        </Box>
      </Box>
      <Divider sx={{ margin: '20px 0px' }} />
    </Box>
  );
};

export default VolumeDetails;
