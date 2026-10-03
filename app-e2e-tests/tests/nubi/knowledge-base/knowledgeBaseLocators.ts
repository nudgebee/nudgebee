import { Page, Locator, expect } from "@playwright/test";
import { NubiLocators } from "../nubiLocators";

export class KnowledgeBaseLocators {
  readonly page: Page;

  readonly bCortexBtn: Locator;
  readonly knowledgeGroupTab: Locator;
  readonly knowledgeBaseTab: Locator;
  readonly userSubTab: Locator;
  readonly addKBBtn: Locator;

  readonly nameInput: Locator;
  readonly descriptionInput: Locator;
  readonly contentTextarea: Locator;

  readonly allAgentsChip: Locator;
  readonly specificAgentsChip: Locator;

  readonly createBtn: Locator;
  readonly updateBtn: Locator;
  readonly formCancelBtn: Locator;

  readonly loadHistoryMenuItem: Locator;
  readonly deleteMenuItem: Locator;

  readonly deleteConfirmBtn: Locator;

  readonly infoBtn: Locator;
  readonly infoTooltip: Locator;

  readonly successCreated: Locator;
  readonly successUpdated: Locator;
  readonly successDeleted: Locator;

  constructor(page: Page) {
    this.page = page;

    this.bCortexBtn = page
      .getByTestId("nav-bcortex-btn")
      .or(page.getByRole("button", { name: "b-Cortex", exact: true }));

    // Top-level "Knowledge" tab (docs/ia-consolidation-plan.md, PR 2) — Knowledge
    // Base is one of its sub-tabs now, not reachable until this is selected.
    // exact:true is required: without it this also matches "Knowledge Base" and
    // "Knowledge Graph", both of which are also `role=tab` at that point.
    this.knowledgeGroupTab = page.getByRole("tab", { name: "Knowledge", exact: true });

    this.knowledgeBaseTab = page.locator("#tab-knowledge-base").or(page.getByRole("tab", { name: /Knowledge Base/i }));

    this.userSubTab = page
      .locator('#tab-manual')
      .or(page.getByRole("tab", { name: /User/i }));

    this.addKBBtn = page.locator("button").filter({ hasText: /Add Knowledge Base/ });

    this.nameInput = page
      .getByPlaceholder("e.g. aws_ec2_runbook")
      .or(page.locator('input[placeholder*="aws_ec2"]'));

    this.descriptionInput = page
      .getByPlaceholder("e.g. Steps to debug OOM errors in production K8s pods")
      .or(page.locator('textarea[placeholder*="Steps to debug"]'));

    // The content field is MentionContentInput now, not a bare ds/Input, and it
    // is the one control on this form carrying a data-testid — which ds/Input
    // puts straight on the <textarea>. Its placeholder changed with the
    // notes/sources redesign (enterprise#37607) from a one-liner to a template,
    // so the old placeholder match resolved to nothing.
    this.contentTextarea = page.getByTestId("kb-content-input");

    // The create/edit form's agent-picker chips. Kept only so the mapping case
    // can assert they are GONE: the picker was removed with the notes/sources
    // redesign (enterprise#37607) and a create now always maps to every agent
    // through the wildcard row. ds/Chip renders an interactive chip as a
    // ButtonBase, so these are role-based and resolve to nothing once the chips
    // are absent rather than matching some other control.
    this.allAgentsChip = page.getByRole("button", { name: "All agents", exact: true });
    this.specificAgentsChip = page.getByRole("button", { name: "Specific agents", exact: true });

    this.createBtn = page.getByRole("button", { name: "Create" }).last();
    this.updateBtn = page.getByRole("button", { name: "Update" }).last();
    this.formCancelBtn = page.getByRole("button", { name: "Cancel" }).last();

    // `:visible` is load-bearing: every row keeps its own MUI menu mounted, so a
    // bare [role="menuitem"] match resolves to the FIRST row's closed menu and
    // waits out its timeout on an item that is in the DOM but hidden. Only the
    // menu that was just opened is visible.
    this.loadHistoryMenuItem = page
      .locator('[role="menuitem"]:visible')
      .filter({ hasText: /^Load History$/ })
      .or(page.locator("li:visible, [role='option']:visible").filter({ hasText: /^Load History$/ }));

    this.deleteMenuItem = page
      .locator('[role="menuitem"]:visible')
      .filter({ hasText: /^Delete$/ })
      .or(page.locator("li:visible, [role='option']:visible").filter({ hasText: /^Delete$/ }));

    this.deleteConfirmBtn = page
      .locator('[role="dialog"]')
      .getByRole("button", { name: "Delete" })
      .or(page.locator('[role="dialog"] button').filter({ hasText: /^Delete$/ }));

    this.infoBtn = page
      .locator('[role="dialog"]')
      .getByText('Name *', { exact: true })
      .locator('xpath=..')
      .locator('svg, [data-testid="kb-form-name-info"]')
      .first();
    this.infoTooltip = page.locator('[role="tooltip"]').first();

    this.successCreated = page
      .getByText("Knowledge base created successfully")
      .or(page.locator("[class*='snackbar'], [class*='toast'], [role='alert']")
          .filter({ hasText: "created successfully" }));

    this.successUpdated = page
      .getByText("Knowledge base updated successfully")
      .or(page.locator("[class*='snackbar'], [class*='toast'], [role='alert']")
          .filter({ hasText: "updated successfully" }));

    this.successDeleted = page
      .getByText("Knowledge base deleted successfully")
      .or(page.locator("[class*='snackbar'], [class*='toast'], [role='alert']")
          .filter({ hasText: "deleted successfully" }));
  }

  async openBCortex(): Promise<void> {
    await this.waitNubiPageSettled();

    await this.bCortexBtn.first().waitFor({ state: "visible", timeout: 15000 });
    await this.bCortexBtn.first().click();
    await this.openKnowledgeGroup();

    if (await this.modalGenuinelyClosed()) {
      await this.bCortexBtn.first().click();
      await this.openKnowledgeGroup();
    }
  }

  // b-Cortex opens on its Memory tab by default; Knowledge Base is a sub-tab
  // under the separate top-level "Knowledge" tab, so it only exists in the DOM
  // once that tab is selected.
  private async openKnowledgeGroup(): Promise<void> {
    await this.knowledgeGroupTab.first().waitFor({ state: "visible", timeout: 15000 });
    await this.knowledgeGroupTab.first().click();
    await this.knowledgeBaseTab.first().waitFor({ state: "visible", timeout: 15000 });
  }

  private async waitNubiPageSettled(): Promise<void> {
    await this.page.waitForLoadState("networkidle").catch(() => {});
    await this.page
      .getByText("What can we Optimize?", { exact: false })
      .first()
      .waitFor({ state: "visible", timeout: 15000 })
      .catch(() => {});
  }

  private async modalGenuinelyClosed(): Promise<boolean> {
    await this.page.waitForTimeout(1000);
    if (await this.knowledgeBaseTab.first().isVisible().catch(() => false)) return false;
    await this.page.waitForTimeout(200);
    return !(await this.knowledgeBaseTab.first().isVisible().catch(() => false));
  }

  async navigateToKnowledgeBase(_nubiLocators?: NubiLocators): Promise<void> {
    await this.openBCortex();
    await this.knowledgeBaseTab.first().click();
    // The tab's own toolbar is the landed signal. Deliberately not
    // waitForLoadState("networkidle"): the Nubi surface behind this modal keeps
    // polling, so the network never goes idle and that wait simply burns its
    // timeout — which is how "Clicking Knowledge Base tab API validation"
    // failed while the tab underneath had rendered perfectly.
    await this.knowledgeBaseLanded();
  }

  // The Knowledge Base tab has rendered its listing: either the scope sub-tabs
  // or the Add button, whichever this tenant's data puts up first.
  async knowledgeBaseLanded(timeout = 30000): Promise<void> {
    await this.userSubTab.first().or(this.addKBBtn.first()).first().waitFor({ state: "visible", timeout });
  }

  // Dismisses the b-Cortex modal. The chat behind it is unreachable while the
  // modal is up — MUI's dialog container swallows every click, which is how the
  // ask-Nubi step timed out on the New chat button with the button itself
  // visible, enabled and stable. ds/Modal's close control carries an
  // accessible name, so no fallback is needed.
  async closeBCortex(): Promise<void> {
    const dialog = this.page.locator('[role="dialog"]').filter({ hasText: "b-Cortex" }).first();
    if (!(await dialog.isVisible().catch(() => false))) return;
    await dialog.getByRole("button", { name: "Close" }).first().click();
    await dialog.waitFor({ state: "hidden", timeout: 15000 });
  }

  async navigateToUserTab(): Promise<void> {
    const appeared = await this.userSubTab.first()
      .waitFor({ state: "visible", timeout: 8000 })
      .then(() => true)
      .catch(() => false);
    if (!appeared) return;

    await this.userSubTab.first().click();
    await this.addKBBtn.first().waitFor({ state: "visible", timeout: 5000 });
  }

  // Records the agent id of every MapKBToAgent mutation the form sends. The
  // create/edit form has no mapping control any more (enterprise#37607), so the
  // outgoing mutation is the only place the mapping is observable — hung off
  // "request" rather than "response" because the id being sent is the assertion,
  // not whether the backend accepted it (the snackbar already covers that).
  trackAgentMappings(): { ids: string[] } {
    const log: { ids: string[] } = { ids: [] };
    this.page.on("request", (req) => {
      if (req.method() !== "POST" || !req.url().includes("api/graphql")) return;
      const postData = req.postData();
      if (!postData || !postData.includes("MapKBToAgent")) return;
      try {
        const payload = JSON.parse(postData);
        for (const op of (Array.isArray(payload) ? payload : [payload]) as { variables?: { request?: { agent_id?: string } } }[]) {
          const agentId = op?.variables?.request?.agent_id;
          if (agentId) log.ids.push(agentId);
        }
      } catch {
        // A payload shape this parser cannot read is a real change worth
        // failing on, so nothing is recorded and the poll reports the miss.
      }
    });
    return log;
  }

  async openCreateModal(): Promise<void> {
    await this.addKBBtn.first().waitFor({ state: "visible", timeout: 10000 });
    await this.addKBBtn.first().click();
    await this.nameInput.waitFor({ state: "visible", timeout: 10000 });
  }

  async fillForm(name: string, content: string, description?: string): Promise<void> {
    await this.nameInput.fill(name);
    if (description) {
      await this.descriptionInput.fill(description);
    }
    await this.contentTextarea.fill(content);
  }

  // The knowledge base listed under `name`, as its own table ROW.
  //
  // The tab renders a table, not cards — these locators kept the old name from
  // before the notes-vs-sources redesign. Matching a `div` resolved to an
  // ancestor container of the whole listing, so a row action taken through it
  // ("More actions", "Edit knowledge base") hit whichever knowledge base
  // happened to be first in the table — which is how the delete case removed
  // the wrong one and then reported the intended one as still listed. Scoped to
  // `tbody tr` by element rather than getByRole: MUI aria-hides the b-Cortex
  // dialog whenever a nested overlay (the row menu, the delete confirm) is up,
  // and a role lookup would then resolve to nothing.
  getKBCardByName(name: string): Locator {
    return this.page
      .locator("tbody tr")
      .filter({ has: this.page.getByText(name, { exact: true }) })
      .first();
  }

  async clickEditForCard(name: string): Promise<void> {
    const card = this.getKBCardByName(name);
    // The row's pencil carries aria-label "Edit knowledge base"; the loose name
    // matches it, and the attribute fallback covers the aria-hidden case.
    const editBtn = card.getByRole("button", { name: "Edit" }).or(card.locator("button[aria-label*='Edit']"));

    await editBtn.first().click();
    await this.nameInput.waitFor({ state: "visible", timeout: 10000 });
  }

  async clickThreeDotsForCard(name: string): Promise<void> {
    const card = this.getKBCardByName(name);
    // The listing refetches on every scope switch and on the delete before this
    // one, so the card can be a render behind the caller. Waiting for it here
    // reports "the KB is not listed" instead of a bare click timeout on a menu
    // that never existed.
    await card.waitFor({ state: "visible", timeout: 20000 });
    // ds/ThreeDotsMenu gives its trigger the accessible name "More actions";
    // the id fallback stays for a row that renders the older trigger.
    const menu = card.getByRole("button", { name: "More actions" }).or(card.locator("#three-dot-menu"));

    await menu.first().click();
  }

  // Deletes every knowledge base listed under `name`, or nothing when none is.
  // Looped rather than a single delete: this suite's names are fixed, and a run
  // that aborted between its create and its delete leaves one behind — the next
  // run then creates a second under the same name, and a one-shot cleanup would
  // keep leaving one. Bounded so a listing that never loses the card reports the
  // failure instead of spinning.
  async removeAllKBsNamed(name: string, maxDeletes = 5): Promise<void> {
    for (let i = 0; i < maxDeletes; i++) {
      const present = await this.getKBCardByName(name)
        .isVisible()
        .catch(() => false);
      if (!present) return;
      await this.deleteKBByName(name);
      await this.getKBCardByName(name).waitFor({ state: "hidden", timeout: 20000 }).catch(() => {});
    }
  }

  async openDeleteForCard(name: string): Promise<void> {
    await this.clickThreeDotsForCard(name);
    await this.deleteMenuItem.first().waitFor({ state: "visible", timeout: 5000 });
    await this.deleteMenuItem.first().click();
    await this.deleteConfirmBtn.first().waitFor({ state: "visible", timeout: 5000 });
  }

  async deleteKBByName(name: string): Promise<void> {
    await this.navigateToUserTab();
    await this.openDeleteForCard(name);
    await this.deleteConfirmBtn.first().click();
    await expect(this.successDeleted.first()).toBeVisible({ timeout: 15000 });
  }
}
