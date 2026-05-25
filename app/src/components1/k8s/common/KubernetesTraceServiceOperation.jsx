import NDialog from '@components1/common/modal/NDialog';
import TraceGanttChart from './TracesChart';
import { useEffect, useState } from 'react';
import apiTrace from '@api1/kubernetes/trace';
import CodeMirror, { EditorView } from '@uiw/react-codemirror';
import { colors } from 'src/utils/colors';
import PropTypes from 'prop-types';
import { json } from '@codemirror/lang-json';
import { formatDurationInTrace } from 'src/utils/common';

export const KubernetesTraceServiceOperation = ({ accountId, query, traceData }) => {
  const [data, setData] = useState([]);
  const [loading, setLoading] = useState(false);
  const [selectedTrace, setSelectedTrace] = useState(null);
  const [showModal, setShowModal] = useState(false);

  const randomColors = [
    colors.text.traceRed,
    colors.text.tracBlue,
    colors.text.traceYellow,
    colors.text.traceGreen,
    colors.text.tracePurple,
    colors.text.traceOrange,
    colors.text.traceGray,
    colors.text.traceCyan,
    colors.text.traceMagenta,
    colors.text.traceLime,
    colors.text.traceTeal,
    colors.text.traceBrown,
    colors.text.tracePink,
    colors.text.traceNavy,
    colors.text.traceGold,
    colors.text.traceOlive,
    colors.text.traceDarkGray,
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
          const traceDataRows = res?.traces_heat_map ?? [];
          setData(traceDataRows);
        }
      })
      .finally(() => {
        setLoading(false);
      });
  }, [JSON.stringify(query), accountId, JSON.stringify(traceData)]);

  const additionalComponent = () => {
    return (
      <CodeMirror
        value={JSON.stringify(selectedTrace, null, 2)}
        height='400px'
        extensions={[json(), EditorView.lineWrapping]}
        editable={false}
        style={{
          border: '1px solid silver',
          borderRadius: '5px',
          padding: '10px',
          backgroundColor: colors.background.codeMirror,
          boxShadow: '0 4px 8px rgba(0, 0, 0, 0.1)',
        }}
      />
    );
  };

  const handleClose = () => {
    setShowModal(false);
    setSelectedTrace({});
  };

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
    <>
      <NDialog
        isSubmitRequired={false}
        handleClose={handleClose}
        dialogTitle={'Trace Details'}
        open={showModal}
        additionalComponent={additionalComponent()}
      />
      <div
        style={{
          padding: '16px 20px',
          display: 'flex',
          flexDirection: 'row',
          gap: '48px',
          backgroundColor: 'white',
          borderRadius: '8px 8px 0px 0px',
        }}
      >
        <div style={{ fontWeight: '400', fontSize: '13px', color: colors.text.secondary, paddingBottom: '4px', gap: '4px' }}>
          Trace ID
          <div style={{ fontWeight: '500', fontSize: '14px', color: colors.text.secondary, paddingTop: '4px' }}>{query.trace_id}</div>
        </div>
        <div style={{ fontWeight: '400', fontSize: '13px', color: colors.text.secondary, paddingBottom: '4px', gap: '4px' }}>
          Started at
          <div style={{ fontWeight: '500', fontSize: '14px', color: colors.text.secondary, paddingTop: '4px' }}>
            {new Date(query.timestamp).toLocaleString()}
          </div>
        </div>
        <div style={{ fontWeight: '400', fontSize: '13px', color: colors.text.secondary, paddingBottom: '4px', gap: '4px' }}>
          Duration
          <div style={{ fontWeight: '500', fontSize: '14px', color: colors.text.secondary, paddingTop: '4px' }}>
            {formatDurationInTrace(query.duration_ns)}
          </div>
        </div>

        {httpStatusCode && (
          <div style={{ fontWeight: '400', fontSize: '13px', color: colors.text.secondary, paddingBottom: '4px', gap: '4px' }}>
            Status
            <div style={{ fontWeight: '500', fontSize: '14px', color: colors.text.secondary, paddingTop: '4px' }}>{httpStatusCode}</div>
          </div>
        )}
      </div>
      <TraceGanttChart data={data} loading={loading} randomColors={randomColors} />
    </>
  );
};

KubernetesTraceServiceOperation.propTypes = {
  accountId: PropTypes.string,
  query: PropTypes.object,
  traceData: PropTypes.array,
};
