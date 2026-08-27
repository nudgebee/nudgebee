// Not for OSS
import { Page, Locator } from "@playwright/test";
import { OptimizeModuleLocators } from "../optimizeModuleLocators";

// Security sub-tabs of the tenant-level Optimize module. SecurityView.tsx picks one of
// four views off `subTab`; the strip itself is AnchorComponent -> Tabs.jsx, which stamps
// each MUI Tab with `id={opt.id}` from the tabOptions in app/src/pages/optimise/index.jsx.
//
// Rung choices below were measured, not assumed — every one of the four sub-views
// renders zero data-testid:
//   KubernetesSecurity.jsx 0   KubernetesCisSecurityV2.jsx 0
//   VmVulnerabilities.tsx  0   CloudPostureView.tsx        0
// So rung 1 does not exist here. Controls with an accessible name take rung 2 and fall
// back to the id; container roots, which have neither a role nor a name, are id-only.

export const SECURITY_SUBTABS = {
  imageScan: { id: "image-scan", fragment: "image-scan", label: "Image Scan" },
  cisScan: { id: "cis-scan", fragment: "cis-scan", label: "CIS Scan" },
  vmVulnerabilities: { id: "vm-vulnerabilities", fragment: "vm-vulnerabilities", label: "VM Vulnerabilities" },
  cloudPosture: { id: "cloud-posture", fragment: "cloud-posture", label: "Cloud Posture" },
} as const;

export type SecuritySubTab = keyof typeof SECURITY_SUBTABS;

// CustomTable ids. Image Scan passes its toggle value straight through as the tableId
// (KubernetesSecurity.jsx `tableId={activeToggleButton}`), so the table under it changes
// id when the view toggle moves — that swap is what the toggle test asserts on.
export const APPS_TABLE = "apps";
export const IMAGES_TABLE = "images";
export const CIS_TABLE = "kubernetesSecurityTable";
// VmVulnerabilities derives its id from the grouping; 'all' is the mount default and the
// only grouping this suite touches, so the ungrouped constant is the one that applies.
export const VM_TABLE = "VM_VULNERABILITIES_TABLE";
export const POSTURE_TABLE = "cloud-posture-rules";

// The heading CustomTable's showUpdatedEmptyData branch renders in place of an id'd one.
export const NO_FINDINGS_HEADING = "All good here!";

export class SecurityLocators extends OptimizeModuleLocators {
  readonly imageScanSubTab: Locator;
  readonly cisScanSubTab: Locator;
  readonly vmVulnerabilitiesSubTab: Locator;
  readonly cloudPostureSubTab: Locator;

  readonly imageScanRoot: Locator;
  readonly imageScanViewToggle: Locator;
  readonly imageScanAppsOption: Locator;
  readonly imageScanImagesOption: Locator;
  readonly imageScanSeverityFilter: Locator;
  readonly imageScanStatusFilter: Locator;
  readonly imageSearch: Locator;

  readonly cisRoot: Locator;
  readonly cisStatusFilter: Locator;

  readonly vmRoot: Locator;
  readonly vmGroupingToggle: Locator;
  readonly vmSeverityFilter: Locator;

  readonly postureRoot: Locator;
  readonly postureSeverityFilter: Locator;

  constructor(page: Page) {
    super(page);

    this.imageScanSubTab = this.subTab("imageScan");
    this.cisScanSubTab = this.subTab("cisScan");
    this.vmVulnerabilitiesSubTab = this.subTab("vmVulnerabilities");
    this.cloudPostureSubTab = this.subTab("cloudPosture");

    // ListingLayout renders `id={id}` on its Card, so the id names the whole view.
    // Neither the Card nor its wrapper carries a role or a name, so there is no wider
    // match to fall back to that would not also match the page body.
    this.imageScanRoot = page.locator("#security-best-practices");

    // ds/ToggleGroup gives the group role="group" + the ariaLabel, and each option
    // role="radio" named by its visible text. Options carry no id of their own, so the
    // fallback is the same option reached by exact text inside the same group.
    this.imageScanViewToggle = this.imageScanRoot.getByRole("group", { name: "Security view" });
    this.imageScanAppsOption = this.toggleOption(this.imageScanViewToggle, "Apps");
    this.imageScanImagesOption = this.toggleOption(this.imageScanViewToggle, "Images");

    this.imageScanSeverityFilter = this.filterTrigger("security-filter-severity", "Severity", this.imageScanRoot);
    this.imageScanStatusFilter = this.filterTrigger("security-filter-status", "Status", this.imageScanRoot);

    // ds/SearchInput forwards the id to the <input> and passes `label` through as the
    // placeholder, so the id (rung 3) outranks the placeholder (rung 4) — same call the
    // Recommendations search makes. Rendered only on the Images and Details views.
    this.imageSearch = page.locator("#security-filter-image").or(this.imageScanRoot.getByPlaceholder(/Image/)).first();

    // Same ListingLayout shape as Image Scan above — an id'd Card with no role and no name,
    // so there is no wider match that would not also match the page body.
    this.cisRoot = page.locator("#best-practices");
    this.cisStatusFilter = this.filterTrigger("cis-filter-status", "Status", this.cisRoot);

    // These two views take an id the sub-tab above them already uses — VmVulnerabilities
    // renders <ListingLayout id='vm-vulnerabilities'> under a tab that is also
    // #vm-vulnerabilities, and CloudPostureView does the same with 'cloud-posture'. A
    // bare #id therefore matches two nodes, so both are qualified by "not the tab".
    // Raised as a product follow-up rather than worked around silently.
    this.vmRoot = page.locator('#vm-vulnerabilities:not([role="tab"])');
    // ds/ToggleGroup puts the ariaLabel on a role="group", so this one has a real name to
    // lead with; the id it also renders is the fallback.
    this.vmGroupingToggle = page
      .getByRole("group", { name: "Group vulnerabilities by" })
      .or(page.locator("#vm-vulnerability-grouping"))
      .first();
    this.vmSeverityFilter = this.filterTrigger("vm-vulnerability-severity", "Severity", this.vmRoot);

    this.postureRoot = page.locator('#cloud-posture:not([role="tab"])');
    this.postureSeverityFilter = this.filterTrigger("cloud-posture-severity", "Severity", this.postureRoot);
  }

  // An empty table renders one of two different EmptyData shapes, and which one is not a
  // property of this module but of each caller's props, so both have to be reachable.
  // CustomTable.jsx renderEmptyState: the default branch is `EmptyData id={id}`, whose
  // heading carries `${id}-no-data`; the showUpdatedEmptyData branch renders a fixed
  // "All good here!" EmptyData and forwards NO id, so no `-no-data` anchor exists at all.
  // The Image Scan tables take the second branch — both KubernetesSecurityApps and
  // KubernetesSecurityImages pass showUpdatedEmptyData={tableData?.length == 0} — which a
  // CI run caught. Scoped to the view so the heading fallback cannot match another table's.
  emptyStateFor(tableId: string, scope: Locator): Locator {
    return scope
      .locator(`#${tableId}-no-data`)
      .or(scope.getByRole("heading", { name: NO_FINDINGS_HEADING, exact: true }))
      .first();
  }

  // Tabs.jsx names each MUI Tab by its visible label, so getByRole is the higher rung.
  // The id fallback is qualified with [role="tab"] because two of the four ids are also
  // taken by the view the tab opens, and .or() resolves in document order.
  private subTab(key: SecuritySubTab): Locator {
    const { id, label } = SECURITY_SUBTABS[key];
    return this.page
      .getByRole("tab", { name: new RegExp(`^${label}`) })
      .or(this.page.locator(`[role="tab"]#${id}`))
      .first();
  }

  private toggleOption(group: Locator, label: string): Locator {
    return group.getByRole("radio", { name: label }).or(group.getByText(label, { exact: true })).first();
  }
}
