import { test, expect } from "@playwright/test";
import { createTestApi, loginAsDefault } from "./helpers";
import type { TestApiClient } from "./fixtures";

test.describe("Orchestration", () => {
  let api: TestApiClient;
  let workspaceSlug: string;

  test.beforeEach(async ({ page }) => {
    api = await createTestApi();
    workspaceSlug = await loginAsDefault(page);
  });

  test.afterEach(async () => {
    if (api) await api.cleanup();
  });

  test("issue detail renders orchestration projection after start", async ({ page }) => {
    const issue = await api.createIssue("E2E Orchestration " + Date.now(), {
      status: "backlog",
      priority: "none",
    });
    await api.startIssueOrchestration(issue.id);

    await page.goto(`/${workspaceSlug}/issues/${issue.id}`);
    await expect(page.getByRole("button", { name: "Orchestration" })).toBeVisible({ timeout: 10000 });
    await expect(page.getByText(/starting|running|failed|waiting_human|completed|cancelled/).first()).toBeVisible();
  });
});
