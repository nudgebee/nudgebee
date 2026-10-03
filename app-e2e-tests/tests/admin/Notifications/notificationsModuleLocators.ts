// Not for OSS
import { Page, Locator } from "@playwright/test";
import { NotificationLocators } from "./NotificationLocators";
import { RULES_TABLE } from "./notificationsModuleConstants";

// Extends NotificationLocators rather than restating it: the rule modal's name input,
// source tabs, enable switch, Create Rule button and success toasts are already declared
// there and are inherited unchanged. Only the listing surface, the scope filters and the
// delete dialog are new here.
//
// Every primary below is an id. app/src/components/notifications/ renders zero
// data-testid across all five of its files (0 in index.tsx, NotificationRuleModal.tsx,
// DeleteNotificationRuleModal.tsx, ChannelAccountMapping.tsx and WatchedChannels.tsx),
// so rung 3 of the ladder is the highest rung available for these elements.
export class NotificationsModuleLocators extends NotificationLocators {
  readonly listingRoot: Locator;
  readonly rulesTable: Locator;
  readonly rulesTableBody: Locator;
  readonly ruleRows: Locator;
  readonly emptyState: Locator;

  readonly clusterFilter: Locator;
  readonly namespaceFilter: Locator;
  readonly applicationFilter: Locator;

  readonly ruleDialog: Locator;
  readonly ruleSubmitBtn: Locator;
  readonly ruleCancelBtn: Locator;
  readonly nameError: Locator;

  readonly deleteDialog: Locator;
  readonly deleteConfirmBtn: Locator;
  readonly deleteCancelBtn: Locator;

  readonly accessUsersTab: Locator;
  readonly auditLogMenuItem: Locator;
  readonly auditsTab: Locator;

  constructor(page: Page) {
    super(page);

    this.listingRoot = page.locator("#notification-container").or(page.locator(`#${RULES_TABLE}`)).first();

    this.rulesTable = page.locator(`#${RULES_TABLE}`).or(this.listingRoot.getByRole("table")).first();

    this.rulesTableBody = page.locator(`#${RULES_TABLE}-body`).or(this.rulesTable.locator("tbody")).first();

    // showExpandable={false} on this table, so there is no second <tr> per data row and
    // a plain row count is accurate. Requiring a <td> still excludes the header row.
    this.ruleRows = this.rulesTableBody.locator("tr:has(td)");

    // EmptyData puts `${id}-no-data` on its heading when the table has no rows.
    this.emptyState = page
      .locator(`#${RULES_TABLE}-no-data`)
      .or(this.listingRoot.getByText("No Data Available", { exact: false }))
      .first();

    // FilterDropdown renders its trigger as <button id={`auto-complete-${toKebabCase(id)}`}>
    // (app/src/components/common/ds/FilterDropdown.jsx), so the DOM id is the prop id from
    // index.tsx with that prefix — not the prop id on its own.
    this.clusterFilter = page
      .locator("#auto-complete-notification-filter-cluster")
      .or(this.listingRoot.getByRole("button", { name: /Cluster/i }))
      .first();

    this.namespaceFilter = page
      .locator("#auto-complete-notification-filter-namespace")
      .or(this.listingRoot.getByRole("button", { name: /Namespace/i }))
      .first();

    this.applicationFilter = page
      .locator("#auto-complete-notification-filter-application")
      .or(this.listingRoot.getByRole("button", { name: /Application/i }))
      .first();

    // Both dialogs render #submit and #cancel, so every button below is scoped to its own
    // dialog by the title that dialog shows. Unscoped, `.or()` resolves in document order
    // and a delete confirmation could click the rule form's Save.
    this.ruleDialog = page.locator('[role="dialog"]').filter({ hasText: "Rule Name" }).first();

    this.ruleSubmitBtn = this.ruleDialog.locator("#submit").or(this.ruleDialog.getByRole("button", { name: /^Save$|^Submit$/i })).first();

    this.ruleCancelBtn = this.ruleDialog.locator("#cancel").or(this.ruleDialog.getByRole("button", { name: /^Cancel$/i })).first();

    // ds/Input renders its message as <span id={`${inputId}-error`} role="alert">, so the
    // error is addressable directly rather than by matching its text on the page.
    this.nameError = page.locator("#notificationName-error").or(this.ruleDialog.getByRole("alert")).first();

    this.deleteDialog = page.locator('[role="dialog"]').filter({ hasText: "Confirm Delete" }).first();

    this.deleteConfirmBtn = this.deleteDialog.locator("#submit").or(this.deleteDialog.getByRole("button", { name: /^Delete$/i })).first();

    this.deleteCancelBtn = this.deleteDialog.locator("#cancel").or(this.deleteDialog.getByRole("button", { name: /^Cancel$/i })).first();

    // Access & Users is the top-level tab Audit Log now lives under (AnchorComponent's
    // own anchor-tab-<id> button — id is pinned to "AccessUsers" in
    // user-management/index.jsx since the display name has a space and an
    // ampersand, neither safe in a bare CSS id selector). Audit Log itself is
    // one of its sub-tabs, not reachable until this top-level tab is open — see
    // the two-step click in NotificationsModule.spec.ts.
    this.accessUsersTab = page.locator("#anchor-tab-AccessUsers").or(page.getByRole("link", { name: "Access & Users" })).first();
    // AnchorComponent renders no data-testid; its hover-menu MenuItem id is `dropdown-${item.id}`.
    this.auditLogMenuItem = page
      .locator('[role="menuitem"]#dropdown-audit-log:visible')
      .or(page.locator('[role="menuitem"]:visible').filter({ hasText: "Audit Log" }))
      .first();
    // Sub-tab of Access & Users, rendered by the shared Tabs.jsx (a real MUI Tab)
    // — id comes from a11yProps(value, opt.id) where opt.id is "audit-log".
    this.auditsTab = page.locator("#audit-log").or(page.getByRole("tab", { name: "Audit Log", exact: true })).first();
  }

  // Rows are matched on the rule name, which this suite generates unique per rule — so
  // this never straddles two rows and never touches a pre-existing rule.
  rowByName(name: string): Locator {
    return this.ruleRows.filter({ hasText: name });
  }

  // index.tsx gives the delete button id={`${item.name}-delete`}. The generated name
  // contains spaces, so the attribute form is required — as a `#` selector the spaces
  // would parse as descendant combinators.
  deleteBtnForRule(name: string): Locator {
    return this.rowByName(name).locator(`[id="${name}-delete"]`).or(this.rowByName(name).getByRole("button", { name: "Delete" })).first();
  }
}
