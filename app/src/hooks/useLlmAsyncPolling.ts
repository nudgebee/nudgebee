import { useState, useRef, useEffect, useCallback } from 'react';
import apiAskNudgebee from '@api1/ask-nudgebee';

const DEFAULT_POLL_INTERVAL = 3000;
const TERMINAL_STATUSES = ['COMPLETED', 'FAILED', 'KILLED', 'TERMINATED'];

/**
 * Extracts the query/response result from a completed LLM conversation.
 * Returns the first agent response found, or the last message response as fallback.
 */
export function extractQueryResultFromConversation(conversation: any): {
  response: string;
  conversationId: string;
} | null {
  const messages = conversation?.llm_conversation_messages || [];
  const lastMessage = messages[messages.length - 1];
  if (!lastMessage) return null;

  const allAgents = messages.flatMap((m: any) => m.llm_conversation_agents || []);
  for (const agent of allAgents) {
    if (agent.response) {
      return { response: agent.response, conversationId: conversation.id };
    }
  }
  return { response: lastMessage?.response || '', conversationId: conversation.id };
}

/**
 * Reusable hook for polling LLM conversation status after an async request.
 *
 * Usage:
 *   const { isPolling, startPolling, stopPolling } = useLlmAsyncPolling({ accountId });
 *   // After async API call returns session_id:
 *   startPolling(sessionId, (conversation) => { ... handle result ... });
 */
export const useLlmAsyncPolling = ({ accountId, pollInterval = DEFAULT_POLL_INTERVAL }: { accountId: string; pollInterval?: number }) => {
  const [isPolling, setIsPolling] = useState(false);
  const isPollingRef = useRef(false);
  const intervalRef = useRef<ReturnType<typeof setInterval> | null>(null);
  const callbackRef = useRef<((conv: any) => void) | null>(null);
  const sessionIdRef = useRef<string>('');
  const pollGenerationRef = useRef(0);

  const stopPolling = useCallback(() => {
    pollGenerationRef.current += 1;
    if (intervalRef.current) {
      clearTimeout(intervalRef.current);
      intervalRef.current = null;
    }
    sessionIdRef.current = '';
    callbackRef.current = null;
    isPollingRef.current = false;
    setIsPolling(false);
  }, []);

  const schedulePoll = useCallback(() => {
    if (intervalRef.current) return;
    if (!sessionIdRef.current) return;
    intervalRef.current = setTimeout(async () => {
      intervalRef.current = null;
      const sessionId = sessionIdRef.current;
      const pollGeneration = pollGenerationRef.current;
      if (!sessionId) return;
      try {
        const res = await apiAskNudgebee.getLlmConversation({ accountId, sessionId });
        // Validate poll is still relevant (not stopped or restarted)
        if (pollGenerationRef.current !== pollGeneration || sessionIdRef.current !== sessionId) return;
        const conversations = res?.data?.data?.llm_conversations;
        if (!conversations?.length) {
          schedulePoll();
          return;
        }
        const conv = conversations[0];
        if (TERMINAL_STATUSES.includes(conv.status)) {
          const onComplete = callbackRef.current;
          stopPolling();
          onComplete?.(conv);
        } else {
          schedulePoll();
        }
      } catch (err) {
        console.error('Error polling LLM conversation:', err);
        if (pollGenerationRef.current === pollGeneration && sessionIdRef.current === sessionId) {
          const onComplete = callbackRef.current;
          stopPolling();
          onComplete?.({ status: 'FAILED' });
        }
      }
    }, pollInterval);
  }, [accountId, stopPolling, pollInterval]);

  const resumePolling = useCallback(() => {
    if (intervalRef.current) return;
    if (!sessionIdRef.current) return;
    schedulePoll();
  }, [schedulePoll]);

  const startPolling = useCallback(
    (sessionId: string, onComplete: (conv: any) => void) => {
      stopPolling();
      sessionIdRef.current = sessionId;
      callbackRef.current = onComplete;
      isPollingRef.current = true;
      setIsPolling(true);
      if (!document.hidden) {
        resumePolling();
      }
    },
    [stopPolling, resumePolling]
  );

  // Pause polling when tab is hidden, resume when visible
  // Use refs to avoid recreating the listener on every state change
  useEffect(() => {
    const handleVisibilityChange = (): void => {
      if (document.hidden) {
        if (intervalRef.current) {
          clearTimeout(intervalRef.current);
          intervalRef.current = null;
        }
      } else if (isPollingRef.current) {
        resumePolling();
      }
    };
    document.addEventListener('visibilitychange', handleVisibilityChange);
    return () => {
      document.removeEventListener('visibilitychange', handleVisibilityChange);
    };
  }, [isPolling, resumePolling]);

  // Clean up timeout on unmount
  useEffect(() => {
    return () => stopPolling();
  }, [stopPolling]);

  return { isPolling, startPolling, stopPolling };
};
