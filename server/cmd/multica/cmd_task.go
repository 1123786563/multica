package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var taskCmd = &cobra.Command{
	Use:   "task",
	Short: "Work with agent tasks",
}

var taskCompleteCmd = &cobra.Command{
	Use:   "complete --task-id <id> --result <file>",
	Short: "Complete a task with a structured result JSON file",
	RunE:  runTaskComplete,
}

func init() {
	taskCmd.AddCommand(taskCompleteCmd)
	taskCompleteCmd.Flags().String("task-id", "", "Task ID to complete")
	taskCompleteCmd.Flags().String("result", "", "Path to structured result JSON")
}

func runTaskComplete(cmd *cobra.Command, args []string) error {
	taskID, _ := cmd.Flags().GetString("task-id")
	if taskID == "" {
		taskID = os.Getenv("MULTICA_TASK_ID")
	}
	if taskID == "" {
		return fmt.Errorf("--task-id is required")
	}
	resultPath, _ := cmd.Flags().GetString("result")
	if resultPath == "" {
		return fmt.Errorf("--result is required")
	}
	data, err := os.ReadFile(resultPath)
	if err != nil {
		return fmt.Errorf("read result: %w", err)
	}
	var payload any
	if err := json.Unmarshal(data, &payload); err != nil {
		return fmt.Errorf("result must be valid JSON: %w", err)
	}
	if _, ok := payload.(map[string]any); !ok {
		return fmt.Errorf("result must be a JSON object")
	}
	raw := json.RawMessage(data)
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	var resp any
	if err := client.PostJSON(cmd.Context(), fmt.Sprintf("/api/daemon/tasks/%s/complete", taskID), map[string]any{
		"result": raw,
	}, &resp); err != nil {
		return err
	}
	out, _ := json.MarshalIndent(resp, "", "  ")
	fmt.Fprintln(cmd.OutOrStdout(), string(out))
	return nil
}
