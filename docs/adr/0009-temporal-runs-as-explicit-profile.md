# ADR 0009: Temporal runs as an explicit profile

## Status

Accepted

## Context

Temporal is a new infrastructure dependency for the orchestration MVP. Adding it to the default Multica API process or default `make dev` path would expand setup and startup requirements for every developer before the Temporal path is proven.

## Decision

Temporal Server is an explicitly configured external dependency, and orchestration workflows run in a separate orchestration worker process. The Multica API connects to Temporal through configuration such as address, namespace, and task queue. Default `make dev` does not require or start Temporal. If Temporal is not configured or unavailable, orchestration start returns an explicit unavailable error instead of silently falling back to the old DB-owned kernel path.

## Consequences

Local development should document an explicit Temporal setup path, such as `temporal server start-dev` or a docker compose profile, plus a separate worker command. CI can start with workflow/activity unit tests and mocked Temporal clients; full Temporal integration tests belong behind an explicit integration profile.
