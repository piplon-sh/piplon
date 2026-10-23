package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"path"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// migrationNames returns the migration files in apply order. Names start with
// a zero-padded number, so lexical order is numeric order.
func migrationNames() ([]string, error) {
	paths, err := fs.Glob(migrationsFS, "migrations/*.sql")
	if err != nil {
		return nil, err
	}
	names := make([]string, len(paths))
	for i, p := range paths {
		names[i] = path.Base(p)
	}
	return names, nil
}

// migrate applies every migration above PRAGMA user_version, each in its own
// transaction together with the version bump.
func migrate(ctx context.Context, db *sql.DB) error {
	names, err := migrationNames()
	if err != nil {
		return err
	}
	var current int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&current); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if current > len(names) {
		return fmt.Errorf("version %d, this binary knows %d: %w", current, len(names), ErrSchemaTooNew)
	}
	for v := current + 1; v <= len(names); v++ {
		if err := apply(ctx, db, v, names[v-1]); err != nil {
			return fmt.Errorf("migration %s: %w", names[v-1], err)
		}
	}
	return nil
}

func apply(ctx context.Context, db *sql.DB, version int, name string) error {
	body, err := migrationsFS.ReadFile("migrations/" + name)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() // no-op once committed
	if _, err := tx.ExecContext(ctx, string(body)); err != nil {
		return err
	}
	// PRAGMA takes no bind parameters; version is an int we computed.
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", version)); err != nil {
		return err
	}
	return tx.Commit()
}
