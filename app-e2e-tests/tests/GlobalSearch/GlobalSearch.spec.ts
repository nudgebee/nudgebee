// Not for OSS
import { test, expect } from "@playwright/test";
import {
  LANDING_PATH,
  escapeForRegExp,
  expectPanelClosed,
  gotoSearchHost,
  noMatchTerm,
  openGlobalSearch,
  reopenGlobalSearch,
  requireClusterName,
  searchFor,
} from "./globalSearchHelper";

// Header global search (Ctrl/Cmd+K) — app/src/components/common/navigation/GlobalPageSearch.jsx.
//
// Everything here is read-only. The panel's only write is the recent-picks list, which
// is per-browser localStorage ('nudgebee.userPreferences'), so nothing a test does here
// is visible to anyone else on the shared dev tenant. The two "Ask <assistant>" buttons
// are asserted but never clicked: pressing either creates a real AI investigation
// session against the tenant.
//
// Expected rows below come from the static index in app/src/lib/navSearchPages.ts. Only
// ungated groups are used — Admin rows are hidden without admin access, and LLM
// Analyser / AI Gateway are feature- and env-flagged, so none of them can anchor an
// assertion that has to hold on any dev tenant.
const SPEC_TIMEOUT_MS = 180000;

test("Global Search sanity - open the header search from its trigger button, verify the query input, results list and keyboard hint bar render", { tag: ["@dev", "@sanity", "@functional"] }, async ({ page }) => {
  test.setTimeout(SPEC_TIMEOUT_MS);
  const search = await openGlobalSearch(page);

  await expect(search.searchInput).toBeVisible();
  await expect(search.optionsList).toBeVisible();
  await expect(search.closeBtn).toBeVisible();

  await test.step("The unscoped placeholder advertises the @account shortcut", async () => {
    await expect(search.searchInput).toHaveAttribute("placeholder", /type @ for an account/);
  });

  await test.step("The hint bar carries the arrow-key hint and the acronym tip", async () => {
    await expect(search.footerHints).toBeVisible();
    await expect(search.footerHints).toContainText("Navigate");
    // The tip names the same "umu" example the acronym test below exercises.
    await expect(search.footerHints).toContainText("umu");
  });

  await test.step("An unfiltered panel already offers pages to jump to", async () => {
    // The static index is ~200 rows and each section renders at most five of them, so
    // any authenticated tenant lands here with rows — an empty list means the index
    // never built, not that this tenant happens to be empty.
    expect(await search.optionRows.count()).toBeGreaterThan(0);
  });
});

test("Global Search sanity - press Ctrl+K, verify the panel opens, press Ctrl+K again, verify the panel closes", { tag: ["@dev", "@sanity", "@functional"] }, async ({ page }) => {
  test.setTimeout(SPEC_TIMEOUT_MS);
  const search = await gotoSearchHost(page);

  await expect(search.searchInput).toBeHidden();

  await page.keyboard.press("Control+k");
  await expect(search.searchInput).toBeFocused();
  await expect(search.optionsList).toBeVisible();

  await page.keyboard.press("Control+k");
  await expectPanelClosed(search);
});

test("Global Search - open the search, type \"Knowledge Graph\", verify the Knowledge Graph row is listed under the Suggested Pages section", { tag: ["@dev", "@smoke", "@search", "@functional"] }, async ({ page }) => {
  test.setTimeout(SPEC_TIMEOUT_MS);
  const search = await openGlobalSearch(page);

  await searchFor(search, "Knowledge Graph");

  const row = search.searchResultRow("Knowledge Graph");
  await expect(row).toBeVisible();
  // The right-aligned path chip is the row's own destination, so asserting it proves
  // the match resolved to the Troubleshoot page rather than a same-named row.
  await expect(row).toContainText("/troubleshoot/kg");
  await expect(search.sectionCaption("Suggested Pages")).toBeVisible();
});

test("Global Search - open the search, type the path acronym \"vs\", verify the VM Summary row is offered for vm/summary", { tag: ["@dev", "@regression", "@search"] }, async ({ page }) => {
  test.setTimeout(SPEC_TIMEOUT_MS);
  const search = await openGlobalSearch(page);

  // "vs" appears nowhere in the label "VM Summary" or in its path, so a hit here can
  // only come from pathAcronym('vm/summary') — that is what this asserts.
  await searchFor(search, "vs");

  const row = search.searchResultRow("VM Summary");
  await expect(row).toBeVisible();
  await expect(row).toContainText("/vm/summary");
});

test("Global Search - open the search, type the misspelled query \"knowlege graph\", verify the Knowledge Graph row is still offered", { tag: ["@dev", "@regression", "@search"] }, async ({ page }) => {
  test.setTimeout(SPEC_TIMEOUT_MS);
  const search = await openGlobalSearch(page);

  // "knowlege" is one edit from "knowledge" and eight characters long, so it is inside
  // fuzzyTokenMatches' distance budget (2 for tokens over five characters).
  await searchFor(search, "knowlege graph");

  await expect(search.searchResultRow("Knowledge Graph")).toBeVisible();
});

test("Global Search - open the search, type the words out of order as \"rules triage\", verify the Troubleshoot Triage Rules row is offered", { tag: ["@dev", "@regression", "@search"] }, async ({ page }) => {
  test.setTimeout(SPEC_TIMEOUT_MS);
  const search = await openGlobalSearch(page);

  await searchFor(search, "rules triage");

  const row = search.searchResultRow("Troubleshoot Triage Rules");
  await expect(row).toBeVisible();
  await expect(row).toContainText("/troubleshoot/all-events/triage-rules");
});

test("Global Search - open the search, type the wildcard query \"optimi*resolutions\", verify the Optimize Resolutions row is offered", { tag: ["@dev", "@regression", "@search"] }, async ({ page }) => {
  test.setTimeout(SPEC_TIMEOUT_MS);
  const search = await openGlobalSearch(page);

  await searchFor(search, "optimi*resolutions");

  const row = search.searchResultRow("Optimize Resolutions");
  await expect(row).toBeVisible();
  await expect(row).toContainText("/optimise/resolutions");
});

test("Global Search - open the search, type \"Optimize Configuration\", click the result, verify the app lands on the Optimize Configuration tab and Back returns to the previous page", { tag: ["@dev", "@smoke", "@functional"] }, async ({ page }) => {
  test.setTimeout(SPEC_TIMEOUT_MS);
  const search = await openGlobalSearch(page);

  await searchFor(search, "Optimize Configuration");
  await search.searchResultRow("Optimize Configuration").click();

  await expect(page).toHaveURL(/\/optimise(\?[^#]*)?#configuration/);
  await expectPanelClosed(search);

  await test.step("Browser Back returns to the page the search was opened from", async () => {
    await page.goBack();
    await expect(page).toHaveURL(new RegExp(`${escapeForRegExp(LANDING_PATH)}`));
  });
});

test("Global Search - open the search, type \"all-events/threshold-suggestions\", press ArrowDown then Enter, verify the app lands on the Alert Tuning page", { tag: ["@dev", "@regression", "@functional"] }, async ({ page }) => {
  test.setTimeout(SPEC_TIMEOUT_MS);
  const search = await openGlobalSearch(page);

  // The full fragment path narrows the list to Alert Tuning's own row, so the first
  // Arrow-reachable entry is known without depending on how the dev tenant ranks.
  await searchFor(search, "all-events/threshold-suggestions");
  await expect(search.optionRows.first()).toContainText("Alert Tuning");

  await page.keyboard.press("ArrowDown");
  await page.keyboard.press("Enter");

  await expect(page).toHaveURL(/\/troubleshoot(\?[^#]*)?#all-events\/threshold-suggestions/);
  await expectPanelClosed(search);
});

test("Global Search - pick Optimize Resolutions from the search, reload the page, reopen the search, verify the pick persists as the first Recents row", { tag: ["@dev", "@regression", "@functional"] }, async ({ page }) => {
  test.setTimeout(SPEC_TIMEOUT_MS);
  const search = await openGlobalSearch(page);

  await searchFor(search, "Optimize Resolutions");
  await search.searchResultRow("Optimize Resolutions").click();
  await expect(page).toHaveURL(/\/optimise(\?[^#]*)?#resolutions/);

  await test.step("The pick survives a full page reload, not just the open panel", async () => {
    // Recents is written to localStorage ('nudgebee.userPreferences', keyed by tenant)
    // and re-read on every open — a reload is what separates a stored pick from one
    // that only ever lived in component state.
    await page.reload();
    await reopenGlobalSearch(search);

    await expect(search.sectionCaption("Recents")).toBeVisible();
    // Most-recent-first, and Recents is the first section, so the pick is row zero.
    await expect(search.optionRows.first()).toContainText("Optimize Resolutions");
  });
});

test("Global Search - open the search, type a term no page can match, verify no result rows are listed and the Ask assistant hand-off stays offered", { tag: ["@dev", "@regression", "@negative", "@search"] }, async ({ page }) => {
  test.setTimeout(SPEC_TIMEOUT_MS);
  const search = await openGlobalSearch(page);

  await searchFor(search, noMatchTerm());

  await expect(search.optionRows).toHaveCount(0);
  // Outside mention mode the empty list renders no message of its own — the persistent
  // Ask button beside the input is the only offered next step, so its absence would
  // leave the query a dead end. Asserted, never clicked: clicking creates a real AI
  // session against the tenant.
  await expect(search.askAiTopBtn).toBeVisible();
});

test("Global Search - open the search, dismiss it with the close button, verify the panel is gone and the page did not navigate", { tag: ["@dev", "@regression", "@functional"] }, async ({ page }) => {
  test.setTimeout(SPEC_TIMEOUT_MS);
  const search = await openGlobalSearch(page);

  const urlBeforeClose = page.url();
  await searchFor(search, "Optimize Resolutions");
  await expect(search.searchResultRow("Optimize Resolutions")).toBeVisible();

  await search.closeBtn.click();

  await expectPanelClosed(search);
  await test.step("Dismissing is a true cancel — the typed query navigates nowhere", async () => {
    expect(page.url()).toBe(urlBeforeClose);
  });

  await test.step("Reopening starts from an empty query, not the dismissed one", async () => {
    await reopenGlobalSearch(search);
    await expect(search.searchInput).toHaveValue("");
  });
});

test("Global Search - open the search, type @ to scope by account, pick the configured cluster, verify its name is pinned into the search placeholder", { tag: ["@dev", "@regression", "@search", "@functional"] }, async ({ page }) => {
  test.setTimeout(SPEC_TIMEOUT_MS);
  const search = await openGlobalSearch(page);
  const cluster = requireClusterName();

  await searchFor(search, `@${cluster}`);
  await expect(search.optionsList).toHaveAttribute("data-mention-mode", "true");

  // Wait for the row to actually carry the cluster name before reading it. data-mention-mode
  // flips the moment the query starts with "@", which is before the list re-filters, so a
  // bare textContent() read here can return a row from the pre-filter list. Matched
  // case-insensitively because the mention filter itself lowercases both sides.
  await expect(search.optionRows.first()).toContainText(new RegExp(escapeForRegExp(cluster), "i"));

  // The account's own display label, read off the row rather than assumed from the env
  // value — the dropdown label only has to contain the configured name, not equal it.
  const accountLabel = ((await search.optionRows.first().textContent()) ?? "").trim();
  expect(accountLabel).not.toBe("");

  await search.optionRows.first().click();

  await test.step("The scoped account replaces the placeholder and leaves mention mode", async () => {
    await expect(search.searchInput).toHaveAttribute("placeholder", new RegExp(`^Search for ${escapeForRegExp(accountLabel)}`));
    await expect(search.searchInput).toHaveValue("");
  });

  await test.step("Backspace on the empty query drops the scope again", async () => {
    await search.searchInput.press("Backspace");
    await expect(search.searchInput).toHaveAttribute("placeholder", /type @ for an account/);
  });
});

test("Global Search - open the search, type @ and a name no account can match, verify the No results found message", { tag: ["@dev", "@regression", "@negative", "@search"] }, async ({ page }) => {
  test.setTimeout(SPEC_TIMEOUT_MS);
  const search = await openGlobalSearch(page);

  await searchFor(search, `@${noMatchTerm("zz-no-such-account")}`);

  await expect(search.optionsList).toHaveAttribute("data-mention-mode", "true");
  await expect(search.optionRows).toHaveCount(0);
  // Mention mode is the one empty state that says so: the Ask hand-off does not apply
  // while picking an account, so this text is the only feedback the user gets.
  await expect(search.mentionNoResults).toBeVisible();
});
