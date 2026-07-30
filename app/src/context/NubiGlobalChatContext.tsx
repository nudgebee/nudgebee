import React, { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react';
import { useRouter } from 'next/router';
import { useData } from '@context/DataContext';
import { getUserSession } from '@lib/auth';
import apiAskNudgebee from '@api1/ask-nudgebee';

const WIP_POLL_MS = 5000;

export const NUBI_GLOBAL_CHAT_SHORTCUT_LABEL = '⌘J';

interface NubiGlobalChatContextValue {
  isOpen: boolean;
  open: () => void;
  close: () => void;
  toggle: () => void;
  wipCount: number;
  completedWhileClosed: boolean;
  accountId: string;
  reportWipActivity: () => void;
}

const NubiGlobalChatContext = createContext<NubiGlobalChatContextValue | null>(null);

export const useNubiGlobalChat = (): NubiGlobalChatContextValue => {
  const ctx = useContext(NubiGlobalChatContext);
  if (!ctx) {
    throw new Error('useNubiGlobalChat must be used within a NubiGlobalChatProvider');
  }
  return ctx;
};

// Non-throwing variant — the generator is reused outside the global chat layer.
export const useOptionalNubiGlobalChat = (): NubiGlobalChatContextValue | null => useContext(NubiGlobalChatContext);

export const NubiGlobalChatProvider: React.FC<{ children: React.ReactNode }> = ({ children }) => {
  const router = useRouter();
  const { selectedCluster } = useData();

  const [isOpen, setIsOpen] = useState(false);
  const [wipCount, setWipCount] = useState(0);
  const [completedWhileClosed, setCompletedWhileClosed] = useState(false);
  const [hasInteracted, setHasInteracted] = useState(false);

  const accountId = useMemo(() => {
    const fromQuery = router.query.accountId;
    const normalized = Array.isArray(fromQuery) ? fromQuery[0] : fromQuery;
    return normalized || selectedCluster?.value || '';
  }, [router.query.accountId, selectedCluster?.value]);

  const open = useCallback(() => {
    setHasInteracted(true);
    setIsOpen(true);
  }, []);
  const close = useCallback(() => setIsOpen(false), []);
  const toggle = useCallback(() => {
    setHasInteracted(true);
    setIsOpen((prev) => !prev);
  }, []);

  const reportWipActivity = useCallback(() => {
    setWipCount((prev) => Math.max(1, prev));
  }, []);

  useEffect(() => {
    if (isOpen) {
      setCompletedWhileClosed(false);
    }
  }, [isOpen]);

  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && !e.shiftKey && !e.altKey && (e.key === 'j' || e.key === 'J')) {
        e.preventDefault();
        toggle();
      }
    };
    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [toggle]);

  const isOpenRef = useRef(isOpen);
  const wipCountRef = useRef(wipCount);
  // Last count the poll actually saw — compared to detect completions; kept
  // separate from the optimistic reportWipActivity bump to avoid a false "ready".
  const prevPolledWipRef = useRef(0);
  useEffect(() => {
    isOpenRef.current = isOpen;
  }, [isOpen]);
  useEffect(() => {
    wipCountRef.current = wipCount;
  }, [wipCount]);

  useEffect(() => {
    if (!hasInteracted || !accountId) {
      return;
    }
    const email = getUserSession()?.user?.email;
    if (!email) {
      return;
    }

    let cancelled = false;
    let timer: ReturnType<typeof setTimeout> | null = null;

    const tick = async () => {
      if (isOpenRef.current || wipCountRef.current > 0) {
        try {
          const res = await apiAskNudgebee.llmConversationHistory({
            account_id: accountId,
            user_username: email,
            status: 'IN_PROGRESS',
            skipTotalCount: true,
            limit: 50,
          });
          const rows = res?.data?.data?.llm_conversations ?? [];
          const newCount = rows.length;
          if (!cancelled) {
            if (!isOpenRef.current && newCount < prevPolledWipRef.current) {
              setCompletedWhileClosed(true);
            }
            prevPolledWipRef.current = newCount;
            setWipCount(newCount);
          }
        } catch {
          // Transient poll failures are non-fatal.
        }
      }
      if (!cancelled) {
        timer = setTimeout(tick, WIP_POLL_MS);
      }
    };

    tick();

    return () => {
      cancelled = true;
      if (timer) {
        clearTimeout(timer);
      }
    };
  }, [hasInteracted, accountId]);

  const value = useMemo<NubiGlobalChatContextValue>(
    () => ({ isOpen, open, close, toggle, wipCount, completedWhileClosed, accountId, reportWipActivity }),
    [isOpen, open, close, toggle, wipCount, completedWhileClosed, accountId, reportWipActivity]
  );

  return <NubiGlobalChatContext.Provider value={value}>{children}</NubiGlobalChatContext.Provider>;
};
