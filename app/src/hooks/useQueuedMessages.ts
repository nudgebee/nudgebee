import { useState, useCallback, useEffect, useRef, useMemo } from 'react';
import apiAskNudgebee from '@api1/ask-nudgebee';

export interface QueuedMessageAttachment {
  data?: string;
  mime_type?: string;
  id?: string;
  size?: number;
  description?: string;
}

export interface QueuedMessage {
  id: string;
  text: string;
  images?: QueuedMessageAttachment[];
  config?: any;
  createdAt: number;
  user?: string;
  userId?: string;
  isServer?: boolean;
  saving?: boolean;
  isForeground?: boolean;
  saveError?: string;
  notAdmitted?: boolean;
}

export const DEFAULT_MAX_QUEUED_MESSAGES = 5;

export interface UseQueuedMessagesOptions {
  isConversationInProgress: boolean;
  conversationStatus: string;
  conversationId?: string;
  accountId?: string;
  serverQueuedMessages?: QueuedMessage[];
  maxQueuedMessages?: number;
  onDispatch: (message: QueuedMessage) => Promise<void> | void;
  onStop?: () => Promise<void> | void;
  onRefresh?: () => Promise<unknown>;
}

export interface UseQueuedMessagesReturn {
  queuedMessages: QueuedMessage[];
  isExpanded: boolean;
  setIsExpanded: React.Dispatch<React.SetStateAction<boolean>>;
  toggleExpanded: () => void;
  enqueue: (text: string, images?: QueuedMessageAttachment[], config?: any) => string;
  remove: (id: string) => Promise<void> | void;
  update: (id: string, text: string) => Promise<void>;
  error: string | null;
  sendNow: (id: string) => Promise<void>;
  clear: () => void;
  maxQueuedMessages: number;
  isMaxQueueReached: boolean;
}

export const useQueuedMessages = ({
  isConversationInProgress,
  conversationStatus,
  conversationId,
  accountId,
  serverQueuedMessages = [],
  maxQueuedMessages = DEFAULT_MAX_QUEUED_MESSAGES,
  onDispatch,
  onStop,
  onRefresh,
}: UseQueuedMessagesOptions): UseQueuedMessagesReturn => {
  const [localQueuedMessages, setLocalQueuedMessages] = useState<QueuedMessage[]>([]);
  const [isExpanded, setIsExpanded] = useState<boolean>(true);
  const [error, setError] = useState<string | null>(null);
  const [removedIds, setRemovedIds] = useState<string[]>([]);
  // Edited text shown until the next conversation fetch carries it from the server.
  const [textOverrides, setTextOverrides] = useState<Record<string, string>>({});

  // When serverQueuedMessages arrives, reconcile localQueue by dropping any local
  // items that the server now reflects (by message identity)
  useEffect(() => {
    if (!serverQueuedMessages || serverQueuedMessages.length === 0) return;
    setLocalQueuedMessages((prev) => {
      if (prev.length === 0) return prev;
      const serverIds = new Set(serverQueuedMessages.map((m) => m.id));
      return prev.filter((local) => !serverIds.has(local.id));
    });
  }, [serverQueuedMessages]);

  const queuedMessages = useMemo(() => {
    const serverList = (serverQueuedMessages || [])
      .filter((m) => !removedIds.includes(m.id))
      .map((m) => (textOverrides[m.id] !== undefined ? { ...m, text: textOverrides[m.id] } : m));
    if (localQueuedMessages.length === 0) return serverList;
    const serverIds = new Set(serverList.map((m) => m.id));
    const remainingLocal = localQueuedMessages.filter((m) => !serverIds.has(m.id));
    return [...serverList, ...remainingLocal];
  }, [serverQueuedMessages, localQueuedMessages, removedIds, textOverrides]);

  const queuedMessagesRef = useRef(queuedMessages);
  useEffect(() => {
    queuedMessagesRef.current = queuedMessages;
  }, [queuedMessages]);

  const onDispatchRef = useRef(onDispatch);
  useEffect(() => {
    onDispatchRef.current = onDispatch;
  }, [onDispatch]);

  const onStopRef = useRef(onStop);
  useEffect(() => {
    onStopRef.current = onStop;
  }, [onStop]);

  const prevInProgressRef = useRef(isConversationInProgress);
  const isDispatchingRef = useRef(false);

  const toggleExpanded = useCallback(() => {
    setIsExpanded((prev) => !prev);
  }, []);

  const isMaxQueueReached = queuedMessages.length >= maxQueuedMessages;

  const enqueue = useCallback(
    (text: string, images?: QueuedMessageAttachment[], config?: any): string => {
      if (queuedMessagesRef.current.length >= maxQueuedMessages) {
        console.warn('%s', `Cannot enqueue message: queue limit of ${maxQueuedMessages} reached`);
        return '';
      }
      const id = `queued-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`;
      const newMessage: QueuedMessage = {
        id,
        text: text.trim(),
        images: images && images.length > 0 ? images : undefined,
        config,
        createdAt: Date.now(),
        user: 'You',
        isServer: false,
      };
      setLocalQueuedMessages((prev) => [...prev, newMessage]);
      return id;
    },
    [maxQueuedMessages]
  );

  const remove = useCallback(
    async (id: string) => {
      const target = queuedMessagesRef.current.find((m) => m.id === id);
      setError(null);
      if (target?.isServer) {
        if (!accountId || !conversationId) throw new Error('Conversation is unavailable.');
        try {
          await apiAskNudgebee.deleteQueuedMessage({ accountId, conversationId, messageId: id });
        } catch (error) {
          if ((error as { upstreamStatus?: number })?.upstreamStatus === 409) {
            // Refresh only: never retry deletion or treat a conflict as success.
            try {
              await onRefresh?.();
            } catch {
              /* Keep the original operation error. */
            }
          }
          throw error;
        }
        setRemovedIds((prev) => [...prev, id]);
      }
      setLocalQueuedMessages((prev) => prev.filter((msg) => msg.id !== id));
    },
    [accountId, conversationId, onRefresh]
  );

  const update = useCallback(
    async (id: string, text: string) => {
      const target = queuedMessagesRef.current.find((m) => m.id === id);
      const message = text.trim();
      if (!target || !message) return;
      if (!target.isServer) {
        setLocalQueuedMessages((prev) => prev.map((m) => (m.id === id ? { ...m, text: message } : m)));
        return;
      }
      if (!accountId || !conversationId) throw new Error('Conversation is unavailable.');
      setError(null);
      try {
        await apiAskNudgebee.updateQueuedMessage({ accountId, conversationId, messageId: id, message });
      } catch (error) {
        if ((error as { upstreamStatus?: number })?.upstreamStatus === 409) {
          // The message started running or was removed; show the server's current queue.
          try {
            await onRefresh?.();
          } catch {
            /* Keep the original operation error. */
          }
        }
        throw error;
      }
      setTextOverrides((prev) => ({ ...prev, [id]: message }));
    },
    [accountId, conversationId, onRefresh]
  );

  const sendNow = useCallback(
    async (id: string) => {
      const target = queuedMessagesRef.current.find((m) => m.id === id);
      if (!target || isDispatchingRef.current) return;
      isDispatchingRef.current = true;
      setError(null);
      try {
        if (target.isServer) {
          if (!accountId || !conversationId) throw new Error('Conversation is unavailable.');
          await apiAskNudgebee.forceExecuteQueuedMessage({ accountId, conversationId, messageId: id });
          setRemovedIds((prev) => [...prev, id]);
        } else {
          if (isConversationInProgress && onStopRef.current) await onStopRef.current();
          await onDispatchRef.current(target);
          setLocalQueuedMessages((prev) => prev.filter((m) => m.id !== id));
        }
      } catch (e) {
        setError(e instanceof Error ? e.message : 'Unable to send queued message.');
      } finally {
        isDispatchingRef.current = false;
      }
    },
    [isConversationInProgress, accountId, conversationId]
  );

  const clear = useCallback(() => {
    setLocalQueuedMessages([]);
    setRemovedIds([]);
    setTextOverrides({});
    setError(null);
  }, []);

  useEffect(() => {
    clear();
  }, [accountId, conversationId, clear]);

  // Auto-dispatch next queued message when turn transitions from running to COMPLETED.
  // Note: Server-persisted queued messages are auto-claimed by llm-server's background
  // dispatcher when a turn finishes, so we only auto-dispatch local-only messages here.
  useEffect(() => {
    const wasInProgress = prevInProgressRef.current;
    prevInProgressRef.current = isConversationInProgress;

    if (wasInProgress && !isConversationInProgress) {
      if (conversationStatus === 'COMPLETED' && queuedMessages.length > 0 && !isDispatchingRef.current) {
        const [nextMessage] = queuedMessages;
        if (!nextMessage.isServer) {
          isDispatchingRef.current = true;
          setLocalQueuedMessages((prev) => prev.filter((m) => m.id !== nextMessage.id));
          Promise.resolve(onDispatchRef.current(nextMessage)).finally(() => {
            isDispatchingRef.current = false;
          });
        }
      }
    }
  }, [isConversationInProgress, conversationStatus, queuedMessages]);

  return {
    queuedMessages,
    isExpanded,
    setIsExpanded,
    toggleExpanded,
    enqueue,
    remove: async (id) => {
      try {
        await remove(id);
      } catch (e) {
        setError(e instanceof Error ? e.message : 'Unable to delete queued message.');
      }
    },
    error,
    update,
    sendNow,
    clear,
    maxQueuedMessages,
    isMaxQueueReached,
  };
};

export default useQueuedMessages;
