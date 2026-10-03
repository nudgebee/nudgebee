// Not for OSS
import { Locator, Page, expect } from "@playwright/test";
import path from "path";
import { LoginPage } from "../../../pages/LoginPage";
import { AccountContextLocators, E2E_NAME_PREFIX } from "./accountContextLocators";
import { waitForGraphQLAndValidate } from "../../utils/GraphQLNetworkWatcher";

// app/src/api1/global-context/index.ts names each operation directly, with no module
// prefix — these are the strings queryGraphQL is called with.
export const OP_LIST = "ListGlobalContexts";
export const OP_CREATE = "CreateGlobalContext";
export const OP_UPDATE = "UpdateGlobalContext";
export const OP_DELETE = "DeleteGlobalContext";

// How many accounts the picker is walked through looking for one with no context. Bounded
// so a tenant where every account is occupied reports that by name instead of running to
// the test timeout.
const MAX_ACCOUNTS_TO_SCAN = 8;

export const SAMPLE_CONTEXT_FILE = path.join(__dirname, "..", "fixtures", "sample-context.txt");

export function uniqueContextName(): string {
  return `${E2E_NAME_PREFIX}${Date.now()}_${Math.random().toString(36).slice(2, 6)}`;
}

// Logs in, opens the Nubi panel and lands on b-Cortex > Knowledge > Account Context.
// Knowledge is not b-Cortex's default landing group and Account Context is not its default
// sub-tab, so this takes two clicks: the group tab, then the sub-tab.
export async function openAccountContextTab(page: Page): Promise<AccountContextLocators> {
  const loginPage = new LoginPage(page);
  const locators = new AccountContextLocators(page);

  await loginPage.doFullLogin();
  await locators.nubi.openPanel();
  await locators.nubi.openBCortex(locators.knowledgeGroupTab);
  await locators.knowledgeGroupTab.click();
  await locators.accountContextTab.waitFor({ state: "visible", timeout: 20000 });
  await locators.accountContextTab.click();

  // A tenant with b-Cortex off keeps Account Context on the Admin > AI & Tools side
  // instead (aiToolsConfig.js `legacyOnly`), where none of the locators below exist. The
  // sub-tab being absent is the only observable difference, and it is already waited for
  // above, so reaching here means the b-Cortex mount is the live one.
  await waitForListSettled(locators);
  return locators;
}

// GlobalContextTab returns early on both loading and error, so the header — and with it
// the tab's description copy — renders only once the list request has landed cleanly. The
// error banner replaces that same body, so the two are raced rather than the body being
// waited on alone: waiting on the body alone turns a backend failure into a bare timeout
// that names nothing, and asserting the banner's absence first passes against the
// not-yet-rendered tab and proves nothing.
export async function waitForListSettled(locators: AccountContextLocators, timeout = 45000): Promise<void> {
  await expect(locators.tabDescription.or(locators.loadErrorBanner).first()).toBeVisible({ timeout });
  await expect(locators.loadErrorBanner).toHaveCount(0);
}

// Leaves the tab and comes back, which unmounts GlobalContextTab and makes it refetch —
// the only way to tell a persisted record from one still held in React state. Knowledge
// Base is a sibling sub-tab of the same group, so there is no need to leave it.
export async function remountAccountContextTab(locators: AccountContextLocators): Promise<void> {
  await locators.knowledgeBaseTab.click();
  await locators.tabDescription.waitFor({ state: "detached", timeout: 20000 });
  await locators.accountContextTab.click();
  await waitForListSettled(locators);
}


// Commits one pick and waits for the tab to finish reloading under the new scope.
//
// Picking is the only way this suite ever closes the picker. Opening it and dismissing it
// by hand is what broke the first version: the dismiss is best-effort, and a panel left up
// leaves an invisible MUI backdrop over the trigger that blocks the next open for its whole
// timeout. So every open is followed by a pick, and the panel closes itself.
async function pickAccountOption(locators: AccountContextLocators, option: Locator): Promise<void> {
  await option.waitFor({ state: "visible", timeout: 30000 });
  await option.click();
  // The trigger's own flag is the app's signal that the panel really went, rather than the
  // node merely being mid-exit-transition — and it is the state the next open depends on.
  await expect(locators.accountSelectTrigger).toHaveAttribute("aria-expanded", "false", { timeout: 20000 });
  await waitForListSettled(locators);
}

// True when the account currently showing holds no context at all — the state in which
// GlobalContextTab enables its "Add Account Context" button (it is disabled at one, since
// the backend caps an account at a single record).
//
// A loaded tab shows exactly one of two things: the empty-state panel, or the single card
// (and with it the card's kebab). Racing them settles the moment the answer is known, so
// the occupied branch costs nothing — a plain waitFor on the empty state alone would burn
// its whole timeout every time the answer is "no", once per account selectWritableAccount
// scans. The read afterwards is direct rather than caught: the race has already proven one
// of the two rendered, so there is nothing left for a swallowed error to hide.
async function accountIsEmpty(locators: AccountContextLocators, timeout = 30000): Promise<boolean> {
  await expect(locators.emptyState.or(locators.soleCardMenuBtn).first()).toBeVisible({ timeout });
  return locators.emptyState.isVisible();
}

// Selects an account this suite can actually write to, and returns its picker index.
//
// The one-context-per-account cap means a create case needs an account that is empty, and
// "empty" is not something this suite may arrange by deleting someone else's record. So the
// picker is walked: the first account with no context wins, and an account holding only
// this suite's own leftover (an nb_e2e_acctx_ name from a run that failed before its
// cleanup) is reclaimed by deleting that leftover first. Anything else is left alone.
//
// A serial-mode retry re-runs the whole group from the start, so run 1's leftover would
// otherwise be run 2's "account is occupied" failure — reclaiming is what keeps the suite
// safe to run twice.
export async function selectWritableAccount(page: Page, locators: AccountContextLocators): Promise<number> {
  for (let i = 0; i < MAX_ACCOUNTS_TO_SCAN; i++) {
    const option = locators.realAccountOption(i);
    await locators.openAccountPanel(option);

    // Probe: running past the last account is how this loop discovers the tenant's size —
    // there is no count to read up front, because counting would mean opening the panel
    // without picking from it.
    if (!(await option.isVisible().catch(() => false))) {
      if (i === 0) {
        throw new Error("the b-Cortex account filter offered no real account — Account Context cannot be written to tenant-wide");
      }
      throw new Error(
        `all ${i} account(s) the b-Cortex filter offers already hold an account context created outside this suite — ` +
          "the one-context-per-account cap leaves nothing for these cases to create"
      );
    }

    await pickAccountOption(locators, option);

    if (await accountIsEmpty(locators)) return i;

    // Probe: most accounts hold either nothing or somebody else's context, so "no leftover
    // of ours here" is the normal answer and must not throw. Presence is all that is read —
    // one context per account means a prefixed name on screen can only be the card's.
    const hasLeftover = await locators.e2eLeftover
      .waitFor({ state: "visible", timeout: 5000 })
      .then(() => true)
      .catch(() => false);
    if (!hasLeftover) continue;

    await deleteSoleContext(page, locators, "Nubi Account Context - reclaim a leftover from an earlier run");
    return i;
  }

  throw new Error(
    `every one of the first ${MAX_ACCOUNTS_TO_SCAN} accounts already holds an account context created outside this suite — ` +
      "the one-context-per-account cap leaves nothing for these cases to create"
  );
}

export async function openCreateForm(locators: AccountContextLocators): Promise<void> {
  await expect(locators.addContextBtn).toBeEnabled({ timeout: 20000 });
  await locators.addContextBtn.click();
  await expect(locators.formModalIntro).toBeVisible({ timeout: 20000 });
}

// Fills the form and asserts each field committed. ds/Input is controlled, so a value that
// did not land is a real failure rather than a timing artefact — and asserting it here is
// what keeps the create/update assertions below about persistence rather than about typing.
export async function fillContextForm(
  locators: AccountContextLocators,
  fields: { name?: string; description?: string; content?: string }
): Promise<void> {
  if (fields.name !== undefined) {
    await locators.nameInput.fill(fields.name);
    await expect(locators.nameInput).toHaveValue(fields.name, { timeout: 10000 });
  }
  if (fields.description !== undefined) {
    await locators.descriptionInput.fill(fields.description);
    await expect(locators.descriptionInput).toHaveValue(fields.description, { timeout: 10000 });
  }
  if (fields.content !== undefined) {
    await locators.contentInput.fill(fields.content);
    await expect(locators.contentInput).toHaveValue(fields.content, { timeout: 10000 });
  }
}

// Submits the create form and proves the mutation left the browser and came back clean.
// The modal closing is the app's own "the write committed" signal — handleFormSubmit only
// reaches setFormModalOpen(false) after the response arrives with no errors.
export async function submitCreate(page: Page, locators: AccountContextLocators, testName: string): Promise<void> {
  await expect(locators.formCreateBtn).toBeEnabled({ timeout: 10000 });
  await waitForGraphQLAndValidate(
    page,
    async () => {
      await locators.formCreateBtn.click();
      await expect(locators.formModal).toBeHidden({ timeout: 45000 });
    },
    { testName, operationNames: OP_CREATE, timeoutMs: 60000 }
  );
  await waitForListSettled(locators);
}

export async function submitUpdate(page: Page, locators: AccountContextLocators, testName: string): Promise<void> {
  await expect(locators.formUpdateBtn).toBeEnabled({ timeout: 10000 });
  await waitForGraphQLAndValidate(
    page,
    async () => {
      await locators.formUpdateBtn.click();
      await expect(locators.formModal).toBeHidden({ timeout: 45000 });
    },
    { testName, operationNames: OP_UPDATE, timeoutMs: 60000 }
  );
  await waitForListSettled(locators);
}

// Opens one named card's kebab menu and picks an entry. The menu is a portalled MUI Menu,
// so the wait is on the item itself rather than on any container.
export async function openCardMenu(locators: AccountContextLocators, name: string, item: "edit" | "delete"): Promise<void> {
  await locators.cardMenuBtn(name).click();
  const entry = locators.menuItem(item, item === "edit" ? "Edit" : "Delete");
  await entry.waitFor({ state: "visible", timeout: 15000 });
  await entry.click();
}

// Drives the delete confirmation through to the empty state. Split out from the two
// callers below because the confirm half is identical whether the caller knows the
// record's name or only that one is on screen.
async function confirmDelete(page: Page, locators: AccountContextLocators, testName: string): Promise<void> {
  await expect(locators.deleteModal).toBeVisible({ timeout: 20000 });
  await waitForGraphQLAndValidate(
    page,
    async () => {
      await locators.deleteConfirmBtn.click();
      await expect(locators.deleteModal).toBeHidden({ timeout: 45000 });
    },
    { testName, operationNames: OP_DELETE, timeoutMs: 60000 }
  );
  await waitForListSettled(locators);
  // The empty state is the app's own proof the row went, and the only assertion available
  // to the unnamed caller — a card that failed to delete leaves it absent.
  await expect(locators.emptyState).toBeVisible({ timeout: 30000 });
}

// Deletes one named context and asserts that card is gone, so a cleanup that silently did
// nothing cannot read as a success. Scoped to that card's own kebab throughout.
export async function deleteContext(page: Page, locators: AccountContextLocators, name: string, testName: string): Promise<void> {
  await openCardMenu(locators, name, "delete");
  await confirmDelete(page, locators, testName);
  await expect(locators.contextCard(name)).toHaveCount(0, { timeout: 30000 });
}

// Deletes the one context on screen without needing its name — used only to reclaim this
// suite's own leftover, after the prefix check has already established the record is ours.
export async function deleteSoleContext(page: Page, locators: AccountContextLocators, testName: string): Promise<void> {
  await locators.soleCardMenuBtn.click();
  const entry = locators.menuItem("delete", "Delete");
  await entry.waitFor({ state: "visible", timeout: 15000 });
  await entry.click();
  await confirmDelete(page, locators, testName);
}

// Best-effort teardown for the cases that leave a record behind. Never throws: a case that
// failed before it created anything has nothing to remove, and a teardown error would mask
// the real failure. It does not swallow a failed delete silently — the warning names the
// record so a genuine leak is traceable in the run log, and the next run reclaims it.
export async function cleanupContext(page: Page, locators: AccountContextLocators, name: string): Promise<void> {
  try {
    // Same race as accountIsEmpty, for the same reason: the delete case removes its own
    // record, so "already gone" is the common answer here and it now costs nothing rather
    // than waiting out a timeout on a card that is never coming. Once the tab has settled
    // the card lookup is an immediate read — a card that exists is already on screen.
    if (await accountIsEmpty(locators)) return;
    if (!(await locators.contextCard(name).isVisible())) return;
    await deleteContext(page, locators, name, "Nubi Account Context - teardown");
  } catch (error) {
    console.warn(`[cleanup] the account context this test created could not be removed: ${error}`);
  }
}
