// Not for OSS
import { Page, Response, expect } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { NubiLocators } from "../nubiLocators";
import { CollectiveLocators } from "./collectiveLocators";

// app/src/api1/memory/index.ts wraps every memory call as Memory_<action>, and both the
// unfiltered browse (collectiveList) and the search (collectiveSearch) go out as the SAME
// action — ai_memory_list. They are told apart on the wire by `request.q`, which only the
// search sets, and that is exactly the split the server dispatches on too
// (memV2DispatchList in llm/llm-server/api/memory_v2.go: a non-empty q routes to
// memV2CollectiveSearch, anything else to memV2CollectiveList).
export const OP_MEMORY_LIST = "Memory_ai_memory_list";

// Other b-Cortex tabs read through ai_memory_list as well (patterns, decisions, prefs),
// so every read below is filtered on this layer too — without it, landing on the Memory
// group would log PatternsTab's own load as a Collective browse.
export const COLLECTIVE_LAYER = "collective";

// This suite never deletes an insight. Collective rows are tenant-wide shared memory
// written by the agents from real conversations, they are injected into future prompts for
// everyone in the tenant, and the tab has no create path to put one back — so a delete
// here would permanently remove another team's fixture from a shared dev environment. The
// two delete cases below open the confirmation and assert what it says, then Cancel.

// The `q` of every collective read that COMPLETES, in completion order. An unfiltered
// browse logs "" — the request simply carries no q — so a case can assert which of the two
// paths the tab took without depending on response bodies.
export interface CollectiveReadLog {
  reads: string[];
}

// Bound to 'response' rather than 'request' on purpose, same reasoning digestsHelper.ts
// records: a read that is issued and then fails at the network layer would still satisfy a
// request-based log, so a case could assert it searched and only discover the read never
// landed at the next wait, with a worse message. Logging on completion makes this a real
// "the data landed" barrier.
//
// Only one operation per request: queryGraphQL posts a single { query, variables,
// operationName } object, so there is no batched array-of-operations body shape to unwrap.
export function trackCollectiveReads(page: Page): CollectiveReadLog {
  const log: CollectiveReadLog = { reads: [] };

  page.on("response", (response: Response) => {
    const request = response.request();
    if (!request.url().includes("/api/graphql")) return;
    // A probe, not an assertion: most traffic on this page is other operations with other
    // body shapes, and "this is not a memory read" is the normal answer.
    let body: { operationName?: string; variables?: { request?: { layer?: string; q?: string } } } | null = null;
    try {
      body = request.postDataJSON();
    } catch {
      return;
    }
    if (body?.operationName !== OP_MEMORY_LIST) return;
    if (body?.variables?.request?.layer !== COLLECTIVE_LAYER) return;
    const q = body?.variables?.request?.q;
    log.reads.push(typeof q === "string" ? q : "");
  });

  return log;
}

// The delete the cancel case must prove never happened. collectiveDelete posts
// ai_memory_delete with layer='collective', so this is the one operation that would remove
// a row from the tenant's shared memory.
export const OP_MEMORY_DELETE = "Memory_ai_memory_delete";

// Counts collective deletes leaving the page. Asserting on the wire rather than on the row
// still being listed is what makes "Cancel had no side effect" provable: a delete that was
// sent and failed server-side would leave the row on screen too.
export interface DeleteCallLog {
  count: number;
}

export function trackCollectiveDeletes(page: Page): DeleteCallLog {
  const log: DeleteCallLog = { count: 0 };

  page.on("request", (request) => {
    if (!request.url().includes("/api/graphql")) return;
    // A probe, not an assertion: nearly every request here is some other operation, and
    // "this is not a delete" is the normal answer.
    let body: { operationName?: string; variables?: { request?: { layer?: string } } } | null = null;
    try {
      body = request.postDataJSON();
    } catch {
      return;
    }
    if (body?.operationName !== OP_MEMORY_DELETE) return;
    if (body?.variables?.request?.layer !== COLLECTIVE_LAYER) return;
    log.count += 1;
  });

  return log;
}

// CollectiveTab shows nothing while the read is in flight and then resolves to either the
// grouped cards or one of the two ds/EmptyState titles. Waiting for whichever rendered is
// the only end-of-load signal there is, and it is why every case reaches the tab through
// this rather than asserting a row straight away.
export async function waitForCollectiveSettled(locators: CollectiveLocators): Promise<void> {
  await expect(
    locators.deleteButtons.first().or(locators.noKnowledgeEmptyState).or(locators.noMatchEmptyState)
  ).toBeVisible({ timeout: 40000 });
}

// CollectiveTab has no error state of its own — a failed read raises a toast and leaves the
// row list empty, so a broken backend renders as the ordinary "nothing here yet" empty
// state. Asserting the toast is absent is what stops every empty-state branch below from
// passing green on an environment where the read is simply failing.
export async function expectNoLoadFailure(locators: CollectiveLocators): Promise<void> {
  await expect(locators.toastWithText("Could not load Collective")).toHaveCount(0);
}

// Logs in, opens the Nubi panel and lands on b-Cortex > Memory > Collective. Memory is
// b-Cortex's default group but Patterns is that group's default sub-tab, so unlike Digests
// this needs the sub-tab click as well.
export async function openCollectiveTab(page: Page): Promise<{ locators: CollectiveLocators; log: CollectiveReadLog }> {
  const loginPage = new LoginPage(page);
  const nubi = new NubiLocators(page);
  const locators = new CollectiveLocators(page);

  await loginPage.doFullLogin();
  await nubi.openPanel();
  await nubi.openBCortex(locators.memoryGroupTab);

  // Attached before the sub-tab is selected so the tab's own first browse is captured — it
  // is issued on mount.
  const log = trackCollectiveReads(page);
  await locators.collectiveTab.click();

  // A tenant with the b-Cortex module off renders the placeholder instead of the tab, which
  // would otherwise surface as every control below being absent. Raced rather than asserted
  // straight away: checking for the placeholder on its own right after the click passes
  // against the not-yet-rendered tab and proves nothing, so wait until whichever of the two
  // rendered, then name which it was.
  await expect(locators.tabDescription.or(locators.bCortexDisabledPanel).first()).toBeVisible({ timeout: 40000 });
  await expect(locators.bCortexDisabledPanel).toHaveCount(0);
  await waitForCollectiveSettled(locators);
  await expectNoLoadFailure(locators);
  return { locators, log };
}

// Fails with a message naming what was missing rather than letting a tenant with no shared
// memory surface as a locator timeout halfway down a case. Collective rows are written by
// the agents as the team uses Nubi, so this is a data precondition, not a product bug.
// Deliberately does NOT assert the empty state is absent first: on a tenant with no shared
// memory that assertion fails with a generic Playwright count error and the named message
// below never fires, which is the whole reason this helper exists. waitForCollectiveSettled
// has already run by this point, so a zero count here means the list really is empty rather
// than still loading.
export async function requireCollectiveRows(locators: CollectiveLocators, minimum: number): Promise<number> {
  const rows = await locators.deleteButtons.count();
  if (rows < minimum) {
    throw new Error(
      `this case needs at least ${minimum} insight row(s) in b-Cortex > Memory > Collective, found ${rows} — no collective knowledge has been captured for this tenant on this environment`
    );
  }
  return rows;
}

// Waits for the next collective read to come back and returns its q. Polled on the log
// rather than awaited as a single response so a read that completed between the keystroke
// and this call is not missed.
//
// Returns the read at `seen` — the FIRST read after the action — not the most recent one.
// The two differ only when the action produced more than one read, and then the first is
// the honest answer: for the Enter-to-search case a second read means a keystroke searched, and
// reporting the last one would quietly hand back the full query and hide exactly that.
export async function waitForNewRead(log: CollectiveReadLog, seen: number): Promise<string> {
  await expect
    .poll(() => log.reads.length, { timeout: 40000, message: "no further collective read came back from the backend" })
    .toBeGreaterThan(seen);
  return log.reads[seen];
}

// Types a query into the search box one character at a time, presses Enter and waits for the
// read it triggers. CollectiveTab only commits the query on Enter (#40743), and pressSequentially
// produces the per-character input events a real user does, so a search fired per keystroke
// would show up in the log as extra reads.
export async function searchFor(
  locators: CollectiveLocators,
  log: CollectiveReadLog,
  query: string
): Promise<{ readQ: string; readsBefore: number }> {
  const readsBefore = log.reads.length;
  await locators.searchInput.click();
  await locators.searchInput.pressSequentially(query);
  await expect(locators.searchInput).toHaveValue(query, { timeout: 15000 });
  await locators.searchInput.press("Enter");
  const readQ = await waitForNewRead(log, readsBefore);
  await waitForCollectiveSettled(locators);
  return { readQ, readsBefore };
}

// Empties the search box, which CollectiveTab treats as a browse and fetches immediately
// without waiting for Enter.
export async function clearSearch(locators: CollectiveLocators, log: CollectiveReadLog): Promise<string> {
  const readsBefore = log.reads.length;
  await locators.searchInput.fill("");
  await expect(locators.searchInput).toHaveValue("", { timeout: 15000 });
  const readQ = await waitForNewRead(log, readsBefore);
  await waitForCollectiveSettled(locators);
  return readQ;
}

// A query no stored insight can contain, so the no-match branch is reached on its own
// merits rather than on a word that merely happens to be absent today. Unique per call so a
// future cached-search layer cannot answer it from an earlier run.
export function unmatchableQuery(): string {
  return `zqxvj${Date.now()}`;
}

// The longest word in a row's headline, which is what the search case searches for.
// Postgres full-text search matches stemmed words rather than substrings, so a partial word
// or a short stopword would be a bad query through no fault of the tab; the longest word is
// the one least likely to be stemmed into something else or dropped as noise.
//
// \p{L} rather than [A-Za-z]: collective memory is written from real conversations, so a
// headline can carry accented, Cyrillic or CJK characters, and an ASCII-only class would
// throw below on data the tab renders perfectly well.
export function longestWord(text: string): string {
  const words = (text.match(/\p{L}{4,}/gu) ?? []).slice().sort((a, b) => b.length - a.length);
  if (words.length === 0) {
    throw new Error(`could not take a searchable word from the insight headline "${text}"`);
  }
  return words[0];
}
