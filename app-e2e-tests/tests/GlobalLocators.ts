import { Page, Locator } from "@playwright/test";

export class CommonLocators {
  protected page: Page;

  readonly homeBtn: Locator;
  readonly adminBtn: Locator;
  readonly saveBtn: Locator;
  readonly gitlabsavebutton: Locator;
  readonly servicenowsavebutton: Locator;
  readonly submitBtn: Locator;
  readonly cancelBtn: Locator;
  readonly TroubleshootBtn: Locator;
  readonly OptimizeBtn: Locator;
  readonly ClusterBtn: Locator;

  constructor(page: Page) {
    this.page = page;

    this.homeBtn = page.locator("#home-sidenavbutton");
    this.adminBtn = page.locator("#admin-sidenav");
    this.saveBtn = page.locator("#create-integration-acc");
    this.gitlabsavebutton = page.locator("#create-gitlab-acc");
    this.servicenowsavebutton = page.locator("#create-servicenow-acc");
    this.submitBtn = page.locator("#submit");
    this.cancelBtn = page.getByRole("button", { name: "Cancel" });
    this.OptimizeBtn = page.locator("#optimize-sidenavbutton");
    this.TroubleshootBtn = page.locator("#troubleshoot-sidenavbutton");
    this.ClusterBtn = page.locator("#clusters-sidenavbutton");
  }

  getExactText(text: string): Locator {
    return this.page.getByText(text, { exact: true });
  }

  getOption(name: string): Locator {
    return this.page.getByRole("option", { name });
  }
}
