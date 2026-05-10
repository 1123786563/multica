import { test, expect } from "@playwright/test";
import { loginAsDefault, openWorkspaceMenu } from "./helpers";

test.describe("Navigation", () => {
  test.beforeEach(async ({ page }, testInfo) => {
    await loginAsDefault(page, testInfo);
  });

  test("sidebar navigation works", async ({ page }) => {
    // Click Inbox
    await page.getByRole("link", { name: "Inbox" }).click();
    await page.waitForURL("**/inbox");
    await expect(page).toHaveURL(/\/inbox/);

    // Click Agents
    await page.getByRole("link", { name: "Agents" }).click();
    await page.waitForURL("**/agents");
    await expect(page).toHaveURL(/\/agents/);

    // Click Issues
    await page.getByRole("link", { name: "Issues", exact: true }).click();
    await page.waitForURL("**/issues");
    await expect(page).toHaveURL(/\/issues/);
  });

  test("settings page loads via workspace menu", async ({ page }) => {
    await page.getByRole("link", { name: "Settings" }).click();
    await page.waitForURL("**/settings");

    await expect(page.getByRole("heading", { name: "Settings" })).toBeVisible();
    await page.getByRole("tab", { name: "General" }).click();
    await expect(page.getByRole("heading", { name: "General" })).toBeVisible();
    await page.getByRole("tab", { name: "Members" }).click();
    await expect(page.getByRole("heading", { name: /Members/ })).toBeVisible();
  });

  test("agents page shows agent list", async ({ page }) => {
    await page.getByRole("link", { name: "Agents" }).click();
    await page.waitForURL("**/agents");

    // Should show "Agents" heading
    await expect(page.locator("text=Agents").first()).toBeVisible();
  });
});
