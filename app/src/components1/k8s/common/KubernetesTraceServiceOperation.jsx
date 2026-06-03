import TraceGanttChart from './TracesChart';
import { useEffect, useState } from 'react';
import apiTrace from '@api1/kubernetes/trace';
import PropTypes from 'prop-types';
import { formatDurationInTrace } from 'src/utils/common';
import { Label } from '@components1/ds/Label';
import { Card } from '@components1/ds/Card';
import WidgetCard from '@components1/ds/WidgetCard';
import { Box, Typography } from '@mui/material';

export const KubernetesTraceServiceOperation = ({ accountId, query, traceData }) => {
  const [data, setData] = useState([]);
  const [loading, setLoading] = useState(false);

  const randomColors = [
    'var(--ds-red-300)',
    'var(--ds-blue-400)',
    'var(--ds-yellow-400)',
    'var(--ds-green-400)',
    'var(--ds-purple-400)',
    'var(--ds-amber-400)',
    'var(--ds-gray-400)',
    'var(--ds-teal-400)',
    'var(--ds-pink-500)',
    'var(--ds-green-300)',
    'var(--ds-teal-500)',
    'var(--ds-amber-700)',
    'var(--ds-pink-400)',
    'var(--ds-brand-600)',
    'var(--ds-yellow-500)',
    'var(--ds-yellow-700)',
    'var(--ds-gray-600)',
  ];

  useEffect(() => {
    if (traceData && traceData.length > 0) {
      setData(traceData);
      return;
    }
    if (!query.trace_id || !accountId) {
      return;
    }
    setData([]);
    setLoading(true);
    apiTrace
      .traceServiceAndOperationV2(accountId, query.trace_id)
      .then((res) => {
        if (res) {
          const traceDataRows = res?.traces_get_heatmap ?? [];
          setData(traceDataRows);
        }
      })
      .finally(() => {
        setLoading(false);
      });
  }, [JSON.stringify(query), accountId, JSON.stringify(traceData)]);

  const getHttpStatus = (item) => {
    let httpStatusText = '';
    if (item) {
      if (item.span_name !== 'query') {
        httpStatusText = item.http_status_code ? 'HTTP-' + item.http_status_code : '';
      } else if (item.status_code === 'STATUS_CODE_UNSET') {
        httpStatusText = 'OK';
      } else if (item.status_code === 'STATUS_CODE_ERROR') {
        httpStatusText = 'ERROR';
      }
    }
    return httpStatusText;
  };

  const httpStatusCode = getHttpStatus(query);

  return (
    <WidgetCard>
      <Card variant='outlined' size='md' header={<Typography sx={{ fontWeight: 'bold' }}>Trace Info</Typography>}>
        <div style={{ display: 'flex', flexDirection: 'row', gap: 'var(--ds-space-7)' }}>
          <div
            style={{
              fontWeight: 'var(--ds-font-weight-regular)',
              fontSize: 'var(--ds-text-body)',
              color: 'var(--ds-gray-700)',
              paddingBottom: 'var(--ds-space-1)',
              gap: 'var(--ds-space-1)',
            }}
          >
            Trace ID
            <div
              style={{
                fontWeight: 'var(--ds-font-weight-medium)',
                fontSize: 'var(--ds-text-body-lg)',
                color: 'var(--ds-gray-700)',
                paddingTop: 'var(--ds-space-1)',
              }}
            >
              {query.trace_id}
            </div>
          </div>
          <div
            style={{
              fontWeight: 'var(--ds-font-weight-regular)',
              fontSize: 'var(--ds-text-body)',
              color: 'var(--ds-gray-700)',
              paddingBottom: 'var(--ds-space-1)',
              gap: 'var(--ds-space-1)',
            }}
          >
            Started at
            <div
              style={{
                fontWeight: 'var(--ds-font-weight-medium)',
                fontSize: 'var(--ds-text-body-lg)',
                color: 'var(--ds-gray-700)',
                paddingTop: 'var(--ds-space-1)',
              }}
            >
              {new Date(query.timestamp).toLocaleString()}
            </div>
          </div>
          <div
            style={{
              fontWeight: 'var(--ds-font-weight-regular)',
              fontSize: 'var(--ds-text-body)',
              color: 'var(--ds-gray-700)',
              paddingBottom: 'var(--ds-space-1)',
              gap: 'var(--ds-space-1)',
            }}
          >
            Duration
            <div
              style={{
                fontWeight: 'var(--ds-font-weight-medium)',
                fontSize: 'var(--ds-text-body-lg)',
                color: 'var(--ds-gray-700)',
                paddingTop: 'var(--ds-space-1)',
              }}
            >
              {formatDurationInTrace(query.duration_ns)}
            </div>
          </div>

          {httpStatusCode && (
            <div
              style={{
                fontWeight: 'var(--ds-font-weight-regular)',
                fontSize: 'var(--ds-text-body)',
                color: 'var(--ds-gray-700)',
                paddingBottom: 'var(--ds-space-1)',
                gap: 'var(--ds-space-1)',
              }}
            >
              Status
              <div style={{ paddingTop: 'var(--ds-space-1)' }}>
                <Label text={httpStatusCode} />
              </div>
            </div>
          )}
        </div>
      </Card>
      <Box sx={{ mt: 'var(--ds-space-3)' }}>
        <TraceGanttChart data={data} loading={loading} randomColors={randomColors} />
      </Box>
    </WidgetCard>
  );
};

KubernetesTraceServiceOperation.propTypes = {
  accountId: PropTypes.string,
  query: PropTypes.object,
  traceData: PropTypes.array,
};
