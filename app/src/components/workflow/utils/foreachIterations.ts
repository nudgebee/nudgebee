// Pure helpers for foreach execution display. The backend synthesizes
// per-iteration containers ({id: "Iteration N", type: "iteration", status,
// children}) inside a foreach task's `children` (see runbook-server
// internal/workflow/service.go processWorkflowHistory).

export interface ForeachIterationSummary {
  total: number;
  completed: number;
  failed: number;
  running: number;
}

// Summarize iteration statuses for the canvas badge. Returns null when
// children is not an array (no execution data yet); an empty array yields
// zeros ("0 iterations" — items list was empty). Status values mirror the
// backend TaskStatus constants (COMPLETED / FAILED / STARTED / SCHEDULED).
export const summarizeForeachIterations = (children: any): ForeachIterationSummary | null => {
  if (!Array.isArray(children)) return null;
  const iterations = children.filter((c: any) => c?.type === 'iteration');
  const summary: ForeachIterationSummary = { total: iterations.length, completed: 0, failed: 0, running: 0 };
  for (const iteration of iterations) {
    const status = String(iteration?.status ?? '').toUpperCase();
    if (status === 'FAILED') {
      summary.failed += 1;
    } else if (status === 'STARTED' || status === 'SCHEDULED' || status === 'RUNNING') {
      summary.running += 1;
    } else {
      summary.completed += 1;
    }
  }
  return summary;
};
