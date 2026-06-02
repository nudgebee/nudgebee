import { useCallback, useEffect, useMemo, useState } from 'react';
import { listProductUpdates, ProductUpdate } from '@api1/product-updates';

const LAST_SEEN_KEY = 'nb.productUpdates.lastSeenAt';

const readLastSeen = (): string | null => {
  if (typeof window === 'undefined') {
    return null;
  }
  try {
    return window.localStorage.getItem(LAST_SEEN_KEY);
  } catch {
    return null;
  }
};

export interface UseProductUpdatesResult {
  updates: ProductUpdate[];
  loading: boolean;
  error: string | null;
  /** Number of updates newer than the client-stored "last seen" high-water-mark. */
  unreadCount: number;
  /** The last-seen timestamp at the time of read (used to flag "New" items). */
  lastSeenAt: string | null;
  /** Move the high-water-mark to the newest update, clearing the unread badge. */
  markAllSeen: () => void;
}

/**
 * Loads the platform-wide product updates and derives an unread count against a
 * client-side "last seen" high-water-mark (localStorage). Per-user read state is
 * intentionally not persisted server-side — a single timestamp covers the
 * changelog UX (see the feature's architecture decision).
 */
export function useProductUpdates(): UseProductUpdatesResult {
  const [updates, setUpdates] = useState<ProductUpdate[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [lastSeenAt, setLastSeenAt] = useState<string | null>(() => readLastSeen());

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const data = await listProductUpdates();
        if (!cancelled) {
          setUpdates(data);
        }
      } catch (err) {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : 'Failed to load product updates');
        }
      } finally {
        if (!cancelled) {
          setLoading(false);
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  // Updates arrive newest-first from the backend.
  const newestPublishedAt = updates[0]?.published_at ?? null;

  // First-ever load (no stored marker): baseline the high-water-mark to the
  // newest update so a freshly onboarded tenant/user starts at 0 unread — only
  // updates published AFTER this first visit will badge. Standard changelog
  // behaviour (Beamer/Headway): the back-catalog stays readable in the drawer,
  // it just isn't flagged "unread". Without this, a new tenant would see every
  // historical update as unread on day one.
  useEffect(() => {
    if (lastSeenAt === null && newestPublishedAt) {
      try {
        window.localStorage.setItem(LAST_SEEN_KEY, newestPublishedAt);
      } catch {
        /* no-op: baseline simply won't persist */
      }
      setLastSeenAt(newestPublishedAt);
    }
  }, [lastSeenAt, newestPublishedAt]);

  // Until the first-load baseline is established (lastSeenAt === null), report
  // no unread rather than treating the entire history as new.
  const unreadCount = useMemo(() => {
    if (!lastSeenAt) {
      return 0;
    }
    // `highlight: false` entries (historical back-catalog) are shown but never
    // counted toward the unread badge.
    return updates.filter((u) => u.highlight !== false && u.published_at > lastSeenAt).length;
  }, [updates, lastSeenAt]);

  const markAllSeen = useCallback(() => {
    if (!newestPublishedAt) {
      return;
    }
    try {
      window.localStorage.setItem(LAST_SEEN_KEY, newestPublishedAt);
    } catch {
      /* no-op: badge simply won't persist as cleared */
    }
    setLastSeenAt(newestPublishedAt);
  }, [newestPublishedAt]);

  return { updates, loading, error, unreadCount, lastSeenAt, markAllSeen };
}

export default useProductUpdates;
