import { useCallback, useEffect, useMemo, useState } from 'react';
import { listProductUpdates, ProductUpdate } from '@api1/product-updates';
import apiUser, { PREFERENCE_PRODUCT_UPDATES_LAST_SEEN } from '@api1/user';

const readLastSeen = (): string | null => {
  if (typeof window === 'undefined') {
    return null;
  }
  const value = apiUser.getUserPreferences()?.[PREFERENCE_PRODUCT_UPDATES_LAST_SEEN];
  return typeof value === 'string' ? value : null;
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
 * client-side "last seen" high-water-mark, stored in the consolidated
 * `nudgebee.userPreferences` localStorage entry (via apiUser). Per-user read
 * state is intentionally not persisted server-side — a single timestamp covers
 * the changelog UX (see the feature's architecture decision).
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

  // Unread = highlightable entries the user hasn't seen yet. The `highlight`
  // flag is the author's control: `highlight: false` entries (historical
  // back-catalog) are shown in the drawer but never badge. A user with no stored
  // high-water-mark (first visit) sees every highlightable entry as unread, so
  // the badge count is exactly what content authors mark `highlight: true`.
  const unreadCount = useMemo(
    () => updates.filter((u) => u.highlight !== false && (!lastSeenAt || u.published_at > lastSeenAt)).length,
    [updates, lastSeenAt]
  );

  const markAllSeen = useCallback(() => {
    if (!newestPublishedAt) {
      return;
    }
    try {
      apiUser.storeUserPreferences(PREFERENCE_PRODUCT_UPDATES_LAST_SEEN, newestPublishedAt);
    } catch {
      /* no-op: badge simply won't persist as cleared */
    }
    setLastSeenAt(newestPublishedAt);
  }, [newestPublishedAt]);

  return { updates, loading, error, unreadCount, lastSeenAt, markAllSeen };
}

export default useProductUpdates;
