/** Browse sub-tab: full readable query/answer/feedback text per record, via expandable Accordion rows. */
import * as React from 'react';
import { Box, CircularProgress } from '@mui/material';
import dayjs from 'dayjs';
import OpenInNewOutlinedIcon from '@mui/icons-material/OpenInNewOutlined';
import { Accordion, type AccordionItem } from '@ui/Accordion';
import { Card } from '@ui/Card';
import { Banner } from '@ui/Banner';
import { Chip } from '@ui/Chip';
import { Button } from '@ui/Button';
import { Divider } from '@ui/Divider';
import FilterDropdown from '@ui/FilterDropdown';
import MarkDowns from '@shared/viewers/MarkDowns';
import CustomTablePagination from '@shared/tables/CustomTablePagination';
import { useCritiqueList, CRITIQUE_BROWSE_PAGE_SIZE, type CritiqueFilters } from '../useCritiqueData';
import type { CritiqueListRow } from '@api1/critiques';

interface BrowseViewProps {
  filters: CritiqueFilters;
  decisions: string[];
  onDecisionsChange: (decisions: string[]) => void;
  /** Overrides the decision filter server-side when set. */
  theme?: string;
  themeLabel?: string;
  onClearTheme?: () => void;
}

const DECISION_OPTIONS = ['accept', 'refine', 'skipped', 'update', 'reject', 'complete'];

const preBox = {
  fontFamily: 'var(--ds-font-mono, monospace)',
  fontSize: 'var(--ds-text-caption)',
  color: 'var(--ds-gray-700)',
  whiteSpace: 'pre-wrap',
  wordBreak: 'break-word',
  backgroundColor: 'var(--ds-background-200)',
  borderRadius: 'var(--ds-radius-md)',
  padding: 'var(--ds-space-3)',
  maxHeight: '40vh',
  overflowY: 'auto',
} as const;

const previewText = {
  fontSize: 'var(--ds-text-small)',
  color: 'var(--ds-gray-500)',
  overflow: 'hidden',
  textOverflow: 'ellipsis',
  whiteSpace: 'nowrap',
  maxWidth: 480,
} as const;

function decisionTone(decision: string): 'success' | 'warning' | 'neutral' {
  if (decision === 'accept') return 'success';
  if (decision === 'refine') return 'warning';
  return 'neutral';
}

function FieldBlock({ label, text }: { label: string; text: string }) {
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-1)' }}>
      <Box sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-500)', fontWeight: 'var(--ds-font-weight-semibold)' }}>{label}</Box>
      <Box sx={preBox}>{text || '(empty)'}</Box>
    </Box>
  );
}

/** Answer block: rendered markdown by default, with a toggle back to the raw
 * text — the exact agent output still matters when debugging. */
function AnswerBlock({ text }: { text: string }) {
  const [showRaw, setShowRaw] = React.useState(false);
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-1)' }}>
      <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
        <Box sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-500)', fontWeight: 'var(--ds-font-weight-semibold)' }}>Answer</Box>
        <Button tone='link' size='xs' onClick={() => setShowRaw((v) => !v)} id='critique-answer-toggle-raw'>
          {showRaw ? 'Show rendered' : 'Show raw'}
        </Button>
      </Box>
      {showRaw ? (
        <Box sx={preBox}>{text || '(empty)'}</Box>
      ) : text ? (
        <Box sx={{ ...preBox, fontFamily: 'inherit', whiteSpace: 'normal' }}>
          <MarkDowns data={text} sx={{ width: '100%' }} allowExecutable={undefined} canRunCode={false} onLinkClick={null} />
        </Box>
      ) : (
        <Box sx={preBox}>(empty)</Box>
      )}
    </Box>
  );
}

/** Critique feedback: prose (not monospace) in a tinted card so it reads as
 * the reviewer's note rather than a fourth text dump. Tone follows the row's
 * own decision (accept → success, refine → warning) instead of a fixed hue. */
function FeedbackBlock({ text, tone }: { text: string; tone: 'success' | 'warning' | 'neutral' }) {
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-1)' }}>
      <Box sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-500)', fontWeight: 'var(--ds-font-weight-semibold)' }}>
        Critique feedback
      </Box>
      <Card variant='tinted' tone={tone} size='sm'>
        <Box sx={{ fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-700)', whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}>
          {text || '(empty)'}
        </Box>
      </Card>
    </Box>
  );
}

function CritiqueBody({ row }: { row: CritiqueListRow }) {
  const canOpenConversation = !!row.session_id && !!row.account_id;
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-3)' }}>
      {canOpenConversation && (
        <Box sx={{ display: 'flex', justifyContent: 'flex-end' }}>
          <Button
            tone='secondary'
            size='sm'
            icon={<OpenInNewOutlinedIcon sx={{ fontSize: 16 }} />}
            onClick={() => window.open(`/ask-nudgebee?accountId=${row.account_id}&session_id=${row.session_id}`, '_blank', 'noopener,noreferrer')}
            id={`critique-goto-conversation-${row.id}`}
          >
            Go to conversation
          </Button>
        </Box>
      )}
      <FieldBlock label='Query' text={row.input} />
      <Divider />
      <AnswerBlock text={row.critiqued_content} />
      <Divider />
      <FeedbackBlock text={row.feedback} tone={decisionTone(row.decision)} />
    </Box>
  );
}

export function BrowseView({ filters, decisions, onDecisionsChange, theme, themeLabel, onClearTheme }: BrowseViewProps) {
  const [page, setPage] = React.useState(0);

  // Reset to page 0 whenever the scoping filters change.
  const scopeKey = JSON.stringify({ startDate: filters.startDate, endDate: filters.endDate, agents: filters.agents, decisions, theme });
  React.useEffect(() => setPage(0), [scopeKey]);

  const { loading, error, list } = useCritiqueList(filters, decisions, page, theme);

  const items: AccordionItem[] = (list?.rows ?? []).map((r) => ({
    id: r.id,
    label: `${r.agent_name} · ${dayjs(r.created_at).format('DD MMM HH:mm')}`,
    description: (
      <Box sx={previewText} title={r.feedback}>
        {r.feedback || '(no feedback text)'}
      </Box>
    ),
    meta: (
      <Chip size='xs' variant='tag' tone={decisionTone(r.decision)}>
        {r.decision}
      </Chip>
    ),
    body: <CritiqueBody row={r} />,
  }));

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-3)' }}>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)', flexWrap: 'wrap' }}>
        {theme ? (
          <>
            <Box sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-500)' }}>
              Theme: <strong>{themeLabel ?? theme}</strong> (refine only)
            </Box>
            {onClearTheme && (
              <Button tone='link' size='sm' onClick={onClearTheme}>
                Clear theme
              </Button>
            )}
          </>
        ) : (
          <FilterDropdown
            id='critique-browse-decision'
            label='Decision'
            multiple
            options={DECISION_OPTIONS}
            value={decisions}
            onSelect={(_e: unknown, sel: (string | { value?: string })[]) =>
              onDecisionsChange((sel ?? []).map((o) => (typeof o === 'string' ? o : String(o?.value ?? ''))))
            }
          />
        )}
        {filters.agents.length > 0 && (
          <Box sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-500)' }}>Scoped to agent: {filters.agents.join(', ')}</Box>
        )}
      </Box>

      {error && <Banner tone='critical' title='Could not load critiques' message={error} />}

      {loading ? (
        <Box sx={{ display: 'flex', justifyContent: 'center', alignItems: 'center', minHeight: 240 }}>
          <CircularProgress size={28} />
        </Box>
      ) : items.length === 0 ? (
        <Card>
          <Box sx={{ p: 'var(--ds-space-4)', color: 'var(--ds-gray-500)', fontSize: 'var(--ds-text-body)' }}>No critiques match these filters.</Box>
        </Card>
      ) : (
        <>
          <Accordion items={items} selection='single' density='sm' />
          <CustomTablePagination
            page={page + 1}
            totalRows={list?.total ?? 0}
            totalPages={Math.max(1, Math.ceil((list?.total ?? 0) / CRITIQUE_BROWSE_PAGE_SIZE))}
            rowsPerPage={CRITIQUE_BROWSE_PAGE_SIZE}
            onPageChange={(nextPage: number) => setPage(nextPage - 1)}
          />
        </>
      )}
    </Box>
  );
}

export default BrowseView;
