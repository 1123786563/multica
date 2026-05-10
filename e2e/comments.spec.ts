import { test, expect } from "@playwright/test";
import { createTestApi, loginAsDefault } from "./helpers";
import type { TestApiClient } from "./fixtures";

test.describe("Comments", () => {
  let api: TestApiClient;
  let issueId: string;

  test.beforeEach(async ({ page }, testInfo) => {
    api = await createTestApi(testInfo);
    const issue = await api.createIssue("E2E Comment Test " + Date.now());
    issueId = issue.id;
    await loginAsDefault(page, testInfo);
  });

  test.afterEach(async () => {
    await api.cleanup();
  });

  test("can add a comment on an issue", async ({ page }) => {
    await page.goto(`${page.url().replace(/\/issues.*$/, "")}/issues/${issueId}`);
    await page.waitForURL(/\/issues\/[\w-]+/);

    // Wait for issue detail to load
    await expect(page.locator("text=Properties")).toBeVisible();

    // Type a comment
    const commentText = "E2E comment " + Date.now();
    const commentInput = page
      .locator('[data-placeholder="Leave a comment..."]')
      .last();
    await commentInput.fill(commentText);

    // Submit the comment
    await page.getByRole("button", { name: "Submit comment" }).click({ force: true });

    // Comment should appear in the activity section
    await expect(page.locator(`text=${commentText}`)).toBeVisible({
      timeout: 5000,
    });
  });

  test("comment submit button is disabled when empty", async ({ page }) => {
    await page.goto(`${page.url().replace(/\/issues.*$/, "")}/issues/${issueId}`);
    await page.waitForURL(/\/issues\/[\w-]+/);

    await expect(page.locator("text=Properties")).toBeVisible();

    // Submit button should be disabled when input is empty
    const submitBtn = page.locator("main button").last();
    await expect(submitBtn).toBeDisabled();
  });
});
