import { expect, Page, Locator } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { IntegrationLocators } from "./IntegrationLocators";
import { waitForGraphQLAndValidate } from "../../utils/GraphQLNetworkWatcher";
import { notifyIntegrationMissingOnce } from "../../utils/IntegrationStatusCache";

async function navigateToAdminIntegrationsPage(page: Page, locators: IntegrationLocators): Promise<void> {
  await locators.adminBtn.waitFor({ state: "visible" });
  await locators.adminBtn.click();

  const tabVisible = await locators.integrationsTab
    .waitFor({ state: "visible", timeout: 15000 })
    .then(() => true)
    .catch(() => false);

  if (!tabVisible) {
    console.log("Admin nav click did not navigate — falling back to direct URL");
    await page.goto(`${process.env.BASE_URL}/user-management`);
    await locators.integrationsTab.waitFor({ state: "visible", timeout: 20000 });
  }

  await locators.integrationsTab.click();
}

async function loginAndGoToIntegrations(page: Page): Promise<IntegrationLocators> {
  const loginPage = new LoginPage(page);
  const locators = new IntegrationLocators(page);
  await loginPage.doFullLogin();
  await navigateToAdminIntegrationsPage(page, locators);
  return locators;
}

export async function navigateToCloudTab(page: Page): Promise<IntegrationLocators> {
  const locators = await loginAndGoToIntegrations(page);
  await expect(locators.kubernetestcloudTab).toBeVisible({ timeout: 15000 });
  await locators.kubernetestcloudTab.click();
  return locators;
}

export async function navigateToCicdTab(page: Page): Promise<IntegrationLocators> {
  const locators = await loginAndGoToIntegrations(page);
  await expect(locators.cicdTab).toBeVisible({ timeout: 15000 });
  await locators.cicdTab.click();
  return locators;
}

export async function navigateToDatabaseTab(page: Page): Promise<IntegrationLocators> {
  const locators = await loginAndGoToIntegrations(page);
  await expect(locators.databaseTab).toBeVisible({ timeout: 15000 });
  await locators.databaseTab.click();
  return locators;
}

export async function navigateToDocsTab(page: Page): Promise<IntegrationLocators> {
  const locators = await loginAndGoToIntegrations(page);
  await expect(locators.docsTab).toBeVisible({ timeout: 15000 });
  await locators.docsTab.click();
  return locators;
}

export async function navigateToInMemoryTab(page: Page): Promise<IntegrationLocators> {
  const locators = await loginAndGoToIntegrations(page);
  await expect(locators.inmemoryTab).toBeVisible({ timeout: 15000 });
  await locators.inmemoryTab.click();
  return locators;
}

export async function navigateToMessagingQueueTab(page: Page): Promise<IntegrationLocators> {
  const locators = await loginAndGoToIntegrations(page);
  await expect(locators.messagingQueueTab).toBeVisible({ timeout: 15000 });
  await locators.messagingQueueTab.click();
  return locators;
}

export async function navigateToServersTab(page: Page): Promise<IntegrationLocators> {
  const locators = await loginAndGoToIntegrations(page);
  await expect(locators.serversTab).toBeVisible({ timeout: 15000 });
  await locators.serversTab.click();
  return locators;
}

export async function navigateToMessagingTab(page: Page): Promise<IntegrationLocators> {
  const locators = await loginAndGoToIntegrations(page);
  await expect(locators.messagingTab).toBeVisible({ timeout: 15000 });
  await locators.messagingTab.click();
  return locators;
}

export async function navigateToTicketingTab(page: Page): Promise<IntegrationLocators> {
  const locators = await loginAndGoToIntegrations(page);
  await expect(locators.ticketingTab).toBeVisible({ timeout: 15000 });
  await locators.ticketingTab.click();
  return locators;
}

export async function navigateToReposTab(page: Page): Promise<IntegrationLocators> {
  const locators = await loginAndGoToIntegrations(page);
  await expect(locators.reposTab).toBeVisible({ timeout: 15000 });
  await locators.reposTab.click();
  return locators;
}

export async function navigateToIntegrationsPage(page: Page): Promise<IntegrationLocators> {
  return loginAndGoToIntegrations(page);
}

// The dropdown only renders a search box for longer option lists, and a grouped
// list arrives with every group collapsed — so the option may be absent from the
// DOM entirely. Search when the box is there; otherwise open groups until the
// account shows up. Never assume which of the two the dropdown gives us.
export async function selectAccountFromDropdown(
  page: Page,
  dropdown: Locator,
  accountName: string,
): Promise<void> {
  await dropdown.click();
  const panel = page.locator(".MuiPopover-paper").last();
  await expect(panel).toBeVisible({ timeout: 10000 });

  const option = page
    .locator('[role="option"]')
    .filter({ hasText: new RegExp(`^${accountName}$`) })
    .first();

  // Options are fetched, so the panel renders a skeleton first. Wait for it to
  // settle into one shape or the other before deciding which one we got.
  const search = page.getByPlaceholder("Search...").first();
  await expect(
    search.or(panel.locator('[role="button"], [role="option"]')).first(),
  ).toBeVisible({ timeout: 10000 });

  if (await search.isVisible()) {
    // Searching force-opens every group that matched, so the option renders.
    await search.fill(accountName);
  } else if (!(await option.isVisible().catch(() => false))) {
    // Group headers are the only role=button in the panel while all groups are
    // collapsed, so read their names first, then open them one at a time.
    const headers = panel.locator('[role="button"]');
    const groupNames: string[] = [];
    for (let i = 0; i < (await headers.count()); i++) {
      groupNames.push((await headers.nth(i).innerText()).trim());
    }
    for (const groupName of groupNames) {
      await panel
        .locator('[role="button"]')
        .filter({ hasText: groupName })
        .first()
        .click()
        .catch(() => {});
      if (await option.isVisible().catch(() => false)) break;
    }
  }

  await expect(option).toBeVisible({ timeout: 10000 });
  await option.click();
  await dropdown.press("Escape");
}

/**
 * Returns true if connection test succeeded (save can proceed).
 * Returns false if backend was unreachable / timed out (skipOnBackendError: true).
 * Throws if connection test explicitly failed and skipOnBackendError is false (default).
 *
 * When skipOnBackendError: true — the watcher still validates the operation and fires
 * a Slack alert on data-level errors (success:false), but the throw is caught so the
 * test continues. This gives accurate Slack alerts without failing the test for known
 * backend issues.
 */
export async function testConnection(
  page: Page,
  {
    testConnectionBtn,
    successToast,
    serviceName,
    saveBtn,
    operationNames = [],
    timeout = 40000,
    skipOnBackendError = false,
    checkDataErrors = false,
  }: {
    testConnectionBtn: Locator;
    successToast: Locator;
    serviceName: string;
    saveBtn: Locator;
    operationNames?: string[];
    timeout?: number;
    skipOnBackendError?: boolean;
    checkDataErrors?: boolean;
  },
): Promise<boolean> {
  const errorToast = page.locator(
    '[role="alert"].MuiAlert-filledError, [role="alert"].MuiAlert-standardError, .toast-error',
  ).first();

  let succeeded = false;

  // Abort signal: resolves when watcher detects a targeted failure, so the
  // toast wait exits early instead of running to the full timeout.
  let resolveAbort!: () => void;
  const abortPromise = new Promise<void>(resolve => { resolveAbort = resolve; });

  const runWatcher = async () => {
    await waitForGraphQLAndValidate(
      page,
      async () => {
        await testConnectionBtn.click();

        let abortedEarly = false;
        const appeared = await Promise.race([
          successToast
            .or(errorToast)
            .first()
            .waitFor({ state: "visible", timeout })
            .then(() => true)
            .catch(() => false),
          abortPromise.then(() => { abortedEarly = true; return false; }),
        ]);

        if (!appeared || abortedEarly) {
          if (skipOnBackendError) {
            const reason = abortedEarly
              ? "API failure detected by GraphQL watcher"
              : `no toast within ${timeout / 1000}s (backend unreachable or slow)`;
            console.warn(`⚠️  ${serviceName} test connection — ${reason}. Skipping save.`);
            return;
          }
          // abortedEarly: the watcher already captured + logged a targeted API failure — surface that, not a misleading "no toast" timeout.
          throw new Error(
            abortedEarly
              ? `${serviceName} test connection failed — GraphQL watcher detected an API failure (see the logged data-level error above)`
              : `Neither success nor error toast appeared within ${timeout / 1000}s`,
          );
        }

        if (await successToast.isVisible()) {
          console.log(`Test connection SUCCESS: ${await successToast.innerText().catch(() => `${serviceName} connection successful`)}`);
          await expect(saveBtn).toBeEnabled();
          succeeded = true;
        } else if (await errorToast.isVisible()) {
          const errorText = (await errorToast.innerText().catch(() => "Unknown error")).trim();
          if (skipOnBackendError) {
            console.warn(`⚠️  ${serviceName} test connection error (treating as backend issue): ${errorText}. Skipping save.`);
          } else {
            console.error(`Test connection FAILED: ${errorText}`);
            throw new Error(`${serviceName} test connection failed: ${errorText}`);
          }
        }
      },
      {
        testName: `${serviceName} - Test Connection`,
        operationNames,
        checkDataErrors,
        onTargetedFailure: () => resolveAbort(),
      },
    );
  };

  if (skipOnBackendError) {
    // Let the watcher capture + alert on real errors, but don't propagate the throw
    // so the test can skip save gracefully instead of failing.
    try {
      await runWatcher();
    } catch (e) {
      console.warn(`⚠️  ${serviceName} - GraphQL watcher reported an issue (skipOnBackendError=true): ${e}`);
      return false;
    }
  } else {
    await runWatcher();
  }

  return succeeded;
}

/** Returns true if a config row named `configName` is visible in the table. */
async function isRowVisible(page: Page, configName: string, timeout = 8000): Promise<boolean> {
  return page
    .locator("tbody tr")
    .filter({ hasText: configName })
    .first()
    .waitFor({ state: "visible", timeout })
    .then(() => true)
    .catch(() => false);
}

// Filters the list to `configName` via the "Enter Name" search box so the target row lands on page 1 — row helpers only scan the visible page, so a row on a later page is otherwise missed. No-op when the box is absent.
async function filterListByName(page: Page, configName: string): Promise<void> {
  const search = page.getByPlaceholder("Enter Name").first();
  // Wait for the box to render — disable/enable call this before the list finishes loading; an instant check would skip filtering.
  const appeared = await search
    .waitFor({ state: "visible", timeout: 10000 })
    .then(() => true)
    .catch(() => false);
  if (!appeared) return;
  const refetch = page
    .waitForResponse(
      (r) => r.url().includes("/api/graphql") && !!r.request().postData()?.includes("ListIntegrations"),
      { timeout: 8000 },
    )
    .catch(() => null);
  await search.fill(configName);
  // The box filters on submit, not keystroke — Enter triggers the ListIntegrations refetch.
  await search.press("Enter");
  await refetch;
}

/**
 * Opens an integration's list page (via the caller-supplied `openList`), clears
 * the status filter so BOTH enabled and disabled configs are listed, and returns
 * whether a config named `configName` exists.
 *
 * Showing all statuses matters for idempotency: a config left disabled by a prior
 * run (e.g. an enable step that failed) must still be detected so the delete step
 * can clean it up before re-adding — otherwise the re-add no-ops on "already
 * exists" and the config stays disabled.
 *
 * `openList` performs the navigation to the list — usually a single section-card
 * click, but MCP needs a search step first, so it is injected rather than
 * hard-coded here. `statusFilterId` is the ListIntegrations status-filter id —
 * `${integrationName}-status-filter`.
 */
export async function isIntegrationPresent(
  page: Page,
  {
    openList,
    configName,
    serviceName,
    statusFilterId,
  }: { openList: () => Promise<void>; configName: string; serviceName: string; statusFilterId: string },
): Promise<boolean> {
  // Opening the list fires the ListIntegrations query — validate it so the
  // presence-check step also carries GraphQL network validation.
  await waitForGraphQLAndValidate(
    page,
    async () => {
      await openList();
    },
    {
      testName: `Check ${serviceName} Integration Exists`,
      operationNames: ["ListIntegrations"],
      checkDataErrors: true,
    },
  );

  await showAllStatuses(page, statusFilterId);
  await filterListByName(page, configName);
  const hit = await isRowVisible(page, configName);
  console.log(`${serviceName} integration "${configName}" present: ${hit}`);
  return hit;
}

/**
 * Live presence check for a config that the suite expects to exist (created by
 * the Add step) — used to gate Disable/Enable. Returns whether the row exists;
 * when it doesn't, fires a SINGLE consolidated `@qa` Slack alert per integration
 * per run (deduped in-memory, so Disable + Enable don't each post a message).
 *
 * The caller is expected to `test.skip(!present, "<Service> integration is not
 * Active — Slack notification sent")` so SlackReporter suppresses the per-test
 * skip spam (its reason matches `/integration is not Active/i`), leaving exactly
 * the one alert this posts.
 *
 * `integrationKey` is the dedup key (lowercase integration name, e.g.
 * "confluence"); presence itself is never cached — it's re-checked every call —
 * because the config is created/deleted within the run.
 */
export async function ensureIntegrationPresent(
  page: Page,
  {
    openList,
    configName,
    serviceName,
    statusFilterId,
    integrationKey,
  }: {
    openList: () => Promise<void>;
    configName: string;
    serviceName: string;
    statusFilterId: string;
    integrationKey: string;
  },
): Promise<boolean> {
  const present = await isIntegrationPresent(page, { openList, configName, serviceName, statusFilterId });
  if (!present) {
    await notifyIntegrationMissingOnce(integrationKey, {
      displayName: serviceName,
      statusLabel: "Not Available",
    });
  }
  return present;
}

/**
 * Deletes the config named `configName` via the row kebab → Delete → confirm
 * flow, validating the DeleteIntegrationConfig mutation. Opens the list via
 * `openList` and clears the status filter so a leftover config of any status —
 * enabled or disabled — is located and deleted.
 *
 * `statusFilterId` is the ListIntegrations status-filter id — `${integrationName}-status-filter`.
 */
/**
 * Opens a config row's kebab ("More actions") menu and clicks the `itemName`
 * menu item. The menu is a portaled MUI Menu whose items mount while it animates
 * open, so Playwright can see the item as attached-but-not-yet-"visible" — a
 * plain click then waits out its timeout. Waiting for the item to be ATTACHED
 * and firing the React onClick via `dispatchEvent` clicks it regardless of the
 * open animation; the menu unmounting (item detached) confirms it registered.
 *
 * Items are located by their stable `id` (`delete` / `disable` / `enable` /
 * `edit`), NOT by role+name: the DS DropdownMenu renders each item's label in a
 * nested span, so the `menuitem`'s accessible name is empty and
 * `getByRole("menuitem", { name })` never matches. Every row's menu stays
 * mounted at once (so `#id` matches one item per row), so the selector is scoped
 * to `:visible` — only the currently-open row's item is visible.
 */
async function clickRowMenuItem(page: Page, row: Locator, itemName: string): Promise<void> {
  const menuItem = page.locator(`[role="menuitem"]#${itemName.toLowerCase()}:visible`);
  for (let attempt = 0; attempt < 4; attempt++) {
    await row.getByRole("button", { name: "More actions" }).click();
    const attached = await menuItem
      .waitFor({ state: "attached", timeout: 4000 })
      .then(() => true)
      .catch(() => false);
    if (attached) {
      await menuItem.dispatchEvent("click").catch(() => {});
      const registered = await menuItem
        .waitFor({ state: "detached", timeout: 3000 })
        .then(() => true)
        .catch(() => false);
      if (registered) return;
    }
    await page.keyboard.press("Escape").catch(() => {});
  }
  await row.getByRole("button", { name: "More actions" }).click();
  await menuItem.waitFor({ state: "attached", timeout: 5000 });
  await menuItem.dispatchEvent("click");
}

export async function deleteIntegration(
  page: Page,
  {
    openList,
    configName,
    serviceName,
    statusFilterId,
  }: { openList: () => Promise<void>; configName: string; serviceName: string; statusFilterId: string },
): Promise<void> {
  await openList();
  await showAllStatuses(page, statusFilterId);
  await filterListByName(page, configName);

  const row = page.locator("tbody tr").filter({ hasText: configName }).first();
  await row.waitFor({ state: "visible", timeout: 10000 });

  await clickRowMenuItem(page, row, "Delete");

  const successToast = page.getByText("Deleted the", { exact: false }).first();

  await waitForGraphQLAndValidate(
    page,
    async () => {
      await page
        .getByRole("dialog")
        .getByRole("button", { name: "Delete", exact: true })
        .click();
      await successToast.waitFor({ state: "visible", timeout: 30000 });
    },
    {
      testName: `Delete ${serviceName} Integration`,
      operationNames: ["DeleteIntegrationConfig"],
      checkDataErrors: true,
    },
  );

  console.log(`Deleted ${serviceName} integration "${configName}"`);
}

/**
 * Runs a status-change action ("Disable" or "Enable") on the config row named
 * `configName` from its currently-open list page: row kebab → <action> menu
 * item → confirm dialog, validating the UpdateIntegrationConfig mutation.
 * Assumes the row is visible under the current status filter.
 */
async function changeRowStatus(
  page: Page,
  {
    configName,
    action,
    serviceName,
  }: { configName: string; action: "Disable" | "Enable"; serviceName: string },
): Promise<void> {
  await filterListByName(page, configName);
  const row = page.locator("tbody tr").filter({ hasText: configName }).first();
  await row.waitFor({ state: "visible", timeout: 10000 });

  await clickRowMenuItem(page, row, action);

  // Success toast: "Disabled the <name> subscription" / "Enabled the ...".
  const toast = page.getByText(`${action}d the`, { exact: false }).first();

  await waitForGraphQLAndValidate(
    page,
    async () => {
      await page
        .getByRole("dialog")
        .getByRole("button", { name: action, exact: true })
        .click();
      await toast.waitFor({ state: "visible", timeout: 15000 });
    },
    {
      testName: `${action} ${serviceName} Integration`,
      operationNames: ["UpdateIntegrationConfig"],
      checkDataErrors: true,
    },
  );

  console.log(`${action}d ${serviceName} integration "${configName}"`);
}

/**
 * Clears the ListIntegrations status filter (default "Enabled") so the table
 * lists configs of EVERY status — enabled and disabled together. Replaces
 * toggling the filter between "Enabled" and "Disabled": switching options in the
 * popover was flaky (the open popover's backdrop swallowed the retry click), and
 * every caller only needs to find a config regardless of its status.
 *
 * The trigger shows a clear-X while a value is selected; clicking it resets the
 * filter to no status (`status: undefined` → all rows). Idempotent — a no-op
 * once already cleared.
 *
 * `statusFilterId` is the filter's id — `${integrationName}-status-filter`.
 */
async function showAllStatuses(page: Page, statusFilterId: string): Promise<void> {
  const trigger = page.locator(`#auto-complete-${statusFilterId}`);
  await trigger.waitFor({ state: "visible", timeout: 10000 });
  const clearIcon = trigger.locator("svg").last();

  for (let attempt = 0; attempt < 4; attempt++) {
    const text = (await trigger.innerText().catch(() => "")).trim();
    if (!/Enabled|Disabled/.test(text)) break;
    // Clearing refetches ListIntegrations with all statuses; wait for that
    // response so the table has finished re-rendering before callers interact
    // with a row — otherwise a menu opened mid-refetch is remounted shut.
    const refetch = page
      .waitForResponse(
        (r) => r.url().includes("/api/graphql") && !!r.request().postData()?.includes("ListIntegrations"),
        { timeout: 8000 },
      )
      .catch(() => null);
    await clearIcon.click({ force: true }).catch(() => {});
    await refetch;
    try {
      await expect(trigger).not.toContainText(/Enabled|Disabled/, { timeout: 2000 });
      return;
    } catch {
      // clear didn't register — retry
    }
  }
}

/**
 * Disables the config named `configName` via its row three-dot menu, validating
 * the UpdateIntegrationConfig mutation. Assumes the caller has already opened the
 * list page and the row is present under the default "Enabled" filter.
 */
export async function disableIntegration(
  page: Page,
  { configName, serviceName }: { configName: string; serviceName: string },
): Promise<void> {
  await changeRowStatus(page, { configName, action: "Disable", serviceName });
}

/**
 * Enables the config named `configName`: clears the status filter so the
 * disabled config is listed, then enables it via its row three-dot menu —
 * validating the UpdateIntegrationConfig mutation. Assumes the caller has already
 * opened the list page.
 *
 * `statusFilterId` is the ListIntegrations status-filter id — `${integrationName}-status-filter`.
 */
export async function enableIntegration(
  page: Page,
  {
    configName,
    serviceName,
    statusFilterId,
  }: { configName: string; serviceName: string; statusFilterId: string },
): Promise<void> {
  await showAllStatuses(page, statusFilterId);
  await changeRowStatus(page, { configName, action: "Enable", serviceName });
}

export async function saveAndHandleAlreadyExists(
  page: Page,
  {
    saveBtn,
    successToast,
    testName,
    operationNames = ["AddIntegrations"],
    ignoreErrorMessages = [],
    onSuccess,
    inlineError,
  }: {
    saveBtn: Locator;
    successToast: Locator;
    testName: string;
    operationNames?: string[];
    ignoreErrorMessages?: string[];
    onSuccess?: () => Promise<void>;
    inlineError?: Locator;
  },
): Promise<void> {
  const errorToast = page.locator(
    '[role="alert"].MuiAlert-filledError, [role="alert"].MuiAlert-standardError, .toast-error',
  ).first();

  await waitForGraphQLAndValidate(
    page,
    async () => {
      await saveBtn.click();

      if (inlineError) {
        const inlineAppeared = await inlineError
          .waitFor({ state: "visible", timeout: 2000 })
          .then(() => true)
          .catch(() => false);
        if (inlineAppeared) {
          const trimmed = (await inlineError.innerText().catch(() => "Unknown error")).trim();
          const isDuplicate = trimmed.includes("already exists") || trimmed.includes("already has");
          if (isDuplicate) {
            console.log("ALREADY_EXISTS (inline):", trimmed);
            return;
          }
          throw new Error(`Account creation failed (inline): ${trimmed}`);
        }
      }

      const toastAppeared = await successToast
        .or(errorToast)
        .first()
        .waitFor({ state: "visible", timeout: 10000 })
        .then(() => true)
        .catch(() => false);

      if (!toastAppeared) {
        // A caller tolerating duplicates (ignoreErrorMessages set) accepts a silent no-op; otherwise a missing snackbar means the save never succeeded.
        if (ignoreErrorMessages.length > 0) {
          console.log(`${testName}: No toast appeared after save — likely duplicate ignored by backend`);
          return;
        }
        throw new Error(`${testName}: expected a success snackbar after save, but none appeared`);
      }

      if (await successToast.isVisible()) {
        const toastText = await successToast.innerText().catch(() => "Success");
        console.log("SUCCESS:", toastText);
        if (onSuccess) await onSuccess();
      } else if (await errorToast.isVisible()) {
        const trimmed = (await errorToast.innerText().catch(() => "Unknown error")).trim();
        const isDuplicate = trimmed.includes("already exists") || trimmed.includes("already has");
        if (isDuplicate) {
          console.log("ALREADY_EXISTS:", trimmed);
        } else {
          console.error("FAILED:", trimmed);
          throw new Error(`Account creation failed: ${trimmed}`);
        }
      }
    },
    { testName, operationNames: inlineError ? [] : operationNames, ignoreErrorMessages },
  );
}

/**
 * Ensures `accountName` has Loki set as its default log provider, turning it on
 * if needed. Loki's Observability card opens a dedicated page
 * (/accounts/account-form?cloudProvider=loki — AccountCard.handleClick in
 * integration.jsx) listing every linked account with a per-account Logs/Traces/
 * Metrics Switch (IntegrationDynamicFormModal's agentAccountProviders block,
 * handleAgentProviderToggle) — there is no separate Save; each toggle saves itself.
 *
 * Returns a `restore()` that puts the switch back to what it was before this
 * call, so a test that turns Loki on for itself doesn't leave that change behind
 * for every other account/test that reads "the default log provider" afterward.
 * Callers should invoke `restore()` in a `finally`/`test.afterAll` so it still
 * runs when the rest of the test fails partway through.
 */
interface LokiLogsSwitch {
  // The native <input type="checkbox"> — for reading .isChecked() only. MUI's
  // Switch renders this visually hidden (opacity 0) under a styled track, which
  // a live run proved directly: the accessibility snapshot at the moment of
  // timeout showed checkbox "Enabled" [checked] sitting right there, but
  // waitFor({state:"visible"}) on it never resolved — the note in a11y trees
  // is unaffected by CSS opacity, Playwright's actionability check is not.
  checkbox: Locator;
  // The FormControlLabel wrapper around the switch AND its Enabled/Disabled
  // text (rendered with cursor:pointer) — this is what's actually visible and
  // clickable, and clicking anywhere in it toggles the switch it wraps.
  clickTarget: Locator;
}

// Navigates fresh from wherever `page` currently is to accountName's "Default
// Log Provider" checkbox inside the "Edit Loki Account" dialog, leaving the
// dialog OPEN — callers read/toggle the checkbox, then close it themselves.
// Split out so restore() can call this again on its own: the dialog is closed
// after the initial check (see below), so its checkbox locator goes stale the
// moment that happens, and by the time restore() runs the page may be
// anywhere else in the test — it cannot assume the dialog is still open, or
// even that it's still on this page at all.
async function openLokiAccountLogsSwitch(page: Page, accountName: string): Promise<LokiLogsSwitch> {
  const locators = await loginAndGoToIntegrations(page);
  await locators.observabilityTab.waitFor({ state: "visible", timeout: 15000 });
  await locators.observabilityTab.click();

  await locators.lokiSectionCard.waitFor({ state: "visible", timeout: 15000 });
  await locators.lokiSectionCard.click();
  await page.waitForURL(/cloudProvider=loki/i, { timeout: 20000 });
  // The URL commits before the account-form page's own data has loaded — a
  // live run timed out here once with the page still showing this loader and
  // no table at all yet. Same alt-text loader LoginPage.waitForLoaderToDisappear
  // waits on; a generous timeout since this list has its own fetch to finish.
  await page.getByAltText("Loading...").waitFor({ state: "hidden", timeout: 60000 }).catch(() => {});

  // Confirmed against a live run: this lands on a TABLE of Loki config entries,
  // not directly on a per-account toggle form. Loki can have more than one entry
  // here — e.g. a direct "loki saas" connection alongside the K8s-collector-
  // managed "loki" agent one, each its own row with its own Account column. K8s
  // cluster logs come from the agent entry: exact Name "loki" (not "loki saas"),
  // Account column listing accountName among a comma-separated list of clusters.
  const row = page
    .locator("tbody tr")
    .filter({ has: page.getByRole("cell", { name: "loki", exact: true }) })
    .filter({ hasText: accountName })
    .first();
  await row.waitFor({ state: "visible", timeout: 20000 });

  // Edit opens "Edit Loki Account" (IntegrationDynamicFormModal's agent-account
  // view) — one block per linked account, each showing the account name then a
  // "Default Log Provider" field (confirmed against a live run — NOT the
  // generic "Logs" label the source's providerKeyLabels map suggested) next to
  // its Enabled/Disabled checkbox. clickRowMenuItem is the already-proven
  // row-menu helper every other integration flow in this file uses.
  const dialog = page.getByRole("dialog", { name: "Edit Loki Account" });
  await clickRowMenuItem(page, row, "Edit");
  await dialog.waitFor({ state: "visible", timeout: 15000 });

  // The account name renders inside a Typography (a <p>, not a <div>) that is
  // the FIRST child of its per-account <Box> — and that Box also holds the
  // Default Log Provider row, so the Box's own full text is the name plus that
  // row's label and Enabled/Disabled state concatenated together, not the name
  // alone. Matching a <div> whose ENTIRE text is exactly the account name would
  // therefore never hit — go to the exact-text leaf itself, then its direct
  // parent (the Box), which is the same "leaf text -> immediate container"
  // approach KnowledgeBaseLocators.getKBCardByName already relies on elsewhere
  // in this suite, just via a direct parent instead of an ancestor filter.
  //
  // Scoped to `dialog`, NOT page-wide: the background list table (still in the
  // DOM under the modal overlay) can carry its own exact-text "k8s-dev" cell —
  // e.g. a "loki saas" row for the same account — and a MUI Dialog portals to
  // the end of <body>, so an unscoped page-wide search can resolve .first() to
  // that background cell instead of the dialog's own row. A live run hit
  // exactly this: the checkbox lookup timed out because accountRow had silently
  // resolved to the table cell's ancestor, which holds no "Default Log
  // Provider" block at all.
  const accountNameEl = dialog.getByText(accountName, { exact: true }).first();
  await accountNameEl.waitFor({ state: "visible", timeout: 20000 });
  const accountRow = accountNameEl.locator("xpath=..");

  // accountRow (accountName's direct parent) holds exactly one "Default Log
  // Provider" field for this integration type, so there is no need to first
  // isolate that field's own wrapper the way earlier attempts did via
  // `.locator("div").filter(...)` — repeated live-run failures traced back to
  // that div-tag assumption: the accessibility snapshot used to confirm this
  // structure reports every non-semantic wrapper as "generic" regardless of
  // its real HTML tag, so a bare `.locator("div")` CSS-tag selector had no
  // guarantee of ever matching it. Querying accountRow directly by role/text
  // sidesteps the tag guess entirely and is scoped tightly enough on its own
  // (one account's block only, confirmed by the accountNameEl parent above).
  const stateLabel = accountRow.getByText(/^(Enabled|Disabled)$/, { exact: true }).first();
  const clickTarget = stateLabel.locator("xpath=..");
  // Role-based, not a raw input[type=checkbox] CSS guess: a live run's
  // accessibility snapshot confirmed role=checkbox directly (`checkbox
  // "Enabled" [checked]`), and getByRole matches on that semantic regardless
  // of whether the underlying markup is a native <input> or an ARIA-only
  // custom element — an attribute selector would only match the former.
  const checkbox = accountRow.getByRole("checkbox").first();
  await checkbox.waitFor({ state: "attached", timeout: 15000 });
  await clickTarget.waitFor({ state: "visible", timeout: 15000 });

  return { checkbox, clickTarget };
}

async function closeEditLokiAccountDialog(page: Page): Promise<void> {
  const dialog = page.getByRole("dialog", { name: "Edit Loki Account" });
  // Done only closes the dialog — handleAgentProviderToggle already saved
  // each toggle itself — but left open it would sit over the page and
  // intercept every click the caller makes right after this returns.
  await dialog.getByRole("button", { name: "Done" }).click();
  await dialog.waitFor({ state: "hidden", timeout: 10000 });
}

export async function ensureLokiDefaultLogProvider(
  page: Page,
  { accountName }: { accountName: string },
): Promise<{ wasAlreadyDefault: boolean; restore: () => Promise<void> }> {
  const logsSwitch = await openLokiAccountLogsSwitch(page, accountName);
  let wasAlreadyDefault = false;
  try {
    wasAlreadyDefault = await logsSwitch.checkbox.isChecked();

    if (!wasAlreadyDefault) {
      await logsSwitch.clickTarget.click();
      await expect(logsSwitch.checkbox).toBeChecked({ timeout: 15000 });
      console.log(`Turned Loki ON as the default log provider for "${accountName}"`);
    } else {
      console.log(`Loki was already the default log provider for "${accountName}"`);
    }
  } finally {
    // Runs even if the checkbox read or the click above threw, so a failed
    // setup never leaves this dialog open over whatever the test does next.
    await closeEditLokiAccountDialog(page);
  }

  const restore = async (): Promise<void> => {
    if (wasAlreadyDefault) return; // nothing to undo — it was already on before us
    try {
      // The dialog closed above, so its checkbox locator is stale — this must
      // navigate to it again from whatever page the test finished on, not reuse it.
      const freshSwitch = await openLokiAccountLogsSwitch(page, accountName);
      const stillChecked = await freshSwitch.checkbox.isChecked().catch(() => null);
      if (stillChecked === false) {
        console.log(`Loki default-log-provider switch for "${accountName}" was already back off`);
      } else {
        // Log what actually happened, not what was attempted, then let the
        // failure propagate — a swallowed error here just relocates the
        // exact "nobody notices" bug this restore exists to prevent.
        await freshSwitch.clickTarget.click().then(
          () => console.log(`Restored Loki default-log-provider switch to OFF for "${accountName}"`),
          (err) => {
            console.log(`Failed to restore Loki default-log-provider switch to OFF for "${accountName}": ${err}`);
            throw err;
          }
        );
      }
    } finally {
      await closeEditLokiAccountDialog(page).catch(() => {});
    }
  };

  return { wasAlreadyDefault, restore };
}

