package service

import (
	"context"
	"errors"
	"strings"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
)

const (
	defaultTemporalNamespace      = "default"
	defaultTemporalTaskQueue      = "multica-orchestration"
	defaultIssueWorkflowType      = "IssueWorkflow"
	defaultAgentTaskOutcomeSignal = "agent_task_outcome"
	defaultApprovalActionSignal   = "approval_action"
)

type TemporalStarterConfig struct {
	HostPort  string
	Namespace string
	TaskQueue string
	Workflow  string
}

func NewTemporalWorkflowStarter(cfg TemporalStarterConfig) TemporalWorkflowStarter {
	c := newTemporalWorkflowClient(cfg)
	if c == nil {
		return nil
	}
	return c
}

func NewTemporalAgentTaskOutcomeSignaler(cfg TemporalStarterConfig) AgentTaskOutcomeSignaler {
	c := newTemporalWorkflowClient(cfg)
	if c == nil {
		return nil
	}
	return c
}

func NewTemporalApprovalActionSignaler(cfg TemporalStarterConfig) ApprovalActionSignaler {
	c := newTemporalWorkflowClient(cfg)
	if c == nil {
		return nil
	}
	return c
}

func newTemporalWorkflowClient(cfg TemporalStarterConfig) *TemporalWorkflowStarterClient {
	hostPort := strings.TrimSpace(cfg.HostPort)
	if hostPort == "" {
		return nil
	}
	namespace := strings.TrimSpace(cfg.Namespace)
	if namespace == "" {
		namespace = defaultTemporalNamespace
	}
	taskQueue := strings.TrimSpace(cfg.TaskQueue)
	if taskQueue == "" {
		taskQueue = defaultTemporalTaskQueue
	}
	workflow := strings.TrimSpace(cfg.Workflow)
	if workflow == "" {
		workflow = defaultIssueWorkflowType
	}
	return &TemporalWorkflowStarterClient{
		HostPort:  hostPort,
		Namespace: namespace,
		TaskQueue: taskQueue,
		Workflow:  workflow,
	}
}

type TemporalWorkflowStarterClient struct {
	HostPort  string
	Namespace string
	TaskQueue string
	Workflow  string
}

func (s *TemporalWorkflowStarterClient) StartIssueWorkflow(ctx context.Context, input IssueWorkflowStartInput) (TemporalWorkflowStart, error) {
	dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	c, err := client.DialContext(dialCtx, client.Options{
		HostPort:  s.HostPort,
		Namespace: s.Namespace,
	})
	if err != nil {
		return TemporalWorkflowStart{}, err
	}
	defer c.Close()

	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:                    input.WorkflowID,
		TaskQueue:             s.TaskQueue,
		WorkflowIDReusePolicy: enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
	}, s.Workflow, input)
	if err != nil {
		var alreadyStarted *serviceerror.WorkflowExecutionAlreadyStarted
		if errors.As(err, &alreadyStarted) {
			return TemporalWorkflowStart{}, WorkflowAlreadyStartedError{WorkflowID: input.WorkflowID}
		}
		return TemporalWorkflowStart{}, err
	}
	return TemporalWorkflowStart{
		WorkflowID: run.GetID(),
		RunID:      run.GetRunID(),
	}, nil
}

func (s *TemporalWorkflowStarterClient) SignalAgentTaskOutcome(ctx context.Context, input AgentTaskOutcomeSignalInput) error {
	dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	c, err := client.DialContext(dialCtx, client.Options{
		HostPort:  s.HostPort,
		Namespace: s.Namespace,
	})
	if err != nil {
		return err
	}
	defer c.Close()

	return c.SignalWorkflow(ctx, input.WorkflowID, "", defaultAgentTaskOutcomeSignal, input)
}

func (s *TemporalWorkflowStarterClient) SignalApprovalAction(ctx context.Context, input ApprovalActionSignalInput) error {
	dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	c, err := client.DialContext(dialCtx, client.Options{
		HostPort:  s.HostPort,
		Namespace: s.Namespace,
	})
	if err != nil {
		return err
	}
	defer c.Close()

	return c.SignalWorkflow(ctx, input.WorkflowID, "", defaultApprovalActionSignal, input)
}
