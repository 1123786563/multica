package orchestration

import (
	"os"
	"strings"
	"testing"
)

func TestTemporalMVPCheckoutDocCoversRepeatableValidation(t *testing.T) {
	raw, err := os.ReadFile("../../../docs/temporal-orchestration-mvp-checkout.md")
	if err != nil {
		t.Fatalf("read checkout doc: %v", err)
	}
	doc := string(raw)
	required := []string{
		"temporal server start-dev",
		"TEMPORAL_HOST_PORT=127.0.0.1:7233",
		"ORCHESTRATION_EINO_PROVIDER=openai-compatible",
		"ORCHESTRATION_EINO_API_KEY=replace-me",
		"ORCHESTRATION_EINO_MODEL=replace-me",
		"ORCHESTRATION_EINO_PROFILE_REF=worker-default",
		"ORCHESTRATION_EINO_ALLOW_STATIC=0",
		"ORCHESTRATION_EINO_LIVE_TEST=1",
		"TestEinoReasonerLiveProviderSmoke",
		"AnalyzeIssue, ReviewOutcome, and SummarizeOutcome",
		"make orchestration-worker",
		"make server",
		"make daemon",
		"go test -count=1 ./internal/orchestration",
		"go test -count=1 ./internal/handler -run",
		"pnpm --filter @multica/core exec vitest run api/schema.test.ts",
		"pnpm --filter @multica/views exec vitest run issues/components/issue-detail.test.tsx",
		"Happy path",
		"Fail-closed path",
		"Failure / retry / approval path",
		"Issue Detail",
		"Expanded events",
		"Evidence",
		"Artifacts",
		"Final evidence",
	}
	for _, want := range required {
		if !strings.Contains(doc, want) {
			t.Fatalf("checkout doc missing %q", want)
		}
	}
}

func TestSelfhostComposePassesEinoProviderConfigToWorker(t *testing.T) {
	raw, err := os.ReadFile("../../../docker-compose.selfhost.yml")
	if err != nil {
		t.Fatalf("read selfhost compose: %v", err)
	}
	compose := string(raw)
	required := []string{
		"ORCHESTRATION_EINO_PROVIDER",
		"ORCHESTRATION_EINO_API_KEY",
		"ORCHESTRATION_EINO_MODEL",
		"ORCHESTRATION_EINO_BASE_URL",
		"ORCHESTRATION_EINO_TIMEOUT",
		"ORCHESTRATION_EINO_PROFILE_REF",
		"ORCHESTRATION_EINO_ALLOW_STATIC",
	}
	for _, want := range required {
		if !strings.Contains(compose, want) {
			t.Fatalf("selfhost orchestration worker must pass %s", want)
		}
	}
}

func TestWorkerRegistersSignalAuditProjectionActivity(t *testing.T) {
	raw, err := os.ReadFile("worker.go")
	if err != nil {
		t.Fatalf("read worker: %v", err)
	}
	if !strings.Contains(string(raw), "ProjectSignalAuditActivityName") {
		t.Fatal("orchestration worker must register the signal audit projection activity used by the workflow")
	}
	if !strings.Contains(string(raw), "ProjectEinoFailureActivityName") {
		t.Fatal("orchestration worker must register the Eino failure projection activity used by the workflow")
	}
}
