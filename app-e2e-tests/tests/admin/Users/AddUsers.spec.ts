import { test, expect } from "@playwright/test";
import { users } from "./usersConstants";
import { setup, ensureUserInactive } from "./usersHelper";
import { waitForGraphQLAndValidate } from "../../utils/GraphQLNetworkWatcher";

test.describe.configure({ mode: "default", timeout: 180000 });

test("Add multiple users from list", async ({ page }) => {
  const locators = await setup(page);

  for (const user of users) {
    await test.step(`Processing: ${user.email}`, async () => {
      await locators.addNewUserBtn.click();
      await locators.firstNameInput.fill(user.first);
      await locators.lastNameInput.fill(user.last);
      await locators.emailInput.fill(user.email);
      await locators.selectRole(user.role);
      await locators.addUserSubmitBtn.click();

      const result = await locators.successMsg
        .or(locators.duplicateMsg)
        .waitFor({ state: "visible", timeout: 8000 })
        .then(async () => ((await locators.successMsg.isVisible()) ? "success" : "duplicate"))
        .catch(() => "timeout");

      if (result === "success") {
        console.log(`SUCCESS: User "${user.email}" added successfully.`);
        await locators.addUserSubmitBtn.waitFor({ state: "hidden", timeout: 5000 }).catch(() => {});
      } else if (result === "duplicate") {
        console.log(`DUPLICATE: User "${user.email}" already exists.`);
        await locators.cancelBtn.click();
      } else {
        console.log(`ERROR: Timeout for "${user.email}". No response notification found.`);
        if (await locators.cancelBtn.isVisible()) {
          await locators.cancelBtn.click();
        }
      }
      await page.waitForTimeout(500);
    });
  }
});

test("Activate, edit role, verify, then reset an inactive user", async ({ page }, testInfo) => {
  const locators = await setup(page);

  const user = users[0];
  const editedRole = "ReadOnly Admin";

  // 0. Self-normalize: make sure the user exists and is Inactive before we start.
  await ensureUserInactive(locators, user);

  // 1. Find the inactive user and open its edit modal.
  await locators.selectStatusFilter("Inactive");
  await locators.searchByName(`${user.first} ${user.last}`);
  await locators.getEditBtnForUser(user.email).click();
  await locators.editUserModal.waitFor({ state: "visible", timeout: 15000 });
  await expect(locators.emailInput).toBeDisabled();

  // 2. Activate + save (validate the update API).
  await locators.setUserStatus("Active");
  await waitForGraphQLAndValidate(
    page,
    async () => { await locators.addUserSubmitBtn.click(); },
    { testName: testInfo.title, operationNames: [] }
  );
  await locators.editUserModal.waitFor({ state: "hidden", timeout: 10000 });

  // 3. Verify the user now appears in the Active list.
  await locators.selectStatusFilter("Active");
  await locators.searchByName(`${user.first} ${user.last}`);
  await expect(locators.getUserRow(user.email)).toBeVisible();

  // 4. Edit tenant role only -> save (validate API) -> verify persisted.
  //    Name is intentionally left unchanged so this test can never strand the user
  //    (search is by name); name-edit coverage lives in its own test below.
  await locators.getEditBtnForUser(user.email).click();
  await locators.editUserModal.waitFor({ state: "visible", timeout: 15000 });
  await locators.selectRole(editedRole);
  await waitForGraphQLAndValidate(
    page,
    async () => { await locators.addUserSubmitBtn.click(); },
    { testName: testInfo.title, operationNames: [] }
  );
  await locators.editUserModal.waitFor({ state: "hidden", timeout: 10000 });
  await locators.searchByName(`${user.first} ${user.last}`);
  await expect(locators.getUserRow(user.email)).toContainText(editedRole);

  // 5. Reset: restore original role and flip back to Inactive (validate API).
  await locators.getEditBtnForUser(user.email).click();
  await locators.editUserModal.waitFor({ state: "visible", timeout: 15000 });
  await locators.selectRole(user.role);
  await locators.setUserStatus("Inactive");
  await waitForGraphQLAndValidate(
    page,
    async () => { await locators.addUserSubmitBtn.click(); },
    { testName: testInfo.title, operationNames: [] }
  );
  await locators.editUserModal.waitFor({ state: "hidden", timeout: 10000 });
});

test("Suspend an inactive user and verify it appears in Suspended list", async ({ page }, testInfo) => {
  const locators = await setup(page);

  const user = users[1];

  // 0. Self-normalize: make sure the user exists and is Inactive before we start.
  await ensureUserInactive(locators, user);

  // 1. Find the inactive user and open its edit modal.
  await locators.selectStatusFilter("Inactive");
  await locators.searchByName(`${user.first} ${user.last}`);
  await locators.getEditBtnForUser(user.email).click();
  await locators.editUserModal.waitFor({ state: "visible", timeout: 15000 });

  // 2. Set status to Suspended and save (validate the update API).
  await locators.setUserStatus("Suspended");
  await waitForGraphQLAndValidate(
    page,
    async () => { await locators.addUserSubmitBtn.click(); },
    { testName: testInfo.title, operationNames: [] }
  );
  await locators.editUserModal.waitFor({ state: "hidden", timeout: 10000 });

  // 3. Verify the user now appears in the Suspended list.
  await locators.selectStatusFilter("Suspended");
  await locators.searchByName(`${user.first} ${user.last}`);
  await expect(locators.getUserRow(user.email)).toBeVisible();

  // 4. Reset: flip status back to Inactive (validate API).
  await locators.getEditBtnForUser(user.email).click();
  await locators.editUserModal.waitFor({ state: "visible", timeout: 15000 });
  await locators.setUserStatus("Inactive");
  await waitForGraphQLAndValidate(
    page,
    async () => { await locators.addUserSubmitBtn.click(); },
    { testName: testInfo.title, operationNames: [] }
  );
  await locators.editUserModal.waitFor({ state: "hidden", timeout: 10000 });
});

test("Edit user first and last name persists", async ({ page }, testInfo) => {
  const locators = await setup(page);

  const user = users[2]; // dedicated user (test.user3) — not shared with the role-edit test
  const editedFirst = "TestEdited";
  const editedLast = "User3Edited";
  const editedName = `${editedFirst} ${editedLast}`;

  // 0. Self-normalize; pass the edited name so a user stranded by a crashed run
  //    (left renamed) is still found and restored.
  await ensureUserInactive(locators, user, [editedName]);

  try {
    // 1. Find the inactive user and open its edit modal.
    await locators.selectStatusFilter("Inactive");
    await locators.searchByName(`${user.first} ${user.last}`);
    await locators.getEditBtnForUser(user.email).click();
    await locators.editUserModal.waitFor({ state: "visible", timeout: 15000 });

    // 2. Edit first + last name -> save (validate API).
    await locators.firstNameInput.fill(editedFirst);
    await locators.lastNameInput.fill(editedLast);
    await waitForGraphQLAndValidate(
      page,
      async () => { await locators.addUserSubmitBtn.click(); },
      { testName: testInfo.title, operationNames: [] }
    );
    await locators.editUserModal.waitFor({ state: "hidden", timeout: 10000 });

    // 3. Verify the name change persisted (search by the new name).
    await locators.searchByName(editedName);
    await expect(locators.getUserRow(user.email)).toContainText(editedLast);
  } finally {
    // 4. Always restore original name + Inactive, whatever state we ended in.
    await ensureUserInactive(locators, user, [editedName]);
  }
});
