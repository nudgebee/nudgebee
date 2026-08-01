/**
 * TourProvider — one driver.js instance for the whole app, exposed through the
 * `useTour()` hook. Mounted once in `_app.tsx` inside the authenticated tree.
 *
 * Why a hand-rolled stepper instead of driver.js's built-in `drive(steps)`:
 * our flows cross an async boundary (a listing button opens a MUI Dialog that
 * mounts later) and have conditionally-rendered fields. We therefore advance
 * with `highlight()` one step at a time so we can (a) await the modal mounting
 * before spotlighting a field and (b) skip optional steps whose element is
 * absent rather than render a detached, centered popover.
 *
 * Keyboard: ←/→ mirror Back/Next via our own document-level listener.
 * driver.js's built-in arrow handling is a no-op in `highlight()` mode — it
 * early-returns unless the steps-mode `activeIndex` state is set, which only
 * `drive(steps)` sets. Escape still closes via driver.js itself.
 */
import * as React from 'react';
import { driver } from 'driver.js';
import { TOURS, brandText, type TourStepDef } from './tours';

type DriverInstance = ReturnType<typeof driver>;

interface TourContextValue {
  /** Start a tour by id (see `TOURS`). No-op if the id is unknown. */
  start: (tourId: string) => void;
  /** Stop the active tour, if any. */
  stop: () => void;
  isActive: boolean;
  activeTourId: string | null;
}

const TourContext = React.createContext<TourContextValue | null>(null);

export function useTour(): TourContextValue {
  const ctx = React.useContext(TourContext);
  if (!ctx) {
    throw new Error('useTour must be used within <TourProvider>');
  }
  return ctx;
}

/**
 * Resolve a selector to an element, waiting up to `timeout` ms for it to mount
 * (a MutationObserver covers async-rendered content like modals). Resolves
 * `null` on timeout so callers can decide whether to skip or center.
 */
function waitForElement(selector: string, timeout: number): Promise<HTMLElement | null> {
  const immediate = document.querySelector<HTMLElement>(selector);
  if (immediate) {
    return Promise.resolve(immediate);
  }

  return new Promise((resolve) => {
    let timer: ReturnType<typeof setTimeout>;
    const observer = new MutationObserver(() => {
      const el = document.querySelector<HTMLElement>(selector);
      if (el) {
        clearTimeout(timer);
        observer.disconnect();
        resolve(el);
      }
    });
    observer.observe(document.body, { childList: true, subtree: true });
    timer = setTimeout(() => {
      observer.disconnect();
      resolve(document.querySelector<HTMLElement>(selector));
    }, timeout);
  });
}

const REQUIRED_WAIT_MS = 5000;
const OPTIONAL_WAIT_MS = 600;

// Elements whose own arrow-key semantics must win over tour navigation: the
// spotlit element stays interactive (disableActiveInteraction: false), so a
// user may be moving a text caret, changing a select/radio/slider value, or
// switching tabs when an arrow key lands.
const ARROW_KEY_ROLES = new Set(['slider', 'spinbutton', 'combobox', 'listbox', 'option', 'menuitem', 'radio', 'tab']);

// driver.js renders progressText with innerHTML, so static markup is safe
// here (no user-supplied strings). Styled in styles/tour.css.
function kbdHint(key: string): string {
  return `<kbd class="nb-tour-kbd">${key}</kbd>`;
}

function ownsArrowKeys(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) {
    return false;
  }
  if (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.tagName === 'SELECT' || target.isContentEditable) {
    return true;
  }
  const role = target.getAttribute('role');
  return role !== null && ARROW_KEY_ROLES.has(role);
}

export function TourProvider({ children }: { children: React.ReactNode }): React.ReactElement {
  const driverRef = React.useRef<DriverInstance | null>(null);
  // Guards against re-entrant navigation: the popover stays clickable while an
  // async step transition is in flight, so a double-click could otherwise fire
  // duplicate side-effects or race two goTo() calls.
  const isTransitioningRef = React.useRef(false);
  const [activeTourId, setActiveTourId] = React.useState<string | null>(null);

  const start = React.useCallback((tourId: string) => {
    const tour = TOURS[tourId];
    if (!tour || tour.steps.length === 0) {
      return;
    }

    // Tear down any in-flight tour before starting a new one.
    driverRef.current?.destroy();
    isTransitioningRef.current = false;

    // A welcome card (shown by the launcher) counts as step 1, so offset the
    // popover progress to stay in sync with it.
    const progressOffset = tour.welcome ? 1 : 0;
    const totalSteps = tour.steps.length + progressOffset;

    const d = driver({
      allowClose: true,
      stagePadding: 6,
      stageRadius: 8,
      overlayColor: 'rgba(15, 23, 42, 0.55)',
      popoverClass: 'nb-tour-popover',
      // The spotlit element is view-only during a tour — clicks/typing on it are
      // blocked so the user can't accidentally submit forms or trigger actions
      // outside the tour's own onBeforeNext side-effects.
      disableActiveInteraction: true,
      onDestroyed: () => {
        document.removeEventListener('keydown', onKeyDown);
        driverRef.current = null;
        setActiveTourId(null);
      },
    });
    driverRef.current = d;
    setActiveTourId(tourId);

    const steps = tour.steps;
    // Index of the step currently on screen, for the keyboard handler. -1
    // until the first step renders.
    let currentIndex = -1;

    // Show step `target`, skipping over optional steps that aren't on screen.
    // `direction` decides which way to skip so Back also lands on a real step.
    const goTo = async (target: number, direction: 1 | -1): Promise<void> => {
      let i = target;
      while (i >= 0 && i < steps.length) {
        // Bail if the tour was torn down while we were awaiting an element.
        if (driverRef.current !== d) {
          return;
        }
        const step: TourStepDef = steps[i];
        const el = await waitForElement(step.element, step.optional ? OPTIONAL_WAIT_MS : REQUIRED_WAIT_MS);
        if (!el) {
          // Optional step that never mounted → skip it. A required step that
          // never mounted means the flow is broken (e.g. modal didn't open), so
          // end the tour cleanly rather than show a detached, centered popover.
          if (step.optional) {
            i += direction;
            continue;
          }
          d.destroy();
          return;
        }
        if (driverRef.current !== d) {
          return;
        }
        renderStep(i, el, step);
        return;
      }
      // Walked off either end → tour complete.
      d.destroy();
    };

    const goPrev = async (from: number): Promise<void> => {
      // No-op on step 1 (the Back button is hidden there, but ArrowLeft isn't):
      // goTo(-1) would walk off the front and destroy the tour.
      if (from <= 0 || isTransitioningRef.current) {
        return;
      }
      isTransitioningRef.current = true;
      try {
        await goTo(from - 1, -1);
      } finally {
        isTransitioningRef.current = false;
      }
    };

    const goNext = async (from: number): Promise<void> => {
      if (isTransitioningRef.current) {
        return;
      }
      isTransitioningRef.current = true;
      try {
        if (from === steps.length - 1) {
          d.destroy();
          return;
        }
        try {
          await steps[from].onBeforeNext?.();
        } catch (err) {
          // Side-effect failed (e.g. trigger missing) — keep advancing so
          // the tour doesn't dead-end. goTo() waits for the next step's
          // element and ends the tour if a required step never mounts.
          console.warn(`[tour] onBeforeNext failed on "${tourId}" step ${from + 1} (${steps[from].element})`, err);
        }
        // No explicit wait here: goTo() already waits for the next step's
        // element to mount before highlighting it.
        await goTo(from + 1, 1);
      } finally {
        isTransitioningRef.current = false;
      }
    };

    const onKeyDown = (e: KeyboardEvent): void => {
      if (e.key !== 'ArrowRight' && e.key !== 'ArrowLeft') {
        return;
      }
      // e.repeat: a held key would otherwise fast-forward through the tour.
      // Modifiers: leave browser/OS shortcuts alone (Alt+Left = history back).
      if (e.repeat || e.altKey || e.ctrlKey || e.metaKey || e.shiftKey || ownsArrowKeys(e.target)) {
        return;
      }
      if (driverRef.current !== d || currentIndex < 0) {
        return;
      }
      e.preventDefault();
      void (e.key === 'ArrowRight' ? goNext(currentIndex) : goPrev(currentIndex));
    };

    const renderStep = (i: number, el: HTMLElement, step: TourStepDef): void => {
      currentIndex = i;
      const isLast = i === steps.length - 1;
      // Bring the target into view before highlighting. driver.js scrolls too,
      // but its window-based check misses elements clipped inside a scrollable
      // modal body (e.g. a button below a long form); scrollIntoView walks every
      // scrollable ancestor. 'nearest' is a no-op for already-visible elements,
      // so page-level anchors (sidebar, header) don't scroll unexpectedly.
      el.scrollIntoView({ block: 'nearest' });
      d.highlight({
        element: el,
        popover: {
          title: brandText(step.title),
          description: brandText(step.description),
          side: step.side ?? 'bottom',
          align: step.align ?? 'start',
          // highlight() defaults showProgress to false (unlike drive(steps)),
          // so it must be opted into or progressText never renders.
          showProgress: true,
          // ← / → chips flank the count, each sitting next to the button it
          // mirrors. No ← on step 1 — Back is hidden there and ArrowLeft is a
          // no-op.
          progressText: `${i > 0 ? `${kbdHint('←')} ` : ''}${i + 1 + progressOffset} of ${totalSteps} ${kbdHint('→')}`,
          // No Back on step 1 — there's nowhere to go back to.
          showButtons: i === 0 ? ['next', 'close'] : ['next', 'previous', 'close'],
          nextBtnText: isLast ? 'Done' : 'Next',
          prevBtnText: 'Back',
          onPrevClick: () => {
            void goPrev(i);
          },
          onCloseClick: () => {
            d.destroy();
          },
          onNextClick: () => {
            void goNext(i);
          },
        },
      });
    };

    document.addEventListener('keydown', onKeyDown);
    void goTo(0, 1);
  }, []);

  const stop = React.useCallback(() => {
    driverRef.current?.destroy();
  }, []);

  // Destroy any active tour on unmount so a stray overlay can't outlive the app.
  React.useEffect(() => {
    return (): void => {
      driverRef.current?.destroy();
    };
  }, []);

  const value = React.useMemo<TourContextValue>(() => ({ start, stop, isActive: activeTourId !== null, activeTourId }), [start, stop, activeTourId]);

  return <TourContext.Provider value={value}>{children}</TourContext.Provider>;
}
