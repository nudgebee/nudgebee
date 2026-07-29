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
    async () => {
      await locators.addUserSubmitBtn.click();
      // Assert the success snackbar inside the action, before the 10s auto-capture
      // window — the toast is transient and gone by the time the watcher returns.
      await expect(locators.successUpdateMsg).toBeVisible({ timeout: 8000 });
    },
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
    async () => {
      await locators.addUserSubmitBtn.click();
      // Assert the success snackbar inside the action, before the 10s auto-capture
      // window — the toast is transient and gone by the time the watcher returns.
      await expect(locators.successUpdateMsg).toBeVisible({ timeout: 8000 });
    },
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
    async () => {
      await locators.addUserSubmitBtn.click();
      // Assert the success snackbar inside the action, before the 10s auto-capture
      // window — the toast is transient and gone by the time the watcher returns.
      await expect(locators.successUpdateMsg).toBeVisible({ timeout: 8000 });
    },
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
    async () => {
      await locators.addUserSubmitBtn.click();
      // Assert the success snackbar inside the action, before the 10s auto-capture
      // window — the toast is transient and gone by the time the watcher returns.
      await expect(locators.successUpdateMsg).toBeVisible({ timeout: 8000 });
    },
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
    async () => {
      await locators.addUserSubmitBtn.click();
      // Assert the success snackbar inside the action, before the 10s auto-capture
      // window — the toast is transient and gone by the time the watcher returns.
      await expect(locators.successUpdateMsg).toBeVisible({ timeout: 8000 });
    },
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
      async () => {
      await locators.addUserSubmitBtn.click();
      // Assert the success snackbar inside the action, before the 10s auto-capture
      // window — the toast is transient and gone by the time the watcher returns.
      await expect(locators.successUpdateMsg).toBeVisible({ timeout: 8000 });
    },
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

test.describe("Users — validation & edge cases", () => {
  // ── VALIDATION (inline helper-text, never hits the backend) ──

  test(
    "Add user — name rejects special characters",
    { tag: ["@regression", "@validation", "@negative"] },
    async ({ page }) => {
      const locators = await setup(page);

      await locators.addNewUserBtn.click();

      // Alphanumeric is allowed; special chars (@ # $ %) must be rejected.
      await locators.firstNameInput.fill("Test@#$%");
      await locators.lastNameInput.fill("User@#$%");
      await locators.emailInput.click(); // blur the last-name field to trigger its validation

      // Both name fields show the inline validation error (rendered as role="alert").
      await expect(page.getByText("This field should be alpha-numeric")).toHaveCount(2);

      // Close without saving — non-mutating, nothing to clean up.
      await locators.cancelBtn.click();
    }
  );

  test(
    "Add user — name is required",
    { tag: ["@regression", "@validation", "@negative"] },
    async ({ page }) => {
      const locators = await setup(page);

      await locators.addNewUserBtn.click();

      // Type then clear to trigger the required-field validation.
      await locators.firstNameInput.fill("A");
      await locators.firstNameInput.fill("");
      await locators.lastNameInput.fill("A");
      await locators.lastNameInput.fill("");
      await locators.emailInput.click(); // blur

      await expect(page.getByText("This field required")).toHaveCount(2);

      await locators.cancelBtn.click();
    }
  );

  test(
    "Add user — name must start with a letter",
    { tag: ["@regression", "@validation", "@negative"] },
    async ({ page }) => {
      const locators = await setup(page);

      await locators.addNewUserBtn.click();

      // Numbers are allowed inside the name, but the first character must be a letter.
      await locators.firstNameInput.fill("123User");
      await locators.lastNameInput.fill("456Test");
      await locators.emailInput.click(); // blur

      await expect(page.getByText("Should start with an alphabet")).toHaveCount(2);

      await locators.cancelBtn.click();
    }
  );

  test(
    "Add user — email must be a valid format",
    { tag: ["@regression", "@validation", "@negative"] },
    async ({ page }) => {
      const locators = await setup(page);

      await locators.addNewUserBtn.click();

      // Invalid format.
      await locators.emailInput.fill("notanemail");
      await locators.firstNameInput.click(); // blur email
      await expect(page.getByText("Please enter a valid email")).toBeVisible();

      // Required (empty).
      await locators.emailInput.fill("");
      await locators.firstNameInput.click(); // blur email
      await expect(page.getByText("Please enter a email")).toBeVisible();

      await locators.cancelBtn.click();
    }
  );

  test(
    "Add user — email rejects malformed formats",
    { tag: ["@regression", "@validation", "@negative"] },
    async ({ page }) => {
      const locators = await setup(page);

      await locators.addNewUserBtn.click();

      // Double @, consecutive dots in local part and in domain — all invalid.
      const malformed = ["test@@example.com", "test..user@example.com", "test@example..com"];
      for (const email of malformed) {
        // Reset to a valid email first so each malformed case is proven independently
        // (a stale error can't mask a format that's wrongly accepted).
        await locators.emailInput.fill("valid@example.com");
        await locators.firstNameInput.click(); // blur email
        await expect(page.getByText("Please enter a valid email")).toBeHidden();

        await locators.emailInput.fill(email);
        await locators.firstNameInput.click(); // blur email
        await expect(page.getByText("Please enter a valid email")).toBeVisible();
      }

      await locators.cancelBtn.click();
    }
  );

  // ── EDIT VALIDATION ──

  test(
    "Edit user — name field enforces validation",
    { tag: ["@regression", "@validation", "@negative"] },
    async ({ page }) => {
      const locators = await setup(page);
      const user = users[1]; // test.user2

      // Guarantee the user exists and is Inactive, then open its edit modal.
      await ensureUserInactive(locators, user);
      await locators.selectStatusFilter("Inactive");
      await locators.searchByName(`${user.first} ${user.last}`);
      await locators.getEditBtnForUser(user.email).click();
      await locators.editUserModal.waitFor({ state: "visible", timeout: 15000 });

      // Same name rules apply in edit mode — check each on the first-name field.
      await locators.firstNameInput.fill(""); // required
      await expect(page.getByText("This field required")).toBeVisible();

      await locators.firstNameInput.fill("Test@#"); // special chars
      await expect(page.getByText("This field should be alpha-numeric")).toBeVisible();

      await locators.firstNameInput.fill("123Test"); // must start with a letter
      await expect(page.getByText("Should start with an alphabet")).toBeVisible();

      // Save is blocked while the field is invalid.
      await expect(locators.addUserSubmitBtn).toBeDisabled();

      // Cancel without saving — no mutation, no reset needed.
      await locators.cancelBtn.click();
    }
  );

  // ── NEGATIVE ──

  test(
    "Add user — duplicate email is rejected",
    { tag: ["@regression", "@negative"] },
    async ({ page }) => {
      const locators = await setup(page);
      const existing = users[0]; // test.user1 — already exists

      await locators.addNewUserBtn.click();
      await locators.firstNameInput.fill("DupTest");
      await locators.lastNameInput.fill("User");
      await locators.emailInput.fill(existing.email);
      await locators.selectRole(existing.role);
      await locators.addUserSubmitBtn.click();

      // Snackbar rejection, no success, modal stays open — nothing created.
      await expect(locators.duplicateMsg).toBeVisible();
      await expect(locators.successMsg).not.toBeVisible();
      await expect(locators.firstNameInput).toBeVisible();

      await locators.cancelBtn.click();
    }
  );

  // ── VIEW / SEARCH (non-mutating) ──

  test(
    "Search — finds a user by name",
    { tag: ["@smoke", "@search"] },
    async ({ page }) => {
      const locators = await setup(page);
      const user = users[0];

      // Guarantee the user exists and is Inactive so the search is deterministic.
      await ensureUserInactive(locators, user);

      await locators.selectStatusFilter("Inactive");
      await locators.searchByName(`${user.first} ${user.last}`);
      await expect(locators.getUserRow(user.email)).toBeVisible();
    }
  );

  test(
    "Search — no match shows empty state, clear restores list",
    { tag: ["@regression", "@search"] },
    async ({ page }) => {
      const locators = await setup(page);

      // A name that cannot exist -> empty state.
      await locators.searchByName("zzz_nonexistent_user_000");
      await expect(page.getByText("No Data Available")).toBeVisible();

      // Clearing the search restores the list.
      await locators.searchByName("");
      await expect(page.getByText("No Data Available")).toBeHidden();
    }
  );
});
