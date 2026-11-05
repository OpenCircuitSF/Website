// Package venues is the data-access layer for the admin-managed list of places a
// listing can happen (#0617; PRD §6.2, migrations/000048). A venue is one row, so
// correcting an address reaches every listing that uses it, past ones included.
// outreach_locations is a different table, for flyer posting places.
//
// Disabling means "no new listings here"; it never hides a listing that already
// has the venue. internal/workshops enforces that on assignment.
//
// The default venue is held in settings, not on the row. Every path that could
// leave that setting naming a disabled or deleted venue locks it first through
// internal/defaults, then touches the venues row.
package venues

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/defaults"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrNotFound is returned when no venues row matches a lookup.
	ErrNotFound = errors.New("venues: not found")
	// ErrInUse is returned by Delete when a listing still has this venue.
	ErrInUse = errors.New("venues: in use by listings; disable instead of deleting")
	// ErrIsDefault is returned when disabling or deleting the default venue.
	ErrIsDefault = errors.New("venues: is the default venue")
	// ErrDisabled is returned by SetDefault for a disabled venue.
	ErrDisabled = errors.New("venues: venue is disabled")
)

// Venue is a single row of the venues table.
type Venue struct {
	ID        int64
	Name      string
	Address   *string
	Note      *string
	Enabled   bool
	IsDefault bool
	SortOrder int
	CreatedAt time.Time
	UpdatedAt time.Time
}

// CreateInput is the payload for Create.
type CreateInput struct {
	Name      string
	Address   *string
	Note      *string
	SortOrder int
}

// UpdateInput carries every field's fully resolved target value. A nil Address or
// Note clears that field.
type UpdateInput struct {
	Name      string
	Address   *string
	Note      *string
	SortOrder int
	Enabled   bool
}

// Store is the data-access layer over the venues table.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore constructs a Store over the shared connection pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// venueColumns derives is_default from the setting. A broken setting flags no row
// and does not fail the read, so the admin can still reach "Make default".
const venueColumns = `id, name, address, note, enabled,
	(enabled AND COALESCE(id::text = (SELECT value FROM settings WHERE key = '` + defaults.KeyVenue + `'), FALSE)) AS is_default,
	sort_order, created_at, updated_at`

func scanVenue(row pgx.Row) (Venue, error) {
	var v Venue
	if err := row.Scan(&v.ID, &v.Name, &v.Address, &v.Note, &v.Enabled, &v.IsDefault, &v.SortOrder, &v.CreatedAt, &v.UpdatedAt); err != nil {
		return Venue{}, err
	}
	return v, nil
}

// ListAll returns every venue, enabled or not, ordered by sort_order then name.
func (s *Store) ListAll(ctx context.Context) ([]Venue, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+venueColumns+` FROM venues ORDER BY sort_order, name, id`)
	if err != nil {
		return nil, fmt.Errorf("venues: listing: %w", err)
	}
	defer rows.Close()
	var out []Venue
	for rows.Next() {
		v, err := scanVenue(rows)
		if err != nil {
			return nil, fmt.Errorf("venues: scanning row: %w", err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("venues: iterating rows: %w", err)
	}
	return out, nil
}

// GetByID looks up one venue by primary key, enabled or not.
func (s *Store) GetByID(ctx context.Context, id int64) (Venue, error) {
	v, err := scanVenue(s.pool.QueryRow(ctx,
		`SELECT `+venueColumns+` FROM venues WHERE id = $1`, id))
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Venue{}, ErrNotFound
	case err != nil:
		return Venue{}, fmt.Errorf("venues: getting id %d: %w", id, err)
	}
	return v, nil
}

// Create inserts a new enabled, non-default venue. Names need not be unique: a
// venue that moves keeps its name, by disabling the old row and adding a new one.
func (s *Store) Create(ctx context.Context, in CreateInput) (Venue, error) {
	v, err := scanVenue(s.pool.QueryRow(ctx,
		`INSERT INTO venues (name, address, note, sort_order) VALUES ($1, $2, $3, $4)
		 RETURNING `+venueColumns,
		in.Name, in.Address, in.Note, in.SortOrder))
	if err != nil {
		return Venue{}, fmt.Errorf("venues: creating %q: %w", in.Name, err)
	}
	return v, nil
}

// Update replaces a venue's fields. Disabling takes the setting's lock first, so
// it cannot race SetDefault, and is refused for the default, or with
// defaults.ErrUnset when the default cannot be determined. Any other update stays
// a single statement, so a rename works with a broken setting.
func (s *Store) Update(ctx context.Context, id int64, in UpdateInput) (Venue, error) {
	const update = `UPDATE venues
		    SET name = $2, address = $3, note = $4, sort_order = $5, enabled = $6, updated_at = now()
		  WHERE id = $1
		 RETURNING ` + venueColumns
	if in.Enabled {
		v, err := scanVenue(s.pool.QueryRow(ctx, update, id, in.Name, in.Address, in.Note, in.SortOrder, in.Enabled))
		return updateResult(v, err, id)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Venue{}, fmt.Errorf("venues: beginning update of id %d: %w", id, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	defaultID, err := defaults.Lock(ctx, tx, defaults.KeyVenue)
	if err != nil {
		return Venue{}, fmt.Errorf("venues: disabling id %d: %w", id, err)
	}
	if defaultID == id {
		return Venue{}, ErrIsDefault
	}
	v, err := scanVenue(tx.QueryRow(ctx, update, id, in.Name, in.Address, in.Note, in.SortOrder, in.Enabled))
	if v, err = updateResult(v, err, id); err != nil {
		return Venue{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Venue{}, fmt.Errorf("venues: committing update of id %d: %w", id, err)
	}
	return v, nil
}

func updateResult(v Venue, err error, id int64) (Venue, error) {
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Venue{}, ErrNotFound
	case err != nil:
		return Venue{}, fmt.Errorf("venues: updating id %d: %w", id, err)
	}
	return v, nil
}

// SetDefault points the setting at id. It locks the setting, then the row, and
// ignores defaults.ErrUnset, because this is how a broken setting gets repaired.
func (s *Store) SetDefault(ctx context.Context, id int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("venues: beginning set-default of id %d: %w", id, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := defaults.Lock(ctx, tx, defaults.KeyVenue); err != nil && !errors.Is(err, defaults.ErrUnset) {
		return fmt.Errorf("venues: locking for set-default of id %d: %w", id, err)
	}
	var enabled bool
	err = tx.QueryRow(ctx, `SELECT enabled FROM venues WHERE id = $1 FOR UPDATE`, id).Scan(&enabled)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return ErrNotFound
	case err != nil:
		return fmt.Errorf("venues: reading id %d for set-default: %w", id, err)
	}
	if !enabled {
		return ErrDisabled
	}
	if err := defaults.Write(ctx, tx, defaults.KeyVenue, id); err != nil {
		return fmt.Errorf("venues: setting default to id %d: %w", id, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("venues: committing set-default of id %d: %w", id, err)
	}
	return nil
}

// UsageCounts returns the number of listings per venue. A venue with none is
// absent from the map, and so are listings with no venue.
func (s *Store) UsageCounts(ctx context.Context) (map[int64]int64, error) {
	rows, err := s.pool.Query(ctx, `SELECT venue_id, count(*) FROM workshops WHERE venue_id IS NOT NULL GROUP BY 1`)
	if err != nil {
		return nil, fmt.Errorf("venues: counting usage: %w", err)
	}
	defer rows.Close()
	out := make(map[int64]int64)
	for rows.Next() {
		var id, count int64
		if err := rows.Scan(&id, &count); err != nil {
			return nil, fmt.Errorf("venues: scanning usage count: %w", err)
		}
		out[id] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("venues: iterating usage counts: %w", err)
	}
	return out, nil
}

// Delete removes a venue that no listing uses. The foreign key's ON DELETE
// RESTRICT is the in-use guard, mapped to ErrInUse. Holding the setting's lock
// keeps a concurrent SetDefault from making this row the default mid-delete. It
// returns defaults.ErrUnset when the default cannot be determined.
func (s *Store) Delete(ctx context.Context, id int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("venues: beginning delete of id %d: %w", id, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	defaultID, err := defaults.Lock(ctx, tx, defaults.KeyVenue)
	if err != nil {
		return fmt.Errorf("venues: deleting id %d: %w", id, err)
	}
	var one int
	err = tx.QueryRow(ctx, `SELECT 1 FROM venues WHERE id = $1 FOR UPDATE`, id).Scan(&one)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return ErrNotFound
	case err != nil:
		return fmt.Errorf("venues: locking id %d for delete: %w", id, err)
	}
	if defaultID == id {
		return ErrIsDefault
	}
	if _, err := tx.Exec(ctx, `DELETE FROM venues WHERE id = $1`, id); err != nil {
		if pgCode(err) == "23503" && pgConstraint(err) == "workshops_venue_id_fkey" {
			return ErrInUse
		}
		return fmt.Errorf("venues: deleting id %d: %w", id, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("venues: committing delete of id %d: %w", id, err)
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
