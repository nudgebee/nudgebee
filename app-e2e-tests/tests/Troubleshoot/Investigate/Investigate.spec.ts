// Not for OSS
import { test, expect } from "@playwright/test";
import { InvestigateTabs, MoreActions } from "./investigateLocators";
import {
  INVESTIGATE_TIMEOUT_MS,
  NO_INVESTIGABLE_EVENT_HINT,
  UNKNOWN_EVENT_ID,
  dismissMenu,
  expectSelectedTab,
  openEventsListing,
  openFirstInvestigation,
  openInvestigateTab,
  openMoreActions,
  parseInvestigateParams,
  parseTaskCount,
  revealInvestigationCard,
} from "./investigateHelper";

// Troubleshoot > Investigate — the per-event investigation dashboard
// (app/src/pages/investigate.jsx), reached from an event's Investigate link.
//
// Everything here is read-only. Every write this module offers acts on a
// pre-existing event in a shared tenant and none of them can be undone from the
// app — Generate RCA and Run Automation start real jobs, Classify Event and
// Update Event rewrite the event, Create Ticket files a ticket in a live
// ticketing integration — so those controls are only ever opened and dismissed.
// See "Follow-ups" in the PR.
test.describe.configure({ timeout: INVESTIGATE_TIMEOUT_MS });

test(
  "Investigate sanity - open Troubleshoot Events, follow an event's Investigate link, verify the investigation loads with its details sidebar and Tasks tab",
  { tag: ["@dev", "@test", "@sanity", "@functional"] },
  async ({ page }) => {
    const investigate = await openFirstInvestigation(page);

    await test.step("The page settled on the investigation route", async () => {
      await expect(page).toHaveURL(/\/investigate\?/);
    });

    await test.step("The details sidebar rendered its expanded body", async () => {
      await expect(investigate.sidebarWhereLabel).toBeVisible();
      await expect(investigate.sidebarCollapseToggle).toBeVisible();
    });

    await test.step("The tab strip offers Tasks, which every event has", async () => {
      await expect(investigate.tabStrip).toBeVisible();
      await expect(investigate.tab(InvestigateTabs.tasks)).toBeVisible();
    });
  }
);

test(
  "Investigate - follow an event's Investigate link, verify the url carries the event id and account id the listing row linked to",
  { tag: ["@dev", "@test", "@sanity", "@functional"] },
  async ({ page }) => {
    const investigate = await openEventsListing(page);

    await expect(investigate.investigateLink, NO_INVESTIGABLE_EVENT_HINT).toBeVisible({ timeout: 60000 });
    const href = (await investigate.investigateLink.getAttribute("href")) ?? "";
    const fromRow = parseInvestigateParams(href);

    // The listing composes this href from the row's own event and account, so a
    // blank half would make the comparison below pass against nothing.
    expect(fromRow.id, "The Investigate link carried no event id").not.toBe("");
    expect(fromRow.accountId, "The Investigate link carried no account id").not.toBe("");

    await investigate.investigateLink.click();
    await page.waitForURL(/\/investigate\?/, { timeout: 60000 });

    const landed = parseInvestigateParams(page.url());
    expect(landed.id).toBe(fromRow.id);
    expect(landed.accountId).toBe(fromRow.accountId);
  }
);

test(
  "Investigate - open an investigation, select the Tasks tab, verify its panel becomes the visible one and the tab reports a task count",
  { tag: ["@dev", "@test", "@regression", "@functional"] },
  async ({ page }) => {
    const investigate = await openFirstInvestigation(page);

    await openInvestigateTab(investigate, InvestigateTabs.tasks);

    await test.step("Tasks owns the open panel", async () => {
      await expect(investigate.tabPanel(InvestigateTabs.tasks)).toBeVisible();
      // TabPanel keeps every panel in the dom and marks the closed ones hidden,
      // so the other two prove the switch rather than merely being absent.
      await expect(investigate.tabPanel(InvestigateTabs.analysis)).toBeHidden();
      await expect(investigate.tabPanel(InvestigateTabs.rca)).toBeHidden();
    });

    await test.step("The tab label carries the number of task cards behind it", async () => {
      const label = (await investigate.tab(InvestigateTabs.tasks).textContent()) ?? "";
      const count = parseTaskCount(label);
      expect(count, `Tasks tab label "${label}" carried no count`).not.toBeNull();
      expect(count).toBeGreaterThanOrEqual(0);
    });
  }
);

test(
  "Investigate - open an investigation, verify Investigation Analysis and RCA Report are offered only when the event produced them",
  { tag: ["@dev", "@test", "@regression", "@functional"] },
  async ({ page }) => {
    const investigate = await openFirstInvestigation(page);

    // Tasks is the only unconditional tab — the other two mount off a matched
    // AskAiCard / RCACard, so this asserts the pairing rather than their presence.
    await expect(investigate.tab(InvestigateTabs.tasks)).toBeVisible();

    for (const tab of [InvestigateTabs.analysis, InvestigateTabs.rca]) {
      // A waiting probe rather than count(): these two mount only once card
      // generation finishes, which lands after the Tasks tab this test already
      // waited on. count() would read 0 while they were still coming and take
      // the else-branch, turning a skipped assertion into a silent pass.
      const offered = await investigate
        .tab(tab)
        .waitFor({ state: "visible", timeout: 10000 })
        .then(() => true)
        // Absence is the expected outcome for an event that produced no such card.
        .catch(() => false);

      if (offered) {
        await openInvestigateTab(investigate, tab);
        await expect(investigate.tabPanel(tab)).toBeVisible();
        await expect(investigate.tabPanel(InvestigateTabs.tasks)).toBeHidden();
        await openInvestigateTab(investigate, InvestigateTabs.tasks);
      } else {
        // The tab is absent, so its panel must be too — a rendered panel with no
        // tab would be content the user has no way to reach.
        await expect(investigate.tabPanel(tab)).toBeHidden();
      }
    }
  }
);

test(
  "Investigate - open an investigation, collapse the details sidebar, expand it again, verify the Where row comes back",
  { tag: ["@dev", "@test", "@regression", "@functional"] },
  async ({ page }) => {
    const investigate = await openFirstInvestigation(page);

    await expect(investigate.sidebarWhereLabel).toBeVisible();

    await test.step("Collapsing swaps the sidebar body for the expand rail", async () => {
      await investigate.sidebarCollapseToggle.click();
      await expect(investigate.sidebarExpandToggle).toBeVisible();
      await expect(investigate.sidebarWhereLabel).toBeHidden();
    });

    await test.step("Expanding restores the details it was hiding", async () => {
      await investigate.sidebarExpandToggle.click();
      await expect(investigate.sidebarWhereLabel).toBeVisible();
      await expect(investigate.sidebarCollapseToggle).toBeVisible();
    });
  }
);

test(
  "Investigate - open an investigation, open the More actions menu, verify every entry it offers is one of the module's own actions",
  { tag: ["@dev", "@test", "@regression", "@functional"] },
  async ({ page }) => {
    const investigate = await openFirstInvestigation(page);

    await expect(investigate.moreActionsBtn).toBeVisible();
    await investigate.moreActionsBtn.click();
    await expect(investigate.openMenu).toBeVisible();

    const entries = await investigate.openMenu.locator('[role="menuitem"]').allTextContents();
    expect(entries.length, "The More actions menu opened with no entries").toBeGreaterThan(0);

    const known: string[] = Object.values(MoreActions);
    for (const entry of entries) {
      const label = entry.trim();
      expect(known, `More actions offered an unknown entry: "${label}"`).toContain(label);
    }

    await dismissMenu(page);
    await expect(investigate.openMenu).toBeHidden();
  }
);

test(
  "Investigate - open the More actions menu, choose Event Trend, verify the Event Trend Chart dialog opens and closes again",
  { tag: ["@dev", "@test", "@regression", "@functional"] },
  async ({ page }) => {
    const investigate = await openFirstInvestigation(page);

    const offered = await openMoreActions(investigate, MoreActions.eventTrend);

    if (!offered) {
      // investigate.jsx gates Event Trend and Knowledge Base on the same isK8s
      // flag, so a cloud-sourced event must be missing both. Asserting the pair
      // keeps this branch a real check rather than a silent pass.
      await expect(investigate.menuItem(MoreActions.knowledgeBase)).toHaveCount(0);
      await dismissMenu(page);
      await expect(investigate.openMenu).toBeHidden();
      return;
    }

    await investigate.menuItem(MoreActions.eventTrend).click();

    const dialog = investigate.dialog("Event Trend Chart");
    await expect(dialog).toBeVisible({ timeout: 60000 });

    await investigate.dialogCloseBtn("Event Trend Chart").click();
    await expect(dialog).toBeHidden({ timeout: 30000 });
  }
);

test(
  "Investigate - open the More actions menu, choose Create Ticket, close the form without submitting, verify no ticket becomes linked to the event",
  { tag: ["@dev", "@test", "@regression", "@negative"] },
  async ({ page }) => {
    const investigate = await openFirstInvestigation(page);

    const offered = await openMoreActions(investigate, MoreActions.createTicket);

    if (!offered) {
      // The only thing that removes this entry is an already-linked ticket, so
      // the chip for that ticket has to be on screen. Asserting it keeps this
      // branch a real check on the same gating rule.
      await dismissMenu(page);
      await expect(page.getByTestId("linked-ticket-btn")).toBeVisible();
      return;
    }

    await investigate.menuItem(MoreActions.createTicket).click();

    const dialog = investigate.dialog("Create Ticket");
    await expect(dialog).toBeVisible({ timeout: 60000 });

    await investigate.dialogCloseBtn("Create Ticket").click();
    await expect(dialog).toBeHidden({ timeout: 30000 });

    // The linked-ticket chip is what the page renders once an event owns a
    // ticket, so its continued absence is the evidence that closing the form
    // filed nothing.
    await expect(page.getByTestId("linked-ticket-btn")).toHaveCount(0);
  }
);

test(
  "Investigate sanity - open the page with an event id that matches no event, verify no investigation is rendered for it",
  { tag: ["@dev", "@test", "@regression", "@negative"] },
  async ({ page }) => {
    const investigate = await openEventsListing(page);

    await expect(investigate.investigateLink, NO_INVESTIGABLE_EVENT_HINT).toBeVisible({ timeout: 60000 });
    const href = (await investigate.investigateLink.getAttribute("href")) ?? "";
    // A real account id, so the page fails on the event alone rather than on an
    // account it was never allowed to read.
    const { accountId } = parseInvestigateParams(href);
    expect(accountId, "The Investigate link carried no account id").not.toBe("");

    await page.goto(`/investigate?id=${UNKNOWN_EVENT_ID}&accountId=${accountId}`);
    await page.waitForURL(/\/investigate\?/, { timeout: 60000 });

    // The subject row is populated from the event record, so an id that resolves
    // to nothing must leave the sidebar with no subject to name.
    await expect(investigate.podNameLink).toHaveText(/^\s*-?\s*$/, { timeout: 60000 });
    await expect(investigate.followUpBtn).toHaveCount(0);
  }
);

test(
  "Investigate - open an investigation, navigate back, verify the Troubleshoot Events listing is restored",
  { tag: ["@dev", "@test", "@smoke", "@functional"] },
  async ({ page }) => {
    const investigate = await openFirstInvestigation(page);
    await expect(page).toHaveURL(/\/investigate\?/);

    await page.goBack();
    await page.waitForURL(/\/troubleshoot/, { timeout: 60000 });

    await test.step("The events listing and its Investigate links are back", async () => {
      await expect(investigate.eventsTable).toBeVisible({ timeout: 60000 });
      await expect(investigate.investigateLink).toBeVisible({ timeout: 60000 });
    });

    await test.step("Following the link a second time lands on an investigation again", async () => {
      await investigate.investigateLink.click();
      await page.waitForURL(/\/investigate\?/, { timeout: 60000 });
      await revealInvestigationCard(investigate);
      // Which tab opens depends on whether the event produced an AskAiCard, so
      // the reachable claim is that Tasks is offered — then select it and let
      // the app confirm the selection.
      await openInvestigateTab(investigate, InvestigateTabs.tasks);
      await expectSelectedTab(investigate, InvestigateTabs.tasks);
    });
  }
);
