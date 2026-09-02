// Product-analytics event emitter.
//
// The only consumer of these events is the EE-bundled Pendo agent
// (src/ee/components/PendoTracking.jsx), which loads on SaaS tenants with
// PENDO_ENABLE=true. This module deliberately does NOT import from src/ee/ —
// that whole tree is stripped in OSS extractions, so call sites live in the
// normal component tree and simply no-op wherever the agent never loaded.
//
// Why `track` is optional-chained separately from `pendo`: the Pendo install
// snippet defines window.pendo immediately as a stub queue, but it only stubs
// initialize/identify/updateOptions/pageLoad. `track` exists only once
// pendo.js itself has downloaded — so the namespace being present says
// nothing about the method being callable. Events fired before that (i.e. in
// the first moments after the agent is initialized) are dropped rather than
// queued; that is acceptable for these UI-interaction counters, which need a
// rendered page to fire at all.
//
// Track Events must also be enabled on the Pendo subscription before they
// appear in the UI — the client-side call is silently discarded otherwise.

declare global {
  interface Window {
    pendo?: {
      track?: (eventName: string, properties?: Record<string, unknown>) => void;
    };
  }
}

export const trackProductEvent = (eventName: string, properties?: Record<string, unknown>): void => {
  if (typeof window === 'undefined') {
    return;
  }
  try {
    window.pendo?.track?.(eventName, properties);
  } catch {
    // Analytics must never break the interaction it is measuring.
  }
};
