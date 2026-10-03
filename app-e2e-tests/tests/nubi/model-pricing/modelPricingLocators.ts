// Not for OSS
import { Page, Locator, expect } from "@playwright/test";
import { CommonLocators } from "../../GlobalLocators";

// Nubi > Settings > Model Pricing (app/src/components/llm/ModelPricingTab.jsx).
// The tab renders exactly four data-testids — add-model-price, the per-row
// edit-price-*/remove-price-* pair and confirm-remove-price — so those are the
// primary for the controls that carry them. Everything else on the surface is a
// ds primitive given an explicit id (ListingLayout, CustomTable, SearchInput,
// FilterDropdown), which is rung 3, with a role fallback scoped to the same
// container wherever the accessible name is unambiguous.
export class ModelPricingLocators extends CommonLocators {
  readonly budgetsLimitsTab: Locator;
  readonly modelPricingToggle: Locator;
  readonly usageLimitsToggle: Locator;
  readonly listingCard: Locator;
  readonly table: Locator;
  readonly tableBody: Locator;
  readonly rateUnitCaption: Locator;
  readonly readOnlyBanner: Locator;
  readonly searchInput: Locator;
  readonly providerFilterTrigger: Locator;
  readonly sourceFilterTrigger: Locator;
  readonly addPriceBtn: Locator;
  readonly noMatchEmptyState: Locator;
  readonly priceFormDialog: Locator;
  readonly addPriceDialog: Locator;
  readonly providerInput: Locator;
  readonly modelInput: Locator;
  readonly inputRateInput: Locator;
  readonly outputRateInput: Locator;
  readonly thresholdInput: Locator;
  readonly formSaveBtn: Locator;
  readonly formCancelBtn: Locator;
  readonly removeDialog: Locator;
  readonly confirmRemoveBtn: Locator;
  readonly notificationsRegion: Locator;

  constructor(page: Page) {
    super(page);

    // The panel entry point and its AI & Tools button are not redeclared here —
    // tests/nubi/nubiLocators.ts already owns them, and openModelPricingTab()
    // drives that class to get into the modal.
    //
    // Model Pricing now lives inside the "Budgets & Limits" top-level tab
    // (AIToolsModal.jsx / BudgetsAndLimitsAdminTab.jsx), as an internal
    // ToggleGroup option alongside "Usage & Limits" — role=radio, not role=tab.
    // The top-level tab still renders through shared/navigation/Tabs (MUI Tabs),
    // so it is a real role=tab even while scrolled out of view.
    this.budgetsLimitsTab = page.getByRole("tab", { name: "Budgets & Limits" });
    this.modelPricingToggle = page.getByRole("radio", { name: "Model Pricing" });
    this.usageLimitsToggle = page.getByRole("radio", { name: "Usage & Limits" });

    // ListingLayout puts its `id` on the wrapping DS Card, so this is the whole
    // listing: toolbar, table and empty state. Deliberately id-only — the Card
    // is a plain styled div with no role, and it is what every locator below
    // scopes to, so a wider fallback here would widen all of them.
    this.listingCard = page.locator("#model-pricing-list");

    // CustomTable renders id={id} on the <table> and `${id}-body` on its tbody.
    this.table = page.locator("#model-pricing").or(this.listingCard.getByRole("table")).first();
    this.tableBody = page.locator("#model-pricing-body").or(this.table.locator("tbody")).first();

    // The units caption is the tab's own copy and only renders once rows exist.
    this.rateUnitCaption = this.listingCard.getByText("Rates are USD per 1M tokens.", { exact: true }).first();

    // Shown instead of the Add/Edit controls when the signed-in user is not a
    // tenant admin, so an assertion on it explains a missing Add price button.
    this.readOnlyBanner = page.getByText(/Pricing is read-only here/).first();

    this.searchInput = page.locator("#pricing-model-search").or(this.listingCard.getByPlaceholder("Filter models…")).first();

    // FilterDropdown renders its trigger as <button id={`auto-complete-${kebab(id)}`}>;
    // the ids passed in ModelPricingTab kebab to themselves.
    this.providerFilterTrigger = page
      .locator("#auto-complete-pricing-provider-filter")
      .or(this.listingCard.getByRole("button", { name: /^Provider/ }))
      .first();
    this.sourceFilterTrigger = page
      .locator("#auto-complete-pricing-source-filter")
      .or(this.listingCard.getByRole("button", { name: /^Source/ }))
      .first();

    this.addPriceBtn = page.getByTestId("add-model-price").or(this.listingCard.getByRole("button", { name: "Add price" })).first();

    // ds/EmptyState renders role='status'; scoped to the listing because the
    // toast region uses the same role.
    this.noMatchEmptyState = this.listingCard
      .getByRole("status")
      .filter({ hasText: "No models match these filters" })
      .or(this.listingCard.getByText("No models match these filters", { exact: true }))
      .first();

    // ds/Modal portals to the body, so the pricing dialog is a DOM sibling of
    // the Settings dialog rather than a descendant. Both stamp the same
    // #alert-dialog-title id, hence the filter on body text. This caption is
    // rendered by the add and the edit dialog alike, so it matches whichever
    // of the two is open; the listing's own caption is a different string.
    this.priceFormDialog = page.getByRole("dialog").filter({ hasText: "apply to every account in this tenant" }).first();
    this.addPriceDialog = page.getByRole("dialog").filter({ hasText: "Add model price" }).first();

    // ds/Input wires a real <label htmlFor>, so the form fields have accessible
    // names. The rate fields are type='number', which is role=spinbutton, not
    // textbox. exact:true separates "Input rate" from "Input rate above threshold".
    // Provider and Model render only in add mode.
    this.providerInput = this.priceFormDialog.getByRole("textbox", { name: "Provider", exact: true });
    this.modelInput = this.priceFormDialog.getByRole("textbox", { name: "Model", exact: true });
    this.inputRateInput = this.priceFormDialog.getByRole("spinbutton", { name: "Input rate", exact: true });
    this.outputRateInput = this.priceFormDialog.getByRole("spinbutton", { name: "Output rate", exact: true });
    this.thresholdInput = this.priceFormDialog.getByRole("spinbutton", { name: "Threshold (prompt tokens)", exact: true });

    this.formSaveBtn = this.priceFormDialog.getByRole("button", { name: "Save", exact: true });
    this.formCancelBtn = this.priceFormDialog.getByRole("button", { name: "Cancel", exact: true });

    // Substring rather than an anchored regex: the dialog's text content starts
    // with the modal chrome, not with the title.
    this.removeDialog = page.getByRole("dialog").filter({ hasText: "Remove override" }).first();
    this.confirmRemoveBtn = page
      .getByTestId("confirm-remove-price")
      .or(this.removeDialog.getByRole("button", { name: "Remove", exact: true }))
      .first();

    // SnackbarComponent mounts at the app root (_app.tsx:97), so it is a sibling
    // of the Settings dialog's portal, and while that dialog is open MUI marks
    // it aria-hidden. That removes it from the accessibility tree getByRole
    // queries, so a role-based match on the toast finds nothing even though the
    // element is in the DOM. Matched by attribute instead, which is DOM-based.
    this.notificationsRegion = page.locator('[aria-label="Notifications"]');
  }

  // One price row, matched inside the table body so a model id that also appears
  // in an open dialog or a toast cannot satisfy it.
  row(model: string): Locator {
    return this.tableBody
      .getByRole("row")
      .filter({ hasText: model })
      .or(this.tableBody.locator("tr").filter({ hasText: model }))
      .first();
  }

  // Per-row testid is `edit-price-${provider}-${model}`, so the row's own Edit
  // control is reachable without positional indexing.
  editRowBtn(provider: string, model: string): Locator {
    return this.page
      .getByTestId(`edit-price-${provider}-${model}`)
      .or(this.row(model).getByRole("button", { name: /^(Edit|Override)$/ }))
      .first();
  }

  removeRowBtn(provider: string, model: string): Locator {
    return this.page
      .getByTestId(`remove-price-${provider}-${model}`)
      .or(this.row(model).getByRole("button", { name: "Remove", exact: true }))
      .first();
  }

  // Every "Built-in" source chip currently rendered. ds/Chip is a plain span
  // when it is not interactive, so this is a scoped text match by necessity.
  builtInSourceChips(): Locator {
    return this.tableBody.getByText("Built-in", { exact: true });
  }

  customRateSourceChips(): Locator {
    return this.tableBody.getByText("Custom rate", { exact: true });
  }

  // Toast text, kept inside the notifications region so a matching string
  // elsewhere on the page cannot pass for a toast. The fallback is an attribute
  // CSS match on the toast's own role rather than getByRole, for the
  // aria-hidden reason above; the text filter is what keeps it off the
  // listing's role=status empty state.
  toastWithText(text: string | RegExp): Locator {
    return this.notificationsRegion
      .getByText(text)
      .or(this.page.locator('[role="status"], [role="alert"]').filter({ hasText: text }))
      .first();
  }

  filterOption(label: string): Locator {
    return this.page
      .locator('[role="option"]:visible')
      .filter({ hasText: new RegExp(`^${label.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}$`) })
      .first();
  }

  // Opens a toolbar filter and commits `label`. FilterDropdown only renders its
  // search box above eight options, so the wait is on the options themselves.
  async chooseFilter(trigger: Locator, label: string): Promise<void> {
    await trigger.click();
    await this.page.locator('[role="option"]:visible').first().waitFor({ state: "visible", timeout: 15000 });
    await this.filterOption(label).click();
    // The trigger shows its label plus the committed value, so this is the
    // signal that the selection landed rather than a fixed pause.
    await expect(trigger).toContainText(label, { timeout: 15000 });
  }
}
