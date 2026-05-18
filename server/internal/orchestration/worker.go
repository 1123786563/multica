package orchestration

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"

	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type WorkerConfig struct {
	HostPort  string
	Namespace string
	TaskQueue string
}

func Run(ctx context.Context, pool *pgxpool.Pool, cfg WorkerConfig) error {
	hostPort := strings.TrimSpace(cfg.HostPort)
	if hostPort == "" {
		return fmt.Errorf("temporal host port is required")
	}
	namespace := strings.TrimSpace(cfg.Namespace)
	if namespace == "" {
		namespace = "default"
	}
	taskQueue := strings.TrimSpace(cfg.TaskQueue)
	if taskQueue == "" {
		taskQueue = "multica-orchestration"
	}

	c, err := client.DialContext(ctx, client.Options{
		HostPort:  hostPort,
		Namespace: namespace,
	})
	if err != nil {
		return fmt.Errorf("dial temporal: %w", err)
	}
	defer c.Close()

	queries := db.New(pool)
	orchestrationSvc := service.NewOrchestrationService(pool, pool, nil)
	activities := ActivitySet{
		DB:            pool,
		Queries:       queries,
		Orchestration: orchestrationSvc,
	}

	w := worker.New(c, taskQueue, worker.Options{})
	w.RegisterWorkflowWithOptions(IssueWorkflow, workflow.RegisterOptions{Name: IssueWorkflowName})
	w.RegisterActivityWithOptions(activities.LoadIssue, activity.RegisterOptions{Name: LoadIssueActivityName})
	w.RegisterActivityWithOptions(activities.AnalyzeIssue, activity.RegisterOptions{Name: AnalyzeIssueActivityName})
	w.RegisterActivityWithOptions(activities.DispatchDaemonTask, activity.RegisterOptions{Name: DispatchTaskActivityName})
	w.RegisterActivityWithOptions(activities.ValidateOutcome, activity.RegisterOptions{Name: ValidateOutcomeActivityName})
	w.RegisterActivityWithOptions(activities.ReviewOutcome, activity.RegisterOptions{Name: ReviewOutcomeActivityName})
	w.RegisterActivityWithOptions(activities.SummarizeOutcome, activity.RegisterOptions{Name: SummarizeOutcomeActivityName})
	w.RegisterActivityWithOptions(activities.FinalizeWorkflow, activity.RegisterOptions{Name: FinalizeWorkflowActivityName})
	w.RegisterActivityWithOptions(activities.ProjectAnalysis, activity.RegisterOptions{Name: ProjectAnalysisActivityName})

	slog.Info("temporal orchestration worker starting",
		"task_queue", taskQueue,
		"namespace", namespace,
		"host_port", hostPort,
	)
	return w.Run(worker.InterruptCh())
}
