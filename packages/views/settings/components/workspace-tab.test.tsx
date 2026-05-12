import { describe, it, expect, beforeEach, vi } from "vitest";
import { render, screen } from "@testing-library/react";

const mockUpdateWorkspace = vi.hoisted(() => vi.fn());
const mockUseQuery = vi.hoisted(() => vi.fn());
const mockSetQueriesData = vi.hoisted(() => vi.fn());
const mockInvalidateQueries = vi.hoisted(() => vi.fn());

vi.mock("../../i18n", () => ({
  useT: () => ({
    t: (selector: (value: any) => string) =>
      selector({
        workspace: {
          section_general: "General",
          name_label: "Name",
          description_label: "Description",
          description_placeholder: "What does this workspace focus on?",
          context_label: "Context",
          context_placeholder: "Background information and context for AI agents working in this workspace",
          slug_label: "Slug",
          save: "Save",
          saving: "Saving...",
          manage_hint: "Only admins and owners can update workspace settings.",
          toast_saved: "Workspace settings saved",
          toast_save_failed: "Failed to save workspace settings",
          danger_zone: "Danger Zone",
          leave_title: "Leave workspace",
          leave_sole_owner:
            "You're the only owner. Promote another member to owner first, or delete the workspace.",
          leave_sole_member: "You're the only member. Delete the workspace to leave.",
          leave_default: "Remove yourself from this workspace.",
          leave_button: "Leave workspace",
          leaving: "Leaving...",
          leave_confirm_title: "Leave workspace",
          leave_confirm_description: "Leave {{name}}? You will lose access until re-invited.",
          toast_leave_failed: "Failed to leave workspace",
          delete_title: "Delete workspace",
          delete_description: "Permanently delete this workspace and its data.",
          delete_button: "Delete workspace",
          deleting: "Deleting...",
          toast_delete_failed: "Failed to delete workspace",
          confirm_cancel: "Cancel",
          confirm_action: "Confirm",
        },
      }),
  }),
}));

vi.mock("@tanstack/react-query", () => ({
  useQuery: (...args: unknown[]) => mockUseQuery(...args),
  useQueryClient: () => ({
    getQueryData: vi.fn(() => []),
    setQueriesData: mockSetQueriesData,
    invalidateQueries: mockInvalidateQueries,
  }),
}));

vi.mock("@multica/core/auth", () => ({
  useAuthStore: (selector: (state: { user: { id: string } | null }) => unknown) =>
    selector({ user: { id: "user-1" } }),
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@multica/core/paths", () => ({
  useCurrentWorkspace: () => ({
    id: "ws-1",
    name: "Acme",
    slug: "acme",
    context: "",
    description: "",
    settings: {},
  }),
  useHasOnboarded: () => true,
  resolvePostAuthDestination: () => "/",
  useWorkspacePaths: () => ({}),
}));

vi.mock("@multica/core/platform", () => ({
  setCurrentWorkspace: vi.fn(),
}));

vi.mock("@multica/core/api", () => ({
  api: {
    updateWorkspace: mockUpdateWorkspace,
  },
}));

vi.mock("@multica/core/workspace/mutations", () => ({
  useLeaveWorkspace: () => ({ mutateAsync: vi.fn() }),
  useDeleteWorkspace: () => ({ mutateAsync: vi.fn() }),
}));

vi.mock("@multica/core/workspace/queries", () => ({
  memberListOptions: () => ({ queryKey: ["members"] }),
  workspaceKeys: {
    list: () => ["workspaces", "list"],
  },
  workspaceListOptions: () => ({ queryKey: ["workspaces", "list"] }),
}));

vi.mock("../../navigation", () => ({
  useNavigation: () => ({ push: vi.fn() }),
}));

vi.mock("./delete-workspace-dialog", () => ({
  DeleteWorkspaceDialog: () => null,
}));

import { WorkspaceTab } from "./workspace-tab";

describe("WorkspaceTab", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockUseQuery.mockReturnValue({
      data: [{ user_id: "user-1", role: "owner" }],
      isFetched: true,
    });
  });

  it("does not expose an orchestration rollout toggle", () => {
    render(<WorkspaceTab />);

    expect(screen.queryByRole("switch")).not.toBeInTheDocument();
    expect(screen.queryByText("Enable orchestration")).not.toBeInTheDocument();
    expect(mockUpdateWorkspace).not.toHaveBeenCalled();
    expect(mockSetQueriesData).not.toHaveBeenCalled();
    expect(mockInvalidateQueries).not.toHaveBeenCalled();
  });
});
