package service

import (
	"context"
	"encoding/json"
	"fmt"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func (o *Orchestrator) runInTx(ctx context.Context, fn func(*db.Queries) error) error {
	if o.TxStarter == nil {
		return fn(o.Queries)
	}
	tx, err := o.TxStarter.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)
	if err := fn(o.Queries.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte(`{}`)
	}
	return b
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
