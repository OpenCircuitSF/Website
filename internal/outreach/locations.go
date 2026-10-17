package outreach

import (
	"context"
	"errors"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	// ErrLocationNotFound is returned when no location matches an id.
	ErrLocationNotFound = errors.New("outreach: location not found")
	// ErrCodeTaken is returned when a location code is already in use.
	ErrCodeTaken = errors.New("outreach: location code already in use")
	// ErrCodeLocked is returned when a code change is refused because a flyer
	// link already uses the location.
	ErrCodeLocked = errors.New("outreach: location code is locked by flyer links")
	// ErrLocationInUse is returned when a delete is refused because a flyer
	// link uses the location.
	ErrLocationInUse = errors.New("outreach: location has flyer links")
)

// CodeMinLen and CodeMaxLen bound a location code's length.
const (
	CodeMinLen = 2
	CodeMaxLen = 16
)

var codePattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// ValidCode reports whether s is a legal location code: 2-16 characters,
// lowercase ASCII letters, digits and single interior dashes. The same rule
// is enforced by the table's CHECK constraint and the SPA's validateCode.
func ValidCode(s string) bool {
	return len(s) >= CodeMinLen && len(s) <= CodeMaxLen && codePattern.MatchString(s)
}

// Location is one row of the outreach_locations table.
type Location struct {
	ID           int64
	Name         string
	Code         string
	Address      string
	Notes        string
	Active       bool
	CodeLockedAt *time.Time
	CreatedAt    time.Time
	UpdatedAt    *time.Time
}

// LocationInput is the data for a new location.
type LocationInput struct {
	Name    string
	Code    string
	Address string
	Notes   string
	Active  bool
}

// LocationPatch is a partial update; a nil field is left unchanged.
type LocationPatch struct {
	Name    *string
	Code    *string
	Address *string
	Notes   *string
	Active  *bool
}

// Execer is satisfied by *pgxpool.Pool and pgx.Tx, so a caller can run
// MarkLocationUsed inside its own transaction.
type Execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

const locationCols = `id, name, code, address, notes, active, code_locked_at, created_at, updated_at`

func scanLocation(row pgx.Row) (Location, error) {
	var l Location
	err := row.Scan(&l.ID, &l.Name, &l.Code, &l.Address, &l.Notes, &l.Active, &l.CodeLockedAt, &l.CreatedAt, &l.UpdatedAt)
	return l, err
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func isFKViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}

// ListLocations returns every location, active first, then by name.
func (s *Store) ListLocations(ctx context.Context) ([]Location, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+locationCols+` FROM outreach_locations ORDER BY active DESC, lower(name), id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Location{}
	for rows.Next() {
		l, err := scanLocation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// GetLocation loads one location, or ErrLocationNotFound.
func (s *Store) GetLocation(ctx context.Context, id int64) (Location, error) {
	l, err := scanLocation(s.pool.QueryRow(ctx, `SELECT `+locationCols+` FROM outreach_locations WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Location{}, ErrLocationNotFound
	}
	return l, err
}

// CreateLocation inserts a location. A duplicate code is ErrCodeTaken.
func (s *Store) CreateLocation(ctx context.Context, in LocationInput) (Location, error) {
	l, err := scanLocation(s.pool.QueryRow(ctx,
		`INSERT INTO outreach_locations (name, code, address, notes, active) VALUES ($1, $2, $3, $4, $5) RETURNING `+locationCols,
		in.Name, in.Code, in.Address, in.Notes, in.Active))
	if isUniqueViolation(err) {
		return Location{}, ErrCodeTaken
	}
	return l, err
}

// UpdateLocation applies a partial update in one statement. Changing the code
// of a location a flyer link already uses is ErrCodeLocked; re-sending its
// current code is fine.
func (s *Store) UpdateLocation(ctx context.Context, id int64, p LocationPatch) (Location, error) {
	l, err := scanLocation(s.pool.QueryRow(ctx,
		`UPDATE outreach_locations SET
		     name = COALESCE($2, name), code = COALESCE($3, code),
		     address = COALESCE($4, address), notes = COALESCE($5, notes),
		     active = COALESCE($6, active), updated_at = now()
		 WHERE id = $1 AND (code_locked_at IS NULL OR $3::text IS NULL OR $3::text = code)
		 RETURNING `+locationCols,
		id, p.Name, p.Code, p.Address, p.Notes, p.Active))
	switch {
	case err == nil:
		return l, nil
	case isUniqueViolation(err):
		return Location{}, ErrCodeTaken
	case errors.Is(err, pgx.ErrNoRows):
		if _, gerr := s.GetLocation(ctx, id); gerr != nil {
			return Location{}, gerr
		}
		return Location{}, ErrCodeLocked
	}
	return Location{}, err
}

// DeleteLocation deletes an unused location. A used one is ErrLocationInUse:
// deactivate it instead.
func (s *Store) DeleteLocation(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM outreach_locations WHERE id = $1 AND code_locked_at IS NULL`, id)
	if isFKViolation(err) {
		return ErrLocationInUse
	}
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		if _, gerr := s.GetLocation(ctx, id); gerr != nil {
			return gerr
		}
		return ErrLocationInUse
	}
	return nil
}

// MarkLocationUsed records that a flyer link uses the location, locking its
// code and blocking deletion. Idempotent: the first timestamp is kept. The
// flyer-link issue (#0549) calls it inside the transaction that inserts a
// link row, passing its pgx.Tx as q.
func (s *Store) MarkLocationUsed(ctx context.Context, q Execer, id int64) error {
	tag, err := q.Exec(ctx, `UPDATE outreach_locations SET code_locked_at = COALESCE(code_locked_at, now()) WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrLocationNotFound
	}
	return nil
}
