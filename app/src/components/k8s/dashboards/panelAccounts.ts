import type { AccountOption, Panel } from '@api1/dashboards';

/** The account-scope half of a panel — what the editor commits on save. */
export type PanelScope = Pick<Panel, 'account_type' | 'account_ids'>;

/**
 * Resolves the accounts a panel queries.
 *
 * A panel names EITHER a cloud provider (every account of that type) or a fixed
 * set of account ids — never both; the backend rejects that at save. Type-scoped
 * panels are resolved here, at render, against the accounts the viewer can
 * actually see, so the panel widens as accounts are connected and never asks for
 * one the viewer has no access to.
 *
 * Ids that no longer resolve (account deleted, or access revoked) are dropped
 * rather than passed through: querying them would only produce a 403 per panel.
 * Callers compare the resolved length against `panel.account_ids` when they need
 * to tell the user something went missing.
 */
export function resolvePanelAccounts(panel: PanelScope, accounts: AccountOption[]): AccountOption[] {
  if (panel.account_type) {
    return accounts.filter((a) => a.cloud_provider === panel.account_type);
  }
  const wanted = panel.account_ids || [];
  if (wanted.length === 0) return [];
  const byId = new Map(accounts.map((a) => [a.value, a]));
  // Authoring order is preserved so series stay in the order the author picked,
  // rather than in whatever order the account list happens to arrive in.
  return wanted.map((id) => byId.get(id)).filter((a): a is AccountOption => Boolean(a));
}

/**
 * The account type to show in the editor for an existing panel.
 *
 * An id-scoped panel stores no type — the ids already imply it — so it is read
 * back off the first account that still resolves. Without this, reopening such a
 * panel would show an empty type and an account list filtered to nothing.
 */
export function deriveAccountType(panel: PanelScope, accounts: AccountOption[]): string {
  if (panel.account_type) return panel.account_type;
  return resolvePanelAccounts(panel, accounts)[0]?.cloud_provider || '';
}

/**
 * Normalises the editor's two controls into the one-or-the-other shape the
 * backend enforces.
 *
 * Account type doubles as the filter for the account picker, so both controls
 * are populated while editing. Picking specific accounts wins and the type is
 * dropped — the ids already carry it. Picking none means "every account of this
 * type". That keeps the stored panel unambiguous without making the type filter
 * something the user has to clear by hand.
 */
export function panelScope(accountType: string, accountIds: string[]): PanelScope {
  const ids = accountIds.filter(Boolean);
  if (ids.length > 0) return { account_type: undefined, account_ids: ids };
  return { account_type: accountType || undefined, account_ids: [] };
}

/** Every account belonging to any of these providers, in account-list order. */
export function accountsOfTypes(types: string[], accounts: AccountOption[]): AccountOption[] {
  const wanted = new Set(types.filter(Boolean));
  return accounts.filter((a) => wanted.has(a.cloud_provider));
}

/**
 * The same normalisation for a panel that may span SEVERAL providers.
 *
 * A `nudgebee` panel reads the internal query engine, which takes a list of
 * account ids and does not care what provider they came from — so "AWS and GCP"
 * is a scope that means something there, and only there. Every other datasource
 * resolves one provider's integration and gets the single-type control.
 *
 * `account_type` names exactly ONE provider, so a multi-provider choice can only
 * be stored as the accounts themselves. That is a real trade-off: an id list is
 * pinned at authoring time and will not pick up an account connected later,
 * where a single type keeps resolving at render. It is the price of the scope
 * being expressible at all; `deriveAccountTypes` reads the types back out so the
 * editor still shows providers rather than a wall of account names.
 */
export function panelScopeFromTypes(accountTypes: string[], accountIds: string[], accounts: AccountOption[]): PanelScope {
  const ids = accountIds.filter(Boolean);
  if (ids.length > 0) return { account_type: undefined, account_ids: ids };

  const types = accountTypes.filter(Boolean);
  if (types.length === 0) return { account_type: undefined, account_ids: [] };
  if (types.length === 1) return { account_type: types[0], account_ids: [] };
  return { account_type: undefined, account_ids: accountsOfTypes(types, accounts).map((a) => a.value) };
}

/**
 * The account types to show in the editor for an existing panel — the plural of
 * `deriveAccountType`, for the multi-provider control.
 */
export function deriveAccountTypes(panel: PanelScope, accounts: AccountOption[]): string[] {
  if (panel.account_type) return [panel.account_type];
  const providers = resolvePanelAccounts(panel, accounts)
    .map((a) => a.cloud_provider)
    .filter(Boolean);
  return [...new Set(providers)];
}

/**
 * Whether a panel names EVERY account of the providers it spans.
 *
 * That is what `panelScopeFromTypes` writes for a multi-provider choice, and it
 * has to be told apart from a genuine hand-picked subset: the first should
 * reopen as "these providers, all accounts" with the account picker empty, the
 * second as the exact accounts that were ticked.
 */
export function coversAllOfTypes(panel: PanelScope, accounts: AccountOption[]): boolean {
  if (panel.account_type) return true;
  const ids = new Set((panel.account_ids || []).filter(Boolean));
  if (ids.size === 0) return false;
  const inTypes = accountsOfTypes(deriveAccountTypes(panel, accounts), accounts);
  return inTypes.length > 0 && inTypes.every((a) => ids.has(a.value));
}

/**
 * Labels describing what a panel is scoped to, one per chip: the provider for a
 * type-scoped panel, or every account name for an id-scoped one. Empty when
 * nothing resolves.
 */
export function panelScopeLabels(panel: PanelScope, accounts: AccountOption[]): string[] {
  if (panel.account_type) return [panel.account_type];
  return resolvePanelAccounts(panel, accounts).map((a) => a.label);
}

/**
 * Narrows a panel's accounts to the viewer's filter. An empty filter means "no
 * filter applied" — the toolbar-filter convention — not "show nothing".
 */
export function applyAccountFilter(resolved: AccountOption[], filterIds?: string[]): AccountOption[] {
  if (!filterIds || filterIds.length === 0) return resolved;
  const wanted = new Set(filterIds);
  return resolved.filter((a) => wanted.has(a.value));
}

/**
 * The account a panel's own filter resolves to, given what the viewer picked.
 *
 * The scope moves under the selection: the author edits the panel's accounts, or
 * one is disconnected. The panel is not remounted for either — its key is the
 * panel id, which does not change — so a pick made before the change is still
 * held. Left alone it narrows the panel to an account outside its scope, which
 * resolves to nothing and blanks the panel behind a filter message the viewer
 * never set, and on a panel that now names ONE account the picker is not even
 * rendered to clear it with.
 *
 * So a selection that no longer resolves is dropped rather than kept, and the
 * panel falls back to the same default it had before anyone picked anything.
 */
export function effectiveFilterAccount(selected: string, scoped: AccountOption[]): string {
  if (selected && scoped.some((a) => a.value === selected)) return selected;
  return scoped[0]?.value || '';
}

/** What a panel should actually query, given its scope and the viewer's filter. */
export interface PanelQueryAccounts {
  accounts: AccountOption[];
  /** True when the account was chosen for the viewer rather than by them. */
  autoSelected: boolean;
}

/**
 * Decides which accounts a panel queries.
 *
 * A panel scoped to MANY accounts queries exactly ONE — the first, until the
 * viewer picks another from the panel's Account filter. Querying all of them
 * would be an unreadable chart and one provider request per account on every
 * render (Datadog bills per call), and waiting for a choice left the panel
 * showing nothing at all, which reads as broken.
 *
 * `singleCall` is the exception, and it is the query engine: it takes an
 * account_id LIST and answers in one request, so the two reasons for the rule —
 * cost per call, and a chart with N series of the same line — do not apply.
 * Slicing there is not a safeguard but a silent wrong answer: a "spend by
 * account" table scoped to five accounts would show one account's spend under a
 * title that claims all five. Rollup widgets are the whole point of the summary
 * dashboards, so they must see every account they were scoped to.
 *
 * `filterIds` is a list even though the panel picker is single-select, so the
 * multi-account fan-out in usePanelData stays exercised by a one-account panel
 * and this rule does not have to change if the picker ever gains multi-select.
 */
export function panelQueryAccounts(scoped: AccountOption[], filterIds?: string[], singleCall = false): PanelQueryAccounts {
  if (!filterIds || filterIds.length === 0) {
    if (singleCall) return { accounts: scoped, autoSelected: false };
    // Authoring order, so every viewer of the dashboard auto-lands on the same
    // account rather than on whichever one the account list happened to sort
    // first.
    return { accounts: scoped.slice(0, 1), autoSelected: scoped.length > 1 };
  }
  // A filter naming only out-of-scope accounts resolves to nothing; that is a
  // filter miss, not an unmade choice, and gets its own message downstream.
  return { accounts: applyAccountFilter(scoped, filterIds), autoSelected: false };
}

/** Single-chip summary, for surfaces with room for only one label. */
export function describePanelScope(panel: PanelScope, accounts: AccountOption[]): string {
  if (panel.account_type) return `All ${panel.account_type}`;
  const resolved = resolvePanelAccounts(panel, accounts);
  if (resolved.length === 0) return 'No account';
  if (resolved.length === 1) return resolved[0].label;
  return `${resolved.length} accounts`;
}
