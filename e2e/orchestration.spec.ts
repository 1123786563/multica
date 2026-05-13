import { test, expect } from "@playwright/test";
import { loginAsDefault, createTestApi } from "./helpers";
import type { TestApiClient } from "./fixtures";

async function minimizeChatIfOpen(page: import("@playwright/test").Page) {
  const newChatButton = page.getByRole("button", { name: "New chat" });
  if (await newChatButton.isVisible().catch(() => false)) {
    await page.locator("main button").nth(3).click();
    await expect(newChatButton).toBeHidden();
  }
}

async function removeChatOverlay(page: import("@playwright/test").Page) {
  await page.evaluate(() => {
    for (const heading of Array.from(document.querySelectorAll("main h3"))) {
      if (heading.textContent?.includes("Chat with your agents")) {
        heading.closest("main > div")?.remove();
      }
    }
  });
}

async function claimTaskEventually(api: TestApiClient, runtimeId: string) {
  let claimed: Awaited<ReturnType<TestApiClient["claimTask"]>> | null = null;
  await expect
    .poll(async () => {
      claimed = await api.claimTask(runtimeId);
      return claimed?.id ?? null;
    })
    .not.toBeNull();
  return claimed!;
}

// Orchestration acceptance uses a deterministic fake runtime/agent inserted by
// the fixture and drives task lifecycle through the daemon HTTP API. No live
// external agent provider is required.
test.describe("Orchestration acceptance", () => {
  let api: TestApiClient;
  let workspaceSlug: string;

  test.beforeEach(async ({ page }, testInfo) => {
    await page.addInitScript(() => {
      window.localStorage.setItem("multica:chat:isOpen", "false");
    });
    api = await createTestApi(testInfo);
    workspaceSlug = await loginAsDefault(page, testInfo);
    await api.setOrchestrationEnabled(true);
    await page.evaluate(() => {
      window.localStorage.setItem("multica:chat:isOpen", "false");
    });
    await page.reload();
  });

  test.afterEach(async () => {
    if (api) {
      await api.cleanup();
    }
  });

  test("happy path completes an agent-assigned issue and refreshes Issue Detail without reload", async ({ page }) => {
    const { agentId, runtimeId } = await api.createAgentFixture(`E2E Orchestrator ${Date.now()}`);
    const issue = await api.createIssue(`E2E Orchestration Happy ${Date.now()}`, {
      description: "Exercise the orchestration happy path from issue assignment through verification.",
      status: "todo",
      priority: "high",
      assignee_type: "agent",
      assignee_id: agentId,
    });

    const claimed = await api.claimTask(runtimeId);
    expect(claimed?.id).toBeTruthy();
    expect(claimed?.orchestration?.orchestration_plan_id).toBeTruthy();
    await api.startTask(claimed.id);

    await page.goto(`/${workspaceSlug}/issues/${issue.id}`);
    await minimizeChatIfOpen(page);
    await removeChatOverlay(page);
    await expect(page.getByRole("button", { name: "Orchestration" })).toBeVisible();
    await expect(page.getByText("node.running")).toBeVisible();

    await api.completeTask(claimed.id, {
      schema_version: 1,
      status: "completed",
      summary: "Planned the orchestration E2E happy path.",
      criteria_evidence: [
        {
          criterion: "node_objective",
          evidence: "The fake daemon produced a valid structured result for the plan node.",
        },
      ],
      risks: [],
      confidence: 0.96,
    });

    const executeTask = await claimTaskEventually(api, runtimeId);
    expect(executeTask?.id).toBeTruthy();
    expect(executeTask?.orchestration?.node_type).toBe("execute");
    await api.startTask(executeTask!.id);
    await api.completeTask(executeTask!.id, {
      schema_version: 1,
      status: "completed",
      summary: "Implemented orchestration E2E happy path.",
      changed_files: ["e2e/orchestration.spec.ts"],
      criteria_evidence: [
        {
          criterion: "node_objective",
          evidence: "The fake daemon completed the execute node with changed files evidence.",
        },
      ],
      risks: [],
      confidence: 0.96,
    });

    const claimedVerify = await claimTaskEventually(api, runtimeId);
    expect(claimedVerify?.id).toBeTruthy();
    expect(claimedVerify?.orchestration?.node_type).toBe("verify");
    await api.startTask(claimedVerify!.id);
    await api.completeTask(claimedVerify!.id, {
      schema_version: 1,
      status: "completed",
      summary: "Verified the orchestration E2E happy path.",
      test_result: {
        status: "passed",
        passed: true,
      },
      criteria_evidence: [
        {
          criterion: "node_objective",
          evidence: "The fake daemon completed the verify node with passing test evidence.",
        },
      ],
      risks: [],
      confidence: 0.96,
    });

    await expect.poll(async () => (await api.getIssue(issue.id)).status).toBe("in_review");
    await expect.poll(async () => {
      const snapshot = await api.getIssueOrchestration(issue.id);
      return snapshot.nodes?.map((node: { status: string }) => node.status).join(",");
    }).toBe("completed,completed,completed");
    await expect.poll(async () => {
      const snapshot = await api.getIssueOrchestration(issue.id);
      return snapshot.plans?.[0]?.status;
    }).toBe("completed");
    await expect(page).toHaveURL(new RegExp(`/${workspaceSlug}/issues/${issue.id}$`));
    await removeChatOverlay(page);
    await expect(page.getByText("completed").first()).toBeVisible({ timeout: 10000 });
  });

  test("malformed result retries and exposes Decision Panel reason without external agent provider", async ({ page }) => {
    const { agentId, runtimeId } = await api.createAgentFixture(`E2E Blocked ${Date.now()}`);
    const issue = await api.createIssue(`E2E Orchestration Blocked ${Date.now()}`, {
      description: "Malformed structured output should be visible in the decision panel.",
      status: "todo",
      priority: "high",
      assignee_type: "agent",
      assignee_id: agentId,
    });

    const claimed = await api.claimTask(runtimeId);
    expect(claimed?.id).toBeTruthy();
    await api.startTask(claimed.id);

    await page.goto(`/${workspaceSlug}/issues/${issue.id}`);
    await minimizeChatIfOpen(page);
    await removeChatOverlay(page);
    await expect(page.getByRole("button", { name: "Orchestration" })).toBeVisible();

    await api.completeTask(claimed.id, {
      output: "legacy summary only",
    });

    await expect.poll(async () => {
      const snapshot = await api.getIssueOrchestration(issue.id);
      return snapshot.nodes?.[0]?.summary?.reason_code;
    }).toBe("retry_scheduled");
    await expect(page.getByText("Retry scheduled")).toBeVisible({ timeout: 10000 });
    await expect(page.getByText("evidence_insufficient", { exact: true })).toBeVisible();
    await expect(page.getByText("evaluation.invalid_result")).toBeVisible();
  });

  test("historical workspace rollout flag does not block orchestration-by-default", async () => {
    await api.setOrchestrationEnabled(false);
    const { agentId, runtimeId } = await api.createAgentFixture(`E2E Legacy ${Date.now()}`);
    const issue = await api.createIssue(`E2E Legacy Task ${Date.now()}`, {
      description: "Historical rollout flags should not block the default orchestration path.",
      status: "todo",
      priority: "medium",
      assignee_type: "agent",
      assignee_id: agentId,
    });

    const snapshot = await api.getIssueOrchestration(issue.id);
    expect(snapshot.plans).toHaveLength(1);
    expect(snapshot.nodes).toHaveLength(3);

    const claimed = await api.claimTask(runtimeId);
    expect(claimed?.id).toBeTruthy();
    expect(claimed?.issue_id).toBe(issue.id);
    expect(claimed?.orchestration?.orchestration_plan_id).toBeTruthy();
  });
});
