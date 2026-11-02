import observability from '@api1/observability';
import type { Panel } from '@api1/dashboards';
import { panelStep } from './panelBounds';

/**
 * Pieces of the "Generate with AI" option that do not need React: reading the
 * agent's draft out of its conversation, and checking each generated panel
 * against the account before the editor opens.
 */

/** A dashboard file in the shape the importer reads, as the agent returns it. */
export interface GeneratedDashboardModel {
  title: string;
  description?: string;
  definition: { panels: unknown[] };
  /** What the agent dropped or changed while checking its own answer. */
  warnings?: string[];
}

export type ExtractResult = { model: GeneratedDashboardModel } | { error: string };

function parseDraft(text: unknown): GeneratedDashboardModel | null {
  if (typeof text !== 'string' || !text.trim().startsWith('{')) return null;
  try {
    const parsed = JSON.parse(text);
    if (parsed && typeof parsed === 'object' && Array.isArray(parsed.definition?.panels)) return parsed as GeneratedDashboardModel;
  } catch {
    // Not JSON — an error message, handled by the caller.
  }
  return null;
}

/** The agent whose answer is the checked draft; its child `dashboard_writer` row holds the raw, unchecked one. */
const BUILDER_AGENT = 'dashboard_builder';

/**
 * Finds the draft in a finished conversation, or the reason there is none.
 * `messageId` names the turn to read (a Nubi answer opens its own turn, even
 * after a follow-up); without it the latest turn is read.
 *
 * Only the turn's own answer is read: the message response, else the top-level
 * `dashboard_builder` row. The writer agent's row sits beside it and holds the
 * model's raw JSON before the builder checked it — reading that would bypass the
 * checks, and a raw answer the builder rejected would still open. For the same
 * reason nothing is taken from a turn that did not complete.
 */
export function extractGeneratedDashboard(conversation: any, messageId?: string): ExtractResult {
  const messages: any[] = conversation?.llm_conversation_messages || [];
  const message = messageId ? messages.find((m) => m?.id === messageId) : messages[messages.length - 1];
  if (messageId && !message) return { error: 'That answer is no longer in the conversation.' };
  // A named turn is judged by its own status: the conversation's is the latest turn's.
  const status = messageId ? message?.status : conversation?.status;
  const builder = (message?.llm_conversation_agents || []).find((a: any) => a?.agent_name === BUILDER_AGENT && !a?.parent_agent_id);
  const answers: unknown[] = [message?.response, builder?.response];
  const text = answers.find((r): r is string => typeof r === 'string' && r.trim() !== '');

  if (status !== 'COMPLETED') {
    return { error: text || 'Dashboard generation did not finish. Try again.' };
  }
  for (const answer of answers) {
    const model = parseDraft(answer);
    if (model) return { model };
  }
  return {
    error: text ? `The answer was not a dashboard: ${text.slice(0, 300)}` : 'The answer was empty. Try describing the dashboard differently.',
  };
}

/** The conversation source llm-server stamps on every dashboard generation. */
export const DASHBOARD_BUILDER_SOURCE = 'DashboardBuilder';

/** Query param that tells the Dashboards page a generated draft is waiting for it. */
export const NUBI_DRAFT_PARAM = 'draftFrom';
const NUBI_DRAFT_KEY = 'nubiGeneratedDashboard';

export interface StashedDashboardDraft {
  accountId: string;
  model: GeneratedDashboardModel;
  /** The description it was generated from, shown again in the preview. */
  prompt?: string;
}

/**
 * Hands a generated dashboard from a Nubi conversation to the Dashboards page.
 * The draft rides in sessionStorage (the workflow builder's hand-off for AI
 * output) rather than the URL: a 20-panel definition does not fit in one.
 * Returns why it could not, or null once it is stashed.
 */
export function stashGeneratedDashboard(conversation: any, messageId?: string): string | null {
  const extracted = extractGeneratedDashboard(conversation, messageId);
  if ('error' in extracted) return extracted.error;
  const accountId = conversation?.account_id;
  if (!accountId) return 'This conversation has no account to scope the dashboard to.';
  const message = (conversation?.llm_conversation_messages || []).find((m: any) => m?.id === messageId);
  const prompt = String(message?.message || conversation?.title || '');
  const draft: StashedDashboardDraft = { accountId, model: extracted.model, prompt };
  try {
    sessionStorage.setItem(NUBI_DRAFT_KEY, JSON.stringify(draft));
  } catch {
    return 'Your browser blocked handing the dashboard over. Generate it from the Dashboards page instead.';
  }
  return null;
}

/** Reads and clears the stashed draft, so a reload or Back does not reopen it. */
export function takeStashedDashboard(): StashedDashboardDraft | null {
  try {
    const raw = sessionStorage.getItem(NUBI_DRAFT_KEY);
    sessionStorage.removeItem(NUBI_DRAFT_KEY);
    const parsed = raw ? JSON.parse(raw) : null;
    if (parsed && typeof parsed.accountId === 'string' && Array.isArray(parsed.model?.definition?.panels)) return parsed;
  } catch {
    // Storage blocked or the value is not ours — nothing to open.
  }
  return null;
}

/** Whether a panel's queries returned anything when tried against the account. */
export type PanelCheck = 'data' | 'empty' | 'error';

export interface PanelCheckResult {
  status: PanelCheck;
  /** The provider's message for an `error`, or for the failed queries of a panel that otherwise answered. */
  reason?: string;
  /** Targets whose query failed on a panel that still has a working one; left out of the draft. */
  failedRefIds?: string[];
}

/** Window the check reads: the last hour, the window a new dashboard opens on. */
const CHECK_WINDOW_MS = 60 * 60 * 1000;
/** Queries in flight at once; a 20-panel draft should not fire 20 requests together. */
const CHECK_CONCURRENCY = 4;

function gatewayMessage(errors: any): string {
  return String(errors?.[0]?.message || errors?.[0]?.extensions?.internal?.error?.message || '');
}

/**
 * Tries one panel's queries the way the panel itself will: the same
 * `metricsQuery` request with the same account over the last hour, an instant
 * query for a stat, gauge or table and a range query with the panel's step for a
 * chart — the shapes usePanelData sends, so a query only a range evaluation
 * rejects is caught here too. A series that stopped reporting reads as empty,
 * which is why empty panels are kept and only named.
 *
 * Not checked: usePanelData sends an AWS account's metrics to CloudWatch, and
 * this always asks the account's default backend — the generator writes PromQL
 * for that backend only.
 */
export async function checkPanel(panel: Panel, accountId: string, now: number = Date.now()): Promise<PanelCheckResult> {
  if (panel.type === 'text') return { status: 'data' };
  const queries: Record<string, string> = {};
  for (const target of panel.targets || []) {
    if (target.expr?.trim()) queries[target.ref_id] = target.expr;
  }
  if (Object.keys(queries).length === 0) return { status: 'error', reason: 'The panel has no query.' };

  const start = now - CHECK_WINDOW_MS;
  const instant = panel.type === 'stat' || panel.type === 'gauge' || panel.type === 'table' || panel.type === 'pie';
  try {
    const response: any = await observability.metricsQuery({
      account_id: accountId,
      queries,
      start_time: start,
      end_time: now,
      instant,
      ...(instant ? {} : { step_interval: panelStep(start, now) }),
    });
    const errors = response?.data?.errors;
    if (errors?.length) return { status: 'error', reason: gatewayMessage(errors) };
    const results: any[] = response?.data?.data?.metrics_list?.results || [];
    // A failed query comes back as a key carrying `error` inside a 200.
    const keyErrors = results.filter((r) => r?.error);
    const answered = results.filter((r) => !r?.error && Array.isArray(r?.payload) && r.payload.length > 0);
    if (answered.length > 0) {
      if (keyErrors.length === 0) return { status: 'data' };
      return { status: 'data', failedRefIds: keyErrors.map((r) => String(r.query_key)), reason: String(keyErrors[0].error) };
    }
    if (keyErrors.length > 0) return { status: 'error', reason: String(keyErrors[0].error) };
    return { status: 'empty' };
  } catch (err: any) {
    return { status: 'error', reason: String(err?.message || err || '') };
  }
}

/** Checks every panel, a few at a time, keyed by panel id. */
export async function checkPanels(panels: Panel[], accountId: string, now: number = Date.now()): Promise<Record<number, PanelCheckResult>> {
  const results: Record<number, PanelCheckResult> = {};
  let next = 0;
  const worker = async (): Promise<void> => {
    while (next < panels.length) {
      const panel = panels[next++];
      results[panel.id] = await checkPanel(panel, accountId, now);
    }
  };
  await Promise.all(Array.from({ length: Math.min(CHECK_CONCURRENCY, panels.length) }, worker));
  return results;
}
