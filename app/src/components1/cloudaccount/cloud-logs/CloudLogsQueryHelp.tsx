import React, { useState } from 'react';
import { Box, Collapse, Typography } from '@mui/material';
import ContentCopyIcon from '@mui/icons-material/ContentCopy';
import HelpOutlineIcon from '@mui/icons-material/HelpOutline';
import ExpandMoreIcon from '@mui/icons-material/ExpandMore';
import ExpandLessIcon from '@mui/icons-material/ExpandLess';
import { Chip as DsChip } from '@components1/ds/Chip';
import { Button as DsButton } from '@components1/ds/Button';
import { ds } from '@utils/colors';

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

  const toggleExpanded = () => setExpanded((v) => !v);

  return (
    <Box>
      <Box
        sx={{
          display: 'inline-flex',
          alignItems: 'center',
          cursor: 'pointer',
          gap: ds.space[1],
          '&:focus-visible': { outline: `2px solid ${ds.blue[500]}`, outlineOffset: '2px', borderRadius: ds.radius.sm },
        }}
        onClick={toggleExpanded}
        onKeyDown={(e) => {
          if (e.key === 'Enter' || e.key === ' ') {
            e.preventDefault();
            toggleExpanded();
          }
        }}
        role='button'
        tabIndex={0}
        aria-expanded={expanded}
      >
        <HelpOutlineIcon sx={{ fontSize: ds.text.body, color: ds.gray[500] }} />
        <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500] }}>Query Help — {syntaxInfo?.title}</Typography>
        {expanded ? (
          <ExpandLessIcon sx={{ fontSize: ds.text.body, color: ds.gray[500] }} />
        ) : (
          <ExpandMoreIcon sx={{ fontSize: ds.text.body, color: ds.gray[500] }} />
        )}
      </Box>
      <Collapse in={expanded}>
        <Box
          sx={{
            mt: ds.space[2],
            p: ds.space[3],
            bgcolor: ds.gray[100],
            borderRadius: ds.radius.sm,
            border: `1px solid ${ds.gray[200]}`,
          }}
        >
          <Typography sx={{ fontSize: ds.text.caption, fontFamily: 'monospace', whiteSpace: 'pre-line', mb: ds.space[3], color: ds.gray[600] }}>
            {syntaxInfo?.syntax}
          </Typography>
          <Typography sx={{ fontSize: ds.text.small, fontWeight: ds.weight.semibold, mb: ds.space[2], color: ds.gray[700] }}>Examples</Typography>
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[1] }}>
            {examples.map((example, idx) => (
              <Box
                key={idx}
                sx={{
                  display: 'flex',
                  alignItems: 'flex-start',
                  gap: ds.space[2],
                  p: ds.space[1],
                  borderRadius: ds.radius.sm,
                  '&:hover': { bgcolor: ds.gray[200] },
                }}
              >
                <Box sx={{ flexShrink: 0, minWidth: 100 }}>
                  <DsChip variant='tag' tone='neutral' size='xs'>
                    {example.label}
                  </DsChip>
                </Box>
                <Box sx={{ flex: 1, minWidth: 0 }}>
                  <Typography sx={{ fontSize: ds.text.caption, fontFamily: 'monospace', color: ds.gray[700], wordBreak: 'break-all' }}>
                    {example.query}
                  </Typography>
                  <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500], mt: ds.space[1] }}>{example.description}</Typography>
                </Box>
                <DsButton
                  tone='ghost'
                  size='xs'
                  composition='icon-only'
                  icon={<ContentCopyIcon fontSize='small' />}
                  aria-label={`Copy "${example.label}" query`}
                  tooltip='Copy to clipboard and insert into query'
                  onClick={() => {
                    navigator.clipboard.writeText(example.query);
                    if (onInsertQuery) onInsertQuery(example.query);
                  }}
                />
              </Box>
            ))}
          </Box>
        </Box>
      </Collapse>
    </Box>
  );
};

export default CloudLogsQueryHelp;
