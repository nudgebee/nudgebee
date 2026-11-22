// Not for OSS
import { Page, Locator } from "@playwright/test";
import { CommonLocators } from "../../GlobalLocators";

// Nubi > b-Cortex > Knowledge Graph > Settings > Manual Declarations
// (app/src/components/knowledge-graph/KGSettings.jsx, ManualDependenciesSection.jsx,
// ManualDependenciesTable.jsx, EditManualDependencyDialog.jsx, ConfirmDialog.jsx).
// `grep -c data-testid` returns 0 for every one of those files, so nothing on this
// surface is reachable at rung 1. Buttons carry stable visible labels and are taken
// at rung 2; ds/FilterDropdown, ThreeDotsMenu, CustomTable and ds/Modal each stamp
// an id and no role or name, so those are rung 3; the form inputs are unlabelled
// ds/Inputs whose only handle is a placeholder repeated on both sides of the form,
// so they are rung 4 scoped to their own Source or Destination column.

// ManualDependenciesTable's STATUS_DISPLAY labels, one of which every row shows.
export const RESOLUTION_STATUS_LABELS =
  /^(Resolved|Pending|Source unmatched|Dest unmatched|Source ambiguous|Dest ambiguous|Source: too many|Dest: too many|Node inactive|Invalid payload)$/;
export const RELATIONSHIP_LABELS = /^(CALLS|PUBLISHES_TO|SUBSCRIBES_TO)$/;
// ManualDependenciesSection's STATUS_FILTER_OPTIONS, in render order.
export const STATUS_FILTER_LABELS = ["All", "Resolved", "Pending", "Ambiguous", "Unmatched", "Too many matches", "Node inactive"] as const;

function exactText(label: string): RegExp {
  return new RegExp(`^${label.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}$`);
}

export class KgManualDependenciesLocators extends CommonLocators {
  readonly bCortexDialog: Locator;
  readonly knowledgeGraphTab: Locator;
  readonly kgCanvas: Locator;
  readonly kgSettingsBtn: Locator;
  readonly settingsDialog: Locator;
  readonly settingsCloseBtn: Locator;
  readonly coverageTab: Locator;
  readonly manualDeclarationsTab: Locator;
  readonly coverageDescription: Locator;
  readonly manualDepsDescription: Locator;
  readonly statusFilter: Locator;
  readonly statusFilterPanel: Locator;
  readonly statusFilterOptions: Locator;
  readonly importJsonBtn: Locator;
  readonly declareNodeBtn: Locator;
  readonly declareDependencyBtn: Locator;
  readonly nodesTableBody: Locator;
  readonly tableBody: Locator;
  readonly emptyState: Locator;
  readonly pagerFirstPageBtn: Locator;
  readonly pagerNextBtn: Locator;
  readonly declarationDialog: Locator;
  readonly newDeclarationDialog: Locator;
  readonly editDeclarationDialog: Locator;
  readonly sourceColumn: Locator;
  readonly destColumn: Locator;
  readonly sourceKubernetesKindBtn: Locator;
  readonly sourceCloudKindBtn: Locator;
  readonly sourceNameInput: Locator;
  readonly sourceNamespaceInput: Locator;
  readonly sourceClusterInput: Locator;
  readonly sourceResourceIdInput: Locator;
  readonly sourceRegionInput: Locator;
  readonly sourceNodeTypeSelect: Locator;
  readonly destNameInput: Locator;
  readonly relationshipSelect: Locator;
  readonly notesInput: Locator;
  readonly createOrSaveBtn: Locator;
  readonly declarationCancelBtn: Locator;
  readonly confirmDeleteDialog: Locator;
  readonly confirmDeleteBtn: Locator;
  readonly confirmCancelBtn: Locator;
  readonly openListbox: Locator;
  readonly editMenuItem: Locator;
  readonly reresolveMenuItem: Locator;
  readonly deleteMenuItem: Locator;

  constructor(page: Page) {
    super(page);

    // Matched by attribute, not getByRole("dialog"): the moment the Settings
    // dialog or a ds/Select panel opens on top of it MUI marks the b-Cortex
    // dialog aria-hidden, and a role lookup would then report it closed while it
    // is still on screen. Filtered on its own title, and deliberately single-rung:
    // every scoped fallback below hangs off it, so widening it widens them all.
    this.bCortexDialog = page.locator('[role="dialog"]').filter({ hasText: "b-Cortex" }).first();

    // BCortexModal renders its top-level strip through shared/navigation/Tabs (MUI
    // Tabs), and Knowledge Graph's icon is an MUI icon rather than an image file,
    // so its accessible name is the bare label and can be matched exactly.
    this.knowledgeGraphTab = page
      .getByRole("tab", { name: "Knowledge Graph", exact: true })
      .or(this.bCortexDialog.locator('[role="tab"]').filter({ hasText: /^Knowledge Graph$/ }))
      .first();

    // KnowledgeGraph.jsx gives its canvas an id and nothing else — no testid, no
    // role, no name — so this is rung 3 by necessity; it is the "tab mounted" signal.
    this.kgCanvas = this.bCortexDialog.locator("#kg-canvas");

    // Rendered only for a tenant admin (isTenantAdmin() in KnowledgeGraph.jsx).
    this.kgSettingsBtn = this.bCortexDialog
      .getByRole("button", { name: "Settings", exact: true })
      .or(this.bCortexDialog.locator("#kg-settings-btn"))
      .first();

    // ds/Modal portals to <body>, so the Settings dialog is a sibling of the
    // b-Cortex dialog rather than a descendant, and both stamp #alert-dialog-title
    // — hence every dialog below is matched on its own title element.
    this.settingsDialog = this.dialogWithTitle(/^Knowledge Graph$/);
    this.settingsCloseBtn = this.settingsDialog
      .getByRole("button", { name: "Close", exact: true })
      .or(this.settingsDialog.locator("#close-modal-btn"))
      .first();

    // KGSettings' strip icons are image files, and Tabs passes the tab text as the
    // image alt, so each tab's accessible name is its label twice ("Coverage
    // Coverage"); a loose name match reaches it where an exact one would not.
    this.coverageTab = this.settingsDialog
      .getByRole("tab", { name: "Coverage" })
      .or(this.settingsDialog.locator('[role="tab"]').filter({ hasText: /^Coverage$/ }))
      .first();
    this.manualDeclarationsTab = this.settingsDialog
      .getByRole("tab", { name: "Manual Declarations" })
      .or(this.settingsDialog.locator('[role="tab"]').filter({ hasText: /^Manual Declarations$/ }))
      .first();

    // Each tab's guidance paragraph appears exactly once and only while that tab is
    // mounted, which makes it the honest "the tab body rendered" signal. Text has no
    // role and no id here, so rung 4 scoped to the dialog is the highest rung there is.
    this.coverageDescription = this.settingsDialog.getByText(/Choose which cloud accounts and flow sources feed the Knowledge Graph/).first();
    this.manualDepsDescription = this.settingsDialog.getByText(/Declare service-to-service dependencies/).first();

    // ds/FilterDropdown renders no testid and builds its trigger id as
    // `auto-complete-${id}`; the trigger's accessible name is the label plus the
    // current selection, so the id is the half that stays still.
    this.statusFilter = this.settingsDialog
      .locator('[id="auto-complete-manual-deps-status-filter"]')
      .or(this.settingsDialog.getByRole("button", { name: /^Status/ }))
      .first();
    // FilterDropdown keeps disablePortal, so its open panel mounts inline under the
    // trigger's own wrapper — anchoring the panel there is exact, and the only
    // handle the panel offers.
    this.statusFilterPanel = this.statusFilter.locator("xpath=..").locator(".MuiPopover-paper");
    // `[role="option"]` as CSS: FilterDropdown's rows set the attribute but
    // Playwright's role engine resolves them to 0 at runtime (documented in
    // tests/Troubleshoot/KnowledgeGraph/knowledgeGraphLocators.ts).
    this.statusFilterOptions = this.statusFilterPanel.locator('[role="option"]');

    // The tab's shared toolbar. Each button carries an id, and ds/Button renders its
    // SafeIcon as <img alt='add'> / <img alt='upload'>, so the accessible names read
    // "add Declare dependency" and "upload Import JSON" — hence the loose fallback.
    this.declareDependencyBtn = this.settingsDialog
      .locator("#manual-deps-new")
      .or(this.settingsDialog.locator("button").filter({ hasText: /Declare dependency/ }))
      .first();
    this.declareNodeBtn = this.settingsDialog
      .locator("#manual-nodes-new")
      .or(this.settingsDialog.locator("button").filter({ hasText: /Declare node/ }))
      .first();
    this.importJsonBtn = this.settingsDialog
      .locator("#manual-declarations-import-json")
      .or(this.settingsDialog.locator("button").filter({ hasText: /Import JSON/ }))
      .first();

    // CustomTable stamps `${id}-body` on its data tbody only, and
    // ManualDependenciesTable renders its own empty panel instead of the table when
    // there are no rows, so a row count of 0 is meaningful. Id-only on purpose: a
    // `tbody` fallback would settle every wait on the loading skeleton.
    this.tableBody = this.settingsDialog.locator("#manual-dependencies-table-body");
    // The Nodes section above it, built the same way (CustomTable id manual-nodes-table).
    this.nodesTableBody = this.settingsDialog.locator("#manual-nodes-table-body");
    this.emptyState = this.settingsDialog.getByText("No manual dependencies yet", { exact: true }).first();

    // CustomTable paginates client-side past ten rows through MUI Pagination, whose
    // page buttons are named "Go to page N" and, once selected, "page N" with
    // aria-current="true" (MUI's value, not the ARIA-suggested "page"). Under ten
    // rows no pager renders at all.
    this.pagerFirstPageBtn = this.settingsDialog
      .getByRole("button", { name: /^(Go to )?page 1$/ })
      .or(this.settingsDialog.locator('[aria-label="Go to page 1"], [aria-label="page 1"]'))
      .first();
    this.pagerNextBtn = this.settingsDialog
      .getByRole("button", { name: "Go to next page", exact: true })
      .or(this.settingsDialog.locator('[aria-label="Go to next page"]'))
      .first();

    this.declarationDialog = this.dialogWithTitle(/^(New|Edit) Manual Dependency$/);
    this.newDeclarationDialog = this.dialogWithTitle(/^New Manual Dependency$/);
    this.editDeclarationDialog = this.dialogWithTitle(/^Edit Manual Dependency$/);

    // The form is two identical columns headed "Source" and "Destination", and every
    // control inside repeats its placeholder on both sides, so each column is
    // anchored on its own heading and walked up one level — a labelled anchor, not
    // an index chain. Single-rung: the heading is the column's only distinguishing mark.
    this.sourceColumn = this.declarationDialog.getByText("Source", { exact: true }).locator("xpath=..");
    this.destColumn = this.declarationDialog.getByText("Destination", { exact: true }).locator("xpath=..");

    this.sourceKubernetesKindBtn = this.kindBtnOf(this.sourceColumn, "Kubernetes");
    this.sourceCloudKindBtn = this.kindBtnOf(this.sourceColumn, "Cloud");
    this.sourceNameInput = this.inputOf(this.sourceColumn, "e.g. payment-svc");
    this.sourceNamespaceInput = this.inputOf(this.sourceColumn, "e.g. prod");
    this.sourceClusterInput = this.inputOf(this.sourceColumn, "e.g. us-east-1");
    this.sourceResourceIdInput = this.inputOf(this.sourceColumn, /^arn:aws/);
    this.sourceRegionInput = this.inputOf(this.sourceColumn, "us-east-1 | eastus | us-central1");
    this.destNameInput = this.inputOf(this.destColumn, "e.g. payment-svc");

    // ds/Select's trigger is a <button> named by its selected option. Node type
    // opens on K8sService and resets to Database when the kind flips to Cloud;
    // Relationship is always one of three fixed constants — neither name is tenant
    // data, so role wins, with a walk from the field's own label as the fallback.
    this.sourceNodeTypeSelect = this.sourceColumn
      .getByRole("button", { name: /^(K8sService|Database)/ })
      .or(this.selectTriggerByLabel(this.sourceColumn, "Node type"))
      .first();
    this.relationshipSelect = this.declarationDialog
      .getByRole("button", { name: RELATIONSHIP_LABELS })
      .or(this.selectTriggerByLabel(this.declarationDialog, "Relationship"))
      .first();
    this.notesInput = this.inputOf(this.declarationDialog, "Reason for declaration / context for future readers");

    // The primary action reads "Create" in add mode and "Save" in edit mode.
    this.createOrSaveBtn = this.declarationDialog
      .getByRole("button", { name: /^(Create|Save)$/ })
      .or(this.declarationDialog.locator("button").filter({ hasText: /^(Create|Save)$/ }))
      .first();
    this.declarationCancelBtn = this.declarationDialog
      .getByRole("button", { name: "Cancel", exact: true })
      .or(this.declarationDialog.locator("button").filter({ hasText: /^Cancel$/ }))
      .first();

    this.confirmDeleteDialog = this.dialogWithTitle(/^Delete declaration\?$/);
    this.confirmDeleteBtn = this.confirmDeleteDialog
      .getByRole("button", { name: "Delete", exact: true })
      .or(this.confirmDeleteDialog.locator("button").filter({ hasText: /^Delete$/ }))
      .first();
    this.confirmCancelBtn = this.confirmDeleteDialog
      .getByRole("button", { name: "Cancel", exact: true })
      .or(this.confirmDeleteDialog.locator("button").filter({ hasText: /^Cancel$/ }))
      .first();

    // ds/Select's panel is a MUI Menu whose list carries role=listbox, so its
    // presence is the "picker is open" signal and its absence the "closed" one.
    this.openListbox = page.locator('[role="listbox"]:visible');

    this.editMenuItem = this.menuItem("edit", "Edit");
    this.reresolveMenuItem = this.menuItem("reresolve", "Re-resolve");
    this.deleteMenuItem = this.menuItem("delete", "Delete");
  }

  // The dialog whose own ds/Modal title element reads `title`. Matched by attribute
  // rather than getByRole("dialog"): a ds/Select panel opened inside a dialog makes
  // MUI aria-hide that dialog, and a role lookup would then read "closed" while it
  // is still on screen.
  private dialogWithTitle(title: RegExp): Locator {
    return this.page
      .locator('[role="dialog"]')
      .filter({ has: this.page.locator("#alert-dialog-title").filter({ hasText: title }) })
      .first();
  }

  // ds/Input is given no label anywhere on this form, so the placeholder is the only
  // handle a field has, and the same placeholder sits in the twin column — hence no
  // wider fallback: anything broader inside the column would land on another field.
  private inputOf(container: Locator, placeholder: string | RegExp): Locator {
    return container.getByPlaceholder(placeholder).first();
  }

  private kindBtnOf(column: Locator, label: string): Locator {
    return column
      .getByRole("button", { name: label, exact: true })
      .or(column.locator("button").filter({ hasText: exactText(label) }))
      .first();
  }

  // The dialog's FieldLabel is a <p> inside a wrapper Box that sits beside the
  // ds/Select in the same field Box, so walking two levels up from that <p> and
  // taking the field's first button reaches the trigger without depending on the
  // selected option's text. Anchored on the <p> itself, since its wrapper carries
  // the same text and a bare text match could land there instead.
  private selectTriggerByLabel(container: Locator, label: string): Locator {
    return container
      .locator("p")
      .filter({ hasText: new RegExp(`^${label.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}`) })
      .first()
      .locator("xpath=../..")
      .locator("button")
      .first();
  }

  // ThreeDotsMenu keeps every row's menu mounted, so the items exist for every row
  // at once and only the open menu's are visible; each item carries its action id.
  // getByRole("menuitem") matches zero in this app (nested spans), hence the
  // attribute form, with a visible-text fallback.
  private menuItem(id: string, label: string): Locator {
    return this.page
      .locator(`[role="menuitem"]#${id}:visible`)
      .or(this.page.locator('[role="menuitem"]:visible').filter({ hasText: exactText(label) }))
      .first();
  }

  // The selected pager button for page `n`; it exists only while that page is showing.
  pagerCurrentPage(n: number): Locator {
    return this.settingsDialog
      .getByRole("button", { name: `page ${n}`, exact: true })
      .or(this.settingsDialog.locator(`[aria-label="page ${n}"]`))
      .first();
  }

  // Every data row currently rendered in the table body — the current page only.
  rows(): Locator {
    return this.tableBody.getByRole("row").or(this.tableBody.locator("tr"));
  }

  // Rows carry no id; the run-unique service name in the Source → Destination cell
  // is the only value that identifies one.
  declarationRow(name: string): Locator {
    return this.rows().filter({ hasText: name }).first();
  }

  rowMenuBtn(name: string): Locator {
    const row = this.declarationRow(name);
    return row.getByRole("button", { name: "More actions" }).or(row.locator("#three-dot-menu")).first();
  }

  // ds/Label renders as nested spans with no role, so the status and type chips are
  // a scoped exact-text match by necessity.
  rowStatusLabel(name: string): Locator {
    return this.declarationRow(name).getByText(RESOLUTION_STATUS_LABELS).first();
  }

  rowRelationshipLabel(name: string): Locator {
    return this.declarationRow(name).getByText(RELATIONSHIP_LABELS).first();
  }

  // Every status chip in the table, whichever rows are listed.
  allStatusLabels(): Locator {
    return this.tableBody.getByText(RESOLUTION_STATUS_LABELS);
  }

  // One option in the open Status filter panel. Exact text so "All" cannot match
  // any other label that happens to contain it.
  statusOption(label: string): Locator {
    return this.statusFilterOptions.filter({ hasText: exactText(label) }).or(this.statusFilterPanel.getByText(label, { exact: true })).first();
  }

  // One option in whichever ds/Select panel is open; visible-only so a stale,
  // closing panel cannot satisfy it.
  selectOption(label: string): Locator {
    return this.page.locator('[role="option"]:visible').filter({ hasText: exactText(label) }).first();
  }
}
