import { Box } from '@mui/material';
import PropTypes from 'prop-types';
import { ds } from 'src/utils/colors';

const isConnectedUsingDate = (lastConnectedDateStr) => {
  if (!lastConnectedDateStr) {
    return false;
  }
  // If last connected is more than 2 days ago, mark it as disconnected
  const lastConnectedDate = new Date(lastConnectedDateStr);
  return new Date().getTime() - lastConnectedDate.getTime() < 2 * 24 * 3600 * 1000;
};

/**
 * Is a connected agent fully healthy (green) or only partly (amber)?
 *
 * Exported because the cluster dropdown sorts by the same verdict — it used to
 * carry its own copy of this function, which is exactly the kind of pair that
 * drifts.
 *
 * Only meaningful for an agent already reporting CONNECTED; the caller decides
 * red/grey before asking.
 */
export const checkConnections = (clusterData = {}) => {
  const provider = clusterData.cloud_provider?.toLowerCase();
  const connectionStatus = clusterData.agent?.connection_status;

  // A self-hosted fleet has no cloud sync to be stale and no in-cluster agent to
  // interrogate — the proxy agent connection IS the health, qualified by the
  // datasources it reports. Without this it fell through to the cloud branch
  // below, which demands four sync timestamps a VM account never produces, and
  // so could never read anything but amber.
  if (provider === 'selfhosted') {
    const datasources = Object.values(connectionStatus?.datasources || {});
    return datasources.every((datasource) => datasource?.status === 'healthy');
  }

  if (provider != 'k8s') {
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

  if (!connectionStatus) {
    return false;
  }

  // Signals the agent may not be the one serving. When a non-agent integration is the
  // account's provider for one of these, the agent's flag describes a backend it never
  // touches — demanding it kept a cluster amber forever on healthy SaaS logs or metrics.
  // A healthy integration satisfies the check instead, exactly as opencostServerSide does
  // for OpenCost below. `integration_id` present means the provider-status check probed a
  // real backend; see api-server observability/provider_status.go.
  const providerStatus = connectionStatus.providerStatus;
  const servedByHealthyIntegration = (signal) => {
    const entry = providerStatus?.[signal];
    return !!entry?.integration_id && !!entry.connected;
  };

  // Relay and the node agent are the agent's own business; nothing can stand in for them.
  const agentOwnedProps = ['nodeAgentConnection', 'relayConnection'];
  for (const prop of agentOwnedProps) {
    if (!connectionStatus[prop]) {
      return false;
    }
  }

  if (!connectionStatus.logsConnection && !servedByHealthyIntegration('logs')) {
    return false;
  }

  if (!connectionStatus.prometheusConnection && !servedByHealthyIntegration('metrics')) {
    return false;
  }

  // OpenCost is healthy when cost is collected either in-cluster (legacy opencostConnection)
  // or server-side (opencostServerSide, stamped by the backend spend sync post-migration).
  if (!connectionStatus.opencostConnection && !connectionStatus.opencostServerSide) {
    return false;
  }

  return true;
};

const ClusterStatusIndicator = ({ clusterData = {}, showBorder = false }) => {
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
