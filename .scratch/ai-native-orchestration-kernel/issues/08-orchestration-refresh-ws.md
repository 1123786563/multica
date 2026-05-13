# WebSocket refresh keeps Issue Detail orchestration state current

Status: done
Type: AFK
Risk: Medium

## What to build

Build the coarse WebSocket refresh path for orchestration changes. Persisted Kernel Events remain the source of truth; the WebSocket event should only notify clients that issue-scoped orchestration data changed so React Query can refresh.

This slice should integrate with the existing realtime conventions and avoid duplicating server state into Zustand.

## Acceptance criteria

- [x] The server emits `orchestration:updated` after committed orchestration changes that affect Issue Detail.
- [x] The event payload includes at least `issue_id`, `run_id`, and `changed_at`.
- [x] The frontend/core realtime layer invalidates the issue orchestration query when the event arrives.
- [x] Existing `task:message` behavior remains responsible for live Agent Task messages.
- [x] No run/node/event/evidence server state is stored in Zustand; only local UI state may be stored there.
- [x] Tests cover server event emission after commit, client query invalidation, and no direct Zustand writes for orchestration facts.

## Agent / human ownership

Suitable for agent implementation.

## Blocked by

- 07-decision-panel-read-api
