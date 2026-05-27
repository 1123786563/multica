# ADR 0020: Worker-scoped Eino reasoning provider

## Status

Accepted

## Context

The production Eino reasoning path covers analysis, advisory review, and summarization inside the fixed Temporal workflow. A tempting alternative is to reuse Multica Agent Runtime providers or workspace-owned provider settings, but those providers execute Agent Tasks through daemon runtimes and carry different ownership, secret, usage, and policy boundaries.

## Decision

Eino reasoning uses a worker-scoped provider configuration owned by the orchestration worker deployment. The MVP starts with a single OpenAI-compatible ChatModel provider selected through a lightweight factory, requires an equivalent strict structured-output contract for each Eino reasoning node, and does not support prompt-only JSON fallback, workspace provider selection, database-backed provider registries, or Agent Runtime provider reuse.

Each Orchestration Run binds a semantic Eino reasoning profile reference in Temporal workflow input at run start and projects it for visibility. The MVP preloads a single default profile reference without a profile registry. Later provider, model, or prompt profile changes affect only new runs; existing runs fail closed if the bound profile is unavailable instead of switching to the current default profile.

Eino provider traces are projected as orchestration artifact evidence with parsed structured output, safe historical profile metadata, and raw usage counters. They do not store raw prompts, raw provider responses, API keys, or cost calculations.

## Consequences

The API server and workspace database are not the default Eino secret owners. Future workspace policy may allow, disable, or constrain reasoning profiles, context-sending rules, human-gate behavior, and audit retention, but it should not directly edit prompts or store provider credentials by default.

Provider adapters may use native JSON Schema, tool calling, or equivalent validation, but they must enforce the same strict contract. Providers that only support weak JSON mode or best-effort prose parsing are incompatible with the production Eino reasoning path.
