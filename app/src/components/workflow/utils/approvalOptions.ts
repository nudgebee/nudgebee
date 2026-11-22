/**
 * The button labels to offer for a pending `core.approval` task.
 *
 * `approval_options` is only a literal list in the simple case. It is equally
 * valid to template it off an upstream task — e.g.
 * `approval_options: "{{ Tasks['vpc-options'].output.data }}"` to let the
 * approver pick one of the VPCs a previous step discovered. In that case the
 * definition holds a *string*, and the real list only exists in the params as
 * they were rendered for this run (the activity input the execution API
 * returns as `task.input`).
 *
 * So `renderedInput` is the source of truth and `taskConfig` is the fallback
 * — the reverse order silently drops every templated list, which is what
 * produced "No approval_options configured on this task." on a run whose
 * options had resolved fine server-side.
 *
 * When neither yields a list the field is simply unset, which is valid input:
 * the task runs with `["approve", "reject"]` (approval_task.go:65, and the
 * schema default at :332) and CompleteApprovalTaskFromUI skips its allow-list
 * check entirely when the rendered input carries no options (service.go:3342).
 * Offering nothing there is what leaves those runs unapprovable (#38377).
 */
export const DEFAULT_APPROVAL_OPTIONS = ['approve', 'reject'];

export function resolveApprovalOptions(renderedInput: unknown, configuredParams: unknown): string[] {
  const fromRun = pickOptions(renderedInput);
  if (fromRun.length > 0) return fromRun;
  const fromConfig = pickOptions(configuredParams);
  if (fromConfig.length > 0) return fromConfig;
  return [...DEFAULT_APPROVAL_OPTIONS];
}

function pickOptions(params: unknown): string[] {
  const raw = (params as Record<string, unknown> | null | undefined)?.approval_options;
  if (!Array.isArray(raw)) return [];
  return raw.filter((o): o is string => typeof o === 'string' && o.length > 0);
}

const IM_PROVIDER_NAMES: Record<string, string> = { slack: 'Slack', ms_teams: 'Microsoft Teams' };

const asRecord = (v: unknown): Record<string, unknown> | null =>
  v && typeof v === 'object' && !Array.isArray(v) && Object.keys(v).length > 0 ? (v as Record<string, unknown>) : null;

// The approver's question and where else they can answer. Rendered input first, like
// resolveApprovalOptions; in_app (or unset) sends nothing, so it names no channel (#41476).
export function describePendingApproval(renderedInput: unknown, configuredParams: unknown): { message: string; respondHint: string } {
  const run = asRecord(renderedInput);
  const config = asRecord(configuredParams);
  const rawMessage = run?.message ?? config?.message;
  const message = rawMessage == null ? '' : String(rawMessage).trim();
  return { message, respondHint: respondHintFor(run?.approval_type ?? config?.approval_type, run?.im_provider ?? config?.im_provider) };
}

function respondHintFor(approvalType: unknown, imProvider: unknown): string {
  if (approvalType === 'instant_message') {
    const name = typeof imProvider === 'string' ? IM_PROVIDER_NAMES[imProvider] : undefined;
    return name ? `Respond below or in ${name}` : 'Respond below or in the approval message';
  }
  if (approvalType === 'email') return 'Respond below or from the approval email';
  return 'Respond below';
}
