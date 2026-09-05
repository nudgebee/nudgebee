// Not for OSS
import { test, expect } from "@playwright/test";
import {
  askQuestion,
  deleteConversationIfPresent,
  findOwnConversationRow,
  markerQuestion,
  openAskNubiPage,
  openChatHistory,
  openRowMenu,
  searchConversations,
  uniqueMarker,
  waitUntilConversationDeletable,
} from "./chatHistoryHelper";

// Nubi > Ask Nubi (full page) > Chat History rail
// (app/src/components/llm/ConversationListV2.jsx). Conversations are shared
// tenant data, so every case that writes asks its own question under a generated
// marker and deletes it again; nothing here touches a conversation it did not
// create.

test.describe("Nubi Chat History Rail", () => {
  test(
    "Chat History sanity - open Ask Nubi, open the Chat History rail from the left rail, verify the search box, the Mine and Everyone scope buttons, the type filter and the Saved and Waiting chips render",
    { tag: ["@dev", "@sanity", "@functional"] },
    async ({ page }) => {
      test.setTimeout(180000);
      const { locators } = await openAskNubiPage(page);

      // The rail starts collapsed, so none of these exist on screen until the
      // click below — this is a state change, not a re-read of what was already up.
      await openChatHistory(locators);

      await expect(locators.panelTitle).toBeVisible({ timeout: 30000 });
      await expect(locators.searchInput).toBeVisible({ timeout: 15000 });
      await expect(locators.mineScopeBtn).toBeVisible({ timeout: 15000 });
      await expect(locators.everyoneScopeBtn).toBeVisible({ timeout: 15000 });
      await expect(locators.typeMenuTrigger).toBeVisible({ timeout: 15000 });
      await expect(locators.savedChip).toBeVisible({ timeout: 15000 });
      await expect(locators.waitingChip).toBeVisible({ timeout: 15000 });
      await expect(locators.collapseBtn).toBeVisible({ timeout: 15000 });

      // "Type: All" is the unfiltered default the filter case below moves off.
      await expect(locators.typeMenuTrigger).toContainText("Type: All");
    }
  );

  test(
    "Chat History sanity - open the Chat History rail, verify past conversations are listed under a dated group heading",
    { tag: ["@dev", "@sanity", "@functional"] },
    async ({ page }) => {
      test.setTimeout(180000);
      const { locators } = await openAskNubiPage(page);
      await openChatHistory(locators);

      // The tenant this suite runs against always carries conversation history,
      // so an empty rail here is a real failure rather than a fresh tenant.
      await expect(locators.conversationRows.first()).toBeVisible({ timeout: 60000 });
      await expect(locators.dateGroupHeadings.first()).toBeVisible({ timeout: 30000 });

      // Grouping is the point: every row sits under one of the five buckets, so
      // there can never be more headings than rows.
      const headingCount = await locators.dateGroupHeadings.count();
      const rowCount = await locators.conversationRows.count();
      expect(headingCount).toBeGreaterThan(0);
      expect(headingCount).toBeLessThanOrEqual(rowCount);
    }
  );

  test(
    "Chat History - ask a new question, search the rail for its marker, open the found conversation, verify only that conversation is listed and the chat reopens on its session",
    { tag: ["@dev", "@regression", "@search", "@crud"] },
    async ({ page }) => {
      test.setTimeout(480000);
      const { nubi, locators } = await openAskNubiPage(page);
      const marker = uniqueMarker();

      try {
        await askQuestion(nubi, markerQuestion(marker));
        await openChatHistory(locators);

        const row = await findOwnConversationRow(page, locators, marker);
        await expect(locators.conversationRows).toHaveCount(1);

        // The Delete item only renders for the signed-in owner of a conversation,
        // so its presence is what proves the search returned our row rather than
        // somebody else's.
        await openRowMenu(locators, row);
        await expect(locators.deleteMenuItem).toBeVisible({ timeout: 15000 });
        await page.keyboard.press("Escape");
        await expect(locators.deleteMenuItem).toBeHidden({ timeout: 15000 });

        await locators.conversationRows.first().click();
        await expect(page).toHaveURL(/session_id=/, { timeout: 60000 });
        await expect(page.getByText(marker).first()).toBeVisible({ timeout: 60000 });
      } finally {
        await deleteConversationIfPresent(page, nubi, locators, marker);
      }
    }
  );

  test(
    "Chat History - search the rail for a string no conversation contains, verify the list empties, then clear the search and verify the conversations come back",
    { tag: ["@dev", "@regression", "@negative", "@search"] },
    async ({ page }) => {
      test.setTimeout(180000);
      const { locators } = await openAskNubiPage(page);
      await openChatHistory(locators);

      await expect(locators.conversationRows.first()).toBeVisible({ timeout: 60000 });

      await searchConversations(page, locators, `no-such-conversation-${uniqueMarker()}`);
      await expect(locators.conversationRows).toHaveCount(0, { timeout: 60000 });

      // Clearing re-runs the unfiltered query, so the rows returning is the proof
      // the empty list was the search result and not a broken fetch.
      await locators.clearSearchBtn.click();
      await expect(locators.searchInput).toHaveValue("", { timeout: 15000 });
      await expect(locators.conversationRows.first()).toBeVisible({ timeout: 60000 });
    }
  );

  test(
    "Chat History - ask a new question, save it from the row menu, filter by Saved and verify it is listed, then unsave it and verify it drops out of the Saved list",
    { tag: ["@dev", "@regression", "@crud", "@snackbar"] },
    async ({ page }) => {
      test.setTimeout(480000);
      const { nubi, locators } = await openAskNubiPage(page);
      const marker = uniqueMarker();

      try {
        await askQuestion(nubi, markerQuestion(marker));
        await openChatHistory(locators);

        const row = await findOwnConversationRow(page, locators, marker);
        await openRowMenu(locators, row);
        await locators.toggleSaveMenuItem.click();
        await expect(locators.savedSnackbar).toBeVisible({ timeout: 30000 });

        // The Saved chip re-queries the server with activeFilter=Saved while
        // keeping the marker in the search box, so a hit here is the saved row
        // coming back from the backend rather than optimistic local state.
        await locators.savedChip.click();
        await expect(locators.conversationRows).toHaveCount(1, { timeout: 60000 });

        await openRowMenu(locators, locators.conversationRows.first());
        await locators.toggleSaveMenuItem.click();
        await expect(locators.unsavedSnackbar).toBeVisible({ timeout: 30000 });

        // Re-running the Saved query is what separates a real unsave from the
        // optimistic removal the hook performs before its response lands.
        await searchConversations(page, locators, marker);
        await expect(locators.conversationRows).toHaveCount(0, { timeout: 60000 });
      } finally {
        await deleteConversationIfPresent(page, nubi, locators, marker);
      }
    }
  );

  test(
    "Chat History - ask a new question, wait for it to stop running, delete it from the row menu, verify the delete snackbar and that the conversation stays gone when the rail is searched again",
    { tag: ["@dev", "@regression", "@crud", "@snackbar"] },
    async ({ page }) => {
      test.setTimeout(480000);
      const { nubi, locators } = await openAskNubiPage(page);
      const marker = uniqueMarker();

      try {
        await askQuestion(nubi, markerQuestion(marker));
        await openChatHistory(locators);

        await findOwnConversationRow(page, locators, marker);
        // Deleting is refused while the agent is still working, so the wait is
        // part of the journey a user takes, not a workaround.
        await waitUntilConversationDeletable(page, locators, marker);

        await openRowMenu(locators, locators.conversationRows.first());
        await locators.deleteMenuItem.click();

        await expect(locators.deletedSnackbar).toBeVisible({ timeout: 30000 });
        await expect(locators.conversationRows).toHaveCount(0, { timeout: 30000 });

        // A fresh query against the marker is the persistence check — the row
        // vanishing above is only local state being filtered.
        await searchConversations(page, locators, marker);
        await expect(locators.conversationRows).toHaveCount(0, { timeout: 60000 });
      } finally {
        await deleteConversationIfPresent(page, nubi, locators, marker);
      }
    }
  );

  test(
    "Chat History - open the type filter and pick Chats, verify the trigger reads Chats and every listed conversation carries a chat source badge",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      test.setTimeout(240000);
      const { locators } = await openAskNubiPage(page);
      await openChatHistory(locators);

      await expect(locators.conversationRows.first()).toBeVisible({ timeout: 60000 });

      await locators.typeMenuTrigger.click();
      await locators.typeMenuItem("Chats").click();
      await expect(locators.typeMenuTrigger).toContainText("Chats", { timeout: 30000 });

      // Read the rows only once the refetch has replaced them: the list is
      // cleared and refilled by the filter change, so a single read could catch
      // either the pre-clear set or an empty one mid-flight.
      await expect(async () => {
        const rowTexts = await locators.conversationRows.allTextContents();
        expect(rowTexts.length).toBeGreaterThan(0);
        for (const text of rowTexts) {
          expect(text).toMatch(/User Chat|Slack Channel/);
        }
      }).toPass({ timeout: 90000, intervals: [2000, 3000, 5000] });

      await locators.typeMenuTrigger.click();
      await locators.typeMenuItem("All types").click();
      await expect(locators.typeMenuTrigger).toContainText("Type: All", { timeout: 30000 });
    }
  );

  test(
    "Chat History - collapse the rail with Collapse Recent, verify the conversation list is hidden and the rail button brings it back",
    { tag: ["@dev", "@sanity", "@functional"] },
    async ({ page }) => {
      test.setTimeout(180000);
      const { locators } = await openAskNubiPage(page);
      await openChatHistory(locators);

      await expect(locators.conversationRows.first()).toBeVisible({ timeout: 60000 });

      await locators.collapseBtn.click();
      await expect(locators.searchInput).toBeHidden({ timeout: 30000 });
      await expect(locators.conversationRows.first()).toBeHidden({ timeout: 30000 });

      await openChatHistory(locators);
      await expect(locators.searchInput).toBeVisible({ timeout: 30000 });
      await expect(locators.conversationRows.first()).toBeVisible({ timeout: 60000 });
    }
  );
});
