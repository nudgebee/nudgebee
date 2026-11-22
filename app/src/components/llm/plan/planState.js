// Structured plan of a Nubi turn (update_notebook_v2, #41443).
//
// The llm-server stores the plan as JSON on the synthetic `notebook` agent row
// ({ version: 1, steps: [{ id, phase, title, kind, status, prior, finding, parent }] })
// and tags each tool call's metadata with the `plan_step` it served. These
// helpers turn that into what the plan card renders. Everything here is pure.

export const NOTEBOOK_AGENT_NAME = 'notebook';
export const PLAN_TOOL_NAME = 'update_notebook_v2';
/** Pseudo-step for work the planner did after planning without naming a step. */
export const OUTSIDE_PLAN_STEP = '_outside';

const CLOSED_STATUSES = new Set(['done', 'skipped', 'blocked', 'supported', 'refuted', 'inconclusive']);
const ACTIVE_STATUSES = new Set(['in_progress', 'testing']);

export const isClosedStatus = (status) => CLOSED_STATUSES.has(status);
export const isActiveStatus = (status) => ACTIVE_STATUSES.has(status);

const parseJson = (value) => {
  if (!value) return null;
  if (typeof value === 'object') return value;
  try {
    return JSON.parse(value);
  } catch {
    return null;
  }
};

/** Steps from a notebook row's plan_state, or null when there is no plan. */
export const parsePlanState = (planState) => {
  const parsed = parseJson(planState);
  const steps = Array.isArray(parsed?.steps) ? parsed.steps.filter((s) => s && s.id) : [];
  return steps.length > 0 ? steps : null;
};

/**
 * Work of one message grouped by the plan step it served. A step owns the
 * top-level calls tagged with it plus everything run by the sub-agents those
 * calls started (recursively), so its count matches the Tasks drawer.
 *
 * Returns { calls: { stepId: [top-level call] }, counts: { stepId: n },
 * nodeIds: { stepId: Set(tool-call and agent ids) } }.
 */
export const groupToolCallsByStep = (agents, fallbackStepFor = () => null) => {
  const callsByAgent = {};
  (agents || []).forEach((agent) => {
    if (agent?.id) callsByAgent[agent.id] = agent.llm_conversation_tool_calls || [];
  });
  const calls = {};
  const counts = {};
  const nodeIds = {};
  const addSubtree = (stepId, call, seen) => {
    nodeIds[stepId].add(call.id);
    counts[stepId] += 1;
    const child = call.child_agent_id;
    if (!child || seen.has(child) || !callsByAgent[child]) return;
    seen.add(child);
    nodeIds[stepId].add(child);
    callsByAgent[child].forEach((c) => addSubtree(stepId, c, seen));
  };
  // Only agents nobody started are grouped directly; a sub-agent's calls are
  // counted once, under the step of the call that started it (addSubtree).
  const childAgents = new Set();
  (agents || []).forEach((agent) => {
    (agent?.llm_conversation_tool_calls || []).forEach((call) => {
      if (call?.child_agent_id) childAgents.add(call.child_agent_id);
    });
  });
  (agents || []).forEach((agent) => {
    if (childAgents.has(agent?.id)) return;
    (agent?.llm_conversation_tool_calls || []).forEach((call) => {
      const stepId = parseJson(call?.metadata)?.plan_step || fallbackStepFor(call, agent);
      if (!stepId) return;
      if (!calls[stepId]) {
        calls[stepId] = [];
        counts[stepId] = 0;
        nodeIds[stepId] = new Set();
      }
      calls[stepId].push({ id: call.id, tool: call.tool_name, thought: call.thought, status: call.status });
      addSubtree(stepId, call, new Set());
    });
  });
  return { calls, counts, nodeIds };
};

/**
 * The plan of one message, or null. Built from the message's notebook row and
 * the tool calls of all its agents.
 */
export const buildMessagePlan = (agents) => {
  const notebook = (agents || []).find((a) => a?.agent_name === NOTEBOOK_AGENT_NAME && a?.plan_state);
  const steps = parsePlanState(notebook?.plan_state);
  if (!steps) return null;
  // Calls the planning agent made after its first plan update without naming a
  // step (e.g. after a review sent the answer back) show as "Outside the plan",
  // so the card never looks finished while work is still happening.
  const planner = (agents || []).find((a) => (a?.llm_conversation_tool_calls || []).some((c) => c.tool_name === PLAN_TOOL_NAME));
  const planCalls = (planner?.llm_conversation_tool_calls || []).filter((c) => c.tool_name === PLAN_TOOL_NAME);
  // A plan call without a readable timestamp is skipped: one NaN would make
  // Math.min NaN and silently drop every call from "Outside the plan".
  const planTimes = planCalls.map((c) => new Date(c.created_at).getTime()).filter((t) => !Number.isNaN(t));
  const firstPlanAt = planTimes.length ? Math.min(...planTimes) : Infinity;
  const fallbackStepFor = (call, agent) =>
    agent === planner &&
    call.tool_name !== PLAN_TOOL_NAME &&
    call.tool_name !== 'update_notebook' &&
    new Date(call.created_at).getTime() > firstPlanAt
      ? OUTSIDE_PLAN_STEP
      : null;
  const { calls, counts, nodeIds } = groupToolCallsByStep(agents, fallbackStepFor);
  return { notebookAgentId: notebook.id, steps, toolCallsByStep: calls, callCountByStep: counts, nodeIdsByStep: nodeIds };
};

/** Top-level steps grouped into phases, in first-seen order; checks nested under their parent. */
export const groupStepsByPhase = (steps) => {
  const ids = new Set(steps.map((s) => s.id));
  const children = {};
  steps.forEach((s) => {
    if (s.parent && ids.has(s.parent)) {
      if (!children[s.parent]) children[s.parent] = [];
      children[s.parent].push(s);
    }
  });
  const phases = [];
  steps.forEach((s) => {
    if (s.parent && ids.has(s.parent)) return;
    const name = s.phase || '';
    let phase = phases.find((p) => p.name === name);
    if (!phase) {
      phase = { name, steps: [] };
      phases.push(phase);
    }
    phase.steps.push({ ...s, children: children[s.id] || [] });
  });
  return phases;
};

/** "3 of 5 closed" style summary counts. */
export const summarizePlan = (steps) => {
  const total = steps.length;
  const closed = steps.filter((s) => isClosedStatus(s.status)).length;
  const active = steps.find((s) => isActiveStatus(s.status));
  return { total, closed, activeTitle: active?.title || null };
};

/** Drawer rows that belong to a step, keeping the top-level orchestrator header. */
export const filterDrawerTasksForStep = (drawerTasks, nodeIds) =>
  (drawerTasks || []).filter((t) => nodeIds?.has(t.id) || (t.nodeKind === 'agent' && !t.parentId));
