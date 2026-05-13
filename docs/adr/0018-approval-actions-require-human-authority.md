# ADR 0018: Approval actions require human authority

Status: Accepted

## Context

The orchestration MVP can pause in an Approval Gate when risk, failed tests, unverifiable evidence, retry exhaustion, or policy requires human judgment. Approval actions change workflow direction, accept risk, create a retry attempt, or cancel active work.

If agents can approve, retry, or cancel their own orchestration runs, the system loses the human accountability boundary that Approval Gate is meant to provide.

## Decision

Approval Actions require an authorized human actor.

Allowed actors:

- workspace owner;
- workspace admin;
- Issue creator;
- human Issue assignee.

Disallowed actors:

- agent assignee;
- the agent that initiated the run;
- the agent that executed the run;
- users or agents without Issue visibility.

The MVP supports only `approve`, `retry`, and `cancel`; all three actions use the same permission boundary.

Every Approval Action writes an approval audit event with:

- `actor_id`
- `actor_type=human`
- `action`
- `reason`
- `plan_id`
- `node_id`

## Consequences

Agents cannot self-approve risk acceptance, retry their own failed work through the approval path, or cancel their own orchestration run. Tests must cover allowed human actors, denied agent actors, denied non-members, audit event payloads, and Temporal signal or cancellation dispatch after audit persistence.
