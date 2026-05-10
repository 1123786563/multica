import { type Page, type TestInfo } from "@playwright/test";
import { TestApiClient } from "./fixtures";

const DEFAULT_E2E_NAME = "E2E User";

function testSlug(testInfo: TestInfo) {
  return `e2e-${testInfo.workerIndex}-${testInfo.repeatEachIndex}-${testInfo.retry}`;
}

function testEmail(testInfo: TestInfo) {
  return `${testSlug(testInfo)}@multica.ai`;
}

function testWorkspaceSlug(testInfo: TestInfo) {
  return `${testSlug(testInfo)}-workspace`;
}

/**
 * Log in as the default E2E user and ensure the workspace exists first.
 * Authenticates via API (send-code → DB read → verify-code), then injects
 * the token into localStorage so the browser session is authenticated.
 *
 * Returns the E2E workspace slug so callers can build workspace-scoped URLs.
 */
export async function loginAsDefault(page: Page, testInfo: TestInfo): Promise<string> {
  const api = new TestApiClient();
  await api.login(testEmail(testInfo), DEFAULT_E2E_NAME);
  const workspace = await api.ensureWorkspace(
    "E2E Workspace",
    testWorkspaceSlug(testInfo),
  );
  await api.dismissStarterContent();

  const token = api.getToken();
  await page.addInitScript((t) => {
    window.localStorage.setItem("multica_token", t);
  }, token);
  await page.goto(`/${workspace.slug}/issues`);
  await page.waitForURL("**/issues", { timeout: 10000 });
  return workspace.slug;
}

/**
 * Create a TestApiClient logged in as the default E2E user.
 * Call api.cleanup() in afterEach to remove test data created during the test.
 */
export async function createTestApi(testInfo: TestInfo): Promise<TestApiClient> {
  const api = new TestApiClient();
  await api.login(testEmail(testInfo), DEFAULT_E2E_NAME);
  await api.ensureWorkspace("E2E Workspace", testWorkspaceSlug(testInfo));
  await api.dismissStarterContent();
  return api;
}

export async function openWorkspaceMenu(page: Page) {
  await page.getByRole("button", { name: /E2E Workspace/ }).click();
  await page.getByRole("menuitem", { name: "Log out" }).waitFor({
    state: "visible",
  });
}
