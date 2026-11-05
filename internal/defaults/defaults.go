// Package defaults manages settings rows that name another table's row, such as
// the default event type (#0618). A default held in settings has no foreign key:
// settings.value is text. So the store that owns the named table enforces the
// rules, in a transaction, by locking the setting through this package before it
// touches its own row. The order is always setting, then row, so the paths cannot
// deadlock.
//
// A managed row is per-database state, not a seed, and only its owning store's
// admin screen may change it: PATCH /admin/settings refuses these keys.
package defaults

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5"
)

// KeyEventType names the default event type's row in settings.
const KeyEventType = "default_event_type_id"

// ErrUnset means the setting is missing or does not hold a valid id. A store
// cannot tell which row is the default in this state, so it refuses to disable or
// delete anything until a new default is written.
var ErrUnset = errors.New("defaults: setting is missing or is not a valid id")

// Keys returns every key this package manages.
func Keys() []string {
	return []string{KeyEventType}
}

// IsManaged reports whether key is one this package manages.
func IsManaged(key string) bool {
	for _, k := range Keys() {
		if k == key {
			return true
		}
	}
	return false
}

// parseID accepts only the canonical form of an id, so "01" and " 1" are refused.
// That matches the SQL function event_types_default_id, which compares id::text
// to the raw value.
func parseID(raw string) (int64, error) {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || strconv.FormatInt(id, 10) != raw {
		return 0, ErrUnset
	}
	return id, nil
}

// Lock takes a row lock on the setting until tx ends and returns the id it holds.
// It returns ErrUnset for no row or a value that is not a canonical id. No lock is
// held when the row is missing.
func Lock(ctx context.Context, tx pgx.Tx, key string) (int64, error) {
	var raw string
	err := tx.QueryRow(ctx, `SELECT value FROM settings WHERE key = $1 FOR UPDATE`, key).Scan(&raw)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return 0, ErrUnset
	case err != nil:
		return 0, fmt.Errorf("defaults: locking %s: %w", key, err)
	}
	return parseID(raw)
}

// Write stores id in the setting, creating the row if it is missing, which is what
// makes choosing a default the repair path for a broken setting.
func Write(ctx context.Context, tx pgx.Tx, key string, id int64) error {
	_, err := tx.Exec(ctx,
		`INSERT INTO settings (key, value, updated_at) VALUES ($1, $2, now())
		 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`,
		key, strconv.FormatInt(id, 10))
	if err != nil {
		return fmt.Errorf("defaults: writing %s: %w", key, err)
	}
	return nil
}
