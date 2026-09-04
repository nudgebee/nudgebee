// Not for OSS

import { Page, Locator, expect } from "@playwright/test";
import { CommonLocators } from "../../GlobalLocators";
import { registerWelcomeTourAutoDismiss } from "../../utils/helpers";
import {
  FEATURES_TABLE_ID,
  FIELD_ALLOWED_DOMAINS,
  FIELD_CLUSTER_LABEL,
  FIELD_DEFAULT_AUTH_ROLE,
  FIELD_TENANT_NAME,
  CHECKBOX_SELF_ONBOARDING,
  READ_ONLY_BANNER_TEXT,
  SUBTAB_LOGS,
  SUBTAB_TRACES,
  SUBTAB_WEBHOOK,
  TAB_FEATURES,
  TAB_GENERAL,
  TAB_LABEL_MAPPING,
  TABLIST_FEATURE_GROUPS,
  TABLIST_LABEL_MAPPING,
  TABLIST_TENANT_SETTINGS,
  TENANT_SETTINGS_PATH,
  TENANT_SETTINGS_TAB_NAME,
  TRACE_ADVANCED_PANEL_ID,
  TRACE_ADVANCED_TESTID,
} from "./tenantSettingsConstants";

// Page object for Admin -> Tenant Settings (/user-management#tenant-settings),
// whose body is app/src/components/common/settings/TenantSettings.jsx.
//
// Locator note for the whole file: TenantSettings.jsx passes no `data-testid` to
// any ds/Input, ds/Checkbox, ds/Switch or ds/Button it renders, and ds/Input
// derives its DOM id from React.useId() — an unstable value that changes between
// renders and cannot be selected on. What those primitives DO give us is a real
// accessible name: ds/Input renders <label htmlFor={inputId}>, ds/Checkbox and
// ds/Switch forward aria-label onto the native input. So rung 2 (getByRole with
// a name) is the highest rung actually available for the form controls, and it
// is used as the primary throughout rather than a fabricated id.
//
// The two exceptions are the places the app does render a stable handle:
//   - TenantAccountCommonSettings.jsx emits `<idPrefix>-<field>` as BOTH id and
//     data-testid, so the log/trace mapper fields get a rung-1 testid primary.
//   - CustomTable puts the caller's `id` on the <table> ('tenant-features-table')
//     and `${id}-body` on the <tbody>; it renders no data-testid on either, so an
//     id primary there is the correct rung, not a violation.
export class TenantSettingsLocators extends CommonLocators {
  // The top-level Admin tab. AnchorComponent renders every entry as
  // `anchor-tab-${opt.id || opt.name}` and this filter pins no `id`, so the DOM
  // id carries the tab's display name — spaces and all. Written in the
  // [id="..."] form because "#anchor-tab-Tenant Settings" is parsed by CSS as an
  // id plus a descendant selector and matches nothing.
  readonly tenantSettingsTab: Locator;

  // Top-level tab strip inside the tab body.
  readonly tabList: Locator;
  readonly generalTab: Locator;
  readonly labelMappingTab: Locator;
  readonly featuresTab: Locator;

  // General tab.
  readonly tenantNameInput: Locator;
  readonly selfOnboardingCheckbox: Locator;
  readonly allowedDomainsInput: Locator;
  readonly defaultAuthRoleInput: Locator;

  // Label Mapping sub-tabs.
  readonly labelTabList: Locator;
  readonly logsSubTab: Locator;
  readonly tracesSubTab: Locator;
  readonly webhookSubTab: Locator;
  readonly clusterLabelInput: Locator;
  readonly traceAdvancedToggle: Locator;
  readonly traceAdvancedPanel: Locator;

  // Features tab.
  readonly featureGroupTabList: Locator;
  readonly featuresTable: Locator;
  readonly featureRows: Locator;
  readonly flagIdsToggle: Locator;

  // Shown instead of every Save button when the viewer lacks tenants:Write.
  readonly readOnlyBanner: Locator;

  constructor(page: Page) {
    super(page);

    this.tenantSettingsTab = page
      .locator(`[id="anchor-tab-${TENANT_SETTINGS_TAB_NAME}"]`)
      .or(page.getByRole("link", { name: TENANT_SETTINGS_TAB_NAME, exact: true }))
      .first();

    // ds/Tabs renders a MUI Tabs whose tablist carries `aria-label={ariaLabel}`,
    // and each option as a role="tab" named by its text. Every tab locator below
    // is scoped to its own tablist: "General" and "All" are short strings that
    // also appear elsewhere on the page, and an unscoped .or() fallback resolves
    // in document order, which would silently pick the wrong strip.
    this.tabList = page.getByRole("tablist", { name: TABLIST_TENANT_SETTINGS });
    this.generalTab = this.tabList.getByRole("tab", { name: TAB_GENERAL, exact: true });
    this.labelMappingTab = this.tabList.getByRole("tab", { name: TAB_LABEL_MAPPING, exact: true });
    this.featuresTab = this.tabList.getByRole("tab", { name: TAB_FEATURES, exact: true });

    // ds/Input's <label htmlFor> gives the textbox its accessible name. No
    // .or() fallback: the only thing below role+name here is the useId-derived
    // id, which is regenerated per render, so a fallback on it would be wrong
    // rather than merely slower.
    this.tenantNameInput = page.getByRole("textbox", { name: FIELD_TENANT_NAME, exact: true });
    this.allowedDomainsInput = page.getByRole("textbox", { name: FIELD_ALLOWED_DOMAINS, exact: true });
    this.defaultAuthRoleInput = page.getByRole("textbox", { name: FIELD_DEFAULT_AUTH_ROLE, exact: true });
    this.clusterLabelInput = page.getByRole("textbox", { name: FIELD_CLUSTER_LABEL, exact: true });

    // ds/Checkbox forwards aria-label onto its native input; the label text is
    // the accessible name.
    this.selfOnboardingCheckbox = page.getByRole("checkbox", { name: CHECKBOX_SELF_ONBOARDING, exact: true });

    this.labelTabList = page.getByRole("tablist", { name: TABLIST_LABEL_MAPPING });
    this.logsSubTab = this.labelTabList.getByRole("tab", { name: SUBTAB_LOGS, exact: true });
    this.tracesSubTab = this.labelTabList.getByRole("tab", { name: SUBTAB_TRACES, exact: true });
    this.webhookSubTab = this.labelTabList.getByRole("tab", { name: SUBTAB_WEBHOOK, exact: true });

    // The disclosure control is a ds/Button carrying both an id and a
    // data-testid, so testid is the primary and the id is the same-element
    // fallback rather than a wider match.
    this.traceAdvancedToggle = page.getByTestId(TRACE_ADVANCED_TESTID).or(page.locator(`#${TRACE_ADVANCED_TESTID}`)).first();
    // Id-only on purpose: the disclosure panel is an unlabelled <Box> with no
    // role, no accessible name and no testid, so every wider match (a text or
    // structural one) would resolve against the mapper grid above it instead.
    this.traceAdvancedPanel = page.locator(`#${TRACE_ADVANCED_PANEL_ID}`);

    this.featureGroupTabList = page.getByRole("tablist", { name: TABLIST_FEATURE_GROUPS });
    // CustomTable renders no data-testid on the table element itself, so the
    // caller-supplied id is the top available rung; aria-label="table" is set on
    // the same node, which makes the role fallback same-element rather than a
    // page-wide match.
    this.featuresTable = page.locator(`#${FEATURES_TABLE_ID}`).or(page.getByRole("table", { name: "table" })).first();
    // Id-only on purpose: rows are counted, so the locator has to stay pinned to
    // this one <tbody>. A getByRole("row") fallback matches every row on the
    // page — header rows included — and would silently inflate every count.
    this.featureRows = page.locator(`#${FEATURES_TABLE_ID}-body tr`);
    // The eye button toggles its own aria-label between the two states, so both
    // have to be reachable from one locator.
    this.flagIdsToggle = page.getByRole("button", { name: /(Show|Hide) flag ids/ }).first();

    // ds/Banner renders no role, id or testid, so its own copy is the only
    // handle. The string names a specific permission and appears nowhere else on
    // the page.
    this.readOnlyBanner = page.getByText(READ_ONLY_BANNER_TEXT, { exact: true }).first();
  }

  // Opens Admin -> Tenant Settings on the stored auth state, via the hash route
  // the page itself uses.
  async open(): Promise<void> {
    // These specs navigate straight in rather than through doFullLogin(), so the
    // first-login tour overlay is not yet handled on this page and would
    // intercept clicks on the form. Registration is idempotent per page.
    await registerWelcomeTourAutoDismiss(this.page);
    // domcontentloaded, not the default "load": the dashboard's subresources keep
    // the load event pending well past the point the tab is usable on dev. The
    // tab strip below is the real readiness signal — TenantSettings renders a
    // bare <Loader/> until its first fetch resolves, so the strip existing means
    // the tenant attributes and feature catalog have both landed.
    await this.page.goto(TENANT_SETTINGS_PATH, { waitUntil: "domcontentloaded" });
    await this.tabList.waitFor({ state: "visible", timeout: 90000 });
  }

  // One of the three top-level tabs, waiting on the panel content rather than on
  // the click, since ds/Tabs swaps the body in the same commit as the selection.
  async openTab(tab: Locator): Promise<void> {
    await tab.click();
    await this.expectTabSelected(tab);
  }

  // MUI's Tab sets aria-selected on the active tab; unlike the URL it is always
  // in step with the body that is rendered, because this tab strip is component
  // state and writes no hash of its own.
  async expectTabSelected(tab: Locator): Promise<void> {
    await expect(tab).toHaveAttribute("aria-selected", "true", { timeout: 30000 });
  }

  // A label-mapper field, by the `<idPrefix>-<field>` contract that
  // TenantAccountCommonSettings emits as both data-testid and id.
  mapperField(testId: string): Locator {
    return this.page.getByTestId(testId).or(this.page.locator(`#${testId}`)).first();
  }

  // A webhook FilterDropdown, located by the label text it renders.
  // FilterDropdown builds no id, testid or accessible name from its `label`
  // prop, so that text is the only handle the component offers — rung 4 with
  // nothing above it. Each of the three labels is unique on this sub-tab, which
  // is what makes an unscoped text match safe here.
  webhookDropdown(label: string): Locator {
    return this.page.getByText(label, { exact: true }).first();
  }

  // One feature row's on/off Switch. ds/Switch forwards aria-label onto the
  // native input, and FeatureToggleCell sets it to the feature's display name,
  // which is unique per row. MUI renders that input as type="checkbox", so the
  // role is checkbox rather than switch.
  featureToggle(featureName: string): Locator {
    return this.page
      .getByRole("checkbox", { name: featureName, exact: true })
      .or(this.page.locator(`input[type="checkbox"][aria-label="${featureName}"]`))
      .first();
  }

  // A Save button. Every tab and sub-tab carries its own, all wired to the same
  // handleSaveSettings, and none of them renders at all without tenants:Write —
  // so this is scoped to the visible card rather than assumed to exist.
  saveButton(): Locator {
    return this.page.getByRole("button", { name: "Save", exact: true }).first();
  }

  // Whether this viewer holds tenants:Write. The Save button and the read-only
  // banner are mutually exclusive in the markup, so racing them is what
  // distinguishes the two shapes without a fixed wait on either.
  async canEdit(): Promise<boolean> {
    await this.saveButton()
      .or(this.readOnlyBanner)
      .first()
      .waitFor({ state: "visible", timeout: 60000 });
    // Absence is the expected answer for a read-only viewer, which is exactly
    // the branch this probe exists to report.
    return this.saveButton()
      .isVisible()
      .catch(() => false);
  }
}
