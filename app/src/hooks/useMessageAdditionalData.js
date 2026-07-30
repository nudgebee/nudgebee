import { useState, useEffect, useRef } from 'react';
import apiAskNudgebee from '@api1/ask-nudgebee';
import { useBCortexEnabled } from '@hooks/useBCortexEnabled';

// A response message's status can land on any of these (see
// useLLMInvestigationControl.js's isMessageCompleted) — not just COMPLETED/SUCCESS.
// The old per-card feedback fetch in KubernetesLLMRequestResponseV2 had no status
// check at all, so failed/killed/terminated turns could still show prior feedback;
// matching that here (for feedback only — references/memory keep the narrower
// COMPLETED/SUCCESS gate they always had).
const TERMINAL_RESPONSE_STATUSES = ['COMPLETED', 'SUCCESS', 'FAILED', 'KILLED', 'TERMINATED'];

/**
 * Fetches references, memory, and feedback for newly-completed responses in one batch
 * per poll cycle (message_ids/session_id: [...newIds] — llm-server's ai_list_references,
 * ai_list_memory, and ai_list_conversation_feedback all accept an id array, scoped to just
 * the ids that changed), then groups the flat rows by message_id client-side. Replaces
 * one-getFeedbackForSessionId/listReferences/listMemory-call-per-message.
 *
 * Memory (ai_list_memory / llm_conversation_memory) is legacy-only: when the tenant has the
 * Memory Module (b-Cortex) enabled, the backend's extraction path skips
 * llm_conversation_memory entirely for these tenants (writes go to the new typed stores
 * instead), so this read would always return empty — it's skipped outright rather than
 * fetched and discarded. References and feedback are unaffected by this flag either way.
 *
 * Uses AbortController to prevent state updates on unmounted components.
 */
export default function useMessageAdditionalData(groupedMessages, accountId, conversationId) {
  const [additionalData, setAdditionalData] = useState({});
  const fetchedReferenceIdsRef = useRef(new Set());
  const fetchedLegacyMemoryIdsRef = useRef(new Set());
  const fetchedFeedbackIdsRef = useRef(new Set());
  const prevConversationIdRef = useRef(conversationId);

  // useBCortexEnabled needs a truthy retryKey to fire its lookup; a stable `true` fires it
  // once per mount, then reads from hasFeatureAccess's own tenant-level cache thereafter
  // (cheap — shared across every component that calls the hook, not refetched per mount).
  // `null` (still resolving) is treated the same as `true` — the hook's own doc comment
  // documents that as the correct optimistic default ("render as if enabled"), so the
  // legacy fetch stays skipped during that window too, not just once resolved `true`.
  const bCortexEnabled = useBCortexEnabled(true);
  const legacyMemoryActive = bCortexEnabled === false;

  useEffect(() => {
    if (prevConversationIdRef.current !== conversationId) {
      prevConversationIdRef.current = conversationId;
      setAdditionalData({});
      fetchedReferenceIdsRef.current = new Set();
      fetchedLegacyMemoryIdsRef.current = new Set();
      fetchedFeedbackIdsRef.current = new Set();
    }
  }, [conversationId]);

  useEffect(() => {
    if (!accountId || !conversationId) {
      return undefined;
    }

    const responses = groupedMessages
      .map((group) => group.children.find((c) => (c.tool ?? c.type) === 'response'))
      .filter((response) => response?.id);

    const completedResponseIds = responses
      .filter((response) => ['COMPLETED', 'SUCCESS'].includes(response.status?.toUpperCase()))
      .map((response) => response.id);
    const terminalResponseIds = responses
      .filter((response) => TERMINAL_RESPONSE_STATUSES.includes(response.status?.toUpperCase()))
      .map((response) => response.id);

    const newReferenceIds = completedResponseIds.filter((id) => !fetchedReferenceIdsRef.current.has(id));
    const newLegacyMemoryIds = legacyMemoryActive ? completedResponseIds.filter((id) => !fetchedLegacyMemoryIdsRef.current.has(id)) : [];
    const newFeedbackIds = terminalResponseIds.filter((id) => !fetchedFeedbackIdsRef.current.has(id));

    const shouldFetchReferences = newReferenceIds.length > 0;
    const shouldFetchLegacyMemory = newLegacyMemoryIds.length > 0;
    const shouldFetchFeedback = newFeedbackIds.length > 0;
    if (!shouldFetchReferences && !shouldFetchLegacyMemory && !shouldFetchFeedback) {
      return undefined;
    }
    newReferenceIds.forEach((id) => fetchedReferenceIdsRef.current.add(id));
    newLegacyMemoryIds.forEach((id) => fetchedLegacyMemoryIdsRef.current.add(id));
    newFeedbackIds.forEach((id) => fetchedFeedbackIdsRef.current.add(id));

    const controller = new AbortController();

    Promise.all([
      shouldFetchReferences ? apiAskNudgebee.listReferences({ accountId, conversationId, messageIds: newReferenceIds }) : null,
      shouldFetchLegacyMemory ? apiAskNudgebee.listMemory(accountId, conversationId, undefined, undefined, undefined, newLegacyMemoryIds) : null,
      shouldFetchFeedback ? apiAskNudgebee.getFeedbackForSessionId({ account_id: accountId, session_id: newFeedbackIds }) : null,
    ])
      .then(([refRes, memRes, feedbackRes]) => {
        if (controller.signal.aborted) return;
        setAdditionalData((prev) => {
          // Start from the previous state and only touch the buckets for ids we actually
          // fetched this cycle — every call is scoped to just the new ids, so old messages'
          // references/memories/feedback are left completely untouched.
          const byMessage = {};
          Object.keys(prev).forEach((id) => {
            byMessage[id] = { ...prev[id] };
          });
          const bucketFor = (messageId) => {
            if (!byMessage[messageId]) {
              byMessage[messageId] = { references: [], memories: [] };
            }
            return byMessage[messageId];
          };
          if (refRes) {
            newReferenceIds.forEach((id) => {
              bucketFor(id).references = [];
            });
            (refRes?.data || []).forEach((ref) => bucketFor(ref.message_id).references.push(ref));
          }
          if (memRes) {
            newLegacyMemoryIds.forEach((id) => {
              bucketFor(id).memories = [];
            });
            (memRes?.data || []).forEach((mem) => bucketFor(mem.message_id).memories.push(mem));
          }
          if (feedbackRes) {
            const feedbackRows = feedbackRes?.data?.data?.ai_list_conversation_feedback?.rows ?? [];
            feedbackRows.forEach((row) => {
              bucketFor(row.session_id).feedback = {
                submitted: true,
                isPositive: row.useful === true,
                message: row.additional_notes ?? '',
              };
            });
          }
          return byMessage;
        });
      })
      .catch((err) => {
        if (controller.signal.aborted) return;
        console.error('Failed to fetch additional data for conversation', conversationId, err);
        newReferenceIds.forEach((id) => fetchedReferenceIdsRef.current.delete(id));
        newLegacyMemoryIds.forEach((id) => fetchedLegacyMemoryIdsRef.current.delete(id));
        newFeedbackIds.forEach((id) => fetchedFeedbackIdsRef.current.delete(id));
      });

    return () => controller.abort();
  }, [groupedMessages, accountId, conversationId, legacyMemoryActive]);

  return additionalData;
}
