package audit

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// DropOldPartitionsSQL returns a statement that drops every monthly partition
// whose end-exclusive boundary is older than cutoff.
func DropOldPartitionsSQL(table string, cutoff time.Time) string {
	ym := cutoff.UTC().Format("2006_01")
	return fmt.Sprintf(`DROP TABLE IF EXISTS %s_%s`, table, ym)
}

func RunRetention(ctx context.Context, pool *pgxpool.Pool, retentionDays int) error {
	cutoff := time.Now().UTC().AddDate(0, 0, -retentionDays)
	stmt := DropOldPartitionsSQL("audit_events", cutoff)
	_, err := pool.Exec(ctx, stmt)
	return err
}