// Schema version reader (#0516): reads golang-migrate's schema_migrations
// stamp so the admin console's build-info line can show "which schema" next
// to "which code" — the two halves of the same "what is production running?"
// question, and a mismatch between them is exactly what a bad deploy looks
// like (#0516 criterion 6; the decision to show it is recorded there and in
// the handler).
//
// Why this lives in internal/db and not a domain store: schema_migrations is
// migration-tooling metadata, owned by golang-migrate rather than by any
// domain table, so no domain store is its natural home. internal/db owns the
// pool and the database-level concerns (see pool.go), so the one SELECT that
// reads the stamp goes here. The handler depends on the narrow method
// signature, not this type — internal/handlers/admin_version.go declares the
// interface.
package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// SchemaReader reads database-level metadata over the shared pool. It is a
// stateless wrapper, like every other store in this project — constructing it
// a second time alongside an existing pool costs nothing.
type SchemaReader struct {
	Pool *pgxpool.Pool
}

// SchemaVersion returns the migration version schema_migrations records and
// whether that migration is marked dirty (mid-failure). The zero-value pair
// (0, false) is never returned with a nil error: a database that has never
// been migrated has no row, and reporting "version 0, clean" would read as a
// real — if odd — schema rather than an absent one, so that case is an error.
func (r *SchemaReader) SchemaVersion(ctx context.Context) (version int64, dirty bool, err error) {
	err = r.Pool.QueryRow(ctx, "SELECT version, dirty FROM schema_migrations").
		Scan(&version, &dirty)
	if err != nil {
		return 0, false, fmt.Errorf("db: reading schema_migrations: %w", err)
	}
	return version, dirty, nil
}
