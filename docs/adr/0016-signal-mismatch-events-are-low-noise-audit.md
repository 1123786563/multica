# ADR 0016: Signal mismatch events are low-noise audit events

Status: Accepted

## Context

The Agent Task outcome signal contract rejects stale, duplicate, and mismatched outcomes. These facts are important for debugging daemon callbacks, repair jobs, and retry races, but surfacing every ignored callback as a primary user-facing error would make the Issue Detail orchestration panel noisy.

## Decision

The MVP records ignored or rejected Agent Task outcome signals as low-noise audit events:

- `signal.duplicate_ignored`
- `signal.stale_ignored`
- `signal.mismatched_rejected`

These events are included in `orchestration_event` and appear in expanded event detail by default. They do not make the Decision Panel show a primary error by themselves.

The Decision Panel escalates only when the current waiting node has no valid signal within policy, repair or reconciliation fails, or the Workflow itself enters `failed` or `waiting_human`.

## Consequences

Operators keep the evidence needed to debug callback races and stale outcomes without flooding normal users with repeated signal noise. Tests should assert both audit-event projection and Decision Panel non-escalation for isolated ignored signals.
