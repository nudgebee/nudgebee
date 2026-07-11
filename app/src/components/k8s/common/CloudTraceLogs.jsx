import { useEffect, useState, useCallback } from 'react';
import PropTypes from 'prop-types';
import { Box, Typography, CircularProgress } from '@mui/material';
import observability from '@api1/observability';
import CloudLogViewer from '@components/cloudaccount/investigate/cards/CloudLogViewer';

// CloudTraceLogs fetches GCP Cloud Logging entries correlated to a single trace.
// It reuses the existing native cloud-logs query (observability.fetchLogs ->
// cloud.QueryLogs); `trace="projects/<project>/traces/<id>"` is a real Cloud
// Logging filter string, so it scopes the result to exactly this trace's logs
// (request log + the service's own app logs for that request) and bypasses scope
// resolution. Rendering is delegated to CloudLogViewer for parity with the
// Cloud Logs evidence card.
const CloudTraceLogs = ({ accountId, project, region, serviceName, traceId, timestamp }) => {
  const [logs, setLogs] = useState([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);

  const fetchLogs = useCallback(async () => {
    if (!traceId || !project) {
      setLoading(false);
      setError(project ? null : 'Trace is missing its GCP project; cannot query logs.');
      return;
    }
    const base = timestamp ? new Date(timestamp).getTime() : Date.now();
    setLoading(true);
    setError(null);
    try {
      const res = await observability.fetchLogs({
        account_id: accountId,
        log_provider: 'aws_cloudwatch', // "native" sentinel; backend dispatches by cloud-account type (GCP here)
        log_provider_source: 'user',
        query: `trace="projects/${project}/traces/${traceId}"`,
        start_time: base - 5 * 60 * 1000,
        end_time: base + 5 * 60 * 1000,
        limit: 200,
        request: { region, service_name: serviceName },
      });
      const list = res?.data?.data?.logs_list || [];
      // observability flattens GCP label pairs into a `labels` object; CloudLogViewer
      // renders structured fields from `attributes`, so alias the two.
      setLogs(
        list.map((l) => ({
          timestamp: l.timestamp,
          message: l.message,
          attributes: l.labels && typeof l.labels === 'object' ? l.labels : {},
        }))
      );
    } catch (err) {
      setError(err?.response?.data?.errors?.[0]?.message || err?.message || 'Failed to fetch logs');
    } finally {
      setLoading(false);
    }
  }, [accountId, project, region, serviceName, traceId, timestamp]);

  useEffect(() => {
    fetchLogs();
  }, [fetchLogs]);

  if (loading) {
    return (
      <Box sx={{ display: 'flex', justifyContent: 'center', p: 'var(--ds-space-4, 16px)' }}>
        <CircularProgress size={20} />
      </Box>
    );
  }
  if (error) {
    return (
      <Box sx={{ p: 'var(--ds-space-4, 16px)' }}>
        <Typography sx={{ color: 'var(--ds-text-danger, #d32f2f)' }}>{error}</Typography>
      </Box>
    );
  }
  if (!logs.length) {
    return (
      <Box sx={{ p: 'var(--ds-space-4, 16px)' }}>
        <Typography>No logs found for this trace.</Typography>
      </Box>
    );
  }
  return <CloudLogViewer logs={logs} />;
};

CloudTraceLogs.propTypes = {
  accountId: PropTypes.string,
  project: PropTypes.string,
  region: PropTypes.string,
  serviceName: PropTypes.string,
  traceId: PropTypes.string,
  timestamp: PropTypes.oneOfType([PropTypes.string, PropTypes.number]),
};

export default CloudTraceLogs;
