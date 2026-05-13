package service

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func (o *Orchestrator) notifyTaskQueued(ctx context.Context, task db.AgentTaskQueue) {
	if o.TaskSvc == nil {
		return
	}
	o.TaskSvc.broadcastTaskEvent(ctx, "task:queued", task)
	o.TaskSvc.NotifyTaskEnqueued(ctx, task)
}

func (o *Orchestrator) createAttentionCommentIfNeeded(ctx context.Context, task db.AgentTaskQueue) {
	if o == nil || o.TaskSvc == nil || !task.IssueID.Valid {
		return
	}
	issue, err := o.Queries.GetIssue(ctx, task.IssueID)
	if err != nil {
		return
	}
	taskCtx, ok := ParseOrchestrationTaskContext(task.Context)
	if !ok {
		return
	}
	nodeID, err := util.ParseUUID(taskCtx.OrchestrationNodeID)
	if err != nil {
		return
	}
	node, err := o.Queries.GetOrchestrationNode(ctx, nodeID)
	if err != nil {
		return
	}
	events, err := o.Queries.ListOrchestrationEventsByPlan(ctx, node.PlanID)
	if err != nil {
		return
	}
	summary := BuildNodeSummaryFromRecords(node, events)
	switch summary.ReasonCode {
	case "waiting_for_approval":
		var b strings.Builder
		b.WriteString("Approval required\n\n")
		b.WriteString("Recommended action: Approve\n\n")
		if msg := strings.TrimSpace(summary.LatestAgentSummary); msg != "" {
			b.WriteString(msg)
			b.WriteString("\n\n")
		}
		if detail := strings.TrimSpace(summary.ReasonDetail); detail != "" {
			b.WriteString(detail)
		}
		o.TaskSvc.createAgentComment(ctx, task.IssueID, task.AgentID, b.String(), "system", pgtype.UUID{})
		o.ensureAttentionAudience(ctx, issue)
	case "runtime_failed":
		var b strings.Builder
		b.WriteString("Runtime failed\n\n")
		b.WriteString("Recommended action: Retry\n\n")
		if msg := strings.TrimSpace(summary.LatestAgentSummary); msg != "" {
			b.WriteString(msg)
			b.WriteString("\n\n")
		}
		if detail := strings.TrimSpace(summary.ReasonDetail); detail != "" {
			b.WriteString(detail)
		}
		o.TaskSvc.createAgentComment(ctx, task.IssueID, task.AgentID, b.String(), "system", pgtype.UUID{})
		o.ensureAttentionAudience(ctx, issue)
	case "retry_exhausted":
		var b strings.Builder
		b.WriteString("Retries exhausted\n\n")
		b.WriteString("Recommended action: Retry\n\n")
		if msg := strings.TrimSpace(summary.LatestAgentSummary); msg != "" {
			b.WriteString(msg)
			b.WriteString("\n\n")
		}
		if detail := strings.TrimSpace(summary.ReasonDetail); detail != "" {
			b.WriteString(detail)
		}
		o.TaskSvc.createAgentComment(ctx, task.IssueID, task.AgentID, b.String(), "system", pgtype.UUID{})
		o.ensureAttentionAudience(ctx, issue)
	default:
		return
	}
}

func (o *Orchestrator) ensureAttentionAudience(ctx context.Context, issue db.Issue) {
	if o == nil {
		return
	}
	addMember := func(userID pgtype.UUID, reason string) {
		if !userID.Valid {
			return
		}
		_ = o.Queries.AddIssueSubscriber(ctx, db.AddIssueSubscriberParams{
			IssueID:  issue.ID,
			UserType: "member",
			UserID:   userID,
			Reason:   reason,
		})
	}

	if issue.CreatorType == "member" {
		addMember(issue.CreatorID, "creator")
	}
	if issue.AssigneeType.Valid && issue.AssigneeType.String == "member" {
		addMember(issue.AssigneeID, "assignee")
	}

	subscribers, err := o.Queries.ListIssueSubscribers(ctx, issue.ID)
	if err != nil {
		return
	}
	for _, subscriber := range subscribers {
		if subscriber.UserType != "member" {
			continue
		}
		addMember(subscriber.UserID, subscriber.Reason)
	}
}

func (o *Orchestrator) publishOrchestrationUpdated(ctx context.Context, plan db.OrchestrationPlan) {
	if o == nil || o.TaskSvc == nil || o.TaskSvc.Bus == nil {
		return
	}
	if !plan.SourceID.Valid {
		return
	}
	o.TaskSvc.Bus.Publish(events.Event{
		Type:        "orchestration:updated",
		WorkspaceID: util.UUIDToString(plan.WorkspaceID),
		ActorType:   "system",
		ActorID:     "",
		Payload: map[string]any{
			"issue_id":   util.UUIDToString(plan.SourceID),
			"run_id":     util.UUIDToString(plan.ID),
			"changed_at": time.Now().UTC().Format(time.RFC3339),
		},
	})
}

func (o *Orchestrator) publishOrchestrationUpdatedFromIssue(ctx context.Context, issueID pgtype.UUID) {
	if !issueID.Valid {
		return
	}
	plan, err := o.Queries.GetActiveOrchestrationPlanBySource(ctx, db.GetActiveOrchestrationPlanBySourceParams{
		SourceType: "issue",
		SourceID:   issueID,
	})
	if err != nil {
		return
	}
	o.publishOrchestrationUpdated(ctx, plan)
}
