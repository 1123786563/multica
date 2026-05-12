import { z } from "zod";
import type { IssueOrchestration, ListIssuesResponse, TimelineEntry } from "../types";

// ---------------------------------------------------------------------------
// Schemas for the highest-risk API endpoints — those whose responses drive
// the issue detail page (timeline, comments, subscribers) and the issues
// list. These are the surfaces that white-screened in #2143 / #2147 / #2192.
//
// These schemas are intentionally LENIENT:
//   - String enums are stored as `z.string()` rather than `z.enum([...])`.
//     A new server-side enum value should render as a generic fallback in
//     the UI, never crash a `safeParse`.
//   - Optional fields are unioned with `null` and given fallbacks where
//     existing UI code already coerces them.
//   - Arrays default to `[]` so a missing `reactions` / `attachments` /
//     `entries` field doesn't take the page down.
//   - Every object schema ends with `.loose()` so unknown server-side
//     fields pass through unchanged. zod 4's `.object()` defaults to STRIP,
//     which would silently delete fields the schema didn't explicitly list
//     — fine while the TS type doesn't claim them, but the moment a future
//     PR adds a TS field without updating the schema, the cast `as T` lies
//     and the field shows up as `undefined` at runtime. `.loose()` removes
//     that synchronisation hazard.
//
// These schemas are deliberately not typed as `z.ZodType<TimelineEntry>` /
// `z.ZodType<Issue>` etc. — the strict TS types narrow string fields to
// literal unions, which would defeat the leniency above. `parseWithFallback`
// returns the parsed value cast to the caller-supplied `T`, so the strict
// type still flows out at the call site; the schema only guards shape.
// ---------------------------------------------------------------------------

const ReactionSchema = z.object({
  id: z.string(),
  comment_id: z.string(),
  actor_type: z.string(),
  actor_id: z.string(),
  emoji: z.string(),
  created_at: z.string(),
});

const AttachmentSchema = z.object({
  id: z.string(),
}).loose();

// All object schemas use `.loose()` so unknown server-side fields pass
// through unchanged. zod 4's `.object()` defaults to STRIP, which would
// silently drop new fields and surface as a "field neither showed up in
// the UI" mystery the next time the TS type adopted them but the schema
// wasn't updated in lock-step. `.loose()` removes that synchronisation
// hazard — the schema validates the shape it knows about and leaves the
// rest alone.
const TimelineEntrySchema = z.object({
  type: z.string(),
  id: z.string(),
  actor_type: z.string(),
  actor_id: z.string(),
  created_at: z.string(),
  action: z.string().optional(),
  details: z.record(z.string(), z.unknown()).optional(),
  content: z.string().optional(),
  parent_id: z.string().nullable().optional(),
  updated_at: z.string().optional(),
  comment_type: z.string().optional(),
  reactions: z.array(ReactionSchema).optional(),
  attachments: z.array(AttachmentSchema).optional(),
  coalesced_count: z.number().optional(),
}).loose();

// /timeline returns a flat array of TimelineEntry, oldest first. The
// previously cursor-paginated wrapper was removed (#1929) — at observed data
// sizes (p99 ~30 entries per issue) paged delivery only created bugs.
export const TimelineEntriesSchema = z.array(TimelineEntrySchema);

export const EMPTY_TIMELINE_ENTRIES: TimelineEntry[] = [];

export const CommentSchema = z.object({
  id: z.string(),
  issue_id: z.string(),
  author_type: z.string(),
  author_id: z.string(),
  content: z.string(),
  type: z.string(),
  parent_id: z.string().nullable(),
  reactions: z.array(ReactionSchema).default([]),
  attachments: z.array(AttachmentSchema).default([]),
  created_at: z.string(),
  updated_at: z.string(),
}).loose();

export const CommentsListSchema = z.array(CommentSchema);

const IssueSchema = z.object({
  id: z.string(),
  workspace_id: z.string(),
  number: z.number(),
  identifier: z.string(),
  title: z.string(),
  description: z.string().nullable(),
  status: z.string(),
  priority: z.string(),
  assignee_type: z.string().nullable(),
  assignee_id: z.string().nullable(),
  creator_type: z.string(),
  creator_id: z.string(),
  parent_issue_id: z.string().nullable(),
  project_id: z.string().nullable(),
  position: z.number(),
  due_date: z.string().nullable(),
  reactions: z.array(z.unknown()).optional(),
  labels: z.array(z.unknown()).optional(),
  created_at: z.string(),
  updated_at: z.string(),
}).loose();

export const ListIssuesResponseSchema = z.object({
  issues: z.array(IssueSchema).default([]),
  total: z.number().default(0),
}).loose();

export const EMPTY_LIST_ISSUES_RESPONSE: ListIssuesResponse = {
  issues: [],
  total: 0,
};

const SubscriberSchema = z.object({
  issue_id: z.string(),
  user_type: z.string(),
  user_id: z.string(),
  reason: z.string(),
  created_at: z.string(),
}).loose();

export const SubscribersListSchema = z.array(SubscriberSchema);

export const ChildIssuesResponseSchema = z.object({
  issues: z.array(IssueSchema).default([]),
}).loose();

const OrchestrationPlanSchema = z.object({
  id: z.string(),
  workspace_id: z.string(),
  source_type: z.string(),
  source_id: z.string(),
  objective: z.string(),
  status: z.string(),
  policy: z.record(z.string(), z.unknown()).default({}),
  metadata: z.record(z.string(), z.unknown()).default({}),
  created_by_type: z.string().nullable(),
  created_by_id: z.string().nullable(),
  created_at: z.string(),
  updated_at: z.string(),
}).loose();

const OrchestrationNodeSchema = z.object({
  id: z.string(),
  plan_id: z.string(),
  type: z.string(),
  title: z.string(),
  description: z.string().nullable(),
  status: z.string(),
  assignee_agent_id: z.string().nullable(),
  input_contract: z.record(z.string(), z.unknown()).default({}),
  output_contract: z.record(z.string(), z.unknown()).default({}),
  evaluator_policy: z.record(z.string(), z.unknown()).default({}),
  retry_policy: z.record(z.string(), z.unknown()).default({}),
  runtime_constraints: z.record(z.string(), z.unknown()).default({}),
  attempt_count: z.number().default(0),
  max_attempts: z.number().default(0),
  summary: z.object({
    status: z.string().default(""),
    reason_code: z.string().default(""),
    reason_title: z.string().default(""),
    reason_detail: z.string().default(""),
    recommended_action: z.string().default("none"),
    action_enabled: z.boolean().default(false),
    attempt_count: z.number().default(0),
    max_attempts: z.number().default(0),
    latest_evaluation_status: z.string().default(""),
    latest_agent_summary: z.string().default(""),
    updated_at: z.string().nullable().default(null),
  }).loose().nullable().optional(),
  started_at: z.string().nullable(),
  completed_at: z.string().nullable(),
  created_at: z.string(),
  updated_at: z.string(),
}).loose();

const OrchestrationEventSchema = z.object({
  id: z.string(),
  plan_id: z.string(),
  node_id: z.string().nullable(),
  task_id: z.string().nullable(),
  event_type: z.string(),
  actor_type: z.string(),
  actor_id: z.string().nullable(),
  payload: z.record(z.string(), z.unknown()).default({}),
  created_at: z.string(),
}).loose();

const OrchestrationArtifactSchema = z.object({
  id: z.string(),
  plan_id: z.string(),
  node_id: z.string().nullable(),
  task_id: z.string().nullable(),
  type: z.string(),
  uri: z.string().nullable(),
  content: z.record(z.string(), z.unknown()).default({}),
  metadata: z.record(z.string(), z.unknown()).default({}),
  content_hash: z.string().nullable(),
  created_at: z.string(),
}).loose();

export const IssueOrchestrationSchema = z.object({
  plans: z.array(OrchestrationPlanSchema).default([]),
  nodes: z.array(OrchestrationNodeSchema).default([]),
  events: z.array(OrchestrationEventSchema).default([]),
  artifacts: z.array(OrchestrationArtifactSchema).default([]),
}).loose();

export const EMPTY_ISSUE_ORCHESTRATION: IssueOrchestration = {
  plans: [],
  nodes: [],
  events: [],
  artifacts: [],
};
