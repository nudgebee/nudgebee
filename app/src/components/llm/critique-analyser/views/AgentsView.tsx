/** Refine-rate-by-agent table, worst-first. Row click jumps to Browse pre-filtered to that agent. */
import * as React from 'react';
import { Box, CircularProgress } from '@mui/material';
import { Card } from '@ui/Card';
import { Banner } from '@ui/Banner';
import CustomTable2 from '@shared/tables/CustomTable';
import { fmtPct } from '@components/llm/cost-analyser/format';
import type { CritiqueSummary } from '@api1/critiques';

interface AgentsViewProps {
  summary: CritiqueSummary | null;
  loading: boolean;
  error: string | null;
  onSelectAgent: (agentName: string) => void;
}

function fmtCount(n: number | null | undefined): string {
  if (n === null || n === undefined) return '—';
  return n.toLocaleString('en-US');
}

const numCell = { fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-700)', fontVariantNumeric: 'tabular-nums' } as const;

export function AgentsView({ summary, loading, error, onSelectAgent }: AgentsViewProps) {
  if (error) return <Banner tone='critical' title='Could not load critique data' message={error} />;

  if (loading) {
    return (
      <Box sx={{ display: 'flex', justifyContent: 'center', alignItems: 'center', minHeight: 240 }}>
        <CircularProgress size={28} />
      </Box>
    );
  }

  const rows = [...(summary?.by_agent ?? [])].sort((a, b) => b.refine_pct - a.refine_pct);

  const headers = [
    { name: 'Agent', width: '34%' },
    { name: 'Judged', width: '18%' },
    { name: 'Refined', width: '18%' },
    { name: 'Refine rate', width: '30%' },
  ];
  const tableData = rows.map((r) => [
    {
      component: (
        <Box
          role='button'
          tabIndex={0}
          onClick={() => onSelectAgent(r.agent_name)}
          sx={{
            fontSize: 'var(--ds-text-body)',
            color: 'var(--ds-blue-600)',
            cursor: 'pointer',
            '&:hover': { textDecoration: 'underline' },
          }}
        >
          {r.agent_name}
        </Box>
      ),
    },
    { component: <Box sx={numCell}>{fmtCount(r.judged)}</Box> },
    { component: <Box sx={numCell}>{fmtCount(r.refined)}</Box> },
    {
      component: (
        <Box
          sx={{ ...numCell, fontWeight: 'var(--ds-font-weight-semibold)', color: r.refine_pct >= 30 ? 'var(--ds-red-600)' : 'var(--ds-gray-700)' }}
        >
          {fmtPct(r.refine_pct / 100, 1)}
        </Box>
      ),
    },
  ]);

  return (
    <Card>
      <CustomTable2 id='critique-agents-table' headers={headers} tableData={tableData} />
    </Card>
  );
}

export default AgentsView;
