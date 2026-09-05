// Not for OSS
import { Page, Locator, expect } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { NubiLocators } from "../nubiLocators";
import { ChatHistoryLocators } from "./chatHistoryLocators";

export interface ChatHistoryContext {
  nubi: NubiLocators;
  locators: ChatHistoryLocators;
}

// Every conversation this suite creates is asked under a generated marker so it
// can never collide with a real one, and so a leftover from a failed run cannot
// fail the next run's search-and-delete.
export function uniqueMarker(): string {
  return `nbe2ech${Date.now()}${Math.random().toString(36).slice(2, 6)}`;
}

export function markerQuestion(marker: string): string {
  return `Reply with OK only. End to end marker ${marker}`;
}

// Logs in and lands on the full /ask-nudgebee page through the header's own Nubi
// shortcut, which is what carries the accountId the rail's saved/waiting count
// queries need. The long-form placeholder is the page's own chat box — the
// floating panel renders a different one — so waiting on it also proves this is
// the ConversationListV2 surface and not the drawer.
export async function openAskNubiPage(page: Page): Promise<ChatHistoryContext> {
  const loginPage = new LoginPage(page);
  const nubi = new NubiLocators(page);
  const locators = new ChatHistoryLocators(page);

  await loginPage.doFullLogin();
  await locators.askNubiPageBtn.waitFor({ state: "visible", timeout: 30000 });
  await locators.askNubiPageBtn.click();
  await page.waitForURL(/\/ask-nudgebee/, { timeout: 60000 });
  await nubi.chatTextbox.waitFor({ state: "visible", timeout: 60000 });

  return { nubi, locators };
}

// The rail button is a toggle and the rail starts collapsed, so a single click
// opens it; the retry covers the click landing before the layout has wired the
// handler up, which leaves the rail closed with no other signal. The guard is
// what keeps it a toggle-safe "open": clicking an already-open rail would close it.
export async function openChatHistory(locators: ChatHistoryLocators): Promise<void> {
  if (await locators.searchInput.isVisible()) return;
  for (let attempt = 1; attempt <= 3; attempt++) {
    await locators.chatsRailBtn.click();
    const opened = await locators.searchInput
      .waitFor({ state: "visible", timeout: 10000 })
      .then(() => true)
      // A closed rail is the normal outcome of a click that did not register,
      // and the retry is the handling — this must come back false, not throw.
      .catch(() => false);
    if (opened) return;
    if (attempt === 3) throw new Error("Chat History rail did not open after 3 click attempts");
  }
}

// Resolves when the server has answered a conversation query carrying this exact
// search term. Only the list query carries it — the saved/waiting badge queries
// hardcode `searchText: ''` (ConversationListV2.jsx:457) — so this cannot settle
// on a count request and report the list as ready too early.
function conversationSearchResponse(page: Page, text: string, timeout: number): Promise<unknown> {
  return page.waitForResponse(
    (response) =>
      response.url().includes("/api/graphql") && response.status() === 200 && (response.request().postData() ?? "").includes(text),
    { timeout }
  );
}

// ConversationListV2 only re-queries on Enter while a search term is set, so the
// commit is a keypress rather than a debounce.
//
// Waiting on the response is not optional here: fetchConversations('on-enter')
// calls setRawConversations([]) synchronously before its request goes out, so a
// "no rows" assertion made right after the keypress reads a list the app just
// emptied and would pass even against a broken search.
//
// The keypress itself is retried, not just the wait: that same function drops an
// on-enter call outright while a request from the same epoch is still in flight,
// so a single Enter can issue no request at all.
export async function searchConversations(page: Page, locators: ChatHistoryLocators, text: string): Promise<void> {
  await locators.searchInput.click();
  await locators.searchInput.fill(text);
  await expect(locators.searchInput).toHaveValue(text, { timeout: 10000 });

  await expect(async () => {
    const response = conversationSearchResponse(page, text, 20000);
    await locators.searchInput.press("Enter");
    await response;
  }).toPass({ timeout: 90000, intervals: [1000, 2000, 3000] });
}

// Asks one question from the page's chat box. The textarea clearing is the app's
// own signal that the question was accepted and a conversation row now exists;
// the answer itself is irrelevant to this suite, so nothing here waits for it.
export async function askQuestion(nubi: NubiLocators, question: string): Promise<void> {
  await nubi.chatTextbox.click();
  await nubi.chatTextbox.fill(question);
  await expect(nubi.chatTextbox).toHaveValue(question, { timeout: 15000 });
  await nubi.submitBtn.click();
  await expect(nubi.chatTextbox).toHaveValue("", { timeout: 60000 });
}

// Re-runs the search until the freshly asked conversation is indexed and comes
// back. Each attempt is a real server round trip, not a sleep — the list has no
// other signal that a conversation created moments ago is now queryable.
export async function findOwnConversationRow(
  page: Page,
  locators: ChatHistoryLocators,
  marker: string,
  timeout = 120000
): Promise<Locator> {
  await expect(async () => {
    await searchConversations(page, locators, marker);
    await expect(locators.conversationRows).toHaveCount(1, { timeout: 10000 });
  }).toPass({ timeout, intervals: [2000, 3000, 5000] });

  return locators.conversationRows.first();
}

// A conversation cannot be deleted while the agent is still working on it: the
// backend refuses IN_PROGRESS and PENDING outright
// (api-server/services/feedback/service.go:182), which the rail draws as the
// "Running" and "Queued" status icons. Asking a question and deleting straight
// away therefore fails with an error toast and leaves the row in place.
//
// The re-search is what makes this observable at all: while a search term is
// set, ConversationListV2 reschedules no polling (fetchConversations only
// re-arms the timer for source === 'polling'), so a row's status never changes
// on its own and each attempt has to re-query.
export async function waitUntilConversationDeletable(
  page: Page,
  locators: ChatHistoryLocators,
  marker: string,
  timeout = 150000
): Promise<void> {
  await expect(async () => {
    await searchConversations(page, locators, marker);
    const row = locators.conversationRows.first();
    await expect(row).toBeVisible({ timeout: 10000 });
    await expect(row.locator('img[alt="Running"], img[alt="Queued"]')).toHaveCount(0, { timeout: 2000 });
  }).toPass({ timeout, intervals: [5000, 5000, 10000] });
}

// Opens one row's kebab. It only mounts while its own row is hovered, so the
// hover is part of the interaction rather than a convenience.
export async function openRowMenu(locators: ChatHistoryLocators, row: Locator): Promise<void> {
  await row.hover();
  const menuBtn = locators.rowMenuBtn(row);
  await menuBtn.waitFor({ state: "visible", timeout: 15000 });
  await menuBtn.click();
}

// Cleanup. Reloading first is what makes this safe to call from a finally block
// after a test failed part way through: a fresh mount resets the rail's scope and
// Saved/Waiting filters, so the search below is not silently narrowed by whatever
// state the test left behind.
export async function deleteConversationIfPresent(
  page: Page,
  nubi: NubiLocators,
  locators: ChatHistoryLocators,
  marker: string
): Promise<void> {
  await page.reload();
  await nubi.chatTextbox.waitFor({ state: "visible", timeout: 60000 });
  await openChatHistory(locators);
  await searchConversations(page, locators, marker);

  // The search above is answered before this returns, so the row is either
  // rendered or the server has no such conversation — this waits on rendering,
  // not on the request.
  const row = locators.conversationRows.first();
  const present = await row
    .waitFor({ state: "visible", timeout: 15000 })
    .then(() => true)
    // Nothing to clean up is the expected outcome whenever the test deleted the
    // conversation itself, so absence must come back as false.
    .catch(() => false);
  if (!present) return;

  // Deleting a still-running conversation is refused, so cleanup has to wait the
  // agent out exactly as the delete case does. Left to throw rather than swallowed:
  // a conversation this suite created and could not remove is litter on a shared
  // tenant, and the marker in the message is what makes it findable by hand.
  await waitUntilConversationDeletable(page, locators, marker);

  await openRowMenu(locators, locators.conversationRows.first());
  await locators.deleteMenuItem.click();
  await expect(locators.conversationRows).toHaveCount(0, { timeout: 30000 });
}
