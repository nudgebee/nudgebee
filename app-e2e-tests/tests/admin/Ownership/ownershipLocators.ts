// Not for OSS

import { Page, Locator } from "@playwright/test";
import { CommonLocators } from "../../GlobalLocators";
import { registerWelcomeTourAutoDismiss } from "../../utils/helpers";
import { LISTING_TITLE } from "./ownershipConstants";

// Page object for Admin -> Ownership (/user-management#ownership).
//
// LOCATOR LADDER — OwnershipRules.jsx, OwnershipRuleModal.jsx and OwnerPicker.jsx
// render ZERO data-testid between them (verified with
// `grep -c data-testid` on all three: 0). So rung 1 does not exist here and the
// primaries below are ids the components do declare, with an accessible-name or
// scoped-text fallback behind .or(). Every fallback is scoped to the same
// container as its primary — .or() resolves in document order, so an unscoped
// fallback would silently match the listing behind the open modal.
export class OwnershipLocators extends CommonLocators {
  // Listing
  readonly ownershipTab: Locator;
  readonly usersTab: Locator;
  readonly listingRoot: Locator;
  readonly listingTitle: Locator;
  readonly addRuleBtn: Locator;
  readonly rulesTable: Locator;

  // Rule modal
  readonly ruleDialog: Locator;
  readonly nameInput: Locator;
  readonly domainSelect: Locator;
  readonly scopeSelect: Locator;
  readonly matchKeyInput: Locator;
  readonly matchValueInput: Locator;
  readonly cloudTagKeyInput: Locator;
  readonly ownerSelect: Locator;
  readonly saveBtn: Locator;
  readonly cancelRuleBtn: Locator;

  // Overlay shared by every ds/Select popup in the modal
  readonly openListbox: Locator;
  readonly listboxOptions: Locator;
  readonly listboxSearch: Locator;

  // Delete confirm
  readonly deleteDialog: Locator;
  readonly deleteConfirmBtn: Locator;

  constructor(page: Page) {
    super(page);

    // AnchorComponent renders each tab as `#anchor-tab-<name>` carrying
    // data-tab-selected — the only selection signal on it (no aria-selected).
    this.ownershipTab = page.locator("#anchor-tab-Ownership").or(page.getByRole("link", { name: "Ownership", exact: true })).first();
    // The sibling tab this suite navigates away to and back from.
    this.usersTab = page.locator("#anchor-tab-Users").or(page.getByRole("link", { name: "Users", exact: true })).first();

    // id-only: ListingLayout's root takes a data-testid prop but OwnershipRules
    // does not pass one, and the wrapper has no role or text of its own — any
    // wider match here would resolve to a layout Box shared with every other tab.
    this.listingRoot = page.locator("#ownership-rules");
    // Scoped to the listing root so the sidebar's own "Ownership" entry cannot match.
    this.listingTitle = this.listingRoot.getByText(LISTING_TITLE, { exact: true }).first();
    this.addRuleBtn = page.locator("#add-rule").or(page.getByRole("button", { name: "Add rule", exact: true })).first();
    // id lands on the <Table> itself (CustomTable passes id={id} to it); the
    // fallback is the same table reached by role, scoped to this listing.
    this.rulesTable = page.locator("#ownership-rules-table").or(this.listingRoot.getByRole("table")).first();

    // ds/Modal is a MUI Dialog, so role=dialog is the accessible container — but
    // it is NOT the only one: the welcome tour is a DS Modal too, so a bare
    // getByRole("dialog") is a strict-mode violation for the window in which the
    // tour is still on screen. Pinned to the dialog that owns the save button.
    this.ruleDialog = page.getByRole("dialog").filter({ has: page.locator("#rule-save") });
    // ds/Input puts the id straight on the <input>. The placeholder fallback is
    // scoped to the dialog; the accessible name is "Name" even though the field
    // is required, because ds/Input's asterisk is aria-hidden.
    this.nameInput = this.ruleDialog
      .locator("#rule-name")
      .or(this.ruleDialog.getByPlaceholder("Enter rule name"))
      .first();
    // ds/Select renders its trigger as <button id={id} aria-haspopup="listbox">.
    // id-only on purpose: the field's <label htmlFor> points at that button, but a
    // label does not name a <button> in the accessibility tree, so getByRole with
    // a name matches zero — and a bare aria-haspopup match would be positional.
    this.domainSelect = this.ruleDialog.locator("#rule-domain");
    this.scopeSelect = this.ruleDialog.locator("#rule-scope");
    // Only rendered on the cloud domain; asserted empty after a domain switch.
    this.cloudTagKeyInput = this.ruleDialog
      .locator("#rule-cloud-tag-key")
      .or(this.ruleDialog.getByPlaceholder("Enter tag key"))
      .first();
    this.matchKeyInput = this.ruleDialog
      .locator("#rule-match-key")
      .or(this.ruleDialog.getByPlaceholder("Enter label key"))
      .first();
    this.matchValueInput = this.ruleDialog
      .locator("#rule-match-value")
      .or(this.ruleDialog.getByPlaceholder("Enter label value"))
      .first();
    // Same ds/Select naming limitation as the two selects above.
    this.ownerSelect = this.ruleDialog.locator("#rule-owner");
    // The submit button's label is "Create" when adding and "Save" when editing,
    // so the fallback accepts either — both scoped to the dialog.
    this.saveBtn = this.ruleDialog
      .locator("#rule-save")
      .or(this.ruleDialog.getByRole("button", { name: /^(Create|Save)$/ }))
      .first();
    this.cancelRuleBtn = this.ruleDialog
      .locator("#rule-cancel")
      .or(this.ruleDialog.getByRole("button", { name: "Cancel", exact: true }))
      .first();

    // Not getByRole("option"): ds/Select opens its popup with disablePortal
    // INSIDE the dialog, and MUI's ModalManager then marks that container
    // aria-hidden, so the accessibility tree exposes no options at all. The CSS
    // role attribute survives. Same trap documented in admin/roles/rolesLocators.
    this.openListbox = page.locator('[role="listbox"]:visible').first();
    this.listboxOptions = page.locator('[role="listbox"] [role="option"]:visible');
    this.listboxSearch = this.openListbox.locator('input[placeholder="Search…"]').first();

    // Pinned by the confirm copy for the same reason as ruleDialog above — the
    // tour modal would otherwise make this ambiguous.
    this.deleteDialog = page.getByRole("dialog").filter({ hasText: "This cannot be undone." });
    // ds/Modal's standard confirm footer renders the confirm control as
    // <Button id='submit'> labelled with confirmText, which is "Delete" here.
    this.deleteConfirmBtn = this.deleteDialog
      .locator("#submit")
      .or(this.deleteDialog.getByRole("button", { name: "Delete", exact: true }))
      .first();
  }

  // Open Admin -> Ownership on the stored auth state, via the hash route the
  // page itself uses.
  async open(): Promise<void> {
    // These specs navigate straight in rather than through doFullLogin(), so the
    // first-login tour overlay is not yet handled on this page and would
    // intercept clicks on the listing. Registration is idempotent per page.
    await registerWelcomeTourAutoDismiss(this.page);
    // domcontentloaded, not the default "load": the dashboard's subresources keep
    // the load event pending well past the point the tab is usable on dev. The
    // listing root below is the real readiness signal.
    await this.page.goto("/user-management#ownership", { waitUntil: "domcontentloaded" });
    await this.listingRoot.waitFor({ state: "visible", timeout: 60000 });
  }

  // One listing row, matched by the rule name in its first cell.
  ruleRow(name: string): Locator {
    return this.rulesTable.locator("tr").filter({ hasText: name }).first();
  }

  // The kebab trigger on a rule's row. ThreeDotsMenu puts the id on its trigger
  // button; the id carries the rule's uuid, so the row-scoped accessible name is
  // the reachable form when only the name is known.
  rowMenuBtn(name: string): Locator {
    return this.ruleRow(name).getByRole("button", { name: "More actions" }).first();
  }

  // A row action inside the open kebab menu. DropdownMenu keeps its items mounted
  // (keepMounted), so :visible is what separates the open menu from every closed
  // one, and getByRole("menuitem") matches zero for the aria-hidden reason above.
  rowMenuItem(action: "edit" | "delete"): Locator {
    return this.page
      .locator(`[role="menuitem"]#${action}:visible`)
      .or(this.page.locator('[role="menuitem"]:visible').filter({ hasText: action === "edit" ? "Edit" : "Delete" }))
      .first();
  }

  // The inline enable/disable toggle on a rule's row. ds/Switch forwards
  // aria-label onto the native checkbox input, and OwnershipRules sets it to
  // `Toggle <rule name>`, which is unique per row.
  enabledToggle(name: string): Locator {
    return this.page
      .getByRole("checkbox", { name: `Toggle ${name}`, exact: true })
      .or(this.page.locator(`input[type="checkbox"][aria-label="Toggle ${name}"]`))
      .first();
  }

  // One option row of whichever ds/Select popup is currently open.
  option(label: string | RegExp): Locator {
    return this.listboxOptions.filter({ hasText: label }).first();
  }
}
