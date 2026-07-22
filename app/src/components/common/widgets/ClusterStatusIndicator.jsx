import { Box } from '@mui/material';
import PropTypes from 'prop-types';
import { ds } from 'src/utils/colors';

const ClusterStatusIndicator = ({ clusterData = {}, showBorder = false }) => {
  const isConnectedUsingDate = (lastConnectedDateStr) => {
    if (!lastConnectedDateStr) {
      return false;
    }
    // If last connected is more than 2 days ago, mark it as disconnected
    const lastConnectedDate = new Date(lastConnectedDateStr);
    return new Date().getTime() - lastConnectedDate.getTime() < 2 * 24 * 3600 * 1000;
  };

  const checkConnections = (clusterData) => {
    if (clusterData.cloud_provider?.toLowerCase() != 'k8s') {
      const connectionStatus = clusterData.agent?.connection_status;

      if (!connectionStatus) {
        return clusterData.agent?.status === 'CONNECTED';
      }

      const servicesStatus = {
        events: isConnectedUsingDate(connectionStatus?.events?.end),
        resources: isConnectedUsingDate(connectionStatus?.resources?.updated_at),
        recommendations: isConnectedUsingDate(connectionStatus?.recommendations?.updated_at),
        spends: isConnectedUsingDate(connectionStatus?.spends?.updated_at),
      };

      return Object.values(servicesStatus).every((status) => status === true);
    }

    const connectionStatus = clusterData.agent?.connection_status;
    if (!connectionStatus) {
      return false;
    }

    const requiredProps = ['logsConnection', 'nodeAgentConnection', 'prometheusConnection', 'relayConnection'];

    for (const prop of requiredProps) {
      if (!connectionStatus[prop]) {
        return false;
      }
    }

    // OpenCost is healthy when cost is collected either in-cluster (legacy opencostConnection)
    // or server-side (opencostServerSide, stamped by the backend spend sync post-migration).
    if (!connectionStatus.opencostConnection && !connectionStatus.opencostServerSide) {
      return false;
    }

    return true;
  };

  if (clusterData?.agent?.status === 'CONNECTED') {
    const color = checkConnections(clusterData) ? ds.green[400] : ds.amber[400];
    return (
      <Box
        sx={{
          minHeight: `${ds.space.mul(0, 18)} !important`,
          minWidth: `${ds.space.mul(0, 18)} !important`,
          height: `${ds.space.mul(0, 18)} !important`,
          width: `${ds.space.mul(0, 18)} !important`,
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          position: 'relative',
          '&::after': {
            content: showBorder ? `""` : null,
            background: ds.gray[300],
            height: ds.space[5],
            width: '1px',
            position: 'absolute',
            right: 0,
          },
        }}
      >
        <Box
          sx={{
            padding: 'var(--ds-space-1)',
            width: '7px',
            height: '7px',
            border: `1px solid ${color}`,
            borderRadius: '100%',
          }}
        >
          <Box
            sx={{
              bgcolor: color,
              width: '7px',
              height: '7px',
              borderRadius: '100%',
            }}
          />
        </Box>
      </Box>
    );
  }

  const dotColor = clusterData?.agent?.status === 'NOT_CONNECTED' ? ds.red[600] : ds.gray[400];
  const bgColor = clusterData?.agent?.status === 'NOT_CONNECTED' ? ds.red[500] : ds.gray[400];

  return (
    <Box
      sx={{
        minHeight: `${ds.space.mul(0, 18)} !important`,
        minWidth: `${ds.space.mul(0, 18)} !important`,
        height: `${ds.space.mul(0, 18)} !important`,
        width: `${ds.space.mul(0, 18)} !important`,
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        position: 'relative',
        '&::after': {
          content: showBorder ? `""` : null,
          background: ds.gray[300],
          height: ds.space[5],
          width: '1px',
          position: 'absolute',
          right: 0,
        },
      }}
    >
      <Box
        sx={{
          padding: 'var(--ds-space-1)',
          width: '7px',
          height: '7px',
          border: `1px solid ${dotColor}`,
          borderRadius: '100%',
        }}
      >
        <Box
          sx={{
            bgcolor: bgColor,
            width: '7px',
            height: '7px',
            borderRadius: '100%',
          }}
        />
      </Box>
    </Box>
  );
};

export default ClusterStatusIndicator;

ClusterStatusIndicator.propTypes = {
  clusterData: PropTypes.any,
  showBorder: PropTypes.bool,
};
