package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func newTestTaskCompleteCmd() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Flags().String("task-id", "", "Task ID to complete")
	cmd.Flags().String("result", "", "Path to structured result JSON")
	return cmd
}

func TestTaskCompleteRequiresResultFile(t *testing.T) {
	t.Setenv("MULTICA_TASK_ID", "")

	err := runTaskComplete(newTestTaskCompleteCmd(), nil)
	if err == nil || !strings.Contains(err.Error(), "--task-id is required") {
		t.Fatalf("expected --task-id error, got %v", err)
	}

	cmd := newTestTaskCompleteCmd()
	if err := cmd.Flags().Set("task-id", "task-1"); err != nil {
		t.Fatalf("set task-id: %v", err)
	}
	err = runTaskComplete(cmd, nil)
	if err == nil || !strings.Contains(err.Error(), "--result is required") {
		t.Fatalf("expected --result error, got %v", err)
	}
}

func TestTaskCompleteRejectsInvalidJSONFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "result.json")
	if err := os.WriteFile(path, []byte(`{bad`), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := newTestTaskCompleteCmd()
	if err := cmd.Flags().Set("task-id", "task-1"); err != nil {
		t.Fatalf("set task-id: %v", err)
	}
	if err := cmd.Flags().Set("result", path); err != nil {
		t.Fatalf("set result: %v", err)
	}
	err := runTaskComplete(cmd, nil)
	if err == nil || !strings.Contains(err.Error(), "result must be valid JSON") {
		t.Fatalf("expected invalid JSON error, got %v", err)
	}
}

func TestTaskCompleteRejectsNonObjectJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "result.json")
	if err := os.WriteFile(path, []byte(`["not","object"]`), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := newTestTaskCompleteCmd()
	if err := cmd.Flags().Set("task-id", "task-1"); err != nil {
		t.Fatalf("set task-id: %v", err)
	}
	if err := cmd.Flags().Set("result", path); err != nil {
		t.Fatalf("set result: %v", err)
	}
	err := runTaskComplete(cmd, nil)
	if err == nil || !strings.Contains(err.Error(), "result must be a JSON object") {
		t.Fatalf("expected JSON object error, got %v", err)
	}
}
