// The steps an alert's analysis runs through, in the order the server runs them.
const STAGES = [
  ['summary', 'Summarising the alert'],
  ['investigation', 'Investigating: collecting and reading the evidence'],
  ['log_analysis', 'Checking logs and code for a fix'],
  ['detailed_response', 'Writing the report'],
];

const isWaiting = (status) => ['IN_PROGRESS', 'PENDING'].includes(String(status || '').toUpperCase());

/**
 * Which step of the analysis is running, as "Step 2 of 4 · Investigating: …". The server reports
 * each step's status with every poll; the wait screen used to ignore it and show a random phrase.
 * A run with a single step is named without the count. Empty when the statuses are unknown or no
 * step is waiting.
 *
 * @param {Record<string, string>|null|undefined} taskStatuses step name → status
 */
export const describeAnalysisStage = (taskStatuses) => {
  if (!taskStatuses || typeof taskStatuses !== 'object') return '';
  const present = STAGES.filter(([key]) => taskStatuses[key] !== undefined);
  // Every step is marked in progress when a run starts and completes in order, so the first one
  // still waiting is the one running now.
  const index = present.findIndex(([key]) => isWaiting(taskStatuses[key]));
  if (index < 0) return '';
  const label = present[index][1];
  return present.length > 1 ? `Step ${index + 1} of ${present.length} · ${label}` : label;
};
