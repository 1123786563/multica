# Human-only approval actions with audit events

Label: ready-for-agent
Type: AFK
Risk: High

## Parent

.scratch/ai-native-orchestration-kernel/temporal-mvp-prd-github-issue.md

## What to build

Build MVP Approval Actions for `approve`, `retry`, and `cancel`. Approval actions must require authorized human actors, deny agent self-approval/self-retry/self-cancel, write an approval audit event with actor and reason metadata, and then signal or update Temporal.

This slice should prove approval accountability through API, projection, permission checks, and UI controls.

## Acceptance criteria

- [ ] Approval endpoints accept approve, retry, and cancel only; request changes is not supported.
- [ ] Workspace owner, workspace admin, Issue creator, and human Issue assignee can perform allowed Approval Actions.
- [ ] Agent assignee, initiating agent, executing agent, unrelated members, and non-human actors are denied.
- [ ] Every Approval Action writes an audit event before dispatching Temporal Signal/Update or cancellation request.
- [ ] Audit payload includes actor identity, `actor_type=human`, action, reason, plan identity, and node identity.
- [ ] Linear Orchestration Panel renders approval buttons only when server-projected permission and action allow them.
- [ ] Tests cover allowed actors, denied agent actors, denied unrelated actors, audit payload, UI button visibility, and Temporal dispatch after audit persistence.

## Blocked by

- 08-outcome-policy-routes-risks-and-failed-tests-to-approval-gate

