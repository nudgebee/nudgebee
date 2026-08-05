/**
 * Memory Module API client.
 *
 * Thin wrappers around the 9 verb-named RPC actions registered in
 * `app/src/lib/actions.yaml`:
 *
 *   - ai_memory_get              — single-record reads (soul, consent)
 *   - ai_memory_list             — collection reads (prefs, patterns,
 *                                  decisions, collective, session, events)
 *   - ai_memory_upsert           — insert-or-update writes
 *   - ai_memory_delete           — per-row deletes + (layer, scope) wipes,
 *                                  plus the GDPR cross-layer erase (layer=all)
 *   - ai_memory_create_decision  — click-wired user decisions
 *   - ai_memory_update_decision  — append-supersede / promote-to-global
 *   - ai_memory_update_pattern   — description edit + pin toggle
 *   - ai_memory_execute_maintenance — manual distillation job trigger
 *   - ai_memory_generate_export  — GDPR portability JSON bundle
 *
 * The wrappers translate the legacy per-(layer, scope, verb) call shape
 * (preserved as the exported API so UI callers don't change) into
 * { layer, scope, ... } payloads dispatched server-side in
 * llm/llm-server/api/memory_v2.go.
 *
 * Backend: nudgebee/llm/llm-server (Memory Module).
 * Spec:    nudgebee/llm/llm-server/docs/memory-ui-alignment.md
 */
import { queryGraphQL } from '@lib/HttpService';

type Scope = 'user' | 'tenant';

interface ActionResponse<T = unknown> {
  data: T | null;
  errors: unknown[];
}

async function call<T = unknown>(action: string, request: Record<string, unknown>): Promise<ActionResponse<T>> {
  const query = `
    query Memory_${action}($request: ${action}_request!) {
      ${action}(request: $request) {
        data
        errors
      }
    }
  `;
  try {
    const response = await queryGraphQL(query, `Memory_${action}`, { request });
    const root = response?.data?.data?.[action];
    return {
      data: (root?.data as T) ?? null,
      errors: root?.errors ?? response?.data?.errors ?? [],
    };
  } catch (error) {
    return { data: null, errors: [error] };
  }
}

// ── Soul ────────────────────────────────────────────────────────────────

interface SoulStyle {
  tone?: string;
  verbosity?: string;
  explainReasoning?: string;
  format?: { codeFirst?: boolean; tablesOverProse?: boolean; prefersDiagrams?: boolean };
}

export interface SoulRow {
  tenant_id: string;
  scope: Scope;
  user_id: string;
  style: SoulStyle;
  /**
   * Per-field provenance for user-scope rows (memory-ui-alignment.md §A.4).
   * Keys are top-level style field names; values are 'user' | 'agent'.
   * Tenant rows omit this map. UI uses it to compute the layer-level
   * Explicit / Inferred provenance chip.
   */
  sources?: Record<string, 'user' | 'agent'>;
  markdown?: string;
  created_by?: string;
  updated_by?: string;
}

export interface MemoryLayerResponse<T> {
  layer: string;
  entries: T[];
  total: number;
}

export const soulGet = (opts: { userId?: string; scope?: Scope } = {}) =>
  call<MemoryLayerResponse<SoulRow>>('ai_memory_get', {
    layer: 'soul',
    scope: opts.scope ?? 'user',
    user_id: opts.userId ?? '',
  });

export const soulSet = (opts: { userId?: string; style?: SoulStyle; markdown?: string; scope?: Scope }) =>
  call('ai_memory_upsert', {
    layer: 'soul',
    scope: opts.scope ?? 'user',
    user_id: opts.userId ?? '',
    style: opts.style,
    markdown: opts.markdown,
  });

export const soulClear = (opts: { userId?: string; scope?: Scope } = {}) =>
  call('ai_memory_delete', {
    layer: 'soul',
    scope: opts.scope ?? 'user',
    user_id: opts.userId ?? '',
  });

// ── Preferences ─────────────────────────────────────────────────────────

export interface PreferenceRow {
  id: string;
  tenant_id: string;
  scope: Scope;
  user_id: string;
  agent_module: string | null;
  key: string;
  value: unknown;
  previous_value?: unknown;
  source: 'explicit' | 'inferred';
  // Origin records how the row arrived and is never rewritten, so a kept
  // preference (source 'explicit', origin 'inferred') stays distinguishable
  // from one the user typed in Settings (origin absent).
  origin?: 'inferred';
  confirmed_at?: string;
  source_conversation_id?: string;
  source_session_id?: string;
  source_account_id?: string;
}

/** A preference the user accepted, as opposed to one still awaiting review. */
export const isKeptPreference = (row: PreferenceRow) => row.origin === 'inferred' && !!row.confirmed_at;

/** Every preference that came from inference, kept or not — the b-Cortex list. */
export const isInferredPreference = (row: PreferenceRow) => row.origin === 'inferred' || row.source === 'inferred';

export const prefsList = (opts: { userId?: string; agentModule?: string; scope?: Scope } = {}) =>
  call<MemoryLayerResponse<PreferenceRow>>('ai_memory_list', {
    layer: 'prefs',
    scope: opts.scope ?? 'user',
    user_id: opts.userId ?? '',
    agent_module: opts.agentModule ?? '',
  });

export const prefsSet = (opts: { userId?: string; key: string; value: unknown; agentModule?: string; scope?: Scope }) =>
  call('ai_memory_upsert', {
    layer: 'prefs',
    scope: opts.scope ?? 'user',
    user_id: opts.userId ?? '',
    key: opts.key,
    value: opts.value,
    agent_module: opts.agentModule ?? '',
  });

export const prefsClear = (opts: { userId?: string; key: string; agentModule?: string; scope?: Scope }) =>
  call('ai_memory_delete', {
    layer: 'prefs',
    scope: opts.scope ?? 'user',
    user_id: opts.userId ?? '',
    key: opts.key,
    agent_module: opts.agentModule ?? '',
  });

// Keep / Unkeep. Deliberately not prefsSet: accepting an inferred preference
// must retain its evidence and inferred origin so it stays reviewable and the
// decision can be taken back — prefsSet would strip both.
export const prefsConfirm = (opts: { userId?: string; key: string; agentModule?: string }) =>
  call('ai_memory_confirm', {
    layer: 'prefs',
    scope: 'user',
    user_id: opts.userId ?? '',
    key: opts.key,
    agent_module: opts.agentModule ?? '',
  });

export const prefsUnconfirm = (opts: { userId?: string; key: string; agentModule?: string }) =>
  call('ai_memory_unconfirm', {
    layer: 'prefs',
    scope: 'user',
    user_id: opts.userId ?? '',
    key: opts.key,
    agent_module: opts.agentModule ?? '',
  });

// ── Patterns ────────────────────────────────────────────────────────────

export interface PatternRow {
  id: string;
  tenant_id: string;
  scope: Scope;
  user_id: string;
  agent_module: string | null;
  pattern_kind: string;
  subject: string;
  description?: string;
  pinned: boolean;
  decay_state: 'active' | 'fading' | 'stale';
  source: 'explicit' | 'inferred';
  score: number;
  last_seen_at: string;
}

export const patternsList = (opts: { userId?: string; limit?: number; scope?: Scope } = {}) =>
  call<MemoryLayerResponse<PatternRow>>('ai_memory_list', {
    layer: 'patterns',
    scope: opts.scope ?? 'user',
    user_id: opts.userId ?? '',
    limit: opts.limit ?? 100,
  });

export const patternsUpdate = (opts: { userId?: string; id: string; description: string }) =>
  call('ai_memory_update_pattern', {
    user_id: opts.userId ?? '',
    id: opts.id,
    description: opts.description,
  });

export const patternsPin = (opts: { userId?: string; id: string; pinned: boolean }) =>
  call('ai_memory_update_pattern', {
    user_id: opts.userId ?? '',
    id: opts.id,
    pinned: opts.pinned,
  });

export const patternsDelete = (opts: { userId?: string; id: string }) =>
  call('ai_memory_delete', { layer: 'patterns', scope: 'user', user_id: opts.userId ?? '', id: opts.id });

// ── Decisions ───────────────────────────────────────────────────────────

export interface DecisionRow {
  id: string;
  tenant_id: string;
  scope: Scope;
  user_id: string;
  conversation_id?: string;
  agent_module?: string | null;
  decision_type: string;
  subject: string;
  rationale?: string;
  superseded: boolean;
  supersedes_id?: string;
  /** Set on tenant rows that were created via "Save to global" — points to the user row that was promoted. */
  promoted_from_id?: string;
  /** Derived. True on a Personal row when at least one tenant row has promoted_from_id pointing at it. */
  promoted_to_global?: boolean;
  /** Derived. First name of the row's creator (from public.users.display_name). Used by the Global view to render "Promoted by <first-name>" badges. */
  promoted_by_name?: string;
  /** Original creator — the user who clicked Save to global on a promoted tenant row, or the user who owns a personal row. */
  created_by?: string;
  decided_at: string;
}

export const decisionsList = (opts: { userId?: string; limit?: number; scope?: Scope; includeSuperseded?: boolean } = {}) =>
  call<MemoryLayerResponse<DecisionRow>>('ai_memory_list', {
    layer: 'decisions',
    scope: opts.scope ?? 'user',
    user_id: opts.userId ?? '',
    limit: opts.limit ?? 100,
    include_superseded: opts.includeSuperseded ?? false,
  });

/**
 * Records a decision. Callers that know their own subject (AutoPilot
 * approve/decline) pass `subject`. A vote on an answer passes `messageId` and
 * `accountId` instead: the server derives the subject from that answer, so the
 * decision names the root cause rather than the question that prompted it, and
 * re-voting the same message updates rather than appends.
 */
export const decisionsRecord = (opts: {
  userId?: string;
  decisionType: string;
  subject?: string;
  rationale?: string;
  messageId?: string;
  accountId?: string;
  conversationId?: string;
  agentModule?: string;
}) =>
  call('ai_memory_create_decision', {
    user_id: opts.userId ?? '',
    decision_type: opts.decisionType,
    subject: opts.subject ?? '',
    rationale: opts.rationale ?? '',
    message_id: opts.messageId ?? '',
    account_id: opts.accountId ?? '',
    conversation_id: opts.conversationId ?? '',
    agent_module: opts.agentModule ?? '',
  });

export const decisionsCorrect = (opts: { userId?: string; id: string; subject?: string; rationale?: string }) =>
  call('ai_memory_update_decision', {
    user_id: opts.userId ?? '',
    id: opts.id,
    subject: opts.subject,
    rationale: opts.rationale,
  });

export const decisionsPromoteToGlobal = (opts: { userId?: string; id: string }) =>
  call('ai_memory_update_decision', { user_id: opts.userId ?? '', id: opts.id, promote_to_global: true });

export const decisionsDelete = (opts: { userId?: string; id: string }) =>
  call('ai_memory_delete', { layer: 'decisions', scope: 'user', user_id: opts.userId ?? '', id: opts.id });

// ── Sessions ────────────────────────────────────────────────────────────

export interface SessionRow {
  session_id: string;
  conversation_id?: string;
  title?: string;
  source?: string;
  updated_at: string;
  working_memory?: unknown;
}

export const sessionsList = (opts: { userId?: string; limit?: number } = {}) =>
  call<MemoryLayerResponse<SessionRow>>('ai_memory_list', {
    layer: 'session',
    user_id: opts.userId ?? '',
    limit: opts.limit ?? 50,
  });

export const sessionDelete = (opts: { userId?: string; sessionId: string }) =>
  call('ai_memory_delete', {
    layer: 'session',
    user_id: opts.userId ?? '',
    session_id: opts.sessionId,
    mode: 'single',
  });

// ── Collective (tenant-wide; no Mine version) ───────────────────────────

export interface CollectiveRow {
  id: string;
  tenant_id: string;
  agent_module?: string | null;
  entry_kind: string;
  subject: string;
  body?: string;
  confidence?: number;
  status?: string;
}

export const collectiveList = (opts: { limit?: number; agentModule?: string } = {}) =>
  call<MemoryLayerResponse<CollectiveRow>>('ai_memory_list', {
    layer: 'collective',
    limit: opts.limit ?? 100,
    agent_module: opts.agentModule ?? '',
  });

export const collectiveSearch = (opts: { q: string; agentModule?: string; limit?: number }) =>
  call<{ entries: CollectiveRow[] }>('ai_memory_list', {
    layer: 'collective',
    q: opts.q,
    agent_module: opts.agentModule ?? '',
    limit: opts.limit ?? 50,
  });

export const collectiveDelete = (opts: { id: string }) => call('ai_memory_delete', { layer: 'collective', id: opts.id });

// ── Consent ─────────────────────────────────────────────────────────────

export interface ConsentToggle {
  tenant_id: string;
  user_id: string;
  layer: string;
  enabled: boolean;
  updated_at: string;
  updated_by?: string;
  updated_by_name?: string;
}

export const consentGet = (opts: { userId?: string } = {}) =>
  call<{ toggles: ConsentToggle[] }>('ai_memory_get', { layer: 'consent', scope: 'user', user_id: opts.userId ?? '' });

export const consentSet = (opts: { userId?: string; layer: string; enabled: boolean }) =>
  call('ai_memory_upsert', {
    layer: 'consent',
    scope: 'user',
    user_id: opts.userId ?? '',
    consent_layer: opts.layer,
    enabled: opts.enabled,
  });

export const tenantConsentGet = () => call<{ toggles: ConsentToggle[] }>('ai_memory_get', { layer: 'consent', scope: 'tenant' });

export const tenantConsentSet = (opts: { layer: string; enabled: boolean }) =>
  call('ai_memory_upsert', { layer: 'consent', scope: 'tenant', consent_layer: opts.layer, enabled: opts.enabled });

// ── Erase / Export (admin) ──────────────────────────────────────────────

export const memoryErase = (opts: { userId?: string } = {}) => call('ai_memory_delete', { layer: 'all', scope: 'user', user_id: opts.userId ?? '' });

export const memoryExport = (opts: { userId?: string; format?: string } = {}) =>
  call<{ format: string; data: string }>('ai_memory_generate_export', {
    user_id: opts.userId ?? '',
    format: opts.format ?? 'json',
  });
