/**
 * ToolsView — the AI Gateway's Tools sub-tab.
 *
 * Per tool: how often it was OFFERED (declared tool set, `attributes.actual.
 * tool_names`), how often it was actually CALLED (`called_tools`), and how many
 * calls FAILED (`failed_tools`) with the resulting failure rate — the calls/failures
 * are extracted from the conversation tail at capture (no response parsing needed).
 * Failures are Anthropic-only (its tool_result carries a clean is_error; OpenAI/Gemini
 * expose no structured tool-error flag), so a tool with calls but 0 failures on those
 * providers means "not measured", not "never failed". Per-tool cost/tokens are omitted
 * (a request can offer several tools, so attributing its cost to one would double-count).
 */
import * as React from 'react';
import { Box, CircularProgress } from '@mui/material';
import CustomTable2 from '@shared/tables/CustomTable';
import { Card } from '@ui/Card';
import { Banner } from '@ui/Banner';
import { EmptyState } from '@ui/EmptyState';
import HeaderLabel from '@components/llm/cost-analyser/components/HeaderLabel';
import { fmtDuration } from '@components/llm/cost-analyser/format';
import type { GatewayUsageMetrics } from '@api1/gateway-usage';

interface ToolsViewProps {
  metrics: GatewayUsageMetrics | null;
  loading: boolean;
  error: string | null;
  /** Drill-in: jump to the Requests tab scoped to requests that offered this tool. */
  onSelectTool: (tool: string) => void;
}

const numCell = { fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-700)', fontVariantNumeric: 'tabular-nums' } as const;

/** failureRate formats failures/calls as a percentage (e.g. "3.8%"); empty when no calls. */
function failureRate(failures: number, calls: number): string {
  if (!calls) return '';
  const pct = (failures / calls) * 100;
  return `${pct % 1 === 0 ? pct.toFixed(0) : pct.toFixed(1)}%`;
}

export function ToolsView({ metrics, loading, error, onSelectTool }: ToolsViewProps) {
  const tools = metrics?.tools ?? [];

  if (error) return <Banner tone='critical' title='Could not load gateway tools' message={error} />;
  if (loading) {
    return (
      <Box sx={{ display: 'flex', justifyContent: 'center', alignItems: 'center', minHeight: 240 }}>
        <CircularProgress size={28} />
      </Box>
    );
  }

  const headers = [
    { name: 'Tool', width: '26%', component: <HeaderLabel label='Tool' info='A tool the caller offered to the model in the request.' /> },
    { name: 'Models', width: '22%', component: <HeaderLabel label='Models' info='The distinct models that offered this tool.' /> },
    {
      name: 'Requests',
      width: '13%',
      align: 'right' as const,
      component: <HeaderLabel label='Requests' info='Number of requests that offered this tool.' />,
    },
    {
      name: 'Calls',
      width: '13%',
      align: 'right' as const,
      component: <HeaderLabel label='Calls' info='Times the model actually invoked this tool.' />,
    },
    {
      name: 'Failures',
      width: '13%',
      align: 'right' as const,
      component: (
        <HeaderLabel
          label='Failures'
          info='Calls whose result was an error. Anthropic only (tool_result.is_error); OpenAI/Gemini expose no error flag, so 0 there means not measured.'
        />
      ),
    },
    {
      name: 'Avg latency',
      width: '13%',
      align: 'right' as const,
      component: <HeaderLabel label='Avg latency' info='Average request latency when this tool was offered.' />,
    },
  ];
  const tableData = tools.map((t) => [
    {
      component: (
        <Box
          component='button'
          type='button'
          title={`View requests that offered ${t.tool}`}
          onClick={() => onSelectTool(t.tool)}
          id={`gateway-tool-link-${t.tool}`}
          sx={{
            all: 'unset',
            cursor: 'pointer',
            boxSizing: 'border-box',
            textAlign: 'left',
            color: 'var(--ds-blue-600)',
            fontSize: 'var(--ds-text-body)',
            fontWeight: 'var(--ds-font-weight-medium)',
            overflowWrap: 'anywhere',
            '&:hover': { textDecoration: 'underline' },
            '&:focus-visible': { outline: '2px solid var(--ds-blue-400)', outlineOffset: 2, borderRadius: 'var(--ds-radius-sm)' },
          }}
        >
          {t.tool}
        </Box>
      ),
    },
    {
      component: (
        <Box sx={{ fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-600)', overflowWrap: 'anywhere' }}>
          {t.models && t.models.length > 0 ? t.models.join(', ') : '—'}
        </Box>
      ),
    },
    { component: <Box sx={{ ...numCell, textAlign: 'right' }}>{t.requests.toLocaleString()}</Box> },
    { component: <Box sx={{ ...numCell, textAlign: 'right' }}>{(t.calls ?? 0).toLocaleString()}</Box> },
    {
      component: (
        <Box sx={{ ...numCell, textAlign: 'right', color: (t.failures ?? 0) > 0 ? 'var(--ds-red-600)' : 'var(--ds-gray-500)' }}>
          {(t.failures ?? 0) > 0 ? `${t.failures.toLocaleString()} (${failureRate(t.failures, t.calls)})` : '—'}
        </Box>
      ),
    },
    { component: <Box sx={{ ...numCell, textAlign: 'right' }}>{fmtDuration((t.avg_latency_seconds ?? 0) * 1000)}</Box> },
  ]);

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-4)' }}>
      <Box
        sx={{
          p: 'var(--ds-space-3)',
          backgroundColor: 'var(--ds-gray-100)',
          border: '1px solid var(--ds-gray-200)',
          borderRadius: 'var(--ds-radius-md)',
          fontSize: 'var(--ds-text-body)',
          color: 'var(--ds-gray-600)',
          lineHeight: 1.5,
        }}
      >
        Per tool: how often it was <b>offered</b> to the model, how often it was actually <b>called</b>, and how many calls <b>failed</b>. Failures
        are Anthropic-only (its tool result carries an error flag); on OpenAI/Gemini a 0 means not measured, not never-failed.
      </Box>

      {tools.length === 0 ? (
        <EmptyState
          size='section'
          illustration='no-results'
          title='No tools'
          description='No requests in this range offered tools. Try widening the date range.'
        />
      ) : (
        <Card>
          <CustomTable2 id='gateway-tools-table' headers={headers} tableData={tableData} />
        </Card>
      )}
    </Box>
  );
}

export default ToolsView;
