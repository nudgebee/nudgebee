// What the Generate-with-AI window needs from a builder session that saves server-side
// (source "Automation"): its poll result, the automation it saved, and that automation's state.
import apiWorkflow from '@api1/workflow';
import { parseHttpResponseBodyMessage } from 'src/utils/common';

export const parseConversationPollResult = (conversation: any) => {
  const status = conversation.status;
  const messages = conversation.llm_conversation_messages || [];
  const reversedMessages = [...messages].reverse();
  const lastGenMsg = reversedMessages.find((m: any) => m.message_type === 'generation');
  const lastFollowupMsg = reversedMessages.find((m: any) => m.message_type === 'followup');

  if (status === 'COMPLETED') {
    // After plan approval, the workflow JSON is stored on the followup message's response.
    // For direct generation (no plan approval), it's on the generation message.
    let workflowJson = '';
    const followupResponse = (lastFollowupMsg?.response || '').trimStart();
    if (followupResponse.startsWith('{')) {
      workflowJson = lastFollowupMsg?.response || '';
    }
    if (!workflowJson) {
      const genResponse = (lastGenMsg?.response || '').trimStart();
      if (genResponse.startsWith('{')) {
        workflowJson = lastGenMsg?.response || '';
      }
    }
    // Automation / post-approval builds keep JSON out of `response` (that holds a human-readable
    // summary, and the last followup `response` is just the user's "Approve and Build" click).
    // The built workflow lives in the finalize tool call — use it as the success signal.
    if (!workflowJson) {
      const finalizeCall = reversedMessages
        .flatMap((m: any) => m.llm_conversation_agents || [])
        .flatMap((a: any) => a.llm_conversation_tool_calls || [])
        .find((tc: any) => tc.tool_name === 'finalize' && tc.status === 'success');
      if (finalizeCall?.response) {
        workflowJson = finalizeCall.response;
      }
    }
    // Don't fall back to non-JSON responses — they're text answers, not workflows.
    // The polling loop handles COMPLETED without workflowJson separately.
    // `summaryText` is the human-readable build summary (markdown) shown on the success screen.
    const summaryText = lastGenMsg?.response || '';
    return { status: 'COMPLETED', workflowJson, summaryText, conversationId: conversation.id };
  }

  if (status === 'FAILED') {
    // The generation message's own `response` is empty for the Automation flow — the actionable
    // detail (e.g. an auto-save failure with the runbook server's reason) lives on the agent
    // record. Fall back to the last agent response before the generic message.
    const agentResponse = (lastGenMsg?.llm_conversation_agents || [])
      .map((a: any) => a?.response)
      .filter(Boolean)
      .pop();
    return {
      status: 'FAILED',
      errorMessage: lastGenMsg?.response || agentResponse || 'Automation generation failed. Please try again.',
    };
  }

  // What the builder has done so far. The modal shows it while a build runs and uses it to tell
  // a run that is moving from one that is stuck.
  const toolCalls = messages.flatMap((m: any) => m.llm_conversation_agents || []).flatMap((a: any) => a.llm_conversation_tool_calls || []);
  const progress = {
    steps: toolCalls.length,
    tasksAdded: toolCalls.filter((tc: any) => tc.tool_name === 'add_task' && tc.status === 'success').length,
  };

  if (status !== 'WAITING') {
    return { status, progress };
  }

  // WAITING: could be plan approval or config approval. The followup question
  // text lives in three places depending on the backend version:
  //   1) message_config.question — canonical location (always populated)
  //   2) followup message.message — also populated by the backend
  //   3) followup message.response — legacy; backend stopped writing this in
  //      llm-server #29309 to avoid duplicate rendering in ask-nudgebee chat.
  // Read in that order so we recover the plan text regardless of backend
  // version.
  const agents = lastGenMsg?.llm_conversation_agents || [];
  const lastAgent = agents[agents.length - 1];
  let planOptions: string[] | undefined;
  let followupType: string | undefined;
  let configQuestion = '';
  let agentId = lastAgent?.id;

  let followupData: any;

  const rawConfig = lastFollowupMsg?.message_config;
  if (rawConfig) {
    try {
      const config = typeof rawConfig === 'string' ? JSON.parse(rawConfig) : rawConfig;
      planOptions = config.followupOptions;
      followupType = config.followupType;
      followupData = config.followupData;
      configQuestion = typeof config.question === 'string' ? config.question : '';
      if (config.agentId) {
        agentId = config.agentId;
      }
    } catch {
      // ignore parse errors
    }
  }

  const followupResponse = lastFollowupMsg?.response || '';
  const followupMessage = lastFollowupMsg?.message || '';
  const genResponse = lastGenMsg?.response || '';
  let planText: string;
  if (configQuestion) {
    planText = configQuestion;
  } else if (followupMessage) {
    planText = followupMessage;
  } else if (followupResponse && !followupResponse.trimStart().startsWith('{')) {
    planText = followupResponse;
  } else {
    planText = genResponse;
  }
  planText = planText.replace(/^Here's my plan for building your workflow:\s*/i, '');
  planText = planText.replace(/\s*Would you like to approve this plan or request changes\?\s*$/i, '');

  return {
    status: 'WAITING',
    progress,
    planText,
    planOptions,
    followupType,
    followupData,
    conversationId: conversation.id,
    messageId: lastFollowupMsg?.id,
    agentId,
  };
};

// The automation a session saved, by created_from_session_id (the newest-first first page holds
// it). null: the session saved nothing; undefined: the list call failed.
export const findWorkflowSavedBySession = async (accountId: string, sessionId: string): Promise<any | null | undefined> => {
  if (!accountId || !sessionId) {
    return undefined;
  }
  try {
    const res: any = await apiWorkflow.listWorkflows([accountId], undefined, undefined, undefined, 50, '', '', undefined, undefined);
    const workflows = res?.data?.workflow_list?.workflows;
    if (!Array.isArray(workflows)) {
      return undefined;
    }
    return workflows.find((w: any) => w.created_from_session_id === sessionId) ?? null;
  } catch (error) {
    console.error('Failed to locate the saved automation by session:', error);
    return undefined;
  }
};

// Where a just-built automation ended up: active; still paused (the switch to ACTIVE was refused);
// saved INACTIVE for missing configs; or not found (unverified), so nothing was switched on.
export type AiBuildLiveState = 'active' | 'paused' | 'inactive' | 'unverified';

export interface AiBuildActivation {
  liveState: AiBuildLiveState;
  savedWorkflow?: any;
  error?: string;
}

// The server save already lands the automation as live v1, PAUSED; this runs the list's Activate
// (resume) on it. An INACTIVE save lacks config values, so it is left for the user.
export const activateSavedWorkflow = async (accountId: string, savedWorkflow: any): Promise<AiBuildActivation> => {
  if (!savedWorkflow?.id) {
    return { liveState: 'unverified' };
  }
  if (savedWorkflow.status === 'ACTIVE') {
    return { liveState: 'active', savedWorkflow };
  }
  if (savedWorkflow.status === 'INACTIVE') {
    return { liveState: 'inactive', savedWorkflow };
  }
  try {
    const response: any = await apiWorkflow.resumeWorkflow(accountId, savedWorkflow.id);
    const error = parseHttpResponseBodyMessage(response) || (response?.errors?.length ? 'The automation could not be set active.' : '');
    if (error) {
      return { liveState: 'paused', savedWorkflow, error };
    }
    return { liveState: 'active', savedWorkflow: { ...savedWorkflow, status: 'ACTIVE' } };
  } catch (err) {
    const error = err instanceof Error && err.message ? err.message : 'The automation could not be set active.';
    return { liveState: 'paused', savedWorkflow, error };
  }
};

// Once the automation is active, a one-line paragraph saying it is saved paused / to activate it is
// stale. Multi-line blocks (the task list) and other mentions of "paused" are kept; no match = as is.
const PAUSED_STATE_NOTE = /\bpaused\b/i;
const STATE_WORDS = /\b(saved|activate[sd]?)\b/i;
export const dropPausedNotes = (summary: string): string =>
  summary
    .split(/\n{2,}/)
    .filter((paragraph) => paragraph.includes('\n') || !(PAUSED_STATE_NOTE.test(paragraph) && STATE_WORDS.test(paragraph)))
    .join('\n\n');

/**
 * Turns a COMPLETED build on the event page into the window's result: finds the automation the
 * session saved and switches it on. `activations` holds one activation per session, so overlapping
 * polls never call resume twice; `onSettled` runs once, when that activation finishes.
 */
export const finishEventPageBuild = async (
  accountId: string,
  sessionId: string,
  result: any,
  activations: Map<string, Promise<AiBuildActivation>>,
  onSettled: () => void
) => {
  const savedWorkflow = await findWorkflowSavedBySession(accountId, sessionId);
  if (savedWorkflow === undefined) {
    return { ...result, liveState: 'unverified' as AiBuildLiveState };
  }
  if (savedWorkflow === null) {
    return { ...result, saved: false };
  }
  if (!activations.has(sessionId)) {
    activations.set(
      sessionId,
      activateSavedWorkflow(accountId, savedWorkflow).then((activation) => {
        onSettled();
        return activation;
      })
    );
  }
  const activation = await (activations.get(sessionId) as Promise<AiBuildActivation>);
  return {
    ...result,
    saved: true,
    savedWorkflow: activation.savedWorkflow,
    liveState: activation.liveState,
    liveStateError: activation.error,
    summaryText: activation.liveState === 'active' ? dropPausedNotes(result.summaryText || '') : result.summaryText,
  };
};
