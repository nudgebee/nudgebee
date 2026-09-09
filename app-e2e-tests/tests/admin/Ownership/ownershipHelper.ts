// Not for OSS

import { Browser, Page, Locator, expect } from "@playwright/test";
import { existsSync } from "fs";
import { OwnershipLocators } from "./ownershipLocators";
import { AUTH_STATE_PATH } from "../../utils/paths";
import { E2E_RULE_PREFIX, NO_RESULTS_TEXT } from "./ownershipConstants";

// API-level helpers for the Ownership suite.
//
// Everything goes through `page.request`, which shares the authenticated
// storageState cookie with the browser context, so these calls are made AS the
// signed-in run user and traverse the same /api/graphql gateway the UI does.
// They exist to seed and sweep fixtures cheaply and to PROVE persistence
// independently of the listing that was just rendered.

export type OwnershipRule = {
  id: string;
  name: string;
  resource_domain: string;
  match_scope: string;
  match_key: string;
  match_value: string;
  cloud_account_id: string;
  owner_type: string;
  owner_id: string;
  enabled: boolean;
};

export type OwnerRef = { ownerType: "user" | "group"; ownerId: string };

// Mirrors app/src/api1/ownership/index.js — same operations the UI issues.
const RULE_FIELDS = "id name resource_domain match_scope match_key match_value cloud_account_id owner_type owner_id enabled";

const LIST_RULES = `
query OwnershipListRules {
  ownership_list_rules {
    ${RULE_FIELDS}
  }
}`;

const UPSERT_RULE = `
mutation OwnershipUpsertRule($id: String, $name: String!, $resource_domain: String, $match_scope: String!, $match_key: String, $match_value: String, $cloud_account_id: String, $owner_type: String!, $owner_id: String!, $enabled: Boolean) {
  ownership_upsert_rule(id: $id, name: $name, resource_domain: $resource_domain, match_scope: $match_scope, match_key: $match_key, match_value: $match_value, cloud_account_id: $cloud_account_id, owner_type: $owner_type, owner_id: $owner_id, enabled: $enabled) {
    status id
  }
}`;

const DELETE_RULE = `
mutation OwnershipDeleteRule($id: String!) {
  ownership_delete_rule(id: $id) {
    status count
  }
}`;

// where:{} is inlined exactly as the app inlines it — GET_USERS in
// app/src/lib/UserService.ts substitutes gqlStringify({}) into the same slot.
const GET_USERS = `
query GetUsers($offset: Int, $limit: Int) {
  users: users_list_by_tenant(limit: $limit, offset: $offset, order_by: [{column: "username", order: asc}], where: {}) {
    rows {
      id
      status
    }
  }
}`;

async function gql(page: Page, query: string, operationName: string, variables: Record<string, unknown> = {}): Promise<any> {
  const res = await page.request.post("/api/graphql", {
    data: { query, operationName, variables },
    headers: { "Content-Type": "application/json" },
    failOnStatusCode: false,
  });
  const text = await res.text();
  let body: any;
  try {
    body = JSON.parse(text);
  } catch {
    throw new Error(`${operationName} returned non-JSON (HTTP ${res.status()})`);
  }
  const err = body?.errors?.[0];
  if (err) throw new Error(`${operationName} failed: ${err.message ?? JSON.stringify(err)}`);
  return body?.data;
}

// Runs an API-only task on a throwaway page in beforeAll / afterAll, where no
// `page` fixture exists.
//
// The storageState and baseURL are passed EXPLICITLY here on purpose: the
// runner applies playwright.config's `use` block in its `context` fixture
// (node_modules/playwright/lib/index.js — `browser.newContext({...options})`),
// NOT by patching the browser, so a bare browser.newPage() would come up
// logged out and with no baseURL, and every relative request below would fail
// before reaching the gateway.
export async function withApiPage<T>(browser: Browser, fn: (page: Page) => Promise<T>): Promise<T> {
  const context = await browser.newContext({
    baseURL: process.env.BASE_URL,
    // Absent only when global-setup has not run; the tests themselves fail with
    // a clearer message in that case than an ENOENT at context creation would.
    storageState: existsSync(AUTH_STATE_PATH) ? AUTH_STATE_PATH : undefined,
  });
  try {
    return await fn(await context.newPage());
  } finally {
    await context.close();
  }
}

// Unique-but-recognisable rule name, so two runs never collide on the shared dev
// tenant and a crashed run's leftovers stay sweepable by prefix.
export function ruleName(suffix: string): string {
  return `${E2E_RULE_PREFIX}${suffix}-${Date.now().toString(36)}`;
}

export async function listRules(page: Page): Promise<OwnershipRule[]> {
  const data = await gql(page, LIST_RULES, "OwnershipListRules");
  const rows = data?.ownership_list_rules;
  return Array.isArray(rows) ? rows : [];
}

export async function findRule(page: Page, name: string): Promise<OwnershipRule | undefined> {
  return (await listRules(page)).find((r) => r.name === name);
}

export async function deleteRuleById(page: Page, id: string): Promise<void> {
  await gql(page, DELETE_RULE, "OwnershipDeleteRule", { id });
}

// Removes every rule this suite has ever created on this tenant. Runs before AND
// after the suite so a run that died mid-test cannot leave the shared dev tenant
// dirty, and so the listing assertions are never confused by an earlier run.
export async function sweepE2ERules(page: Page): Promise<void> {
  for (const rule of await listRules(page)) {
    if (rule.name?.startsWith(E2E_RULE_PREFIX)) {
      await deleteRuleById(page, rule.id);
    }
  }
}

// An owner the tenant actually has, for seeding rules without hardcoding any
// person. Only ACTIVE users are assignable (useOwnerDirectory filters the picker
// the same way), so the guard mirrors the UI rather than trusting row order.
export async function firstActiveOwner(page: Page): Promise<OwnerRef> {
  const data = await gql(page, GET_USERS, "GetUsers", { limit: 1000, offset: 0 });
  const rows: Array<{ id: string; status?: string }> = data?.users?.rows ?? [];
  const active = rows.find((u) => u.id && (!u.status || u.status === "active"));
  if (!active) {
    throw new Error("No active user on this tenant to own a rule — the owner directory would be empty in the UI too");
  }
  return { ownerType: "user", ownerId: active.id };
}

// Seeds a label-scope rule straight through the API. Used by the tests whose
// subject is edit / delete / toggle / navigation rather than creation, so a
// regression in the create flow fails only the create test.
//
// The default match value is derived from the name but deliberately does NOT
// contain it: the listing renders "<key> = <value>" in the Match column, so a
// rule whose value repeated its own name kept matching a whole-row hasText
// search for the OLD name after a rename — the row is still there, only its
// Name cell changed. Dropping the prefix keeps the value unique per rule (two
// rules matching the same target are rejected as overlapping) while leaving the
// name itself absent from every other cell.
export async function seedLabelRule(
  page: Page,
  name: string,
  owner: OwnerRef,
  overrides: Partial<{ matchKey: string; matchValue: string; enabled: boolean }> = {}
): Promise<string> {
  const data = await gql(page, UPSERT_RULE, "OwnershipUpsertRule", {
    name,
    resource_domain: "k8s",
    match_scope: "label",
    match_key: overrides.matchKey ?? "team",
    match_value: overrides.matchValue ?? `val-${name.slice(E2E_RULE_PREFIX.length)}`,
    cloud_account_id: "",
    owner_type: owner.ownerType,
    owner_id: owner.ownerId,
    enabled: overrides.enabled ?? true,
  });
  const id = data?.ownership_upsert_rule?.id;
  if (!id) throw new Error(`Seeding rule "${name}" returned no id`);
  return id;
}

// Opens Admin -> Ownership and returns the page object, ready for assertions.
export async function openOwnership(page: Page): Promise<OwnershipLocators> {
  const locators = new OwnershipLocators(page);
  await locators.open();
  return locators;
}

// Picks one option out of an open-on-demand ds/Select. The popup is the wait: it
// has to be on screen before an option can be clicked, and gone again before the
// next field is touched, or the overlay swallows that click.
export async function selectOption(locators: OwnershipLocators, trigger: Locator, label: string | RegExp): Promise<void> {
  await trigger.click();
  await expect(locators.openListbox).toBeVisible();
  await locators.option(label).click();
  await expect(locators.openListbox).toBeHidden();
}

// Opens the Owner picker and takes the first offered owner. Returns the option's
// visible label so a caller can assert against it WITHOUT the test ever having to
// name a real person — nothing here is logged.
export async function pickFirstOwner(locators: OwnershipLocators): Promise<string> {
  await locators.ownerSelect.click();
  await expect(locators.openListbox).toBeVisible();
  const first = locators.listboxOptions.first();
  // The directory loads lazily on first picker mount, so the popup can render its
  // skeleton before any row exists — the option itself is the readiness signal.
  await expect(first).toBeVisible({ timeout: 60000 });
  const label = ((await first.textContent()) ?? "").trim();
  await first.click();
  await expect(locators.openListbox).toBeHidden();
  return label;
}

// Opens the Match select and asserts it offers exactly the given scopes. Counted
// first, so an extra or a missing scope fails here rather than passing on a
// subset. Closes by choosing the first scope — the way a user leaves the popup —
// rather than by clicking the trigger again, which would race the backdrop's
// fade against the next click.
export async function expectScopeOptions(locators: OwnershipLocators, labels: string[]): Promise<void> {
  await locators.scopeSelect.click();
  await expect(locators.openListbox).toBeVisible();
  await expect(locators.listboxOptions).toHaveCount(labels.length);
  for (const label of labels) {
    await expect(locators.option(label)).toBeVisible();
  }
  await locators.option(labels[0]).click();
  await expect(locators.openListbox).toBeHidden();
}

// Types a query into the currently open picker's search box and waits for the
// list to settle on either matches or the empty state — both are real outcomes,
// so this never sleeps.
export async function searchInOpenPicker(locators: OwnershipLocators, query: string): Promise<void> {
  await locators.listboxSearch.fill(query);
  // Trailing .first(): the empty-state side of the alternation is not itself
  // pinned, so without it a second matching node would be a strict-mode failure
  // rather than the settle this is waiting for.
  await expect(
    locators.listboxOptions.first().or(locators.openListbox.getByText(NO_RESULTS_TEXT, { exact: true })).first()
  ).toBeVisible();
}
