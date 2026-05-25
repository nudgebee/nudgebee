import { Box, Typography, Divider } from '@mui/material';
import CustomLabels from '@components1/common/widgets/CustomLabels';
import PropTypes from 'prop-types';

const ContainerDetails = ({ containerItem }) => {
  const MapEnvironment = ({ label }) => {
    const labelArray = [];

    for (const item in label) {
      var name = label[item].name + '=' + label[item].value;
      labelArray.push(
        <Box key={item.id} sx={{ margin: '0 8px 15px 0' }}>
          <CustomLabels textTransform='none' height='100%' wordBreak='break-all' text={name} />
        </Box>
      );
    }
    return labelArray;
  };

  return (
    <Box paddingLeft={'29px'}>
      <Box>
        <Box sx={{ display: 'flex', flex: 1, marginBottom: '8px' }}>
          <Typography width={'150px'} sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '500', lineHeight: '20px', color: '#374151' }}>
            Image:
          </Typography>
          <Typography sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '500', lineHeight: '20px', color: '#737373', maxWidth: '570px' }}>
            {containerItem?.image}
          </Typography>
        </Box>
        <Box sx={{ display: 'flex', flex: 1, marginBottom: '8px' }}>
          <Typography width={'150px'} sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '500', lineHeight: '20px', color: '#374151' }}>
            ImagePullPolicy:
          </Typography>
          <Typography sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '500', lineHeight: '20px', color: '#737373', maxWidth: '570px' }}>
            {}
          </Typography>
        </Box>
        <Box sx={{ display: 'flex', flex: 1, marginBottom: '8px' }}>
          <Typography width={'150px'} sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '500', lineHeight: '20px', color: '#374151' }}>
            Environment:
          </Typography>
          <Box sx={{ display: 'flex', flexDirection: 'row', flexWrap: 'wrap', maxWidth: '80%' }}>{<MapEnvironment label={containerItem?.env} />}</Box>
        </Box>
      </Box>
      <Divider sx={{ margin: '20px 0px' }} />
      <Box>
        <Typography sx={{ fontFamily: 'Roboto', fontWeight: 600, fontSize: 12, color: '#9F9F9F' }}>PORTS</Typography>
        {containerItem?.ports && containerItem?.ports.length > 0 ? (
          <Box key={Date.now()} sx={{ display: 'flex', flex: 1, marginBottom: '8px' }}>
            <Typography width={'150px'} sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '500', lineHeight: '20px', color: '#374151' }}>
              {containerItem?.ports.toString()}
            </Typography>
          </Box>
        ) : (
          <></>
        )}
      </Box>
      <Divider sx={{ margin: '20px 0px' }} />

      <Box>
        <Typography sx={{ fontFamily: 'Roboto', fontWeight: 600, fontSize: 12, color: '#9F9F9F' }}>RESOURCES</Typography>
        <Box sx={{ display: 'flex', flex: 1, marginBottom: '8px' }}>
          <Typography width={'150px'} sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '500', lineHeight: '20px', color: '#374151' }}>
            Requests:
          </Typography>
          {containerItem?.resources?.requests?.memory && (
            <Typography
              sx={{
                fontFamily: 'Roboto',
                fontSize: '14px',
                fontWeight: '500',
                lineHeight: '20px',
                color: '#737373',
                marginRight: '20px',
                minWidth: '100px',
              }}
            >
              Memory: {containerItem?.resources?.requests?.memory}
            </Typography>
          )}
          {containerItem?.resources?.requests?.cpu && (
            <Typography sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '500', lineHeight: '20px', color: '#737373', maxWidth: '570px' }}>
              CPU: {containerItem?.resources?.requests?.cpu}
            </Typography>
          )}
        </Box>
        <Box sx={{ display: 'flex', flex: 1, marginBottom: '8px' }}>
          <Typography width={'150px'} sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '500', lineHeight: '20px', color: '#374151' }}>
            Limits:
          </Typography>
          {containerItem?.resources?.limits?.memory && (
            <Typography
              sx={{
                fontFamily: 'Roboto',
                fontSize: '14px',
                fontWeight: '500',
                lineHeight: '20px',
                color: '#737373',
                marginRight: '20px',
                minWidth: '100px',
              }}
            >
              Memory: {containerItem?.resources?.limits?.memory}
            </Typography>
          )}
          {containerItem?.resources?.limits?.cpu && (
            <Typography sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '500', lineHeight: '20px', color: '#737373', maxWidth: '570px' }}>
              CPU: {containerItem?.resources?.limits?.cpu}
            </Typography>
          )}
        </Box>
      </Box>
      <Divider sx={{ margin: '20px 0px' }} />
      <Box>
        <Typography sx={{ fontFamily: 'Roboto', fontWeight: 600, fontSize: 12, color: '#9F9F9F' }}>MOUNTS</Typography>
        <Box sx={{ display: 'flex', flex: 1, marginBottom: '8px' }}>
          <Typography width={'150px'} sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '500', lineHeight: '20px', color: '#374151' }}>
            Mounts:
          </Typography>
          <Typography sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '500', lineHeight: '20px', color: '#737373', maxWidth: '570px' }}>
            <CustomLabels text={'-'} />
          </Typography>
        </Box>
      </Box>
      <Divider sx={{ margin: '20px 0px' }} />
      <Box>
        <Typography sx={{ fontFamily: 'Roboto', fontWeight: 600, fontSize: 12, color: '#9F9F9F' }}>LIVENESS PROBE</Typography>
        <Box sx={{ display: 'flex', flex: 1, marginBottom: '8px' }}>
          <Typography width={'150px'} sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '500', lineHeight: '20px', color: '#374151' }}>
            Path:
          </Typography>
          <CustomLabels textTransform='none' text={containerItem?.liveness_probe?.httpGet?.path || '-'} />
        </Box>
        <Box sx={{ display: 'flex', flex: 1, marginBottom: '8px' }}>
          <Typography width={'150px'} sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '500', lineHeight: '20px', color: '#374151' }}>
            Initial Delay Seconds:
          </Typography>
          <Typography sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '500', lineHeight: '20px', color: '#737373', maxWidth: '570px' }}>
            {containerItem?.liveness_probe?.initial_delay_seconds || '-'}
          </Typography>
        </Box>
        <Box sx={{ display: 'flex', flex: 1, marginBottom: '8px' }}>
          <Typography width={'150px'} sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '500', lineHeight: '20px', color: '#374151' }}>
            Timeout Seconds:
          </Typography>
          <Typography sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '500', lineHeight: '20px', color: '#737373', maxWidth: '570px' }}>
            {containerItem?.liveness_probe?.timeout_seconds || '-'}
          </Typography>
        </Box>
        <Box sx={{ display: 'flex', flex: 1, marginBottom: '8px' }}>
          <Typography width={'150px'} sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '500', lineHeight: '20px', color: '#374151' }}>
            Period Seconds:
          </Typography>
          <Typography sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '500', lineHeight: '20px', color: '#737373', maxWidth: '570px' }}>
            {containerItem?.liveness_probe?.period_seconds || '-'}
          </Typography>
        </Box>
        <Box sx={{ display: 'flex', flex: 1, marginBottom: '8px' }}>
          <Typography width={'150px'} sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '500', lineHeight: '20px', color: '#374151' }}>
            Success Threshold:
          </Typography>
          <Typography sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '500', lineHeight: '20px', color: '#737373', maxWidth: '570px' }}>
            {containerItem?.liveness_probe?.success_threshold || '-'}
          </Typography>
        </Box>
        <Box sx={{ display: 'flex', flex: 1, marginBottom: '8px' }}>
          <Typography width={'150px'} sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '500', lineHeight: '20px', color: '#374151' }}>
            Failure Threshold:
          </Typography>
          <Typography sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '500', lineHeight: '20px', color: '#737373', maxWidth: '570px' }}>
            {containerItem?.liveness_probe?.failure_threshold || '-'}
          </Typography>
        </Box>
        <Box sx={{ display: 'flex', flex: 1, marginBottom: '8px' }}>
          <Typography width={'150px'} sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '500', lineHeight: '20px', color: '#374151' }}>
            Termination Grace Period Seconds:
          </Typography>
          <Typography sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '500', lineHeight: '20px', color: '#737373', maxWidth: '570px' }}>
            {containerItem?.liveness_probe?.termination_grace_period_seconds || '-'}
          </Typography>
        </Box>
      </Box>
      <Divider sx={{ margin: '20px 0px' }} />
      <Box>
        <Typography sx={{ fontFamily: 'Roboto', fontWeight: 600, fontSize: 12, color: '#9F9F9F' }}>READINESS PROBE</Typography>
        <Box sx={{ display: 'flex', flex: 1, marginBottom: '8px' }}>
          <Typography width={'150px'} sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '500', lineHeight: '20px', color: '#374151' }}>
            Path:
          </Typography>
          <CustomLabels textTransform='none' text={containerItem?.readiness_probe?.httpGet?.path || '-'} />
        </Box>
        <Box sx={{ display: 'flex', flex: 1, marginBottom: '8px' }}>
          <Typography width={'150px'} sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '500', lineHeight: '20px', color: '#374151' }}>
            Initial Delay Seconds:
          </Typography>
          <Typography sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '500', lineHeight: '20px', color: '#737373', maxWidth: '570px' }}>
            {containerItem?.readiness_probe?.initial_delay_seconds || '-'}
          </Typography>
        </Box>
        <Box sx={{ display: 'flex', flex: 1, marginBottom: '8px' }}>
          <Typography width={'150px'} sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '500', lineHeight: '20px', color: '#374151' }}>
            Timeout Seconds:
          </Typography>
          <Typography sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '500', lineHeight: '20px', color: '#737373', maxWidth: '570px' }}>
            {containerItem?.readiness_probe?.timeout_seconds || '-'}
          </Typography>
        </Box>
        <Box sx={{ display: 'flex', flex: 1, marginBottom: '8px' }}>
          <Typography width={'150px'} sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '500', lineHeight: '20px', color: '#374151' }}>
            Period Seconds:
          </Typography>
          <Typography sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '500', lineHeight: '20px', color: '#737373', maxWidth: '570px' }}>
            {containerItem?.readiness_probe?.period_seconds || '-'}
          </Typography>
        </Box>
        <Box sx={{ display: 'flex', flex: 1, marginBottom: '8px' }}>
          <Typography width={'150px'} sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '500', lineHeight: '20px', color: '#374151' }}>
            Success Threshold:
          </Typography>
          <Typography sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '500', lineHeight: '20px', color: '#737373', maxWidth: '570px' }}>
            {containerItem?.readiness_probe?.success_threshold || '-'}
          </Typography>
        </Box>
        <Box sx={{ display: 'flex', flex: 1, marginBottom: '8px' }}>
          <Typography width={'150px'} sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '500', lineHeight: '20px', color: '#374151' }}>
            Failure Threshold:
          </Typography>
          <Typography sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '500', lineHeight: '20px', color: '#737373', maxWidth: '570px' }}>
            {containerItem?.readiness_probe?.failure_threshold || '-'}
          </Typography>
        </Box>
        <Box sx={{ display: 'flex', flex: 1, marginBottom: '8px' }}>
          <Typography width={'150px'} sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '500', lineHeight: '20px', color: '#374151' }}>
            Termination Grace Period Seconds:
          </Typography>
          <Typography sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '500', lineHeight: '20px', color: '#737373', maxWidth: '570px' }}>
            {containerItem?.readiness_probe?.termination_grace_period_seconds || '-'}
          </Typography>
        </Box>
      </Box>
      <Divider sx={{ margin: '20px 0px' }} />
      <Box>
        <Typography sx={{ fontFamily: 'Roboto', fontWeight: 600, fontSize: 12, color: '#9F9F9F' }}>ARGUMENTS</Typography>

        <Box sx={{ display: 'flex', flex: 1, marginBottom: '8px' }}>
          <Typography width={'150px'} sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '500', lineHeight: '20px', color: '#374151' }}>
            Arguments:
          </Typography>
          <Typography sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '500', lineHeight: '20px', color: '#737373', maxWidth: '570px' }}>
            -
          </Typography>
        </Box>
      </Box>
    </Box>
  );
};

ContainerDetails.propTypes = {
  containerItem: PropTypes.object,
};

export default ContainerDetails;
