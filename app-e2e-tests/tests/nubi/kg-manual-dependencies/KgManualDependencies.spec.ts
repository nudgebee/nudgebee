// Not for OSS
import { test, expect } from "@playwright/test";
import { waitForGraphQLAndValidate } from "../../utils/GraphQLNetworkWatcher";
import { trackOperations } from "../egress-filter/egressFilterHelper";
import { RESOLUTION_STATUS_LABELS, STATUS_FILTER_LABELS } from "./kgManualDependenciesLocators";
import {
  BULK_OPS,
  OP_CREATE,
  OP_DELETE,
  OP_LIST,
  OP_RERESOLVE,
  OP_UPDATE,
  createDeclaration,
  deleteDeclarationIfPresent,
  draftDeclaration,
  expectDeclarationAbsent,
  expectDeclarationListed,
  fillDeclarationForm,
  openManualDependencies,
  openNewDeclarationDialog,
  openRowMenu,
  pickSelectOption,
  pickStatusFilter,
  remountManualDependencies,
  sourceEndpointText,
  sweepStaleDeclarations,
  walkPages,
} from "./kgManualDependenciesHelper";

// Nubi > b-Cortex > Knowledge Graph > Settings > Manual Declarations, Dependencies section
// (app/src/components/knowledge-graph/ManualDependenciesSection.jsx). Declarations are
// written, always under run-unique service names that no real workload carries so
// the resolver can never pin them to a live node, and always deleted again. The
// toolbar's Re-resolve all and Delete all act on the whole tenant and are never
// clicked; every CRUD case asserts both bulk operations stayed at zero. The
// Coverage tab's save is tenant-wide policy and is never pressed either. The
// table paginates client-side past ten rows and snaps back to page 1 after
// every row-count change, so listed/absent checks always walk the pager.

test.describe("Nubi KG Manual Dependencies", () => {
  test(
    "Nubi KG Manual Dependencies sanity - open b-Cortex, select Knowledge Graph, open Settings, switch to Manual Declarations, verify the tab is selected and shows the declaration guidance, the Status filter, Import JSON, Declare node and Declare dependency, and the list settles on rows or its empty state",
    { tag: ["@dev", "@sanity", "@functional"] },
    async ({ page }) => {
      test.setTimeout(240000);
      const locators = await openManualDependencies(page);

      // openManualDependencies() already proved Settings landed on Coverage first;
      // this is the state after the switch.
      await expect(locators.manualDeclarationsTab).toHaveAttribute("aria-selected", "true");
      await expect(locators.coverageTab).toHaveAttribute("aria-selected", "false");
      await expect(locators.coverageDescription).toHaveCount(0);
      await expect(locators.manualDepsDescription).toContainText("CALLS / PUBLISHES_TO / SUBSCRIBES_TO");

      await expect(locators.statusFilter).toBeVisible({ timeout: 15000 });
      await expect(locators.statusFilter).toContainText("Status");
      await expect(locators.statusFilter).toContainText("All");
      await expect(locators.importJsonBtn).toBeEnabled();
      await expect(locators.declareNodeBtn).toBeEnabled();
      await expect(locators.declareDependencyBtn).toBeEnabled();
      await expect(locators.rows().first().or(locators.emptyState)).toBeVisible();
    }
  );

  test(
    "Nubi KG Manual Dependencies sanity - open the Status filter, verify it offers All, Resolved, Pending, Ambiguous, Unmatched, Too many matches and Node inactive, close it without picking, verify All stays selected and no list request is sent",
    { tag: ["@dev", "@sanity", "@functional"] },
    async ({ page }) => {
      test.setTimeout(240000);
      const locators = await openManualDependencies(page);
      // Attached after the first load so only a filter-triggered read could count.
      const counts = trackOperations(page, [OP_LIST]);

      await locators.statusFilter.click();
      await expect(locators.statusFilterPanel).toBeVisible({ timeout: 15000 });
      await expect(locators.statusFilterOptions).toHaveCount(STATUS_FILTER_LABELS.length);
      for (const label of STATUS_FILTER_LABELS) {
        await expect(locators.statusOption(label)).toBeVisible();
      }

      await page.keyboard.press("Escape");
      await expect(locators.statusFilterPanel).toBeHidden({ timeout: 15000 });
      await expect(locators.statusFilter).toContainText("All");
      expect(counts[OP_LIST]).toBe(0);
    }
  );

  test(
    "Nubi KG Manual Dependencies - open Declare dependency, verify Create is blocked while both names are empty, enter a source name, verify it stays blocked, enter a destination name, verify Create enables, cancel, close and reopen Settings, verify no row with that name is listed and no create mutation is sent",
    { tag: ["@dev", "@regression", "@negative", "@validation"] },
    async ({ page }) => {
      test.setTimeout(240000);
      const counts = trackOperations(page, [OP_CREATE]);
      const locators = await openManualDependencies(page);
      const draft = draftDeclaration();

      await openNewDeclarationDialog(locators);
      // The backend requires a name on both sides, and the form mirrors that gate.
      await expect(locators.createOrSaveBtn).toBeDisabled();

      await locators.sourceNameInput.fill(draft.sourceName);
      await expect(locators.sourceNameInput).toHaveValue(draft.sourceName, { timeout: 10000 });
      await expect(locators.createOrSaveBtn).toBeDisabled();

      await locators.destNameInput.fill(draft.destName);
      await expect(locators.destNameInput).toHaveValue(draft.destName, { timeout: 10000 });
      await expect(locators.createOrSaveBtn).toBeEnabled({ timeout: 10000 });

      await locators.declarationCancelBtn.click();
      await locators.declarationDialog.waitFor({ state: "detached", timeout: 15000 });
      await expectDeclarationAbsent(locators, draft.sourceName);

      await remountManualDependencies(locators);
      await expectDeclarationAbsent(locators, draft.sourceName);
      expect(counts[OP_CREATE]).toBe(0);
    }
  );

  test(
    "Nubi KG Manual Dependencies - open Declare dependency, switch the Source kind to Cloud, verify Namespace and Cluster give way to Resource ID, Cloud account and Region and the node type resets to Database, switch back to Kubernetes, verify Namespace and Cluster return with K8sService, cancel, verify no create mutation is sent",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      test.setTimeout(240000);
      const counts = trackOperations(page, [OP_CREATE]);
      const locators = await openManualDependencies(page);

      await openNewDeclarationDialog(locators);
      await expect(locators.sourceNamespaceInput).toBeVisible();
      await expect(locators.sourceClusterInput).toBeVisible();
      await expect(locators.sourceNodeTypeSelect).toContainText("K8sService");
      await expect(locators.sourceResourceIdInput).toHaveCount(0);

      await locators.sourceCloudKindBtn.click();
      await expect(locators.sourceResourceIdInput).toBeVisible({ timeout: 10000 });
      await expect(locators.sourceRegionInput).toBeVisible();
      await expect(locators.sourceColumn).toContainText("Cloud account");
      // Cloud account opens on its clear option, so the picker is populated even
      // before the tenant's accounts are listed.
      await expect(locators.sourceColumn).toContainText("Any account");
      await expect(locators.sourceNamespaceInput).toHaveCount(0);
      await expect(locators.sourceClusterInput).toHaveCount(0);
      await expect(locators.sourceNodeTypeSelect).toContainText("Database");

      await locators.sourceKubernetesKindBtn.click();
      await expect(locators.sourceNamespaceInput).toBeVisible({ timeout: 10000 });
      await expect(locators.sourceClusterInput).toBeVisible();
      await expect(locators.sourceResourceIdInput).toHaveCount(0);
      await expect(locators.sourceNodeTypeSelect).toContainText("K8sService");

      await locators.declarationCancelBtn.click();
      await locators.declarationDialog.waitFor({ state: "detached", timeout: 15000 });
      expect(counts[OP_CREATE]).toBe(0);
    }
  );

  test(
    "Nubi KG Manual Dependencies - create a CALLS declaration between two unique Kubernetes services with a namespace and cluster, verify the row lists the qualified source, the destination, the CALLS type and a resolution status, close and reopen Settings, verify the declaration persisted",
    { tag: ["@dev", "@regression", "@crud"] },
    async ({ page }) => {
      test.setTimeout(300000);
      const counts = trackOperations(page, [OP_CREATE, ...BULK_OPS]);
      const locators = await openManualDependencies(page);
      const draft = draftDeclaration();
      // Leftovers from a run whose cleanup failed carry this suite's prefix; clearing
      // them first keeps the shared tenant's list from growing run over run.
      await sweepStaleDeclarations(page, locators);

      try {
        await createDeclaration(page, locators, draft);

        const row = locators.declarationRow(draft.sourceName);
        await expect(row).toContainText(sourceEndpointText(draft));
        await expect(row).toContainText(`K8sService: ${draft.destName}`);
        await expect(locators.rowRelationshipLabel(draft.sourceName)).toHaveText("CALLS");
        // The resolver runs on create; whichever way it lands, the row must carry
        // one of the table's own status labels rather than a raw enum value.
        await expect(locators.rowStatusLabel(draft.sourceName)).toHaveText(RESOLUTION_STATUS_LABELS);

        // The remount is what separates a persisted row from one still held in
        // React state: the tab refetches kg_list_manual_dependencies on mount.
        await remountManualDependencies(locators);
        await expectDeclarationListed(locators, draft.sourceName);
        await expect(locators.declarationRow(draft.sourceName)).toContainText(sourceEndpointText(draft));
        await expect(locators.rowRelationshipLabel(draft.sourceName)).toHaveText("CALLS");
        expect(counts[OP_CREATE]).toBe(1);
        for (const op of BULK_OPS) expect(counts[op]).toBe(0);
      } finally {
        await deleteDeclarationIfPresent(page, locators, draft.sourceName);
      }
    }
  );

  test(
    "Nubi KG Manual Dependencies - create a declaration, reopen it with Edit, verify the form is prefilled with the stored names and qualifiers, change the relationship to PUBLISHES_TO and add a unique note, save, close and reopen Settings, verify the row shows PUBLISHES_TO and Edit shows the note",
    { tag: ["@dev", "@regression", "@crud"] },
    async ({ page }) => {
      test.setTimeout(300000);
      const counts = trackOperations(page, BULK_OPS);
      const locators = await openManualDependencies(page);
      const draft = draftDeclaration();
      const note = `nb-e2e note ${Date.now()}`;
      // Leftovers from a run whose cleanup failed carry this suite's prefix; clearing
      // them first keeps the shared tenant's list from growing run over run.
      await sweepStaleDeclarations(page, locators);

      try {
        await createDeclaration(page, locators, draft);

        await openRowMenu(locators, draft.sourceName);
        await locators.editMenuItem.click();
        await expect(locators.editDeclarationDialog).toBeVisible({ timeout: 15000 });
        // Edit mode prefills from the stored row, so these prove the created values
        // round-tripped through the backend rather than what the form last held.
        await expect(locators.sourceNameInput).toHaveValue(draft.sourceName, { timeout: 15000 });
        await expect(locators.sourceNamespaceInput).toHaveValue(draft.sourceNamespace);
        await expect(locators.sourceClusterInput).toHaveValue(draft.sourceCluster);
        await expect(locators.destNameInput).toHaveValue(draft.destName);
        await expect(locators.relationshipSelect).toContainText("CALLS");

        await pickSelectOption(locators, locators.relationshipSelect, "PUBLISHES_TO");
        await locators.notesInput.fill(note);
        await expect(locators.notesInput).toHaveValue(note, { timeout: 10000 });
        await waitForGraphQLAndValidate(
          page,
          async () => {
            await locators.createOrSaveBtn.click();
            await locators.declarationDialog.waitFor({ state: "detached", timeout: 30000 });
          },
          { testName: "KG Manual Dependencies - update a declaration", operationNames: [OP_UPDATE, OP_LIST], timeoutMs: 45000 }
        );

        await remountManualDependencies(locators);
        await expectDeclarationListed(locators, draft.sourceName);
        await expect(locators.rowRelationshipLabel(draft.sourceName)).toHaveText("PUBLISHES_TO");

        // Notes are not a table column, so the reopened form is where the stored
        // note is observable.
        await openRowMenu(locators, draft.sourceName);
        await locators.editMenuItem.click();
        await expect(locators.editDeclarationDialog).toBeVisible({ timeout: 15000 });
        await expect(locators.notesInput).toHaveValue(note, { timeout: 15000 });
        await expect(locators.relationshipSelect).toContainText("PUBLISHES_TO");
        await locators.declarationCancelBtn.click();
        await locators.declarationDialog.waitFor({ state: "detached", timeout: 15000 });
        for (const op of BULK_OPS) expect(counts[op]).toBe(0);
      } finally {
        await deleteDeclarationIfPresent(page, locators, draft.sourceName);
      }
    }
  );

  test(
    "Nubi KG Manual Dependencies - create a declaration then choose Delete from its row menu, verify the confirm names the source and destination, cancel it, verify the row is still listed, confirm the delete, verify the row is gone and stays gone after closing and reopening Settings",
    { tag: ["@dev", "@regression", "@crud"] },
    async ({ page }) => {
      test.setTimeout(300000);
      const counts = trackOperations(page, [OP_DELETE, ...BULK_OPS]);
      const locators = await openManualDependencies(page);
      const draft = draftDeclaration();
      // Leftovers from a run whose cleanup failed carry this suite's prefix; clearing
      // them first keeps the shared tenant's list from growing run over run.
      await sweepStaleDeclarations(page, locators);

      try {
        await createDeclaration(page, locators, draft);

        await openRowMenu(locators, draft.sourceName);
        await locators.deleteMenuItem.click();
        await expect(locators.confirmDeleteDialog).toBeVisible({ timeout: 15000 });
        await expect(locators.confirmDeleteDialog).toContainText(draft.sourceName);
        await expect(locators.confirmDeleteDialog).toContainText(draft.destName);
        await expect(locators.confirmDeleteDialog).toContainText("CALLS");

        await locators.confirmCancelBtn.click();
        await locators.confirmDeleteDialog.waitFor({ state: "detached", timeout: 15000 });
        await expect(locators.declarationRow(draft.sourceName)).toBeVisible();
        expect(counts[OP_DELETE]).toBe(0);

        await openRowMenu(locators, draft.sourceName);
        await locators.deleteMenuItem.click();
        await expect(locators.confirmDeleteDialog).toBeVisible({ timeout: 15000 });
        await waitForGraphQLAndValidate(
          page,
          async () => {
            await locators.confirmDeleteBtn.click();
            await locators.confirmDeleteDialog.waitFor({ state: "detached", timeout: 30000 });
          },
          { testName: "KG Manual Dependencies - delete a declaration", operationNames: [OP_DELETE, OP_LIST], timeoutMs: 45000 }
        );
        await expectDeclarationAbsent(locators, draft.sourceName);

        await remountManualDependencies(locators);
        await expectDeclarationAbsent(locators, draft.sourceName);
        expect(counts[OP_DELETE]).toBe(1);
        for (const op of BULK_OPS) expect(counts[op]).toBe(0);
      } finally {
        await deleteDeclarationIfPresent(page, locators, draft.sourceName);
      }
    }
  );

  test(
    "Nubi KG Manual Dependencies - create a declaration, choose Re-resolve from its row menu, verify exactly one re-resolve request for that row is sent and the row stays listed with a resolution status, and the tenant-wide re-resolve is never sent",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      test.setTimeout(300000);
      const counts = trackOperations(page, [OP_RERESOLVE, ...BULK_OPS]);
      const locators = await openManualDependencies(page);
      const draft = draftDeclaration();
      // Leftovers from a run whose cleanup failed carry this suite's prefix; clearing
      // them first keeps the shared tenant's list from growing run over run.
      await sweepStaleDeclarations(page, locators);

      try {
        await createDeclaration(page, locators, draft);

        await openRowMenu(locators, draft.sourceName);
        await waitForGraphQLAndValidate(
          page,
          async () => {
            await locators.reresolveMenuItem.click();
          },
          { testName: "KG Manual Dependencies - re-resolve a declaration", operationNames: [OP_RERESOLVE, OP_LIST], timeoutMs: 45000 }
        );

        await expectDeclarationListed(locators, draft.sourceName);
        await expect(locators.rowStatusLabel(draft.sourceName)).toHaveText(RESOLUTION_STATUS_LABELS);
        expect(counts[OP_RERESOLVE]).toBe(1);
        for (const op of BULK_OPS) expect(counts[op]).toBe(0);
      } finally {
        await deleteDeclarationIfPresent(page, locators, draft.sourceName);
      }
    }
  );

  test(
    "Nubi KG Manual Dependencies - create a declaration, pick Resolved in the Status filter, verify the unresolvable new declaration drops out and every remaining row reports Resolved or the list is empty, pick All, verify the declaration is listed again",
    { tag: ["@dev", "@regression", "@search"] },
    async ({ page }) => {
      test.setTimeout(300000);
      const counts = trackOperations(page, BULK_OPS);
      const locators = await openManualDependencies(page);
      const draft = draftDeclaration();
      // Leftovers from a run whose cleanup failed carry this suite's prefix; clearing
      // them first keeps the shared tenant's list from growing run over run.
      await sweepStaleDeclarations(page, locators);

      try {
        await createDeclaration(page, locators, draft);
        // Both names are invented, so no KG node can match and the row can never
        // be Resolved — which is what makes it a safe probe for this filter.
        await expect(locators.rowStatusLabel(draft.sourceName)).not.toHaveText("Resolved");

        await pickStatusFilter(page, locators, "Resolved");
        await expectDeclarationAbsent(locators, draft.sourceName);
        // Every page the filter leaves behind must hold only Resolved chips.
        await walkPages(locators, async () => {
          await expect(locators.allStatusLabels().filter({ hasNotText: "Resolved" })).toHaveCount(0);
          return false;
        });

        await pickStatusFilter(page, locators, "All");
        await expectDeclarationListed(locators, draft.sourceName);
        for (const op of BULK_OPS) expect(counts[op]).toBe(0);
      } finally {
        await deleteDeclarationIfPresent(page, locators, draft.sourceName);
      }
    }
  );
});
