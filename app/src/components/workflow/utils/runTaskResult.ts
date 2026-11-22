// What the "Run Task" panel (ActionDetailsSidebar) receives for a single task
// run: a failure message, or the run's own status plus the task output.
export type RunTaskOutcome = { error: string } | { status: string; output: any; error?: string };

// Maps a `workflow_execute_task` response onto the panel's shape.
//
// The status comes from the run, never from the task's output. Check tasks such
// as network.tcp complete with `{ reachable: false, error: "..." }`; reading
// that `error` as a failed run made the panel show FAILED for a run the API
// recorded as COMPLETED. The task's own error text is still passed on, so the
// panel shows why the check came back negative next to the COMPLETED status.
export const toRunTaskOutcome = (taskResult: any): RunTaskOutcome => {
  if (taskResult?.status === 'FAILED') {
    return { error: taskResult.error || 'Task execution failed' };
  }
  const output = taskResult?.result ?? taskResult ?? null;
  const outputError = typeof output?.error === 'string' && output.error ? output.error : undefined;
  return { status: taskResult?.status || 'COMPLETED', output, ...(outputError && { error: outputError }) };
};
