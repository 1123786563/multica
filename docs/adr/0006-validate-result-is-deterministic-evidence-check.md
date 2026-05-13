# ADR 0006: Validate result is deterministic evidence check

## Status

Accepted

## Context

The MVP fixed workflow includes a `validate_result` step after the coding Agent Task outcome signal. Running lint or tests as a separate daemon step would introduce a second execution node, workspace reuse rules, command policy, security boundaries, and result ownership questions before the Temporal bridge is proven.

## Decision

`validate_result` is a deterministic Temporal Activity that validates the Agent Task structured result and projected evidence. It checks schema version, required evidence fields, summary, changed files, artifacts, tests, and risks. It does not run additional daemon commands, invoke a separate test runner, or ask Eino to decide whether evidence is valid.

## Consequences

The coding Agent Task remains responsible for running relevant tests and reporting test results in the structured result. Missing schema, malformed evidence, failed tests, risks, or incomplete evidence route the workflow to review, approval, retry, or failure according to policy, but validation itself is not another code-execution step.
