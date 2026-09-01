import { Box, Typography } from '@mui/material';
import { Chip } from '@ui/Chip';
import { CodeBlock } from '@ui/CodeBlock';
import CopyButton from '@shared/buttons/CopyButton';
import { ds } from '@utils/colors';

interface ExecutedQueryInfoProps {
  /** The provider query that actually ran (evidence `additional_info.executed_query`). */
  query?: string | null;
  /** The log/metric provider it ran against, e.g. `loki` (evidence `additional_info.provider`). */
  provider?: string | null;
}

/**
 * Shows the backend query an evidence was produced by, as a caption row above the
 * evidence's own data.
 *
 * The enrichers stamp `executed_query` + `provider` onto the evidence so a reader can
 * tell what was actually asked of the provider — a where-clause built by the agent is
 * rewritten (label mapping, account default filters) before it runs, so the recorded
 * query is not always the one the playbook asked for. Renders nothing when the backend
 * recorded no query (older evidence, providers that consume the where clause natively),
 * so callers can drop it in unconditionally.
 *
 * Deliberately a one-line row rather than a full `CodeBlock` surface: the evidence data
 * below already sits in its own bordered card, and a second stacked panel for a single
 * line of query reads as the heavier of the two.
 */
const ExecutedQueryInfo = ({ query, provider }: ExecutedQueryInfoProps) => {
  if (!query) return null;

  return (
    <Box data-testid='evidence-executed-query' sx={{ display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: ds.space[2], mb: ds.space[3] }}>
      <Typography sx={{ fontSize: ds.text.caption, fontWeight: ds.weight.medium, color: ds.gray[600], whiteSpace: 'nowrap' }}>
        Executed query
      </Typography>
      {provider && (
        <Chip variant='tag' size='xs' tone='subtle'>
          {provider}
        </Chip>
      )}
      <Box sx={{ flex: '1 1 240px', minWidth: 0 }}>
        <CodeBlock inline code={query} />
      </Box>
      <CopyButton text={query} size='xs' />
    </Box>
  );
};

export default ExecutedQueryInfo;
