import { useState, useEffect, useRef } from 'react';
import apiAskNudgebee from '@api1/ask-nudgebee';

/**
 * Fetches references and memories for completed response messages.
 * Uses AbortController to prevent state updates on unmounted components.
 */
export default function useMessageAdditionalData(groupedMessages, accountId, conversationId) {
  const [additionalData, setAdditionalData] = useState({});
  const fetchedIdsRef = useRef(new Set());
  const prevConversationIdRef = useRef(conversationId);

  useEffect(() => {
    if (prevConversationIdRef.current !== conversationId) {
      prevConversationIdRef.current = conversationId;
      setAdditionalData({});
      fetchedIdsRef.current = new Set();
    }
  }, [conversationId]);

  useEffect(() => {
    const controller = new AbortController();

    groupedMessages.forEach((group) => {
      const response = group.children.find((c) => (c.tool ?? c.type) === 'response');
      if (!response?.id || fetchedIdsRef.current.has(response.id)) {
        return;
      }

      const isCompleted = ['COMPLETED', 'SUCCESS'].includes(response.status?.toUpperCase());
      if (!isCompleted) {
        return;
      }

      fetchedIdsRef.current.add(response.id);

      Promise.all([
        apiAskNudgebee.listReferences({ accountId, messageId: response.id, conversationId }),
        apiAskNudgebee.listMemory(accountId, conversationId, response.id),
      ])
        .then(([refRes, memRes]) => {
          if (controller.signal.aborted) return;
          setAdditionalData((prev) => ({
            ...prev,
            [response.id]: {
              references: refRes?.data || [],
              memories: memRes?.data || [],
            },
          }));
        })
        .catch((err) => {
          if (controller.signal.aborted) return;
          console.error('Failed to fetch additional data for message', response.id, err);
          fetchedIdsRef.current.delete(response.id);
        });
    });

    return () => controller.abort();
  }, [groupedMessages, accountId, conversationId]);

  return additionalData;
}
