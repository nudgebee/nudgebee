import { Page, Locator, expect } from "@playwright/test";
import { MonitoringTabLocator } from "./MonitoringTabLocator";

export function escapeForRegex(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

// Opens a ds/FilterDropdown `trigger` (e.g. the Limit control) and commits
// `optionLabel`. Same shared-component contract AwsMonitoringLocators.
// chooseFilterOption already relies on: options render as role='option' inside
// the open popover, scoped by :visible since only one such panel is ever open.
export async function chooseFilterDropdownOption(page: Page, trigger: Locator, optionLabel: string): Promise<void> {
  await trigger.click();
  const option = page
    .locator('[role="option"]:visible')
    .filter({ hasText: new RegExp(`^${escapeForRegex(optionLabel)}$`) })
    .first();
  await option.waitFor({ state: "visible", timeout: 10000 });
  await option.click();
}

// The drill-down <tr> CustomTable renders directly after a data row, holding its
// expanded "Log Details" panel once opened. LogResultRows deliberately excludes
// this row (see its comment in MonitoringTabLocator.ts).
function logDrilldownRow(locators: MonitoringTabLocator, rowIndex: number): Locator {
  return locators.LogResultRows.nth(rowIndex).locator("xpath=following-sibling::tr[1]");
}

// Expands one result row. CustomTable's expand trigger is role='button' named
// 'Expand row' (same contract as CloudAccountLocators.ServicesRowExpandButton).
// Waits on the "Message" card — KubernetesLogDetails renders it unconditionally
// for every log line (query.data.message), unlike any specific label field,
// which depends on what this environment's log source actually attaches (a
// live run against dev proved even "app" isn't guaranteed present everywhere).
export async function expandLogRow(locators: MonitoringTabLocator, rowIndex: number): Promise<void> {
  const row = locators.LogResultRows.nth(rowIndex);
  await row.getByRole("button", { name: "Expand row" }).click();
  await logDrilldownRow(locators, rowIndex)
    .getByText("Message", { exact: true })
    .first()
    .waitFor({ state: "visible", timeout: 15000 });
}

// Reads one label's value from an already-expanded row's Log Details panel.
// KubernetesLogDetails (KubernetesTable.jsx renderLabelRow) renders "<key>:" and
// its value as two SEPARATE sibling elements (a label Typography, then a
// value span) inside one shared row container — not one combined text node. A
// live run proved this the hard way: getByText(/^key:/) correctly matches
// only the label element, so reading ITS OWN text back returns just "key:",
// which strips down to an empty string — never the real value, and every
// "does this equal X" check silently compared against "" instead. The value
// lives in the label's next sibling, which this reads instead.
export async function readLogRowField(locators: MonitoringTabLocator, rowIndex: number, fieldKey: string): Promise<string | null> {
  const fieldLabel = logDrilldownRow(locators, rowIndex)
    .getByText(new RegExp(`^${fieldKey}:$`))
    .first();
  const present = await fieldLabel.isVisible().catch(() => false);
  if (!present) return null;
  const valueEl = fieldLabel.locator("xpath=following-sibling::*[1]");
  const text = await valueEl.innerText().catch(() => "");
  const trimmed = text.trim();
  return trimmed || null;
}

// Adds ONE filter chip via the 3-step label -> operator -> value builder, picking
// a SPECIFIC operator rather than always the first suggestion (unlike
// addFirstLogLabelFilter). Re-invoking this after a chip already exists adds a
// SECOND, independent chip — LogQueryBuilderAutocomplete stores chips as a
// growable array (queryItems), and the input resets to the label step after
// each commit.
//
// Wraps the actual attempt with one full restart: a live run showed the value
// step occasionally landing with no visible effect — consistently the THIRD
// add-chip call within the same test.step, after two earlier add+clear round
// trips — Add Operation staying disabled and the typed text just sitting in
// the input. A same-step retry of only the last click/Enter did not recover
// it, so this clears everything and redoes the whole label->operator->value
// sequence from a genuinely clean state instead of guessing at which single
// action was dropped.
export async function addLogFilterChip(locators: MonitoringTabLocator, label: string, operatorLabel: string, value: string): Promise<boolean> {
  const first = await attemptAddLogFilterChip(locators, label, operatorLabel, value);
  if (first) return true;

  console.log(`[addLogFilterChip] first attempt for "${label} ${operatorLabel} ${value}" failed — clearing everything and retrying once from scratch`);
  await clearAllLogFilterChips(locators.LogQueryBuilderInput.page());
  return attemptAddLogFilterChip(locators, label, operatorLabel, value);
}

// Step 2 (operator) does not type into the input: LogQueryBuilderAutocomplete
// lists every operator the provider + label type support as soon as the label is
// chosen, so clicking the one matching `operatorLabel` is the direct path —
// mirrors AwsMonitoringLocators.chooseFilterOption's "click the labelled option".
async function attemptAddLogFilterChip(locators: MonitoringTabLocator, label: string, operatorLabel: string, value: string): Promise<boolean> {
  const input = locators.LogQueryBuilderInput;
  await input.waitFor({ state: "visible", timeout: 20000 });
  await input.click();
  // A prior call's value step can fail to commit and leave typed text sitting
  // in this same input uncommitted (confirmed on a live run: "app NOT LIKE
  // accounting" was still sitting here, uncommitted, THREE steps later,
  // silently confusing every label/operator lookup after it). Clearing via
  // fill (a direct value reset) rather than pressing Escape: Escape was tried
  // first and caused its own regression — it collapses more of the builder
  // panel than a plain re-click can recover from, breaking even the very next,
  // otherwise-healthy call.
  await input.fill("");

  // Step 1 — label. Deliberately NOT typed via input.fill(label): that types
  // instantly, racing the suggestion list's own async (re)fetch/filter — a live
  // run proved this exact race is fatal, timing out on .click() against a node
  // that had already gone stale by the time the click fired, on every label
  // tried. Waiting for the naturally-rendered (unfiltered) list and picking the
  // exact-text match from it avoids the race entirely — MAX_SUGGESTIONS=100
  // keeps the full list on screen without needing to filter down to it.
  await locators.LogQuerySuggestions.first().waitFor({ state: "visible", timeout: 15000 }).catch(() => {});
  const labelOption = locators.LogQuerySuggestions.filter({ hasText: new RegExp(`^${escapeForRegex(label)}$`) }).first();
  const labelFound = await labelOption
    .waitFor({ state: "visible", timeout: 10000 })
    .then(() => true)
    .catch(() => false);
  if (!labelFound) {
    console.log(`[addLogFilterChip] label "${label}" not found in suggestions`);
    return false;
  }
  await labelOption.click();

  // Step 2 — operator, matched by its exact visible chip label (=, !=, LIKE, ...).
  const operatorOption = locators.LogQuerySuggestions.filter({ hasText: new RegExp(`^${escapeForRegex(operatorLabel)}$`) }).first();
  const operatorFound = await operatorOption
    .waitFor({ state: "visible", timeout: 10000 })
    .then(() => true)
    .catch(() => false);
  if (!operatorFound) {
    const visibleOps = await locators.LogQuerySuggestions.allTextContents().catch(() => []);
    console.log(`[addLogFilterChip] operator "${operatorLabel}" not found. Visible suggestions: ${JSON.stringify(visibleOps)}`);
    return false;
  }
  await operatorOption.click();
  if (operatorLabel.includes(" ")) {
    const afterOperatorClick = await input.inputValue().catch(() => "(unreadable)");
    console.log(`[addLogFilterChip] after clicking multi-word operator "${operatorLabel}", input reads: "${afterOperatorClick}"`);
  }

  // Step 3 — value. Typed, then committed via a matching suggestion if the
  // provider offers one for this label (real historical values), falling back
  // to Enter for a value with no such suggestion (a regex pattern, or a
  // deliberately-nonexistent probe value for the "finds nothing" check).
  //
  // pressSequentially, not fill: fill types instantly, racing the same async
  // fetchValuesForLabel + re-filter the label step raced (a live run proved
  // this exact race commits nothing — the input was left holding the typed
  // text with no chip added). Typing with a delay plus an explicit settle
  // pause gives that fetch a real chance to land before the suggestion is
  // checked; the click itself is still wrapped, since a value list that keeps
  // re-rendering as results stream in can detach the node between finding it
  // and clicking it.
  await input.pressSequentially(value, { delay: 30 });
  await input.page().waitForTimeout(1500);
  const valueOption = locators.LogQuerySuggestions.filter({ hasText: new RegExp(`^${escapeForRegex(value)}$`) }).first();
  const valueSuggested = await valueOption
    .waitFor({ state: "visible", timeout: 5000 })
    .then(() => true)
    .catch(() => false);
  if (operatorLabel.includes(" ")) {
    console.log(`[addLogFilterChip] for "${label} ${operatorLabel} ${value}", a matching value suggestion was ${valueSuggested ? "" : "NOT "}found`);
  }
  if (valueSuggested) {
    await valueOption.click().catch(() => input.press("Enter"));
  } else {
    await input.press("Enter");
    // Enter alone was proven not to commit for some multi-word (pattern)
    // operators, even though the label/operator selection lands correctly
    // and the value types in cleanly — Tab is a common alternate "commit the
    // raw typed value" key in autocomplete widgets where Enter is reserved
    // for accepting a highlighted suggestion, which doesn't apply when
    // pressSequentially left nothing highlighted.
    if (!(await locators.LogQueryAddOperationBtn.isEnabled().catch(() => false))) {
      await input.press("Tab").catch(() => {});
    }
    if (!(await locators.LogQueryAddOperationBtn.isEnabled().catch(() => false))) {
      await input.press("Enter").catch(() => {});
    }
    // Last resort: click a neutral, inert area (the "Operations" heading) to
    // force a blur on the builder input — proven necessary specifically for
    // negated pattern operators (NOT LIKE, not icontains), where neither
    // Enter nor Tab commits a value with no matching suggestion, even though
    // the identical Enter-only path commits fine for every other operator.
    if (!(await locators.LogQueryAddOperationBtn.isEnabled().catch(() => false))) {
      const operationsHeading = input.page().getByRole("heading", { name: "Operations" }).first();
      await operationsHeading.click({ timeout: 3000 }).catch(() => {});
    }
  }

  // A live run showed Enter occasionally landing with no visible effect —
  // consistently the THIRD add-chip call in the same test.step, after two
  // earlier add+clear round trips — Add Operation still disabled, the typed
  // text still sitting in the input. One retry (re-check the suggestion,
  // re-commit) absorbs that without guessing at why the first attempt was
  // dropped.
  if (!(await locators.LogQueryAddOperationBtn.isEnabled().catch(() => false))) {
    await input.page().waitForTimeout(500);
    const retryOption = locators.LogQuerySuggestions.filter({ hasText: new RegExp(`^${escapeForRegex(value)}$`) }).first();
    const retrySuggested = await retryOption
      .waitFor({ state: "visible", timeout: 3000 })
      .then(() => true)
      .catch(() => false);
    if (retrySuggested) {
      await retryOption.click().catch(() => {});
    }
    if (!(await locators.LogQueryAddOperationBtn.isEnabled().catch(() => false))) {
      await input.press("Enter");
    }
  }

  const committed = await locators.LogQueryAddOperationBtn.isEnabled().catch(() => false);
  if (!committed) {
    const inputLeftover = await input.inputValue().catch(() => "(unreadable)");
    console.log(`[addLogFilterChip] value step for "${label} ${operatorLabel} ${value}" left Add Operation disabled. Input now reads: "${inputLeftover}"`);
    return false;
  }

  // Verify the chip that actually landed carries the value THIS call meant to
  // commit, not just that some chip exists. The suggestion list can reflow
  // between the exact-text match above and the click actually registering —
  // a live run proved the click can land on a neighbouring, similarly-named
  // suggestion instead, silently committing the wrong value while still
  // reporting success. The chip's own rendered text is the only place that
  // wrong commit would be visible; every later check trusts `value` was what
  // actually got filtered on, so this is what makes that trust safe.
  const committedChip = input
    .page()
    .getByRole("button", { name: `Remove ${label} ${operatorLabel} ${value}`, exact: true })
    .first();
  // Polls rather than an instant isVisible() check: the chip can take a brief
  // moment to render after LogQueryAddOperationBtn already reports enabled.
  const chipMatches = await committedChip
    .waitFor({ state: "visible", timeout: 5000 })
    .then(() => true)
    .catch(() => false);
  if (!chipMatches) {
    const anyChips = await input.page().getByTestId("chip-dismiss").allTextContents().catch(() => []);
    const anyChipNames = await input
      .page()
      .locator('[data-testid="chip-dismiss"]')
      .evaluateAll((els) => els.map((el) => el.getAttribute("aria-label")))
      .catch(() => []);
    console.log(
      `[addLogFilterChip] expected chip "Remove ${label} ${operatorLabel} ${value}" not found. ` +
        `Chips actually present: ${JSON.stringify(anyChipNames)} (raw text: ${JSON.stringify(anyChips)})`
    );
  }
  return chipMatches;
}

// The dismiss button on the chip carrying `labelText` as its visible text
// (e.g. "app = app" — LogQueryBuilderAutocomplete.jsx renders it as exactly
// `${chip.label} ${getOperatorDisplayLabel(chip.operator, ...)} ${chip.value}`,
// single spaces). ds/Chip's onDismiss renders a trailing button carrying
// data-testid="chip-dismiss" and aria-labelled 'Remove <chip label>'
// (Chip.tsx TrailingSlot) — matched directly by role+name rather than via the
// earlier div-nesting search, which was both fragile (depended on guessing
// which ancestor div was the "real" chip wrapper) and, on a live run, matched
// two identically-labelled dismiss buttons at once (a MUI strict-mode
// violation) — `.first()` here resolves that the same way clicking a chip
// in the UI would: whichever one is actually on top.
export function logFilterChip(page: Page, labelText: string): Locator {
  return page.getByRole("button", { name: `Remove ${labelText}`, exact: true }).first();
}

export async function removeLogFilterChip(page: Page, labelText: string): Promise<void> {
  const chip = logFilterChip(page, labelText);
  await chip.click();
  await chip.waitFor({ state: "detached", timeout: 10000 }).catch(() => {});
}

// Clicks every chip-dismiss button currently on screen, repeatedly, until none
// remain — rather than targeting one chip by its exact rendered text. A live
// run showed that when one text-targeted removal doesn't fully land, the
// leftover chip silently corrupts every later addLogFilterChip call in the
// same test (label suggestions, and which operators look "offered", both
// shift once a chip already exists). Since every caller's actual intent at
// each cleanup point is "start the next filter from a clean slate," clearing
// everything achieves that goal without needing to match specific chip text at
// all — sidestepping that whole class of mismatch.
export async function clearAllLogFilterChips(page: Page): Promise<void> {
  const dismissBtns = page.getByTestId("chip-dismiss");
  for (let attempt = 0; attempt < 10; attempt++) {
    const countBefore = await dismissBtns.count();
    if (countBefore === 0) return;
    // Wait on the COUNT dropping, not on .first() detaching: .first() is
    // re-resolved live, so the instant the clicked chip starts to detach it
    // silently repoints at the next (still-attached) chip, and a detached-wait
    // on that always burns its full timeout instead of resolving early.
    await dismissBtns.first().click().catch(() => {});
    await expect(dismissBtns).toHaveCount(countBefore - 1, { timeout: 2000 }).catch(() => {});
  }
}
