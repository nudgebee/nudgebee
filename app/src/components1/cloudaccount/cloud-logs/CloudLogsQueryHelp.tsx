import React, { useState } from 'react';
import { Box, Typography, Collapse, IconButton, Chip } from '@mui/material';
import HelpOutlineIcon from '@mui/icons-material/HelpOutline';
import ExpandMoreIcon from '@mui/icons-material/ExpandMore';
import ExpandLessIcon from '@mui/icons-material/ExpandLess';
import ContentCopyIcon from '@mui/icons-material/ContentCopy';

interface CloudLogsQueryHelpProps {
  provider: 'AWS' | 'Azure' | 'GCP';
  onInsertQuery?: (query: string) => void;
}

interface ExampleQuery {
  label: string;
  query: string;
  description: string;
}

const AWS_EXAMPLES: ExampleQuery[] = [
  {
    label: 'Recent logs',
    query: 'fields @timestamp, @message | sort @timestamp desc',
    description: 'Fetch latest logs sorted by time',
  },
  {
    label: 'Filter errors',
    query: 'fields @timestamp, @message | filter @message like /error/i | sort @timestamp desc',
    description: 'Search for error messages (case-insensitive)',
  },
  {
    label: 'Count by interval',
    query: 'stats count(*) by bin(5m)',
    description: 'Count log entries in 5-minute intervals',
  },
  {
    label: 'Parse fields',
    query: 'parse @message "[*] *" as loggingType, loggingMessage | filter loggingType = "ERROR"',
    description: 'Extract fields from structured log messages',
  },
  {
    label: 'Top talkers',
    query: 'stats count(*) as cnt by @logStream | sort cnt desc | limit 10',
    description: 'Find log streams with most entries',
  },
];

const AZURE_EXAMPLES: ExampleQuery[] = [
  {
    label: 'Recent diagnostics',
    query: 'AzureDiagnostics | project TimeGenerated, Message, Category | order by TimeGenerated desc',
    description: 'Fetch latest Azure diagnostic logs',
  },
  {
    label: 'Filter errors',
    query: 'AzureDiagnostics | where Level == "Error" | project TimeGenerated, Message | order by TimeGenerated desc',
    description: 'Filter diagnostic logs by error level',
  },
  {
    label: 'Summarize by category',
    query: 'AzureDiagnostics | summarize count() by Category | order by count_ desc',
    description: 'Count logs grouped by category',
  },
  {
    label: 'Activity logs',
    query: 'AzureActivity | project TimeGenerated, OperationName, ActivityStatus | order by TimeGenerated desc',
    description: 'View Azure activity/audit logs',
  },
];

const GCP_EXAMPLES: ExampleQuery[] = [
  {
    label: 'Filter by severity',
    query: 'severity="ERROR"',
    description: 'Show only error-level logs',
  },
  {
    label: 'Text search',
    query: 'textPayload:"connection refused"',
    description: 'Search for specific text in log payloads',
  },
  {
    label: 'JSON field match',
    query: 'jsonPayload.message="request completed" AND jsonPayload.status>=500',
    description: 'Filter structured JSON log fields',
  },
  {
    label: 'Resource type filter',
    query: 'resource.type="gce_instance" AND severity>="WARNING"',
    description: 'Logs from Compute Engine instances with warnings or worse',
  },
];

const PROVIDER_SYNTAX: Record<string, { title: string; syntax: string }> = {
  AWS: {
    title: 'CloudWatch Logs Insights',
    syntax: `Commands: fields, filter, stats, sort, limit, parse, display
Operators: like, =, !=, >, <, in, not
Functions: count(), avg(), sum(), min(), max(), earliest(), latest()
Time bins: bin(5m), bin(1h), bin(1d)`,
  },
  Azure: {
    title: 'Kusto Query Language (KQL)',
    syntax: `Tables: AzureDiagnostics, AzureActivity, AzureMetrics
Operators: where, project, summarize, order by, extend, join
Functions: count(), avg(), sum(), percentile(), ago()
Time: ago(1h), ago(1d), between(datetime(...)..datetime(...))`,
  },
  GCP: {
    title: 'Cloud Logging Filter',
    syntax: `Fields: severity, textPayload, jsonPayload, resource.type, resource.labels
Operators: =, !=, >, <, >=, <=, :, AND, OR, NOT
Severity: DEFAULT, DEBUG, INFO, NOTICE, WARNING, ERROR, CRITICAL
Time: timestamp>="2024-01-01T00:00:00Z"`,
  },
};

const EXAMPLES_MAP: Record<string, ExampleQuery[]> = {
  AWS: AWS_EXAMPLES,
  Azure: AZURE_EXAMPLES,
  GCP: GCP_EXAMPLES,
};

const CloudLogsQueryHelp: React.FC<CloudLogsQueryHelpProps> = ({ provider, onInsertQuery }) => {
  const [expanded, setExpanded] = useState(false);
  const syntaxInfo = PROVIDER_SYNTAX[provider];
  const examples = EXAMPLES_MAP[provider] || [];

  return (
    <Box sx={{ mb: 1 }}>
      <Box sx={{ display: 'flex', alignItems: 'center', cursor: 'pointer', gap: 0.5 }} onClick={() => setExpanded(!expanded)}>
        <HelpOutlineIcon sx={{ fontSize: 16, color: '#9E9E9E' }} />
        <Typography sx={{ fontSize: 12, color: '#9E9E9E' }}>Query Help — {syntaxInfo?.title}</Typography>
        {expanded ? <ExpandLessIcon sx={{ fontSize: 16, color: '#9E9E9E' }} /> : <ExpandMoreIcon sx={{ fontSize: 16, color: '#9E9E9E' }} />}
      </Box>
      <Collapse in={expanded}>
        <Box sx={{ mt: 1, p: 1.5, bgcolor: '#f8f9fa', borderRadius: 1, border: '1px solid #e0e0e0' }}>
          <Typography sx={{ fontSize: 11, fontFamily: 'monospace', whiteSpace: 'pre-line', mb: 1.5, color: '#555' }}>{syntaxInfo?.syntax}</Typography>
          <Typography sx={{ fontSize: 12, fontWeight: 600, mb: 1 }}>Examples</Typography>
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1 }}>
            {examples.map((example, idx) => (
              <Box
                key={idx}
                sx={{ display: 'flex', alignItems: 'flex-start', gap: 1, p: 0.75, borderRadius: 0.5, '&:hover': { bgcolor: '#eef1f5' } }}
              >
                <Chip label={example.label} size='small' sx={{ fontSize: 11, height: 22, minWidth: 100 }} />
                <Box sx={{ flex: 1, minWidth: 0 }}>
                  <Typography sx={{ fontSize: 11, fontFamily: 'monospace', color: '#333', wordBreak: 'break-all' }}>{example.query}</Typography>
                  <Typography sx={{ fontSize: 10, color: '#888', mt: 0.25 }}>{example.description}</Typography>
                </Box>
                <IconButton
                  size='small'
                  onClick={() => {
                    navigator.clipboard.writeText(example.query);
                    if (onInsertQuery) onInsertQuery(example.query);
                  }}
                  title='Copy to clipboard and insert into query'
                  sx={{ p: 0.25 }}
                >
                  <ContentCopyIcon sx={{ fontSize: 14 }} />
                </IconButton>
              </Box>
            ))}
          </Box>
        </Box>
      </Collapse>
    </Box>
  );
};

export default CloudLogsQueryHelp;
