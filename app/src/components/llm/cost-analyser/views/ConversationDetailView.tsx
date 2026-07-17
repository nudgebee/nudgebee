/**
 * Screen 3 — Conversation Detail (spec §6). Where did this conversation's money
 * and time go? Header summary card, then a tabbed body:
 *   - Sub-tasks & model calls — step → model-call drill-down
 *   - Conversation metrics    — legacy usage-metrics panel
 *   - Details                 — trace waterfall + cost composition
 */
import * as React from 'react';
import { Box, CircularProgress, Collapse, Drawer } from '@mui/material';
import CloseIcon from '@mui/icons-material/Close';
import FullscreenIcon from '@mui/icons-material/Fullscreen';
import FullscreenExitIcon from '@mui/icons-material/FullscreenExit';
import ExpandMoreIcon from '@mui/icons-material/ExpandMore';
import ChevronRightIcon from '@mui/icons-material/ChevronRight';
import FormatListBulletedIcon from '@mui/icons-material/FormatListBulleted';
import PaidOutlinedIcon from '@mui/icons-material/PaidOutlined';
import TimerOutlinedIcon from '@mui/icons-material/TimerOutlined';
import ErrorOutlineIcon from '@mui/icons-material/ErrorOutline';
import OpenInNewOutlinedIcon from '@mui/icons-material/OpenInNewOutlined';
import AutoAwesomeOutlinedIcon from '@mui/icons-material/AutoAwesomeOutlined';
import { Button } from '@ui/Button';
import { Banner } from '@ui/Banner';
import { Modal } from '@ui/Modal';
import { Card } from '@ui/Card';
import { CostCallout } from '@ui/CostCallout';
import { Chip } from '@ui/Chip';
import { Label } from '@ui/Label';
import { ToggleGroup } from '@ui/ToggleGroup';
import { EmptyState } from '@ui/EmptyState';
import CustomTable2 from '@shared/tables/CustomTable';
import CustomTabs from '@shared/navigation/Tabs';
import TraceWaterfall from '../components/TraceWaterfall';
import CostTreemap from '../components/CostTreemap';
import ConversationUsagePanel from '../components/ConversationUsagePanel';
import { MODEL_HUE } from '../components/palette';
import HeaderLabel from '../components/HeaderLabel';
import { makeSeverity, SeverityCell } from '../components/severity';
import { fmtCost, fmtDuration, fmtTokens, runModelBreakdown, triggerLabel } from '../format';
import { adaptAgentDetail, type AgentDetail } from '../adapt';
import {
  getConversationAgent,
  generateConversationOptimization,
  getStoredConversationOptimization,
  analyzePromptTrace,
  getStoredPromptAnalysis,
  type ConversationUsageSummary,
  type ConversationOptimization,
  type OptFinding,
  type OptExemplar,
  type PromptAnalysis,
  type PromptComponent,
  type PromptOptimization,
} from '@api1/ai-cost';
import type { ModelCall, Run, RunStatus, Step, StepToolCall } from '../types';

interface ConversationDetailViewProps {
  run: Run | null;
  loading: boolean;
  error: string | null;
  /** Legacy per-conversation summary for the "basic summary" panel (may be null). */
  usage?: ConversationUsageSummary | null;
  /** session_id of the open conversation — needed to lazy-fetch per-agent detail. */
  conversationId?: string;
  /** account scope for the per-agent detail fetch. */
  accountId?: string;
  onBack: () => void;
  /** Hide the "← Back to conversations" bar (e.g. when shown inside a Modal that has its own close). */
  hideBackBar?: boolean;
  /** When set (cross-link from the Agents tab), focus the Sub-tasks tab on this agent invocation. */
  initialAgentId?: string;
  /** Which tab to open on (default 'subtasks'). The "Analyse" action opens 'optimize'. */
  initialTab?: 'subtasks' | 'optimize';
  /** When opened via "Analyse": auto-run the optimization if there's no cached result. */
  autoRunOptimize?: boolean;
}

type DetailTabId = 'subtasks' | 'metrics' | 'details' | 'optimize';

function BackBar({ onBack }: { onBack: () => void }) {
  return (
    <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)' }}>
      <Button tone='link' size='sm' onClick={onBack} id='cost-detail-back'>
        ← Back to conversations
      </Button>
    </Box>
  );
}

const STATUS_TONE: Record<RunStatus, 'success' | 'critical' | 'warning' | 'neutral'> = {
  completed: 'success',
  failed: 'critical',
  'awaiting-approval': 'warning',
  cancelled: 'neutral',
};

function SummaryStat({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: '2px', minWidth: 110 }}>
      <Box sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>{label}</Box>
      <Box sx={{ fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-700)', fontWeight: 'var(--ds-font-weight-medium)' }}>{children}</Box>
    </Box>
  );
}

function SectionTitle({ children }: { children: React.ReactNode }) {
  return (
    <Box
      sx={{ fontSize: 'var(--ds-text-body-lg)', fontWeight: 'var(--ds-font-weight-semibold)', color: 'var(--ds-gray-700)', mb: 'var(--ds-space-3)' }}
    >
      {children}
    </Box>
  );
}

/** Latency vs wait-time split bar (spec §6a / §2.2). */
function TimeSplitBar({ run }: { run: Run }) {
  const toolTime = run.steps.reduce((a, s) => a + s.toolTimeMs, 0);
  const overhead = Math.max(0, run.wallClockMs - run.totalModelLatencyMs - toolTime - run.totalWaitTimeMs);
  const segs = [
    { label: 'Model latency', ms: run.totalModelLatencyMs, color: 'var(--ds-blue-400)' },
    { label: 'Tool time', ms: toolTime, color: 'var(--ds-gray-400)' },
    { label: 'Wait (approval)', ms: run.totalWaitTimeMs, color: 'var(--ds-amber-400)' },
    { label: 'Overhead', ms: overhead, color: 'var(--ds-gray-300)' },
  ].filter((s) => s.ms > 0);
  const total = run.wallClockMs || 1;

  return (
    <Box>
      <Box sx={{ display: 'flex', height: 14, borderRadius: 'var(--ds-radius-pill)', overflow: 'hidden', border: '1px solid var(--ds-gray-200)' }}>
        {segs.map((s) => (
          <Box key={s.label} sx={{ width: `${(s.ms / total) * 100}%`, backgroundColor: s.color }} title={`${s.label} · ${fmtDuration(s.ms)}`} />
        ))}
      </Box>
      <Box
        sx={{
          display: 'flex',
          flexWrap: 'wrap',
          gap: 'var(--ds-space-3)',
          mt: 'var(--ds-space-2)',
          fontSize: 'var(--ds-text-caption)',
          color: 'var(--ds-gray-600)',
        }}
      >
        {segs.map((s) => (
          <Box key={s.label} sx={{ display: 'inline-flex', alignItems: 'center', gap: 'var(--ds-space-1)' }}>
            <Box sx={{ width: 9, height: 9, borderRadius: 2, backgroundColor: s.color }} />
            {s.label} · {fmtDuration(s.ms)}
          </Box>
        ))}
      </Box>
    </Box>
  );
}

const cellNum = { fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-700)', fontVariantNumeric: 'tabular-nums' } as const;

/** Hover tooltip text justifying a model call's cost from its component split. */
function costBreakdownTitle(c: ModelCall): string | undefined {
  const bd = c.costBreakdown;
  if (!bd?.components?.length) return undefined;
  const lines = bd.components.map((x) => `${x.kind}: ${fmtTokens(x.tokens)} tok → $${x.cost_usd.toFixed(4)}`);
  return `Cost breakdown (tier: ${bd.tier})\n${lines.join('\n')}`;
}

// Header name → sortable value for the model-call table.
const MODEL_CALL_SORT: Record<string, (c: ModelCall) => number | string> = {
  Model: (c) => c.model,
  'In token': (c) => c.inputTokens,
  'Out token': (c) => c.outputTokens,
  Cached: (c) => c.cachedInputTokens ?? 0,
  'Thinking token': (c) => c.thinkingTokens ?? 0,
  TTFT: (c) => c.ttftMs ?? 0,
  Cost: (c) => c.totalCost,
  Latency: (c) => c.latencyMs,
};

/** One parsed prompt message (role + flattened text) for the trace modal. */
interface ParsedPromptMessage {
  role: string;
  text: string;
}

/** Flatten one message "part" to a string. Our trace serializes text parts with
 * `text` and tool parts with `name`/`content`; we also tolerate string parts and
 * arbitrary objects (stringified) so a part is NEVER rendered as a raw object. */
function partToText(p: unknown): string {
  if (typeof p === 'string') return p;
  if (p && typeof p === 'object') {
    const o = p as { text?: string; name?: string; content?: string };
    if (typeof o.text === 'string' && o.text) return o.text;
    if (o.name || o.content) return [o.name, o.content].filter(Boolean).join(': ');
    return JSON.stringify(p);
  }
  return p == null ? '' : String(p);
}

/** Parse the stored `prompt_messages` JSON (`[{role, parts:[{type,text|name|content}]}]`)
 * into readable role/text blocks. `text` is ALWAYS a string — tolerates array /
 * object `content` (other providers), string parts, and malformed JSON (shown raw)
 * so the modal can never render a non-string child. */
function parsePromptMessages(raw: string): ParsedPromptMessage[] {
  if (!raw) return [];
  try {
    const arr = JSON.parse(raw);
    if (!Array.isArray(arr)) return [{ role: '', text: raw }];
    return arr.map((m: { role?: string; parts?: unknown[]; content?: unknown }) => {
      let text = '';
      if (Array.isArray(m?.parts)) text = m.parts.map(partToText).join('');
      else if (Array.isArray(m?.content)) text = (m.content as unknown[]).map(partToText).join('');
      else if (m?.content != null) text = typeof m.content === 'string' ? m.content : JSON.stringify(m.content);
      else text = typeof m === 'string' ? m : '';
      return { role: m?.role || '', text };
    });
  } catch {
    return [{ role: '', text: raw }];
  }
}

const traceFetchState = { loading: true, error: null as string | null, prompt: '', response: '' };
type TraceFetchState = typeof traceFetchState;

const preBox = {
  fontFamily: 'var(--ds-font-mono, monospace)',
  fontSize: 'var(--ds-text-caption)',
  color: 'var(--ds-gray-700)',
  whiteSpace: 'pre-wrap' as const,
  wordBreak: 'break-word' as const,
  backgroundColor: 'var(--ds-background-200)',
  borderRadius: 'var(--ds-radius-md)',
  padding: 'var(--ds-space-3)',
  maxHeight: '40vh',
  overflowY: 'auto' as const,
};

// Single-block tabs (Raw, Response) show one section at a time, so they use most
// of the modal's height (header + tabs take the rest) instead of the 40vh card cap.
const preBoxTall = { ...preBox, maxHeight: '66vh' };

/** Small copy-to-clipboard action with brief "Copied" feedback. Renders nothing
 * when there's no text to copy. */
function CopyButton({ text }: { text: string }) {
  const [copied, setCopied] = React.useState(false);
  if (!text) return null;
  const onCopy = () => {
    navigator.clipboard?.writeText(text).then(
      () => {
        setCopied(true);
        window.setTimeout(() => setCopied(false), 1500);
      },
      () => {}
    );
  };
  return (
    <Button tone='link' size='sm' onClick={onCopy} aria-label='Copy to clipboard'>
      {copied ? 'Copied' : 'Copy'}
    </Button>
  );
}

/** role → display label, Chip tone, and left-accent colour for message cards. */
const ROLE_META: Record<string, { label: string; tone: 'neutral' | 'info' | 'success' | 'warning'; accent: string }> = {
  system: { label: 'System', tone: 'neutral', accent: 'var(--ds-gray-400)' },
  human: { label: 'Human', tone: 'info', accent: 'var(--ds-blue-500)' },
  user: { label: 'Human', tone: 'info', accent: 'var(--ds-blue-500)' },
  ai: { label: 'Assistant', tone: 'success', accent: 'var(--ds-green-500)' },
  assistant: { label: 'Assistant', tone: 'success', accent: 'var(--ds-green-500)' },
  tool: { label: 'Tool', tone: 'warning', accent: 'var(--ds-amber-500)' },
};
function roleMeta(role: string) {
  return ROLE_META[(role || '').toLowerCase()] ?? { label: role || 'Message', tone: 'neutral' as const, accent: 'var(--ds-gray-300)' };
}

// TextEncoder is SSR-safe (global in modern Node + browsers) and avoids allocating
// a Blob per call; reuse one instance for the UTF-8 byte count.
const utf8Encoder = new TextEncoder();
const byteSize = (s: string): number => utf8Encoder.encode(s).length;
const sizeLabel = (s: string): string => {
  const b = byteSize(s);
  return b >= 1024 ? `${(b / 1024).toFixed(1)} KB` : `${b} B`;
};
const lineCount = (s: string): number => (s ? s.split('\n').length : 0);
// Format an already-known byte count (vs sizeLabel, which measures a string).
const fmtBytes = (b: number): string => (b >= 1024 ? `${(b / 1024).toFixed(1)} KB` : `${b} B`);

/** Compact "label value" metadata pair for the trace header strip. */
function MetaChip({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <Box sx={{ display: 'inline-flex', alignItems: 'baseline', gap: '4px', fontSize: 'var(--ds-text-caption)' }}>
      <Box component='span' sx={{ color: 'var(--ds-gray-500)' }}>
        {label}
      </Box>
      <Box component='span' sx={{ color: 'var(--ds-gray-700)', fontVariantNumeric: 'tabular-nums' }}>
        {value}
      </Box>
    </Box>
  );
}

/** One prompt message as a role-coded card: accent border, role badge, size/lines
 * metadata, a relative size bar (where the bytes go), and a per-message copy. */
function MessageCard({ m, maxBytes }: { m: ParsedPromptMessage; maxBytes: number }) {
  const meta = roleMeta(m.role);
  const pct = maxBytes > 0 ? Math.max(2, Math.round((byteSize(m.text) / maxBytes) * 100)) : 0;
  return (
    <Box
      sx={{
        borderLeft: `3px solid ${meta.accent}`,
        borderRadius: 'var(--ds-radius-md)',
        backgroundColor: 'var(--ds-background-200)',
        overflow: 'hidden',
      }}
    >
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)', px: 'var(--ds-space-3)', py: 'var(--ds-space-2)' }}>
        <Chip size='2xs' variant='tag' tone={meta.tone}>
          {meta.label}
        </Chip>
        <Box sx={{ flex: 1 }} />
        <Box component='span' sx={{ fontSize: '10px', color: 'var(--ds-gray-500)', fontVariantNumeric: 'tabular-nums' }}>
          {sizeLabel(m.text)} · {lineCount(m.text)} lines
        </Box>
        <CopyButton text={m.text} />
      </Box>
      <Box sx={{ height: 3, backgroundColor: 'var(--ds-background-300)' }}>
        <Box sx={{ width: `${pct}%`, height: '100%', backgroundColor: meta.accent, opacity: 0.7 }} />
      </Box>
      <Box sx={{ ...preBox, borderRadius: 0, backgroundColor: 'transparent', maxHeight: '32vh' }}>
        {m.text || (
          <Box component='span' sx={{ color: 'var(--ds-gray-400)' }}>
            (empty)
          </Box>
        )}
      </Box>
    </Box>
  );
}

/** Heuristic: split a system prompt into sections on markdown headings (# .. ######).
 * Returns a single untitled section when there are no headings (nothing to split). */
function splitSections(text: string): { title: string; body: string }[] {
  const headingRe = /^\s{0,3}(#{1,6})\s+(.+?)\s*#*\s*$/;
  const out: { title: string; body: string[] }[] = [];
  let cur: { title: string; body: string[] } | null = null;
  for (const line of text.split('\n')) {
    const m = headingRe.exec(line);
    if (m) {
      if (cur) out.push(cur);
      cur = { title: m[2].trim(), body: [] };
    } else {
      if (!cur) cur = { title: '', body: [] };
      cur.body.push(line);
    }
  }
  if (cur) out.push(cur);
  return out.map((s) => ({ title: s.title, body: s.body.join('\n').trim() })).filter((s) => s.title || s.body);
}

/** Collapsible section card for the System tab: heading + size/lines + copy, body
 * in a scroll box. */
const SectionCard = React.memo(function SectionCard({ title, body, defaultOpen }: { title: string; body: string; defaultOpen: boolean }) {
  const [open, setOpen] = React.useState(defaultOpen);
  return (
    <Box sx={{ border: '1px solid var(--ds-gray-200)', borderRadius: 'var(--ds-radius-md)', overflow: 'hidden' }}>
      <Box
        onClick={() => setOpen((o) => !o)}
        sx={{
          display: 'flex',
          alignItems: 'center',
          gap: 'var(--ds-space-2)',
          px: 'var(--ds-space-3)',
          py: 'var(--ds-space-2)',
          cursor: 'pointer',
          backgroundColor: 'var(--ds-background-200)',
        }}
      >
        {open ? (
          <ExpandMoreIcon sx={{ fontSize: 16, color: 'var(--ds-gray-500)' }} />
        ) : (
          <ChevronRightIcon sx={{ fontSize: 16, color: 'var(--ds-gray-500)' }} />
        )}
        <Box
          component='span'
          sx={{ flex: 1, fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-medium)', color: 'var(--ds-gray-700)' }}
        >
          {title || 'Section'}
        </Box>
        <Box component='span' sx={{ fontSize: '10px', color: 'var(--ds-gray-500)', fontVariantNumeric: 'tabular-nums' }}>
          {sizeLabel(body)} · {lineCount(body)} lines
        </Box>
        <CopyButton text={body} />
      </Box>
      <Collapse in={open}>
        <Box sx={{ ...preBox, borderRadius: 0, maxHeight: '52vh' }}>
          {body || (
            <Box component='span' sx={{ color: 'var(--ds-gray-400)' }}>
              (empty)
            </Box>
          )}
        </Box>
      </Collapse>
    </Box>
  );
});

/** System tab: sub-tabs per system message (when >1), each split into collapsible
 * section cards by heading. Falls back to a single scroll block when a message has
 * no detectable headings. */
function SystemTab({ msgs }: { msgs: ParsedPromptMessage[] }) {
  const [active, setActive] = React.useState(0);
  const idx = Math.min(active, Math.max(0, msgs.length - 1));
  const text = msgs[idx]?.text ?? '';
  const sections = React.useMemo(() => splitSections(text), [text]);
  if (!msgs.length) {
    return <Box sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>No system prompt captured.</Box>;
  }
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-2)' }}>
      {msgs.length > 1 && (
        <ToggleGroup
          selection='single'
          size='sm'
          ariaLabel='System message'
          value={String(idx)}
          onChange={(v) => setActive(Number(v))}
          options={msgs.map((_, i) => ({ value: String(i), label: `System ${i + 1}` }))}
        />
      )}
      {sections.length > 1 ? (
        sections.map((s, i) => <SectionCard key={i} title={s.title} body={s.body} defaultOpen={sections.length <= 3} />)
      ) : (
        <Box sx={preBoxTall}>{text || 'No system prompt captured.'}</Box>
      )}
    </Box>
  );
}

// ─── Prompt analysis ("Analyze" action) ────────────────────────────────────
// Follows the "Analyze & Optimize an LLM Prompt for Token Cost" methodology. The
// FRONTEND owns measurement: it builds and measures the component tree from the real
// prompt (a macro node per message → section leaves), and the header total uses the
// call's ACTUAL billed tokens — the model never defines sizes (it can't reproduce a
// large prompt verbatim, which would collapse the totals). The model only classifies
// each component by id (static/dynamic) and returns the qualitative judgment: cache
// verdict, declared-vs-used dead weight, and ranked optimizations. Phase model mirrors
// the Optimize tab: init = cheap stored read (no LLM), idle, loading, data/error.
type PromptAnalysisPhase = 'init' | 'idle' | 'loading' | 'data' | 'error';

interface PromptAnalysisState {
  phase: PromptAnalysisPhase;
  error: string | null;
  data: PromptAnalysis | null;
}

interface MeasuredTree {
  components: PromptComponent[];
  serialized: string; // the id|parent|name|role|loc|size|tokens|%|preview table for the LLM
  totalBytes: number;
  totalTokens: number; // chars ÷ 4 text estimate (header shows billed tokens instead)
}

/** Build and MEASURE the component tree from the real prompt — the source of truth.
 * One macro node per message, split on markdown headings (splitSections) into section
 * leaves. Send order (message, then section) is preserved for the cache-prefix check;
 * leaves carry the real content; macros roll up their children. Exact bytes/lines and
 * ~tokens (chars ÷ 4). Also emits the compact table (with content previews) the model
 * classifies by id. */
function measurePromptComponents(messages: ParsedPromptMessage[]): MeasuredTree {
  const components: PromptComponent[] = [];
  const measure = (text: string) => ({ size: byteSize(text), loc: lineCount(text), tokens: Math.round(text.length / 4) });

  messages.forEach((m, i) => {
    const role = m.role || 'message';
    const label = roleMeta(role).label;
    const macroId = `${i + 1}`;
    const sections = splitSections(m.text).filter((s) => s.title || s.body);
    if (sections.length > 1) {
      // Macro group + one leaf per section.
      components.push({ id: macroId, name: `${label} #${i + 1}`, role, loc: 0, size: 0, tokens: 0, pct: 0 });
      sections.forEach((s, j) => {
        const text = s.title ? `${s.title}\n${s.body}` : s.body;
        components.push({
          id: `${macroId}.${j + 1}`,
          parent: macroId,
          name: s.title || `part ${j + 1}`,
          role,
          ...measure(text),
          pct: 0,
          content: text,
        });
      });
    } else {
      // Single leaf for the whole message.
      const text = sections[0] ? (sections[0].title ? `${sections[0].title}\n${sections[0].body}` : sections[0].body) : m.text;
      components.push({ id: macroId, name: `${label} #${i + 1}`, role, ...measure(text), pct: 0, content: text });
    }
  });

  // Roll macros up from their children, then compute the leaf-token total + per-node %.
  const childrenOf = new Map<string, PromptComponent[]>();
  for (const c of components) {
    if (c.parent) {
      if (!childrenOf.has(c.parent)) childrenOf.set(c.parent, []);
      childrenOf.get(c.parent)!.push(c);
    }
  }
  const isLeaf = (c: PromptComponent) => !childrenOf.has(c.id);
  for (const c of components) {
    if (!isLeaf(c)) {
      const kids = childrenOf.get(c.id)!;
      c.size = kids.reduce((a, k) => a + k.size, 0);
      c.loc = kids.reduce((a, k) => a + k.loc, 0);
      c.tokens = kids.reduce((a, k) => a + k.tokens, 0);
    }
  }
  const totalTokens = components.filter(isLeaf).reduce((a, c) => a + c.tokens, 0) || 1;
  const totalBytes = components.filter(isLeaf).reduce((a, c) => a + c.size, 0);
  for (const c of components) c.pct = (c.tokens / totalTokens) * 100;

  const preview = (s?: string) => (s ? `"${s.replace(/\s+/g, ' ').trim().slice(0, 160)}${s.length > 160 ? '…' : ''}"` : '(group)');
  const serialized = components
    .map(
      (c) =>
        `${c.id} | parent=${c.parent || '-'} | ${c.name} | role=${c.role} | loc=${c.loc} | size=${c.size}B | tokens≈${c.tokens} | ${c.pct.toFixed(
          1
        )}% | ${preview(c.content)}`
    )
    .join('\n');
  return { components, serialized, totalBytes, totalTokens };
}

/** Merge the model's per-id classifications/notes onto the frontend-measured tree. */
function applyClassifications(components: PromptComponent[], classifications?: PromptAnalysis['classifications']): PromptComponent[] {
  if (!classifications) return components;
  return components.map((c) => {
    const ann = classifications[c.id];
    if (!ann) return c;
    if (typeof ann === 'string') return { ...c, classification: ann };
    return { ...c, classification: ann.classification ?? c.classification, note: ann.note ?? c.note };
  });
}

interface PromptAnalysisInput {
  callId?: string;
  accountId?: string;
  promptJson: string;
  measured: string;
  responseContent: string;
}

function usePromptAnalysis(input: PromptAnalysisInput): PromptAnalysisState & { run: () => void } {
  const { callId, accountId, promptJson, measured, responseContent } = input;
  const [state, setState] = React.useState<PromptAnalysisState>({ phase: 'init', error: null, data: null });
  const ctrlRef = React.useRef<AbortController | null>(null);

  // Cheap cached-result read on open / call change (no LLM).
  React.useEffect(() => {
    if (!callId || !accountId) {
      setState({ phase: 'idle', error: null, data: null });
      return;
    }
    const controller = new AbortController();
    let cancelled = false;
    setState({ phase: 'init', error: null, data: null });
    getStoredPromptAnalysis({ accountId, callId }, controller.signal)
      .then((cached) => {
        if (cancelled) return;
        if (cached) setState({ phase: 'data', error: null, data: cached.analysis });
        else setState({ phase: 'idle', error: null, data: null });
      })
      .catch(() => {
        if (!cancelled) setState({ phase: 'idle', error: null, data: null }); // read failure → offer to analyze
      });
    return () => {
      cancelled = true;
      controller.abort();
    };
  }, [callId, accountId]);

  const run = React.useCallback(() => {
    if (!callId || !accountId || !promptJson) return;
    ctrlRef.current?.abort();
    const controller = new AbortController();
    ctrlRef.current = controller;
    setState((s) => ({ ...s, phase: 'loading', error: null }));
    analyzePromptTrace({ accountId, callId, promptJson, measured, responseContent }, controller.signal)
      .then((d) => {
        if (controller.signal.aborted) return;
        if (d) setState({ phase: 'data', error: null, data: d });
        else setState({ phase: 'error', error: 'The analysis response could not be parsed', data: null });
      })
      .catch((e) => {
        if (controller.signal.aborted) return;
        setState({ phase: 'error', error: e instanceof Error ? e.message : 'Failed to analyze', data: null });
      });
  }, [callId, accountId, promptJson, measured, responseContent]);

  React.useEffect(() => () => ctrlRef.current?.abort(), []);

  return { ...state, run };
}

// static = byte-identical every call (cacheable, good); dynamic = varies; mixed = both.
const CLASSIFICATION_TONE: Record<string, 'success' | 'warning' | 'neutral'> = {
  static: 'success',
  dynamic: 'warning',
  mixed: 'neutral',
};
// The three levers, in priority order, each with a distinct hue for its chip.
const LEVER_META: Record<string, { label: string; hue: 'blue' | 'violet' | 'teal' }> = {
  count: { label: 'token count', hue: 'blue' },
  cache: { label: 'cache rate', hue: 'violet' },
  relevance: { label: 'relevance', hue: 'teal' },
};
// Implementation effort: low = quick text edit/reorder, high = cross-agent code change.
const EFFORT_TONE: Record<string, 'success' | 'warning' | 'critical' | 'neutral'> = {
  low: 'success',
  medium: 'warning',
  high: 'critical',
};

const numCell = { fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-700)', fontVariantNumeric: 'tabular-nums' } as const;

// Index the flat component list into a parent→children tree (children keep their
// returned order) plus an id→component lookup for resolving parent names.
function buildComponentTree(components: PromptComponent[]): {
  childrenOf: Map<string, PromptComponent[]>;
  byId: Map<string, PromptComponent>;
  roots: PromptComponent[];
} {
  const childrenOf = new Map<string, PromptComponent[]>();
  const byId = new Map<string, PromptComponent>();
  for (const c of components) byId.set(c.id, c);
  for (const c of components) {
    const p = c.parent && byId.has(c.parent) ? c.parent : '';
    if (!childrenOf.has(p)) childrenOf.set(p, []);
    childrenOf.get(p)!.push(c);
  }
  const roots = childrenOf.get('') ?? [];
  return { childrenOf, byId, roots };
}

/** One node of the component tree as an accordion. Macro nodes group sub-components
 * (rendered nested + indented); the toggle reveals the metadata grid and, for leaf
 * nodes, the verbatim content as a copyable JSON block (component / parent / loc /
 * size / content). */
const ComponentNode = React.memo(function ComponentNode({
  c,
  childrenOf,
  byId,
  depth,
  defaultOpen,
}: {
  c: PromptComponent;
  childrenOf: Map<string, PromptComponent[]>;
  byId: Map<string, PromptComponent>;
  depth: number;
  defaultOpen?: boolean;
}) {
  const [open, setOpen] = React.useState(!!defaultOpen);
  const kids = childrenOf.get(c.id) ?? [];
  const isGroup = kids.length > 0;
  const tone = CLASSIFICATION_TONE[(c.classification || '').toLowerCase()] ?? 'neutral';
  const bar = Math.min(100, Math.max(2, Math.round(c.pct)));
  const parentName = c.parent ? byId.get(c.parent)?.name : undefined;
  // The "json which shows the content of the component" — component / parent / loc / size / content.
  const contentJson = React.useMemo(
    () =>
      JSON.stringify(
        { component: c.name, parent: parentName ?? null, loc: c.loc, size: fmtBytes(c.size), tokens: c.tokens, content: c.content ?? '' },
        null,
        2
      ),
    [c.name, parentName, c.loc, c.size, c.tokens, c.content]
  );
  return (
    <Box sx={{ borderTop: depth === 0 ? '1px solid var(--ds-gray-200)' : 'none' }}>
      <Box
        onClick={() => setOpen((o) => !o)}
        role='button'
        aria-expanded={open}
        sx={{
          display: 'flex',
          flexDirection: 'column',
          gap: '4px',
          py: 'var(--ds-space-2)',
          pl: `calc(${depth} * var(--ds-space-4))`,
          cursor: 'pointer',
          '&:hover': { backgroundColor: 'var(--ds-background-200)' },
        }}
      >
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)', flexWrap: 'wrap' }}>
          {open ? (
            <ExpandMoreIcon sx={{ fontSize: 16, color: 'var(--ds-gray-500)' }} />
          ) : (
            <ChevronRightIcon sx={{ fontSize: 16, color: 'var(--ds-gray-500)' }} />
          )}
          <Box
            component='span'
            sx={{
              fontSize: 'var(--ds-text-caption)',
              fontWeight: depth === 0 ? 'var(--ds-font-weight-semibold)' : 'var(--ds-font-weight-medium)',
              color: 'var(--ds-gray-700)',
            }}
          >
            {c.name}
          </Box>
          {isGroup && (
            <Chip size='2xs' variant='tag' hue='slate'>
              {kids.length}
            </Chip>
          )}
          {c.classification && (
            <Chip size='2xs' variant='tag' tone={tone}>
              {c.classification}
            </Chip>
          )}
          <Box sx={{ flex: 1 }} />
          <Box component='span' sx={numCell}>
            {fmtTokens(c.tokens)} tok · {Math.round(c.pct)}%
          </Box>
          <Box component='span' sx={{ ...numCell, color: 'var(--ds-gray-500)' }}>
            {fmtBytes(c.size)} · {c.loc} LOC
          </Box>
        </Box>
        <Box sx={{ height: 4, backgroundColor: 'var(--ds-background-300)', borderRadius: 2, ml: 'calc(16px + var(--ds-space-2))' }}>
          <Box sx={{ width: `${bar}%`, height: '100%', backgroundColor: 'var(--ds-blue-400)', borderRadius: 2, opacity: 0.7 }} />
        </Box>
      </Box>
      <Collapse in={open}>
        <Box sx={{ pl: `calc(${depth} * var(--ds-space-4))` }}>
          <Box
            sx={{
              ml: 'calc(16px + var(--ds-space-2))',
              mb: 'var(--ds-space-2)',
              p: 'var(--ds-space-3)',
              backgroundColor: 'var(--ds-background-200)',
              borderRadius: 'var(--ds-radius-md)',
              display: 'flex',
              flexDirection: 'column',
              gap: 'var(--ds-space-2)',
            }}
          >
            <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--ds-space-2) var(--ds-space-4)' }}>
              <MetaChip label='Component' value={c.name} />
              <MetaChip label='Parent' value={parentName ?? '—'} />
              {c.role && <MetaChip label='Role' value={c.role} />}
              {c.classification && <MetaChip label='Class' value={c.classification} />}
              <MetaChip label='LOC' value={c.loc} />
              <MetaChip label='Size' value={fmtBytes(c.size)} />
              <MetaChip label='Tokens' value={`~${fmtTokens(c.tokens)}`} />
              <MetaChip label='Share' value={`${Math.round(c.pct)}%`} />
            </Box>
            {c.note && <Box sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-700)' }}>{c.note}</Box>}
            {isGroup ? (
              <Box sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-400)' }}>
                Macro group of {kids.length} sub-component{kids.length === 1 ? '' : 's'} — content lives in the children below.
              </Box>
            ) : (
              <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-1)' }}>
                <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
                  <Box component='span' sx={{ fontSize: '10px', color: 'var(--ds-gray-500)', textTransform: 'uppercase', letterSpacing: '0.04em' }}>
                    content (json)
                  </Box>
                  <CopyButton text={contentJson} />
                </Box>
                <Box sx={{ ...preBox, maxHeight: '32vh' }}>{contentJson}</Box>
              </Box>
            )}
          </Box>
          {kids.map((k) => (
            <ComponentNode key={k.id} c={k} childrenOf={childrenOf} byId={byId} depth={depth + 1} defaultOpen={false} />
          ))}
        </Box>
      </Collapse>
    </Box>
  );
});

/** One ranked optimization: lever + technique chips, projected saving, the explicit
 * accuracy-impact note (always shown — the methodology's core guardrail), and detail. */
function OptimizationCard({ o }: { o: PromptOptimization }) {
  const lever = LEVER_META[(o.lever || '').toLowerCase()];
  return (
    <Box
      sx={{ display: 'flex', alignItems: 'flex-start', gap: 'var(--ds-space-3)', py: 'var(--ds-space-3)', borderTop: '1px solid var(--ds-gray-200)' }}
    >
      <Box sx={{ minWidth: 96 }}>
        <Box
          sx={{
            fontSize: 'var(--ds-text-body)',
            fontWeight: 'var(--ds-font-weight-semibold)',
            color: 'var(--ds-green-700)',
            fontVariantNumeric: 'tabular-nums',
          }}
        >
          −{fmtTokens(o.projected_token_saving)} tok
        </Box>
        {o.projected_cost_saving_pct != null && o.projected_cost_saving_pct > 0 && (
          <Box sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>~{Math.round(o.projected_cost_saving_pct)}% cost</Box>
        )}
      </Box>
      <Box sx={{ flex: 1, display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-1)', minWidth: 0 }}>
        <Box sx={{ display: 'flex', gap: 'var(--ds-space-2)', alignItems: 'center', flexWrap: 'wrap' }}>
          {lever && (
            <Chip size='2xs' variant='tag' hue={lever.hue}>
              {lever.label}
            </Chip>
          )}
          {o.technique && (
            <Chip size='2xs' variant='tag' hue='slate'>
              {o.technique}
            </Chip>
          )}
          {o.effort && <Label tone={EFFORT_TONE[(o.effort || '').toLowerCase()] ?? 'neutral'} text={`${o.effort} effort`} />}
          <Box sx={{ fontWeight: 'var(--ds-font-weight-semibold)', color: 'var(--ds-gray-700)' }}>{o.title}</Box>
        </Box>
        {o.detail && <Box sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-700)' }}>{o.detail}</Box>}
        {o.accuracy_impact && (
          <Box sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-600)' }}>
            <Box component='span' sx={{ color: 'var(--ds-gray-500)' }}>
              accuracy:
            </Box>{' '}
            {o.accuracy_impact}
          </Box>
        )}
      </Box>
    </Box>
  );
}

function AnalysisSection({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <Box>
      <Box
        sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-semibold)', color: 'var(--ds-gray-600)', mb: 'var(--ds-space-1)' }}
      >
        {title}
      </Box>
      {children}
    </Box>
  );
}

/** The Analysis tab body: drives off the usePromptAnalysis phase. */
function PromptAnalysisPanel({
  state,
  measured,
  billedTokens,
  cachedTokens,
  rawTruncated,
  canRun,
  onRun,
}: {
  state: PromptAnalysisState;
  measured: MeasuredTree;
  billedTokens?: number;
  cachedTokens?: number;
  rawTruncated?: boolean;
  canRun: boolean;
  onRun: () => void;
}) {
  if (state.phase === 'init') {
    return (
      <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 'var(--ds-space-3)', minHeight: 120 }}>
        <CircularProgress size={20} />
        <Box sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>Checking for a previous analysis…</Box>
      </Box>
    );
  }
  if (state.phase === 'loading') {
    return (
      <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 'var(--ds-space-3)', minHeight: 140 }}>
        <CircularProgress size={22} />
        <Box sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>
          Analyzing this prompt… this runs an LLM analysis and can take up to a minute.
        </Box>
      </Box>
    );
  }
  if (state.phase === 'idle') {
    return (
      <Box sx={{ display: 'flex', flexDirection: 'column', alignItems: 'flex-start', gap: 'var(--ds-space-3)', py: 'var(--ds-space-2)' }}>
        <Box sx={{ fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-600)', maxWidth: 640 }}>
          Measure this prompt and find where to cut token cost and latency without hurting accuracy — the components that dominate the tokens, the
          static-vs-dynamic split and cache-prefix layout, declared-but-unused dead weight, and ranked fixes. Runs one on-demand analysis.
        </Box>
        <Button
          tone='primary'
          size='sm'
          onClick={onRun}
          disabled={!canRun}
          icon={<AutoAwesomeOutlinedIcon sx={{ fontSize: 16 }} />}
          id='analyze-prompt-run'
        >
          Analyze prompt
        </Button>
        {!canRun && (
          <Box sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>No prompt is available to analyze for this call.</Box>
        )}
      </Box>
    );
  }
  if (state.phase === 'error') {
    return (
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-3)' }}>
        <Banner tone='critical' title='Could not analyze prompt' message={state.error ?? 'Analysis failed'} />
        <Box>
          <Button tone='secondary' size='sm' onClick={onRun} disabled={!canRun} id='analyze-prompt-retry'>
            Try again
          </Button>
        </Box>
      </Box>
    );
  }
  if (!state.data) return null;
  const a = state.data;
  const optimizations = a.optimizations ?? [];
  const deadWeight = a.dead_weight ?? [];
  // Frontend-owned tree + the model's per-id classifications merged in.
  const components = applyClassifications(measured.components, a.classifications);
  const tree = buildComponentTree(components);
  const verdict = a.cache_verdict;
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-4)', maxHeight: '64vh', overflowY: 'auto', pr: 'var(--ds-space-1)' }}>
      {rawTruncated && (
        <Banner
          tone='warning'
          title='Raw sample was truncated'
          message='The prompt exceeded the analysis size cap, so the model saw a truncated sample — its classifications/optimizations may miss content past the cap. The measured component sizes below are complete.'
        />
      )}

      {/* Verdict + authoritative totals (billed tokens from the call). */}
      <Box sx={{ display: 'flex', alignItems: 'flex-start', gap: 'var(--ds-space-4)', flexWrap: 'wrap' }}>
        <Box sx={{ flex: 1, minWidth: 240 }}>
          <Box sx={{ fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-700)' }}>{a.summary}</Box>
          {a.dominant_buckets && (
            <Box sx={{ mt: 'var(--ds-space-1)', fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-600)' }}>
              <Box component='span' sx={{ color: 'var(--ds-gray-500)' }}>
                Dominant:
              </Box>{' '}
              {a.dominant_buckets}
            </Box>
          )}
          <Box sx={{ mt: 'var(--ds-space-2)' }}>
            <Button tone='link' size='sm' onClick={onRun} disabled={!canRun} id='analyze-prompt-rerun'>
              Re-analyze
            </Button>
          </Box>
        </Box>
        <Box sx={{ textAlign: 'right' }}>
          <Box sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>Billed input</Box>
          <Box
            sx={{
              fontSize: 'var(--ds-text-body-lg)',
              fontWeight: 'var(--ds-font-weight-semibold)',
              color: 'var(--ds-gray-700)',
              fontVariantNumeric: 'tabular-nums',
            }}
          >
            {billedTokens != null ? `${fmtTokens(billedTokens)} tok` : `~${fmtTokens(measured.totalTokens)} tok`}
          </Box>
          <Box sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>
            {cachedTokens ? `${fmtTokens(cachedTokens)} cached · ` : ''}
            {fmtBytes(measured.totalBytes)}
          </Box>
        </Box>
      </Box>

      {/* Component tree (macro → sub) with LOC / size / tokens / % and content JSON. */}
      <AnalysisSection title='Components (macro → sub)'>
        {tree.roots.length ? (
          tree.roots.map((c) => <ComponentNode key={c.id} c={c} childrenOf={tree.childrenOf} byId={tree.byId} depth={0} defaultOpen />)
        ) : (
          <Box sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>No components measured.</Box>
        )}
      </AnalysisSection>

      {/* Static/dynamic + cache-prefix verdict. */}
      {verdict && (
        <AnalysisSection title='Cache-prefix verdict'>
          <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)', flexWrap: 'wrap' }}>
            <Label
              tone={verdict.prefix_contiguous ? 'success' : 'critical'}
              text={verdict.prefix_contiguous ? 'Static prefix is contiguous' : 'Prefix is poisoned'}
            />
            <Box sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-700)' }}>{verdict.detail}</Box>
          </Box>
          {verdict.poisoning && (
            <Box sx={{ mt: 'var(--ds-space-1)', fontSize: 'var(--ds-text-caption)', color: 'var(--ds-red-700)' }}>Poisoning: {verdict.poisoning}</Box>
          )}
        </AnalysisSection>
      )}

      {/* Declared-vs-used dead weight. */}
      <AnalysisSection title='Dead weight (declared but unused)'>
        {deadWeight.length ? (
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-1)' }}>
            {deadWeight.map((d, i) => (
              <Box key={i} sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-700)' }}>
                <Box component='span' sx={{ fontWeight: 'var(--ds-font-weight-medium)' }}>
                  {d.name}
                </Box>
                <Box component='span' sx={{ color: 'var(--ds-gray-500)', fontVariantNumeric: 'tabular-nums' }}>
                  {' '}
                  · ~{fmtTokens(d.tokens)} tok
                </Box>
                {d.detail && (
                  <Box component='span' sx={{ color: 'var(--ds-gray-600)' }}>
                    {' '}
                    — {d.detail}
                  </Box>
                )}
              </Box>
            ))}
          </Box>
        ) : (
          <Box sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>None found — every declared section appears to be used.</Box>
        )}
      </AnalysisSection>

      {/* Ranked optimizations w/ projected saving + explicit accuracy impact. */}
      <AnalysisSection title='Optimizations (ranked by saving)'>
        {optimizations.length ? (
          <Box>
            {optimizations.map((o, i) => (
              <OptimizationCard key={i} o={o} />
            ))}
          </Box>
        ) : (
          <Box sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>No optimizations suggested — the prompt looks tight.</Box>
        )}
      </AnalysisSection>

      {a.concrete_instance && (
        <AnalysisSection title='Validated against one call'>
          <Box sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-700)' }}>{a.concrete_instance}</Box>
        </AnalysisSection>
      )}

      <Box sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-400)' }}>
        Billed input is the call&apos;s actual usage; component LOC/size are measured client-side from the stored prompt and component tokens are a
        chars÷4 estimate (so the per-component total can differ from billed). The static/dynamic split, cache verdict, dead weight, and optimizations
        are the model&apos;s judgment.
      </Box>
    </Box>
  );
}

type TraceTab = 'conversation' | 'system' | 'tools' | 'response' | 'raw' | 'analysis';

/** Lazy "view prompt" modal: fetches one model call's prompt/response by id (the
 * same ai_get_conversation_agent action, scoped to model_call_id) only on open.
 * Renders a metadata header + purpose tabs (Conversation / System / Tools /
 * Response / Raw) over role-coded message cards. */
function PromptTraceModal({
  call,
  conversationId,
  accountId,
  agentId,
  onClose,
}: {
  call: ModelCall | null;
  conversationId?: string;
  accountId?: string;
  agentId?: string;
  onClose: () => void;
}) {
  const [state, setState] = React.useState<TraceFetchState>(traceFetchState);
  // Depend on the id (primitive), not the call object — a new object reference with
  // the same id must not trigger a redundant refetch.
  const callId = call?.callId;

  React.useEffect(() => {
    if (!callId || !conversationId || !accountId || !agentId) return;
    const ac = new AbortController();
    setState({ loading: true, error: null, prompt: '', response: '' });
    getConversationAgent({ conversationId, accountId, agentId, modelCallId: callId }, ac.signal)
      .then((d) => {
        if (ac.signal.aborted) return;
        const mc = d?.model_calls?.[0];
        setState({ loading: false, error: null, prompt: mc?.prompt_messages ?? '', response: mc?.response_content ?? '' });
      })
      .catch((e) => {
        if (!ac.signal.aborted)
          setState({ loading: false, error: e instanceof Error ? e.message : 'Failed to load trace', prompt: '', response: '' });
      });
    return () => ac.abort();
  }, [callId, conversationId, accountId, agentId]);

  const messages = React.useMemo(() => parsePromptMessages(state.prompt), [state.prompt]);
  // Readable, copyable form of the whole prompt: each message as "[role]\n<text>".
  const promptCopyText = React.useMemo(() => messages.map((m) => (m.role ? `[${m.role}]\n` : '') + m.text).join('\n\n'), [messages]);

  const lc = (r: string) => (r || '').toLowerCase();
  const systemMsgs = React.useMemo(() => messages.filter((m) => lc(m.role) === 'system'), [messages]);
  const toolMsgs = React.useMemo(() => messages.filter((m) => lc(m.role) === 'tool'), [messages]);
  const convoMsgs = React.useMemo(() => messages.filter((m) => !['system', 'tool'].includes(lc(m.role))), [messages]);
  const maxBytes = React.useMemo(() => messages.reduce((mx, m) => Math.max(mx, byteSize(m.text)), 0), [messages]);
  // Pretty-print the raw prompt JSON for the Raw tab (fall back to the literal string).
  const rawPretty = React.useMemo(() => {
    try {
      return JSON.stringify(JSON.parse(state.prompt), null, 2);
    } catch {
      return state.prompt;
    }
  }, [state.prompt]);

  // Prompt analysis ("Analyze" action) — measure the real artifact client-side,
  // then send the measured component table + raw JSON + response to @LLM for the
  // judgment (classifications + optimizations). The header uses the call's ACTUAL
  // billed tokens, not a text estimate.
  const measured = React.useMemo(() => measurePromptComponents(messages), [messages]);
  const promptTruncated = state.prompt.length > 120_000;
  const analysis = usePromptAnalysis({
    callId,
    accountId,
    promptJson: state.prompt,
    measured: measured.serialized,
    responseContent: state.response,
  });
  const analysisCanRun = !!callId && !!accountId && !!state.prompt;

  const [tab, setTab] = React.useState<TraceTab>('conversation');
  const [maximized, setMaximized] = React.useState(false);
  // Tabs by purpose; only non-empty buckets show (Response/Raw/Analysis always). If
  // the current tab has no content for this trace, fall back to the first available.
  const tabs: { value: TraceTab; label: string }[] = [
    ...(convoMsgs.length ? [{ value: 'conversation' as const, label: `Conversation (${convoMsgs.length})` }] : []),
    ...(systemMsgs.length ? [{ value: 'system' as const, label: `System (${systemMsgs.length})` }] : []),
    ...(toolMsgs.length ? [{ value: 'tools' as const, label: `Tools (${toolMsgs.length})` }] : []),
    { value: 'response' as const, label: 'Response' },
    { value: 'raw' as const, label: 'Raw' },
    { value: 'analysis' as const, label: 'Analysis' },
  ];
  const activeTab = tabs.some((t) => t.value === tab) ? tab : tabs[0].value;

  const truncated = (call?.stopReason || '').toUpperCase().includes('MAX');
  const cardList = (list: ParsedPromptMessage[], empty: string) =>
    list.length ? (
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-2)' }}>
        {list.map((m, i) => (
          <MessageCard key={i} m={m} maxBytes={maxBytes} />
        ))}
      </Box>
    ) : (
      <Box sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>{empty}</Box>
    );

  return (
    <Modal
      width={maximized ? 'xl' : 'lg'}
      title='Prompt & Response'
      open={!!call}
      handleClose={onClose}
      onClose={onClose}
      maxHeight={maximized ? '96vh' : '85vh'}
    >
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-3)', padding: '0 var(--ds-space-5) var(--ds-space-5)' }}>
        {state.loading ? (
          <Box sx={{ display: 'flex', justifyContent: 'center', alignItems: 'center', minHeight: 160 }}>
            <CircularProgress size={22} />
          </Box>
        ) : state.error ? (
          <Banner tone='critical' title='Could not load trace' message={state.error} />
        ) : (
          <>
            {/* Metadata header: provenance + real token/cost figures + quality badges. */}
            {call && (
              <Box
                sx={{
                  display: 'flex',
                  flexWrap: 'wrap',
                  alignItems: 'center',
                  gap: 'var(--ds-space-2) var(--ds-space-4)',
                  padding: 'var(--ds-space-3)',
                  backgroundColor: 'var(--ds-background-200)',
                  borderRadius: 'var(--ds-radius-md)',
                }}
              >
                <MetaChip
                  label='Model'
                  value={`${call.model}${call.provider && call.provider !== ('—' as typeof call.provider) ? ` · ${call.provider}` : ''}`}
                />
                {call.agentName && <MetaChip label='Agent' value={call.agentName} />}
                <MetaChip
                  label='In'
                  value={`${fmtTokens(call.inputTokens)}${call.cachedInputTokens ? ` (${fmtTokens(call.cachedInputTokens)} cached)` : ''}`}
                />
                <MetaChip label='Out' value={fmtTokens(call.outputTokens)} />
                {!!call.thinkingTokens && <MetaChip label='Thinking' value={fmtTokens(call.thinkingTokens)} />}
                <MetaChip label='Cost' value={fmtCost(call.totalCost)} />
                <MetaChip label='Latency' value={fmtDuration(call.latencyMs)} />
                {messages.length > 0 && <MetaChip label='Prompt' value={`${sizeLabel(state.prompt)} · ${messages.length} msgs`} />}
                <Box sx={{ flex: 1 }} />
                <Box sx={{ display: 'inline-flex', gap: 'var(--ds-space-1)' }}>
                  {call.cached && (
                    <Chip size='2xs' variant='tag' tone='success'>
                      cached
                    </Chip>
                  )}
                  {call.retry && (
                    <Chip size='2xs' variant='tag' tone='warning'>
                      retry
                    </Chip>
                  )}
                  {truncated && (
                    <Box component='span' title={`stop_reason: ${call.stopReason}`} sx={{ display: 'inline-flex' }}>
                      <Chip size='2xs' variant='tag' tone='warning'>
                        truncated
                      </Chip>
                    </Box>
                  )}
                  {call.error && (
                    <Box component='span' title={call.errorMessage || 'Request failed'} sx={{ display: 'inline-flex' }}>
                      <Chip size='2xs' variant='tag' tone='critical'>
                        error
                      </Chip>
                    </Box>
                  )}
                </Box>
              </Box>
            )}

            <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 'var(--ds-space-2)' }}>
              <ToggleGroup
                selection='single'
                size='sm'
                ariaLabel='Trace section'
                value={activeTab}
                onChange={(v) => setTab(v as TraceTab)}
                options={tabs}
              />
              <Box sx={{ display: 'inline-flex', alignItems: 'center', gap: 'var(--ds-space-1)' }}>
                {/* Analyze the raw prompt with @LLM, beside the copy action. Switches
                    to the Analysis tab and runs if not already analyzed. */}
                <Button
                  tone='link'
                  size='sm'
                  icon={<AutoAwesomeOutlinedIcon sx={{ fontSize: 14 }} />}
                  disabled={!analysisCanRun || analysis.phase === 'loading'}
                  onClick={() => {
                    setTab('analysis');
                    if (analysis.phase === 'idle' || analysis.phase === 'error') analysis.run();
                  }}
                  aria-label='Analyze prompt'
                  id={`analyze-prompt-${callId ?? ''}`}
                >
                  {analysis.phase === 'loading' ? 'Analyzing…' : 'Analyze'}
                </Button>
                {activeTab === 'response' ? (
                  <CopyButton text={state.response} />
                ) : activeTab === 'raw' ? (
                  <CopyButton text={rawPretty} />
                ) : activeTab === 'analysis' ? null : (
                  <CopyButton text={promptCopyText} />
                )}
                <Box
                  component='button'
                  type='button'
                  onClick={() => setMaximized((m) => !m)}
                  aria-label={maximized ? 'Restore size' : 'Maximize'}
                  title={maximized ? 'Restore size' : 'Maximize'}
                  sx={{
                    display: 'inline-flex',
                    alignItems: 'center',
                    border: 'none',
                    background: 'transparent',
                    cursor: 'pointer',
                    color: 'var(--ds-gray-500)',
                    padding: '2px',
                    '&:hover': { color: 'var(--ds-gray-700)' },
                  }}
                >
                  {maximized ? <FullscreenExitIcon sx={{ fontSize: 18 }} /> : <FullscreenIcon sx={{ fontSize: 18 }} />}
                </Box>
              </Box>
            </Box>

            {activeTab === 'conversation' && cardList(convoMsgs, 'No conversation messages in this prompt.')}
            {activeTab === 'system' && <SystemTab msgs={systemMsgs} />}
            {activeTab === 'tools' && cardList(toolMsgs, 'No tool messages in this prompt.')}
            {activeTab === 'response' && <Box sx={preBoxTall}>{state.response || 'No response captured.'}</Box>}
            {activeTab === 'raw' && <Box sx={preBoxTall}>{rawPretty || 'No prompt captured.'}</Box>}
            {activeTab === 'analysis' && (
              <PromptAnalysisPanel
                state={analysis}
                measured={measured}
                billedTokens={call?.inputTokens}
                cachedTokens={call?.cachedInputTokens}
                rawTruncated={promptTruncated}
                canRun={analysisCanRun}
                onRun={analysis.run}
              />
            )}
          </>
        )}
      </Box>
    </Modal>
  );
}

/** The model-call table (expanded-row content) — one row per LLM call for the agent. */
function ModelCallsTable({
  calls,
  conversationId,
  accountId,
  agentId,
}: {
  calls: ModelCall[];
  conversationId?: string;
  accountId?: string;
  agentId?: string;
}) {
  const [traceCall, setTraceCall] = React.useState<ModelCall | null>(null);
  const [sort, setSort] = React.useState<{ name: string; order: 'asc' | 'desc' }>({ name: '', order: 'desc' });
  const rows = React.useMemo(() => {
    const val = MODEL_CALL_SORT[sort.name];
    if (!val) return calls;
    const sorted = [...calls].sort((a, b) => {
      const av = val(a);
      const bv = val(b);
      return typeof av === 'string' ? String(av).localeCompare(String(bv)) : (av as number) - (bv as number);
    });
    return sort.order === 'desc' ? sorted.reverse() : sorted;
  }, [calls, sort]);

  // Relative outlier highlighting: rank cost/latency within these calls.
  const costSev = React.useMemo(() => makeSeverity(calls.map((c) => c.totalCost)), [calls]);
  const latSev = React.useMemo(() => makeSeverity(calls.map((c) => c.latencyMs)), [calls]);

  if (!calls.length) {
    return <Box sx={{ p: 'var(--ds-space-3)', fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>No model calls for this agent.</Box>;
  }
  const headers = [
    { name: 'Model', width: '20%', sortEnabled: true },
    {
      name: 'In token',
      width: '11%',
      sortEnabled: true,
      component: <HeaderLabel label='In token' info='Input (prompt) tokens sent to the model.' />,
    },
    { name: 'Out token', width: '11%', sortEnabled: true, component: <HeaderLabel label='Out token' info='Output (generated) tokens.' /> },
    {
      name: 'Cached',
      width: '11%',
      sortEnabled: true,
      component: <HeaderLabel label='Cached' info='Input tokens served from cache (cheaper than fresh input).' />,
    },
    {
      name: 'Thinking token',
      width: '14%',
      sortEnabled: true,
      component: <HeaderLabel label='Thinking token' info='Reasoning tokens the model spent before answering.' />,
    },
    { name: 'TTFT', width: '9%', sortEnabled: true, component: <HeaderLabel label='TTFT' info='Time to first token (ms).' /> },
    {
      name: 'Cost',
      width: '10%',
      sortEnabled: true,
      component: <HeaderLabel label='Cost' info='Call cost. Hover the value for the per-component breakdown.' />,
    },
    { name: 'Latency', width: '9%', sortEnabled: true },
    { name: 'Flags', width: '9%', component: <HeaderLabel label='Flags' info='cached · retry · error indicators for the call.' /> },
    {
      name: 'Prompt',
      width: '7%',
      component: <HeaderLabel label='Prompt' info='View the exact prompt sent and the response received for this call (when captured).' />,
    },
  ];
  const tableData = rows.map((c) => [
    { component: <Box sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-700)' }}>{c.model}</Box> },
    { component: <Box sx={cellNum}>{fmtTokens(c.inputTokens)}</Box> },
    { component: <Box sx={cellNum}>{fmtTokens(c.outputTokens)}</Box> },
    {
      component: (
        <Box sx={{ ...cellNum, color: (c.cachedInputTokens ?? 0) > 0 ? 'var(--ds-green-700)' : 'var(--ds-gray-400)' }}>
          {c.cachedInputTokens ? fmtTokens(c.cachedInputTokens) : '—'}
        </Box>
      ),
    },
    { component: <Box sx={cellNum}>{c.thinkingTokens ? `${fmtTokens(c.thinkingTokens)} tokens` : '—'}</Box> },
    { component: <Box sx={cellNum}>{c.ttftMs ? `${Math.round(c.ttftMs)}ms` : '—'}</Box> },
    {
      component: (
        <SeverityCell severity={costSev(c.totalCost)} metric='cost'>
          <Box component='span' title={costBreakdownTitle(c)} sx={{ display: 'inline-flex' }}>
            <CostCallout value={c.totalCost} size='sm' tone='neutral' fractionDigits={2} />
          </Box>
        </SeverityCell>
      ),
    },
    {
      component: (
        <SeverityCell severity={latSev(c.latencyMs)} metric='latency'>
          <Box component='span' sx={cellNum}>
            {fmtDuration(c.latencyMs)}
          </Box>
        </SeverityCell>
      ),
    },
    {
      component: (
        <Box sx={{ display: 'inline-flex', gap: 'var(--ds-space-1)' }}>
          {c.cached && (
            <Chip size='2xs' variant='tag' tone='success'>
              cached
            </Chip>
          )}
          {c.retry && (
            <Chip size='2xs' variant='tag' tone='warning'>
              retry
            </Chip>
          )}
          {c.error && (
            <Box component='span' title={c.errorMessage || 'Request failed'} sx={{ display: 'inline-flex' }}>
              <Chip size='2xs' variant='tag' tone='critical'>
                error
              </Chip>
            </Box>
          )}
          {!c.cached && !c.retry && !c.error && (
            <Box component='span' sx={{ color: 'var(--ds-gray-400)' }}>
              —
            </Box>
          )}
        </Box>
      ),
    },
    {
      component: c.hasTrace ? (
        <Button tone='link' size='sm' onClick={() => setTraceCall(c)} id={`view-prompt-${c.callId}`}>
          View
        </Button>
      ) : (
        <Box component='span' sx={{ color: 'var(--ds-gray-400)' }}>
          —
        </Box>
      ),
    },
  ]);
  return (
    <>
      <CustomTable2 headers={headers} tableData={tableData} sort={sort} onSortChange={(s: { name: string; order: 'asc' | 'desc' }) => setSort(s)} />
      <PromptTraceModal call={traceCall} conversationId={conversationId} accountId={accountId} agentId={agentId} onClose={() => setTraceCall(null)} />
    </>
  );
}

/** The tool-call table (expanded-row content) — the actual tools the agent ran. */
function ToolCallsTable({ tools }: { tools: StepToolCall[] }) {
  if (!tools.length) {
    return <Box sx={{ p: 'var(--ds-space-3)', fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>This agent made no tool calls.</Box>;
  }
  const headers = [
    { name: 'Tool', width: '15%' },
    { name: 'Parameters', width: '30%' },
    { name: 'Response', width: '34%' },
    { name: 'Status', width: '11%' },
    { name: 'Duration', width: '10%' },
  ];
  // Wrapped, scrollable cells so long params/responses stay readable (no truncation).
  const wrap = { fontSize: 'var(--ds-text-caption)', whiteSpace: 'pre-wrap', wordBreak: 'break-word', maxHeight: 140, overflow: 'auto' } as const;
  const empty = (
    <Box component='span' sx={{ color: 'var(--ds-gray-400)' }}>
      —
    </Box>
  );
  const tableData = tools.map((t) => [
    {
      component: (
        <Box sx={{ fontSize: 'var(--ds-text-caption)', color: t.isError ? 'var(--ds-red-700)' : 'var(--ds-gray-700)', wordBreak: 'break-word' }}>
          {t.name}
        </Box>
      ),
    },
    { component: t.parameters ? <Box sx={{ ...wrap, color: 'var(--ds-gray-600)' }}>{t.parameters}</Box> : empty },
    {
      // Response body — for failed calls this is the error/output.
      component: t.response ? <Box sx={{ ...wrap, color: t.isError ? 'var(--ds-red-700)' : 'var(--ds-gray-600)' }}>{t.response}</Box> : empty,
    },
    {
      component: t.status ? (
        <Chip
          size='2xs'
          variant='tag'
          tone={t.isError || /fail|error|terminated/i.test(t.status) ? 'critical' : /success|ok|complete/i.test(t.status) ? 'success' : 'neutral'}
        >
          {t.status}
        </Chip>
      ) : (
        empty
      ),
    },
    { component: <Box sx={cellNum}>{fmtDuration(t.durationMs)}</Box> },
  ]);
  return <CustomTable2 headers={headers} tableData={tableData} />;
}

/** One labelled execution block (query / thought / response) — scrolls if long. */
function ExecBlock({ label, text }: { label: string; text: string }) {
  if (!text) return null;
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: '2px' }}>
      <Box sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-semibold)', color: 'var(--ds-gray-600)' }}>{label}</Box>
      <Box
        sx={{
          fontSize: 'var(--ds-text-caption)',
          color: 'var(--ds-gray-700)',
          whiteSpace: 'pre-wrap',
          wordBreak: 'break-word',
          // Grows with content up to ~420px; long text scrolls. Drag the bottom-right
          // handle to resize, so short snippets stay compact and long ones expand.
          maxHeight: 420,
          overflow: 'auto',
          resize: 'vertical',
          backgroundColor: 'var(--ds-background-100)',
          border: '1px solid var(--ds-gray-200)',
          borderRadius: 'var(--ds-radius-md)',
          p: 'var(--ds-space-2)',
        }}
      >
        {text}
      </Box>
    </Box>
  );
}

const EMPTY_DETAIL: AgentDetail = { query: '', thought: '', response: '', calls: [], tools: [] };

// FocusedAgentPanel renders one agent invocation's full detail at the top of the
// Sub-tasks tab — the deep-link target when you click an agent in the Agents tab.
// It fetches by agent_id directly (the invocation may be nested, not a top-level
// step), so it doesn't depend on CustomTable2 row expansion.
function FocusedAgentPanel({ conversationId, accountId, agentId }: { conversationId?: string; accountId?: string; agentId: string }) {
  const [state, setState] = React.useState<{ loading: boolean; error: string | null; data: AgentDetail | null }>({
    loading: false,
    error: null,
    data: null,
  });

  React.useEffect(() => {
    if (!conversationId || !accountId || !agentId) return;
    let cancelled = false;
    setState({ loading: true, error: null, data: null });
    getConversationAgent({ conversationId, accountId, agentId })
      .then((d) => {
        if (!cancelled) setState({ loading: false, error: null, data: d ? adaptAgentDetail(d, conversationId) : EMPTY_DETAIL });
      })
      .catch((e) => {
        if (!cancelled) setState({ loading: false, error: e instanceof Error ? e.message : 'Failed to load agent', data: null });
      });
    return () => {
      cancelled = true;
    };
  }, [conversationId, accountId, agentId]);

  return (
    <Card>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)', mb: 'var(--ds-space-2)' }}>
        <SectionTitle>Focused agent</SectionTitle>
        <Chip size='2xs' variant='tag' hue='violet'>
          from Agents tab
        </Chip>
      </Box>
      {state.loading && (
        <Box sx={{ display: 'flex', justifyContent: 'center', alignItems: 'center', minHeight: 120 }}>
          <CircularProgress size={22} />
        </Box>
      )}
      {state.error && <Banner tone='critical' title='Could not load agent' message={state.error} />}
      {state.data && (
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-3)' }}>
          <ExecBlock label='Query' text={state.data.query} />
          <ExecBlock label='Thought' text={state.data.thought} />
          <ExecBlock label='Response' text={state.data.response} />
          <Box>
            <Box
              sx={{
                fontSize: 'var(--ds-text-small)',
                fontWeight: 'var(--ds-font-weight-semibold)',
                color: 'var(--ds-gray-600)',
                mb: 'var(--ds-space-1)',
              }}
            >
              Model calls
            </Box>
            <ModelCallsTable calls={state.data.calls} conversationId={conversationId} accountId={accountId} agentId={agentId} />
          </Box>
          {state.data.tools && state.data.tools.length > 0 && (
            <Box>
              <Box
                sx={{
                  fontSize: 'var(--ds-text-small)',
                  fontWeight: 'var(--ds-font-weight-semibold)',
                  color: 'var(--ds-gray-600)',
                  mb: 'var(--ds-space-1)',
                }}
              >
                Tool calls
              </Box>
              <ToolCallsTable tools={state.data.tools} />
            </Box>
          )}
        </Box>
      )}
    </Card>
  );
}

// Module-level cache so the three expand tabs (mounted together when a row opens)
// share ONE `ai_get_conversation_agent` fetch per agent instead of three.
const agentDetailCache = new Map<string, AgentDetail>();
const agentDetailInflight = new Map<string, Promise<AgentDetail>>();

interface AgentDetailState {
  loading: boolean;
  error: string | null;
  data: AgentDetail;
}

function useAgentDetail(step: Step, conversationId?: string, accountId?: string): AgentDetailState {
  const canFetch = !!conversationId && !!accountId;
  const key = `${conversationId}|${accountId}|${step.stepId}`;
  const [state, setState] = React.useState<AgentDetailState>(() => {
    if (canFetch && agentDetailCache.has(key)) return { loading: false, error: null, data: agentDetailCache.get(key)! };
    return { loading: canFetch, error: null, data: { ...EMPTY_DETAIL, calls: step.calls, tools: step.tools ?? [] } };
  });

  React.useEffect(() => {
    if (!canFetch) return;
    if (agentDetailCache.has(key)) {
      setState({ loading: false, error: null, data: agentDetailCache.get(key)! });
      return;
    }
    let cancelled = false;
    setState((s) => ({ ...s, loading: true, error: null }));
    let p = agentDetailInflight.get(key);
    if (!p) {
      p = getConversationAgent({ conversationId: conversationId!, accountId: accountId!, agentId: step.stepId })
        .then((d) => {
          const ad = d ? adaptAgentDetail(d, step.runId) : EMPTY_DETAIL;
          agentDetailCache.set(key, ad);
          return ad;
        })
        .finally(() => agentDetailInflight.delete(key));
      agentDetailInflight.set(key, p);
    }
    p.then((ad) => !cancelled && setState({ loading: false, error: null, data: ad })).catch(
      (e) => !cancelled && setState((s) => ({ loading: false, error: e instanceof Error ? e.message : 'Failed to load agent detail', data: s.data }))
    );
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [canFetch, key]);

  return state;
}

interface AgentModelStat {
  model: string;
  calls: number;
  cost: number;
}

/** Group an agent's model calls into per-model {calls, cost}, cost-desc. */
function modelBreakdown(calls: ModelCall[]): AgentModelStat[] {
  const m = new Map<string, AgentModelStat>();
  for (const c of calls) {
    const e = m.get(c.model) ?? { model: c.model, calls: 0, cost: 0 };
    e.calls += 1;
    e.cost += c.totalCost;
    m.set(c.model, e);
  }
  return [...m.values()].sort((a, b) => b.cost - a.cost);
}

/** Resolve one agent's detail through the shared cache (reused by the expand). */
function fetchAgentDetailCached(conversationId: string, accountId: string, step: Step): Promise<AgentDetail> {
  const key = `${conversationId}|${accountId}|${step.stepId}`;
  const cached = agentDetailCache.get(key);
  if (cached) return Promise.resolve(cached);
  let p = agentDetailInflight.get(key);
  if (!p) {
    p = getConversationAgent({ conversationId, accountId, agentId: step.stepId })
      .then((d) => {
        const ad = d ? adaptAgentDetail(d, step.runId) : EMPTY_DETAIL;
        agentDetailCache.set(key, ad);
        return ad;
      })
      .finally(() => agentDetailInflight.delete(key));
    agentDetailInflight.set(key, p);
  }
  return p;
}

/**
 * Resolve one agent's detail by id (used by the Optimize drill-down). Shares the
 * SAME module cache as the expand rows — their cache key uses step.stepId, which
 * IS the agent id — so opening a finding's backing call reuses an existing fetch.
 */
function fetchAgentDetailById(conversationId: string, accountId: string, agentId: string): Promise<AgentDetail> {
  const key = `${conversationId}|${accountId}|${agentId}`;
  const cached = agentDetailCache.get(key);
  if (cached) return Promise.resolve(cached);
  let p = agentDetailInflight.get(key);
  if (!p) {
    p = getConversationAgent({ conversationId, accountId, agentId })
      .then((d) => {
        const ad = d ? adaptAgentDetail(d, conversationId) : EMPTY_DETAIL;
        agentDetailCache.set(key, ad);
        return ad;
      })
      .finally(() => agentDetailInflight.delete(key));
    agentDetailInflight.set(key, p);
  }
  return p;
}

/**
 * Per-task model breakdown for the list's Models column. The structure-only tree
 * carries no per-agent model names, so we resolve each agent's detail (concurrency
 * limited, shared cache) and group its calls by model. Expanding a row is then instant.
 */
function useAgentModels(steps: Step[], conversationId?: string, accountId?: string): Map<string, AgentModelStat[]> {
  const [map, setMap] = React.useState<Map<string, AgentModelStat[]>>(new Map());
  React.useEffect(() => {
    if (!conversationId || !accountId) return;
    let cancelled = false;
    const targets = steps.filter((s) => (s.modelCallCount ?? 0) > 0);
    setMap(new Map());
    let i = 0;
    const worker = async (): Promise<void> => {
      while (i < targets.length && !cancelled) {
        const s = targets[i++];
        try {
          const ad = await fetchAgentDetailCached(conversationId, accountId, s);
          if (cancelled) return;
          const bd = modelBreakdown(ad.calls);
          setMap((prev) => {
            const next = new Map(prev);
            next.set(s.stepId, bd);
            return next;
          });
        } catch {
          /* leave this row's models unresolved */
        }
      }
    };
    const CONCURRENCY = 6;
    Promise.all(Array.from({ length: Math.min(CONCURRENCY, targets.length) }, worker));
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [conversationId, accountId, steps.length]);
  return map;
}

/** Models cell: per-model chips with (calls · cost); '…' while resolving, '—' if none. */
function StepModels({ models, loading }: { models?: AgentModelStat[]; loading: boolean }) {
  if (!models) {
    return (
      <Box component='span' sx={{ color: 'var(--ds-gray-400)', fontSize: 'var(--ds-text-caption)' }}>
        {loading ? '…' : '—'}
      </Box>
    );
  }
  if (!models.length) {
    return (
      <Box component='span' sx={{ color: 'var(--ds-gray-400)' }}>
        —
      </Box>
    );
  }
  return (
    <Box sx={{ display: 'flex', gap: 'var(--ds-space-1)', flexWrap: 'wrap' }}>
      {models.map((m) => (
        <Chip key={m.model} size='2xs' variant='tag' tone='subtle' hue={MODEL_HUE[m.model] ?? 'slate'}>
          {m.model}
          <Box component='span' sx={{ ml: '3px', color: 'var(--ds-gray-500)', fontWeight: 'var(--ds-font-weight-regular)' }}>
            ({m.calls} · {fmtCost(m.cost)})
          </Box>
        </Chip>
      ))}
    </Box>
  );
}

type AgentTabMode = 'detail' | 'models' | 'tools';

/** One expand tab. All three share `useAgentDetail` (single fetch via the cache). */
function AgentDetailTab({ mode, step, conversationId, accountId }: { mode: AgentTabMode; step: Step; conversationId?: string; accountId?: string }) {
  const { loading, error, data } = useAgentDetail(step, conversationId, accountId);
  if (loading) {
    return (
      <Box sx={{ display: 'flex', justifyContent: 'center', alignItems: 'center', minHeight: 120 }}>
        <CircularProgress size={22} />
      </Box>
    );
  }
  if (error) return <Banner tone='critical' title='Could not load agent detail' message={error} />;

  // Surface the failure reason at the top of every tab when the task failed.
  // The error can live in a model call, a tool response, or — as with a failed
  // agent that produced no errored call/tool — in the agent's own response text.
  const failedCall = data.calls.find((c) => c.error && c.errorMessage);
  const failedTool = data.tools.find((t) => t.isError && t.response);
  const isFailed = step.status === 'failed';
  let errMsg = failedCall?.errorMessage || (failedTool ? `${failedTool.name}: ${failedTool.response}` : '');
  if (!errMsg && isFailed) errMsg = data.response || 'This task failed (no error message returned).';
  const errorBanner = errMsg ? (
    <Banner tone='critical' title='This task failed' message={errMsg.length > 600 ? `${errMsg.slice(0, 600)}…` : errMsg} />
  ) : null;

  let body: React.ReactNode;
  if (mode === 'models') body = <ModelCallsTable calls={data.calls} conversationId={conversationId} accountId={accountId} agentId={step.stepId} />;
  else if (mode === 'tools') body = <ToolCallsTable tools={data.tools} />;
  else {
    const hasExec = data.query || data.thought || data.response;
    body = hasExec ? (
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-3)' }}>
        <ExecBlock label='Query' text={data.query} />
        <ExecBlock label='Thought' text={data.thought} />
        <ExecBlock label='Response' text={data.response} />
      </Box>
    ) : (
      <Box sx={{ p: 'var(--ds-space-3)', fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>No execution content recorded.</Box>
    );
  }

  if (!errorBanner) return <>{body}</>;
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-3)' }}>
      {errorBanner}
      {body}
    </Box>
  );
}

const AGENT_DETAIL_TABS = (['detail', 'models', 'tools'] as const).map((mode, i) => ({
  text: { detail: 'Agent detail', models: 'Model calls', tools: 'Tool calls' }[mode],
  value: i,
  key: mode,
  componentFn: (_o: unknown, q: { step?: Step; conversationId?: string; accountId?: string }) =>
    q?.step ? <AgentDetailTab mode={mode} step={q.step} conversationId={q.conversationId} accountId={q.accountId} /> : <></>,
}));

/** Task (sub-task) cell: agent name, lineage subtext, gate flag, error. Lineage is
 * shown as "from <parent>" (not indentation) so it survives sorting/filtering. */
function TaskCell({ step }: { step: Step }) {
  const lineage = step.parentAgentName ? `from ${step.parentAgentName}` : '';
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: '2px', minWidth: 0 }}>
      <Box sx={{ display: 'inline-flex', alignItems: 'center', gap: 'var(--ds-space-1)', flexWrap: 'wrap' }}>
        <Box component='span' sx={{ fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-700)', fontWeight: 'var(--ds-font-weight-medium)' }}>
          {step.agent}
        </Box>
        {step.waitTimeMs > 0 && (
          <Chip size='2xs' variant='tag' tone='warning'>
            gate
          </Chip>
        )}
      </Box>
      {lineage && <Box sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>{lineage}</Box>}
      {step.errorMessage && (
        <Box
          title={step.errorMessage}
          sx={{
            fontSize: 'var(--ds-text-caption)',
            color: 'var(--ds-red-700)',
            overflow: 'hidden',
            textOverflow: 'ellipsis',
            whiteSpace: 'nowrap',
            maxWidth: 320,
          }}
        >
          {step.errorMessage}
        </Box>
      )}
    </Box>
  );
}

type TaskView = 'all' | 'cost' | 'latency' | 'failed';

const TASK_VIEW_OPTIONS = [
  { value: 'all', label: 'All tasks', icon: <FormatListBulletedIcon sx={{ fontSize: 14 }} /> },
  { value: 'cost', label: 'Top 5 by cost', icon: <PaidOutlinedIcon sx={{ fontSize: 14 }} /> },
  { value: 'latency', label: 'Top 5 by latency', icon: <TimerOutlinedIcon sx={{ fontSize: 14 }} /> },
  { value: 'failed', label: 'Show failed', icon: <ErrorOutlineIcon sx={{ fontSize: 14 }} /> },
];

// Header name → sortable value. Headers absent here are not sortable.
const TASK_SORT_VALUE: Record<string, (s: Step) => number | string> = {
  Task: (s) => s.sequence,
  'Total calls': (s) => (s.modelCallCount ?? s.calls.length) + (s.toolCallCount ?? 0),
  Latency: (s) => s.stepLatencyMs,
  Tokens: (s) => s.stepInputTokens,
  Cost: (s) => s.stepCost,
  Status: (s) => s.status ?? '',
};

/** Step → expandable task table. Built from agent aggregates; the per-agent model
 * + tool calls and execution content load on expand via ai_get_conversation_agent. */
function StepBreakdown({ run, conversationId, accountId }: { run: Run; conversationId?: string; accountId?: string }) {
  const runCost = run.totalCost || 1;
  const [view, setView] = React.useState<TaskView>('all');
  const [sort, setSort] = React.useState<{ name: string; order: 'asc' | 'desc' }>({ name: '', order: 'desc' });
  const agentModels = useAgentModels(run.steps, conversationId, accountId);

  // Same consistency rule as the conversation list: a "Top 5 by X" preset syncs
  // the column sort to that metric so the shown order matches the preset.
  // 'all' / 'failed' fall back to natural task (sequence) order.
  const handleViewChange = (v: string) => {
    const next = (v as TaskView) || 'all';
    setView(next);
    if (next === 'cost') setSort({ name: 'Cost', order: 'desc' });
    else if (next === 'latency') setSort({ name: 'Latency', order: 'desc' });
    else setSort({ name: '', order: 'desc' });
  };
  const modelsResolving = !!conversationId && !!accountId;

  // Top-5 presets narrow the set; column sort then orders whatever is shown.
  const displayed = React.useMemo(() => {
    let list = run.steps;
    if (view === 'cost') list = [...run.steps].sort((a, b) => b.stepCost - a.stepCost).slice(0, 5);
    else if (view === 'latency') list = [...run.steps].sort((a, b) => b.stepLatencyMs - a.stepLatencyMs).slice(0, 5);
    else if (view === 'failed') list = run.steps.filter((s) => (s.status ?? '') === 'failed');
    const val = TASK_SORT_VALUE[sort.name];
    if (val) {
      const sorted = [...list].sort((a, b) => {
        const av = val(a);
        const bv = val(b);
        return typeof av === 'string' ? String(av).localeCompare(String(bv)) : (av as number) - (bv as number);
      });
      list = sort.order === 'desc' ? sorted.reverse() : sorted;
    }
    return list;
  }, [run.steps, view, sort]);

  const headers = [
    {
      name: 'Task',
      width: '18%',
      sortEnabled: true,
      component: <HeaderLabel label='Task' info='The agent (sub-task). “from X” names the parent agent that invoked it.' />,
    },
    { name: 'Models', width: '21%', component: <HeaderLabel label='Models' info='Models this task used, with per-model calls · cost.' /> },
    {
      name: 'Total calls',
      width: '18%',
      sortEnabled: true,
      component: <HeaderLabel label='Total calls' info='Model (LLM) calls + tool calls this task made.' />,
    },
    {
      name: 'Latency',
      width: '10%',
      sortEnabled: true,
      component: <HeaderLabel label='Latency' info='Total model round-trip time for this task.' />,
    },
    {
      name: 'Tokens',
      width: '12%',
      sortEnabled: true,
      component: <HeaderLabel label='Tokens' secondary='(in/out)' info='Input / output tokens for this task.' />,
    },
    { name: 'Cost', width: '8%', sortEnabled: true, component: <HeaderLabel label='Cost' info='This task’s direct model-call cost.' /> },
    { name: 'Status', width: '8%', sortEnabled: true },
    { name: '% run', width: '7%', component: <HeaderLabel label='% run' info='This task’s direct cost as a share of the whole conversation.' /> },
  ];
  const tableData = displayed.map((step) => {
    const models = step.modelCallCount ?? step.calls.length;
    const toolCalls = step.toolCallCount ?? 0;
    const status = step.status ?? 'completed';
    return [
      {
        drilldownQuery: { step, conversationId, accountId },
        component: <TaskCell step={step} />,
      },
      { component: <StepModels models={models === 0 ? [] : agentModels.get(step.stepId)} loading={modelsResolving} /> },
      {
        component: (
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: '2px' }}>
            <Box sx={cellNum}>{models + toolCalls}</Box>
            <Box sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>
              Model: {models} · Tool: {toolCalls}
            </Box>
          </Box>
        ),
      },
      { component: <Box sx={cellNum}>{fmtDuration(step.stepLatencyMs)}</Box> },
      {
        component: (
          <Box sx={cellNum}>
            {fmtTokens(step.stepInputTokens)} / {fmtTokens(step.stepOutputTokens)}
          </Box>
        ),
      },
      { component: <CostCallout value={step.stepCost} size='sm' tone='neutral' fractionDigits={2} /> },
      { component: <Label tone={STATUS_TONE[status]} text={status} /> },
      { component: <Box sx={{ ...cellNum, color: 'var(--ds-gray-500)' }}>{Math.round((step.stepCost / runCost) * 100)}%</Box> },
    ];
  });
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-3)' }}>
      <Box sx={{ display: 'flex', justifyContent: 'flex-start' }}>
        <ToggleGroup selection='single' size='sm' ariaLabel='Filter tasks' value={view} onChange={handleViewChange} options={TASK_VIEW_OPTIONS} />
      </Box>
      <CustomTable2
        headers={headers}
        tableData={tableData}
        expandable={{ tabs: AGENT_DETAIL_TABS }}
        sort={sort}
        onSortChange={(s: { name: string; order: 'asc' | 'desc' }) => setSort(s)}
      />
    </Box>
  );
}

// ─── Optimize tab ──────────────────────────────────────────────────────────
// Lazy-fetches the read-only optimization analysis when the tab is first opened
// (one fetch per conversation; the analysis is an LLM call, so don't refetch on
// every tab toggle).
// phase: init = cheap read for a prior analysis (no LLM); idle = none found, await
// the button; loading = running the LLM analysis; data = showing results; error.
type OptPhase = 'init' | 'idle' | 'loading' | 'data' | 'error';

interface OptimizationState {
  phase: OptPhase;
  error: string | null;
  data: ConversationOptimization | null;
  analyzedAt: string | null;
}

// On open, the runner does a CHEAP read (no LLM): if this conversation was already
// analyzed, show the stored result immediately. The expensive LLM analysis only
// fires on run() (the Analyze / Re-analyze button). run() aborts any in-flight call.
function useOptimizationRunner(conversationId?: string, accountId?: string): OptimizationState & { run: () => void } {
  const [state, setState] = React.useState<OptimizationState>({ phase: 'init', error: null, data: null, analyzedAt: null });
  const ctrlRef = React.useRef<AbortController | null>(null);

  // Cheap cached-result load on open / conversation change.
  React.useEffect(() => {
    if (!conversationId || !accountId) {
      setState({ phase: 'idle', error: null, data: null, analyzedAt: null });
      return;
    }
    const controller = new AbortController();
    let cancelled = false;
    setState({ phase: 'init', error: null, data: null, analyzedAt: null });
    getStoredConversationOptimization({ conversationId, accountId }, controller.signal)
      .then((cached) => {
        if (cancelled) return;
        if (cached) setState({ phase: 'data', error: null, data: cached.optimization, analyzedAt: cached.analyzedAt });
        else setState({ phase: 'idle', error: null, data: null, analyzedAt: null });
      })
      .catch(() => {
        if (!cancelled) setState({ phase: 'idle', error: null, data: null, analyzedAt: null }); // read failure → just offer to analyze
      });
    return () => {
      cancelled = true;
      controller.abort();
    };
  }, [conversationId, accountId]);

  const run = React.useCallback(() => {
    if (!conversationId || !accountId) return;
    ctrlRef.current?.abort();
    const controller = new AbortController();
    ctrlRef.current = controller;
    setState((s) => ({ ...s, phase: 'loading', error: null }));
    generateConversationOptimization({ conversationId, accountId }, controller.signal)
      .then((d) => {
        if (controller.signal.aborted) return;
        if (d) setState({ phase: 'data', error: null, data: d, analyzedAt: new Date().toISOString() });
        else setState({ phase: 'error', error: 'No analysis was returned', data: null, analyzedAt: null });
      })
      .catch((e) => {
        if (controller.signal.aborted) return;
        setState({ phase: 'error', error: e instanceof Error ? e.message : 'Failed to analyze', data: null, analyzedAt: null });
      });
  }, [conversationId, accountId]);

  React.useEffect(() => () => ctrlRef.current?.abort(), []);

  return { ...state, run };
}

// ─── Backing-calls drill-down drawer ───────────────────────────────────────
// Opens the raw per-call rows behind a finding (the same ai_get_conversation_agent
// detail the sub-tasks table expands to) so a hypothesis can be verified.
interface BackingDrawerState {
  loading: boolean;
  error: string | null;
  data: AgentDetail | null;
}

function BackingCallsDrawer({
  agentId,
  conversationId,
  accountId,
  onClose,
}: {
  agentId: string | null;
  conversationId?: string;
  accountId?: string;
  onClose: () => void;
}) {
  const [state, setState] = React.useState<BackingDrawerState>({ loading: false, error: null, data: null });

  React.useEffect(() => {
    if (!agentId || !conversationId || !accountId) return;
    let cancelled = false;
    setState({ loading: true, error: null, data: null });
    fetchAgentDetailById(conversationId, accountId, agentId)
      .then((ad) => !cancelled && setState({ loading: false, error: null, data: ad }))
      .catch((e) => !cancelled && setState({ loading: false, error: e instanceof Error ? e.message : 'Failed to load calls', data: null }));
    return () => {
      cancelled = true;
    };
  }, [agentId, conversationId, accountId]);

  const data = state.data;
  return (
    <Drawer
      anchor='right'
      open={!!agentId}
      onClose={onClose}
      // The Backing-calls drawer is opened from inside the "Conversation details"
      // Dialog (MUI modal z-index 1300). A MUI Drawer defaults to z-index 1200, so
      // it would render BEHIND the dialog — lift it above the modal layer.
      sx={{ zIndex: (theme) => theme.zIndex.modal + 1 }}
      PaperProps={{ sx: { width: 'min(760px, 92vw)' } }}
    >
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-4)', p: 'var(--ds-space-4)' }}>
        <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', gap: 'var(--ds-space-3)' }}>
          <Box>
            <SectionTitle>Backing calls</SectionTitle>
            <Box sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)', fontFamily: 'monospace', wordBreak: 'break-all' }}>
              {agentId}
            </Box>
          </Box>
          <Button tone='link' size='sm' onClick={onClose} id='cost-backing-close'>
            <CloseIcon sx={{ fontSize: 18 }} />
          </Button>
        </Box>

        {state.loading && (
          <Box sx={{ display: 'flex', justifyContent: 'center', alignItems: 'center', minHeight: 160 }}>
            <CircularProgress size={24} />
          </Box>
        )}
        {state.error && <Banner tone='critical' title='Could not load backing calls' message={state.error} />}
        {data && (
          <>
            {(data.query || data.thought || data.response) && (
              <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-3)' }}>
                <ExecBlock label='Query' text={data.query} />
                <ExecBlock label='Thought' text={data.thought} />
                <ExecBlock label='Response' text={data.response} />
              </Box>
            )}
            <Box>
              <Box
                sx={{
                  fontSize: 'var(--ds-text-small)',
                  fontWeight: 'var(--ds-font-weight-semibold)',
                  color: 'var(--ds-gray-600)',
                  mb: 'var(--ds-space-2)',
                }}
              >
                Model calls ({data.calls.length})
              </Box>
              <ModelCallsTable calls={data.calls} conversationId={conversationId} accountId={accountId} agentId={agentId ?? undefined} />
            </Box>
            {data.tools.length > 0 && (
              <Box>
                <Box
                  sx={{
                    fontSize: 'var(--ds-text-small)',
                    fontWeight: 'var(--ds-font-weight-semibold)',
                    color: 'var(--ds-gray-600)',
                    mb: 'var(--ds-space-2)',
                  }}
                >
                  Tool calls ({data.tools.length})
                </Box>
                <ToolCallsTable tools={data.tools} />
              </Box>
            )}
          </>
        )}
      </Box>
    </Drawer>
  );
}

const FINDING_TONE: Record<string, 'success' | 'critical' | 'warning' | 'neutral'> = {
  high: 'success',
  medium: 'warning',
  low: 'neutral',
};

function findingTargetLabel(f: OptFinding): string {
  if (f.target.model) {
    const n = f.target.call_count ? ` · ${f.target.call_count} calls` : '';
    return `${f.target.agent_name ?? ''} / ${f.target.model}${n}`;
  }
  return f.target.agent_name ?? f.target.kind;
}

function exemplarTaskText(ex: OptExemplar): string {
  const t = (ex.task || ex.outcome || '').replace(/\s+/g, ' ').trim();
  return t.length > 90 ? `${t.slice(0, 90)}…` : t;
}

function FindingRow({ f, onDrill }: { f: OptFinding; onDrill: (agentId: string) => void }) {
  const isAdvisory = !!f.advisory;
  const facts = f.supporting_evidence ?? [];
  const exemplars = f.exemplars ?? [];
  const backing = f.backing_agent_ids ?? [];
  return (
    <Box
      sx={{
        display: 'flex',
        alignItems: 'flex-start',
        gap: 'var(--ds-space-3)',
        py: 'var(--ds-space-3)',
        borderTop: '1px solid var(--ds-gray-200)',
      }}
    >
      <Box sx={{ minWidth: 96 }}>
        {isAdvisory ? (
          <>
            <CostCallout value={f.current_cost_usd} size='sm' tone='neutral' fractionDigits={4} />
            <Box sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>addressable</Box>
          </>
        ) : (
          <>
            <CostCallout value={f.estimated_savings_usd} size='sm' tone='high-savings' fractionDigits={4} />
            <Box sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>{f.estimated_savings_pct.toFixed(1)}%</Box>
          </>
        )}
      </Box>
      <Box sx={{ flex: 1, display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-1)', minWidth: 0 }}>
        <Box sx={{ display: 'flex', gap: 'var(--ds-space-2)', alignItems: 'center', flexWrap: 'wrap' }}>
          <Chip size='2xs' variant='tag' hue='blue'>
            {f.type.replace(/_/g, ' ')}
          </Chip>
          <Box sx={{ fontWeight: 'var(--ds-font-weight-semibold)', color: 'var(--ds-gray-700)' }}>{f.title}</Box>
          <Label tone={FINDING_TONE[f.confidence] ?? 'neutral'} text={f.confidence} />
          {f.overlaps_with && f.overlaps_with.length > 0 && (
            <Chip size='2xs' variant='tag' hue='slate'>
              overlaps {f.overlaps_with.join(', ')}
            </Chip>
          )}
        </Box>
        <Box sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-600)' }}>{findingTargetLabel(f)}</Box>
        {f.evidence && <Box sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>evidence: {f.evidence}</Box>}
        {f.recommendation && (
          <Box sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-700)' }}>
            → {f.suggested_model ? `${f.suggested_model}. ` : ''}
            {f.recommendation}
          </Box>
        )}

        {/* Server-derived proof facts — the verifiable numbers behind the hypothesis. */}
        {facts.length > 0 && (
          <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--ds-space-1) var(--ds-space-3)', mt: 'var(--ds-space-1)' }}>
            {facts.map((e, i) => (
              <Box key={`${e.label}-${i}`} sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-600)' }}>
                <Box component='span' sx={{ color: 'var(--ds-gray-500)' }}>
                  {e.label}:
                </Box>{' '}
                <Box component='span' sx={{ fontVariantNumeric: 'tabular-nums', color: 'var(--ds-gray-700)' }}>
                  {e.value}
                </Box>
              </Box>
            ))}
          </Box>
        )}

        {/* Exemplar calls — real numbers; click to open the call rows. */}
        {exemplars.map((ex, i) => {
          const task = exemplarTaskText(ex);
          return (
            <Box
              key={`${ex.agent_id}-${i}`}
              onClick={() => ex.agent_id && onDrill(ex.agent_id)}
              id={`cost-finding-exemplar-${f.id}-${i}`}
              sx={{
                fontSize: 'var(--ds-text-caption)',
                color: 'var(--ds-gray-600)',
                cursor: ex.agent_id ? 'pointer' : 'default',
                '&:hover': ex.agent_id ? { color: 'var(--ds-blue-700)', textDecoration: 'underline' } : undefined,
              }}
            >
              ↳ e.g. {ex.model} · in {fmtTokens(ex.input_tokens)} / out {fmtTokens(ex.output_tokens)} tok · {fmtCost(ex.cost_usd)}
              {task ? ` — ${task}` : ''}
            </Box>
          );
        })}

        {/* Drill-down: open the raw per-call rows for each backing instance. */}
        {backing.length > 0 && (
          <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)', flexWrap: 'wrap', mt: 'var(--ds-space-1)' }}>
            <Box sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>Verify:</Box>
            {backing.map((id, i) => (
              <Button key={id} tone='link' size='sm' onClick={() => onDrill(id)} id={`cost-finding-backing-${f.id}-${i}`}>
                View calls {backing.length > 1 ? `#${i + 1}` : ''} →
              </Button>
            ))}
          </Box>
        )}
      </Box>
    </Box>
  );
}

function OptimizePanel({ conversationId, accountId, autoRun = false }: { conversationId?: string; accountId?: string; autoRun?: boolean }) {
  const { phase, error, data, analyzedAt, run } = useOptimizationRunner(conversationId, accountId);
  const canRun = !!conversationId && !!accountId;
  const [drillAgentId, setDrillAgentId] = React.useState<string | null>(null);

  // Opened via "Analyse": once the cheap cached read finishes with no prior result,
  // auto-start the analysis. (If a cached result was found, we show it instead.)
  const autoRanRef = React.useRef(false);
  React.useEffect(() => {
    if (autoRun && phase === 'idle' && canRun && !autoRanRef.current) {
      autoRanRef.current = true;
      run();
    }
  }, [autoRun, phase, canRun, run]);

  // init — cheap read for a prior analysis (no LLM); brief.
  if (phase === 'init') {
    return (
      <Card>
        <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 'var(--ds-space-3)', minHeight: 120 }}>
          <CircularProgress size={20} />
          <Box sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>Checking for a previous analysis…</Box>
        </Box>
      </Card>
    );
  }

  // idle — no prior analysis; the LLM analysis fires only on the button click.
  if (phase === 'idle') {
    return (
      <Card>
        <Box sx={{ display: 'flex', flexDirection: 'column', alignItems: 'flex-start', gap: 'var(--ds-space-3)' }}>
          <SectionTitle>Optimize this conversation</SectionTitle>
          <Box sx={{ fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-600)', maxWidth: 640 }}>
            Analyze where this conversation&apos;s cost went and get recommendations — lighter models, redundant agents, and retry/failure waste. This
            runs one analysis on demand; it isn&apos;t computed automatically.
          </Box>
          <Button tone='primary' size='sm' onClick={run} disabled={!canRun} id='cost-optimize-run'>
            Analyze cost
          </Button>
          {!canRun && (
            <Box sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>Open a conversation in a specific account to analyze.</Box>
          )}
        </Box>
      </Card>
    );
  }

  if (phase === 'loading') {
    return (
      <Card>
        <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 'var(--ds-space-3)', minHeight: 160 }}>
          <CircularProgress size={24} />
          <Box sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>
            Analyzing… this runs an LLM analysis and can take a minute or two.
          </Box>
        </Box>
      </Card>
    );
  }

  if (phase === 'error') {
    return (
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-3)' }}>
        <Banner tone='critical' title='Could not analyze conversation' message={error ?? 'Analysis failed'} />
        <Box>
          <Button tone='secondary' size='sm' onClick={run} id='cost-optimize-retry'>
            Try again
          </Button>
        </Box>
      </Box>
    );
  }

  if (!data) return null;
  const analyzedLabel = analyzedAt ? new Date(analyzedAt).toLocaleString() : '';

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-3)' }}>
      <Card>
        <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', gap: 'var(--ds-space-4)', flexWrap: 'wrap' }}>
          <Box sx={{ flex: 1, minWidth: 240 }}>
            <SectionTitle>Optimization summary</SectionTitle>
            <Box sx={{ fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-700)' }}>{data.summary}</Box>
            <Box sx={{ mt: 'var(--ds-space-3)', display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)' }}>
              <Button tone='link' size='sm' onClick={run} id='cost-optimize-rerun'>
                Re-analyze
              </Button>
              {analyzedLabel && <Box sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-400)' }}>Analyzed {analyzedLabel}</Box>}
            </Box>
          </Box>
          <Box sx={{ textAlign: 'right' }}>
            <Box sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>Potential savings</Box>
            <CostCallout value={data.total_potential_savings_usd} size='lg' tone='high-savings' fractionDigits={2} />
            <Box sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>
              {data.total_potential_savings_pct.toFixed(1)}% of {fmtCost(data.current_cost_usd)}
            </Box>
          </Box>
        </Box>
      </Card>

      <Card>
        <SectionTitle>Findings</SectionTitle>
        {data.findings.length === 0 ? (
          <Box sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>No optimization opportunities found.</Box>
        ) : (
          <Box>
            {data.findings.map((f) => (
              <FindingRow key={f.id} f={f} onDrill={setDrillAgentId} />
            ))}
          </Box>
        )}
        <Box sx={{ mt: 'var(--ds-space-3)', fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-400)' }}>
          Savings are computed from actual token usage and model pricing; overlapping findings count once toward the total. Open “View calls” to
          verify a finding against its raw per-call rows.
        </Box>
      </Card>

      <BackingCallsDrawer agentId={drillAgentId} conversationId={conversationId} accountId={accountId} onClose={() => setDrillAgentId(null)} />
    </Box>
  );
}

export function ConversationDetailView({
  run,
  loading,
  error,
  usage,
  conversationId,
  accountId,
  onBack,
  hideBackBar = false,
  initialAgentId,
  initialTab = 'subtasks',
  autoRunOptimize = false,
}: ConversationDetailViewProps) {
  const [detailTab, setDetailTab] = React.useState<DetailTabId>(initialTab);
  // Header "Analyze cost" shortcut flips this on and jumps to the Optimize tab,
  // which auto-runs the analysis (same path as the list's "Analyse" action).
  const [optimizeAutoRun, setOptimizeAutoRun] = React.useState(autoRunOptimize);

  if (loading) {
    return (
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-4)' }}>
        {!hideBackBar && <BackBar onBack={onBack} />}
        <Box sx={{ display: 'flex', justifyContent: 'center', alignItems: 'center', minHeight: 240 }}>
          <CircularProgress size={28} />
        </Box>
      </Box>
    );
  }

  if (error || !run) {
    return (
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-4)' }}>
        {!hideBackBar && <BackBar onBack={onBack} />}
        {error ? (
          <Banner tone='critical' title='Could not load conversation' message={error} />
        ) : (
          <EmptyState
            size='section'
            illustration='no-results'
            title='Conversation not found'
            description='It may have been deleted or is outside your access.'
          />
        )}
      </Box>
    );
  }

  const stepSlices = run.steps.filter((s) => s.stepCost > 0).map((s) => ({ key: `${s.sequence}. ${s.agent}`, cost: s.stepCost }));

  // The structure-only tree carries no models and no wall/active timing; the
  // parallel usage-metrics summary (ai_get_conversation_usage_metrics) does, so
  // prefer it for the header and fall back to the tree-derived run when it's
  // absent (that action can fail without failing the view). Per-model COST from
  // this action is legacy/inconsistent — we only read names, tokens, and timing.
  const headerModels = usage?.model_usage?.length ? usage.model_usage.map((m) => m.model_name) : runModelBreakdown(run).map((m) => m.model);
  const wallMs = usage?.wall_time_seconds != null ? usage.wall_time_seconds * 1000 : run.wallClockMs;
  const activeMs = usage?.agent_active_time_seconds != null ? usage.agent_active_time_seconds * 1000 : null;
  const modelLatencyMs = usage?.total_latency_seconds != null ? usage.total_latency_seconds * 1000 : run.totalModelLatencyMs;

  const detailTabOptions = [
    { value: 'subtasks', text: 'Agents' },
    { value: 'metrics', text: 'Models' },
    { value: 'details', text: 'Details' },
    { value: 'optimize', text: 'Optimize' },
  ];

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-5)' }}>
      {!hideBackBar && <BackBar onBack={onBack} />}

      {/* Header summary */}
      <Card>
        <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', gap: 'var(--ds-space-4)', flexWrap: 'wrap' }}>
          <Box>
            <Box sx={{ fontSize: 'var(--ds-text-heading)', fontWeight: 'var(--ds-font-weight-semibold)', color: 'var(--ds-gray-700)' }}>
              {run.title}
            </Box>
            <Box sx={{ display: 'flex', gap: 'var(--ds-space-2)', alignItems: 'center', mt: 'var(--ds-space-1)', flexWrap: 'wrap' }}>
              <Chip size='xs' variant='tag' hue='violet'>
                {run.source ?? triggerLabel[run.triggerType]}
              </Chip>

              <Label tone={STATUS_TONE[run.status]} text={run.status} />
              {run.anomalyFlag && (
                <Chip size='xs' tone='waste' variant='tag'>
                  Anomalous
                </Chip>
              )}
            </Box>
          </Box>
          <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-3)', flexShrink: 0 }}>
            {/* Deep-link to the original conversation in the Ask-Nubi chat (new tab,
                so the Cost Analyser stays open behind the modal). */}
            <CostCallout value={run.totalCost} size='lg' tone={run.anomalyFlag ? 'waste' : 'neutral'} fractionDigits={2} />
            <Button
              tone='secondary'
              size='sm'
              icon={<OpenInNewOutlinedIcon sx={{ fontSize: 16 }} />}
              onClick={() => window.open(`/ask-nudgebee?accountId=${accountId}&session_id=${conversationId}`, '_blank', 'noopener,noreferrer')}
              disabled={!conversationId || !accountId}
              id='cost-goto-conversation'
            >
              Go to conversation
            </Button>
            <Button
              tone='primary'
              size='sm'
              onClick={() => {
                setOptimizeAutoRun(true);
                setDetailTab('optimize');
              }}
              disabled={!conversationId || !accountId}
              id='cost-analyze-header'
            >
              Analyze Cost
            </Button>
          </Box>
        </Box>

        <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--ds-space-5)', mt: 'var(--ds-space-4)' }}>
          <SummaryStat label='Tokens (in / out)'>
            {fmtTokens(run.totalInputTokens)} / {fmtTokens(run.totalOutputTokens)}
          </SummaryStat>
          <SummaryStat label='Wall clock'>{fmtDuration(wallMs)}</SummaryStat>
          {/* Time agents were actively running (from the usage summary). Replaces
              the old "Wait time" slot — the payload exposes no wait/gate field. */}
          <SummaryStat label='Active time'>{activeMs != null ? fmtDuration(activeMs) : '—'}</SummaryStat>
          <SummaryStat label='Model latency'>{fmtDuration(modelLatencyMs)}</SummaryStat>
          <SummaryStat label='Models'>
            {/* Prefer the usage summary's model_usage names; fall back to the
                tree-derived breakdown (which is empty for structure-only trees). */}
            <Box sx={{ display: 'flex', gap: 'var(--ds-space-1)', flexWrap: 'wrap' }}>
              {headerModels.length ? (
                headerModels.map((m) => (
                  <Chip key={m} size='2xs' variant='tag' hue='blue'>
                    {m}
                  </Chip>
                ))
              ) : (
                <Box component='span' sx={{ color: 'var(--ds-gray-400)' }}>
                  —
                </Box>
              )}
            </Box>
          </SummaryStat>
        </Box>

        <Box sx={{ mt: 'var(--ds-space-4)' }}>
          <TimeSplitBar run={run} />
        </Box>
      </Card>

      {/* Tabbed body — sub-tasks / metrics / details */}
      <CustomTabs
        options={{ tabOptions: detailTabOptions }}
        value={detailTab}
        onChange={(next: string) => setDetailTab(next as DetailTabId)}
        behavior='filter'
        variant='primary'
        ariaLabel='Conversation detail sections'
      />

      {detailTab === 'subtasks' && (
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-3)' }}>
          {initialAgentId && <FocusedAgentPanel conversationId={conversationId} accountId={accountId} agentId={initialAgentId} />}
          <Card>
            <StepBreakdown run={run} conversationId={conversationId} accountId={accountId} />
          </Card>
        </Box>
      )}

      {detailTab === 'metrics' &&
        (usage ? (
          <Card>
            <ConversationUsagePanel usage={usage} />
          </Card>
        ) : (
          <EmptyState
            size='section'
            illustration='no-results'
            title='No conversation metrics'
            description='Per-conversation usage metrics are not available for this conversation.'
          />
        ))}

      {detailTab === 'details' && (
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-3)' }}>
          <Card>
            <SectionTitle>Trace</SectionTitle>
            <TraceWaterfall run={run} />
          </Card>
          <Card>
            <SectionTitle>Cost composition</SectionTitle>
            <CostTreemap slices={stepSlices} total={run.totalCost} />
          </Card>
        </Box>
      )}

      {detailTab === 'optimize' && <OptimizePanel conversationId={conversationId} accountId={accountId} autoRun={optimizeAutoRun} />}
    </Box>
  );
}

export default ConversationDetailView;
