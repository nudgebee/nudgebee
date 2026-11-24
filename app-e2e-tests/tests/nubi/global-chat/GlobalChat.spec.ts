import { test, expect } from "@playwright/test";
import { GlobalChatLocators } from "./globalChatLocators";
import {
  DOCKED_WIDTH_PX,
  clearPersistedPanelWidth,
  dragPanelWidthTo,
  expandedWidth,
  expectPanelWidth,
  landOnApp,
  openGlobalChat,
  pickOtherAccount,
} from "./globalChatHelper";
import { readGlobalClusterValue, clickSidenavRail } from "../../utils/helpers";

// Nubi > global chat — the floating launcher and the docked panel _app.tsx mounts on every
// page (app/src/components/llm/NubiGlobalChat.tsx, app/src/context/NubiGlobalChatContext.tsx).
//
// Nothing here asks Nubi a question. The panel embeds the same generator the full /ask-nudgebee
// page does, and that conversation surface is already covered by tests/nubi/chat-history and
// tests/nubi/askNudgebee.spec.ts — so these cases stay on what only the panel has: the
// launcher, the header controls, the chat-local account chip, the drawer's width, and the two
// routes the launcher deliberately withdraws from. That leaves no conversation, no account
// setting and no server record created, so every case is order-free and safe to run twice.
//
// The one exception is the resize case, which commits a width to localStorage. It clears that
// key before it starts and again in a finally, so a run that dies mid-drag cannot hand the next
// run a pre-widened panel.
test.describe("Nubi Global Chat Panel", () => {
  test(
    "Nubi Global Chat sanity - land on the app, verify the floating launcher renders and the chat panel stays unmounted until it is clicked",
    { tag: ["@dev", "@test", "@sanity", "@functional", "@oss"] },
    async ({ page }) => {
      test.setTimeout(180000);
      const { locators } = await landOnApp(page);

      await expect(locators.launcher).toBeVisible({ timeout: 30000 });
      // The drawer is a MUI temporary Drawer, so a closed panel is genuinely absent from the
      // DOM rather than hidden — which is what makes a count assertion meaningful here.
      await expect(locators.panel).toHaveCount(0);
    }
  );

  test(
    "Nubi Global Chat - click the floating launcher, verify the panel opens with its account chip, composer and the New chat, Your Chats, Expand and Close controls, and that the launcher withdraws while it is open",
    { tag: ["@dev", "@test", "@smoke", "@functional", "@oss"] },
    async ({ page }) => {
      test.setTimeout(180000);
      const { locators } = await openGlobalChat(page);

      // The chip only mounts once the accounts list has resolved, which NubiGlobalChat defers
      // to the first open rather than paying for it on every page load.
      await expect(locators.accountChip).toBeVisible({ timeout: 40000 });
      await expect(locators.composer).toBeVisible({ timeout: 40000 });
      await expect(locators.newChatBtn).toBeVisible();
      await expect(locators.historyBtn).toBeVisible();
      await expect(locators.expandToggle).toBeVisible();
      await expect(locators.closeBtn).toBeVisible();
      await expect(locators.openDetailBtn).toBeVisible();

      // The launcher is rendered only while the chat is closed, so an open panel and a visible
      // launcher would mean two ways into the same chat on screen at once.
      await expect(locators.launcher).toHaveCount(0);
    }
  );

  test(
    "Nubi Global Chat - open the panel from the launcher, press Close, verify the panel unmounts and the floating launcher comes back",
    { tag: ["@dev", "@test", "@smoke", "@functional", "@oss"] },
    async ({ page }) => {
      test.setTimeout(180000);
      const { locators } = await openGlobalChat(page);
      await expect(locators.closeBtn).toBeVisible({ timeout: 30000 });

      await locators.closeBtn.click();

      await expect(locators.panel).toHaveCount(0);
      await expect(locators.launcher).toBeVisible({ timeout: 30000 });
    }
  );

  test(
    "Nubi Global Chat - press the Ctrl/Cmd+J shortcut, verify the panel opens without the launcher being clicked, press it again and verify the panel closes and the launcher returns",
    { tag: ["@dev", "@test", "@regression", "@functional", "@oss"] },
    async ({ page }) => {
      test.setTimeout(180000);
      const { locators } = await landOnApp(page);

      // The handler is bound to window and fires whatever has focus, so this does not need the
      // launcher — which is the point: it is the one entry into the chat that produces no click.
      await page.keyboard.press("ControlOrMeta+j");
      await expect(locators.panel).toBeVisible({ timeout: 40000 });
      await expect(locators.launcher).toHaveCount(0);

      await page.keyboard.press("ControlOrMeta+j");
      await expect(locators.panel).toHaveCount(0);
      await expect(locators.launcher).toBeVisible({ timeout: 30000 });
    }
  );

  test(
    "Nubi Global Chat - open the panel, click Expand, verify the control turns into Collapse and the panel widens to half the viewport, then collapse it and verify the docked width returns",
    { tag: ["@dev", "@test", "@regression", "@functional", "@oss"] },
    async ({ page }) => {
      test.setTimeout(180000);
      const { locators } = await openGlobalChat(page);
      await expect(locators.expandToggle).toHaveAttribute("aria-label", "Expand", { timeout: 30000 });
      await expectPanelWidth(locators, DOCKED_WIDTH_PX, "docked panel");

      await locators.expandToggle.click();

      await expect(locators.expandToggle).toHaveAttribute("aria-label", "Collapse");
      await expectPanelWidth(locators, await expandedWidth(page), "expanded panel");

      await locators.expandToggle.click();

      await expect(locators.expandToggle).toHaveAttribute("aria-label", "Expand");
      await expectPanelWidth(locators, DOCKED_WIDTH_PX, "re-docked panel");
    }
  );

  test(
    "Nubi Global Chat - open the panel, click Your Chats, verify the Your Chats list opens over the panel, then click back into the composer and verify the list closes",
    { tag: ["@dev", "@test", "@regression", "@functional", "@oss"] },
    async ({ page }) => {
      test.setTimeout(180000);
      const { locators } = await openGlobalChat(page);
      await expect(locators.composer).toBeVisible({ timeout: 40000 });
      await expect(locators.yourChatsHeading).toHaveCount(0);

      await locators.historyBtn.click();

      await expect(locators.yourChatsHeading).toBeVisible({ timeout: 30000 });

      // The list is wrapped in a ClickAwayListener, so any click outside it dismisses it — the
      // composer is the one neutral target inside the panel that starts nothing when clicked.
      await locators.composer.click();

      await expect(locators.yourChatsHeading).toHaveCount(0);
      await expect(locators.composer).toBeVisible();
    }
  );

  test(
    "Nubi Global Chat - open the panel and click the Nubi title, verify a second tab opens on the full Ask Nubi page carrying the panel's account id, and that the full-page composer renders there",
    { tag: ["@dev", "@test", "@regression", "@functional", "@oss"] },
    async ({ page }) => {
      test.setTimeout(180000);
      const { locators } = await openGlobalChat(page);
      // Waited on before the click: the account id is only put on the query string once the
      // chat has resolved an account, and the chip is that resolution becoming observable.
      await expect(locators.accountChip).toBeVisible({ timeout: 40000 });

      const [fullPage] = await Promise.all([page.waitForEvent("popup"), locators.openDetailBtn.click()]);

      await expect(fullPage).toHaveURL(/\/ask-nudgebee\?.*accountId=/, { timeout: 60000 });
      const fullPageLocators = new GlobalChatLocators(fullPage);
      await expect(fullPageLocators.composer).toBeVisible({ timeout: 60000 });
      // The dedicated page IS this chat at full size, so it must not stack a second launcher
      // or a docked panel on top of itself.
      await expect(fullPageLocators.launcher).toHaveCount(0);

      await fullPage.close();
    }
  );

  test(
    "Nubi Global Chat - open the panel, verify the account chip reads the account the session is on, expand the chip picker's provider groups and pick a second account, verify the chip commits to it while the page's own cluster dropdown is left untouched",
    { tag: ["@dev", "@test", "@regression", "@functional", "@oss"] },
    async ({ page }) => {
      test.setTimeout(240000);
      const { locators, cluster } = await openGlobalChat(page);
      if (!cluster) {
        throw new Error("The login did not commit a cluster, so there is no page account for the chip to be compared against");
      }
      await expect(locators.accountChipTrigger).toContainText(cluster, { timeout: 40000 });

      await locators.accountChipTrigger.click();
      const other = await pickOtherAccount(locators, cluster);
      await locators.accountOptionAt(other.index).click();

      // Read back rather than trusting the click: the chip committing to the wrong account is
      // exactly the failure that would send every later question to the wrong cluster's data.
      await expect(locators.accountPopover).toHaveCount(0, { timeout: 20000 });
      await expect(locators.accountChipTrigger).toContainText(other.label, { timeout: 30000 });

      // setChatAccount is chat-local by design — it must not touch selectedCluster or the URL,
      // or picking an account to ask about would silently re-scope the page behind the chat.
      expect(await readGlobalClusterValue(page, 20000, cluster)).toBe(cluster);
    }
  );

  test(
    "Nubi Global Chat - drag the panel's resize handle wider, verify the new width takes, reload and reopen the panel, verify the width was persisted, then clear it and verify the docked width comes back",
    { tag: ["@dev", "@test", "@regression", "@functional", "@oss"] },
    async ({ page }) => {
      test.setTimeout(240000);
      const widened = 640;
      const { locators } = await landOnApp(page);

      // Cleared before the panel is ever opened: a run that died mid-drag would otherwise hand
      // this one a pre-widened panel and the docked-width assertion below would fail on
      // yesterday's leftover rather than on anything this run did.
      await clearPersistedPanelWidth(page);
      await page.reload();
      await openGlobalChat(page);
      await expectPanelWidth(locators, DOCKED_WIDTH_PX, "docked panel");

      try {
        await dragPanelWidthTo(page, locators, widened);
        await expectPanelWidth(locators, widened, "dragged panel");

        await page.reload();
        await openGlobalChat(page);

        // The real assertion: the width survived a full page load, so it was written to and
        // read back from storage rather than being held in React state.
        await expectPanelWidth(locators, widened, "reopened panel");
      } finally {
        await clearPersistedPanelWidth(page);
      }

      await page.reload();
      await openGlobalChat(page);
      await expectPanelWidth(locators, DOCKED_WIDTH_PX, "restored panel");
    }
  );

  test(
    "Nubi Global Chat sanity - open the full Ask Nubi page, verify neither the floating launcher nor the docked panel is mounted on the route that already is the chat",
    { tag: ["@dev", "@test", "@sanity", "@negative", "@oss"] },
    async ({ page }) => {
      test.setTimeout(180000);
      const { locators } = await landOnApp(page);
      await expect(locators.launcher).toBeVisible({ timeout: 30000 });

      await page.goto("/ask-nudgebee");

      // Asserted against the composer rather than the URL alone, so an /ask-nudgebee that
      // redirected or failed to render cannot pass this as "the launcher is correctly absent".
      await expect(locators.composer).toBeVisible({ timeout: 60000 });
      await expect(locators.launcher).toHaveCount(0);
      await expect(locators.panel).toHaveCount(0);
    }
  );

  test(
    "Nubi Global Chat - open the panel, navigate to Optimize from the side rail with it open, verify the panel stays open across the route change and its controls are still live",
    { tag: ["@dev", "@test", "@regression", "@functional", "@oss"] },
    async ({ page }) => {
      test.setTimeout(180000);
      const { locators } = await openGlobalChat(page);
      await expect(locators.composer).toBeVisible({ timeout: 40000 });

      // The drawer is non-modal precisely so the page behind it stays usable — the side rail
      // being clickable with the chat open is that contract.
      await clickSidenavRail(locators.OptimizeBtn);
      await page.waitForURL(/optimise/, { timeout: 60000 });

      await expect(locators.panel).toBeVisible({ timeout: 30000 });
      await expect(locators.launcher).toHaveCount(0);
      await expect(locators.closeBtn).toBeVisible();
    }
  );
});
