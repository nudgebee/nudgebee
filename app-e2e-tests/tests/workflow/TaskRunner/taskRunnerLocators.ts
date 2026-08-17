import { Page, Locator } from "@playwright/test";
import { CommonLocators } from "../../GlobalLocators";

// Escapes a literal string for use inside a RegExp.
function esc(text: string): string {
  return text.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

// Every locator here is id-first with a structural fallback behind `.or()`, so
// an id rename in the app degrades to a slower match instead of a red suite.
export class TaskRunnerLocators extends CommonLocators {
  readonly automationSidenavBtn: Locator;
  readonly taskRunnerTab: Locator;
  readonly automationsTab: Locator;
  readonly executionsTab: Locator;

  readonly runnerBox: Locator;
  readonly runnerTitle: Locator;
  readonly runnerIntro: Locator;
  readonly accountSelect: Locator;
  readonly searchInput: Locator;

  readonly categorySummaries: Locator;
  readonly taskRows: Locator;

  readonly noMatchingTasks: Locator;
  readonly noTaskSelected: Locator;
  readonly loadErrorBanner: Locator;

  readonly testActionHeading: Locator;
  readonly testActionEmptyState: Locator;
  readonly runTaskBtn: Locator;
  readonly dryRunBtn: Locator;
  readonly runTaskHelperText: Locator;
  readonly runResultHeading: Locator;
  readonly runResultSection: Locator;
  readonly runResultStatusChip: Locator;

  readonly accountSearchInput: Locator;
  readonly dropdownOptions: Locator;

  constructor(page: Page) {
    super(page);

    this.automationSidenavBtn = page.locator("#auto-pilot-sidenavbutton").or(page.locator('a[href="/automation"]')).first();
    this.automationsTab = page.locator("#anchor-tab-automations").or(page.locator('a[href="/automation#automations"]')).first();
    this.executionsTab = page.locator("#anchor-tab-executions").or(page.locator('a[href="/automation#executions"]')).first();
    this.taskRunnerTab = page.locator("#anchor-tab-task-runner").or(page.locator('a[href="/automation#task-runner"]')).first();

    const introText = "Configure and run an individual automation action against the selected account.";
    this.runnerBox = page
      .locator("#task-runner-box")
      .or(page.locator("div").filter({ hasText: introText }).last())
      .first();
    this.runnerTitle = this.runnerBox.getByText("Task Runner", { exact: true }).first();
    this.runnerIntro = this.runnerBox.getByText(introText);
    this.accountSelect = page
      .locator("#task-runner-account-select")
      .or(this.runnerBox.getByRole("button", { name: "Account", exact: true }))
      .first();
    this.searchInput = page.locator("#task-runner-search-input").or(this.runnerBox.getByPlaceholder("Search actions...")).first();

    this.categorySummaries = this.runnerBox.locator(".MuiAccordionSummary-root").or(this.runnerBox.locator('[id^="panel"][aria-expanded]'));
    // Rows only enter the DOM while their category is expanded, and the
    // accordion is single-selection — at most one category's rows exist.
    this.taskRows = this.runnerBox.locator('[id^="task-runner-task-"]');

    this.noMatchingTasks = this.runnerBox.getByText("No matching tasks", { exact: true });
    this.noTaskSelected = this.runnerBox.getByText("No task selected", { exact: true });
    this.loadErrorBanner = this.runnerBox.getByText("Failed to load tasks", { exact: true });

    this.testActionHeading = this.runnerBox.getByText("Test Action", { exact: true });
    this.testActionEmptyState = this.runnerBox.getByText('Use "Run Task" to execute this task in isolation');
    this.runTaskBtn = this.runnerBox
      .getByRole("button", { name: /^(Run Task|Executing\.\.\.)$/ })
      .or(this.runnerBox.locator("button").filter({ hasText: /^(Run Task|Executing\.\.\.)$/ }))
      .first();
    this.dryRunBtn = this.runnerBox.getByRole("button", { name: /^(Dry Run|Running\.\.\.)$/ });
    this.runTaskHelperText = this.runnerBox.getByText(/Executes only this task in isolation|Individual task execution is not supported/);
    this.runResultHeading = this.runnerBox.getByText("Run Task Result:", { exact: true });
    // Scoped to the result row, not the panel: the parameter form sits in the
    // same panel and still holds the values that were just submitted.
    this.runResultSection = this.runResultHeading.locator("xpath=../..");
    // The result status is a MUI Chip; the per-category counts are ds Chips
    // (plain spans), so this cannot collide with them.
    this.runResultStatusChip = this.runnerBox
      .locator(".MuiChip-label")
      .filter({ hasText: /^(COMPLETED|FAILED|SUCCESS|ERROR)$/ })
      .first();

    // The app renders no id on the picker's own search box, so placeholder is the
    // highest unambiguous rung here.
    this.accountSearchInput = page.getByPlaceholder(/^Search/).first();
    // MUI renders the listbox in a portal at body level, which is why these two
    // are page-scoped rather than scoped to runnerBox like everything above.
    this.dropdownOptions = page.locator('[role="option"]');
  }

  dropdownOption(value: string): Locator {
    return this.getOption(value)
      .or(this.dropdownOptions.filter({ hasText: new RegExp(`^${esc(value)}$`, "i") }))
      .first();
  }

  // Dotted ids (task-runner-task-k8s.cli-btn) parse as a class chain in a CSS
  // `#id` selector, so the attribute form is the only one that matches.
  taskRow(taskType: string): Locator {
    return this.page
      .locator(`[id="task-runner-task-${taskType}-btn"]`)
      .or(this.runnerBox.locator(`[id*="${taskType}"][role="button"]`))
      .first();
  }

  categorySummary(label: string): Locator {
    return this.categorySummaries.filter({ hasText: new RegExp(`^${esc(label)}`) }).first();
  }

  // The per-category task count is a ds Chip (a styled span, not a MUI Chip),
  // so it is read off the end of the header's text.
  async categoryTaskCounts(): Promise<number[]> {
    const headers = await this.categorySummaries.allInnerTexts();
    return headers.map((text) => Number(text.trim().match(/(\d+)\s*$/)?.[1] ?? 0));
  }

  // Label and field wrapper are siblings, so the row is the label's parent.
  // Case-insensitive: the same field can read "Integration Id" or "Integration id".
  fieldLabel(label: string): Locator {
    const pattern = new RegExp(`^${esc(label)}\\s*\\*?$`, "i");
    return this.runnerBox
      .locator("p")
      .filter({ hasText: pattern })
      .first()
      .or(this.runnerBox.locator("span, label").filter({ hasText: pattern }).first())
      .first();
  }

  fieldRow(label: string): Locator {
    return this.fieldLabel(label).locator("xpath=..");
  }

  // Deliberately no widened fallback: a grandparent lookup matches every control
  // in the form and `.or().first()` would silently return the wrong field.
  fieldControl(label: string): Locator {
    return this.fieldRow(label).locator("input, textarea, .cm-content").first();
  }

  // The trigger is sometimes an input and sometimes a button, and the row also
  // holds the Select / Expression mode toggle — so exclude those two.
  fieldDropdownTrigger(label: string): Locator {
    return this.fieldRow(label)
      .locator("input, [role='combobox'], [aria-haspopup='listbox'], button")
      .filter({ hasNotText: /^(Select|\{\{ \}\} Expression)$/ })
      .first();
  }
}
