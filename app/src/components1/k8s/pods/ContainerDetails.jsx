import { Box, Typography } from '@mui/material';
import { Divider } from '@components1/ds/Divider';
import { Label } from '@components1/ds/Label';
import { ds } from '@utils/colors';
import PropTypes from 'prop-types';

const ContainerDetails = ({ containerItem }) => {
  const MapEnvironment = ({ label }) => {
    const labelArray = [];

    for (const item in label) {
      var name = label[item].name + '=' + label[item].value;
      labelArray.push(
        <Box key={item.id} sx={{ margin: '0 var(--ds-space-2) var(--ds-space-4) 0' }}>
          <Label textTransform='none' height='100%' wordBreak='break-all' text={name} />
        </Box>
      );
    }
    return labelArray;
  };

  return (
    <Box paddingLeft={ds.space.mul(1, 7)}>
      <Box>
        <Box sx={{ display: 'flex', flex: 1, marginBottom: 'var(--ds-space-2)' }}>
          <Typography
            width={ds.space.mul(0, 75)}
            sx={{
              fontFamily: 'Roboto',
              fontSize: 'var(--ds-text-body-lg)',
              fontWeight: 'var(--ds-font-weight-medium)',
              lineHeight: '20px',
              color: 'var(--ds-brand-500)',
            }}
          >
            Image:
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
            {containerItem?.image}
          </Typography>
        </Box>
        <Box sx={{ display: 'flex', flex: 1, marginBottom: 'var(--ds-space-2)' }}>
          <Typography
            width={ds.space.mul(0, 75)}
            sx={{
              fontFamily: 'Roboto',
              fontSize: 'var(--ds-text-body-lg)',
              fontWeight: 'var(--ds-font-weight-medium)',
              lineHeight: '20px',
              color: 'var(--ds-brand-500)',
            }}
          >
            ImagePullPolicy:
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
            {}
          </Typography>
        </Box>
        <Box sx={{ display: 'flex', flex: 1, marginBottom: 'var(--ds-space-2)' }}>
          <Typography
            width={ds.space.mul(0, 75)}
            sx={{
              fontFamily: 'Roboto',
              fontSize: 'var(--ds-text-body-lg)',
              fontWeight: 'var(--ds-font-weight-medium)',
              lineHeight: '20px',
              color: 'var(--ds-brand-500)',
            }}
          >
            Environment:
          </Typography>
          <Box sx={{ display: 'flex', flexDirection: 'row', flexWrap: 'wrap', maxWidth: '80%' }}>{<MapEnvironment label={containerItem?.env} />}</Box>
        </Box>
      </Box>
      <Divider sx={{ margin: 'var(--ds-space-4) 0px' }} />
      <Box>
        <Typography sx={{ fontFamily: 'Roboto', fontWeight: 'var(--ds-font-weight-semibold)', fontSize: ds.text.small, color: 'var(--ds-gray-400)' }}>
          PORTS
        </Typography>
        {containerItem?.ports && containerItem?.ports.length > 0 ? (
          <Box key={Date.now()} sx={{ display: 'flex', flex: 1, marginBottom: 'var(--ds-space-2)' }}>
            <Typography
              width={ds.space.mul(0, 75)}
              sx={{
                fontFamily: 'Roboto',
                fontSize: 'var(--ds-text-body-lg)',
                fontWeight: 'var(--ds-font-weight-medium)',
                lineHeight: '20px',
                color: 'var(--ds-brand-500)',
              }}
            >
              {containerItem?.ports.toString()}
            </Typography>
          </Box>
        ) : (
          <></>
        )}
      </Box>
      <Divider sx={{ margin: 'var(--ds-space-4) 0px' }} />

      <Box>
        <Typography sx={{ fontFamily: 'Roboto', fontWeight: 'var(--ds-font-weight-semibold)', fontSize: ds.text.small, color: 'var(--ds-gray-400)' }}>
          RESOURCES
        </Typography>
        <Box sx={{ display: 'flex', flex: 1, marginBottom: 'var(--ds-space-2)' }}>
          <Typography
            width={ds.space.mul(0, 75)}
            sx={{
              fontFamily: 'Roboto',
              fontSize: 'var(--ds-text-body-lg)',
              fontWeight: 'var(--ds-font-weight-medium)',
              lineHeight: '20px',
              color: 'var(--ds-brand-500)',
            }}
          >
            Requests:
          </Typography>
          {containerItem?.resources?.requests?.memory && (
            <Typography
              sx={{
                fontFamily: 'Roboto',
                fontSize: 'var(--ds-text-body-lg)',
                fontWeight: 'var(--ds-font-weight-medium)',
                lineHeight: '20px',
                color: 'var(--ds-gray-600)',
                marginRight: 'var(--ds-space-4)',
                minWidth: ds.space.mul(0, 50),
              }}
            >
              Memory: {containerItem?.resources?.requests?.memory}
            </Typography>
          )}
          {containerItem?.resources?.requests?.cpu && (
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
              CPU: {containerItem?.resources?.requests?.cpu}
            </Typography>
          )}
        </Box>
        <Box sx={{ display: 'flex', flex: 1, marginBottom: 'var(--ds-space-2)' }}>
          <Typography
            width={ds.space.mul(0, 75)}
            sx={{
              fontFamily: 'Roboto',
              fontSize: 'var(--ds-text-body-lg)',
              fontWeight: 'var(--ds-font-weight-medium)',
              lineHeight: '20px',
              color: 'var(--ds-brand-500)',
            }}
          >
            Limits:
          </Typography>
          {containerItem?.resources?.limits?.memory && (
            <Typography
              sx={{
                fontFamily: 'Roboto',
                fontSize: 'var(--ds-text-body-lg)',
                fontWeight: 'var(--ds-font-weight-medium)',
                lineHeight: '20px',
                color: 'var(--ds-gray-600)',
                marginRight: 'var(--ds-space-4)',
                minWidth: ds.space.mul(0, 50),
              }}
            >
              Memory: {containerItem?.resources?.limits?.memory}
            </Typography>
          )}
          {containerItem?.resources?.limits?.cpu && (
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
              CPU: {containerItem?.resources?.limits?.cpu}
            </Typography>
          )}
        </Box>
      </Box>
      <Divider sx={{ margin: 'var(--ds-space-4) 0px' }} />
      <Box>
        <Typography sx={{ fontFamily: 'Roboto', fontWeight: 'var(--ds-font-weight-semibold)', fontSize: ds.text.small, color: 'var(--ds-gray-400)' }}>
          MOUNTS
        </Typography>
        <Box sx={{ display: 'flex', flex: 1, marginBottom: 'var(--ds-space-2)' }}>
          <Typography
            width={ds.space.mul(0, 75)}
            sx={{
              fontFamily: 'Roboto',
              fontSize: 'var(--ds-text-body-lg)',
              fontWeight: 'var(--ds-font-weight-medium)',
              lineHeight: '20px',
              color: 'var(--ds-brand-500)',
            }}
          >
            Mounts:
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
            <Label text={'-'} />
          </Typography>
        </Box>
      </Box>
      <Divider sx={{ margin: 'var(--ds-space-4) 0px' }} />
      <Box>
        <Typography sx={{ fontFamily: 'Roboto', fontWeight: 'var(--ds-font-weight-semibold)', fontSize: ds.text.small, color: 'var(--ds-gray-400)' }}>
          LIVENESS PROBE
        </Typography>
        <Box sx={{ display: 'flex', flex: 1, marginBottom: 'var(--ds-space-2)' }}>
          <Typography
            width={ds.space.mul(0, 75)}
            sx={{
              fontFamily: 'Roboto',
              fontSize: 'var(--ds-text-body-lg)',
              fontWeight: 'var(--ds-font-weight-medium)',
              lineHeight: '20px',
              color: 'var(--ds-brand-500)',
            }}
          >
            Path:
          </Typography>
          <Label textTransform='none' text={containerItem?.liveness_probe?.httpGet?.path || '-'} />
        </Box>
        <Box sx={{ display: 'flex', flex: 1, marginBottom: 'var(--ds-space-2)' }}>
          <Typography
            width={ds.space.mul(0, 75)}
            sx={{
              fontFamily: 'Roboto',
              fontSize: 'var(--ds-text-body-lg)',
              fontWeight: 'var(--ds-font-weight-medium)',
              lineHeight: '20px',
              color: 'var(--ds-brand-500)',
            }}
          >
            Initial Delay Seconds:
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
            {containerItem?.liveness_probe?.initial_delay_seconds || '-'}
          </Typography>
        </Box>
        <Box sx={{ display: 'flex', flex: 1, marginBottom: 'var(--ds-space-2)' }}>
          <Typography
            width={ds.space.mul(0, 75)}
            sx={{
              fontFamily: 'Roboto',
              fontSize: 'var(--ds-text-body-lg)',
              fontWeight: 'var(--ds-font-weight-medium)',
              lineHeight: '20px',
              color: 'var(--ds-brand-500)',
            }}
          >
            Timeout Seconds:
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
            {containerItem?.liveness_probe?.timeout_seconds || '-'}
          </Typography>
        </Box>
        <Box sx={{ display: 'flex', flex: 1, marginBottom: 'var(--ds-space-2)' }}>
          <Typography
            width={ds.space.mul(0, 75)}
            sx={{
              fontFamily: 'Roboto',
              fontSize: 'var(--ds-text-body-lg)',
              fontWeight: 'var(--ds-font-weight-medium)',
              lineHeight: '20px',
              color: 'var(--ds-brand-500)',
            }}
          >
            Period Seconds:
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
            {containerItem?.liveness_probe?.period_seconds || '-'}
          </Typography>
        </Box>
        <Box sx={{ display: 'flex', flex: 1, marginBottom: 'var(--ds-space-2)' }}>
          <Typography
            width={ds.space.mul(0, 75)}
            sx={{
              fontFamily: 'Roboto',
              fontSize: 'var(--ds-text-body-lg)',
              fontWeight: 'var(--ds-font-weight-medium)',
              lineHeight: '20px',
              color: 'var(--ds-brand-500)',
            }}
          >
            Success Threshold:
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
            {containerItem?.liveness_probe?.success_threshold || '-'}
          </Typography>
        </Box>
        <Box sx={{ display: 'flex', flex: 1, marginBottom: 'var(--ds-space-2)' }}>
          <Typography
            width={ds.space.mul(0, 75)}
            sx={{
              fontFamily: 'Roboto',
              fontSize: 'var(--ds-text-body-lg)',
              fontWeight: 'var(--ds-font-weight-medium)',
              lineHeight: '20px',
              color: 'var(--ds-brand-500)',
            }}
          >
            Failure Threshold:
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
            {containerItem?.liveness_probe?.failure_threshold || '-'}
          </Typography>
        </Box>
        <Box sx={{ display: 'flex', flex: 1, marginBottom: 'var(--ds-space-2)' }}>
          <Typography
            width={ds.space.mul(0, 75)}
            sx={{
              fontFamily: 'Roboto',
              fontSize: 'var(--ds-text-body-lg)',
              fontWeight: 'var(--ds-font-weight-medium)',
              lineHeight: '20px',
              color: 'var(--ds-brand-500)',
            }}
          >
            Termination Grace Period Seconds:
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
            {containerItem?.liveness_probe?.termination_grace_period_seconds || '-'}
          </Typography>
        </Box>
      </Box>
      <Divider sx={{ margin: 'var(--ds-space-4) 0px' }} />
      <Box>
        <Typography sx={{ fontFamily: 'Roboto', fontWeight: 'var(--ds-font-weight-semibold)', fontSize: ds.text.small, color: 'var(--ds-gray-400)' }}>
          READINESS PROBE
        </Typography>
        <Box sx={{ display: 'flex', flex: 1, marginBottom: 'var(--ds-space-2)' }}>
          <Typography
            width={ds.space.mul(0, 75)}
            sx={{
              fontFamily: 'Roboto',
              fontSize: 'var(--ds-text-body-lg)',
              fontWeight: 'var(--ds-font-weight-medium)',
              lineHeight: '20px',
              color: 'var(--ds-brand-500)',
            }}
          >
            Path:
          </Typography>
          <Label textTransform='none' text={containerItem?.readiness_probe?.httpGet?.path || '-'} />
        </Box>
        <Box sx={{ display: 'flex', flex: 1, marginBottom: 'var(--ds-space-2)' }}>
          <Typography
            width={ds.space.mul(0, 75)}
            sx={{
              fontFamily: 'Roboto',
              fontSize: 'var(--ds-text-body-lg)',
              fontWeight: 'var(--ds-font-weight-medium)',
              lineHeight: '20px',
              color: 'var(--ds-brand-500)',
            }}
          >
            Initial Delay Seconds:
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
            {containerItem?.readiness_probe?.initial_delay_seconds || '-'}
          </Typography>
        </Box>
        <Box sx={{ display: 'flex', flex: 1, marginBottom: 'var(--ds-space-2)' }}>
          <Typography
            width={ds.space.mul(0, 75)}
            sx={{
              fontFamily: 'Roboto',
              fontSize: 'var(--ds-text-body-lg)',
              fontWeight: 'var(--ds-font-weight-medium)',
              lineHeight: '20px',
              color: 'var(--ds-brand-500)',
            }}
          >
            Timeout Seconds:
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
            {containerItem?.readiness_probe?.timeout_seconds || '-'}
          </Typography>
        </Box>
        <Box sx={{ display: 'flex', flex: 1, marginBottom: 'var(--ds-space-2)' }}>
          <Typography
            width={ds.space.mul(0, 75)}
            sx={{
              fontFamily: 'Roboto',
              fontSize: 'var(--ds-text-body-lg)',
              fontWeight: 'var(--ds-font-weight-medium)',
              lineHeight: '20px',
              color: 'var(--ds-brand-500)',
            }}
          >
            Period Seconds:
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
            {containerItem?.readiness_probe?.period_seconds || '-'}
          </Typography>
        </Box>
        <Box sx={{ display: 'flex', flex: 1, marginBottom: 'var(--ds-space-2)' }}>
          <Typography
            width={ds.space.mul(0, 75)}
            sx={{
              fontFamily: 'Roboto',
              fontSize: 'var(--ds-text-body-lg)',
              fontWeight: 'var(--ds-font-weight-medium)',
              lineHeight: '20px',
              color: 'var(--ds-brand-500)',
            }}
          >
            Success Threshold:
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
            {containerItem?.readiness_probe?.success_threshold || '-'}
          </Typography>
        </Box>
        <Box sx={{ display: 'flex', flex: 1, marginBottom: 'var(--ds-space-2)' }}>
          <Typography
            width={ds.space.mul(0, 75)}
            sx={{
              fontFamily: 'Roboto',
              fontSize: 'var(--ds-text-body-lg)',
              fontWeight: 'var(--ds-font-weight-medium)',
              lineHeight: '20px',
              color: 'var(--ds-brand-500)',
            }}
          >
            Failure Threshold:
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
            {containerItem?.readiness_probe?.failure_threshold || '-'}
          </Typography>
        </Box>
        <Box sx={{ display: 'flex', flex: 1, marginBottom: 'var(--ds-space-2)' }}>
          <Typography
            width={ds.space.mul(0, 75)}
            sx={{
              fontFamily: 'Roboto',
              fontSize: 'var(--ds-text-body-lg)',
              fontWeight: 'var(--ds-font-weight-medium)',
              lineHeight: '20px',
              color: 'var(--ds-brand-500)',
            }}
          >
            Termination Grace Period Seconds:
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
            {containerItem?.readiness_probe?.termination_grace_period_seconds || '-'}
          </Typography>
        </Box>
      </Box>
      <Divider sx={{ margin: 'var(--ds-space-4) 0px' }} />
      <Box>
        <Typography sx={{ fontFamily: 'Roboto', fontWeight: 'var(--ds-font-weight-semibold)', fontSize: ds.text.small, color: 'var(--ds-gray-400)' }}>
          ARGUMENTS
        </Typography>

        <Box sx={{ display: 'flex', flex: 1, marginBottom: 'var(--ds-space-2)' }}>
          <Typography
            width={ds.space.mul(0, 75)}
            sx={{
              fontFamily: 'Roboto',
              fontSize: 'var(--ds-text-body-lg)',
              fontWeight: 'var(--ds-font-weight-medium)',
              lineHeight: '20px',
              color: 'var(--ds-brand-500)',
            }}
          >
            Arguments:
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
