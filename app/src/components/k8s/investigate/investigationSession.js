// The alert's one conversation: its investigation, the person's questions, and the assistant's remediation turns
// (plans, approvals, runs).
export const alertSessionOf = (fingerprint) => {
  const fp = typeof fingerprint === 'string' ? fingerprint.trim() : '';
  return fp ? `event-${fp}` : '';
};

// investigationRunOf is the investigation run a message's config names (investigation_run_id), or ''. With the
// remediation loop on, the server tags the investigation's own turns with it.
export function investigationRunOf(messageConfig) {
  if (!messageConfig) return '';
  let cfg = messageConfig;
  if (typeof cfg === 'string') {
    if (!cfg.includes('investigation_run_id')) return '';
    try {
      cfg = JSON.parse(cfg);
    } catch {
      return '';
    }
  }
  return typeof cfg?.investigation_run_id === 'string' ? cfg.investigation_run_id : '';
}

// investigationIsAsking reports whether the conversation's waiting turn is the investigation's (so its question is
// the investigation's to show). With the remediation loop on the conversation also holds the assistant's
// remediation turns, whose approvals belong to the Remediation tab: only a waiting turn the server tagged as an
// investigation stage counts. A conversation with no tagged turn at all (loop off) is the investigation's, as before.
export function investigationIsAsking(messages) {
  const list = Array.isArray(messages) ? messages : [];
  if (!list.some((m) => investigationRunOf(m?.message_config))) return true;
  for (let i = list.length - 1; i >= 0; i--) {
    const m = list[i];
    if (m?.message_type === 'generation' && m?.status === 'WAITING') return Boolean(investigationRunOf(m.message_config));
  }
  return false;
}

// Why the server keeps a re-investigation waiting (the analysis's status_reason while it is IN_PROGRESS), as a
// sentence for the page; '' when it is not waiting on the alert's conversation.
const WAITING_REASONS = {
  'waiting on an approval':
    'An approval is waiting in this alert’s conversation. It starts on its own once the approval is answered (open the Remediation tab), or when the approval lapses after 12 hours.',
  "waiting on an answer in the alert's conversation":
    'A question is waiting in this alert’s conversation. It starts on its own once the question is answered.',
  'waiting for a step that is running, or for its follow-up check':
    'A remediation step is running or being checked. It starts on its own once that is done.',
  "waiting for the current turn in the alert's conversation":
    'Another turn is running in this alert’s conversation. It starts on its own once that turn ends.',
  "waiting: the alert's conversation could not be checked": 'The alert’s conversation could not be checked. It is retried shortly.',
};

export function investigationWaitingReason(aiData) {
  if (String(aiData?.status || '').toUpperCase() !== 'IN_PROGRESS') return '';
  return WAITING_REASONS[String(aiData?.status_reason || '').trim()] || '';
}
