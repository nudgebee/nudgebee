// Conversation status maps + icon helpers, shared by the two sidebar renderers
// (ConversationListDrawer and ConversationListV2). Extracted to keep the two
// views in lock-step: prior drift caused #36205 (KILLED / TERMINATED falling
// through the map and rendering as a red 'Failed' icon).
//
// Keep STATUS_MAP keys in sync with the Go enum at
// llm/llm-server/agents/core/interface.go (ConversationStatus*). Any new
// backend status left unmapped falls through to 'Unknown' — a neutral gray dot
// rather than the alarming red 'Failed'. All status colors are baked into the
// SVG assets themselves; we do NOT apply a wrapper CSS `color` because SafeIcon
// renders <img src> and the browser ignores parent `color` on the SVG fill.
//
// Color legend (baked into the SVGs):
//   Running               — blue  #3B82F6  spinner    agent working
//   Waiting for Approval  — amber #FBBF24  clock      user must act
//   Queued                — grey-blue #a4b0c5 dashed  not yet started
//   Completed / Failed / Stopped / Interrupted / Unknown — matching semantic icons

import {
  ErrorIcon,
  SuccessIcon,
  StoppedIcon,
  InterruptedIcon,
  QueuedIcon,
  UnknownIcon,
  // Running = blue spinner, Waiting = amber arc (RunningIcon). The arc is safe
  // for waiting now that Running is unambiguously blue — the old collision was
  // both statuses sharing the same amber glyph.
  AskNudgebeeInProgressIcon,
  RunningIcon,
} from '@assets';

export const STATUS_MAP = Object.freeze({
  IN_PROGRESS: 'Running',
  COMPLETED: 'Completed',
  FAILED: 'Failed',
  WAITING: 'Waiting for Approval',
  WAITING_FOR_CLIENT_TOOL: 'Waiting for Approval', // same UX intent as WAITING
  PENDING: 'Queued', // queued but not yet started
  TERMINATED: 'Stopped', // user pressed Stop — deliberate, not an error
  KILLED: 'Interrupted', // supervisor / watchdog reaped the worker — often mid-answer
});

export const STATUS_ICON_MAP = Object.freeze({
  Running: AskNudgebeeInProgressIcon, // blue spinner — AI is actively working
  Completed: SuccessIcon,
  Failed: ErrorIcon,
  'Waiting for Approval': RunningIcon, // amber arc — user must click to unblock
  Queued: QueuedIcon, // lighter-blue dashed circle — queued, not started
  Stopped: StoppedIcon, // grey stop square — user pressed Stop
  Interrupted: InterruptedIcon, // amber dash — supervisor reap, cut short
  Unknown: UnknownIcon, // neutral gray dot — unmapped backend status
});

// Resolve a backend status string (or a nested for_status[0].status) to the
// display label used everywhere else in this module. Optional chaining on
// `for_status` and its `[0]` guards against undefined/null payloads.
export const resolveStatusLabel = (rawStatus, forStatus) => STATUS_MAP[rawStatus] || STATUS_MAP[forStatus?.[0]?.status] || 'Unknown';

export const getStatusIcon = (status) => STATUS_ICON_MAP[status] || UnknownIcon;
