// Package interests is the data-access layer for the workshop interest
// taxonomy (PRD §6.1, §6.2). Interests are rows, not a Go enum — new workshop
// themes appear constantly and adding one must not require a deploy.
//
// Deactivating an interest (Deactivate) must not remove it: existing
// subscriber_interests rows reference it and the historical record matters.
// `active = false` hides it from the signup form (via ListActive) while
// preserving associations. Delete (added by #0024, the admin CRUD) is
// deliberately narrower than its name suggests: it hard-removes a row only
// when nothing references it at all, and refuses otherwise. #0474 widened
// that check from subscriber_interests alone to the campaign and workshop
// join tables too, so the moment any subscriber has selected an interest,
// any campaign has targeted it, or any workshop has been tagged with it,
// Deactivate is the only way to retire it. The slug format and uniqueness
// constraints are enforced both here and by the
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

// ErrHasWorkshops is returned by Delete when one or more workshop_interests
// rows still reference the interest (#0474) -- a workshop is still tagged
// with it. workshop_interests.interest_id is ON DELETE CASCADE
// (migrations/000020), so without this check a delete that passed
// ErrHasSubscribers would silently strip the tag from every workshop that
// carries it.
var ErrHasWorkshops = errors.New("interests: has workshop tags; deactivate instead of deleting")

// ErrHasCampaigns is returned by Delete when one or more campaign_interests
// rows still reference the interest (#0474) -- a campaign was targeted at it.
// campaign_interests.interest_id is ON DELETE CASCADE (migrations/000017),
// so without this check a delete that passed ErrHasSubscribers would silently
// erase the record of who an already-sent campaign was targeted at -- the
// one of the three that cannot be reconstructed after the fact.
var ErrHasCampaigns = errors.New("interests: has campaign segments; deactivate instead of deleting")

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
// active flag. The slug is immutable through this method — not because a
// rename would break an already-issued link (measured for #0475: nothing
// references an interest by slug, since subscriber_interests,
// campaign_interests, and workshop_interests all key off id, and no issued
// preference-center link or other URL carries a slug at all) but because a
// slug is the taxonomy's stable external name, and changing one is a
// reviewed, recorded operation rather than a form field. The supported way
// to change a slug is a migration, per "Changing an existing slug" in
// docs/mailing-list.md.
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

// Delete permanently removes an interest row, but ONLY when nothing
// references it. #0474 widened this check from subscriber_interests alone.
// campaign_interests is ON DELETE CASCADE on interest_id (migrations/000017).
// workshop_interests is ON DELETE CASCADE on interest_id (migrations/000020).
// Left unchecked, either cascade would have let a delete silently erase
// every campaign's recorded segment or every workshop's topic tag. This
// does not weaken the "no hard-delete" design documented at the top of this
// file: an interest that any subscriber has ever selected, any campaign has
// ever targeted, or any workshop has ever tagged can still only be hidden
// via Deactivate, which preserves every historical association. Delete
// exists for the case Deactivate does not cover -- an interest created in
// error (a typo, a duplicate) that nothing has referenced yet, where
// deactivating it would leave permanent clutter in the admin list for no
// reason.
//
// Delete runs in a transaction that locks the interests row first, with
// `SELECT ... FOR UPDATE`, before checking any of the three referencing
// tables (#0477). Every INSERT into subscriber_interests, campaign_interests,
// or workshop_interests takes a FOR KEY SHARE lock on the interest row it
// references, to enforce the foreign key, and FOR KEY SHARE conflicts with
// FOR UPDATE -- so a tagging transaction racing this Delete cannot commit its
// insert while the lock is held, and Delete cannot proceed past the lock
// statement while a tagging transaction already holds it. The lock and the
// check are deliberately two separate statements: a lock taken inside the
// same statement as the check would buy nothing, since every sub-statement of
// one query shares a single snapshot, and only a statement whose snapshot is
// taken after the lock was granted can see a reference committed by the
// transaction it just waited for. Once the lock is granted, the check below
// runs against a fresh READ COMMITTED snapshot that reflects every write the
// lock forced to finish first, so a reference committed while Delete waited
// is always seen rather than cascaded away.
//
// The check itself is unchanged from #0474: a data-modifying CTE attempts the
// delete, and the surrounding SELECT reports both whether it succeeded and
// which of the three tables reference the row, all from the identical
// snapshot the DELETE's own NOT EXISTS clauses read. With the lock held first,
// this NOT EXISTS check is now provably accurate rather than merely usually
// accurate -- but it is kept rather than replaced by a plain read, as a
// database-level backstop: a mistake in the locking reasoning above would
// surface as a refusal here, never as a silent cascade. Reported in a fixed
// priority order when more than one table references the same row: the
// subscriber case first (it was the original, and only, guard before
// #0474), then the campaign case, then the workshop case.
func (s *Store) Delete(ctx context.Context, id int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("interests: beginning delete of id %d: %w", id, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var locked int64
	err = tx.QueryRow(ctx,
		`SELECT id FROM interests WHERE id = $1 FOR UPDATE`, id).Scan(&locked)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return ErrNotFound
	case err != nil:
		return fmt.Errorf("interests: locking id %d for delete: %w", id, err)
	}

	var deleted, hasSubscribers, hasCampaigns, hasWorkshops bool
	if err := tx.QueryRow(ctx,
		`WITH del AS (
		    DELETE FROM interests
		     WHERE id = $1
		       AND NOT EXISTS (SELECT 1 FROM subscriber_interests WHERE interest_id = $1)
		       AND NOT EXISTS (SELECT 1 FROM campaign_interests WHERE interest_id = $1)
		       AND NOT EXISTS (SELECT 1 FROM workshop_interests WHERE interest_id = $1)
		    RETURNING id
		 )
		 SELECT
		    EXISTS (SELECT 1 FROM del),
		    EXISTS (SELECT 1 FROM subscriber_interests WHERE interest_id = $1),
		    EXISTS (SELECT 1 FROM campaign_interests WHERE interest_id = $1),
		    EXISTS (SELECT 1 FROM workshop_interests WHERE interest_id = $1)`,
		id,
	).Scan(&deleted, &hasSubscribers, &hasCampaigns, &hasWorkshops); err != nil {
		return fmt.Errorf("interests: deleting id %d: %w", id, err)
	}
	if deleted {
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("interests: committing delete of id %d: %w", id, err)
		}
		return nil
	}
	switch {
	case hasSubscribers:
		return ErrHasSubscribers
	case hasCampaigns:
		return ErrHasCampaigns
	case hasWorkshops:
		return ErrHasWorkshops
	default:
		// Unreachable in ordinary operation: this transaction has held an
		// exclusive lock on the interests row since the statement above, so
		// nothing could have deleted it or added a reference to it between
		// the lock and this check. Reported as an error rather than assumed
		// away, per the doc comment's backstop reasoning.
		return fmt.Errorf("interests: delete id %d removed nothing while holding its row lock; retry", id)
	}
}

// isUniqueViolation reports whether err is a Postgres unique_violation
// (SQLSTATE 23505), e.g. a duplicate slug.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
