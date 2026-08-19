// Package interests is the data-access layer for the workshop interest
// taxonomy (PRD §6.1, §6.2). Interests are rows, not a Go enum — new workshop
// themes appear constantly and adding one must not require a deploy.
//
// There is deliberately no hard-delete method. Deactivating an interest
// (Deactivate) must not remove it: existing subscriber_interests rows
// reference it and the historical record matters. `active = false` hides it
// from the signup form (via ListActive) while preserving associations. The
// slug format and uniqueness constraints are enforced both here and by the
// `interests_slug_format` CHECK and UNIQUE index added in
// migrations/000009_create_interests.up.sql, so a future direct INSERT can't
// slip past them either.
package interests

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

// ErrNotFound is returned when no interests row matches a lookup.
var ErrNotFound = errors.New("interests: not found")

// ErrInvalidSlug is returned when a slug fails the lowercase-hyphenated format
// check before a query is even issued.
var ErrInvalidSlug = errors.New("interests: slug must be lowercase and hyphenated")

// ErrDuplicateSlug is returned when a Create or rename would collide with an
// existing slug.
var ErrDuplicateSlug = errors.New("interests: slug already exists")

// slugPattern matches the same format enforced by the database CHECK
// constraint: lowercase alphanumerics separated by single hyphens, no leading,
// trailing, or doubled hyphens.
var slugPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// ValidSlug reports whether slug is lowercase and hyphenated.
func ValidSlug(slug string) bool {
	return slugPattern.MatchString(slug)
}

// Interest is a single row of the interests table.
type Interest struct {
	ID          int64
	Slug        string
	Name        string
	Description *string
	SortOrder   int
	Active      bool
	CreatedAt   time.Time
}

// Store is the data-access layer over the interests table.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore constructs a Store over the shared connection pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

const interestColumns = `id, slug, name, description, sort_order, active, created_at`

func scanInterest(row pgx.Row) (Interest, error) {
	var it Interest
	if err := row.Scan(&it.ID, &it.Slug, &it.Name, &it.Description, &it.SortOrder, &it.Active, &it.CreatedAt); err != nil {
		return Interest{}, err
	}
	return it, nil
}

// ListActive returns every active interest ordered by sort_order then name,
// for rendering the public signup form. Inactive interests never appear here
// even though their subscriber_interests rows persist.
func (s *Store) ListActive(ctx context.Context) ([]Interest, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+interestColumns+` FROM interests
		 WHERE active = TRUE
		 ORDER BY sort_order, name`)
	if err != nil {
		return nil, fmt.Errorf("interests: listing active: %w", err)
	}
	defer rows.Close()
	return collectInterests(rows)
}

// ListAll returns every interest, active or not, ordered by sort_order then
// name, for the admin CRUD screen (#0024).
func (s *Store) ListAll(ctx context.Context) ([]Interest, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+interestColumns+` FROM interests
		 ORDER BY sort_order, name`)
	if err != nil {
		return nil, fmt.Errorf("interests: listing all: %w", err)
	}
	defer rows.Close()
	return collectInterests(rows)
}

func collectInterests(rows pgx.Rows) ([]Interest, error) {
	var out []Interest
	for rows.Next() {
		it, err := scanInterest(rows)
		if err != nil {
			return nil, fmt.Errorf("interests: scanning row: %w", err)
		}
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("interests: iterating rows: %w", err)
	}
	return out, nil
}

// GetBySlug looks up a single interest by its slug. Returns ErrNotFound when
// no row matches, regardless of the active flag (a subscriber's preference
// center needs to resolve a slug that has since been deactivated).
func (s *Store) GetBySlug(ctx context.Context, slug string) (Interest, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT `+interestColumns+` FROM interests WHERE slug = $1`, slug)
	it, err := scanInterest(row)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Interest{}, ErrNotFound
	case err != nil:
		return Interest{}, fmt.Errorf("interests: getting slug %q: %w", slug, err)
	}
	return it, nil
}

// GetByIDs resolves a set of interest ids, ignoring any that don't exist. Used
// by the subscribers store to validate a signup form's selected interest ids
// before writing subscriber_interests rows.
func (s *Store) GetByIDs(ctx context.Context, ids []int64) ([]Interest, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx,
		`SELECT `+interestColumns+` FROM interests WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, fmt.Errorf("interests: getting by ids: %w", err)
	}
	defer rows.Close()
	return collectInterests(rows)
}

// Create inserts a new interest. slug must already be lowercase and
// hyphenated (ValidSlug) — checked here so the caller gets ErrInvalidSlug
// instead of a raw constraint-violation error, with the database CHECK as the
// backstop for any path that bypasses this method.
func (s *Store) Create(ctx context.Context, slug, name string, description *string, sortOrder int) (Interest, error) {
	if !ValidSlug(slug) {
		return Interest{}, ErrInvalidSlug
	}
	row := s.pool.QueryRow(ctx,
		`INSERT INTO interests (slug, name, description, sort_order)
		 VALUES ($1, $2, $3, $4)
		 RETURNING `+interestColumns,
		slug, name, description, sortOrder,
	)
	it, err := scanInterest(row)
	if err != nil {
		if isUniqueViolation(err) {
			return Interest{}, ErrDuplicateSlug
		}
		return Interest{}, fmt.Errorf("interests: creating %q: %w", slug, err)
	}
	return it, nil
}

// Update changes an existing interest's name, description, sort_order, and
// active flag. The slug is immutable through this method — renaming a slug
// would break any already-issued preference-center link that references it
// by slug; #0024 can add a dedicated rename path later if that's ever needed.
func (s *Store) Update(ctx context.Context, id int64, name string, description *string, sortOrder int, active bool) (Interest, error) {
	row := s.pool.QueryRow(ctx,
		`UPDATE interests
		    SET name = $2, description = $3, sort_order = $4, active = $5
		  WHERE id = $1
		 RETURNING `+interestColumns,
		id, name, description, sortOrder, active,
	)
	it, err := scanInterest(row)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Interest{}, ErrNotFound
	case err != nil:
		return Interest{}, fmt.Errorf("interests: updating id %d: %w", id, err)
	}
	return it, nil
}

// Deactivate sets active = FALSE without deleting the row, per the issue's
// notes: existing subscriber_interests rows reference it and the historical
// record matters.
func (s *Store) Deactivate(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE interests SET active = FALSE WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("interests: deactivating id %d: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// isUniqueViolation reports whether err is a Postgres unique_violation
// (SQLSTATE 23505), e.g. a duplicate slug.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
