// Not for OSS
import { Page, Locator, expect } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { NubiLocators } from "../nubiLocators";
import { KgManualDependenciesLocators } from "./kgManualDependenciesLocators";
import { waitForGraphQLAndValidate } from "../../utils/GraphQLNetworkWatcher";

// app/src/api1/knowledge-graph/index.ts names each kg_*_manual_dependenc* call this way.
export const OP_LIST = "KgListManualDependencies";
export const OP_CREATE = "KgCreateManualDependency";
export const OP_UPDATE = "KgUpdateManualDependency";
export const OP_DELETE = "KgDeleteManualDependency";
export const OP_RERESOLVE = "KgReresolveManualDependency";

// The two toolbar actions that act on every declaration in the tenant. Nothing in
// this suite may click Re-resolve all or Delete all, and the CRUD cases assert
// these two operations stayed at zero, which is what keeps a shared dev tenant safe.
export const BULK_OPS = ["KgReresolveManualDependencies", "KgDeleteAllManualDependencies"];

export const DECLARATION_NAME_PREFIX = "nb-e2e-kgdep-";

// Upper bound on pager walks and on the stale-row sweep, so a pager that never
// runs out of pages or a delete that never lands cannot loop forever.
const MAX_PAGES = 20;
const MAX_SWEEP = 15;

function uniqueSuffix(): string {
  return `${Date.now()}${Math.random().toString(36).slice(2, 6)}`;
}

// A service name no real workload carries, so the resolver can never pin the row
// to a live KG node; it doubles as the row's identity, since rows carry no name.
export function uniqueDeclarationName(side: "src" | "dst"): string {
  return `${DECLARATION_NAME_PREFIX}${side}-${uniqueSuffix()}`;
}

export interface DeclarationDraft {
  sourceName: string;
  sourceNamespace: string;
  sourceCluster: string;
  destName: string;
}

// Namespace and cluster are invented qualifiers, not real infrastructure: they only
// have to round-trip into the row's Source → Destination cell so the case can
// prove the qualifiers were stored with the name.
export function draftDeclaration(): DeclarationDraft {
  return {
    sourceName: uniqueDeclarationName("src"),
    sourceNamespace: "nb-e2e-ns",
    sourceCluster: "nb-e2e-cluster",
    destName: uniqueDeclarationName("dst"),
  };
}

// The Source → Destination cell as ManualDependenciesTable formats a Kubernetes
// endpoint with both qualifiers set: `<type>: <ns>/<name> @ <cluster>`.
export function sourceEndpointText(draft: DeclarationDraft): string {
  return `K8sService: ${draft.sourceNamespace}/${draft.sourceName} @ ${draft.sourceCluster}`;
}

// Logs in, opens the Nubi panel, lands on b-Cortex > Knowledge Graph, opens its
// Settings dialog and moves to the Manual Declarations tab.
export async function openManualDependencies(page: Page): Promise<KgManualDependenciesLocators> {
  const loginPage = new LoginPage(page);
  const nubi = new NubiLocators(page);
  const locators = new KgManualDependenciesLocators(page);

  await loginPage.doFullLogin();
  await nubi.openPanel();
  await nubi.openBCortex(locators.knowledgeGraphTab);
  await locators.knowledgeGraphTab.click();
  await expect(locators.kgCanvas).toBeVisible({ timeout: 60000 });
  // Settings renders only for a tenant admin, so its absence is a precondition
  // failure worth naming rather than a locator timeout further down the case.
  await expect(locators.kgSettingsBtn, "the Knowledge Graph Settings control is missing — it renders only for a tenant admin").toBeVisible({
    timeout: 30000,
  });
  await openSettingsOnManualDeclarations(locators);
  return locators;
}

// Opens the Settings dialog, which always lands on Coverage, and moves to Manual
// Declarations. Coverage is asserted on the way so a dialog that opened on the
// wrong tab reports itself by name.
export async function openSettingsOnManualDeclarations(locators: KgManualDependenciesLocators): Promise<void> {
  await locators.kgSettingsBtn.click();
  await expect(locators.settingsDialog).toBeVisible({ timeout: 30000 });
  await expect(locators.coverageDescription).toBeVisible({ timeout: 30000 });
  await locators.manualDeclarationsTab.click();
  await expect(locators.manualDepsDescription).toBeVisible({ timeout: 30000 });
  await waitForListLoaded(locators);
}

// The tab shows a loader until kg_list_manual_dependencies resolves, then exactly
// one of rows or its empty panel; waiting on the alternation is the only honest
// "the list finished loading" signal it gives.
export async function waitForListLoaded(locators: KgManualDependenciesLocators): Promise<void> {
  await expect(locators.rows().first().or(locators.emptyState)).toBeVisible({ timeout: 40000 });
}

// Closes and reopens Settings. KnowledgeGraph.jsx renders KGSettings only while
// settingsOpen, so closing unmounts the tab and reopening refetches — the only way
// to tell a persisted row from one still held in React state. It also resets the
// status filter, since that is local state of the unmounted tab.
export async function remountManualDependencies(locators: KgManualDependenciesLocators): Promise<void> {
  await locators.settingsCloseBtn.click();
  await locators.settingsDialog.waitFor({ state: "detached", timeout: 20000 });
  await openSettingsOnManualDeclarations(locators);
}

// CustomTable paginates client-side past ten rows (AUTO_PAGINATE_THRESHOLD) and
// snaps back to page 1 whenever the row count changes, so on a tenant that
// already holds ten declarations a freshly created one is on a later page.
// Every "is it listed" question therefore walks the pager from page 1. Under
// ten rows no pager renders, which is the normal state and not a failure.
async function gotoFirstPage(locators: KgManualDependenciesLocators): Promise<void> {
  if ((await locators.pagerFirstPageBtn.count()) === 0) return;
  await locators.pagerFirstPageBtn.click();
  await expect(locators.pagerCurrentPage(1)).toHaveAttribute("aria-current", /^(true|page)$/, { timeout: 10000 });
}

// Runs `visit` on page 1 and each following page until it returns true or the
// pager runs out; the return value says whether any page satisfied it.
export async function walkPages(locators: KgManualDependenciesLocators, visit: () => Promise<boolean>): Promise<boolean> {
  await waitForListLoaded(locators);
  await gotoFirstPage(locators);
  for (let page = 1; page <= MAX_PAGES; page++) {
    if (await visit()) return true;
    if ((await locators.pagerNextBtn.count()) === 0 || !(await locators.pagerNextBtn.isEnabled())) return false;
    await locators.pagerNextBtn.click();
    await expect(locators.pagerCurrentPage(page + 1)).toHaveAttribute("aria-current", /^(true|page)$/, { timeout: 10000 });
  }
  return false;
}

// Leaves the pager on the page that holds the row when it finds one.
export async function findDeclarationRow(locators: KgManualDependenciesLocators, name: string): Promise<boolean> {
  return walkPages(locators, () => locators.declarationRow(name).isVisible());
}

export async function expectDeclarationListed(locators: KgManualDependenciesLocators, name: string): Promise<void> {
  const found = await findDeclarationRow(locators, name);
  expect(found, `the declaration ${name} is not listed on any page of the Dependencies section`).toBe(true);
  await expect(locators.declarationRow(name)).toBeVisible();
}

// The retrying count on the current page absorbs the render that follows a
// refetch; the walk then proves no other page holds the row either.
export async function expectDeclarationAbsent(locators: KgManualDependenciesLocators, name: string): Promise<void> {
  await waitForListLoaded(locators);
  await expect(locators.declarationRow(name)).toHaveCount(0, { timeout: 30000 });
  const found = await findDeclarationRow(locators, name);
  expect(found, `the declaration ${name} is still listed on a page of the Dependencies section`).toBe(false);
}

export async function openNewDeclarationDialog(locators: KgManualDependenciesLocators): Promise<void> {
  await locators.declareDependencyBtn.click();
  await expect(locators.newDeclarationDialog).toBeVisible({ timeout: 15000 });
  await expect(locators.sourceNameInput).toBeVisible({ timeout: 15000 });
}

export async function fillDeclarationForm(locators: KgManualDependenciesLocators, draft: DeclarationDraft): Promise<void> {
  await locators.sourceNameInput.fill(draft.sourceName);
  await expect(locators.sourceNameInput).toHaveValue(draft.sourceName, { timeout: 10000 });
  await locators.sourceNamespaceInput.fill(draft.sourceNamespace);
  await expect(locators.sourceNamespaceInput).toHaveValue(draft.sourceNamespace, { timeout: 10000 });
  await locators.sourceClusterInput.fill(draft.sourceCluster);
  await expect(locators.sourceClusterInput).toHaveValue(draft.sourceCluster, { timeout: 10000 });
  await locators.destNameInput.fill(draft.destName);
  await expect(locators.destNameInput).toHaveValue(draft.destName, { timeout: 10000 });
}

// Fills and creates a declaration, asserting the create and the reload that
// follows it both left the browser and came back clean before the row is looked for.
export async function createDeclaration(page: Page, locators: KgManualDependenciesLocators, draft: DeclarationDraft): Promise<void> {
  await openNewDeclarationDialog(locators);
  await fillDeclarationForm(locators, draft);
  await expect(locators.createOrSaveBtn).toBeEnabled({ timeout: 10000 });
  await waitForGraphQLAndValidate(
    page,
    async () => {
      await locators.createOrSaveBtn.click();
      await locators.declarationDialog.waitFor({ state: "detached", timeout: 30000 });
    },
    { testName: "KG Manual Dependencies - create a declaration", operationNames: [OP_CREATE, OP_LIST], timeoutMs: 45000 }
  );
  await expectDeclarationListed(locators, draft.sourceName);
}

export async function openRowMenu(locators: KgManualDependenciesLocators, name: string): Promise<void> {
  await expectDeclarationListed(locators, name);
  await locators.rowMenuBtn(name).click();
  await expect(locators.editMenuItem).toBeVisible({ timeout: 10000 });
}

// Picks one Status filter option and waits for the list read it triggers, so a
// non-retrying read taken afterwards cannot catch the table mid-fetch.
export async function pickStatusFilter(page: Page, locators: KgManualDependenciesLocators, label: string): Promise<void> {
  await locators.statusFilter.click();
  await expect(locators.statusFilterPanel).toBeVisible({ timeout: 15000 });
  await waitForGraphQLAndValidate(
    page,
    async () => {
      await locators.statusOption(label).click();
      await expect(locators.statusFilterPanel).toBeHidden({ timeout: 15000 });
    },
    { testName: `KG Manual Dependencies - filter status to ${label}`, operationNames: OP_LIST, timeoutMs: 45000 }
  );
  await expect(locators.statusFilter).toContainText(label, { timeout: 15000 });
  await waitForListLoaded(locators);
}

// Opens a ds/Select and commits one option. A single-selection panel closes on its
// own after the pick, and the trigger then shows the chosen label, which is the
// signal that the selection landed rather than a fixed pause.
export async function pickSelectOption(locators: KgManualDependenciesLocators, trigger: Locator, label: string): Promise<void> {
  await trigger.click();
  await locators.openListbox.waitFor({ state: "visible", timeout: 15000 });
  await locators.selectOption(label).click();
  await locators.openListbox.waitFor({ state: "detached", timeout: 15000 });
  await expect(trigger).toContainText(label, { timeout: 15000 });
}

// Deletes the first row matching `name` through its own menu and confirm,
// asserting the delete and the reload after it both came back clean. Absence is
// the caller's assertion: a sweep over a shared prefix legitimately leaves
// further matches behind for its next pass.
export async function deleteDeclaration(page: Page, locators: KgManualDependenciesLocators, name: string): Promise<void> {
  await openRowMenu(locators, name);
  await locators.deleteMenuItem.click();
  await expect(locators.confirmDeleteDialog).toBeVisible({ timeout: 15000 });
  await waitForGraphQLAndValidate(
    page,
    async () => {
      await locators.confirmDeleteBtn.click();
    },
    { testName: "KG Manual Dependencies - delete a declaration", operationNames: [OP_DELETE, OP_LIST], timeoutMs: 45000 }
  );
  // Outside the watcher: the dialog only closes once the delete succeeds, so a slow or failed delete must surface as the watcher's API result.
  await locators.confirmDeleteDialog.waitFor({ state: "detached", timeout: 30000 });
  await waitForListLoaded(locators);
}

// Removes declarations a failed earlier run left behind under this suite's
// prefix, so leftovers cannot accumulate on the shared tenant. Bounded, and
// every pass must observe its own delete land, so it cannot spin.
export async function sweepStaleDeclarations(page: Page, locators: KgManualDependenciesLocators): Promise<number> {
  let removed = 0;
  for (let i = 0; i < MAX_SWEEP; i++) {
    if (!(await findDeclarationRow(locators, DECLARATION_NAME_PREFIX))) break;
    await deleteDeclaration(page, locators, DECLARATION_NAME_PREFIX);
    removed += 1;
  }
  return removed;
}

// Best-effort close of a dialog a failed step may have left on top, so the cleanup
// clicks below are not swallowed by a modal backdrop.
async function closeIfOpen(dialog: Locator, cancelBtn: Locator): Promise<void> {
  // A test that never opened this dialog is the normal case here, so absence has
  // to come back as false rather than throw.
  const open = await dialog.isVisible().catch(() => false);
  if (!open) return;
  await cancelBtn.click();
  await dialog.waitFor({ state: "detached", timeout: 10000 });
}

// Teardown for a declaration this suite created. It closes whatever a failed step
// left open, reopens Settings so a narrowed status filter cannot hide the row,
// scopes the delete to the row's own menu rather than a loose text match, and
// asserts the row is gone so a silently failed cleanup cannot leave a stray
// declaration on the shared tenant.
export async function deleteDeclarationIfPresent(page: Page, locators: KgManualDependenciesLocators, name: string): Promise<void> {
  try {
    await closeIfOpen(locators.confirmDeleteDialog, locators.confirmCancelBtn);
    await closeIfOpen(locators.declarationDialog, locators.declarationCancelBtn);
    // An open row menu is a modal too, and Escape closes only the topmost one, so
    // it is pressed only when a menu is actually up. "No menu open" is the normal
    // case and must come back as false.
    if (await locators.editMenuItem.isVisible().catch(() => false)) {
      await page.keyboard.press("Escape");
      await locators.editMenuItem.waitFor({ state: "hidden", timeout: 10000 });
    }
    // Whether the case ended with Settings open is a normal branch either way.
    if (await locators.settingsDialog.isVisible().catch(() => false)) {
      await remountManualDependencies(locators);
    } else {
      await openSettingsOnManualDeclarations(locators);
    }
    // A test that never got as far as saving leaves nothing to delete, so a row
    // found on no page is a normal outcome here.
    if (!(await findDeclarationRow(locators, name))) return;
    await deleteDeclaration(page, locators, name);
    await expectDeclarationAbsent(locators, name);
  } catch (error) {
    console.warn(`[cleanup] the generated manual dependency could not be deleted: ${error}`);
  }
}
