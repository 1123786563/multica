package orchestration

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
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
	RedisURL  string
	Eino      EinoReasoningConfig
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
	taskSvc := service.NewTaskService(queries, pool, nil, nil)
	if redisURL := strings.TrimSpace(cfg.RedisURL); redisURL != "" {
		opts, err := redis.ParseURL(redisURL)
		if err != nil {
			return fmt.Errorf("parse redis url: %w", err)
		}
		rdb := redis.NewClient(opts)
		defer rdb.Close()
		taskSvc.EmptyClaim = service.NewEmptyClaimCache(rdb)
	}
	orchestrationSvc.TaskNotifier = taskSvc
	activities, err := NewWorkerActivitySet(ctx, pool, queries, orchestrationSvc, cfg.Eino)
	if err != nil {
		return err
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
	w.RegisterActivityWithOptions(activities.ProjectSignalAudit, activity.RegisterOptions{Name: ProjectSignalAuditActivityName})
	w.RegisterActivityWithOptions(activities.ProjectEinoFailure, activity.RegisterOptions{Name: ProjectEinoFailureActivityName})

	slog.Info("temporal orchestration worker starting",
		"task_queue", taskQueue,
		"namespace", namespace,
		"host_port", hostPort,
	)
	return w.Run(worker.InterruptCh())
}

func NewWorkerActivitySet(ctx context.Context, pool service.OrchestrationDB, queries *db.Queries, orchestrationSvc *service.OrchestrationService, cfg EinoReasoningConfig) (ActivitySet, error) {
	reasoner, err := newWorkerEinoReasoner(ctx, cfg)
	if err != nil {
		return ActivitySet{}, err
	}
	return ActivitySet{
		DB:            pool,
		Queries:       queries,
		Orchestration: orchestrationSvc,
		Reasoner:      reasoner,
	}, nil
}

func newWorkerEinoReasoner(ctx context.Context, cfg EinoReasoningConfig) (EinoReasoner, error) {
	cfg.ReasoningProfileRef = normalizeReasoningProfileRef(cfg.ReasoningProfileRef)
	if strings.EqualFold(strings.TrimSpace(cfg.Provider), EinoProviderStatic) {
		if !cfg.AllowStatic {
			return nil, fmt.Errorf("static Eino reasoning provider requires ORCHESTRATION_EINO_ALLOW_STATIC=1")
		}
		return StaticEinoReasoner{}, nil
	}
	reasoner, err := NewEinoIssueAnalyzer(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("configure Eino reasoning provider: %w", err)
	}
	return reasoner, nil
}
