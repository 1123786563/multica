import { forwardRef, useRef, useState, useImperativeHandle } from "react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { Issue, TimelineEntry } from "@multica/core/types";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enIssues from "../../locales/en/issues.json";

const TEST_RESOURCES = { en: { common: enCommon, issues: enIssues } };

const mockViewport = vi.hoisted(() => ({ isMobile: false }));

vi.mock("@multica/ui/hooks/use-mobile", () => ({
  useIsMobile: () => mockViewport.isMobile,
}));

// useWorkspaceId() derives from useCurrentWorkspace (relative import inside
// @multica/core/hooks.tsx). vi.mock("@multica/core/paths") only intercepts
// the bare-specifier, not the internal relative import. Mock the hooks module
// directly so the bridge hook returns the test UUID.
vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

// Mock @multica/core/auth
const mockAuthUser = { id: "user-1", email: "test@test.com", name: "Test User" };
vi.mock("@multica/core/auth", () => ({
  useAuthStore: Object.assign(
    (selector?: any) => {
      const state = { user: mockAuthUser, isAuthenticated: true };
      return selector ? selector(state) : state;
    },
    { getState: () => ({ user: mockAuthUser, isAuthenticated: true }) },
  ),
  registerAuthStore: vi.fn(),
  createAuthStore: vi.fn(),
}));

// Mock @multica/core/workspace/hooks
vi.mock("@multica/core/workspace/hooks", () => ({
  useActorName: () => ({
    getMemberName: (id: string) => (id === "user-1" ? "Test User" : "Unknown"),
    getAgentName: (id: string) => (id === "agent-1" ? "Claude Agent" : "Unknown Agent"),
    getActorName: (type: string, id: string) => {
      if (type === "member" && id === "user-1") return "Test User";
      if (type === "agent" && id === "agent-1") return "Claude Agent";
      return "Unknown";
    },
    getActorInitials: (type: string) => (type === "member" ? "TU" : "CA"),
    getActorAvatarUrl: () => null,
  }),
}));

// Mock workspace queries
vi.mock("@multica/core/workspace/queries", () => ({
  memberListOptions: () => ({
    queryKey: ["workspaces", "ws-1", "members"],
    queryFn: () => Promise.resolve([{ user_id: "user-1", name: "Test User", email: "test@test.com", role: "admin" }]),
  }),
  agentListOptions: () => ({
    queryKey: ["workspaces", "ws-1", "agents"],
    queryFn: () => Promise.resolve([]),
  }),
  assigneeFrequencyOptions: () => ({
    queryKey: ["workspaces", "ws-1", "assignee-frequency"],
    queryFn: () => Promise.resolve([]),
  }),
  workspaceListOptions: () => ({
    queryKey: ["workspaces"],
    queryFn: () => Promise.resolve([{ id: "ws-1", name: "Test WS", slug: "test" }]),
  }),
}));

// Mock @multica/core/paths — after the URL-driven workspace refactor,
// useCurrentWorkspace / useWorkspacePaths derive from the workspace slug in
// URL Context. Tests don't mount a real route, so we short-circuit to fixtures.
vi.mock("@multica/core/paths", async () => {
  const actual = await vi.importActual<typeof import("@multica/core/paths")>(
    "@multica/core/paths",
  );
  return {
    ...actual,
    useCurrentWorkspace: () => ({ id: "ws-1", name: "Test WS", slug: "test" }),
    useWorkspacePaths: () => actual.paths.workspace("test"),
  };
});

// Mock navigation
vi.mock("../../navigation", () => ({
  AppLink: ({ children, href, ...props }: any) => (
    <a href={href} {...props}>
      {children}
    </a>
  ),
  useNavigation: () => ({
    push: vi.fn(),
    pathname: "/issues/issue-1",
    getShareableUrl: (p: string) => `https://app.multica.com${p}`,
  }),
  NavigationProvider: ({ children }: { children: React.ReactNode }) => children,
}));

// Mock editor components (Tiptap requires real DOM)
vi.mock("../../editor", () => ({
  useFileDropZone: () => ({ isDragOver: false, dropZoneProps: {} }),
  FileDropOverlay: () => null,
  ReadonlyContent: ({ content }: { content: string }) => (
    <div data-testid="readonly-content">{content}</div>
  ),
  ContentEditor: forwardRef(function MockContentEditor(
    { defaultValue, onUpdate, placeholder }: any,
    ref: any,
  ) {
    const valueRef = useRef(defaultValue || "");
    const [value, setValue] = useState(defaultValue || "");
    useImperativeHandle(ref, () => ({
      getMarkdown: () => valueRef.current,
      clearContent: () => { valueRef.current = ""; setValue(""); },
      focus: () => {},
      uploadFile: () => {},
    }));
    return (
      <textarea
        value={value}
        onChange={(e) => {
          valueRef.current = e.target.value;
          setValue(e.target.value);
          onUpdate?.(e.target.value);
        }}
        placeholder={placeholder}
        data-testid="rich-text-editor"
      />
    );
  }),
  TitleEditor: forwardRef(function MockTitleEditor(
    { defaultValue, placeholder, onBlur, onChange }: any,
    ref: any,
  ) {
    const valueRef = useRef(defaultValue || "");
    const [value, setValue] = useState(defaultValue || "");
    useImperativeHandle(ref, () => ({
      getText: () => valueRef.current,
      focus: () => {},
    }));
    return (
      <input
        value={value}
        onChange={(e) => {
          valueRef.current = e.target.value;
          setValue(e.target.value);
          onChange?.(e.target.value);
        }}
        onBlur={() => onBlur?.(valueRef.current)}
        placeholder={placeholder}
        data-testid="title-editor"
      />
    );
  }),
}));

// Mock common components
vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: ({ actorType, actorId }: any) => (
    <span data-testid="actor-avatar">
      {actorType}:{actorId}
    </span>
  ),
}));

vi.mock("../../projects/components/project-picker", () => ({
  ProjectPicker: () => <span data-testid="project-picker">Project</span>,
}));

// Mock api
const mockApiObj = vi.hoisted(() => ({
  getIssue: vi.fn(),
  listTimeline: vi.fn().mockResolvedValue([]),
  listComments: vi.fn().mockResolvedValue([]),
  createComment: vi.fn(),
  updateComment: vi.fn(),
  deleteComment: vi.fn(),
  deleteIssue: vi.fn(),
  updateIssue: vi.fn(),
  listIssueSubscribers: vi.fn().mockResolvedValue([]),
  subscribeToIssue: vi.fn().mockResolvedValue(undefined),
  unsubscribeFromIssue: vi.fn().mockResolvedValue(undefined),
  getActiveTasksForIssue: vi.fn().mockResolvedValue({ tasks: [] }),
  listTasksByIssue: vi.fn().mockResolvedValue([]),
  getIssueOrchestration: vi.fn().mockResolvedValue({
    plans: [],
    nodes: [],
    events: [],
    artifacts: [],
  }),
  listTaskMessages: vi.fn().mockResolvedValue([]),
  listChildIssues: vi.fn().mockResolvedValue({ issues: [] }),
  listIssues: vi.fn().mockResolvedValue({ issues: [], total: 0 }),
  uploadFile: vi.fn(),
  listIssueReactions: vi.fn().mockResolvedValue([]),
  addIssueReaction: vi.fn(),
  removeIssueReaction: vi.fn(),
  addCommentReaction: vi.fn(),
  removeCommentReaction: vi.fn(),
  listMembers: vi.fn().mockResolvedValue([{ user_id: "user-1", name: "Test User", email: "test@test.com", role: "admin" }]),
  listAgents: vi.fn().mockResolvedValue([]),
}));

vi.mock("@multica/core/api", () => ({
  api: mockApiObj,
  getApi: () => mockApiObj,
  setApiInstance: vi.fn(),
}));

// Mock issue config
vi.mock("@multica/core/issues/config", () => ({
  ALL_STATUSES: ["backlog", "todo", "in_progress", "in_review", "done", "blocked", "cancelled"],
  BOARD_STATUSES: ["backlog", "todo", "in_progress", "in_review", "done", "blocked"],
  STATUS_ORDER: ["backlog", "todo", "in_progress", "in_review", "done", "blocked", "cancelled"],
  STATUS_CONFIG: {
    backlog: { label: "Backlog", iconColor: "text-muted-foreground", hoverBg: "hover:bg-accent" },
    todo: { label: "Todo", iconColor: "text-muted-foreground", hoverBg: "hover:bg-accent" },
    in_progress: { label: "In Progress", iconColor: "text-warning", hoverBg: "hover:bg-warning/10" },
    in_review: { label: "In Review", iconColor: "text-success", hoverBg: "hover:bg-success/10" },
    done: { label: "Done", iconColor: "text-info", hoverBg: "hover:bg-info/10" },
    blocked: { label: "Blocked", iconColor: "text-destructive", hoverBg: "hover:bg-destructive/10" },
    cancelled: { label: "Cancelled", iconColor: "text-muted-foreground", hoverBg: "hover:bg-accent" },
  },
  PRIORITY_ORDER: ["urgent", "high", "medium", "low", "none"],
  PRIORITY_CONFIG: {
    urgent: { label: "Urgent", bars: 4, color: "text-destructive", badgeBg: "bg-destructive/10", badgeText: "text-destructive" },
    high: { label: "High", bars: 3, color: "text-warning", badgeBg: "bg-warning/10", badgeText: "text-warning" },
    medium: { label: "Medium", bars: 2, color: "text-warning", badgeBg: "bg-warning/10", badgeText: "text-warning" },
    low: { label: "Low", bars: 1, color: "text-info", badgeBg: "bg-info/10", badgeText: "text-info" },
    none: { label: "No priority", bars: 0, color: "text-muted-foreground", badgeBg: "bg-muted", badgeText: "text-muted-foreground" },
  },
}));

// Mock recent issues store
const mockRecordVisit = vi.fn();
vi.mock("@multica/core/issues/stores", () => ({
  useRecentIssuesStore: Object.assign(
    (selector?: any) => {
      const state = { items: [], recordVisit: mockRecordVisit };
      return selector ? selector(state) : state;
    },
    { getState: () => ({ items: [], recordVisit: mockRecordVisit }) },
  ),
  useCommentCollapseStore: (selector?: any) => {
    const state = {
      collapsedByIssue: {},
      isCollapsed: () => false,
      toggle: () => {},
    };
    return selector ? selector(state) : state;
  },
}));

// Mock modals
vi.mock("@multica/core/modals", () => ({
  useModalStore: Object.assign(
    () => ({ open: vi.fn() }),
    { getState: () => ({ open: vi.fn() }) },
  ),
}));

// Mock core/utils
vi.mock("@multica/core/utils", () => ({
  timeAgo: () => "1d ago",
}));

// Mock core/hooks/use-file-upload
vi.mock("@multica/core/hooks/use-file-upload", () => ({
  useFileUpload: () => ({ uploadWithToast: vi.fn().mockResolvedValue("https://example.com/file.png") }),
}));

// Mock realtime
vi.mock("@multica/core/realtime", () => ({
  useWSEvent: vi.fn(),
  useWSReconnect: vi.fn(),
  useWS: () => ({ subscribe: vi.fn(() => () => {}), onReconnect: vi.fn(() => () => {}) }),
  WSProvider: ({ children }: { children: React.ReactNode }) => children,
  useRealtimeSync: () => {},
}));

// Mock sonner
vi.mock("sonner", () => ({
  toast: { error: vi.fn(), success: vi.fn() },
}));

// Mock react-resizable-panels (used by @multica/ui/components/ui/resizable)
vi.mock("react-resizable-panels", () => ({
  Group: ({ children, ...props }: any) => <div data-testid="panel-group" {...props}>{children}</div>,
  Panel: ({ children, ...props }: any) => <div data-testid="panel" {...props}>{children}</div>,
  Separator: ({ children, ...props }: any) => <div data-testid="panel-handle" {...props}>{children}</div>,
  useDefaultLayout: () => ({ defaultLayout: undefined, onLayoutChanged: vi.fn() }),
  usePanelRef: () => ({ current: { isCollapsed: () => false, expand: vi.fn(), collapse: vi.fn() } }),
}));

// ---------------------------------------------------------------------------
// Test data
// ---------------------------------------------------------------------------

const mockIssue: Issue = {
  id: "issue-1",
  workspace_id: "ws-1",
  number: 1,
  identifier: "TES-1",
  title: "Implement authentication",
  description: "Add JWT auth to the backend",
  status: "in_progress",
  priority: "high",
  assignee_type: "member",
  assignee_id: "user-1",
  creator_type: "member",
  creator_id: "user-1",
  parent_issue_id: null,
  project_id: null,
  position: 0,
  due_date: "2026-06-01T00:00:00Z",
  created_at: "2026-01-15T00:00:00Z",
  updated_at: "2026-01-20T00:00:00Z",
};

const mockTimeline: TimelineEntry[] = [
  {
    type: "comment",
    id: "comment-1",
    actor_type: "member",
    actor_id: "user-1",
    content: "Started working on this",
    parent_id: null,
    created_at: "2026-01-16T00:00:00Z",
    updated_at: "2026-01-16T00:00:00Z",
    comment_type: "comment",
  },
  {
    type: "comment",
    id: "comment-2",
    actor_type: "agent",
    actor_id: "agent-1",
    content: "I can help with this",
    parent_id: null,
    created_at: "2026-01-17T00:00:00Z",
    updated_at: "2026-01-17T00:00:00Z",
    comment_type: "comment",
  },
];

// ---------------------------------------------------------------------------
// Import component under test (after mocks)
// ---------------------------------------------------------------------------

import { IssueDetail } from "./issue-detail";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function createTestQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0 },
      mutations: { retry: false },
    },
  });
}

function renderIssueDetail(issueId = "issue-1") {
  const queryClient = createTestQueryClient();
  return render(
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      <QueryClientProvider client={queryClient}>
        <IssueDetail issueId={issueId} />
      </QueryClientProvider>
    </I18nProvider>,
  );
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe("IssueDetail (shared)", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockViewport.isMobile = false;
    // Default: issue loads successfully
    mockApiObj.getIssue.mockResolvedValue(mockIssue);
    // /timeline returns the entries flat in chronological order (oldest first).
    mockApiObj.listTimeline.mockResolvedValue(mockTimeline);
    mockApiObj.listIssueReactions.mockResolvedValue([]);
    mockApiObj.listIssueSubscribers.mockResolvedValue([]);
    mockApiObj.listChildIssues.mockResolvedValue({ issues: [] });
    mockApiObj.listIssues.mockResolvedValue({ issues: [], total: 0 });
    mockApiObj.getActiveTasksForIssue.mockResolvedValue({ tasks: [] });
    mockApiObj.listTasksByIssue.mockResolvedValue([]);
    mockApiObj.listMembers.mockResolvedValue([
      { user_id: "user-1", name: "Test User", email: "test@test.com", role: "admin" },
    ]);
    mockApiObj.listAgents.mockResolvedValue([]);
  });

  it("shows loading skeleton while data is loading", () => {
    // Make the API hang to keep loading state
    mockApiObj.getIssue.mockReturnValue(new Promise(() => {}));
    renderIssueDetail();

    expect(
      screen.getAllByRole("generic").some((el) => el.getAttribute("data-slot") === "skeleton"),
    ).toBe(true);
  });

  it("renders issue title and description after loading", async () => {
    renderIssueDetail();

    await waitFor(() => {
      expect(screen.getByDisplayValue("Implement authentication")).toBeInTheDocument();
    });

    expect(screen.getByDisplayValue("Add JWT auth to the backend")).toBeInTheDocument();
  });

  it("renders workspace name as breadcrumb link", async () => {
    renderIssueDetail();

    await waitFor(() => {
      expect(screen.getByText("Test WS")).toBeInTheDocument();
    });

    const wsLink = screen.getByText("Test WS");
    // After the URL-driven workspace refactor, issue paths are scoped under
    // /<workspaceSlug>/issues.
    expect(wsLink.closest("a")).toHaveAttribute("href", "/test/issues");
  });

  it("renders properties sidebar with status, priority, assignee, due date", async () => {
    renderIssueDetail();

    await waitFor(() => {
      expect(screen.getByText("Properties")).toBeInTheDocument();
    });

    expect(screen.getByText("Status")).toBeInTheDocument();
    expect(screen.getByText("Priority")).toBeInTheDocument();
    expect(screen.getByText("Assignee")).toBeInTheDocument();
    expect(screen.getByText("Due date")).toBeInTheDocument();
  });

  it("uses a non-resizable layout with the sidebar sheet closed by default on mobile", async () => {
    mockViewport.isMobile = true;

    renderIssueDetail();

    await waitFor(() => {
      expect(screen.getByDisplayValue("Implement authentication")).toBeInTheDocument();
    });

    expect(screen.queryByTestId("panel-group")).not.toBeInTheDocument();
    expect(screen.queryByText("Properties")).not.toBeInTheDocument();
  });

  it("renders Details section with Created by and dates", async () => {
    renderIssueDetail();

    await waitFor(() => {
      expect(screen.getByText("Details")).toBeInTheDocument();
    });

    expect(screen.getByText("Created by")).toBeInTheDocument();
    expect(screen.getByText("Created")).toBeInTheDocument();
    expect(screen.getByText("Updated")).toBeInTheDocument();
  });

  it("shows 'not found' message when issue does not exist", async () => {
    mockApiObj.getIssue.mockRejectedValue(new Error("Not found"));

    renderIssueDetail("nonexistent-id");

    await waitFor(() => {
      expect(
        screen.getByText("This issue does not exist or has been deleted in this workspace."),
      ).toBeInTheDocument();
    });
  });

  it("shows 'Back to Issues' button when issue is not found and no onDelete prop", async () => {
    mockApiObj.getIssue.mockRejectedValue(new Error("Not found"));

    renderIssueDetail("nonexistent-id");

    await waitFor(() => {
      expect(screen.getByText("Back to Issues")).toBeInTheDocument();
    });
  });

  it("renders Activity section header", async () => {
    renderIssueDetail();

    await waitFor(() => {
      expect(screen.getAllByText("Activity").length).toBeGreaterThanOrEqual(1);
    });
  });

  it("renders comments from timeline", async () => {
    renderIssueDetail();

    await waitFor(() => {
      expect(screen.getByText("Started working on this")).toBeInTheDocument();
    });

    expect(screen.getByText("I can help with this")).toBeInTheDocument();
  });

  it("sends empty description when editor is cleared", async () => {
    renderIssueDetail();

    await waitFor(() => {
      expect(screen.getByDisplayValue("Add JWT auth to the backend")).toBeInTheDocument();
    });

    const editor = screen.getByPlaceholderText("Add description...");
    fireEvent.change(editor, { target: { value: "" } });

    await waitFor(() => {
      expect(mockApiObj.updateIssue).toHaveBeenCalledWith(
        "issue-1",
        expect.objectContaining({ description: "" }),
      );
    });
  });

  it("surfaces orchestration evaluator reason from evaluation events", async () => {
    mockApiObj.getIssueOrchestration.mockResolvedValue({
      plans: [
        {
          id: "plan-1",
          workspace_id: "ws-1",
          source_type: "issue",
          source_id: "issue-1",
          objective: "Implement authentication",
          status: "running",
          policy: {},
          metadata: {},
          created_by_type: "member",
          created_by_id: "user-1",
          created_at: "2026-05-11T00:00:00Z",
          updated_at: "2026-05-11T00:00:00Z",
        },
      ],
      nodes: [
        {
          id: "node-1",
          plan_id: "plan-1",
          parent_node_id: null,
          type: "implement",
          title: "Implement authentication",
          description: null,
          status: "evaluating",
          assignee_agent_id: "agent-1",
          input_contract: {},
          output_contract: {},
          attempt_count: 1,
          max_attempts: 2,
          summary: {
            status: "evaluating",
            reason_code: "evidence_insufficient",
            reason_title: "Evidence insufficient",
            reason_detail: "Structured result payload did not satisfy the orchestration result contract.",
            recommended_action: "retry",
            action_enabled: true,
            attempt_count: 1,
            max_attempts: 2,
            latest_evaluation_status: "evidence_insufficient",
          },
          position_x: null,
          position_y: null,
          created_at: "2026-05-11T00:00:00Z",
          updated_at: "2026-05-11T00:00:00Z",
        },
      ],
      events: [
        {
          id: "event-1",
          plan_id: "plan-1",
          node_id: "node-1",
          task_id: "task-1",
          event_type: "evaluation.invalid_result",
          actor_type: "kernel",
          actor_id: null,
          payload: { reason: "evidence_insufficient" },
          created_at: "2026-05-11T00:00:00Z",
        },
      ],
      artifacts: [],
    });

    renderIssueDetail();

    await waitFor(() => {
      expect(screen.getByText("Orchestration")).toBeInTheDocument();
    });

    expect(screen.getAllByText("Evidence insufficient").length).toBeGreaterThan(0);
    expect(screen.getByText("Current status")).toBeInTheDocument();
    expect(screen.getByText("Why this state")).toBeInTheDocument();
  });

  it("renders summary-backed orchestration decision details when node summary is present", async () => {
    mockApiObj.getIssueOrchestration.mockResolvedValue({
      plans: [
        {
          id: "plan-1",
          workspace_id: "ws-1",
          source_type: "issue",
          source_id: "issue-1",
          objective: "Implement authentication",
          status: "waiting_human",
          policy: {},
          metadata: {},
          created_by_type: "member",
          created_by_id: "user-1",
          created_at: "2026-05-11T00:00:00Z",
          updated_at: "2026-05-11T00:03:00Z",
        },
      ],
      nodes: [
        {
          id: "node-1",
          plan_id: "plan-1",
          parent_node_id: null,
          type: "implement",
          title: "Implement JWT auth",
          description: null,
          status: "waiting_human",
          assignee_agent_id: "agent-1",
          input_contract: {},
          output_contract: {},
          attempt_count: 1,
          max_attempts: 2,
          summary: {
            status: "waiting_human",
            reason_code: "waiting_for_approval",
            reason_title: "Approval required",
            reason_detail: "Kernel evaluation requires human approval before marking this node complete.",
            recommended_action: "approve",
            action_enabled: true,
            attempt_count: 1,
            max_attempts: 2,
            latest_evaluation_status: "waiting_human",
            latest_agent_summary: "Implementation is ready; waiting for sign-off.",
            prior_evidence_summary: "Previous attempt lacked criteria evidence.",
            updated_at: "2026-05-11T00:03:00Z",
          },
          permissions: {
            can_approve: true,
            can_request_changes: true,
            can_retry: false,
          },
          approval_history: [
            {
              action: "request_changes",
              actor_type: "member",
              actor_id: "user-1",
              created_at: "2026-05-11T00:02:30Z",
              change_request: "Add rollback notes before approval.",
            },
          ],
          position_x: null,
          position_y: null,
          created_at: "2026-05-11T00:01:00Z",
          updated_at: "2026-05-11T00:03:00Z",
        },
      ],
      events: [],
      artifacts: [],
    });

    renderIssueDetail();

    await waitFor(() => {
      expect(screen.getByText("Waiting for approval")).toBeInTheDocument();
    });

    expect(screen.getByText("Recommended action")).toBeInTheDocument();
    expect(screen.getAllByText("Approve").length).toBeGreaterThan(0);
    expect(screen.getByText("Implementation is ready; waiting for sign-off.")).toBeInTheDocument();
    expect(screen.getByText("Prior evidence summary")).toBeInTheDocument();
    expect(screen.getByText("Previous attempt lacked criteria evidence.")).toBeInTheDocument();
    expect(screen.getByText("Add rollback notes before approval.")).toBeInTheDocument();
  });

  it("shows approval controls only when permissions and recommended action allow them", async () => {
    mockApiObj.getIssueOrchestration.mockResolvedValue({
      plans: [
        {
          id: "plan-1",
          workspace_id: "ws-1",
          source_type: "issue",
          source_id: "issue-1",
          objective: "Implement authentication",
          status: "waiting_human",
          policy: {},
          metadata: {},
          created_by_type: "member",
          created_by_id: "user-1",
          created_at: "2026-05-11T00:00:00Z",
          updated_at: "2026-05-11T00:03:00Z",
        },
      ],
      nodes: [
        {
          id: "node-1",
          plan_id: "plan-1",
          type: "implement",
          title: "Implement JWT auth",
          description: null,
          status: "waiting_human",
          assignee_agent_id: "agent-1",
          input_contract: {},
          output_contract: {},
          evaluator_policy: {},
          retry_policy: {},
          runtime_constraints: {},
          attempt_count: 1,
          max_attempts: 2,
          linked_task_id: "task-1",
          artifact_count: 1,
          summary: {
            status: "waiting_human",
            reason_code: "waiting_for_approval",
            reason_title: "Approval required",
            reason_detail: "Kernel evaluation requires human approval before marking this node complete.",
            recommended_action: "approve",
            action_enabled: true,
            attempt_count: 1,
            max_attempts: 2,
          },
          permissions: {
            can_approve: true,
            can_request_changes: false,
            can_retry: false,
          },
          approval_history: [],
          started_at: null,
          completed_at: null,
          created_at: "2026-05-11T00:01:00Z",
          updated_at: "2026-05-11T00:03:00Z",
        },
      ],
      events: [],
      artifacts: [],
    });

    renderIssueDetail();

    await waitFor(() => {
      expect(screen.getAllByText("Approve").length).toBeGreaterThan(0);
    });
    expect(screen.queryByText("Request changes")).not.toBeInTheDocument();
  });

  it("shows request-changes control when the server grants that permission", async () => {
    mockApiObj.getIssueOrchestration.mockResolvedValue({
      plans: [
        {
          id: "plan-1",
          workspace_id: "ws-1",
          source_type: "issue",
          source_id: "issue-1",
          objective: "Implement authentication",
          status: "waiting_human",
          policy: {},
          metadata: {},
          created_by_type: "member",
          created_by_id: "user-1",
          created_at: "2026-05-11T00:00:00Z",
          updated_at: "2026-05-11T00:03:00Z",
        },
      ],
      nodes: [
        {
          id: "node-1",
          plan_id: "plan-1",
          type: "implement",
          title: "Implement JWT auth",
          description: null,
          status: "waiting_human",
          assignee_agent_id: "agent-1",
          input_contract: {},
          output_contract: {},
          evaluator_policy: {},
          retry_policy: {},
          runtime_constraints: {},
          attempt_count: 1,
          max_attempts: 2,
          linked_task_id: "task-1",
          artifact_count: 1,
          summary: {
            status: "waiting_human",
            reason_code: "waiting_for_approval",
            reason_title: "Approval required",
            reason_detail: "Kernel evaluation requires human approval before marking this node complete.",
            recommended_action: "approve",
            action_enabled: true,
            attempt_count: 1,
            max_attempts: 2,
          },
          permissions: {
            can_approve: false,
            can_request_changes: true,
            can_retry: false,
          },
          approval_history: [],
          started_at: null,
          completed_at: null,
          created_at: "2026-05-11T00:01:00Z",
          updated_at: "2026-05-11T00:03:00Z",
        },
      ],
      events: [],
      artifacts: [],
    });

    renderIssueDetail();

    await waitFor(() => {
      expect(screen.getByText("Request changes")).toBeInTheDocument();
    });
    expect(screen.queryByRole("button", { name: "Approve" })).not.toBeInTheDocument();
  });

  it("renders orchestration process details for nodes, events, and artifacts", async () => {
    mockApiObj.getIssueOrchestration.mockResolvedValue({
      plans: [
        {
          id: "plan-1",
          workspace_id: "ws-1",
          source_type: "issue",
          source_id: "issue-1",
          objective: "Implement authentication",
          status: "running",
          policy: {},
          metadata: {},
          created_by_type: "member",
          created_by_id: "user-1",
          created_at: "2026-05-11T00:00:00Z",
          updated_at: "2026-05-11T00:03:00Z",
        },
      ],
      nodes: [
        {
          id: "node-1",
          plan_id: "plan-1",
          parent_node_id: null,
          type: "inspect",
          title: "Inspect current auth flow",
          description: null,
          status: "completed",
          assignee_agent_id: "agent-1",
          input_contract: {},
          output_contract: {},
          attempt_count: 1,
          max_attempts: 2,
          linked_task_id: "task-1",
          artifact_count: 0,
          position_x: null,
          position_y: null,
          created_at: "2026-05-11T00:00:00Z",
          updated_at: "2026-05-11T00:01:00Z",
        },
        {
          id: "node-2",
          plan_id: "plan-1",
          parent_node_id: null,
          type: "implement",
          title: "Implement JWT auth",
          description: null,
          status: "waiting_human",
          assignee_agent_id: "agent-1",
          input_contract: {},
          output_contract: {},
          attempt_count: 2,
          max_attempts: 2,
          linked_task_id: "task-2",
          artifact_count: 1,
          summary: {
            status: "waiting_human",
            reason_code: "waiting_for_approval",
            reason_title: "Approval required",
            reason_detail: "Kernel evaluation requires human approval before marking this node complete.",
            recommended_action: "approve",
            action_enabled: true,
            attempt_count: 2,
            max_attempts: 2,
            latest_evaluation_status: "waiting_human",
            latest_agent_summary: "Waiting for sign-off.",
            prior_evidence_summary: "Previous attempt lacked criteria evidence.",
          },
          position_x: null,
          position_y: null,
          created_at: "2026-05-11T00:01:00Z",
          updated_at: "2026-05-11T00:03:00Z",
        },
      ],
      events: [
        {
          id: "event-1",
          plan_id: "plan-1",
          node_id: "node-1",
          task_id: "task-1",
          event_type: "node.dispatched",
          actor_type: "kernel",
          actor_id: null,
          payload: { attempt_count: 1 },
          created_at: "2026-05-11T00:00:10Z",
        },
        {
          id: "event-2",
          plan_id: "plan-1",
          node_id: "node-2",
          task_id: "task-2",
          event_type: "evaluation.waiting_human",
          actor_type: "kernel",
          actor_id: null,
          payload: { reason: "need_human_review" },
          created_at: "2026-05-11T00:03:00Z",
        },
      ],
      artifacts: [
        {
          id: "artifact-1",
          plan_id: "plan-1",
          node_id: "node-2",
          task_id: "task-2",
          type: "changed_files",
          uri: null,
          content: {},
          metadata: { count: 3 },
          content_hash: null,
          created_at: "2026-05-11T00:02:30Z",
        },
      ],
    });

    renderIssueDetail();

    await waitFor(() => {
      expect(screen.getByText("Orchestration")).toBeInTheDocument();
    });

    await waitFor(() => {
      expect(screen.getByText("Nodes")).toBeInTheDocument();
    });

    expect(screen.getByText("Inspect current auth flow")).toBeInTheDocument();
    expect(screen.getAllByText("Implement JWT auth")).toHaveLength(2);
    expect(screen.getByText("task-2")).toBeInTheDocument();
    expect(screen.getByText("1 evidence")).toBeInTheDocument();
    expect(screen.getByText("Events")).toBeInTheDocument();
    expect(screen.getByText("node.dispatched")).toBeInTheDocument();
    expect(screen.getByText("evaluation.waiting_human")).toBeInTheDocument();
    expect(screen.getAllByText("Artifacts")).toHaveLength(2);
    expect(screen.getByText("changed_files")).toBeInTheDocument();
  });
});
