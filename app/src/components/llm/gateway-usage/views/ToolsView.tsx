/**
 * ToolsView — the AI Gateway's Tools sub-tab (Phase 1).
 *
 * Shows the tools each request OFFERED to the model (its declared tool set, from
 * `attributes.actual.tool_names`): how often each was made available and the
 * request latency. Which tools were actually CALLED, and tool failures, need
 * response-body parsing and land in a later phase — hence the note. Per-tool
 * cost/tokens are intentionally not shown (a request can offer several tools, so
 * attributing its cost to one would double-count).
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
    { name: 'Tool', width: '56%', component: <HeaderLabel label='Tool' info='A tool the caller offered to the model in the request.' /> },
    {
      name: 'Requests',
      width: '24%',
      align: 'right' as const,
      component: <HeaderLabel label='Requests' info='Number of requests that offered this tool.' />,
    },
    {
      name: 'Avg latency',
      width: '20%',
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
    { component: <Box sx={{ ...numCell, textAlign: 'right' }}>{t.requests.toLocaleString()}</Box> },
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
        These are the tools each request <b>offered</b> to the model (its declared tool set), and how often. Which tools the model actually{' '}
        <b>called</b>, and tool <b>failures</b>, are coming in a later update.
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
