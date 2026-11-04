// Package eventtypes is the data-access layer for the admin-managed list of
// listing types (#0613; PRD §6.2, migrations/000045). Types are rows so that an
// admin can add, rename, reorder and disable one without a deploy.
//
// The slug is immutable after creation, for the same reason as
// interests.Store.Update, and because #0614 puts it in public URLs. "Rename"
// means the display name.
//
// Disabling means "no new listings of this type"; it never hides a listing that
// already has it. internal/workshops enforces that on assignment.
package eventtypes

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrNotFound is returned when no event_types row matches a lookup.
	ErrNotFound = errors.New("eventtypes: not found")
	// ErrInvalidSlug is returned when a slug fails the format check before a
	// query is issued.
	ErrInvalidSlug = errors.New("eventtypes: slug must be lowercase and hyphenated")
	// ErrDuplicateSlug is returned when Create would collide with an existing slug.
	ErrDuplicateSlug = errors.New("eventtypes: slug already exists")
	// ErrInUse is returned by Delete when a listing still has this type.
	ErrInUse = errors.New("eventtypes: in use by listings; disable instead of deleting")
	// ErrIsDefault is returned when disabling or deleting the default type.
	ErrIsDefault = errors.New("eventtypes: is the default type")
	// ErrDisabled is returned by SetDefault for a disabled type.
	ErrDisabled = errors.New("eventtypes: type is disabled")
)

// slugPattern matches the event_types_slug_format CHECK.
var slugPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// ValidSlug reports whether slug is lowercase and hyphenated.
func ValidSlug(slug string) bool {
	return slugPattern.MatchString(slug)
}

// EventType is a single row of the event_types table.
type EventType struct {
	ID        int64
	Slug      string
	Name      string
	Enabled   bool
	IsDefault bool
	SortOrder int
	CreatedAt time.Time
}

// Store is the data-access layer over the event_types table.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore constructs a Store over the shared connection pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

const eventTypeColumns = `id, slug, name, enabled, is_default, sort_order, created_at`

func scanEventType(row pgx.Row) (EventType, error) {
	var et EventType
	if err := row.Scan(&et.ID, &et.Slug, &et.Name, &et.Enabled, &et.IsDefault, &et.SortOrder, &et.CreatedAt); err != nil {
		return EventType{}, err
	}
	return et, nil
}

// ListAll returns every type, enabled or not, ordered by sort_order then name.
func (s *Store) ListAll(ctx context.Context) ([]EventType, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+eventTypeColumns+` FROM event_types ORDER BY sort_order, name`)
	if err != nil {
		return nil, fmt.Errorf("eventtypes: listing: %w", err)
	}
	defer rows.Close()
	var out []EventType
	for rows.Next() {
		et, err := scanEventType(rows)
		if err != nil {
			return nil, fmt.Errorf("eventtypes: scanning row: %w", err)
		}
		out = append(out, et)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("eventtypes: iterating rows: %w", err)
	}
	return out, nil
}

// GetByID looks up one type by primary key, enabled or not.
func (s *Store) GetByID(ctx context.Context, id int64) (EventType, error) {
	et, err := scanEventType(s.pool.QueryRow(ctx,
		`SELECT `+eventTypeColumns+` FROM event_types WHERE id = $1`, id))
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return EventType{}, ErrNotFound
	case err != nil:
		return EventType{}, fmt.Errorf("eventtypes: getting id %d: %w", id, err)
	}
	return et, nil
}

// Create inserts a new enabled, non-default type.
func (s *Store) Create(ctx context.Context, slug, name string, sortOrder int) (EventType, error) {
	if !ValidSlug(slug) {
		return EventType{}, ErrInvalidSlug
	}
	et, err := scanEventType(s.pool.QueryRow(ctx,
		`INSERT INTO event_types (slug, name, sort_order) VALUES ($1, $2, $3)
		 RETURNING `+eventTypeColumns,
		slug, name, sortOrder))
	if err != nil {
		if pgCode(err) == "23505" {
			return EventType{}, ErrDuplicateSlug
		}
		return EventType{}, fmt.Errorf("eventtypes: creating %q: %w", slug, err)
	}
	return et, nil
}

// Update changes a type's name, sort order and enabled flag. It does no
// pre-read: disabling the default is refused by the event_types_default_enabled
// CHECK and mapped to ErrIsDefault, so the check cannot race SetDefault.
func (s *Store) Update(ctx context.Context, id int64, name string, sortOrder int, enabled bool) (EventType, error) {
	et, err := scanEventType(s.pool.QueryRow(ctx,
		`UPDATE event_types SET name = $2, sort_order = $3, enabled = $4
		  WHERE id = $1
		 RETURNING `+eventTypeColumns,
		id, name, sortOrder, enabled))
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return EventType{}, ErrNotFound
	case pgCode(err) == "23514" && pgConstraint(err) == "event_types_default_enabled":
		return EventType{}, ErrIsDefault
	case err != nil:
		return EventType{}, fmt.Errorf("eventtypes: updating id %d: %w", id, err)
	}
	return et, nil
}

// SetDefault moves the default flag to id. These must stay separate statements:
// Postgres checks event_types_one_default row by row, so a single-statement swap
// raises a duplicate key. Locking every row first is what stops two concurrent
// callers colliding, since each later statement gets a fresh snapshot.
func (s *Store) SetDefault(ctx context.Context, id int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("eventtypes: beginning set-default of id %d: %w", id, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SELECT id FROM event_types ORDER BY id FOR UPDATE`); err != nil {
		return fmt.Errorf("eventtypes: locking for set-default of id %d: %w", id, err)
	}
	var enabled bool
	err = tx.QueryRow(ctx, `SELECT enabled FROM event_types WHERE id = $1`, id).Scan(&enabled)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return ErrNotFound
	case err != nil:
		return fmt.Errorf("eventtypes: reading id %d for set-default: %w", id, err)
	}
	if !enabled {
		return ErrDisabled
	}
	if _, err := tx.Exec(ctx, `UPDATE event_types SET is_default = FALSE WHERE is_default`); err != nil {
		return fmt.Errorf("eventtypes: clearing default: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE event_types SET is_default = TRUE WHERE id = $1`, id); err != nil {
		return fmt.Errorf("eventtypes: setting default to id %d: %w", id, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("eventtypes: committing set-default of id %d: %w", id, err)
	}
	return nil
}

// UsageCounts returns the number of listings per type. A type with none is
// absent from the map.
func (s *Store) UsageCounts(ctx context.Context) (map[int64]int64, error) {
	rows, err := s.pool.Query(ctx, `SELECT event_type_id, count(*) FROM workshops GROUP BY 1`)
	if err != nil {
		return nil, fmt.Errorf("eventtypes: counting usage: %w", err)
	}
	defer rows.Close()
	out := make(map[int64]int64)
	for rows.Next() {
		var id, count int64
		if err := rows.Scan(&id, &count); err != nil {
			return nil, fmt.Errorf("eventtypes: scanning usage count: %w", err)
		}
		out[id] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("eventtypes: iterating usage counts: %w", err)
	}
	return out, nil
}

// Delete removes a type that no listing uses. The foreign key's ON DELETE
// RESTRICT is the in-use guard, mapped to ErrInUse. The row lock keeps a
// concurrent SetDefault from making this row the default mid-delete.
func (s *Store) Delete(ctx context.Context, id int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("eventtypes: beginning delete of id %d: %w", id, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var isDefault bool
	err = tx.QueryRow(ctx, `SELECT is_default FROM event_types WHERE id = $1 FOR UPDATE`, id).Scan(&isDefault)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return ErrNotFound
	case err != nil:
		return fmt.Errorf("eventtypes: locking id %d for delete: %w", id, err)
	}
	if isDefault {
		return ErrIsDefault
	}
	if _, err := tx.Exec(ctx, `DELETE FROM event_types WHERE id = $1`, id); err != nil {
		if pgCode(err) == "23503" && pgConstraint(err) == "workshops_event_type_id_fkey" {
			return ErrInUse
		}
		return fmt.Errorf("eventtypes: deleting id %d: %w", id, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("eventtypes: committing delete of id %d: %w", id, err)
	}
	return nil
}

func pgCode(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

func pgConstraint(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.ConstraintName
	}
	return ""
}
