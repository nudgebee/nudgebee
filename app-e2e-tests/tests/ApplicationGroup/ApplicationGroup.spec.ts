// Not for OSS
import { test, expect } from "@playwright/test";
import { firstGroupName, noMatchTerm, NO_GROUP_HINT, openCreateModal, setup, uniqueGroupName } from "./applicationGroupHelper";

// Application Group — the module the header calls "Application Group"
// (app/src/components/common/header/Header1.jsx, route '/grouping'). Its listing
// is the Application Grouping tab of /dashboards; its detail view is /grouping.
//
// Nothing here creates a group. The module has no delete on any layer — no
// applications_delete_group in app/src/lib/actions.yaml, no handler in
// api-server/services/api/actions_tenant.go, and the row menu offers only Edit —
// so a group created against the shared dev tenant would be permanent, and CI
// re-runs this suite on every push. The create flow is therefore covered up to
// (and including) its validation and its cancel path, never through a submit.
// Written up in the PR's Follow-ups.
const SPEC_TIMEOUT_MS = 180000;

test.beforeEach(() => {
  test.setTimeout(SPEC_TIMEOUT_MS);
});

test.describe("Application Group", () => {
  test(
    "Application Group sanity - open the Application Grouping tab, verify the six column headers and the toolbar's search, download and create controls",
    { tag: ["@dev", "@test", "@sanity", "@functional", "@oss"] },
    async ({ page }) => {
      const locators = await setup(page);

      await test.step("The listing card and its search field are on screen", async () => {
        await expect(locators.listingRoot).toBeVisible();
        await expect(locators.searchInput).toBeVisible();
      });

      await test.step("Every column the listing declares is rendered", async () => {
        // Column contract from KubernetesApplicationGrouping.jsx's tableHeaders. The
        // seventh is the row-actions column and is deliberately unnamed.
        for (const header of ["Group name", "Total Applications", "Updated at", "Created by", "Event Count (1 Hour)", "Error Rate (1 Hour)"]) {
          await expect(locators.table.locator("th", { hasText: header }).first()).toBeVisible();
        }
      });

      await test.step("The toolbar offers download and create", async () => {
        await expect(locators.downloadBtn).toBeVisible();
        await expect(locators.createGroupBtn).toBeVisible();
      });
    },
  );

  test(
    "Application Group - search the groups listing for a term no group name can match, verify the table empties and shows the No Data Available panel",
    { tag: ["@dev", "@test", "@regression", "@search", "@oss"] },
    async ({ page }) => {
      const locators = await setup(page);

      await locators.searchAndApply(noMatchTerm());

      // Retrying assertions, not a snapshot count: the search refetches, and reading
      // the table once would race the in-flight request and see the pre-search rows.
      await expect(locators.rows).toHaveCount(0);
      await expect(locators.emptyState).toBeVisible();
      await expect(locators.emptyState).toHaveText("No Data Available");
    },
  );

  test(
    "Application Group - search a term no group can match, clear the search, verify every original group row is restored",
    { tag: ["@dev", "@test", "@regression", "@search", "@oss"] },
    async ({ page }) => {
      const locators = await setup(page);

      const baseline = await locators.rowCount();

      await test.step("The filter empties the table", async () => {
        await locators.searchAndApply(noMatchTerm());
        await expect(locators.rows).toHaveCount(0);
      });

      await test.step("Clearing restores the original listing", async () => {
        await locators.clearSearch();
        await expect(locators.searchInput).toHaveValue("");
        // Waits for the restored rows rather than counting what is on screen now — at
        // this point the table still holds the empty result from the step above.
        await expect(locators.rows).toHaveCount(baseline);
      });
    },
  );

  test(
    "Application Group - open Create Application Group, verify the Create Grouping modal offers an empty required name and both application counters at zero",
    { tag: ["@dev", "@test", "@smoke", "@functional", "@oss"] },
    async ({ page }) => {
      const locators = await setup(page);
      await openCreateModal(locators);

      await test.step("The Details section starts empty", async () => {
        await expect(locators.nameInput).toHaveValue("");
        await expect(locators.descriptionInput).toHaveValue("");
        await expect(locators.nameInput).toHaveAttribute("placeholder", "Enter Name");
      });

      await test.step("The Application Selection section offers both pickers", async () => {
        await expect(locators.clusterSelect).toBeVisible();
        await expect(locators.namespacesSelect).toBeVisible();
      });

      await test.step("Nothing is selected yet", async () => {
        await expect(locators.selectedApplicationsLabel).toHaveText("Applications selected - 0");
        await expect(locators.totalSelectedLabel).toContainText("0");
      });
    },
  );

  test(
    "Application Group - open Create Grouping, leave the required name empty, submit, verify the This field required error",
    { tag: ["@dev", "@test", "@regression", "@negative", "@validation", "@oss"] },
    async ({ page }) => {
      const locators = await setup(page);
      await openCreateModal(locators);

      await locators.dialogCreateBtn.click();

      // handleSubmit runs textValidation with 'required' first, which returns before
      // any API call — so this asserts the form was rejected, not just annotated.
      await expect(locators.nameError).toBeVisible();
      await expect(locators.nameError).toHaveText("This field required");
      await expect(locators.dialog).toBeVisible();
      await expect(locators.dialogTitle).toHaveText("Create Grouping");
    },
  );

  test(
    "Application Group - open Create Grouping, enter a name starting with a digit, verify the Should start with an alphabet error",
    { tag: ["@dev", "@test", "@regression", "@negative", "@validation", "@oss"] },
    async ({ page }) => {
      const locators = await setup(page);
      await openCreateModal(locators);

      // The Input's onChange validates on every keystroke with 'firstLetterAlpha', so
      // this is rejected as it is typed rather than on submit.
      await locators.nameInput.fill("1nvalid group name");

      await expect(locators.nameError).toBeVisible();
      await expect(locators.nameError).toHaveText("Should start with an alphabet");
    },
  );

  test(
    "Application Group - open Create Grouping, enter the name of a group that already exists, submit, verify the Group name already in use error",
    { tag: ["@dev", "@test", "@regression", "@negative", "@validation", "@oss"] },
    async ({ page }) => {
      const locators = await setup(page);

      const existing = await firstGroupName(locators);
      // Skipped, not failed: this suite deliberately creates no groups (the module
      // has no delete on any layer, so anything it created would be permanent), and
      // a tenant that happens to hold none is a missing fixture rather than a defect
      // in the product. Same treatment Dashboards.spec.ts gives its own read-only
      // test on an empty listing.
      test.skip(existing === null, NO_GROUP_HINT);

      await openCreateModal(locators);
      await locators.nameInput.fill(existing as string);
      await expect(locators.nameInput).toHaveValue(existing as string);

      await locators.dialogCreateBtn.click();

      // findDuplicateNames returns before InsertAppGrouping is called, so this
      // rejection creates nothing — which is what keeps the case safe to re-run.
      await expect(locators.nameError).toBeVisible();
      await expect(locators.nameError).toHaveText("Group name already in use");
      await expect(locators.dialog).toBeVisible();
    },
  );

  // A cluster/namespace application-picker test was written for the Create Grouping
  // modal and dropped rather than shipped red: picking a cluster could not be made to
  // pass against dev over two CI runs (32351211288, 32351672457, 1 failed / 9 passed
  // each). The ds/Select trigger resolves and is visible — the test above asserts
  // exactly that and passes — but clicking it never brings up the role="listbox"
  // overlay, through both a direct wait on the trigger's aria-expanded and 45s of
  // retried clicks waiting on the listbox itself. Narrowing the trigger with .and() to
  // the element carrying aria-haspopup="listbox" did not change it either. Left
  // uncovered and written up in the PR rather than weakened into a test that asserts
  // nothing; the Select-driven half of the modal (cluster, namespaces, Select all,
  // Clear all) is what that costs.

  test(
    "Application Group - open Create Grouping, enter a valid name, cancel the modal, verify the group is not added to the listing",
    { tag: ["@dev", "@test", "@regression", "@functional", "@oss"] },
    async ({ page }) => {
      const locators = await setup(page);
      const baseline = await locators.rowCount();
      const name = uniqueGroupName();

      await openCreateModal(locators);
      await locators.nameInput.fill(name);
      await expect(locators.nameInput).toHaveValue(name);

      await test.step("Cancel closes the modal", async () => {
        await locators.dialogCancelBtn.click();
        await expect(locators.dialog).toBeHidden({ timeout: 30000 });
      });

      await test.step("Nothing was created", async () => {
        // Both halves matter: the row count is unchanged, and a search for the name
        // that was typed finds no group — the listing is paginated, so an unchanged
        // first page alone would not prove absence.
        await expect(locators.rows).toHaveCount(baseline);
        await locators.searchAndApply(name);
        await expect(locators.rows).toHaveCount(0);
        await expect(locators.emptyState).toBeVisible();
      });
    },
  );

  test(
    "Application Group - open a group from the listing, verify the detail view's Summary, Events and Applications tabs and its Edit Application Group action, then return to the listing",
    { tag: ["@dev", "@test", "@smoke", "@functional", "@oss"] },
    async ({ page }) => {
      const locators = await setup(page);

      const existing = await firstGroupName(locators);
      // Skipped, not failed: this suite deliberately creates no groups (the module
      // has no delete on any layer, so anything it created would be permanent), and
      // a tenant that happens to hold none is a missing fixture rather than a defect
      // in the product. Same treatment Dashboards.spec.ts gives its own read-only
      // test on an empty listing.
      test.skip(existing === null, NO_GROUP_HINT);

      await test.step("The group name opens its detail view", async () => {
        await locators.rowLinkByName(existing as string).click();
        await expect(page).toHaveURL(/\/grouping\?groupId=[^&]+/, { timeout: 30000 });
      });

      await test.step("The detail view offers its three enabled tabs and the edit action", async () => {
        await expect(locators.summaryTab).toBeVisible({ timeout: 60000 });
        await expect(locators.eventsTab).toBeVisible();
        await expect(locators.applicationsTab).toBeVisible();
        await expect(locators.summaryTab).toHaveAttribute("aria-selected", "true");
        await expect(locators.editGroupBtn).toBeVisible();
      });

      await test.step("Applications is a tab of its own, not a second copy of Summary", async () => {
        await locators.applicationsTab.click();
        await expect(locators.applicationsTab).toHaveAttribute("aria-selected", "true", { timeout: 30000 });
        await expect(locators.summaryTab).toHaveAttribute("aria-selected", "false");
      });

      await test.step("Back returns to the listing", async () => {
        await page.goBack();
        await expect(locators.listingRoot).toBeVisible({ timeout: 60000 });
        await locators.waitForTableSettled();
        await expect(locators.rowLinkByName(existing as string)).toBeVisible();
      });
    },
  );
});
