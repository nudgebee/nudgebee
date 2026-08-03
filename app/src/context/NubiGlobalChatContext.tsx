import React, { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react';
import { useRouter } from 'next/router';
import { useData } from '@context/DataContext';
import { getUserSession } from '@lib/auth';
import apiAskNudgebee from '@api1/ask-nudgebee';

const WIP_POLL_MS = 5000;

export const NUBI_GLOBAL_CHAT_SHORTCUT_LABEL = '⌘J';

const NUBI_FULL_PAGE_ROUTES = ['/ask-nudgebee'];

const NUBI_LAUNCHER_HIDDEN_ROUTES = ['/investigate', '/automation/[workflowId]'];

export const isNubiFullPageRoute = (pathname: string): boolean => NUBI_FULL_PAGE_ROUTES.some((route) => pathname.startsWith(route));

export const isNubiLauncherHiddenRoute = (pathname: string): boolean => NUBI_LAUNCHER_HIDDEN_ROUTES.some((route) => pathname.startsWith(route));

export interface NubiChatContext {
  accountId?: string;
  sessionId?: string;
  query?: string;
  source?: string;
  categorySource?: string;
  aboveModal?: boolean;
}

interface NubiGlobalChatContextValue {
  isOpen: boolean;
  open: () => void;
  close: () => void;
  toggle: () => void;
  wipCount: number;
  completedWhileClosed: boolean;
  accountId: string;
  reportWipActivity: () => void;
  chatContext: NubiChatContext | null;
  openWithContext: (context: NubiChatContext) => void;
  clearChatContext: () => void;
  pageAccountId: string;
  // Points the chat at another account without touching the page's filter.
  setChatAccount: (accountId: string) => void;
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

  const isFullPage = isNubiFullPageRoute(router.pathname);

  const [chatContext, setChatContext] = useState<NubiChatContext | null>(null);

  const pageAccountId = useMemo(() => {
    const fromQuery = router.query.accountId;
    const normalized = Array.isArray(fromQuery) ? fromQuery[0] : fromQuery;
    return normalized || selectedCluster?.value || '';
  }, [router.query.accountId, selectedCluster?.value]);

  // A chat-local account wins while it lasts; otherwise the chat follows the page.
  const accountId = chatContext?.accountId || pageAccountId;

  const open = useCallback(() => {
    setHasInteracted(true);
    setIsOpen(true);
  }, []);
  const close = useCallback(() => setIsOpen(false), []);

  const openWithContext = useCallback((context: NubiChatContext) => {
    setChatContext(context);
    setHasInteracted(true);
    setIsOpen(true);
  }, []);

  // "New chat" drops the conversation, not the account — falling back to the page's
  // account would leave the drawer clusterless on the all-accounts listing pages,
  // which is exactly where the chat is opened about a specific row.
  const clearChatContext = useCallback(() => {
    setChatContext((prev) => (prev?.accountId ? { accountId: prev.accountId } : null));
  }, []);

  // Chat-local by design: must NOT touch `selectedCluster` or the URL.
  const setChatAccount = useCallback((nextAccountId: string) => {
    setChatContext({ accountId: nextAccountId });
  }, []);

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

  // Drop any drawer state carried in from the previous page, so the hidden drawer
  // doesn't keep the WIP poll running (and doesn't pop back open on the way out).
  useEffect(() => {
    if (isFullPage) {
      setIsOpen(false);
    }
  }, [isFullPage]);

  useEffect(() => {
    // No shortcut where there's nothing to toggle.
    if (isFullPage) {
      return;
    }
    const handleKeyDown = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && !e.shiftKey && !e.altKey && (e.key === 'j' || e.key === 'J')) {
        e.preventDefault();
        toggle();
      }
    };
    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [toggle, isFullPage]);

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

  const lastPathnameRef = useRef(router.pathname);
  useEffect(() => {
    if (lastPathnameRef.current === router.pathname) {
      return;
    }
    lastPathnameRef.current = router.pathname;
    if (!isOpenRef.current) {
      setChatContext(null);
    }
  }, [router.pathname]);

  // The page's filter outranks a chat-local pick. Both sides must be non-empty so the
  // page account merely resolving isn't mistaken for a switch.
  const lastPageAccountRef = useRef(pageAccountId);
  useEffect(() => {
    const previous = lastPageAccountRef.current;
    if (previous === pageAccountId) {
      return;
    }
    lastPageAccountRef.current = pageAccountId;
    if (previous && pageAccountId) {
      setChatContext(null);
    }
  }, [pageAccountId]);

  useEffect(() => {
    if (!hasInteracted || !accountId) {
      return;
    }
    const email = getUserSession()?.user?.email;
    if (!email) {
      return;
    }

    // Counts are per-account; a stale baseline would read as "a chat finished".
    prevPolledWipRef.current = 0;
    setWipCount(0);

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
    () => ({
      isOpen,
      open,
      close,
      toggle,
      wipCount,
      completedWhileClosed,
      accountId,
      reportWipActivity,
      chatContext,
      openWithContext,
      clearChatContext,
      pageAccountId,
      setChatAccount,
    }),
    [
      isOpen,
      open,
      close,
      toggle,
      wipCount,
      completedWhileClosed,
      accountId,
      reportWipActivity,
      chatContext,
      openWithContext,
      clearChatContext,
      pageAccountId,
      setChatAccount,
    ]
  );

  return <NubiGlobalChatContext.Provider value={value}>{children}</NubiGlobalChatContext.Provider>;
};
