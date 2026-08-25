/**
 * Admission queue for panel queries.
 *
 * Panels already wait to be scrolled into view, but "in view" on a dashboard of
 * short stat panels is a dozen of them: opening the GitHub Runners dashboard
 * (30 panels, 616px viewport) admitted 11 and fired 14 `/api/graphql` posts in
 * one burst, each fanning out to a provider behind the gateway.
 *
 * So visibility decides WHETHER a panel loads, and this decides WHEN. A panel
 * takes a slot before its first fetch and gives it back once that fetch settles,
 * so a dashboard fills in waves of `MAX_CONCURRENT` instead of all at once, and
 * a fast scroll past twenty panels queues them rather than launching twenty
 * requests.
 *
 * Deliberately module-level rather than per-dashboard: the cap is about the
 * gateway and the browser's connection pool, both of which are per-tab.
 */

/**
 * Four: enough that the visible rows still fill quickly on a fast connection,
 * few enough that a slow provider cannot leave the whole dashboard spinning at
 * once. Chrome's own per-host HTTP/1.1 cap is six, so this stays under it and
 * leaves room for the page's other requests.
 */
const MAX_CONCURRENT = 4;

/**
 * A slot is force-released after this long even if the panel never reports back.
 * Nothing in the fetch layer times out, so without this one wedged provider
 * request would hold a slot for the life of the page and starve everything
 * queued behind it. Releasing early only relaxes the cap; never releasing would
 * strand panels on a skeleton forever.
 */
const SLOT_TIMEOUT_MS = 15_000;

let active = 0;
const waiting: (() => void)[] = [];

/**
 * Takes a slot, resolving once one is free. The returned release is idempotent —
 * callers release from both the settle path and an effect cleanup, and those two
 * can happen in either order.
 */
export function acquirePanelSlot(): Promise<() => void> {
  return new Promise((resolve) => {
    const grant = () => {
      active += 1;
      let released = false;
      let timer: ReturnType<typeof setTimeout> | undefined;
      const release = () => {
        if (released) return;
        released = true;
        if (timer) clearTimeout(timer);
        active -= 1;
        // Hand the slot straight to whoever has been waiting longest, rather
        // than waiting for their next render to notice it is free.
        waiting.shift()?.();
      };
      timer = setTimeout(release, SLOT_TIMEOUT_MS);
      resolve(release);
    };
    if (active < MAX_CONCURRENT) grant();
    else waiting.push(grant);
  });
}

/** Test seam: how many slots are taken, and how many panels are queued. */
export function panelQueueState(): { active: number; waiting: number } {
  return { active, waiting: waiting.length };
}
