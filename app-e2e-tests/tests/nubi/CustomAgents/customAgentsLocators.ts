// Not for OSS
import { Page, Locator } from "@playwright/test";
import { CommonLocators } from "../../GlobalLocators";

// Escapes a literal string for use inside a RegExp.
function esc(text: string): string {
  return text.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

// Agents tab of the Nubi AI & Tools modal — app/src/components/llm/ListAgents.jsx.
//
// That component renders zero data-testid (`grep -c data-testid ListAgents.jsx` -> 0),
// and so do AIToolsModal.jsx and CreateAgentNew.jsx, so rung 1 is unavailable
// everywhere here. Controls that carry an accessible name are reached by role
// first; the toolbar and table primitives have none, so their ListingLayout /
// CustomTable ids are the highest rung available for them and are used as the
// primary with a container-scoped fallback.
//
// The agent form controls, the row menu items and the CRUD toasts already live in
// NubiLocators (tests/nubi/nubiLocators.ts) and are deliberately NOT redeclared
// here — the spec holds both classes and takes each control from its owner.
export class CustomAgentsLocators extends CommonLocators {
  readonly listingBox: Locator;
  readonly table: Locator;
  readonly tableBody: Locator;
  readonly nameCells: Locator;
  readonly emptyState: Locator;

  readonly agentTypeFilter: Locator;

  readonly createAgentModal: Locator;
  readonly editAgentModal: Locator;
  readonly agentModalCancelBtn: Locator;

  readonly nameFieldError: Locator;
  readonly descriptionFieldError: Locator;
  readonly requiredFieldsToast: Locator;

  constructor(page: Page) {
    super(page);

    // Deliberately id-only: the listing is an unlabelled ListingLayout Box, so every
    // wider match (a bare div, the modal body) would scope the toolbar and table
    // locators below to the whole dialog instead of to the agents listing.
    this.listingBox = page.locator("#all-agents");
    this.table = page.locator("#agents").or(this.listingBox.getByRole("table")).first();
    this.tableBody = page.locator("#agents-body").or(this.table.locator("tbody")).first();
    // First cell of each data row. CustomTable renders no per-row id, so the column
    // position is the only handle on the Name cell; it is read through columnCellsAt
    // rather than duplicated here so a header reorder moves both together.
    this.nameCells = this.tableBody.locator("tr td:first-child");
    // CustomTable delegates its zero-row state to EmptyData, which ids the heading
    // `<table id>-no-data`; the fallback narrows by that heading's own text.
    this.emptyState = page
      .locator("#agents-no-data")
      .or(this.listingBox.getByRole("heading", { name: "No Data Available" }))
      .first();

    // ds/FilterDropdown renders its trigger as `auto-complete-<the id prop>`, not the
    // id the call site passes — see FilterDropdown.jsx's inputId. The trigger's text is
    // "<label><selected value>", so role+name is not stable; the id is.
    this.agentTypeFilter = page
      .locator("#auto-complete-agent-type-filter")
      .or(this.listingBox.getByRole("button", { name: /Agent Type/ }))
      .first();

    // Not getByRole("dialog", { name }): ds/Modal hardcodes aria-labelledby="alert-dialog-title"
    // (Modal.tsx:300) and stamps that same static id on every modal's title (:357). The agent
    // form opens while the Settings modal is still mounted, so two elements carry that id, the
    // IDREF resolves to the first in document order — the Settings title — and the agent dialog
    // never has "Add Agent" as its accessible name. Match on the title element each dialog
    // actually contains instead; the `has:` filter is scoped per candidate dialog, so the
    // duplicated id is harmless there.
    this.createAgentModal = this.dialogTitled("Add Agent");
    this.editAgentModal = this.dialogTitled("Edit Agent");
    // Scoped to the agent dialog rather than reusing CommonLocators' page-level
    // cancelBtn: the delete confirmation also renders a Cancel, and the two are on
    // screen together during the delete flow.
    this.agentModalCancelBtn = this.createAgentModal
      .getByRole("button", { name: "Cancel" })
      .or(this.editAgentModal.getByRole("button", { name: "Cancel" }))
      .first();

    // FormComponents derives an unset field id as `field-for-<lowercased label>`, and
    // ds/Input renders its message at `<id>-error` with role=alert. Name is a
    // textfield so DS Input owns the message; Description is a textarea, which gets a
    // boolean error only, so FormField prints its text in a plain Typography — hence
    // the text match rather than an -error id for that one.
    this.nameFieldError = page
      .locator("#field-for-name-error")
      .or(this.createAgentModal.getByRole("alert").filter({ hasText: /Agent name already exists|Name is required/ }))
      .first();
    this.descriptionFieldError = this.createAgentModal.getByText("Description cannot be empty.").first();
    // handleSubmit builds this toast from the previous render's `errors`, so on a
    // first submit the per-field bullet lines are still empty and only the heading
    // line is posted — matching the heading is what makes this assertion hold.
    this.requiredFieldsToast = page.getByText(/Please fill the following fields:/).first();
  }

  // The dropdown panel is portaled to body level, so options are page-scoped.
  // Takes a RegExp because the system-agent option is labelled with the tenant's
  // own branding ("<baseTitle> System Agent") and is not a fixed string.
  filterOption(label: RegExp): Locator {
    return this.page
      .getByRole("option", { name: label })
      .or(this.page.locator('[role="option"]').filter({ hasText: label }))
      .first();
  }

  // The dialog whose OWN title element reads `title`. See the constructor for why the
  // accessible name cannot be used here.
  private dialogTitled(title: string): Locator {
    return this.page
      .locator('[role="dialog"]')
      .filter({ has: this.page.locator("#alert-dialog-title").filter({ hasText: title }) })
      .first();
  }

  // Deliberately a contains match, not `^name$`: the Name cell stacks the avatar initial
  // and a type chip ("<tenant> System Agent" / "User Created Agent") around the name
  // (ListAgents.jsx:554-574), and `hasText` tests the cell's whole normalized text, so an
  // anchored pattern matches nothing. Use this for presence and absence only — anywhere
  // an exact name matters, count through listedAgentNames(), which picks the cell's name
  // line. Both must read a name the same way or an assertion can fail on a row that is there.
  nameCellContaining(name: string): Locator {
    return this.nameCells.filter({ hasText: new RegExp(esc(name)) });
  }

  nameCellAt(index: number): Locator {
    return this.nameCells.nth(index);
  }

  // ds/ThreeDotsMenu renders its trigger with aria-label "More actions". Scoped to
  // the row: NubiLocators' agentMoreActionsBtn is page-level and would resolve to
  // whichever row happens to come first in the table.
  rowMenuTrigger(row: Locator): Locator {
    return row.getByRole("button", { name: "More actions" }).or(row.locator("#three-dot-menu")).first();
  }

  // Position of a column by its header text, so cell reads survive a column being
  // added ahead of the one under test.
  async columnIndex(headerName: string): Promise<number> {
    const headers = await this.table.locator("thead th").allInnerTexts();
    const index = headers.findIndex((header) => header.trim().startsWith(headerName));
    if (index < 0) {
      throw new Error(`Column "${headerName}" is not in the agents table — headers were: ${headers.join(" | ")}`);
    }
    return index;
  }

  columnCellsAt(index: number): Locator {
    return this.tableBody.locator(`tr td:nth-child(${index + 1})`);
  }
}
