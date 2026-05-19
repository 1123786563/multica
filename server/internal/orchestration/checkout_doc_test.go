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
