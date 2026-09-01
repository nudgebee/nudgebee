import { test, expect } from "@playwright/test";
import { createDashboard, deleteDashboardIfPresent, setup, uniqueDashboardTitle } from "./dashboardsHelper";
import { waitForGraphQLAndValidate } from "../utils/GraphQLNetworkWatcher";

/**
 * Custom Dashboards (`/dashboards`) — the listing, its authoring entry points
 * and the dashboard viewer.
 *
 * Every test that writes anything names it with a per-run unique title and
 * removes it in a `finally`, so the suite is safe to run twice and safe to run
 * against the shared dev tenant beside somebody else. Nothing pre-existing is
 * opened for edit or deleted.
 */
test.describe("Dashboards", () => {
  test("Dashboard list renders its toolbar, column headers and authoring actions", { tag: ["@dev", "@test", "@sanity", "@functional", "@oss"] }, async ({ page }) => {
    const locators = await setup(page);

    await test.step("The listing card and its search field are on screen", async () => {
      await expect(locators.listingRoot).toBeVisible();
      await expect(locators.searchInput).toBeVisible();
      await expect(locators.searchInput).toHaveAttribute("placeholder", "Search dashboards");
    });

    await test.step("All five columns are present", async () => {
      for (const header of ["Dashboard", "Panels", "Created At", "Updated At", "Action"]) {
        await expect(locators.listingRoot.getByRole("columnheader", { name: header, exact: true })).toBeVisible();
      }
    });

    await test.step("The toolbar offers one authoring button", async () => {
      await expect(locators.newDashboardBtn).toBeVisible();
      await expect(locators.newDashboardBtn).toHaveText("New dashboard");
      // Menu closed: its items are not mounted, which is what makes the toolbar
      // read as one action instead of three competing buttons.
      await expect(locators.templateGalleryBtn).toHaveCount(0);
      await expect(locators.importDashboardBtn).toHaveCount(0);
    });

    await test.step("Its menu holds the three ways to start a dashboard", async () => {
      await locators.openAuthoringMenu();
      await expect(locators.templateGalleryBtn).toHaveText("Use a template");
      await expect(locators.importDashboardBtn).toHaveText("Import dashboard");
      await expect(locators.newBlankDashboardBtn).toHaveText("Blank dashboard");
    });
  });

  test("Sidenav moves between Dashboard List and Application Grouping and back", { tag: ["@dev", "@test", "@regression", "@functional", "@oss"] }, async ({ page }) => {
    const locators = await setup(page);
    await expect(locators.listingRoot).toBeVisible();

    await test.step("Application Grouping replaces the listing", async () => {
      // The sub-items live behind the rail button's hover flyout, and the flyout
      // closes on any stray pointer move — so the hover and the click retry
      // together, the same shape CommonLocators.openInfraSection uses.
      await expect(async () => {
        await locators.dashboardsNavBtn.hover();
        await locators.sidenavDashboardGroups.waitFor({ state: "visible", timeout: 3000 });
        await locators.sidenavDashboardGroups.click();
        await page.waitForURL(/\/dashboards#groups/, { timeout: 5000 });
      }).toPass({ timeout: 30000, intervals: [500, 1000, 2000] });
      await page.mouse.move(0, 0);

      await expect(locators.groupingRoot).toBeVisible({ timeout: 60000 });
      await expect(locators.listingRoot).toHaveCount(0);
    });

    await test.step("Dashboard List brings the listing back", async () => {
      await expect(async () => {
        await locators.dashboardsNavBtn.hover();
        await locators.sidenavDashboardList.waitFor({ state: "visible", timeout: 3000 });
        await locators.sidenavDashboardList.click();
        await page.waitForURL(/\/dashboards#list/, { timeout: 5000 });
      }).toPass({ timeout: 30000, intervals: [500, 1000, 2000] });
      await page.mouse.move(0, 0);

      await locators.waitForListSettled();
      await expect(locators.listingRoot).toBeVisible();
      await expect(locators.groupingRoot).toHaveCount(0);
    });
  });

  test("Search filters the dashboard list, shows the no-data state and restores every row when cleared", { tag: ["@dev", "@test", "@regression", "@search", "@oss"] }, async ({ page }) => {
    const locators = await setup(page);
    const title = uniqueDashboardTitle();

    try {
      // The tenant can legitimately hold no dashboards at all — every spec here
      // removes what it creates — and on an empty list "filter lifted" and
      // "filter still applied" look identical: both show the no-data state. So
      // this test brings its own row rather than asserting against whatever
      // happened to be there.
      await createDashboard(locators, title);
      await locators.backBtn.click();
      const baseline = await locators.waitForListSettled();

      await test.step("A term nothing can match empties the table", async () => {
        await locators.search(`no-such-dashboard-${Date.now()}`);
        await expect(locators.rows).toHaveCount(0);
        await expect(locators.emptyStateHeading).toBeVisible();
      });

      await test.step("A term that matches narrows the table to it", async () => {
        await locators.search(title);
        await expect(locators.emptyStateHeading).toHaveCount(0);
        await expect(locators.rowByTitle(title)).toHaveCount(1);
      });

      await test.step("Clearing the field restores the original rows", async () => {
        await locators.clearSearch();
        await expect(locators.emptyStateHeading).toHaveCount(0);
        // Greater-or-equal, not exactly `baseline`: the suite runs against a
        // shared tenant, so another spec (or another engineer) may have added a
        // dashboard since the count was taken. Going back to at least the
        // original rows is what proves the filter was lifted.
        await expect.poll(() => locators.rows.count()).toBeGreaterThanOrEqual(baseline);
      });
    } finally {
      await deleteDashboardIfPresent(page, locators, title);
    }
  });

  test("Creating a dashboard saves it, confirms with a toast and lists it", { tag: ["@dev", "@test", "@smoke", "@functional", "@oss"] }, async ({ page }, testInfo) => {
    const locators = await setup(page);
    const title = uniqueDashboardTitle();
    const description = "Created by the automated e2e coverage pass.";

    try {
      await test.step("Author and save a new dashboard", async () => {
        await locators.startBlankDashboard();
        await expect(locators.titleInput).toBeVisible();
        await expect(locators.saveBtn).toBeVisible();
        await locators.titleInput.fill(title);
        await locators.descriptionInput.fill(description);

        // Assert the toast inside the watched action — it is transient and gone
        // by the time the watcher returns.
        await waitForGraphQLAndValidate(
          page,
          async () => {
            await locators.saveBtn.click();
            await expect(locators.createdToast).toBeVisible();
          },
          { testName: testInfo.title, operationNames: ["CreateDashboard"] }
        );
      });

      await test.step("The save lands on the stored dashboard", async () => {
        // A create answers with the new id, which the page writes to the URL.
        await expect(page).toHaveURL(/[?&]dashboard=/);
        await expect(locators.dashboardToolbar).toContainText(title);
      });

      await test.step("Back returns to the listing with the dashboard in it", async () => {
        await locators.backBtn.click();
        await locators.waitForListSettled();
        await locators.search(title);
        await expect(locators.rowByTitle(title)).toHaveCount(1);
        await expect(locators.rowByTitle(title)).toContainText(description);
      });
    } finally {
      await deleteDashboardIfPresent(page, locators, title);
    }
  });

  test("Saving a dashboard with no title is rejected and keeps the editor open", { tag: ["@dev", "@test", "@regression", "@negative", "@validation", "@oss"] }, async ({ page }) => {
    const locators = await setup(page);

    await test.step("Open the blank editor", async () => {
      await locators.startBlankDashboard();
      await expect(locators.titleInput).toBeVisible();
      await expect(locators.titleInput).toHaveValue("");
    });

    await test.step("Save with the required title empty is refused", async () => {
      await locators.saveBtn.click();
      await expect(locators.titleRequiredToast).toBeVisible();
      // Still in the editor: nothing was created and the field is waiting.
      await expect(locators.titleInput).toBeVisible();
      await expect(locators.saveBtn).toBeVisible();
      await expect(page).not.toHaveURL(/[?&]dashboard=/);
    });

    await test.step("Discarding leaves the editor with nothing saved", async () => {
      // The rejection toast lands over the toolbar and its notification region
      // swallows the click, so let it age out first. Moving the pointer off is
      // what makes that possible: SnackbarComponent pauses its dismiss timer
      // while the toast is hovered, and Playwright leaves the mouse parked on
      // the Save button the toast just covered — so the toast would hang there
      // for the whole test.
      await page.mouse.move(0, 0);
      await expect(locators.titleRequiredToast).toBeHidden({ timeout: 15000 });
      await locators.discardBtn.click();
      // Nothing was typed and no panel added, so there is no work to lose and
      // DashboardView leaves without asking — `unsavedWork` stays false for an
      // untouched create. Asserting the prompt is absent is what proves that.
      await expect(locators.exitDialogDiscardBtn).toHaveCount(0);
      await locators.waitForListSettled();
      await expect(locators.listingRoot).toBeVisible();
      await expect(locators.titleInput).toHaveCount(0);
    });
  });

  test("Import dashboard rejects malformed JSON and keeps Import disabled", { tag: ["@dev", "@test", "@regression", "@negative", "@validation", "@oss"] }, async ({ page }) => {
    const locators = await setup(page);

    await test.step("The import modal opens with Import unavailable", async () => {
      await locators.openImportModal();
      await expect(locators.dialogTitle).toHaveText("Import dashboard");
      await expect(locators.importJsonEditor).toBeVisible();
      // Nothing pasted yet — there is no dashboard to import.
      await expect(locators.importSubmitBtn).toBeDisabled();
    });

    await test.step("Unparseable JSON is reported and Import stays disabled", async () => {
      await locators.importJsonEditor.fill("{ this is not json");
      await expect(locators.importParseError).toBeVisible();
      await expect(locators.importSubmitBtn).toBeDisabled();
    });

    await test.step("Well-formed JSON that is not a dashboard is rejected too", async () => {
      await locators.importJsonEditor.fill('{ "title": "not a dashboard" }');
      await expect(locators.importParseError).toBeVisible();
      await expect(locators.importParseError).toContainText("definition.panels");
      await expect(locators.importSubmitBtn).toBeDisabled();
    });

    await test.step("Cancel closes the modal and imports nothing", async () => {
      await locators.importCancelBtn.click();
      await expect(locators.importJsonEditor).toHaveCount(0);
      // A successful import toasts "Imported N panels" and navigates straight
      // into the new dashboard — asserting neither happened is what makes this
      // a no-side-effect cancel, and it stays true no matter what other specs
      // are doing to the shared tenant's row count.
      await expect(page.getByText(/^Imported \d+ panels?$/)).toHaveCount(0);
      await expect(page).not.toHaveURL(/[?&]dashboard=/);
      await locators.waitForListSettled();
      await expect(locators.listingRoot).toBeVisible();
    });
  });

  test("Template gallery filters its templates and cancels without creating a dashboard", { tag: ["@dev", "@test", "@regression", "@functional", "@oss"] }, async ({ page }) => {
    const locators = await setup(page);

    await test.step("The gallery opens with templates in it", async () => {
      await locators.openTemplateGallery();
      await expect(locators.dialogTitle).toHaveText("Start from a template");
      await expect(locators.templateGallery).toBeVisible();
      await expect(locators.templateCards.first()).toBeVisible();
    });

    const totalTemplates = await locators.templateCards.count();

    await test.step("Picking a role narrows the gallery to that role's templates", async () => {
      // The gallery filters by role, not by a typed term, and every role in the
      // catalogue owns at least one template — so "empty gallery" is not a state
      // this filter can produce. CFO is the narrowest role
      // (dashboardTemplates.ts), which makes "fewer than All, still not zero"
      // the assertion that proves the filter ran.
      await locators.templateRoleOption("CFO").click();
      await expect(locators.templateEmpty).toHaveCount(0);
      await expect.poll(() => locators.templateCards.count()).toBeGreaterThan(0);
      expect(await locators.templateCards.count()).toBeLessThan(totalTemplates);
    });

    await test.step("Back to All brings every template back", async () => {
      await locators.templateRoleOption("All").click();
      await expect(locators.templateCards).toHaveCount(totalTemplates);
    });

    await test.step("Cancel closes the gallery and creates nothing", async () => {
      await locators.templateCancelBtn.click();
      await expect(locators.templateGallery).toHaveCount(0);
      // Picking a template drafts it straight into the editor, so an editor
      // that never opened is the proof nothing was started.
      await expect(locators.titleInput).toHaveCount(0);
      await locators.waitForListSettled();
      await expect(locators.listingRoot).toBeVisible();
    });
  });

  test("Opening a dashboard from the list shows its viewer and Back returns to the list", { tag: ["@dev", "@test", "@smoke", "@functional", "@oss"] }, async ({ page }) => {
    const locators = await setup(page);
    const title = uniqueDashboardTitle();

    try {
      await createDashboard(locators, title);
      await locators.backBtn.click();
      await locators.waitForListSettled();
      await locators.search(title);
      await expect(locators.rowByTitle(title)).toHaveCount(1);

      await test.step("Clicking the name opens the dashboard", async () => {
        await locators.openLinkForRow(title).click();
        await expect(locators.dashboardView).toBeVisible();
        await expect(page).toHaveURL(/[?&]dashboard=/);
        await expect(locators.dashboardToolbar).toContainText(title);
        // A brand-new dashboard carries no panels, and says so rather than
        // rendering an empty grid.
        await expect(locators.dashboardView.getByText("No panels yet")).toBeVisible();
      });

      await test.step("Back returns to the listing and drops the dashboard from the URL", async () => {
        await locators.backBtn.click();
        await locators.waitForListSettled();
        await expect(locators.listingRoot).toBeVisible();
        await expect(page).not.toHaveURL(/[?&]dashboard=/);
      });
    } finally {
      await deleteDashboardIfPresent(page, locators, title);
    }
  });

  test("Cancelling the delete confirmation leaves the dashboard in the list", { tag: ["@dev", "@test", "@regression", "@negative", "@functional", "@oss"] }, async ({ page }) => {
    const locators = await setup(page);
    const title = uniqueDashboardTitle();

    try {
      await createDashboard(locators, title);
      await locators.backBtn.click();
      await locators.waitForListSettled();
      await locators.search(title);
      await expect(locators.rowByTitle(title)).toHaveCount(1);

      await test.step("The confirmation names the dashboard it would remove", async () => {
        await locators.deleteBtnForRow(title).click();
        await expect(locators.dialogTitle).toHaveText("Delete dashboard?");
        await expect(locators.dialog).toContainText(title);
      });

      await test.step("Cancel dismisses it and the dashboard survives", async () => {
        await locators.dialogCancelBtn.click();
        await expect(locators.dialogTitle).toHaveCount(0);
        await expect(locators.deletedToast).toHaveCount(0);
        await expect(locators.rowByTitle(title)).toHaveCount(1);
      });
    } finally {
      await deleteDashboardIfPresent(page, locators, title);
    }
  });

  test("Every dashboard row offers an export action and opens from its name", { tag: ["@dev", "@test", "@regression", "@functional", "@oss"] }, async ({ page }) => {
    const locators = await setup(page);
    const rowCount = await locators.waitForListSettled();
    test.skip(rowCount === 0, "Tenant has no dashboards to inspect.");

    await test.step("Export is offered on every row", async () => {
      // Export is a read, so CustomDashboards offers it unconditionally — Edit
      // and Delete are the ones gated on write access and `is_builtin`. Compared
      // as a relationship rather than against a captured number, so a dashboard
      // arriving on the shared tenant mid-test cannot fail it.
      await expect
        .poll(async () => {
          const rows = await locators.rows.count();
          const exports = await locators.rows.locator("[data-testid^='export-dashboard-']").count();
          return rows > 0 && rows === exports;
        })
        .toBe(true);
    });

    await test.step("Every row's name is the handle that opens it", async () => {
      await expect
        .poll(async () => {
          const rows = await locators.rows.count();
          const links = await locators.rows.locator("[data-testid^='open-dashboard-']").count();
          return rows > 0 && rows === links;
        })
        .toBe(true);
    });
  });
});
