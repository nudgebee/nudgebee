// Not for OSS
import { Page, expect } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { NubiLocators } from "../nubiLocators";
import { waitForGraphQLAndValidate } from "../../utils/GraphQLNetworkWatcher";
import {
  DECLARATION_NAME_PREFIX,
  OP_LIST,
  deleteDeclarationIfPresent,
  openSettingsOnManualDeclarations,
  uniqueDeclarationName,
  waitForListLoaded,
} from "../kg-manual-dependencies/kgManualDependenciesHelper";
import { KgJsonImportLocators } from "./kgJsonImportLocators";

// app/src/api1/knowledge-graph/index.ts names these calls this way.
export const OP_IMPORT = "KgCreateManualDeclarations";
export const OP_LIST_NODES = "KgListManualNodes";
export const OP_DELETE_NODE = "KgDeleteManualNode";
export const NODE_BULK_OPS = ["KgDeleteAllManualNodes"];

// A dependency between two Kubernetes services no real workload carries, named
// with the sibling suite's prefix so its stale-row sweep covers what a failed run
// leaves behind. Namespace and cluster are invented qualifiers that only have to
// round-trip into the row's Source cell.
export interface DependencyDraft {
  sourceName: string;
  sourceNamespace: string;
  sourceCluster: string;
  destName: string;
}

export function draftDependency(): DependencyDraft {
  return {
    sourceName: uniqueDeclarationName("src"),
    sourceNamespace: "nb-e2e-ns",
    sourceCluster: "nb-e2e-cluster",
    destName: uniqueDeclarationName("dst"),
  };
}

export function sourceEndpoint(d: DependencyDraft) {
  return { node_type: "K8sService", name: d.sourceName, namespace: d.sourceNamespace, cluster: d.sourceCluster };
}

export function sourceEndpointText(d: DependencyDraft): string {
  return `K8sService: ${d.sourceNamespace}/${d.sourceName} @ ${d.sourceCluster}`;
}

// A node outside any connected account: it never collides with a collected
// resource, and its run-unique name makes it the row's identity.
export function uniqueNodeName(): string {
  return `${DECLARATION_NAME_PREFIX}node-${Date.now()}${Math.random().toString(36).slice(2, 6)}`;
}

// Logs in, lands on b-Cortex > Knowledge Graph > Settings > Manual Declarations
// and opens the Import JSON dialog with an empty editor.
export async function openJsonImport(page: Page): Promise<KgJsonImportLocators> {
  const loginPage = new LoginPage(page);
  const nubi = new NubiLocators(page);
  const locators = new KgJsonImportLocators(page);

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
  await openImportDialog(locators);
  return locators;
}

export async function openImportDialog(locators: KgJsonImportLocators): Promise<void> {
  await locators.importJsonBtn.click();
  await expect(locators.importDialog).toBeVisible({ timeout: 15000 });
  await expect(locators.jsonEditor).toBeVisible({ timeout: 15000 });
}

export async function fillDocument(locators: KgJsonImportLocators, document: unknown): Promise<void> {
  const text = typeof document === "string" ? document : JSON.stringify(document, null, 2);
  await locators.jsonEditor.fill(text);
  await expect(locators.importBtn).toBeEnabled({ timeout: 10000 });
}

// Submits the document, asserting the import call left the browser and came
// back clean, then reads the summary the dialog swaps in.
export async function submitImport(page: Page, locators: KgJsonImportLocators, summary: string): Promise<void> {
  await waitForGraphQLAndValidate(
    page,
    async () => {
      await locators.importBtn.click();
      await expect(locators.resultSummary).toBeVisible({ timeout: 30000 });
    },
    { testName: "KG JSON import - import a declarations document", operationNames: OP_IMPORT, timeoutMs: 60000 }
  );
  await expect(locators.resultSummary).toHaveText(summary);
}

// Done closes the dialog and both sections refetch; the dependency list read is
// asserted so the listed checks that follow cannot run against the old table.
export async function finishImport(page: Page, locators: KgJsonImportLocators): Promise<void> {
  await waitForGraphQLAndValidate(
    page,
    async () => {
      await locators.doneBtn.click();
      await locators.importDialog.waitFor({ state: "detached", timeout: 30000 });
    },
    { testName: "KG JSON import - close the result view", operationNames: [OP_LIST, OP_LIST_NODES], timeoutMs: 45000 }
  );
  await waitForListLoaded(locators);
}

async function closeImportDialog(locators: KgJsonImportLocators): Promise<void> {
  // A test that never opened, or already closed, the dialog is the normal case.
  if (await locators.importDialog.isVisible().catch(() => false)) {
    await locators.importDialogCloseBtn.click();
    await locators.importDialog.waitFor({ state: "detached", timeout: 15000 });
  }
}

// Teardown for a declared node: deleted through its own row menu and confirm,
// asserting the delete landed. A node that was never created is a normal outcome.
export async function deleteNodeIfPresent(page: Page, locators: KgJsonImportLocators, name: string): Promise<void> {
  try {
    await closeImportDialog(locators);
    if (!(await locators.settingsDialog.isVisible().catch(() => false))) {
      await openSettingsOnManualDeclarations(locators);
    }
    if (!(await locators.nodeRow(name).isVisible().catch(() => false))) return;
    await locators.nodeRowMenuBtn(name).click();
    await locators.nodeDeleteMenuItem.click();
    await expect(locators.confirmNodeDeleteDialog).toBeVisible({ timeout: 15000 });
    await waitForGraphQLAndValidate(
      page,
      async () => {
        await locators.confirmNodeDeleteBtn.click();
      },
      { testName: "KG JSON import - delete a declared node", operationNames: [OP_DELETE_NODE, OP_LIST_NODES], timeoutMs: 45000 }
    );
    await locators.confirmNodeDeleteDialog.waitFor({ state: "detached", timeout: 30000 });
    await expect(locators.nodeRow(name)).toHaveCount(0, { timeout: 30000 });
  } catch (error) {
    console.warn(`[cleanup] the declared node ${name} could not be deleted: ${error}`);
  }
}

// Teardown for everything a case imported: dependencies first (a node's delete
// would otherwise leave them unmatched), then nodes.
export async function deleteImported(page: Page, locators: KgJsonImportLocators, dependencySources: string[], nodeNames: string[] = []): Promise<void> {
  try {
    await closeImportDialog(locators);
  } catch (error) {
    console.warn(`[cleanup] the import dialog could not be closed: ${error}`);
  }
  for (const name of dependencySources) {
    await deleteDeclarationIfPresent(page, locators, name);
  }
  for (const name of nodeNames) {
    await deleteNodeIfPresent(page, locators, name);
  }
}
