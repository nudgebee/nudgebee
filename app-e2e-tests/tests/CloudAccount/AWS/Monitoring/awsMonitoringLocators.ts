// Not for OSS
import { Page, Locator, expect } from "@playwright/test";
import { AWSLocators } from "../AWSLocators";

// Table ids the module hands to CustomTable / CloudLogsTable. CustomTable renders its
// body as <tbody id={`${id}-body`}> (CustomTable.jsx:858) and only when it holds rows,
// so a row count of 0 is the no-data state rather than a missing element.
export const ALERT_TABLE = "cloudMgr";
export const LOGS_TABLE = "cloudLogsViewerTable";

// Alert Manager toolbar filter ids, as passed in CloudAccountAlertManager.tsx:310-349.
// ds/FilterDropdown renders its trigger as <button id={`auto-complete-${toKebabCase(id)}`}>
// (FilterDropdown.jsx:1043-1050); these ids contain no spaces, so they kebab to themselves.
export const ALERT_FILTER = {
  category: "cloud-alert-filter-category",
  severity: "cloud-alert-filter-severity",
  source: "cloud-alert-filter-source",
  status: "cloud-alert-filter-status",
} as const;

export const ALERT_FILTER_LABEL: Record<keyof typeof ALERT_FILTER, string> = {
  category: "Category",
  severity: "Severity",
  source: "Source",
  status: "Status",
};

// Status is the only Alert Manager filter whose options are hardcoded in the component
// (CloudAccountAlertManager.tsx:343) instead of fetched per account, so it is the one
// filter that offers the same two values on every environment.
export const ALERT_STATUS_OPTIONS = ["Enabled", "Disabled"] as const;

// Zero-based column indices, from the headers array in CloudAccountAlertManager.tsx:379-387.
export const ALERT_COLUMN = { name: 0, category: 1, source: 2, severity: 3, status: 4 } as const;

// The seven column headers the alert listing declares, in order.
export const ALERT_HEADERS = ["Name", "Category", "Source", "Severity", "Status", "Configured Actions"] as const;

// CloudLogsQueryPanel.tsx:34 — what the AWS query textarea is prefilled with on mount.
export const AWS_DEFAULT_LOG_QUERY = "fields @timestamp, @message | sort @timestamp desc";

// CloudLogsViewer.tsx:72-78 — the Limit filter's fixed option list.
export const LOG_LIMIT_OPTIONS = ["50", "100", "200", "500", "1000"] as const;

function escapeForRegex(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

export class AwsMonitoringLocators extends AWSLocators {
  // Panel roots. Each is the ListingLayout id its sub-tab renders, so "which sub-tab is
  // mounted" is observable as a distinct node rather than as a restyle of a shared one.
  readonly alertManagerRoot: Locator;
  readonly cloudLogsRoot: Locator;
  readonly cloudMetricsRoot: Locator;

  readonly alertNameSearch: Locator;
  readonly logsRunQueryBtn: Locator;
  readonly logsQueryTextarea: Locator;
  readonly metricsRunQueryBtn: Locator;

  constructor(page: Page) {
    super(page);

    // Deliberately id-only, with no .or() fallback.
    //
    // None of the five components behind this module renders a data-testid (verified:
    // `grep -c data-testid` returns 0 for CloudAccountAlertManager.tsx, CloudLogsViewer.tsx,
    // CloudLogsQueryPanel.tsx, CloudMetricsViewer.tsx and CloudMetricsQueryPanel.tsx), and a
    // ListingLayout root is a plain Box with no role, accessible name or unique text of its
    // own — so rung 3 is the highest one available here.
    //
    // A wider fallback would be actively unsafe rather than merely slower: these three roots
    // are what the navigation test uses to prove one sub-tab replaced another, so a structural
    // match that also resolved against a sibling panel would report a switch that never
    // happened. No fallback beats a wrong one.
    this.alertManagerRoot = page.locator("#cloud-alert-manager-list-box");
    this.cloudLogsRoot = page.locator("#cloud-logs-viewer");
    this.cloudMetricsRoot = page.locator("#cloud-metrics-viewer");

    // ds/SearchInput forwards its id straight to the native input (SearchInput.jsx:86)
    // and passes its `label` through as the placeholder (SearchInput.jsx:89), so the id
    // is the field itself and the placeholder backs it up, scoped to the same panel.
    this.alertNameSearch = this.alertManagerRoot
      .locator("#cloud-alert-name-search")
      .or(this.alertManagerRoot.getByPlaceholder("Search By Name"))
      .first();

    // ds/Button puts id on the button element (Button.tsx:314) and the button carries
    // its own visible text, so the role fallback is scoped to the same panel root.
    this.logsRunQueryBtn = this.cloudLogsRoot
      .locator("#cloud-logs-run")
      .or(this.cloudLogsRoot.getByRole("button", { name: "Run Query" }))
      .first();

    this.metricsRunQueryBtn = this.cloudMetricsRoot
      .locator("#cloud-metrics-run")
      .or(this.cloudMetricsRoot.getByRole("button", { name: "Run Query" }))
      .first();

    // ds/Input forwards id to the native textarea (Input.tsx:71,314). The placeholder is
    // the provider-specific string CloudLogsQueryPanel.tsx:238 sets for AWS.
    this.logsQueryTextarea = this.cloudLogsRoot
      .locator("#cloud-logs-aws-query")
      .or(this.cloudLogsRoot.getByPlaceholder("CloudWatch Insights Query"))
      .first();
  }

  // ── Alert Manager ───────────────────────────────────────────────────────────

  alertFilterTrigger(key: keyof typeof ALERT_FILTER): Locator {
    return this.page
      .locator(`#auto-complete-${ALERT_FILTER[key]}`)
      .or(this.alertManagerRoot.getByRole("button", { name: new RegExp(`^${escapeForRegex(ALERT_FILTER_LABEL[key])}`) }))
      .first();
  }

  // Data rows of the alert listing. showExpandable={false} and no `expandable` prop
  // means CustomTable emits exactly one <tr> per record (CustomTable.jsx:486-499 renders
  // the second, collapse row only when isExpandable), so this count is the record count.
  get alertRows(): Locator {
    return this.page.locator(`#${ALERT_TABLE}-body tr`);
  }

  alertCell(rowIndex: number, column: keyof typeof ALERT_COLUMN): Locator {
    return this.alertRows.nth(rowIndex).locator("td").nth(ALERT_COLUMN[column]);
  }

  // One column's cell across every data row, in row order. Uses nth-child rather than
  // .locator("td").nth(n), which would index into the whole table's cells instead of
  // each row's. ALERT_COLUMN is zero-based; nth-child is 1-based.
  alertColumnCells(column: keyof typeof ALERT_COLUMN): Locator {
    return this.alertRows.locator(`td:nth-child(${ALERT_COLUMN[column] + 1})`);
  }

  // The row's three-dot trigger. ds/ThreeDotsMenu gives it aria-label='More actions'
  // (ThreeDotsMenu.jsx:99), so role leads here; its id is not unique across rows, which
  // is why the id fallback is scoped to the row rather than to the page.
  alertRowMenuTrigger(rowIndex: number): Locator {
    const row = this.alertRows.nth(rowIndex);
    return row.getByRole("button", { name: "More actions" }).or(row.locator("#three-dot-menu")).first();
  }

  // ds/DropdownMenu renders items as role='menuitem' carrying the caller's item id
  // (DropdownMenu.tsx:268,416 via Overlay.tsx:114,288). ThreeDotsMenu passes
  // keepMounted={true}, so every closed row menu stays in the DOM — :visible is what
  // narrows this to the one menu that is actually open.
  rowMenuItem(id: string, label: RegExp): Locator {
    return this.page
      .locator(`[role="menuitem"]#${id}:visible`)
      .or(this.page.locator('[role="menuitem"]:visible').filter({ hasText: label }))
      .first();
  }

  // ── Confirmation modal ──────────────────────────────────────────────────────

  // ds/Modal titles its dialog with a fixed id (Modal.tsx:357) and renders no testid.
  // The dialog role is MUI Dialog's own contract, so role leads and the title id scopes
  // it to the decision dialog rather than to any other open dialog.
  get confirmDialog(): Locator {
    return this.page.getByRole("dialog").filter({ has: this.page.locator("#alert-dialog-title") }).first();
  }

  get confirmDialogTitle(): Locator {
    return this.page.locator("#alert-dialog-title").or(this.confirmDialog.getByRole("heading")).first();
  }

  // ── Shared ds/FilterDropdown panel ──────────────────────────────────────────

  // Options are role='option' boxes inside the open panel (FilterDropdown.jsx:197-198).
  // Scoped by :visible rather than by the popover's MUI class: only one panel is ever
  // open, and every other dropdown's options are unmounted. Same approach as admin/Audits.
  filterOption(label: string): Locator {
    return this.page
      .locator('[role="option"]:visible')
      .filter({ hasText: new RegExp(`^${escapeForRegex(label)}$`) })
      .first();
  }

  visibleOptions(): Locator {
    return this.page.locator('[role="option"]:visible');
  }

  // The panel's search box, which FilterDropdown renders only above eight options, so
  // callers must treat its absence as an expected outcome rather than a failure.
  filterSearchInput(): Locator {
    return this.page.locator(".MuiPopover-root").getByPlaceholder("Search...").first();
  }

  // Opens `trigger` and commits `label`.
  //
  // Waits on the options rather than on the search box: the box exists only above eight
  // options, so waiting for it would burn a full timeout on every short list. Modelled on
  // the merged admin/Audits chooseFilter, which is the pattern proven against dev — the
  // shared selectDropdownOption in tests/utils/helpers.ts does not fit, as it waits for a
  // role='listbox' that ds/FilterDropdown's Popover never renders.
  async chooseFilterOption(trigger: Locator, label: string): Promise<void> {
    await trigger.click();
    await this.visibleOptions().first().waitFor({ state: "visible", timeout: 20000 });

    // Options and the search box render in the same pass, so by this point its absence
    // is a settled answer and needs no timeout of its own.
    const search = this.filterSearchInput();
    if (await search.isVisible()) {
      await search.fill(label);
    }

    await this.filterOption(label).click();
    // The trigger renders its label plus the committed value, so this is the signal that
    // the selection landed rather than a fixed pause.
    await expect(trigger).toContainText(label);
  }

  // ── Cloud Logs / Cloud Metrics ──────────────────────────────────────────────

  logsFilterTrigger(id: string, label: string): Locator {
    return this.page
      .locator(`#auto-complete-${id}`)
      .or(this.cloudLogsRoot.getByRole("button", { name: new RegExp(`^${escapeForRegex(label)}`) }))
      .first();
  }

  // Both viewers surface a validation refusal through ds/Banner, which renders
  // role='alert' for tone='critical' (Banner.tsx:136,188).
  bannerIn(root: Locator): Locator {
    return root.getByRole("alert").first();
  }

  // ds/EmptyState renders role='status' (EmptyState.tsx:145). Used by the Cloud Logs and
  // Cloud Metrics viewers, which render ds/EmptyState directly — NOT by the alert
  // listing, whose empty state comes from CustomTable and is a different component.
  emptyStateIn(root: Locator): Locator {
    return root.getByRole("status").first();
  }

  // The alert listing's own empty state. CustomTable renders shared/EmptyData rather than
  // ds/EmptyState, so there is no role='status' here — it is an <h2 id={`${id}-no-data`}>
  // (EmptyData.jsx:26) carrying CustomTable's default heading (CustomTable.jsx:564).
  //
  // This doubles as the listing's "settled" signal: renderEmptyState returns null while
  // `loading` is true (CustomTable.jsx:917), so it appears only once a fetch has finished
  // and genuinely returned nothing. That matters because listAlertManager calls setData([])
  // at the start of every fetch, so a bare row count of 0 is also what a refetch in flight
  // looks like.
  get alertEmptyState(): Locator {
    return this.page
      .locator(`#${ALERT_TABLE}-no-data`)
      .or(this.alertManagerRoot.getByRole("heading", { name: "No Data Available" }))
      .first();
  }

  get logRows(): Locator {
    return this.page.locator(`#${LOGS_TABLE}-body tr`);
  }
}
