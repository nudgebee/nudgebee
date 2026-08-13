/** Themes sub-tab: recurring critique reasons with real examples. Heuristic keyword match, not a taxonomy. */
import * as React from 'react';
import { Box, CircularProgress } from '@mui/material';
import { Accordion, type AccordionItem } from '@ui/Accordion';
import { Banner } from '@ui/Banner';
import { Chip } from '@ui/Chip';
import { Button } from '@ui/Button';
import type { CritiqueSummary } from '@api1/critiques';

interface ThemesViewProps {
  summary: CritiqueSummary | null;
  loading: boolean;
  error: string | null;
  onSelectTheme: (theme: string) => void;
}

function fmtCount(n: number | null | undefined): string {
  if (n === null || n === undefined) return '—';
  return n.toLocaleString('en-US');
}

export const THEME_LABELS: Record<string, string> = {
  root_cause_not_verified: 'Root cause not verified',
  incomplete: 'Incomplete answer',
  manual_action: 'Manual CLI/action instead of executing',
  evidence: 'No evidence cited',
  verify: 'Claim not verified/confirmed',
  guessing: 'Guessing / assuming',
  hallucination: 'Hallucination',
  symptom_not_cause: 'Symptom, not root cause',
  schema_validation: 'Schema/validation failure',
};

const quoteBox = {
  fontFamily: 'var(--ds-font-mono, monospace)',
  fontSize: 'var(--ds-text-caption)',
  color: 'var(--ds-gray-700)',
  whiteSpace: 'pre-wrap',
  wordBreak: 'break-word',
  backgroundColor: 'var(--ds-background-200)',
  borderRadius: 'var(--ds-radius-md)',
  padding: 'var(--ds-space-3)',
} as const;

export function ThemesView({ summary, loading, error, onSelectTheme }: ThemesViewProps) {
  if (error) return <Banner tone='critical' title='Could not load critique data' message={error} />;

  if (loading) {
    return (
      <Box sx={{ display: 'flex', justifyContent: 'center', alignItems: 'center', minHeight: 240 }}>
        <CircularProgress size={28} />
      </Box>
    );
  }

  const themes = [...(summary?.themes ?? [])].filter((t) => t.count > 0).sort((a, b) => b.count - a.count);

  const items: AccordionItem[] = themes.map((t) => ({
    id: t.theme,
    label: THEME_LABELS[t.theme] ?? t.theme,
    meta: (
      <Chip size='sm' variant='tag' tone='warning'>
        {fmtCount(t.count)} refine{t.count === 1 ? '' : 's'}
      </Chip>
    ),
    body: (
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-3)' }}>
        {t.examples.length === 0 ? (
          <Box sx={{ fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-500)' }}>No example text captured.</Box>
        ) : (
          t.examples.map((ex) => (
            <Box key={ex.id} sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-1)' }}>
              <Box sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-500)', fontWeight: 'var(--ds-font-weight-semibold)' }}>
                {ex.agent_name}
              </Box>
              <Box sx={quoteBox}>{ex.feedback || '(no feedback text)'}</Box>
            </Box>
          ))
        )}
        <Box>
          <Button tone='link' size='sm' onClick={() => onSelectTheme(t.theme)}>
            View all {fmtCount(t.count)} in Browse →
          </Button>
        </Box>
      </Box>
    ),
  }));

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-4)' }}>
      <Banner
        tone='warning'
        title='Heuristic, not ground truth'
        message='Themes are keyword matches over critique feedback text, validated by hand against a handful of agents. A row can match several themes or none, and agents outside that validation set may be under- or mis-classified. Read the examples below before prioritizing prompt fixes off these counts alone.'
      />
      {items.length === 0 ? (
        <Box sx={{ p: 'var(--ds-space-4)', color: 'var(--ds-gray-500)', fontSize: 'var(--ds-text-body)' }}>
          No refine rows matched any theme for the current filters.
        </Box>
      ) : (
        <Accordion items={items} selection='multi' density='md' />
      )}
    </Box>
  );
}

export default ThemesView;
