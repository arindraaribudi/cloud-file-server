package db

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed seeds/*.sql
var seedsFS embed.FS

// Seed runs every *.sql file under seeds/ against the pool in lexical order.
// Files must be self-idempotent (use WHERE NOT EXISTS / ON CONFLICT) since
// there is no version tracking on seeds. Caller gates with an env flag.
func Seed(ctx context.Context, pool *pgxpool.Pool) error {
	files, err := fs.Glob(seedsFS, "seeds/*.sql")
	if err != nil {
		return fmt.Errorf("seed: glob: %w", err)
	}
	if len(files) == 0 {
		return nil
	}
	sort.Strings(files)

	for _, name := range files {
		body, err := fs.ReadFile(seedsFS, name)
		if err != nil {
			return fmt.Errorf("seed: read %s: %w", name, err)
		}
		if _, err := pool.Exec(ctx, string(body)); err != nil {
			return fmt.Errorf("seed: exec %s: %w", strings.TrimPrefix(name, "seeds/"), err)
		}
	}
	return nil
}
