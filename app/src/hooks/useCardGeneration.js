import { useRef, useCallback } from 'react';

/**
 * Hook that manages the card rendering loop lifecycle with proper cleanup.
 *
 * Provides:
 * - Tracked timeouts that are automatically cleared on cancel/unmount
 * - Cancellation token that stops the async loop
 * - Safe state update wrappers that check cancellation before updating
 *
 * Usage:
 *   const { startGeneration, cancelGeneration } = useCardGeneration();
 *
 *   // In your useEffect:
 *   const { isCancelled, trackTimeout, safeSetState } = startGeneration();
 *   // ... async card loop using isCancelled(), trackTimeout(), safeSetState(setter, value)
 *
 *   return () => cancelGeneration();
 */
export const useCardGeneration = () => {
  const isCancelledRef = useRef(false);
  const pendingTimeoutsRef = useRef([]);

  const cancelGeneration = useCallback(() => {
    isCancelledRef.current = true;
    pendingTimeoutsRef.current.forEach((item) => {
      clearTimeout(item.id);
      item.reject?.(new Error('generation cancelled'));
    });
    pendingTimeoutsRef.current = [];
  }, []);

  const startGeneration = useCallback(() => {
    // Cancel any previous generation
    cancelGeneration();
    isCancelledRef.current = false;

    const isCancelled = () => isCancelledRef.current;

    const trackTimeout = (fn, delay) => {
      const id = setTimeout(() => {
        pendingTimeoutsRef.current = pendingTimeoutsRef.current.filter((item) => item.id !== id);
        fn();
      }, delay);
      pendingTimeoutsRef.current.push({ id });
      return id;
    };

    const safeSetState = (setter, value) => {
      if (!isCancelledRef.current) {
        setter(value);
      }
    };

    const safeDelay = (ms) =>
      new Promise((resolve, reject) => {
        if (isCancelledRef.current) {
          reject(new Error('generation cancelled'));
          return;
        }
        const id = setTimeout(() => {
          pendingTimeoutsRef.current = pendingTimeoutsRef.current.filter((item) => item.id !== id);
          resolve();
        }, ms);
        pendingTimeoutsRef.current.push({ id, reject });
      });

    return { isCancelled, trackTimeout, safeSetState, safeDelay };
  }, [cancelGeneration]);

  return { startGeneration, cancelGeneration };
};
