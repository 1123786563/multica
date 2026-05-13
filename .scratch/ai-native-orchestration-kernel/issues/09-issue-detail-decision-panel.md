# Issue Detail renders the orchestration Decision Panel

Status: done
Type: HITL
Risk: Medium

## What to build

Build the first Issue Detail UI for orchestration. The UI should be node-centered, showing what is happening, why it is blocked or complete, what action is recommended, and what evidence exists. Raw Kernel Events and Node Evidence should be available as expandable details, not the primary display.

This slice should use shared `packages/views` UI so web and desktop share behavior, and use React Query for server state.

## Acceptance criteria

- [x] Issue Detail shows a Decision Panel when orchestration data exists for the Issue.
- [x] Each node displays status, reason code, recommended action, latest summary, attempts, evidence count, and linked task status.
- [x] Users can expand node details to inspect Kernel Events, Node Evidence, and linked task/message references.
- [x] Approval controls render only when the server says the user has permission and a matching recommended action exists.
- [x] The UI uses shared views and respects existing package boundaries; web/desktop do not duplicate orchestration logic.
- [x] Frontend tests cover normal state, waiting approval, evidence insufficient, completed state, missing optional fields, and approval-control visibility.

## Agent / human ownership

Requires human review for UI/UX and product wording. Implementation can be agent-assisted after the design review.

## Blocked by

- 07-decision-panel-read-api
- 08-orchestration-refresh-ws
