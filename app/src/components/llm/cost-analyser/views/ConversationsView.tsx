/**
 * Screen 2 — Conversations & Workflows explorer (spec §5).
 *
 * Backed by `ai_list_conversation_costs` (cost-desc, up to `listCap` rows). Columns
 * are sorted client-side via the table header sort icons; the title opens the full
 * report. The backend list is row-level (no per-step tree), so rows don't expand —
 * open a conversation for its task/model-call breakdown.
 */
import * as React from 'react';
import FileDownloadOutlinedIcon from '@mui/icons-material/FileDownloadOutlined';
import { Button } from '@ui/Button';
import { Card } from '@ui/Card';
import ConversationsTable from '../components/ConversationsTable';
import { downloadFile, runsToCsv } from '../format';
import type { Run } from '../types';

interface ConversationsViewProps {
  runs: Run[];
  /** Total conversations matching the filter (may exceed the fetched page). */
  total: number;
  /** Max rows fetched in one page. */
  listCap: number;
  /** account_id → display name, for the per-row account label (no extra fetch). */
  accountNameById?: Record<string, string>;
  onSelectRun: (sessionId: string) => void;
  /** Open the detail modal on the Optimize tab and analyze (cached or fresh). */
  onAnalyse?: (sessionId: string) => void;
  /** Shows the table's own skeleton rows instead of an external spinner. */
  loading?: boolean;
}

export function ConversationsView({ runs, total, listCap, accountNameById, onSelectRun, onAnalyse, loading }: ConversationsViewProps) {
  const handleExport = () => downloadFile('ai-cost-conversations.csv', runsToCsv(runs));
  const capped = total > runs.length;
  // Hide the count caption while loading — the row counts aren't meaningful yet.
  const caption = loading
    ? undefined
    : `${
        capped ? `Showing top ${runs.length} of ${total} by cost (max ${listCap})` : `${runs.length} conversations`
      } · click a column header to sort`;

  return (
    <Card>
      <ConversationsTable
        runs={runs}
        accountNameById={accountNameById}
        onSelectRun={onSelectRun}
        onAnalyse={onAnalyse}
        loading={loading}
        defaultSort={{ key: 'cost', order: 'desc' }}
        id='conversations-table'
        caption={caption}
        headerActions={
          <Button tone='secondary' size='sm' icon={<FileDownloadOutlinedIcon sx={{ fontSize: 16 }} />} onClick={handleExport} id='cost-export-csv'>
            Export CSV
          </Button>
        }
      />
    </Card>
  );
}

export default ConversationsView;
