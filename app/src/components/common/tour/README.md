# Guided Tours

A lightweight, reusable in-app product-tour engine. It spotlights real UI
elements one at a time ("click here → then here") to walk a user through a task.

Built on [driver.js](https://driverjs.com/) (MIT, ~5kb, framework-agnostic).
The library only draws the overlay/spotlight/popover; this module owns the
_flow logic_ — which element, in what order, and when to advance across async
boundaries (e.g. a button that opens a modal that mounts later).

## Files

| File                    | Role                                                                                         |
| ----------------------- | -------------------------------------------------------------------------------------------- |
| `tours.ts`              | **Registry.** Tours as plain data — an ordered list of steps. No logic.                      |
| `TourProvider.tsx`      | **Engine.** One driver.js instance per run + the `useTour()` hook + the async-aware stepper. |
| `index.ts`              | Barrel export (`TourProvider`, `useTour`, `TOURS`).                                          |
| `../../styles/tour.css` | Brand styling, scoped to `.nb-tour-popover`. Global CSS, imported in `_app.tsx`.             |

The provider is mounted once in [`_app.tsx`](../../../pages/_app.tsx) inside the
authenticated tree, so any component can launch a tour.

## Mental model

```
registry (data)  →  useTour().start(id)  →  driver.js (draws overlay/popover)
                              ↑
                  stepper: waitForElement + goTo
                  (async mounts, optional steps, re-entrancy lock)
```

The reusable engine is ~100 lines in `TourProvider.tsx`. Adding a tour never
requires touching it.

## Launch a tour

```tsx
import { useTour } from '@components/common/tour';

function MyToolbar() {
  const { start } = useTour();
  return <Button onClick={() => start('create-user')}>How to add a user</Button>;
}
```

`useTour()` also exposes `stop()`, `isActive`, and `activeTourId`.

## Add a new tour

1. **Make sure each element you want to spotlight has a stable `id` or
   `data-testid`.** The engine anchors to selectors; it never reaches into
   component internals. Reuse ids that already ship where possible (the
   `create-user` tour needed zero changes to `UserModal` for this reason).

2. **Append a tour to `TOURS` in `tours.ts`** (hypothetical "invite a team member" shown — see `createUserTour` / `connectClusterTour` for the real ones):

   ```ts
   const inviteTeamTour: TourDef = {
     id: 'invite-team',
     title: 'Invite your team',
     steps: [
       {
         element: '#invite-btn',
         title: 'Invite a teammate',
         description: 'Start here — click Next and we’ll open the form.',
         side: 'bottom',
         align: 'end',
         // Side-effect run when advancing FROM this step. The engine then
         // waits for the NEXT step's element to mount before highlighting it.
         onBeforeNext: () => document.querySelector<HTMLElement>('#invite-btn')?.click(),
       },
       { element: '#invite-email', title: 'Email', description: '…' },
       { element: '#invite-role', title: 'Role', description: '…', optional: true },
       { element: '#invite-submit', title: 'Send', description: 'Click to finish.', side: 'top' },
     ],
   };

   export const TOURS = {
     [createUserTour.id]: createUserTour,
     [connectClusterTour.id]: connectClusterTour,
     [inviteTeamTour.id]: inviteTeamTour,
   };
   ```

3. **Add a launcher** wherever it makes sense (`useTour().start('invite-team')`).

> Tip: if advancing a step has a _destructive or irreversible_ side-effect (e.g.
> a "Next" that actually creates a record, like the cluster-connect "Next"),
> make it the **last** step and just point at the button — don't auto-click it
> via `onBeforeNext`. Let the user pull the trigger.

That's it — no engine changes.

## Step schema (`TourStepDef`)

| Field                   | Required | Purpose                                                                                                                                                          |
| ----------------------- | -------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `element`               | ✓        | CSS selector for the element to spotlight.                                                                                                                       |
| `title` / `description` | ✓        | Popover copy.                                                                                                                                                    |
| `side` / `align`        |          | Popover placement relative to the element.                                                                                                                       |
| `onBeforeNext`          |          | Side-effect to run when advancing _from_ this step (e.g. click a button to open a modal). Awaited. The engine then waits for the next step's `element` to mount. |
| `optional`              |          | If the element isn't on screen, **skip** the step instead of ending the tour (e.g. a field that only renders under certain tenant config).                       |

## How the engine works (the two helpers)

- **`waitForElement(selector, timeout)`** — resolves the element, waiting up to
  `timeout` ms via a `MutationObserver` so async-mounted DOM (modals) is handled.
  Resolves `null` on timeout.
- **`goTo(index, direction)`** — shows one step at a time. For each step: wait
  for its element → if missing & `optional`, skip in the travel direction; if
  missing & required, end the tour cleanly; otherwise `highlight()` it.
  Advancing is driven by the popover's `onNextClick`, which runs `onBeforeNext`
  then calls `goTo(i + 1)`.

Two safety details: a re-entrancy lock (`isTransitioningRef`) ignores clicks
while an async transition is in flight (no duplicate side-effects), and
`driverRef.current !== d` checks bail out if the tour was closed mid-`await`.

## Why a hand-rolled stepper instead of driver's `drive(steps)`

driver's built-in multi-step mode resolves every element up front and can't
(a) wait for a modal to mount between steps, or (b) skip a conditionally-
rendered step. Stepping manually with `highlight()` buys both.

## Theming

One `popoverClass: 'nb-tour-popover'` + scoped rules in `tour.css` using DS
tokens (`--ds-brand-*`). Don't restyle `.driver-popover` globally — keep it
under `.nb-tour-popover` so other driver usage (if any) is unaffected.

## Known limitations / gotchas

- **Single page + its modal only.** The engine drives one DOM context. A flow
  that spans page navigations (route A → route B mid-tour) is **not** supported
  yet — see _Extending_ below.
- **Keyboard inside a MUI Dialog.** MUI's focus-trap pulls focus back from the
  popover, so Tab/Enter onto popover buttons doesn't work inside a dialog
  (mouse does). To support keyboard, pass `disableEnforceFocus` to the dialog
  while a tour is active.
- **Optional-step timing.** An `optional` step waits only briefly
  (`OPTIONAL_WAIT_MS`); if its element is still loading it gets skipped. Fine
  for non-essential fields; don't mark a slow-loading critical step optional.

## Extending: cross-page resume (for onboarding)

To let a tour span routes (e.g. connect cluster → land on dashboard → invite
team), persist the position and auto-resume on the destination page:

1. On each step, persist `{ tourId, stepIndex }` (localStorage via
   `apiUserManagement.storeUserPreferences`, or a backend field for
   cross-device).
2. In `onBeforeNext`, trigger the navigation (`router.push(...)`) instead of a
   DOM click.
3. In `TourProvider`, on mount/route-change, read the persisted position and
   resume the tour at that step.

This is additive — the registry and step schema don't change.
