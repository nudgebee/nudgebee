import { createContext } from 'react';

// Answers cite the steps they rest on as markdown links: `[Kubectl - E3](#task-<row id>)`.
// The server writes the id of the tool-call row the step stored. Answers saved
// before that carry the step number instead (`#task-E3`), which only meant
// something inside the run that produced it and is not looked up here.

const ROW_ID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

/**
 * What a link in an answer points at: `{ rowId }` for a stored step, `{ legacy: true, step }` for an
 * old step-number citation (E5 → step 5), or null for any other link.
 */
export const parseCitationHref = (href) => {
  const match = typeof href === 'string' ? href.match(/^#task-(.+)$/i) : null;
  if (!match) {
    return null;
  }
  if (ROW_ID.test(match[1])) {
    return { rowId: match[1].toLowerCase() };
  }
  const step = match[1].match(/^E(\d+)$/i);
  return step ? { legacy: true, step: Number(step[1]) } : null;
};

const sameId = (a, b) => a != null && b != null && String(a).toLowerCase() === String(b).toLowerCase();

/**
 * The drawer task (from buildDrawerTasks) that shows a cited tool-call row. A row is its own
 * task, except a call that started a sub-agent (shown as that sub-agent) and a sub-step
 * (shown inside the call that produced it).
 */
export const findCitedTask = (rowId, tasks) => {
  if (!rowId || !Array.isArray(tasks)) {
    return null;
  }
  return (
    tasks.find((t) => t.nodeKind === 'tool' && sameId(t.id, rowId)) ||
    tasks.find((t) => t.nodeKind === 'agent' && sameId(t.spawnerToolCallId, rowId)) ||
    tasks.find((t) => t.nodeKind === 'tool' && (t.childSteps || []).some((s) => sameId(s.id, rowId))) ||
    null
  );
};

/**
 * The drawer task an old step-number citation (E5) most likely names: the top-level agent's step
 * whose stored id carries that number (parallel calls do), else its fifth step in run order. The
 * planner numbered steps in that order, but a step it dropped or retried can shift the count, so
 * callers say the match is by order.
 */
export const findLegacyCitedTask = (step, tasks) => {
  if (!step || !Array.isArray(tasks)) {
    return null;
  }
  const steps = tasks.find((t) => t.nodeKind === 'agent' && !t.parentId)?.citationSteps || [];
  const numbered = new RegExp(`-E${step}(-\\d+)?$`, 'i');
  const row = steps.find((s) => numbered.test(String(s.toolId || ''))) || steps[step - 1];
  return row ? findCitedTask(row.rowId, tasks) : null;
};

/** Citation links rendered as compact chips; spread into a MarkDowns `sx`. */
export const citationChipSx = {
  '& a[href^="#task-"]': {
    display: 'inline-block',
    px: 'var(--ds-space-1)',
    borderRadius: 'var(--ds-radius-sm)',
    backgroundColor: 'var(--ds-blue-100)',
    color: 'var(--ds-blue-700)',
    fontSize: '0.86em',
    fontWeight: 'var(--ds-font-weight-medium)',
    lineHeight: 1.5,
    textDecoration: 'none',
    whiteSpace: 'nowrap',
    cursor: 'pointer',
    // The markdown viewer's link hover adds a bottom border; a fixed transparent border keeps the
    // chip the same size on hover so the lines below do not shift.
    border: '1px solid transparent',
    transition: 'background-color 0.15s ease',
  },
  '& a[href^="#task-"]:hover': {
    backgroundColor: 'var(--ds-blue-200)',
    border: '1px solid transparent',
  },
};

/**
 * How a drawer opens a cited step from text rendered inside it — a sub-agent's answer in Tool
 * Details cites that sub-agent's own steps. Receives the link's href, returns true when it opened
 * the step. Null outside a drawer that lists the steps.
 */
export const CitationClickContext = createContext(null);
