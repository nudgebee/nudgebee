// Not for OSS
import { test, expect } from "@playwright/test";
import { KG_RELATIONSHIP_LABELS, KG_LEVEL_OPTIONS } from "./knowledgeGraphLocators";
import { openKnowledgeGraph, waitForCanvasSettled, openFilterDropdown, closeFilterDropdown } from "./knowledgeGraphHelper";

// Troubleshoot > Knowledge Graph (/troubleshoot#kg).
//
// The module is read-only: it renders a tenant-wide graph and filters it. Nothing here
// creates, edits or deletes anything, and every applied filter is client-side view state
// that dies with the page — see "Follow-ups" in the PR for the one write surface (the
// tenant-admin Settings dialog) this file deliberately leaves alone.
//
// The Account / Node Type / Node dropdowns are NOT driven here. Three CI runs established
// that their option rows cannot be reached from a test: see the PR's "What CI established"
// section for the evidence and the app-side change that would unblock them.
test.describe.configure({ timeout: 180000 });

test(
  "Knowledge Graph sanity - open the Troubleshoot Knowledge Graph tab, verify the filter panel, graph canvas and canvas toolbar all render",
  { tag: ["@dev", "@sanity", "@functional"] },
  async ({ page }) => {
    const kg = await openKnowledgeGraph(page);

    await expect(kg.filterPanel).toBeVisible();
    await expect(kg.canvas).toBeVisible();
    await expect(kg.relationshipsBtn).toBeVisible();
    await expect(kg.nodeSearch).toBeVisible();

    // The canvas resolved to a real terminal state rather than sitting on the loader.
    await expect(kg.canvasSettled).toBeVisible();
  }
);

test(
  "Knowledge Graph sanity - open the filter panel, verify it offers the Account, Node Type, Node and Level filters with Apply disabled until something changes",
  { tag: ["@dev", "@sanity", "@functional"] },
  async ({ page }) => {
    const kg = await openKnowledgeGraph(page);

    await expect(kg.filterAccount).toBeVisible();
    await expect(kg.filterNodeType).toBeVisible();
    await expect(kg.filterNode).toBeVisible();
    await expect(kg.filterLevel).toBeVisible();
    await expect(kg.clearAllBtn).toBeVisible();

    // `hasChanges` is false on a freshly loaded panel — draft and applied state are identical.
    await expect(kg.applyFiltersBtn).toBeDisabled();
  }
);

test(
  "Knowledge Graph - collapse the filter panel, verify the panel is replaced by the expand rail, expand it again, verify the filters come back",
  { tag: ["@dev", "@smoke", "@functional"] },
  async ({ page }) => {
    const kg = await openKnowledgeGraph(page);

    await expect(kg.filterPanel).toBeVisible();
    await kg.collapseFiltersBtn.click();

    await expect(kg.filterPanel).toBeHidden();
    await expect(kg.expandFiltersBtn).toBeVisible();
    // The graph keeps rendering while the sidebar is away — collapsing must not tear it down.
    await expect(kg.canvasSettled).toBeVisible();

    await kg.expandFiltersBtn.click();

    await expect(kg.filterPanel).toBeVisible();
    await expect(kg.filterNodeType).toBeVisible();
    await expect(kg.expandFiltersBtn).toBeHidden();
  }
);

test(
  "Knowledge Graph - open the Level filter, verify it offers all three traversal depths from direct neighbours to 3 hops",
  { tag: ["@dev", "@regression", "@functional"] },
  async ({ page }) => {
    const kg = await openKnowledgeGraph(page);

    await openFilterDropdown(page, kg.filterLevel, kg.levelPanel);

    // The three depths are static in the module (KnowledgeGraph.jsx:112-114), so this holds
    // whatever the tenant's graph contains.
    await expect(kg.levelOptions).toHaveCount(KG_LEVEL_OPTIONS.length);
    for (const depth of KG_LEVEL_OPTIONS) {
      await expect(kg.levelOptions.filter({ hasText: depth })).toBeVisible();
    }

    await closeFilterDropdown(page, kg.levelPanel);
  }
);

test(
  "Knowledge Graph - open the Level filter, pick 2 hops with no node selected, verify Apply Filters stays disabled because level only applies to selected nodes",
  { tag: ["@dev", "@regression", "@negative", "@validation"] },
  async ({ page }) => {
    const kg = await openKnowledgeGraph(page);

    await expect(kg.applyFiltersBtn).toBeDisabled();

    await openFilterDropdown(page, kg.filterLevel, kg.levelPanel);
    await kg.levelTwoHopsOption.click();
    await closeFilterDropdown(page, kg.levelPanel);

    // The trigger took the new level, so the click landed.
    await expect(kg.filterLevel).toContainText("2 hops");
    // But `hasChanges` gates level behind a node selection (KnowledgeGraph.jsx:1933) — the
    // API ignores level otherwise, so the panel must refuse to offer an apply that does nothing.
    await expect(kg.applyFiltersBtn).toBeDisabled();
  }
);

test(
  "Knowledge Graph - change Level to 2 hops, click Clear All, verify Level resets to direct neighbours and the graph is still rendered",
  { tag: ["@dev", "@regression", "@functional"] },
  async ({ page }) => {
    const kg = await openKnowledgeGraph(page);

    await openFilterDropdown(page, kg.filterLevel, kg.levelPanel);
    await kg.levelTwoHopsOption.click();
    await closeFilterDropdown(page, kg.levelPanel);
    await expect(kg.filterLevel).toContainText("2 hops");

    await kg.clearAllBtn.click();

    // handleClear resets draft AND applied state together, so the sidebar has to show the
    // default depth again — not just the graph.
    await expect(kg.filterLevel).toContainText("Direct neighbors", { timeout: 60000 });
    await expect(kg.applyFiltersBtn).toBeDisabled();
    await waitForCanvasSettled(kg);
  }
);

test(
  "Knowledge Graph - reload the page on the kg fragment, verify the module comes back on the Knowledge Graph tab rather than the default All Events",
  { tag: ["@dev", "@regression", "@functional"] },
  async ({ page }) => {
    const kg = await openKnowledgeGraph(page);
    await expect(kg.filterPanel).toBeVisible();

    await page.reload();

    // Troubleshoot defaults to All Events, which renders no filter panel and no graph canvas,
    // so both landing here prove the hash fragment survived the reload.
    await expect(page).toHaveURL(/#kg/);
    await expect(kg.filterPanel).toBeVisible({ timeout: 60000 });
    await expect(kg.canvas).toBeVisible();
    await waitForCanvasSettled(kg);
  }
);

test(
  "Knowledge Graph - collapse the filter panel, reload the page, verify the panel returns expanded because the collapse is view-only state",
  { tag: ["@dev", "@regression", "@functional"] },
  async ({ page }) => {
    const kg = await openKnowledgeGraph(page);

    await kg.collapseFiltersBtn.click();
    await expect(kg.filterPanel).toBeHidden();

    await page.reload();

    // isFilterCollapsed is component state with no persistence, so a reload must restore the
    // expanded panel. This pins that the collapse is deliberately not remembered.
    await expect(kg.filterPanel).toBeVisible({ timeout: 60000 });
    await expect(kg.expandFiltersBtn).toBeHidden();
    await waitForCanvasSettled(kg);
  }
);

test(
  "Knowledge Graph - hover the Relationships control on the canvas toolbar, verify the legend lists the graph's relationship types",
  { tag: ["@dev", "@regression", "@functional"] },
  async ({ page }) => {
    const kg = await openKnowledgeGraph(page);

    await kg.relationshipsBtn.hover();

    await expect(kg.legendTooltip).toBeVisible();
    // The knowledge_graph legend is a static taxonomy (ServiceMapLegends.jsx:35-70), so
    // these hold whatever the tenant's graph contains.
    for (const relationship of KG_RELATIONSHIP_LABELS) {
      await expect(kg.legendTooltip).toContainText(relationship);
    }
  }
);
