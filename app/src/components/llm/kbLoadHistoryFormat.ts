// Pure formatters for the KB Load History table. Extracted from
// KnowledgeBaseTab so they can be unit-tested without rendering the component.

export interface KBLoadHistoryEntry {
  trigger_type?: string | null;
  triggered_by?: string | null;
  document_count: number;
  expected_document_count?: number | null;
  load_duration_seconds?: number | null;
}

const TRIGGER_LABELS: Record<string, string> = {
  user_create: 'Create',
  user_update: 'Update',
  user_retrigger: 'Retrigger',
  system_sync: 'Auto sync',
};

// "Retrigger · Bob" / "Auto sync" — what triggered the load and by whom.
export const formatTrigger = (entry: KBLoadHistoryEntry): string => {
  const label = (entry.trigger_type && TRIGGER_LABELS[entry.trigger_type]) || entry.trigger_type || 'Auto sync';
  const by = entry.triggered_by && entry.triggered_by !== 'system' ? entry.triggered_by : '';
  return by ? `${label} · ${by}` : label;
};

export const formatDuration = (seconds?: number | null): string => {
  if (seconds == null) return '-';
  if (seconds < 60) return `${Math.round(seconds)}s`;
  const mins = Math.floor(seconds / 60);
  const secs = Math.round(seconds % 60);
  return `${mins}m ${secs}s`;
};

// Shows "succeeded / submitted" only when they differ — i.e. a load dropped docs.
export const formatDocuments = (entry: KBLoadHistoryEntry): string | number => {
  if (entry.expected_document_count != null && entry.expected_document_count !== entry.document_count) {
    return `${entry.document_count} / ${entry.expected_document_count}`;
  }
  return entry.document_count;
};
