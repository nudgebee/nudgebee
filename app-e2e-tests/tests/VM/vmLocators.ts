import { Page, Locator } from "@playwright/test";
import { CommonLocators } from "../GlobalLocators";

// Table ids come straight from the components:
//   app/src/components/vm/VmInventory.tsx        TABLE_ID = 'VM_INVENTORY_TABLE'
//   app/src/components/vm/VmPackages.tsx         tableId  = 'VM_PACKAGES_TABLE' (account-wide)
//   app/src/components/vm/VmVulnerabilities.tsx  tableId  = 'VM_VULNERABILITIES_TABLE' (flat, unscoped)
//                                                          'VM_VULNERABILITY_GROUPS_<grouping>' (grouped)
// CustomTable puts the id on the <table> and `${id}-body` on the <tbody>. With no rows
// it renders an EmptyData panel in their place — but not an id'd one on these tables;
// see emptyIn() below.
//
// Expandable tables (the VM inventory, and the grouped vulnerability views) emit a
// second <tr> per data row to hold the collapsed drill-down. It is in the DOM whether
// or not the row is open and carries a single colSpan cell, so every row locator here
// requires a second cell — otherwise every count is silently doubled.
const DATA_ROW = "tr:has(td:nth-child(2))";
export const INVENTORY_TABLE = "VM_INVENTORY_TABLE";
export const PACKAGES_TABLE = "VM_PACKAGES_TABLE";
export const VULNERABILITIES_TABLE = "VM_VULNERABILITIES_TABLE";

export class VmLocators extends CommonLocators {
  // Tab strip — AnchorComponent renders `anchor-tab-${opt.id}` for each entry of
  // tabOptions in app/src/pages/vm/index.tsx.
  readonly summaryTab: Locator;
  readonly instancesTab: Locator;
  readonly vulnerabilitiesTab: Locator;
  readonly packagesTab: Locator;

  // Summary tiles (ds/Stat puts `id` on its root Box).
  readonly statVms: Locator;
  readonly statAgents: Locator;
  readonly statPackages: Locator;
  readonly statVulnerabilities: Locator;
  readonly statLastScan: Locator;
  readonly statInventoried: Locator;
  readonly statNeverScanned: Locator;

  // Virtual Machines tab.
  readonly inventoryRoot: Locator;
  readonly inventorySearch: Locator;
  readonly inventoryTable: Locator;
  readonly inventoryRows: Locator;
  readonly scanAccountBtn: Locator;

  // Account scan dialog (ScanAccountDialog.tsx).
  readonly scanAccountDialog: Locator;
  readonly scanAccountCancelBtn: Locator;
  readonly scanAccountSubmitBtn: Locator;

  // Vulnerabilities tab.
  readonly vulnerabilitiesRoot: Locator;
  readonly vulnGroupTabAll: Locator;
  readonly vulnGroupTabVulnerability: Locator;
  readonly vulnGroupTabPackage: Locator;
  readonly vulnGroupTabVm: Locator;

  // Packages tab.
  readonly packagesRoot: Locator;
  readonly packagesSearch: Locator;
  readonly packageTypeFilter: Locator;

  constructor(page: Page) {
    super(page);

    this.summaryTab = page.locator("#anchor-tab-vm-summary");
    this.instancesTab = page.locator("#anchor-tab-vm-instances");
    this.vulnerabilitiesTab = page.locator("#anchor-tab-vm-vulnerabilities");
    this.packagesTab = page.locator("#anchor-tab-vm-packages");

    this.statVms = page.locator("#vm-summary-vms");
    this.statAgents = page.locator("#vm-summary-agents");
    this.statPackages = page.locator("#vm-summary-packages");
    this.statVulnerabilities = page.locator("#vm-summary-vulnerabilities");
    this.statLastScan = page.locator("#vm-summary-last-scan");
    this.statInventoried = page.locator("#vm-summary-scanned");
    this.statNeverScanned = page.locator("#vm-summary-never-scanned");

    this.inventoryRoot = page.locator("#vm-inventory");
    this.inventorySearch = page.locator("#vm-inventory-search");
    this.inventoryTable = page.locator(`#${INVENTORY_TABLE}`);
    this.inventoryRows = page.locator(`#${INVENTORY_TABLE}-body ${DATA_ROW}`);
    this.scanAccountBtn = page.locator("#vm-scan-account");

    this.scanAccountDialog = page.locator('[role="dialog"]').filter({ hasText: "Scan account for vulnerabilities" });
    this.scanAccountCancelBtn = page.locator("#vm-scan-account-cancel");
    this.scanAccountSubmitBtn = page.locator("#vm-scan-account-submit");

    this.vulnerabilitiesRoot = page.locator("#vm-vulnerabilities");
    // The grouping tabs are a ds/ToggleGroup, which renders each option as
    // <button role="radio"> carrying its label. GROUP_TABS in VmVulnerabilities.tsx still
    // declares a per-tab id, but the render maps only {value, label} - so those ids reach
    // no DOM node and the old "#vm-vulnerability-tab-*" locators match nothing.
    const vulnGrouping = page.locator("#vm-vulnerability-grouping");
    this.vulnGroupTabAll = vulnGrouping.getByRole("radio", { name: "All", exact: true });
    this.vulnGroupTabVulnerability = vulnGrouping.getByRole("radio", { name: "Vulnerability", exact: true });
    this.vulnGroupTabPackage = vulnGrouping.getByRole("radio", { name: "Package", exact: true });
    this.vulnGroupTabVm = vulnGrouping.getByRole("radio", { name: "VM", exact: true });

    this.packagesRoot = page.locator("#vm-packages");
    this.packagesSearch = page.locator("#vm-packages-search");
    this.packageTypeFilter = page.locator("#auto-complete-vm-packages-type");
  }

  // A table either has rows or renders its empty panel in their place — never both,
  // and which one shows depends on the fleet's data, not on the code under test. Every
  // list assertion here goes through these so a legitimately empty account reads as a
  // pass rather than a locator that "did not appear".
  rowsFor(tableId: string): Locator {
    return this.page.locator(`#${tableId}-body ${DATA_ROW}`);
  }

  // All three VM tables pass `showUpdatedEmptyData`, and that branch of CustomTable
  // renders a fixed `<EmptyData heading='All good here!'>` — it forwards neither the
  // table `id` nor the `emptyHeading` the caller supplied. So there is no
  // `#<tableId>-no-data` node to key off, and the module's own empty copy
  // ("No virtual machines yet", "No open vulnerabilities") never reaches the DOM.
  // Scoped to the listing root because the string is identical on every table.
  emptyIn(root: Locator): Locator {
    return root.getByRole("heading", { name: "All good here!" });
  }

  // Waits out the fetch. While `loading` is true CustomTable swaps in a skeleton
  // <tbody> that carries no id, so the id'd body being attached means the response
  // has landed and the rows on screen are the current ones.
  async waitForTable(tableId: string, root: Locator, timeout = 60000): Promise<void> {
    await this.page
      .locator(`#${tableId}-body`)
      .or(this.emptyIn(root))
      .first()
      .waitFor({ state: "attached", timeout });
  }

  // Only for a baseline taken before any interaction. After an action that refetches,
  // assert the expected end state with a retrying `expect(rowsFor(...)).toHaveCount(n)`
  // instead — a plain count races the in-flight request and reads the stale table.
  async rowCount(tableId: string, root: Locator): Promise<number> {
    await this.waitForTable(tableId, root);
    return this.rowsFor(tableId).count();
  }

  // SearchInput commits on Enter (onEnterPress) — typing alone filters nothing.
  async searchAndApply(input: Locator, term: string): Promise<void> {
    await input.click();
    await input.fill(term);
    await input.press("Enter");
  }

  // The X only renders while the field has a value, so clearing goes through the
  // field itself and re-commits, matching SearchInput's onChange('') fast path.
  async clearSearch(input: Locator): Promise<void> {
    await input.click();
    await input.fill("");
    await input.press("Enter");
  }
}
