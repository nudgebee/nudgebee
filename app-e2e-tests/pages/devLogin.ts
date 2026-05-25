import { Page } from "@playwright/test";
export async function doDevLogin(page: Page): Promise<void> {
  const baseUrl = process.env.BASE_URL;
  const username = process.env.LDAP_USERNAME || "";
  const password = process.env.LDAP_PASSWORD || "";
  const clusterName = process.env.CLUSTER_NAME || "k8s-dev";

  if (!baseUrl) throw new Error("BASE_URL is not set in environment");
  if (!username || !password) throw new Error("LDAP_USERNAME or LDAP_PASSWORD is not set");

  await page.goto(baseUrl);
  await ldapLogin(page, username, password);
  await waitForLoaderToDisappear(page);

  if (page.url().includes("/api/auth/error")) {
    await page.goto(baseUrl);
    await ldapLogin(page, username, password);
  } else if (page.url().includes("/signin")) {
    await ldapLogin(page, username, password);
  }

  await page.waitForURL(/\/(home|workflow)/, { timeout: 30000 });
  await waitForLoaderToDisappear(page);
  await selectCluster(page, clusterName);
  await waitForLoaderToDisappear(page);
}

async function ldapLogin(page: Page, username: string, password: string): Promise<void> {
  const usernameInput = page.getByRole("textbox", { name: "LDAP Username" });
  const passwordInput = page.getByRole("textbox", { name: "LDAP Password" });
  const submitButton = page.getByRole("button", { name: "Submit" });

  await usernameInput.waitFor({ state: "visible", timeout: 10000 });
  await passwordInput.waitFor({ state: "visible", timeout: 10000 });

  await usernameInput.click();
  await usernameInput.fill("");
  await usernameInput.pressSequentially(username, { delay: 20 });

  await passwordInput.click();
  await passwordInput.fill("");
  await passwordInput.pressSequentially(password, { delay: 20 });

  await submitButton.click();
}

async function selectCluster(page: Page, clusterName: string): Promise<void> {
  const clusterInput = page.locator("#auto-complete-global-cluster");

  const clearAndType = async () => {
    await clusterInput.fill("");
    await page.waitForTimeout(400);
    await clusterInput.pressSequentially(clusterName, { delay: 50 });
  };

  const option = page.locator("[role='option']").filter({ hasText: clusterName }).first();

  for (let attempt = 1; attempt <= 3; attempt++) {
    if (attempt > 1) {
      await page.waitForTimeout(2000);
    }
    await clearAndType();
    await page.waitForTimeout(800);
    const isVisible = await option.isVisible().catch(() => false);
    if (isVisible) break;
    console.log(`Cluster option '${clusterName}' not found (attempt ${attempt}/3), retrying...`);
  }

  await option.waitFor({ state: "visible", timeout: 15000 });
  await option.click();
  await page.mouse.move(0, 0);
  console.log(`Selected cluster: ${clusterName}`);
}

async function waitForLoaderToDisappear(page: Page): Promise<void> {
  const loader = page.getByAltText("Loading...");
  await loader.waitFor({ state: "hidden", timeout: 180000 });
}
