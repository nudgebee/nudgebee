// Not for OSS
import { test, expect } from "@playwright/test";
import { readFile } from "fs/promises";
import { trackOperations } from "../egress-filter/egressFilterHelper";
import { BULK_OPS, OP_LIST, expectDeclarationAbsent, expectDeclarationListed } from "../kg-manual-dependencies/kgManualDependenciesHelper";
import {
  NODE_BULK_OPS,
  OP_IMPORT,
  deleteImported,
  draftDependency,
  fillDocument,
  finishImport,
  openImportDialog,
  openJsonImport,
  sourceEndpoint,
  sourceEndpointText,
  submitImport,
  uniqueNodeName,
} from "./kgJsonImportHelper";

// Nubi > b-Cortex > Knowledge Graph > Settings > Manual Declarations > Import JSON
// (app/src/components/knowledge-graph/JsonImportDialog.jsx). Every imported
// dependency carries a run-unique service name no real workload has, every node
// a run-unique name outside any connected account, and each is deleted again
// through its own row menu. The tenant-wide Re-resolve all and Delete all are
// never clicked; every writing case asserts they stayed at zero.

const NEVER = [...BULK_OPS, ...NODE_BULK_OPS];

test.describe("Nubi KG JSON Import", () => {
  test(
    "Nubi KG JSON Import sanity - open Import JSON, verify the editor shows its empty placeholder and Import is disabled, enter a document, cancel, verify no import request is sent, reopen, verify the editor is empty again",
    { tag: ["@dev", "@sanity", "@functional", "@negative"] },
    async ({ page }) => {
      test.setTimeout(240000);
      const counts = trackOperations(page, [OP_IMPORT]);
      const locators = await openJsonImport(page);

      await expect(locators.jsonEditorPlaceholder).toBeVisible();
      await expect(locators.importBtn).toBeDisabled();
      await expect(locators.uploadBtn).toBeEnabled();
      await expect(locators.importCancelBtn).toBeEnabled();

      const dep = draftDependency();
      await fillDocument(locators, { dependencies: [{ source: sourceEndpoint(dep), dest: { node_type: "K8sService", name: dep.destName } }] });
      await locators.importCancelBtn.click();
      await locators.importDialog.waitFor({ state: "detached", timeout: 15000 });
      expect(counts[OP_IMPORT]).toBe(0);

      // Closing resets the dialog, so the document must not survive a reopen.
      await openImportDialog(locators);
      await expect(locators.jsonEditorPlaceholder).toBeVisible();
      await expect(locators.importBtn).toBeDisabled();
      await locators.importCancelBtn.click();
      await locators.importDialog.waitFor({ state: "detached", timeout: 15000 });
      await expectDeclarationAbsent(locators, dep.sourceName);
    }
  );

  test(
    "Nubi KG JSON Import sanity - click View format, verify the document format and the node type values by account type appear, click Hide format, verify the panel closes",
    { tag: ["@dev", "@sanity", "@functional"] },
    async ({ page }) => {
      test.setTimeout(240000);
      const locators = await openJsonImport(page);

      await expect(locators.formatToggleBtn).toHaveText("View format");
      await expect(locators.formatCodeBlock).toHaveCount(0);
      await locators.formatToggleBtn.click();
      await expect(locators.formatToggleBtn).toHaveText("Hide format");
      await expect(locators.formatCodeBlock).toBeVisible();
      await expect(locators.typeValuesPanel).toBeVisible({ timeout: 15000 });
      await expect(locators.typeValuesPanel.getByRole("group", { name: "Account type" })).toBeVisible();

      await locators.formatToggleBtn.click();
      await expect(locators.formatToggleBtn).toHaveText("View format");
      await expect(locators.formatCodeBlock).toHaveCount(0);
      await expect(locators.typeValuesPanel).toHaveCount(0);
    }
  );

  test(
    "Nubi KG JSON Import - download the sample, verify the file is sample_manual_declarations.json holding nodes and dependencies whose string sides all name a declared ref, and that no import request is sent",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      test.setTimeout(240000);
      const counts = trackOperations(page, [OP_IMPORT]);
      const locators = await openJsonImport(page);

      const [download] = await Promise.all([page.waitForEvent("download", { timeout: 30000 }), locators.sampleDownloadBtn.click()]);
      expect(download.suggestedFilename()).toBe("sample_manual_declarations.json");
      const path = await download.path();
      const sample = JSON.parse(await readFile(path, "utf8"));
      expect(sample.nodes.length).toBeGreaterThan(0);
      expect(sample.dependencies.length).toBeGreaterThan(0);
      const refs = sample.nodes.map((n: { ref: string }) => n.ref);
      expect(new Set(refs).size).toBe(refs.length);
      for (const d of sample.dependencies) {
        for (const side of [d.source, d.dest]) {
          if (typeof side === "string") expect(refs).toContain(side);
        }
      }
      expect(counts[OP_IMPORT]).toBe(0);
    }
  );

  test(
    "Nubi KG JSON Import - enter a document that is not valid JSON, click Import, verify a Not valid JSON toast and no request; enter one that uses a ref twice, verify the server refuses it with the duplicate ref named and nothing is listed",
    { tag: ["@dev", "@regression", "@negative", "@validation"] },
    async ({ page }) => {
      test.setTimeout(240000);
      const counts = trackOperations(page, [OP_IMPORT, ...NEVER]);
      const locators = await openJsonImport(page);
      const dep = draftDependency();

      await fillDocument(locators, '{ "nodes": [ ');
      await locators.importBtn.click();
      await expect(locators.errorToast(/^Not valid JSON/)).toBeVisible({ timeout: 15000 });
      expect(counts[OP_IMPORT]).toBe(0);

      const name = uniqueNodeName();
      await fillDocument(locators, {
        nodes: [
          { ref: "dup", node_type: "Database", name },
          { ref: "dup", node_type: "Cache", name },
        ],
        dependencies: [{ source: sourceEndpoint(dep), dest: "dup" }],
      });
      await locators.importBtn.click();
      await expect(locators.errorToast(/ref "dup" is already used by nodes\[0\]/)).toBeVisible({ timeout: 30000 });
      await expect(locators.resultSummary).toHaveCount(0);
      expect(counts[OP_IMPORT]).toBe(1);

      await locators.importCancelBtn.click();
      await locators.importDialog.waitFor({ state: "detached", timeout: 15000 });
      await expectDeclarationAbsent(locators, dep.sourceName);
      await expect(locators.nodeRow(name)).toHaveCount(0);
      for (const op of NEVER) expect(counts[op], `${op} must never fire from this suite`).toBe(0);
    }
  );

  test(
    "Nubi KG JSON Import - import one dependency between two graph endpoints, verify 0 nodes and 1 dependency created, click Done, verify the row is listed with its qualifiers and the PUBLISHES_TO type",
    { tag: ["@dev", "@regression", "@functional", "@crud"] },
    async ({ page }) => {
      test.setTimeout(300000);
      const counts = trackOperations(page, [OP_IMPORT, OP_LIST, ...NEVER]);
      const locators = await openJsonImport(page);
      const dep = draftDependency();
      try {
        await fillDocument(locators, {
          dependencies: [{ source: sourceEndpoint(dep), dest: { node_type: "K8sService", name: dep.destName }, relationship_type: "PUBLISHES_TO" }],
        });
        await submitImport(page, locators, "0 nodes created, 0 existing, 0 rejected · 1 dependency created, 0 existing, 0 rejected");
        await expect(locators.resultRowsWith("created")).toHaveCount(1);

        await finishImport(page, locators);
        await expectDeclarationListed(locators, dep.sourceName);
        await expect(locators.declarationRow(dep.sourceName)).toContainText(sourceEndpointText(dep));
        await expect(locators.rowRelationshipLabel(dep.sourceName)).toHaveText("PUBLISHES_TO");
        expect(counts[OP_IMPORT]).toBe(1);
        for (const op of NEVER) expect(counts[op], `${op} must never fire from this suite`).toBe(0);
      } finally {
        await deleteImported(page, locators, [dep.sourceName]);
      }
    }
  );

  test(
    "Nubi KG JSON Import - upload a file declaring a node outside any account and a dependency that names it by ref, verify the file name shows, 1 node and 1 dependency created, click Done, verify the node is listed in Nodes and the dependency's destination is that node",
    { tag: ["@dev", "@regression", "@functional", "@crud"] },
    async ({ page }) => {
      test.setTimeout(300000);
      const counts = trackOperations(page, [OP_IMPORT, ...NEVER]);
      const locators = await openJsonImport(page);
      const dep = draftDependency();
      const node = uniqueNodeName();
      const fileName = `${node}.json`;
      try {
        const doc = {
          nodes: [{ ref: "db", node_type: "Database", name: node, namespace: "nb-e2e-ns", properties: { engine: "postgres" } }],
          dependencies: [{ source: sourceEndpoint(dep), dest: "db" }],
        };
        await locators.fileInput.setInputFiles({ name: fileName, mimeType: "application/json", buffer: Buffer.from(JSON.stringify(doc), "utf8") });
        await expect(locators.fileName).toHaveText(`Loaded ${fileName}`, { timeout: 15000 });
        await expect(locators.importBtn).toBeEnabled({ timeout: 10000 });

        await submitImport(page, locators, "1 node created, 0 existing, 0 rejected · 1 dependency created, 0 existing, 0 rejected");
        await expect(locators.resultRowsWith("created").first()).toContainText(`#0 db → node`);

        await finishImport(page, locators);
        await expect(locators.nodeRow(node)).toBeVisible({ timeout: 30000 });
        await expect(locators.nodeRow(node)).toContainText("Outside connected accounts");
        await expectDeclarationListed(locators, dep.sourceName);
        await expect(locators.declarationRow(dep.sourceName)).toContainText(`Database: nb-e2e-ns/${node}`);
        expect(counts[OP_IMPORT]).toBe(1);
        for (const op of NEVER) expect(counts[op], `${op} must never fire from this suite`).toBe(0);
      } finally {
        await deleteImported(page, locators, [dep.sourceName], [node]);
      }
    }
  );
});
