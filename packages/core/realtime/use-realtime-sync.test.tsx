// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach } from "vitest";
import { renderHook, act, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { create } from "zustand";

import { useRealtimeSync } from "./use-realtime-sync";
import { setCurrentWorkspace } from "../platform/workspace-storage";
import { issueKeys } from "../issues/queries";

vi.mock("../paths", () => ({
  useHasOnboarded: () => true,
  resolvePostAuthDestination: () => "/",
}));

class FakeWSClient {
  private anyHandlers = new Set<(msg: { type: string; payload: unknown }) => void>();
  onAny(handler: (msg: { type: string; payload: unknown }) => void) {
    this.anyHandlers.add(handler);
    return () => {
      this.anyHandlers.delete(handler);
    };
  }
  on() {
    return () => {};
  }
  onReconnect() {
    return () => {};
  }
  emit(type: string, payload: unknown) {
    for (const handler of this.anyHandlers) {
      handler({ type, payload });
    }
  }
}

function createAuthStore() {
  return create(() => ({
    user: { id: "user-1" },
    isLoading: false,
  }));
}

describe("useRealtimeSync", () => {
  beforeEach(() => {
    setCurrentWorkspace("test", "ws-1");
  });

  it("invalidates issue orchestration queries on orchestration:updated", async () => {
    const ws = new FakeWSClient();
    const authStore = createAuthStore();
    const queryClient = new QueryClient({
      defaultOptions: {
        queries: { retry: false },
      },
    });
    const issueId = "issue-1";

    queryClient.setQueryData(issueKeys.orchestration(issueId), {
      plans: [],
      nodes: [],
      events: [],
      artifacts: [],
    });

    const wrapper = ({ children }: { children: React.ReactNode }) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    );

    renderHook(() => useRealtimeSync(ws as never, { authStore } as never), { wrapper });

    const before = queryClient.getQueryState(issueKeys.orchestration(issueId));
    const authStateBefore = authStore.getState();
    expect(before?.isInvalidated).toBe(false);

    act(() => {
      ws.emit("orchestration:updated", {
        issue_id: issueId,
        run_id: "plan-1",
        changed_at: "2026-05-13T00:00:00Z",
      });
    });

    await waitFor(() => {
      const after = queryClient.getQueryState(issueKeys.orchestration(issueId));
      expect(after?.isInvalidated).toBe(true);
    });
    expect(authStore.getState()).toBe(authStateBefore);
  });
});
