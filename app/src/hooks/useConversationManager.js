import { useState, useCallback, useRef } from 'react';
import apiAskNudgebee from '@api1/ask-nudgebee'; // Adjust path if needed
import { snackbar } from '@components1/common/snackbarService';

export const useConversationManager = () => {
  // Unified state object to reduce re-renders and clutter
  const [state, setState] = useState({
    rawConversations: [],
    likedConversationIds: [], // Renamed for clarity (was likedConversations)
    selectedConversation: null,
    activeFilter: 'All',
    savingStates: {}, // Map of { sessionId: boolean }
  });

  // Tracks in-flight requests synchronously to prevent race conditions on rapid clicks.
  // A Set updated synchronously inside the callback — independent of React's render cycle.
  const inFlightRef = useRef(new Set());

  // Helper to update state partially
  const updateState = useCallback((updates) => {
    setState((prev) => ({ ...prev, ...updates }));
  }, []);

  /**
   * Toggles the "Like" (Saved) status of a conversation.
   * Handles API calls, optimistic updates, and error handling.
   * * @param {string} sessionId - The ID of the conversation to toggle
   * @param {boolean} isStarred - Current status (true = already saved)
   * @param {string} currentSelectedId - Fallback ID if sessionId is null (e.g. active chat)
   */
  const handleLike = useCallback(async (sessionId, isStarred, currentSelectedId) => {
    // Resolve targetId first — used consistently for all checks, state, and API calls
    const targetId = sessionId || currentSelectedId;

    // Prevent double-clicks: synchronous Set check — not affected by render cycle
    if (inFlightRef.current.has(targetId)) {
      return;
    }
    inFlightRef.current.add(targetId);

    // 1. Set loading state
    setState((prev) => ({
      ...prev,
      savingStates: { ...prev.savingStates, [targetId]: true },
    }));

    try {
      if (!isStarred) {
        // --- SAVE LOGIC ---
        const res = await apiAskNudgebee.saveConversation({ conversation_id: targetId });
        const success = res?.data?.data?.ai_create_saved_conversation?.data?.success ?? false;

        if (success) {
          setState((prev) => {
            const newLiked = prev.likedConversationIds.includes(targetId) ? prev.likedConversationIds : [...prev.likedConversationIds, targetId];
            return { ...prev, likedConversationIds: newLiked };
          });
          snackbar.success('Conversation saved successfully');
        } else {
          snackbar.error('Failed to save conversation');
        }
      } else {
        // --- UNSAVE LOGIC ---

        // Optimistic Update: Remove immediately from UI
        setState((prev) => ({
          ...prev,
          likedConversationIds: prev.likedConversationIds.filter((id) => id !== targetId),
        }));

        const res = await apiAskNudgebee.deleteSavedConversation({ conversation_id: targetId });
        const success = res?.data?.data?.ai_delete_saved_conversation?.data?.success ?? false;

        if (success) {
          // Confirm removal and update raw list if looking at "Saved" filter
          setState((prev) => {
            let newRaw = prev.rawConversations;
            if (prev.activeFilter === 'Saved') {
              newRaw = prev.rawConversations.filter((convo) => convo.id !== targetId);
            }

            return {
              ...prev,
              likedConversationIds: prev.likedConversationIds.filter((id) => id !== targetId),
              rawConversations: newRaw,
            };
          });
          snackbar.success('Conversation unsaved successfully');
        } else {
          // Rollback optimistic update on failure
          setState((prev) => ({
            ...prev,
            likedConversationIds: [...prev.likedConversationIds, targetId],
          }));
          snackbar.error('Failed to unsave the conversation');
        }
      }
    } catch (error) {
      console.error('Error toggling conversation like:', error);
      snackbar.error(`An error occurred while ${!isStarred ? 'saving' : 'unsaving'} the conversation`);
    } finally {
      inFlightRef.current.delete(targetId);
      // Clear loading state
      setState((prev) => {
        const newSaving = { ...prev.savingStates };
        delete newSaving[targetId];
        return { ...prev, savingStates: newSaving };
      });
    }
  }, []);

  // Expose simplified setters for external use
  const setLikedConversations = useCallback((idsOrUpdater) => {
    setState((prev) => ({
      ...prev,
      likedConversationIds: typeof idsOrUpdater === 'function' ? idsOrUpdater(prev.likedConversationIds) : idsOrUpdater,
    }));
  }, []);

  const setRawConversations = useCallback((dataOrUpdater) => {
    setState((prev) => ({
      ...prev,
      rawConversations: typeof dataOrUpdater === 'function' ? dataOrUpdater(prev.rawConversations) : dataOrUpdater,
    }));
  }, []);

  const setActiveFilter = useCallback((filter) => updateState({ activeFilter: filter }), [updateState]);
  const setSelectedConversation = useCallback((convo) => updateState({ selectedConversation: convo }), [updateState]);

  return {
    // Data
    rawConversations: state.rawConversations,
    likedConversations: state.likedConversationIds,
    selectedConversation: state.selectedConversation,
    activeFilter: state.activeFilter,
    savingStates: state.savingStates,

    // Actions
    handleLike,
    setLikedConversations,
    setRawConversations,
    setActiveFilter,
    setSelectedConversation,
  };
};
