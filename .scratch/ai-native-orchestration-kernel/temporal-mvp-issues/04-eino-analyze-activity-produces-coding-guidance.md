# Eino analyze activity produces coding guidance

Label: ready-for-agent
Type: AFK
Risk: Medium

## Parent

.scratch/ai-native-orchestration-kernel/temporal-mvp-prd-github-issue.md

## What to build

Build the first Eino reasoning slice inside the fixed Temporal workflow. Eino should analyze the Issue and produce structured execution advice plus a recommended coding prompt for the daemon-backed Agent Task, without generating or mutating workflow topology.

The generated reasoning output should be projected so users can inspect why the coding task was dispatched with that guidance.

## Acceptance criteria

- [ ] The Eino adapter exposes an analyze activity that returns problem summary, execution advice, suspected context, risks, and recommended agent prompt.
- [ ] Eino analysis is invoked only from an Activity, not directly from Workflow code.
- [ ] Eino output is projected into node detail or evidence so Issue Detail can show the guidance.
- [ ] Eino cannot add, remove, reorder, branch, or loop workflow nodes in this slice.
- [ ] Tests cover mocked Eino output, malformed Eino output, projection of advice, and topology immutability.

## Blocked by

- 03-projection-activities-render-fixed-workflow-progress

