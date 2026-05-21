import { describe, expect, it } from "vitest";
import type { IssueOrchestration } from "../types";
import { issueOrchestrationOptions } from "./queries";

describe("issueOrchestrationOptions", () => {
  it("polls while an orchestration plan is active and stops for terminal history", () => {
    const options = issueOrchestrationOptions("ws-1", "issue-1");
    const refetchInterval = options.refetchInterval;
    if (typeof refetchInterval !== "function") {
      throw new Error("expected orchestration query to use data-aware refetch interval");
    }

    const activeData: IssueOrchestration = {
      plans: [
        {
          id: "plan-1",
          issue_id: "issue-1",
          status: "running",
          workflow_type: "issue_mvp",
          projection_version: 1,
          created_at: "",
          updated_at: "",
          summary: { reason_code: "", recommended_action: "none" },
          available_actions: [],
          nodes: [],
          events: [],
          artifacts: [],
        },
      ],
    };
    const terminalData: IssueOrchestration = {
      plans: [{ ...activeData.plans[0]!, status: "completed" }],
    };

    expect(refetchInterval({ state: { data: activeData } } as never)).toBe(1500);
    expect(refetchInterval({ state: { data: terminalData } } as never)).toBe(false);
  });
});
