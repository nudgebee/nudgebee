import React, { useState } from 'react';
import PropTypes from 'prop-types';
import { Box, Typography, Grid } from '@mui/material';
import { ds } from '@utils/colors';
import { getIcon } from './AgentIcon';
import {
  WrenchIcon,
  AskNudgebeeErrorIcon,
  AskNudgebeeInProgressIcon,
  AskNudgebeeSkipIcon,
  AskNudgebeeSuccessIcon,
  AskNudgebeeWaitingIcon,
} from '@assets';
import SafeIcon from '@shared/icons/SafeIcon';
import Tooltip from '@ui/Tooltip';
import Duration from './Duration';
import LLMAnswerRenderer from './LLMAnswerRenderer';
import MarkDowns from '@shared/viewers/MarkDowns';
import ReferencesPopover from './ReferencesModal';
import FileDownloadIcon from '@mui/icons-material/FileDownload';
import AutoAwesomeOutlinedIcon from '@mui/icons-material/AutoAwesomeOutlined';
import Chart from '@ui/Chart';
import Text from '@shared/format/Text';
import CustomTable from '@shared/tables/CustomTable';
import CopyButton from '@shared/buttons/CopyButton';
import { Divider } from '@ui/Divider';
import KubernetesTable from '@components/k8s/common/KubernetesTable';
import { mapToTableData } from '@components/k8s/details/KubernetesLogStash';
import { LogDate } from '@components/k8s/common/LogDate';
import KubernetesSecurityDetails from '@components/recommendations/security/KubernetesSecurityDetails';
import { DiffViewer } from '@ui/DiffViewer';
import CodeBlock from '@ui/CodeBlock';
import { convertToReadableFormat } from 'src/utils/common';

const FRIENDLY_TOOL_NAMES = {
  react_critique: 'Critique Feedback',
  context_compression: 'Context Compression',
  think: 'Thinking',
};

const cleanToolName = (name) => {
  if (!name) {
    return 'Tool Call';
  }
  const friendly = FRIENDLY_TOOL_NAMES[name] || FRIENDLY_TOOL_NAMES[name.toLowerCase()];
  if (friendly) {
    return friendly;
  }
  return name
    .replace(/_/g, ' ')
    .replace(/([a-z])([A-Z])/g, '$1 $2')
    .replace(/\b\w/g, (c) => c.toUpperCase());
};

const getStatusIcon = (status) => {
  const s = (status || '').toLowerCase();
  if (s === 'fail' || s === 'error' || s === 'failed') {
    return { icon: AskNudgebeeErrorIcon, label: 'Error' };
  }
  if (s === 'skipped') {
    return { icon: AskNudgebeeSkipIcon, label: 'Skipped' };
  }
  if (s === 'waiting' || s === 'waiting_for_client' || s === 'waiting_for_client_tool') {
    return { icon: AskNudgebeeWaitingIcon, label: 'Waiting' };
  }
  if (s === 'in_progress') {
    return { icon: AskNudgebeeInProgressIcon, label: 'In-Progress' };
  }
  return { icon: AskNudgebeeSuccessIcon, label: 'Success' };
};

const StatusBadge = ({ status }) => {
  if (!status) {
    return null;
  }
  const { icon, label } = getStatusIcon(status);
  return (
    <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[1] }}>
      <Tooltip title={label} placement='top'>
        <SafeIcon src={icon} alt='status icon' />
      </Tooltip>
      <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)', fontFamily: ds.font.sans }}>{label}</Typography>
    </Box>
  );
};

StatusBadge.propTypes = {
  status: PropTypes.string,
};

const preStyle = {
  margin: 0,
  color: 'var(--ds-brand-150)',
  fontSize: 'var(--ds-text-small)',
  fontFamily: '"Roboto Mono", monospace',
  whiteSpace: 'pre-wrap',
  wordBreak: 'break-word',
  lineHeight: 1.6,
};

const tryPrettifyJson = (text) => {
  if (!text || typeof text !== 'string') {
    return null;
  }
  let trimmed = text.trim();
  const fenceMatch = trimmed.match(/^```(?:json|JSON)?\s*\n?([\s\S]*?)\n?```$/);
  if (fenceMatch) {
    trimmed = fenceMatch[1].trim();
  }
  if (!trimmed.startsWith('{') && !trimmed.startsWith('[')) {
    return null;
  }
  try {
    const parsed = JSON.parse(trimmed);
    if (parsed && typeof parsed === 'object') {
      return JSON.stringify(parsed, null, 2);
    }
  } catch (e) {
    console.warn('tryPrettifyJson: not JSON', e);
  }
  return null;
};

const prettifyJsonFencesInMarkdown = (text) => {
  if (!text || typeof text !== 'string') {
    return text;
  }
  return text.replace(/```(json|JSON)?\s*\n?([\s\S]*?)\n?```/g, (match, _lang, inner) => {
    const innerTrimmed = inner.trim();
    if (!innerTrimmed.startsWith('{') && !innerTrimmed.startsWith('[')) {
      return match;
    }
    try {
      const parsed = JSON.parse(innerTrimmed);
      if (parsed && typeof parsed === 'object') {
        return '```json\n' + JSON.stringify(parsed, null, 2) + '\n```';
      }
    } catch (e) {
      console.warn('prettifyJsonFencesInMarkdown: invalid JSON in fence', e);
    }
    return match;
  });
};

// Code-edit tools (code-analysis `replace` and friends) carry the proposed
// change as old_string/new_string in parameters — render those as a real diff
// instead of an escaped JSON blob.
const CODE_EDIT_TOOL_NAMES = ['replace', 'str_replace', 'edit_file'];

export const parseCodeEditParams = (tc) => {
  if (!tc || !CODE_EDIT_TOOL_NAMES.includes(tc.tool_name) || typeof tc.parameters !== 'string') {
    return null;
  }
  try {
    const parsed = JSON.parse(tc.parameters);
    if (parsed && typeof parsed.old_string === 'string' && typeof parsed.new_string === 'string') {
      return parsed;
    }
  } catch (e) {
    console.warn('parseCodeEditParams: parameters JSON parse failed', e);
  }
  return null;
};

export const diffLanguageForFile = (filePath) => {
  const ext = (filePath || '').split('.').pop()?.toLowerCase();
  switch (ext) {
    case 'json':
      return 'json';
    case 'yaml':
    case 'yml':
      return 'yaml';
    case 'js':
    case 'jsx':
    case 'ts':
    case 'tsx':
      return 'javascript';
    case 'sh':
    case 'bash':
      return 'shell';
    case 'md':
      return 'markdown';
    case 'sql':
      return 'sql';
    default:
      return 'text';
  }
};

// Persisted parameters can be an unparseable JSON fragment (older backend rows
// were truncated mid-string). Decode the common JSON escapes so the fallback
// reads as text instead of an escaped string literal.
export const lenientUnescape = (text) => {
  if (!text || typeof text !== 'string' || !text.includes('\\')) {
    return text;
  }
  return text
    .replace(/\\u003c/gi, '<')
    .replace(/\\u003e/gi, '>')
    .replace(/\\u0026/gi, '&')
    .replace(/\\n/g, '\n')
    .replace(/\\t/g, '\t')
    .replace(/\\"/g, '"');
};

// file_view responses come back as "NNN: content" lines with the file's real
// line numbers baked into the text. Parse them so the excerpt renders as a
// proper code block with a real-numbered gutter instead of raw prefixed text.
const FILE_VIEW_TOOL_NAMES = ['file_view', 'file_read', 'read_file'];

export const parseFileViewData = (tc) => {
  if (!tc || !FILE_VIEW_TOOL_NAMES.includes(tc.tool_name) || typeof tc.response !== 'string' || !tc.response.trim()) {
    return null;
  }
  const lines = tc.response.replace(/\r\n/g, '\n').split('\n');
  const matches = lines.map((l) => l.match(/^(\d+):(?: (.*))?$/));
  const matchedCount = matches.filter(Boolean).length;
  const nonEmptyCount = lines.filter((l) => l.trim()).length;
  if (matchedCount === 0 || matchedCount < nonEmptyCount * 0.8) {
    return null;
  }
  const first = matches.find(Boolean);
  const code = lines.map((l, i) => (matches[i] ? matches[i][2] || '' : l)).join('\n');
  let filePath = '';
  try {
    const params = JSON.parse(tc.parameters);
    filePath = typeof params?.file_path === 'string' ? params.file_path : '';
  } catch (e) {
    console.warn('parseFileViewData: parameters JSON parse failed', e);
  }
  return { filePath, startLine: parseInt(first[1], 10), code };
};

// Old backend rows were truncated mid-JSON (always inside the giant
// "_tool_outputs" working-memory blob) and can't be parsed. Salvage a display
// string: drop the working-memory keys textually and decode escapes. Returns
// null when nothing but working memory remains.
export const salvageTruncatedParams = (text) => {
  if (!text || typeof text !== 'string') {
    return text;
  }
  let out = text;
  let stripped = false;
  const toIdx = out.indexOf('"_tool_outputs"');
  if (toIdx !== -1) {
    // Truncation happens inside this blob, so nothing useful follows it.
    out = out.slice(0, toIdx);
    stripped = true;
  }
  const intentionRe = /"__intention"\s*:\s*"(?:[^"\\]|\\.)*"\s*,?/;
  if (intentionRe.test(out)) {
    // Already rendered as the Thought section — pure duplication here.
    out = out.replace(intentionRe, '');
    stripped = true;
  }
  if (stripped) {
    out = out.replace(/^\s*\{\s*/, '').replace(/[\s,{]+$/, '');
    if (!out) {
      return null;
    }
  }
  return lenientUnescape(out);
};

const isPreformattedText = (text) => {
  if (!text) {
    return false;
  }
  const lines = text.split('\n').filter((l) => l.trim().length > 0);
  if (lines.length < 2) {
    return false;
  }
  const alignedLines = lines.filter((l) => /\S {2,}\S/.test(l));
  return alignedLines.length >= lines.length * 0.5;
};

const parsePrometheusMetrics = (responseText) => {
  try {
    const metrics = JSON.parse(responseText);
    if (Array.isArray(metrics) && metrics.length > 0 && metrics[0].timestamps && metrics[0].values) {
      return { PrometheusQuery: metrics };
    }
    if (metrics && typeof metrics === 'object' && !Array.isArray(metrics)) {
      const metricsObj = {};
      for (const key in metrics) {
        let value = metrics[key];
        if (typeof value === 'string') {
          value = JSON.parse(value);
        }
        if (Array.isArray(value) && value.length > 0 && value[0].timestamps && value[0].values) {
          metricsObj[key] = value;
        }
      }
      if (Object.keys(metricsObj).length > 0) {
        return metricsObj;
      }
    }
  } catch (e) {
    console.warn('parsePrometheusMetrics: not valid prometheus data', e);
  }
  return null;
};

const renderPrometheusCharts = (metricsQueryObject) => (
  <Grid container sx={{ fontSize: 'var(--ds-text-body)', color: 'var(--ds-blue-700)' }}>
    {Object.keys(metricsQueryObject).map((key) => (
      <React.Fragment key={key}>
        <Grid item xs={12} mb={ds.space[2]}>
          <Typography
            sx={{
              fontWeight: 'var(--ds-font-weight-medium)',
              fontSize: 'var(--ds-text-body)',
              wordBreak: 'break-word',
              color: 'var(--ds-gray-700)',
            }}
          >
            {key}
          </Typography>
        </Grid>
        {metricsQueryObject[key]?.map((e, i) => (
          <React.Fragment key={`${key}-${i}`}>
            {e.metric && Object.keys(e.metric).length > 0 && (
              <Grid item xs={12} mb={ds.space[2]}>
                <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-500)', wordBreak: 'break-word' }}>
                  {JSON.stringify(e.metric)}
                </Typography>
              </Grid>
            )}
            {e.stats && (
              <Grid container spacing={1} mb={ds.space[2]}>
                <Grid item xs={3}>
                  <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>
                    Min: {Number(e.stats.min).toFixed(4)}
                  </Typography>
                </Grid>
                <Grid item xs={3}>
                  <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>
                    Max: {Number(e.stats.max).toFixed(4)}
                  </Typography>
                </Grid>
                <Grid item xs={3}>
                  <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>
                    Avg: {Number(e.stats.avg).toFixed(4)}
                  </Typography>
                </Grid>
                <Grid item xs={3}>
                  <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>
                    P99: {Number(e.stats.p99).toFixed(4)}
                  </Typography>
                </Grid>
              </Grid>
            )}
            {e.values?.length > 1 ? (
              <Grid item xs={12} mb={ds.space[4]}>
                <Chart.Line data={e.values} labels={e.timestamps} />
              </Grid>
            ) : e.values?.[0] != null ? (
              <Grid item xs={12} mb={ds.space[2]}>
                <Typography sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-medium)' }}>Value: {e.values[0]}</Typography>
              </Grid>
            ) : null}
            {i < metricsQueryObject[key].length - 1 && (
              <Grid item xs={12}>
                <Divider sx={{ my: ds.space[2] }} />
              </Grid>
            )}
          </React.Fragment>
        ))}
      </React.Fragment>
    ))}
  </Grid>
);

const renderResponseText = (responseText, toolCall) => {
  if (!responseText) {
    return null;
  }

  // Check for Prometheus/metrics chart data by structure (not just tool name)
  const metricsData = parsePrometheusMetrics(responseText);
  if (metricsData) {
    return renderPrometheusCharts(metricsData);
  }

  try {
    let parsed = JSON.parse(responseText);
    // Unwrap double-encoded JSON (a JSON string whose contents are themselves JSON).
    if (typeof parsed === 'string') {
      const inner = parsed.trim();
      if (inner.startsWith('{') || inner.startsWith('[')) {
        try {
          parsed = JSON.parse(inner);
        } catch (e) {
          console.warn('renderResponseText: double-encoded JSON parse failed', e);
        }
      }
    }
    if (parsed && typeof parsed === 'object') {
      const textContent = parsed.response || parsed.stdout || parsed.stderr;
      if (textContent && typeof textContent === 'string') {
        const output = textContent.replace(/\\n/g, '\n');
        // Render as markdown if content contains markdown indicators
        if (/^#{1,6}\s|(\*\*|__).+(\*\*|__)|^[*-]\s|^\d+\.\s|```/m.test(output)) {
          return (
            <MarkDowns
              data={prettifyJsonFencesInMarkdown(output).replace(/~/g, '\\~')}
              sx={{ width: '100%', overflowX: 'auto', p: 0, fontSize: 'var(--ds-text-small)' }}
            />
          );
        }
        return (
          <Box sx={{ backgroundColor: 'var(--ds-brand-500)', borderRadius: ds.radius.lg, p: ds.space[3], overflowX: 'auto' }}>
            <pre style={preStyle}>{output}</pre>
          </Box>
        );
      }
      // Arrays of objects fall through to LLMAnswerRenderer below so it can
      // render them as a structured table; only prettify plain objects here.
      if (!Array.isArray(parsed)) {
        return (
          <Box sx={{ backgroundColor: 'var(--ds-brand-500)', borderRadius: ds.radius.lg, p: ds.space[3], overflowX: 'auto' }}>
            <pre style={preStyle}>{JSON.stringify(parsed, null, 2)}</pre>
          </Box>
        );
      }
    }
  } catch (e) {
    console.warn('renderResponseText: not JSON', e);
  }

  // Check for markdown in raw (non-JSON) text before falling back to preformatted
  const rawText = responseText.replace(/\\n/g, '\n');
  if (/^#{1,6}\s|(\*\*|__).+(\*\*|__)|^[*-]\s|^\d+\.\s|```/m.test(rawText)) {
    return (
      <MarkDowns
        data={prettifyJsonFencesInMarkdown(rawText).replace(/~/g, '\\~')}
        sx={{ width: '100%', overflowX: 'auto', p: 0, fontSize: 'var(--ds-text-small)' }}
      />
    );
  }

  if (isPreformattedText(responseText)) {
    return (
      <Box sx={{ backgroundColor: 'var(--ds-brand-500)', borderRadius: ds.radius.lg, p: ds.space[3], overflowX: 'auto' }}>
        <pre style={preStyle}>{rawText}</pre>
      </Box>
    );
  }

  return <LLMAnswerRenderer toolCall={{ ...(toolCall || {}), text: responseText }} messages={[]} />;
};

const DB_TOOL_NAMES = [
  'PostgresQueryExecutor',
  'postgres-debug',
  'postgres_debug',
  'postgres',
  'postgres_execute',
  'postgres_query_execute',
  'queryPostgres',
  'mysql-debug',
  'mysql_debug',
  'mysql',
  'mysql_execute',
  'mysql_query_execute',
  'queryMysql',
  'MysqlQueryExecutor',
  'queryEvents',
  'executeEventsSql',
  'events_execute',
  'events',
  'Events',
];

const TRACE_TOOL_NAMES = [
  'queryTraces',
  'traces',
  'traces_execute',
  'traces_execute_v2',
  'getResourceTraces',
  'recommendations',
  'executeRecommendationSql',
  'recommendation_execute',
  'security',
  'security_execute',
];

const isDbTool = (name) => DB_TOOL_NAMES.includes(name) || (name && name.startsWith('clickhouse'));
const isTraceTool = (name) => TRACE_TOOL_NAMES.includes(name);
const isLokiTool = (name) => ['queryLoki', 'loki', 'loki_execute'].includes(name);
const isEsTool = (name) => ['queryES', 'es', 'elastic_search_execute'].includes(name);
const isKubectlTool = (name) => ['KubectlExecutor', 'k8s', 'kubectl', 'kubectl_execute'].includes(name);
const isDocsTool = (name) => ['search_docs', 'docs', 'docs_agent'].includes(name);
const isSecurityIssuesTool = (name) => name === 'GetSecurityIssues';
const isLogsTool = (name) => name && name.toLowerCase().includes('logs');
const isPlannerTool = (name) => name === 'planner' || name === 'TroubleshootPlanner';
const isCloudTool = (name) => ['aws', 'aws_execute', 'gcloud', 'gcloud_execute', 'azure', 'azure_execute'].includes(name);
const isRabbitOrRedisTool = (name) =>
  [
    'rabbit_execute',
    'rabbitmq',
    'rabbitmq_execute',
    'redis_execute',
    'redis',
    'redis_executor',
    'redis_command_executor',
    'redis_command_executer',
  ].includes(name);

/**
 * Stateful component for rendering tool responses with proper formatting and pagination.
 */
const FormattedToolResponse = ({ responseText, toolName, toolCall, accountId }) => {
  const [currentPage, setCurrentPage] = useState(0);
  const [recordsPerPage, setRecordsPerPage] = useState(5);

  const onPageChange = (page, limit) => {
    setCurrentPage(page - 1);
    setRecordsPerPage(limit);
  };

  if (!responseText) {
    return null;
  }

  const getTableData = (arrayData, checkAll) => {
    if (!arrayData || arrayData.length === 0) {
      return null;
    }
    const headers = Object.keys(arrayData[0]);
    if (checkAll) {
      for (let i = 1; i < arrayData.length; i++) {
        const objectKeys = Object.keys(arrayData[i]);
        for (let j = 0; j < objectKeys.length; j++) {
          if (!headers.includes(objectKeys[j])) {
            headers.push(objectKeys[j]);
          }
        }
      }
    }
    const tableData = arrayData.map((item) => {
      const components = headers.map((header) => {
        let val = item[header];
        if (typeof val === 'object' || Array.isArray(val)) {
          val = JSON.stringify(val);
        }
        return { component: <Text value={val} showAutoEllipsis sx={{ minWidth: ds.space.mul(0, 25) }} /> };
      });
      if (item.tool === 'plan_update') {
        components.sx = { backgroundColor: 'var(--ds-background-200)' };
      }
      return components;
    });
    return { headers: headers.map((f) => convertToReadableFormat(f.replaceAll('_', ' '))), tableData };
  };

  // Planner tool
  if (isPlannerTool(toolName)) {
    try {
      let data = JSON.parse(responseText);
      if (Array.isArray(data)) {
        data.sort((a, b) => {
          const iterA = a.iteration || 0;
          const iterB = b.iteration || 0;
          if (iterA !== iterB) {
            return iterA - iterB;
          }
          if (a.tool === 'plan_update' && b.tool !== 'plan_update') {
            return -1;
          }
          if (a.tool !== 'plan_update' && b.tool === 'plan_update') {
            return 1;
          }
          return 0;
        });
      }
      const objectInfo = getTableData(data, true);
      if (objectInfo) {
        objectInfo.headers = objectInfo.headers.map((f) => {
          const fl = typeof f === 'string' ? f.toLowerCase() : f;
          if (fl === 'id') {
            return { width: '10%', name: 'ID' };
          }
          if (fl === 'tool') {
            return { width: '10%', name: 'Tool' };
          }
          if (fl === 'plan') {
            return { width: '40%', name: 'Plan' };
          }
          if (fl === 'query') {
            return { width: '40%', name: 'Query' };
          }
          return f;
        });
        return (
          <CustomTable
            tableData={objectInfo.tableData}
            headers={objectInfo.headers}
            totalRows={objectInfo.tableData.length}
            rowsPerPage={objectInfo.tableData.length}
          />
        );
      }
    } catch (e) {
      console.warn('FormattedToolResponse: object-info render failed', e);
    }
  }

  // DB tools (postgres, mysql, clickhouse, events)
  if (isDbTool(toolName)) {
    try {
      const events = JSON.parse(responseText);
      if (Array.isArray(events) && events.length > 0) {
        const tableInfo = getTableData(events);
        if (tableInfo) {
          const isSingleRow = tableInfo.tableData.length <= 1;
          const startIndex = currentPage * recordsPerPage;
          const endIndex = startIndex + recordsPerPage;
          const paginatedTableData = isSingleRow ? tableInfo.tableData : tableInfo.tableData.slice(startIndex, endIndex);
          return (
            <Box sx={{ overflowX: 'auto' }}>
              <CustomTable
                tableData={paginatedTableData}
                headers={tableInfo.headers}
                totalRows={tableInfo.tableData.length}
                rowsPerPage={isSingleRow ? tableInfo.tableData.length : recordsPerPage}
                onPageChange={isSingleRow ? undefined : onPageChange}
                pageNumber={isSingleRow ? undefined : currentPage + 1}
                renderVertical={isSingleRow}
              />
            </Box>
          );
        }
      }
    } catch (e) {
      console.warn('FormattedToolResponse: DB tool render failed', e);
    }
  }

  // Traces / Recommendations
  if (isTraceTool(toolName)) {
    try {
      const traces = JSON.parse(responseText);
      if (Array.isArray(traces) && traces.length > 0) {
        const headers = Object.keys(traces[0]);
        const tableData = traces.map((t) =>
          headers.map((h) => {
            const raw = t[h];
            const val = typeof raw === 'object' && raw !== null ? JSON.stringify(raw) : raw != null ? String(raw) : raw;
            return { component: <Text value={val} showAutoEllipsis sx={{ minWidth: ds.space.mul(1, 15) }} /> };
          })
        );
        const isSingleRow = tableData.length <= 1;
        const startIndex = currentPage * recordsPerPage;
        const endIndex = startIndex + recordsPerPage;
        const paginatedData = isSingleRow ? tableData : tableData.slice(startIndex, endIndex);
        return (
          <Box sx={{ overflowX: 'auto' }}>
            <CustomTable
              tableData={paginatedData}
              headers={headers.map((h) => h.replaceAll('_', ' '))}
              rowsPerPage={isSingleRow ? tableData.length : recordsPerPage}
              onPageChange={isSingleRow ? undefined : onPageChange}
              pageNumber={isSingleRow ? undefined : currentPage + 1}
              totalRows={tableData.length}
              renderVertical={isSingleRow}
            />
          </Box>
        );
      }
    } catch (e) {
      console.warn('FormattedToolResponse: trace tool render failed', e);
    }
  }

  // Security Issues (dedicated component)
  if (isSecurityIssuesTool(toolName)) {
    try {
      const traces = JSON.parse(responseText);
      return <KubernetesSecurityDetails llmTableData={traces} disableInfographic={true} kubernetes={{ id: accountId }} />;
    } catch (e) {
      console.warn('FormattedToolResponse: security issues render failed', e);
    }
  }

  // Loki logs
  if (isLokiTool(toolName)) {
    try {
      const results = JSON.parse(responseText);
      if (results?.result?.length > 0) {
        const logsData = results.result[0].values;
        const headers = [
          { name: 'Date', width: '25%' },
          { name: 'Message', width: '75%' },
        ];
        const tableData = logsData.map((m) => {
          const dateTimestamp = parseInt(m[0]) / 1000000;
          return [{ component: <LogDate timestamp={dateTimestamp} log={m?.[1]} /> }, { component: <Text value={m[1]} showAutoEllipsis /> }];
        });
        return <CustomTable tableData={tableData} headers={headers} renderVertical={tableData.length <= 1} />;
      }
    } catch (e) {
      console.warn('FormattedToolResponse: Loki tool render failed', e);
    }
  }

  // ElasticSearch logs
  if (isEsTool(toolName)) {
    try {
      const results = JSON.parse(responseText);
      if (results?.result?.length > 0) {
        const tableData = mapToTableData(results.result);
        return (
          <KubernetesTable
            id={'tool-details-es-logs'}
            totalRows={tableData.length}
            data={tableData}
            headers={[{ name: 'Date', width: '20%' }, { name: 'Message', width: '80%' }, '']}
            rowsPerPage={tableData.length}
            showExpandable={true}
            expandable={{ tabs: [{ text: 'Log Details', value: 0, key: 'logstash-log' }] }}
          />
        );
      }
    } catch (e) {
      console.warn('FormattedToolResponse: ES tool render failed', e);
    }
  }

  // Kubectl
  if (isKubectlTool(toolName)) {
    try {
      const results = JSON.parse(responseText);
      const hasStdout = typeof results.stdout === 'string';
      const hasStderr = typeof results.stderr === 'string';
      if (hasStdout || hasStderr) {
        const out = (results.stdout || results.stderr || '').replace(/\\n/g, '\n');
        return (
          <Box sx={{ backgroundColor: 'var(--ds-brand-500)', borderRadius: ds.radius.lg, p: ds.space[3], overflowX: 'auto' }}>
            {out.trim() ? <pre style={preStyle}>{out}</pre> : <pre style={{ ...preStyle, fontStyle: 'italic', opacity: 0.7 }}>(no output)</pre>}
          </Box>
        );
      }
    } catch (e) {
      console.warn('FormattedToolResponse: kubectl tool render failed', e);
    }
  }

  // Docs
  if (isDocsTool(toolName)) {
    try {
      const results = JSON.parse(responseText);
      if (Array.isArray(results) && results.length > 0) {
        return (
          <Box>
            {results.map((r, i) => (
              <React.Fragment key={i}>
                <MarkDowns data={r.PageContent} sx={{ width: '100%', p: 0, fontSize: 'var(--ds-text-small)' }} />
                {i < results.length - 1 && <Divider style='dashed' thickness={0.75} color='var(--ds-gray-300)' sx={{ my: ds.space[2], mx: 0 }} />}
              </React.Fragment>
            ))}
          </Box>
        );
      }
    } catch (e) {
      console.warn('FormattedToolResponse: docs tool render failed', e);
    }
  }

  // Cloud tools (AWS, GCloud, Azure)
  if (isCloudTool(toolName)) {
    return (
      <Box sx={{ backgroundColor: 'var(--ds-brand-500)', borderRadius: ds.radius.lg, p: ds.space[3], overflowX: 'auto' }}>
        <pre style={preStyle}>{responseText}</pre>
      </Box>
    );
  }

  // RabbitMQ / Redis
  if (isRabbitOrRedisTool(toolName)) {
    try {
      const data = JSON.parse(responseText);
      const output = data?.stdout || data?.stderr;
      if (output) {
        return (
          <Box sx={{ backgroundColor: 'var(--ds-brand-500)', borderRadius: ds.radius.lg, p: ds.space[3], overflowX: 'auto' }}>
            <pre style={preStyle}>{output}</pre>
          </Box>
        );
      }
    } catch (e) {
      console.warn('FormattedToolResponse: RabbitMQ/Redis tool render failed', e);
      return (
        <Box sx={{ backgroundColor: 'var(--ds-brand-500)', borderRadius: ds.radius.lg, p: ds.space[3], overflowX: 'auto' }}>
          <pre style={preStyle}>{responseText.replace(/\\n/g, '\n')}</pre>
        </Box>
      );
    }
  }

  // Generic logs tool
  if (isLogsTool(toolName)) {
    try {
      const results = JSON.parse(responseText);
      // logs is an array for the logs_execute_v2/logs tool result, but a string
      // preview for the fetch_logs agent envelope ({query, provider, logs, file_ref}).
      // Read query/provider from metadata (tool result) or the envelope's top level.
      const logsArray = Array.isArray(results?.logs) ? results.logs : null;
      const query = results?.metadata?.query || results?.query;
      const provider = results?.metadata?.provider || results?.provider;
      if ((logsArray && logsArray.length > 0) || query) {
        const headers = [
          { name: 'Date', width: '25%' },
          { name: 'Message', width: '75%' },
        ];
        const tableData = (logsArray || []).map((m) => {
          const dateTimestamp = Date.parse(m.timestamp);
          return [{ component: <LogDate timestamp={dateTimestamp} log={m?.message} /> }, { component: <Text value={m?.message} showAutoEllipsis /> }];
        });
        return (
          <Box>
            {(provider || query) && (
              <Box sx={{ mb: ds.space[2], fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-700)' }}>
                {provider && (
                  <Typography sx={{ fontSize: 'var(--ds-text-small)' }}>
                    <b>Provider:</b> {provider}
                  </Typography>
                )}
                {query && (
                  <Typography sx={{ fontSize: 'var(--ds-text-small)' }}>
                    <b>Query:</b> {query}
                  </Typography>
                )}
              </Box>
            )}
            {logsArray && logsArray.length > 0 ? (
              <CustomTable tableData={tableData} headers={headers} renderVertical={tableData.length <= 1} />
            ) : typeof results?.logs === 'string' && results.logs ? (
              <Box sx={{ backgroundColor: 'var(--ds-brand-500)', borderRadius: ds.radius.lg, p: ds.space[3], overflowX: 'auto' }}>
                <pre style={preStyle}>{results.logs.replace(/\\n/g, '\n')}</pre>
              </Box>
            ) : null}
          </Box>
        );
      }
    } catch (e) {
      console.warn('FormattedToolResponse: logs tool render failed', e);
    }
  }

  // Default: use the existing generic renderResponseText logic
  return renderResponseText(responseText, toolCall);
};

FormattedToolResponse.propTypes = {
  responseText: PropTypes.string,
  toolName: PropTypes.string,
  toolCall: PropTypes.object,
  accountId: PropTypes.string,
};

const sectionLabelSx = {
  fontSize: 'var(--ds-text-caption)',
  fontWeight: 'var(--ds-font-weight-medium)',
  color: 'var(--ds-gray-500)',
  mb: ds.space[1],
  textTransform: 'uppercase',
};

const contentBoxSx = {
  backgroundColor: 'var(--ds-background-200)',
  borderRadius: ds.radius.lg,
  border: '1px solid var(--ds-brand-150)',
  p: ds.space[3],
  overflowX: 'auto',
  width: '100%',
  boxSizing: 'border-box',
  '& td, & th': {
    wordBreak: 'break-word',
    overflowWrap: 'break-word',
    whiteSpace: 'normal',
  },
};

/**
 * Section label row with an optional copy-to-clipboard icon aligned to the right.
 * Used for the Response section so the raw tool output can be copied even when it's
 * rendered as a table/chart/markdown rather than plain text.
 */
const SectionLabel = ({ label, copyText, toastMessage = 'Copied to clipboard' }) => (
  <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', mb: ds.space[1] }}>
    <Typography sx={{ ...sectionLabelSx, mb: 0 }}>{label}</Typography>
    {copyText ? <CopyButton text={copyText} size='xs' toastMessage={toastMessage} /> : null}
  </Box>
);

SectionLabel.propTypes = {
  label: PropTypes.string.isRequired,
  copyText: PropTypes.string,
  toastMessage: PropTypes.string,
};

/**
 * Renders a tool call's parameters as a "Query" box. Accepts either the raw JSON
 * string (per-tool-call task objects) or an already-parsed object so both the
 * grouped view and the single-call fallback can share it.
 */
const ParametersBox = ({ parameters }) => {
  const isEmpty =
    !parameters || parameters === '{}' || (typeof parameters === 'object' && !Array.isArray(parameters) && Object.keys(parameters).length === 0);
  if (isEmpty) {
    return null;
  }
  return (
    <Box sx={{ mb: ds.space[2] }}>
      <Typography sx={sectionLabelSx}>Query</Typography>
      <Box sx={{ ...contentBoxSx, fontFamily: '"Roboto Mono", monospace', fontSize: 'var(--ds-text-small)' }}>
        {(() => {
          try {
            let parsed = null;
            if (Array.isArray(parameters)) {
              // An array param has no command/query shape; show it verbatim rather
              // than spreading it into an object with numeric keys.
              return <pre style={{ margin: 0, whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}>{JSON.stringify(parameters, null, 2)}</pre>;
            }
            if (typeof parameters === 'object') {
              // Shallow-copy so stripping working-memory keys below doesn't mutate the prop.
              parsed = { ...parameters };
            } else if (typeof parameters === 'string' && parameters.trim().startsWith('{')) {
              parsed = JSON.parse(parameters);
            }
            if (parsed && typeof parsed === 'object') {
              // Planner working memory persisted by older backends — internal
              // plumbing, never useful to show.
              delete parsed._tool_outputs;
              delete parsed.__intention;
              // Executor-style tools (shell, kubectl, ...) put the executable in
              // `command` and the actual command line in `args`. Showing only
              // `command` ("shell") hides what actually ran, so prefer `args`.
              let single = parsed.sql || parsed.query;
              if (typeof single !== 'string' && typeof parsed.args === 'string' && parsed.args.trim()) {
                const exe = typeof parsed.command === 'string' && parsed.command && parsed.command !== 'shell' ? parsed.command : '';
                single = exe ? `${exe} ${parsed.args}` : parsed.args;
              }
              if (typeof single !== 'string') {
                single = parsed.command;
              }
              if (typeof single === 'string') {
                return <pre style={{ margin: 0, whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}>{single}</pre>;
              }
              return <pre style={{ margin: 0, whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}>{JSON.stringify(parsed, null, 2)}</pre>;
            }
          } catch (e) {
            console.warn('ParametersBox: parameters JSON parse failed', e);
          }
          // Unparseable (e.g. truncated JSON from older rows): strip the
          // working-memory blob and decode escapes so it reads as text.
          const salvaged = salvageTruncatedParams(parameters);
          if (salvaged === null || salvaged === undefined) {
            return (
              <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-500)', fontStyle: 'italic', fontFamily: ds.font.sans }}>
                Internal working memory omitted (record truncated server-side).
              </Typography>
            );
          }
          return <pre style={{ margin: 0, whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}>{salvaged}</pre>;
        })()}
      </Box>
    </Box>
  );
};

ParametersBox.propTypes = {
  parameters: PropTypes.oneOfType([PropTypes.string, PropTypes.object, PropTypes.array]),
};

/**
 * Renders a single tool call's thought and response.
 */
const ToolCallSection = ({ tc, index, accountId, reasoning }) => {
  const thought = (tc.thought || '').split('\n\nAction:')[0];
  const responseText = tc.response;
  const tcName = tc.tool_name || `Tool Call ${index + 1}`;
  const tcIcon = getIcon(tcName) || WrenchIcon;
  const tcStatus = tc.status;

  const prettyThought = tryPrettifyJson(thought);
  const codeEdit = parseCodeEditParams(tc);
  const fileView = parseFileViewData(tc);

  return (
    <Box sx={{ mb: ds.space[4] }}>
      {/* Tool call sub-header */}
      <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2], mb: ds.space.mul(0, 5) }}>
        <SafeIcon src={tcIcon} alt={tcName} width={18} height={18} />
        <Typography
          sx={{
            fontSize: 'var(--ds-text-body)',
            fontWeight: 'var(--ds-font-weight-medium)',
            fontFamily: ds.font.sans,
            color: 'var(--ds-gray-700)',
            flex: 1,
          }}
        >
          {index + 1}. {cleanToolName(tcName)}
        </Typography>
        <StatusBadge status={tcStatus} />
        <ReasoningBadge reasoning={reasoning} />
        <Duration createdAt={tc.created_at} updatedAt={tc.updated_at} metadata={tc.metadata} />
      </Box>

      {/* Thought */}
      {thought && (
        <Box sx={{ mb: ds.space[2] }}>
          <Typography sx={sectionLabelSx}>Thought</Typography>
          <Box sx={contentBoxSx}>
            {prettyThought ? (
              <pre style={{ ...preStyle, color: 'var(--ds-gray-700)' }}>{prettyThought}</pre>
            ) : (
              <MarkDowns data={prettifyJsonFencesInMarkdown(thought)} sx={{ p: 0, fontSize: 'var(--ds-text-small)' }} />
            )}
          </Box>
        </Box>
      )}

      {/* Code-edit tools: render the proposed change as a diff instead of raw JSON */}
      {codeEdit ? (
        <Box sx={{ mb: ds.space[2] }}>
          <Typography sx={sectionLabelSx}>Proposed Change</Typography>
          {codeEdit.purpose && (
            <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-700)', fontFamily: ds.font.sans, mb: ds.space[1] }}>
              {codeEdit.purpose}
            </Typography>
          )}
          <DiffViewer
            originalCode={codeEdit.old_string}
            newCode={codeEdit.new_string}
            mode='unified'
            fileName={codeEdit.file_path}
            language={diffLanguageForFile(codeEdit.file_path)}
            data-testid={`tool-details-code-edit-diff-${index}`}
          />
        </Box>
      ) : fileView ? null : (
        /* Parameters / Query — file_view's params (path + line range) are fully
           conveyed by the code block header below, so its raw JSON is skipped */
        <ParametersBox parameters={tc.parameters} />
      )}

      {/* Response */}
      {fileView ? (
        <Box>
          <SectionLabel label='Response' />
          <CodeBlock
            code={fileView.code}
            title={
              fileView.filePath
                ? `${fileView.filePath} · lines ${fileView.startLine}–${fileView.startLine + fileView.code.split('\n').length - 1}`
                : undefined
            }
            language={fileView.filePath ? fileView.filePath.split('.').pop() : undefined}
            showLineNumbers
            lineNumberStart={fileView.startLine}
            maxHeight={420}
            copyToast='File excerpt copied to clipboard'
            data-testid={`tool-details-file-view-${index}`}
          />
        </Box>
      ) : (
        responseText && (
          <Box>
            <SectionLabel label='Response' copyText={responseText} toastMessage='Response copied to clipboard' />
            <Box sx={contentBoxSx}>
              <FormattedToolResponse
                responseText={responseText}
                toolName={tcName}
                toolCall={{ agentName: tc.tool_name, tool: tc.tool_name, tool_name: tc.tool_name }}
                accountId={accountId}
              />
            </Box>
          </Box>
        )
      )}
    </Box>
  );
};

ToolCallSection.propTypes = {
  tc: PropTypes.object.isRequired,
  index: PropTypes.number.isRequired,
  accountId: PropTypes.string,
  reasoning: PropTypes.object,
};

// Formats a reasoning duration in seconds as "Xm Ys" / "Ys".
const formatReasoningDuration = (secs) => {
  const s = Math.round(secs || 0);
  if (s < 60) {
    return `${s}s`;
  }
  const m = Math.floor(s / 60);
  const r = s % 60;
  return r ? `${m}m ${r}s` : `${m}m`;
};

// Compact per-tool reasoning badge — sparkle + the thinking time that produced this tool
// call (red when ≥30s). The tooltip carries agent/call/token detail so the row stays terse.
const ReasoningBadge = ({ reasoning }) => {
  if (!reasoning || !(reasoning.reasoning_seconds > 0 || reasoning.calls > 0)) {
    return null;
  }
  const slow = reasoning.reasoning_seconds >= 30;
  const color = slow ? 'var(--ds-red-600, #dc2626)' : 'var(--ds-gray-500)';
  const tip = [
    reasoning.agent_name ? `Agent: ${reasoning.agent_name}` : null,
    reasoning.thinking_tokens > 0 ? `Tokens: ${reasoning.thinking_tokens.toLocaleString()}` : null,
    reasoning.calls > 0 ? `LLM Calls: ${reasoning.calls}` : null,
  ]
    .filter(Boolean)
    .join(' · ');
  return (
    <Tooltip title={`Reasoning — ${tip}`}>
      <Box sx={{ display: 'inline-flex', alignItems: 'center', gap: ds.space[1], flexShrink: 0 }}>
        <AutoAwesomeOutlinedIcon sx={{ fontSize: 13, color }} />
        <Typography
          sx={{
            fontSize: 'var(--ds-text-caption)',
            color,
            fontFamily: ds.font.sans,
            fontWeight: slow ? 'var(--ds-font-weight-medium)' : 'var(--ds-font-weight-regular)',
            lineHeight: 1,
            fontVariantNumeric: 'tabular-nums',
          }}
        >
          {formatReasoningDuration(reasoning.reasoning_seconds)}
        </Typography>
      </Box>
    </Tooltip>
  );
};

ReasoningBadge.propTypes = {
  reasoning: PropTypes.object,
};

const ToolDetails = ({ toolCall, accountId, conversationId, getReasoningForTool }) => {
  const [referencesAnchorEl, setReferencesAnchorEl] = useState(null);

  const parsedReferences = React.useMemo(() => {
    if (!toolCall) return [];
    if (!toolCall.references) return [];
    if (typeof toolCall.references === 'string') {
      try {
        return JSON.parse(toolCall.references);
      } catch (e) {
        console.warn('ToolDetails: references JSON parse failed', e);
        return [];
      }
    }
    return Array.isArray(toolCall.references) ? toolCall.references : [];
  }, [toolCall?.references]);

  if (!toolCall) {
    return null;
  }

  const getUniqueReferencesCount = (references) => {
    if (!references || references.length === 0) {
      return 0;
    }
    const seenUrls = new Set();
    references.forEach((ref) => {
      seenUrls.add(ref.url);
    });
    return seenUrls.size;
  };

  const toolName = toolCall.tool || toolCall.agentName || 'Tool Call';
  const icon = getIcon(toolName) || WrenchIcon;
  const status = toolCall.response_status || toolCall.status;
  const toolCalls = toolCall.toolCalls || [];
  const hasMultipleToolCalls = toolCalls.length > 1;
  const codeEdits = hasMultipleToolCalls ? toolCalls.map(parseCodeEditParams).filter(Boolean) : [];
  const editedFiles = [...new Set(codeEdits.map((e) => e.file_path).filter(Boolean))];

  // Per-tool reasoning lookup: match a tool-call-like object's candidate ids against the
  // time-split reasoning map so each tool shows the thinking that produced it.
  const reasoningFor = (obj) => {
    if (!obj || !getReasoningForTool) {
      return null;
    }
    for (const id of [obj.id, obj.tool_id]) {
      const r = id && getReasoningForTool(id);
      if (r) {
        return r;
      }
    }
    return null;
  };
  // Single-tool view shows its reasoning on the header; the grouped view badges each sub-call.
  const headerReasoning = hasMultipleToolCalls ? null : reasoningFor(toolCalls[0]) || reasoningFor(toolCall);

  // Fallback: single tool call view (agent-level thought + response).
  // Source the thought from `log`/`thought` only — never `text`, which for a
  // per-tool-call task holds the parameters (the "Query" box renders those).
  const agentThought = toolCall.log || toolCall.thought;
  const hasAgentResponse = toolCall.response?.text || toolCall.response_text;
  const prettyAgentThought = tryPrettifyJson(agentThought);

  return (
    <Box sx={{ overflow: 'hidden', width: '100%' }}>
      {/* Agent Header — single-tool view carries its own reasoning badge (the grouped view
          badges each sub-call below instead, so reasoning reads per tool, not per agent). */}
      <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space.mul(0, 5), mb: ds.space[4], flexWrap: 'wrap' }}>
        <SafeIcon src={icon} alt={toolName} width={24} height={24} />
        <Typography
          sx={{
            fontSize: 'var(--ds-text-body-lg)',
            fontWeight: 'var(--ds-font-weight-medium)',
            fontFamily: ds.font.sans,
            color: 'var(--ds-gray-700)',
            flex: 1,
          }}
        >
          {cleanToolName(toolName)}
        </Typography>
        {parsedReferences.length > 0 && (
          <Box
            role='button'
            tabIndex={0}
            onMouseEnter={(e) => setReferencesAnchorEl(e.currentTarget)}
            onClick={(e) => setReferencesAnchorEl(e.currentTarget)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' || e.key === ' ') {
                e.preventDefault();
                setReferencesAnchorEl(e.currentTarget);
              }
            }}
            sx={{
              display: 'flex',
              alignItems: 'center',
              gap: ds.space.mul(0, 3),
              cursor: 'pointer',
              padding: `${ds.space[1]} ${ds.space[2]}`,
              borderRadius: ds.radius.sm,
              transition: 'all 0.2s ease',
              '&:hover': {
                backgroundColor: 'var(--ds-gray-alpha-100)',
              },
            }}
          >
            <Typography
              sx={{
                fontSize: 'var(--ds-text-body)',
                fontWeight: 'var(--ds-font-weight-medium)',
                color: 'var(--ds-blue-600)',
                fontFamily: '"Poppins", sans-serif',
              }}
            >
              {getUniqueReferencesCount(parsedReferences)} source
              {getUniqueReferencesCount(parsedReferences) !== 1 ? 's' : ''}
            </Typography>
            {parsedReferences.some((r) => r.type === 'file') && (
              <FileDownloadIcon sx={{ fontSize: 'var(--ds-text-title)', color: 'var(--ds-blue-600)', ml: ds.space[0] }} />
            )}
          </Box>
        )}
        <StatusBadge status={status} />
        <ReasoningBadge reasoning={headerReasoning} />
        <Duration createdAt={toolCall.created_at} updatedAt={toolCall.updated_at} />
      </Box>

      {hasMultipleToolCalls && (
        <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-500)', mb: ds.space[3], fontFamily: ds.font.sans }}>
          {toolCalls.length} tool calls
        </Typography>
      )}

      {/* Rollup of proposed code edits so changes aren't buried in the call list */}
      {editedFiles.length > 0 && (
        <Box sx={{ mb: ds.space[3] }} data-testid='tool-details-proposed-changes'>
          <Typography sx={sectionLabelSx}>
            Proposed Changes — {codeEdits.length} edit{codeEdits.length !== 1 ? 's' : ''} in {editedFiles.length} file
            {editedFiles.length !== 1 ? 's' : ''}
          </Typography>
          <Box sx={contentBoxSx}>
            {editedFiles.map((f) => (
              <Typography
                key={f}
                sx={{ fontSize: 'var(--ds-text-small)', fontFamily: '"Roboto Mono", monospace', color: 'var(--ds-gray-700)', wordBreak: 'break-all' }}
              >
                {f}
              </Typography>
            ))}
          </Box>
        </Box>
      )}

      {/* Multiple tool calls: render each one */}
      {hasMultipleToolCalls ? (
        toolCalls.map((tc, idx) => (
          <React.Fragment key={tc.tool_id || idx}>
            <ToolCallSection tc={tc} index={idx} accountId={accountId} reasoning={reasoningFor(tc)} />
            {idx < toolCalls.length - 1 && <Divider sx={{ my: ds.space[3] }} />}
          </React.Fragment>
        ))
      ) : (
        <>
          {/* Single tool call fallback: agent-level thought + response */}
          {agentThought && (
            <Box sx={{ mb: ds.space[4] }}>
              <Typography sx={sectionLabelSx}>Thought</Typography>
              <Box sx={contentBoxSx}>
                {prettyAgentThought ? (
                  <pre style={{ ...preStyle, color: 'var(--ds-gray-700)' }}>{prettyAgentThought}</pre>
                ) : (
                  <MarkDowns data={prettifyJsonFencesInMarkdown(agentThought)} sx={{ p: 0, fontSize: 'var(--ds-text-small)' }} />
                )}
              </Box>
            </Box>
          )}
          {/* Parameters / Query — kept in the fallback so a single tool call's
              parameters are shown (they used to leak into the "Thought" box). */}
          <ParametersBox parameters={toolCall.toolParameters} />
          {hasAgentResponse && (
            <Box>
              <SectionLabel
                label='Response'
                copyText={toolCall.response?.text || toolCall.response_text}
                toastMessage='Response copied to clipboard'
              />
              <Box sx={contentBoxSx}>
                <FormattedToolResponse
                  responseText={toolCall.response?.text || toolCall.response_text}
                  toolName={toolName}
                  toolCall={toolCall}
                  accountId={accountId}
                />
              </Box>
            </Box>
          )}
        </>
      )}

      {parsedReferences.length > 0 && (
        <ReferencesPopover
          anchorEl={referencesAnchorEl}
          open={Boolean(referencesAnchorEl)}
          onClose={() => setReferencesAnchorEl(null)}
          references={parsedReferences}
          accountId={accountId}
          conversationId={conversationId}
        />
      )}
    </Box>
  );
};

ToolDetails.propTypes = {
  toolCall: PropTypes.object,
  accountId: PropTypes.string,
  conversationId: PropTypes.string,
  getReasoningForTool: PropTypes.func,
};

export default ToolDetails;
