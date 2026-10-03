// Not for OSS
import { Page, Locator } from "@playwright/test";
import { CommonLocators } from "../../GlobalLocators";

// Every id here is read off app/src/components/vulnerabilities/VulnerabilityTable.tsx:
//   ListingLayout  id='vulnerabilities'                -> ds/ListingLayout puts it on its root Box
//   CustomTable    id='VULNERABILITIES_TABLE'          -> the <table>, plus `${id}-body` on the <tbody>
//   FilterDropdown id='VULNERABILITIES_TABLE-severity'
//   DownloadButton id='VULNERABILITIES_TABLE-download'
export const VULNERABILITIES_TABLE = "VULNERABILITIES_TABLE";

// A data row must carry more than one cell, so a skeleton or empty-state row never counts.
const DATA_ROW = "tr:has(td:nth-child(2))";

// SEVERITY_ORDER in app/src/api1/vulnerabilities — the order VulnerabilityTable builds
// SEVERITY_OPTIONS from, so it is also the order the filter panel lists them in.
export const SEVERITIES = ["Critical", "High", "Medium", "Low", "Info"] as const;

// HEADERS in VulnerabilityTable, for the account-wide table (the scoped one drops Host).
export const HEADERS = ["Severity", "Vulnerability", "Package", "Installed", "Fixed In", "CVSS", "Host", "Last Seen"];

export class VulnerabilitiesLocators extends CommonLocators {
  readonly vulnerabilitiesTab: Locator;
  readonly summaryTab: Locator;
  readonly root: Locator;
  readonly table: Locator;
  readonly rows: Locator;
  readonly severityCells: Locator;
  readonly severityFilter: Locator;
  readonly downloadBtn: Locator;
  readonly openFindingsNote: Locator;

  constructor(page: Page) {
    super(page);

    // AnchorComponent renders `anchor-tab-${opt.id || opt.name}`; the Vulnerabilities entry
    // in [CloudAccountDetails].jsx declares no id, so its name is the id. It is a
    // <Button component={Link} href>, i.e. an <a> carrying the tab name — hence the role
    // primary, with the id behind it in case that Button ever loses its href and its role.
    // `exact` keeps it off the "AWS Vulnerabilities" entries the global search offers.
    this.vulnerabilitiesTab = page
      .getByRole("link", { name: "Vulnerabilities", exact: true })
      .or(page.locator("#anchor-tab-Vulnerabilities"))
      .first();
    this.summaryTab = page.getByRole("link", { name: "Summary", exact: true }).or(page.locator("#anchor-tab-Summary")).first();

    // Id-only and deliberately so: ListingLayout's root is a plain Box with no role, no
    // accessible name and no testid, so there is no wider match that would still be this panel.
    this.root = page.locator("#vulnerabilities");
    this.table = this.root.locator(`#${VULNERABILITIES_TABLE}`);
    // Scoped to the listing root, not the page: VulnerabilityTable also renders host-scoped
    // and embedded copies of itself, and only the root keeps this the account-wide one.
    this.rows = this.root.locator(`#${VULNERABILITIES_TABLE}-body ${DATA_ROW}`);
    // Severity is the first column, rendered by ds/SeverityIcon with a visible label.
    this.severityCells = this.root.locator(`#${VULNERABILITIES_TABLE}-body ${DATA_ROW} td:first-child`);

    // ds/FilterDropdown never renders the `id` it is given: it kebab-cases it and emits
    // `auto-complete-<kebab>` on the trigger <button> instead, which is why the fallback
    // is not the id VulnerabilityTable passes. Same rule produced the working
    // #auto-complete-vm-packages-type in tests/VM. Both rungs scoped to the listing root.
    this.severityFilter = this.root
      .getByRole("button", { name: /Severity/ })
      .or(this.root.locator("#auto-complete-vulnerabilities-table-severity"))
      .first();
    this.downloadBtn = this.root
      .getByRole("button", { name: "Download" })
      .or(this.root.locator(`#${VULNERABILITIES_TABLE}-download`))
      .first();

    this.openFindingsNote = this.root.getByText("Open findings only", { exact: true });
  }

  // CustomTable's `showUpdatedEmptyData` branch renders a fixed <EmptyData heading='All good
  // here!'>, forwarding neither the table id nor the emptyHeading VulnerabilityTable supplies —
  // so "No open vulnerabilities" never reaches the DOM and there is no #<id>-no-data node.
  // Same finding as emptyIn() in tests/VM/vmLocators.ts; reported as a product bug in the PR.
  get emptyPanel(): Locator {
    return this.root.getByRole("heading", { name: "All good here!" });
  }

  // Options carry role="option" (ds/FilterDropdown OptionItem) but are matched on their own
  // text, not their accessible name: a severity-filter test in tests/VM was dropped because
  // getByRole("option", { name }) never resolved on this same dropdown. The label is the
  // option's entire text content here — severity options carry no icon, badge or type chip.
  severityOption(severity: string): Locator {
    return this.page.locator('[role="option"]').filter({ hasText: new RegExp(`^\\s*${severity}\\s*$`) }).first();
  }

  // Rows and the empty panel are mutually exclusive, and which one shows depends on what the
  // account holds rather than on the code under test — so every listing assertion waits on
  // whichever arrives. While `loading` is true CustomTable swaps in a skeleton <tbody> with
  // no id, so the id'd body being attached means the response has landed.
  async waitForFindings(timeout = 60000): Promise<void> {
    await this.root
      .locator(`#${VULNERABILITIES_TABLE}-body`)
      .or(this.emptyPanel)
      .first()
      .waitFor({ state: "attached", timeout });
    // An attached tbody is not proof its rows painted, so settle on whichever end state the
    // account produces before anyone counts.
    await this.rows.first().or(this.emptyPanel).first().waitFor({ state: "visible", timeout });
  }

  // Only for a baseline taken before any interaction. It cannot be trusted after one: a
  // refetch swaps the rows inside the same tbody, which stays attached throughout, so
  // waitForFindings() returns on the pre-filter table. Poll severityColumnState() instead.
  async findingCount(): Promise<number> {
    await this.waitForFindings();
    return this.rows.count();
  }

  // The listing's current end state for a severity filter, as a value a caller can poll until
  // it settles. "loading" is the window the count-based branch used to read as "empty" — the
  // old rows are gone, the new ones have not landed, and the tbody never detached to say so.
  async severityColumnState(severity: string): Promise<"empty" | "filtered" | "mixed" | "loading"> {
    // A probe, not an assertion: an account with no findings of this severity is a normal
    // outcome here, so absence has to come back as a value rather than throw.
    if (await this.emptyPanel.isVisible()) return "empty";
    const cells = await this.severityCells.allTextContents();
    if (cells.length === 0) return "loading";
    return cells.every((text) => text.trim() === severity) ? "filtered" : "mixed";
  }

  // Single-select: picking an option commits it and the trigger takes the label. Escape closes
  // the panel afterwards so its Popover backdrop stops swallowing clicks meant for the table.
  async filterBySeverity(severity: string): Promise<void> {
    await this.severityFilter.click();
    await this.severityOption(severity).click();
    await this.page.keyboard.press("Escape");
    await this.waitForFindings();
  }
}
