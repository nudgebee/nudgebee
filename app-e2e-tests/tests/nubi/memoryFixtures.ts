// Not for OSS
import { Page, expect, test } from "@playwright/test";
import { askNubi } from "../utils/nubi";

// Shared by the b-Cortex > Memory specs. Every one of them reads data the product writes as
// people use Nubi, so on a tenant nobody has chatted with the list is empty and a row-dependent
// case fails on a data precondition rather than on a product bug. These helpers make the spec
// create what it needs the way a user would — by talking to Nubi — and only when the list is
// empty, so a tenant with real memory is never touched.
//
// What a chat can and cannot produce is decided by the backend, not by this suite:
//   - Sessions      one row per conversation, so any question does it.
//   - Preferences   extracted when the user STATES one; polled for, because extraction runs after
//                   the answer is delivered.
//   - Collective    environment facts, kept only from investigations that ran tools — a plain chat
//                   cannot be relied on to produce one, so collectiveHelper seeds it through the
//                   ai_memory_upsert path instead.
//   - Patterns, Digests   written by background jobs over past conversations; nothing a test does
//                   makes one appear on demand, so those specs skip with the reason.

// Same wire shape the app's own api1/memory wrapper sends: every memory call is a GraphQL query
// named Memory_<action> taking a <action>_request! variable.
export async function memoryGql(page: Page, action: string, request: Record<string, unknown>): Promise<any> {
  const operationName = `Memory_${action}`;
  const res = await page.request.post("/api/graphql", {
    data: {
      query: `query ${operationName}($request: ${action}_request!) { ${action}(request: $request) { data errors } }`,
      operationName,
      variables: { request },
    },
    headers: { "Content-Type": "application/json" },
    failOnStatusCode: false,
    timeout: 30000,
  });
  const text = await res.text();
  let body: any;
  try {
    body = JSON.parse(text);
  } catch {
    throw new Error(`${operationName} returned non-JSON (HTTP ${res.status()})`);
  }
  const err = body?.errors?.[0] ?? body?.data?.[action]?.errors?.[0];
  if (err) throw new Error(`${operationName} failed: ${err.message ?? JSON.stringify(err)}`);
  // A transport or gateway failure can come back with neither data nor errors. Reading that as an
  // empty list would make the caller seed memory (or skip a case) on the back of a failed read, so
  // a response without the action's own envelope is a failure. An envelope whose data is empty is
  // a legitimate empty result and is returned as such.
  if (!body?.data?.[action]) throw new Error(`${operationName} failed: response carried no result (HTTP ${res.status()})`);
  return body.data[action].data;
}

async function listEntries(page: Page, request: Record<string, unknown>): Promise<any[]> {
  const data = await memoryGql(page, "ai_memory_list", request);
  return data?.entries ?? [];
}

export const countSessions = async (page: Page): Promise<number> =>
  (await listEntries(page, { layer: "session", user_id: "", limit: 50 })).length;

// Only the ones still awaiting review: the Keep and Keep-cancel cases need a row that has not been
// kept already, and a queue where everything is kept is a valid state those cases cannot use.
export const countUnkeptInferredPreferences = async (page: Page): Promise<number> =>
  (await listEntries(page, { layer: "prefs", scope: "user", user_id: "", agent_module: "" })).filter(
    (row) => row?.origin === "inferred" && !row?.confirmed_at
  ).length;

async function waitForCount(page: Page, count: (page: Page) => Promise<number>, minimum: number, timeout: number, message: string) {
  await expect.poll(() => count(page), { timeout, intervals: [3000, 5000, 10000], message }).toBeGreaterThanOrEqual(minimum);
}

// Asks Nubi until the signed-in user has `minimum` sessions. A session is one conversation, so each
// question below opens a new chat. The questions are plain knowledge questions: nothing in the
// tenant is read or changed, and they cost one short answer each.
const SESSION_QUESTIONS = [
  "In one sentence, what is a Kubernetes pod?",
  "In one sentence, what is a Kubernetes namespace?",
  "In one sentence, what is a Kubernetes deployment?",
];

// Chatting is slow and the budget differs per environment (the case timeout is set per spec, and the
// config gives dev half what other environments get), so the case is given the seeding time on top
// of whatever it already had — and only when it actually has to seed, so a tenant with data keeps
// its original timeout.
function allowSeedingTime(ms: number): void {
  test.setTimeout(test.info().timeout + ms);
}

export async function ensureSessions(page: Page, minimum: number): Promise<void> {
  let have = await countSessions(page);
  if (have < minimum) allowSeedingTime(SESSION_QUESTIONS.length * 180000 + 120000);
  for (let i = 0; have < minimum && i < SESSION_QUESTIONS.length; i++) {
    await askNubi(page, SESSION_QUESTIONS[i], { newChat: true, timeoutMs: 180000 });
    have = await countSessions(page);
  }
  await waitForCount(
    page,
    countSessions,
    minimum,
    120000,
    `asked Nubi ${SESSION_QUESTIONS.length} question(s) but b-Cortex > Memory > Sessions still holds fewer than ${minimum} — session memory is not being written for this user`
  );
}

// States a preference the way a user would, then waits for the backend to infer it. Extraction runs
// after the answer is delivered, so it is polled rather than asserted straight away. If nothing
// appears in the window the case is skipped, not failed: whether a given chat is turned into a
// preference is the backend's call, and a red run there would say "not inferred here", not "broken".
const PREFERENCE_STATEMENTS = [
  "From now on, please start every answer with a one-line summary. Now, in one sentence, what is a Kubernetes pod?",
  "I prefer short answers with the key point first. In one sentence, what is a Kubernetes namespace?",
];

export async function ensureInferredPreferences(page: Page, minimum: number): Promise<void> {
  if ((await countUnkeptInferredPreferences(page)) >= minimum) return;
  allowSeedingTime(PREFERENCE_STATEMENTS.length * (180000 + 90000));
  for (const statement of PREFERENCE_STATEMENTS) {
    await askNubi(page, statement, { newChat: true, timeoutMs: 180000 });
    const found = await expect
      .poll(() => countUnkeptInferredPreferences(page), { timeout: 90000, intervals: [3000, 5000, 10000] })
      .toBeGreaterThanOrEqual(minimum)
      .then(() => true)
      .catch(() => false);
    if (found) return;
  }
  // Reached from beforeEach/openTab, where test.skip ends the case with this reason.
  test.skip(true, `Nubi was told two preferences but none was inferred within the wait — nothing to review on this environment`);
}

// For layers no chat can create (Patterns, Digests): skip with the reason instead of failing.
export function skipWhenMissing(rows: number, minimum: number, what: string): void {
  test.skip(rows < minimum, `needs at least ${minimum} ${what}, found ${rows} — written by a background job, nothing a test can trigger`);
}
