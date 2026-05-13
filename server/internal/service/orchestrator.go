package service

import (
	"log/slog"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type Orchestrator struct {
	Queries   *db.Queries
	TxStarter TxStarter
	TaskSvc   *TaskService
	Logger    *slog.Logger
}

func NewOrchestrator(q *db.Queries, tx TxStarter, taskSvc *TaskService) *Orchestrator {
	return &Orchestrator{Queries: q, TxStarter: tx, TaskSvc: taskSvc, Logger: slog.Default()}
}
