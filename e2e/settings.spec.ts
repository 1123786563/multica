import { test, expect } from "@playwright/test";
import { loginAsDefault, openWorkspaceMenu } from "./helpers";

test.describe("Settings", () => {
  test("updating workspace name reflects in sidebar immediately", async ({
    page,
  }, testInfo) => {
    await loginAsDefault(page, testInfo);

    // Read the current workspace name from the sidebar
    const sidebarName = page
      .locator('[data-sidebar="menu-button"][aria-haspopup="menu"]')
      .first();
    const originalName = await sidebarName.innerText();

    // Navigate to settings
    await page.getByRole("link", { name: "Settings" }).click();
    await page.waitForURL("**/settings");
    await page.getByRole("tab", { name: "General" }).click();

    // Change workspace name
    const nameInput = page.locator('input[type="text"]').first();
    await nameInput.clear();
    const newName = "Renamed WS " + Date.now();
    await nameInput.fill(newName);

    // Save
    await page.locator("button", { hasText: "Save" }).click({ force: true });

    // Sidebar should reflect the new name WITHOUT page refresh
    await expect(sidebarName).toContainText(newName);
    await expect(nameInput).toHaveValue(newName);

    // Restore original name so other tests aren't affected
    await nameInput.clear();
    await nameInput.fill(originalName.trim());
    await page.locator("button", { hasText: "Save" }).click({ force: true });
    await expect(sidebarName).toContainText(originalName.trim());
  });
});
