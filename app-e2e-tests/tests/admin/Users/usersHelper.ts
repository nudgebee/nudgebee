import { Page } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { UserLocators } from "./usersLocators";

// Shared setup: log in (session reused via global-setup) and land on the user-management page.
export async function setup(page: Page): Promise<UserLocators> {
  const locators = new UserLocators(page);
  await new LoginPage(page).doFullLogin();
  await locators.homeBtn.click();
  await locators.adminBtn.click();
  await page.waitForURL("**/user-management**", { timeout: 15000 });
  await locators.addNewUserBtn.waitFor({ state: "visible", timeout: 15000 });
  return locators;
}

// Normalize the user to a known baseline: find it under whatever status it is
// currently in (Inactive / Active / Suspended) and restore original name + Inactive.
// Makes the lifecycle self-healing regardless of how a prior run left the user.
//
// altNames: extra name(s) the user might currently be listed under (e.g. a name-edit
// test may have left it renamed). The search box only matches by name, so a stranded
// user must be looked up under those names too. Callers that never rename pass nothing.
export async function ensureUserInactive(
  locators: UserLocators,
  user: { first: string; last: string; email: string },
  altNames: string[] = []
): Promise<void> {
  const names = [`${user.first} ${user.last}`, ...altNames];
  for (const status of ["Inactive", "Active", "Suspended"] as const) {
    for (const name of names) {
      await locators.selectStatusFilter(status);
      await locators.searchByName(name);
      const found = await locators
        .getUserRow(user.email)
        .waitFor({ state: "visible", timeout: 3000 })
        .then(() => true)
        .catch(() => false);
      if (!found) continue;
      if (status === "Inactive" && name === names[0]) return; // already at baseline
      // Found under a wrong status and/or an edited name -> restore original name + Inactive.
      await locators.getEditBtnForUser(user.email).click();
      await locators.editUserModal.waitFor({ state: "visible", timeout: 15000 });
      await locators.firstNameInput.fill(user.first);
      await locators.lastNameInput.fill(user.last);
      await locators.setUserStatus("Inactive");
      await locators.addUserSubmitBtn.click();
      await locators.successUpdateMsg.waitFor({ state: "visible", timeout: 10000 });
      await locators.editUserModal.waitFor({ state: "hidden", timeout: 10000 });
      return;
    }
  }
  // Not found under any status/name -> the Add step will create it fresh (lands Inactive).
}