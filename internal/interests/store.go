// Package interests is the data-access layer for the workshop interest
// taxonomy (PRD §6.1, §6.2). Interests are rows, not a Go enum — new workshop
// themes appear constantly and adding one must not require a deploy.
//
// Deactivating an interest (Deactivate) must not remove it: existing
// subscriber_interests rows reference it and the historical record matters.
// `active = false` hides it from the signup form (via ListActive) while
// preserving associations. Delete (added by #0024, the admin CRUD) is
// deliberately narrower than its name suggests: it hard-removes a row only
// when zero subscriber_interests rows reference it, and refuses
// (ErrHasSubscribers) otherwise -- the moment any subscriber has ever
// selected an interest, Deactivate is the only way to retire it. The slug
// format and uniqueness constraints are enforced both here and by the
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

// ErrHasSubscribers is returned by Delete when one or more subscriber_interests
// rows still reference the interest. It is distinct from ErrNotFound so the
// admin handler (#0024) can tell "no such interest" apart from "that interest
// has history and must be deactivated instead of deleted".
var ErrHasSubscribers = errors.New("interests: has subscribers; deactivate instead of deleting")

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

// GetByID looks up a single interest by its primary key. Returns ErrNotFound
// when no row matches, regardless of the active flag -- mirrors GetBySlug's
// contract. Used by the admin PATCH handler (#0024) to read the current row
// before merging a partial patch onto it, since Update takes every field.
func (s *Store) GetByID(ctx context.Context, id int64) (Interest, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT `+interestColumns+` FROM interests WHERE id = $1`, id)
	it, err := scanInterest(row)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Interest{}, ErrNotFound
	case err != nil:
		return Interest{}, fmt.Errorf("interests: getting id %d: %w", id, err)
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

// SubscriberCounts returns, for every interest that has at least one
// subscriber_interests row, the number of subscribers currently associated
// with it. Interests are only present in the map when their count is greater
// than zero -- ListAll/ListActive supply the interests with zero. This is
// the count the admin CRUD screen (#0024) shows per interest: it tells the
// operator whether a segment is worth a campaign. Deactivated interests are
// included (a subscriber's history with a since-deactivated interest still
// counts), matching GetBySlug's "resolve regardless of active" convention.
func (s *Store) SubscriberCounts(ctx context.Context) (map[int64]int64, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT interest_id, count(*) FROM subscriber_interests GROUP BY interest_id`)
	if err != nil {
		return nil, fmt.Errorf("interests: counting subscribers: %w", err)
	}
	defer rows.Close()
	out := make(map[int64]int64)
	for rows.Next() {
		var id, count int64
		if err := rows.Scan(&id, &count); err != nil {
			return nil, fmt.Errorf("interests: scanning subscriber count: %w", err)
		}
		out[id] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("interests: iterating subscriber counts: %w", err)
	}
	return out, nil
}

// Delete permanently removes an interest row, but ONLY when no
// subscriber_interests rows reference it. This does not weaken the "no
// hard-delete" design documented at the top of this file: an interest that
// any subscriber has ever selected can still only be hidden via Deactivate,
// which preserves the historical association. Delete exists for the case
// Deactivate does not cover -- an interest created in error (a typo, a
// duplicate) that nobody has selected yet, where deactivating it would leave
// permanent clutter in the admin list for no reason.
//
// The refusal is enforced by the DELETE statement's own WHERE clause (a
// single atomic query, not a check-then-delete race), so a subscriber
// selecting the interest concurrently with this call cannot result in a
// referenced row being deleted. When the statement affects no rows, a
// follow-up existence check distinguishes ErrNotFound (no such id) from
// ErrHasSubscribers (exists, but is referenced) for the caller's error
// message; that follow-up has no effect on which outcome actually occurred.
func (s *Store) Delete(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM interests
		  WHERE id = $1
		    AND NOT EXISTS (SELECT 1 FROM subscriber_interests WHERE interest_id = $1)`,
		id)
	if err != nil {
		return fmt.Errorf("interests: deleting id %d: %w", id, err)
	}
	if tag.RowsAffected() > 0 {
		return nil
	}
	var exists bool
	if err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM interests WHERE id = $1)`, id,
	).Scan(&exists); err != nil {
		return fmt.Errorf("interests: checking existence after refused delete id %d: %w", id, err)
	}
	if !exists {
		return ErrNotFound
	}
	return ErrHasSubscribers
}

// isUniqueViolation reports whether err is a Postgres unique_violation
// (SQLSTATE 23505), e.g. a duplicate slug.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
