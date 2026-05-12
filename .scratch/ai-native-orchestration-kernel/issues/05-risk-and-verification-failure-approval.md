# Risk-bearing or failed verification pauses for human approval

Status: ready-for-agent
Type: AFK
Risk: High

## What to build

Build the conditional Approval Gate path. When verification finds non-empty risks, failed tests, unverifiable results, destructive-operation evidence, or retry exhaustion, the Orchestration Run should pause for human approval rather than auto-succeeding or auto-retrying risky work.

The approval action API should support `approve`, `retry`, `request_changes`, and `cancel`, enforce human-only approval permissions, and record each action as a Kernel Event.

## Acceptance criteria

- [ ] Risk-bearing evidence or failed verification moves the relevant node/run into `waiting_for_approval`.
- [ ] The approval action API supports `approve`, `retry`, `request_changes`, and `cancel`.
- [ ] Workspace owners/admins, Issue creator, and Issue human assignee can act; agent assignees cannot approve their own orchestration.
- [ ] `request_changes` records an audited Change Request and passes it into the next node attempt's Orchestration Context without rewriting the Issue description.
- [ ] The read API exposes permission flags, reason code, recommended action, and approval history.
- [ ] Tests cover approval permission boundaries, each approval action, risk-to-approval routing, request-changes context propagation, and event persistence.

## Agent / human ownership

Suitable for agent implementation. Human review recommended because this defines human accountability behavior.

## Blocked by

- 03-completed-task-records-evidence-and-verifies
