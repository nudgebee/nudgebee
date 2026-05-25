import React, { useState } from 'react';
import PropTypes from 'prop-types';
import { Box, Typography, Divider, Grid } from '@mui/material';
import { colors } from 'src/utils/colors';
import { getIcon } from './AgentIcon';
import {
  WrenchIcon,
  AskNudgebeeErrorIcon,
  AskNudgebeeInProgressIcon,
  AskNudgebeeSkipIcon,
  AskNudgebeeSuccessIcon,
  AskNudgebeeWaitingIcon,
} from '@assets';
import SafeIcon from '@components1/common/SafeIcon';
import CustomTooltip from '@components1/common/CustomTooltip';
import Duration from './Duration';
import LLMAnswerRenderer from './LLMAnswerRenderer';
import MarkDowns from '@components1/common/MarkDowns';
import ReferencesPopover from './ReferencesModal';
import FileDownloadIcon from '@mui/icons-material/FileDownload';
import { LineChart, Text } from '@components1/common';
import CustomTable from '@components1/common/tables/CustomTable2';
import CustomDivider from '@components1/common/CustomDivider';
import ExpandableText from '@components1/common/ExpandableText';
import KubernetesTable2 from '@components1/k8s/common/KubernetesTable2';
import { mapToTableData } from '@components1/k8s/details/KubernetesLogStash';
import { LogDate } from '@components1/k8s/common/LogDate';
import KubernetesSecurityDetails from '@components1/recommendations/security/KubernetesSecurityDetails';
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
    <Box sx={{ display: 'flex', alignItems: 'center', gap: '4px' }}>
      <CustomTooltip title={label} placement='top'>
        <SafeIcon src={icon} alt='status icon' />
      </CustomTooltip>
      <Typography sx={{ fontSize: '11px', color: colors.text.tertiary, fontFamily: 'Roboto' }}>{label}</Typography>
    </Box>
  );
};

StatusBadge.propTypes = {
  status: PropTypes.string,
};

const preStyle = {
  margin: 0,
  color: '#E2E8F0',
  fontSize: '12px',
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
  <Grid container sx={{ fontSize: '13px', color: colors.darkPrimary }}>
    {Object.keys(metricsQueryObject).map((key) => (
      <React.Fragment key={key}>
        <Grid item xs={12} mb={1}>
          <Typography sx={{ fontWeight: 500, fontSize: '13px', wordBreak: 'break-word', color: colors.text.secondary }}>{key}</Typography>
        </Grid>
        {metricsQueryObject[key]?.map((e, i) => (
          <React.Fragment key={`${key}-${i}`}>
            {e.metric && Object.keys(e.metric).length > 0 && (
              <Grid item xs={12} mb={1}>
                <Typography sx={{ fontSize: '12px', color: colors.text.tertiary, wordBreak: 'break-word' }}>{JSON.stringify(e.metric)}</Typography>
              </Grid>
            )}
            {e.stats && (
              <Grid container spacing={1} mb={1}>
                <Grid item xs={3}>
                  <Typography sx={{ fontSize: '11px', color: colors.text.tertiary }}>Min: {Number(e.stats.min).toFixed(4)}</Typography>
                </Grid>
                <Grid item xs={3}>
                  <Typography sx={{ fontSize: '11px', color: colors.text.tertiary }}>Max: {Number(e.stats.max).toFixed(4)}</Typography>
                </Grid>
                <Grid item xs={3}>
                  <Typography sx={{ fontSize: '11px', color: colors.text.tertiary }}>Avg: {Number(e.stats.avg).toFixed(4)}</Typography>
                </Grid>
                <Grid item xs={3}>
                  <Typography sx={{ fontSize: '11px', color: colors.text.tertiary }}>P99: {Number(e.stats.p99).toFixed(4)}</Typography>
                </Grid>
              </Grid>
            )}
            {e.values?.length > 1 ? (
              <Grid item xs={12} mb={2}>
                <LineChart data={e.values} labels={e.timestamps} />
              </Grid>
            ) : e.values?.[0] != null ? (
              <Grid item xs={12} mb={1}>
                <Typography sx={{ fontSize: '12px', fontWeight: 500 }}>Value: {e.values[0]}</Typography>
              </Grid>
            ) : null}
            {i < metricsQueryObject[key].length - 1 && (
              <Grid item xs={12}>
                <Divider sx={{ my: '8px' }} />
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
              sx={{ width: '100%', overflowX: 'auto', p: 0, fontSize: '12px' }}
            />
          );
        }
        return (
          <Box sx={{ backgroundColor: '#2d3748', borderRadius: '8px', p: '12px', overflowX: 'auto' }}>
            <pre style={preStyle}>{output}</pre>
          </Box>
        );
      }
      // Arrays of objects fall through to LLMAnswerRenderer below so it can
      // render them as a structured table; only prettify plain objects here.
      if (!Array.isArray(parsed)) {
        return (
          <Box sx={{ backgroundColor: '#2d3748', borderRadius: '8px', p: '12px', overflowX: 'auto' }}>
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
        sx={{ width: '100%', overflowX: 'auto', p: 0, fontSize: '12px' }}
      />
    );
  }

  if (isPreformattedText(responseText)) {
    return (
      <Box sx={{ backgroundColor: '#2d3748', borderRadius: '8px', p: '12px', overflowX: 'auto' }}>
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
        return { component: <Text value={val} showAutoEllipsis sx={{ minWidth: '50px' }} /> };
      });
      if (item.tool === 'plan_update') {
        components.sx = { backgroundColor: colors.background.suggestionCardBG };
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
        const tableData = traces.map((t) => headers.map((h) => ({ component: <Text value={t[h]} showAutoEllipsis sx={{ minWidth: '60px' }} /> })));
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
          return [{ component: <LogDate timestamp={dateTimestamp} log={m?.[1]} /> }, { component: <ExpandableText text={m[1]} maxSize={150} /> }];
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
          <KubernetesTable2
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
          <Box sx={{ backgroundColor: '#2d3748', borderRadius: '8px', p: '12px', overflowX: 'auto' }}>
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
                <MarkDowns data={r.PageContent} sx={{ width: '100%', p: 0, fontSize: '12px' }} />
                {i < results.length - 1 && (
                  <CustomDivider borderType='dashed' borderWidth='0.75px' borderColor={colors.border.secondary} margin='8px 0px' />
                )}
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
      <Box sx={{ backgroundColor: '#2d3748', borderRadius: '8px', p: '12px', overflowX: 'auto' }}>
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
          <Box sx={{ backgroundColor: '#2d3748', borderRadius: '8px', p: '12px', overflowX: 'auto' }}>
            <pre style={preStyle}>{output}</pre>
          </Box>
        );
      }
    } catch (e) {
      console.warn('FormattedToolResponse: RabbitMQ/Redis tool render failed', e);
      return (
        <Box sx={{ backgroundColor: '#2d3748', borderRadius: '8px', p: '12px', overflowX: 'auto' }}>
          <pre style={preStyle}>{responseText.replace(/\\n/g, '\n')}</pre>
        </Box>
      );
    }
  }

  // Generic logs tool
  if (isLogsTool(toolName)) {
    try {
      const results = JSON.parse(responseText);
      if (results?.logs?.length > 0) {
        const headers = [
          { name: 'Date', width: '25%' },
          { name: 'Message', width: '75%' },
        ];
        const tableData = results.logs.map((m) => {
          const dateTimestamp = Date.parse(m.timestamp);
          return [{ component: <LogDate timestamp={dateTimestamp} log={m?.message} /> }, { component: <ExpandableText text={m?.message} /> }];
        });
        return (
          <Box>
            {results?.metadata && (
              <Box sx={{ mb: '8px', fontSize: '12px', color: colors.text.secondary }}>
                {results.metadata.provider && (
                  <Typography sx={{ fontSize: '12px' }}>
                    <b>Provider:</b> {results.metadata.provider}
                  </Typography>
                )}
                {results.metadata.query && (
                  <Typography sx={{ fontSize: '12px' }}>
                    <b>Query:</b> {results.metadata.query}
                  </Typography>
                )}
              </Box>
            )}
            <CustomTable tableData={tableData} headers={headers} renderVertical={tableData.length <= 1} />
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
  fontSize: '11px',
  fontWeight: 500,
  color: colors.text.tertiary,
  mb: '4px',
  textTransform: 'uppercase',
};

const contentBoxSx = {
  backgroundColor: '#F8FAFC',
  borderRadius: '8px',
  border: '1px solid #E2E8F0',
  p: '12px',
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
 * Renders a single tool call's thought and response.
 */
const ToolCallSection = ({ tc, index, accountId }) => {
  const thought = (tc.thought || '').split('\n\nAction:')[0];
  const responseText = tc.response;
  const tcName = tc.tool_name || `Tool Call ${index + 1}`;
  const tcIcon = getIcon(tcName) || WrenchIcon;
  const tcStatus = tc.status;

  const prettyThought = tryPrettifyJson(thought);

  return (
    <Box sx={{ mb: '16px' }}>
      {/* Tool call sub-header */}
      <Box sx={{ display: 'flex', alignItems: 'center', gap: '8px', mb: '10px' }}>
        <SafeIcon src={tcIcon} alt={tcName} width={18} height={18} />
        <Typography
          sx={{
            fontSize: '13px',
            fontWeight: 500,
            fontFamily: 'Roboto',
            color: colors.text.secondary,
            flex: 1,
          }}
        >
          {index + 1}. {cleanToolName(tcName)}
        </Typography>
        <StatusBadge status={tcStatus} />
        <Duration createdAt={tc.created_at} updatedAt={tc.updated_at} />
      </Box>

      {/* Thought */}
      {thought && (
        <Box sx={{ mb: '8px' }}>
          <Typography sx={sectionLabelSx}>Thought</Typography>
          <Box sx={contentBoxSx}>
            {prettyThought ? (
              <pre style={{ ...preStyle, color: colors.text.secondary }}>{prettyThought}</pre>
            ) : (
              <MarkDowns data={prettifyJsonFencesInMarkdown(thought)} sx={{ p: 0, fontSize: '12px' }} />
            )}
          </Box>
        </Box>
      )}

      {/* Parameters / Query */}
      {tc.parameters && tc.parameters !== '{}' && (
        <Box sx={{ mb: '8px' }}>
          <Typography sx={sectionLabelSx}>Query</Typography>
          <Box sx={{ ...contentBoxSx, fontFamily: '"Roboto Mono", monospace', fontSize: '12px' }}>
            {(() => {
              try {
                if (typeof tc.parameters === 'string' && tc.parameters.trim().startsWith('{')) {
                  const parsed = JSON.parse(tc.parameters);
                  if (parsed && typeof parsed === 'object') {
                    const single = parsed.command || parsed.query || parsed.sql;
                    if (typeof single === 'string') {
                      return <pre style={{ margin: 0, whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}>{single}</pre>;
                    }
                    return <pre style={{ margin: 0, whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}>{JSON.stringify(parsed, null, 2)}</pre>;
                  }
                }
              } catch (e) {
                console.warn('ToolCallSection: parameters JSON parse failed', e);
              }
              return <Box sx={{ wordBreak: 'break-all' }}>{tc.parameters}</Box>;
            })()}
          </Box>
        </Box>
      )}

      {/* Response */}
      {responseText && (
        <Box>
          <Typography sx={sectionLabelSx}>Response</Typography>
          <Box sx={contentBoxSx}>
            <FormattedToolResponse
              responseText={responseText}
              toolName={tcName}
              toolCall={{ agentName: tc.tool_name, tool: tc.tool_name, tool_name: tc.tool_name }}
              accountId={accountId}
            />
          </Box>
        </Box>
      )}
    </Box>
  );
};

ToolCallSection.propTypes = {
  tc: PropTypes.object.isRequired,
  index: PropTypes.number.isRequired,
  accountId: PropTypes.string,
};

const ToolDetails = ({ toolCall, accountId, conversationId }) => {
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

  // Fallback: single tool call view (agent-level thought + response)
  const agentThought = toolCall.log || toolCall.text;
  const hasAgentResponse = toolCall.response?.text || toolCall.response_text;
  const prettyAgentThought = tryPrettifyJson(agentThought);

  return (
    <Box sx={{ overflow: 'hidden', width: '100%' }}>
      {/* Agent Header */}
      <Box sx={{ display: 'flex', alignItems: 'center', gap: '10px', mb: '16px', flexWrap: 'wrap' }}>
        <SafeIcon src={icon} alt={toolName} width={24} height={24} />
        <Typography
          sx={{
            fontSize: '15px',
            fontWeight: 500,
            fontFamily: 'Roboto',
            color: colors.text.secondary,
            flex: 1,
          }}
        >
          {cleanToolName(toolName)}
        </Typography>
        {parsedReferences.length > 0 && (
          <Box
            onMouseEnter={(e) => setReferencesAnchorEl(e.currentTarget)}
            onClick={(e) => setReferencesAnchorEl(e.currentTarget)}
            sx={{
              display: 'flex',
              alignItems: 'center',
              gap: '6px',
              cursor: 'pointer',
              padding: '4px 8px',
              borderRadius: '4px',
              transition: 'all 0.2s ease',
              '&:hover': {
                backgroundColor: 'rgba(0, 0, 0, 0.04)',
              },
            }}
          >
            <Typography
              sx={{
                fontSize: '13px',
                fontWeight: '500',
                color: colors.primary,
                fontFamily: '"Poppins", sans-serif',
              }}
            >
              {getUniqueReferencesCount(parsedReferences)} source
              {getUniqueReferencesCount(parsedReferences) !== 1 ? 's' : ''}
            </Typography>
            {parsedReferences.some((r) => r.type === 'file') && <FileDownloadIcon sx={{ fontSize: '16px', color: colors.primary, ml: '2px' }} />}
          </Box>
        )}
        <StatusBadge status={status} />
        <Duration createdAt={toolCall.created_at} updatedAt={toolCall.updated_at} />
      </Box>

      {hasMultipleToolCalls && (
        <Typography sx={{ fontSize: '12px', color: colors.text.tertiary, mb: '12px', fontFamily: 'Roboto' }}>
          {toolCalls.length} tool calls
        </Typography>
      )}

      {/* Multiple tool calls: render each one */}
      {hasMultipleToolCalls ? (
        toolCalls.map((tc, idx) => (
          <React.Fragment key={tc.tool_id || idx}>
            <ToolCallSection tc={tc} index={idx} accountId={accountId} />
            {idx < toolCalls.length - 1 && <Divider sx={{ my: '12px' }} />}
          </React.Fragment>
        ))
      ) : (
        <>
          {/* Single tool call fallback: agent-level thought + response */}
          {agentThought && (
            <Box sx={{ mb: '16px' }}>
              <Typography sx={sectionLabelSx}>Thought</Typography>
              <Box sx={contentBoxSx}>
                {prettyAgentThought ? (
                  <pre style={{ ...preStyle, color: colors.text.secondary }}>{prettyAgentThought}</pre>
                ) : (
                  <MarkDowns data={prettifyJsonFencesInMarkdown(agentThought)} sx={{ p: 0, fontSize: '12px' }} />
                )}
              </Box>
            </Box>
          )}
          {hasAgentResponse && (
            <Box>
              <Typography sx={sectionLabelSx}>Response</Typography>
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
};

export default ToolDetails;
