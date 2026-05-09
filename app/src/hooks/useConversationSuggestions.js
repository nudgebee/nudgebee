import { useState, useCallback } from 'react';
import apiAskNudgebee from '@api1/ask-nudgebee'; // Adjust path as needed

export const useConversationSuggestions = (accountId) => {
  const [suggestions, setSuggestions] = useState([]);

  // Clear suggestions (used when starting new chat, changing account, etc.)
  const clearSuggestions = useCallback(() => {
    setSuggestions([]);
  }, []);

  // Fetch suggestions from API
  const fetchSuggestions = useCallback(
    async (conversationId, lastMessageId) => {
      if (!accountId || !conversationId || !lastMessageId) {
        return;
      }

      try {
        const res = await apiAskNudgebee.getConversationSuggestions({
          account_id: accountId,
          conversation_id: conversationId,
          message_id: lastMessageId,
          user_id: '', // Empty as per original implementation
        });

        const newSuggestions = res?.data?.data?.ai_get_conversation_suggestions?.data?.suggestions ?? [];
        setSuggestions(newSuggestions);
      } catch (error) {
        console.error('Error fetching conversation suggestions:', error);
        // We generally don't show snackbar errors for background suggestion fetching failures
        // to avoid disrupting the user experience
      }
    },
    [accountId]
  );

  return {
    suggestions,
    fetchSuggestions,
    clearSuggestions,
  };
};
