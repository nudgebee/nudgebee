# Guided Tours

A lightweight, reusable in-app product-tour engine. It spotlights real UI
elements one at a time ("click here → then here") to walk a user through a task.

Built on [driver.js](https://driverjs.com/) (MIT, ~5kb, framework-agnostic).
The library only draws the overlay/spotlight/popover; this module owns the
_flow logic_ — which element, in what order, and when to advance across async
boundaries (e.g. a button that opens a modal that mounts later).

## Files

| File                    | Role                                                                                                           |
| ----------------------- | -------------------------------------------------------------------------------------------------------------- |
| `tours.ts`              | **Registry.** Tours as plain data — an ordered list of steps. No logic.                                        |
| `TourProvider.tsx`      | **Engine.** One driver.js instance per run + the `useTour()` hook + the async-aware stepper.                   |
| `tourAccess.ts`         | **Gate.** `canAccessTour(tour)` — resolves `requires` / `requiresFeature` / `requiresUiFeature`. See _Gating_. |
| `useLaunchGuide.ts`     | `launch(tourId)` — navigates to the tour's `route` first, then starts it.                                      |
| `TourLauncher.jsx`      | The contextual "How to …" ghost button. Self-gates via `canAccessTour`.                                        |
| `index.ts`              | Barrel export (`TourProvider`, `useTour`, `TOURS`, `canAccessTour`, …).                                        |
| `../../styles/tour.css` | Brand styling, scoped to `.nb-tour-popover`. Global CSS, imported in `_app.tsx`.                               |

Four surfaces launch guides, and **all four gate through `canAccessTour`**: the
central catalog (`onboarding/GuidesMenu.jsx`), the contextual `TourLauncher`,
the first-visit offer (`onboarding/SectionFirstVisitTour.jsx`), and the
product-updates drawer (`common/widgets/ProductUpdatesDrawerContent.tsx`).

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

3. **Gate it to match the button it drives** — set `requires` (and
   `requiresFeature`) by reading the target button's actual gate expression. See
   [_Gating a guide_](#gating-a-guide-permissions); the defaults are easy to get
   subtly wrong.

4. **Add a launcher** wherever it makes sense — or nothing at all: giving the
   tour a `module` is enough for it to appear in the central Guides catalog
   automatically.

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

## Gating a guide (permissions)

A guide walks a user through _doing_ something, so it should be offered to
exactly the users who can do it — no more, no less. `canAccessTour` in
`tourAccess.ts` enforces this, and every launch surface calls it.

**The rule: mirror the gate on the button your guide drives.** Open the target
component, read its gate expression, and pick the matching capability. Getting
this wrong fails in one of two directions — both have shipped:

| `requires`        | Resolves to         | Use when the button is gated on | Effectively                  |
| ----------------- | ------------------- | ------------------------------- | ---------------------------- |
| _omitted_         | always true         | nothing — a read-only tour      | everyone                     |
| `'write'`         | `hasWriteAccess()`  | `hasWriteAccess()` (no args)    | **tenant_admin only**        |
| `'account-write'` | write on ≥1 account | `hasWriteAccess(accountId)`     | tenant_admin + account_admin |

> **`hasWriteAccess()` with no args is not "any write access" — it is
> `isTenantAdmin()`.** With no `accountId`, the only branch that can return true
> is the `tenant_admin` one (`accountIds.includes(undefined)` is always false).
> This is the trap: an account-scoped button (`hasWriteAccess(accountId)`) paired
> with `requires: 'write'` hides the guide from the `account_admins` who _can see
> the button_ — a live button with no guide.

`'account-write'` is an **any-account** check, because the Guides catalog is
global and can't know which account the guide will land on. An account_admin of
account X browsing account Y still sees the guide; the tour then ends cleanly
when the gated button doesn't mount. That's the lesser evil.

### `requiresFeature` — opt-in flags only

`requiresFeature: '<feature_id>'` hides the guide unless the tenant flag is on.
It resolves via `hasFeatureAccessCached`, which is **fail-closed**: it needs an
explicit `status === 'enabled'` row.

> **Never use it for an opt-OUT (default-on) flag.** Those tenants have _no row_,
> which reads as "off" — hiding the guide from everyone who has the feature. If
> the surface renders for everyone by default, gate the guide on nothing. (This
> is why the Knowledge Graph guide is ungated despite
> `TRACES_SERVICE_MAP_KNOWLEDGE_GRAPH` existing.)

Two more traps:

- **Warm the cache.** `hasFeatureAccessCached` returns false until
  `fetchFeatureFlagsForTenant()` resolves. `GuidesMenu`, `SectionFirstVisitTour`
  and the product-updates drawer warm it and re-render; **a bare `TourLauncher`
  does not** — so don't put `requiresFeature` on a guide launched only from one.
- **Env vars are not feature flags.** `UI_ENABLE_LLM_GATEWAY` is a
  per-**deployment** `process.env` value, not a tenant `feature_id`, so it never
  reaches `featureflags_list` and `requiresFeature` cannot gate on it. Use
  `requiresUiFeature` instead.

### `requiresUiFeature` — deployment-level toggles

`requiresUiFeature: 'llmGateway'` hides the guide unless the deployment has that
`UI_ENABLE_*` env var on. Use it when a surface is gated on a `UI_ENABLE_*` var —
the AI Gateway tab is the current case. (The LLM Analyser tab used to be one too,
but is now gated on the tenant `LLM_ANALYSER` feature flag via `requiresFeature`
instead — see `llmAnalyserTour` in `tours.ts`.)

The values reach the client on the **session** (`uiFeatures` in `[...nextauth].ts`),
read from `process.env` in the session callback — which runs server-side on every
session fetch, so flipping the pod's env takes effect without a rebuild or a
re-login. `isUiFeatureEnabled` reads them synchronously; unlike `requiresFeature`,
**there is nothing to warm**.

> **Don't promote these via `next.config.js` `env:`.** That inlines at _build_
> time and would freeze the value into the image, breaking per-environment
> toggling — these arrive at runtime from the pod's secret. (`NUDGEBEE_DEPLOYMENT_MODE`
> is in that block precisely because it _is_ build-time.)

Which gate do you need?

| The surface is gated on…                | Use                 | Per        |
| --------------------------------------- | ------------------- | ---------- |
| a role (`hasWriteAccess…`)              | `requires`          | user       |
| a tenant flag (`hasFeatureAccess('X')`) | `requiresFeature`   | tenant     |
| a `UI_ENABLE_*` env var                 | `requiresUiFeature` | deployment |

The rules above are pinned by `__tests__/components/common/tour/tourAccess.test.ts`.

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
