// Package workshops is the data-access layer for the public workshop
// listing (PRD §6.2's workshops/workshop_interests tables, migration 000020;
// #0050). It is the store #0051's admin and public HTTP handlers
// (internal/handlers/admin_workshops.go, public_workshops.go) sit on top of,
// and the eventual source #0054 adapts to internal/seo.WorkshopSource for
// the SEO renderer and sitemap.
//
// # Slug generation (deferred from #0050 to this issue)
//
// migrations/000020 enforces `slug TEXT UNIQUE NOT NULL` but generates
// nothing — #0050's reviewer confirmed that is a runtime concern, not
// something DDL can express (issues/0050.md, "Ruling 3"). Create derives the
// slug from Title (slugify below): lowercase, hyphenated, non-alphanumeric
// runs collapsed to a single hyphen, leading/trailing hyphens trimmed. A
// title that collides with an existing slug gets a numeric suffix appended
// ("-2", "-3", ...) — never "-1", since the bare slug already stands in for
// the first workshop with that title. The candidate is tried against the
// UNIQUE constraint directly, never a pre-check-then-insert race: each
// attempt is a real INSERT inside its own pseudo-nested transaction (a pgx
// savepoint), so two concurrent Creates with the same title can never both
// win the same slug.
//
// # Create is one transaction, savepoints and all (#0134)
//
// Create used to run the slug-retry loop as autocommitted statements outside
// any transaction, then open a second transaction just for the
// workshop_interests insert. That let a bad interest_id return
// ErrInterestNotFound while the workshop row it should have taken down with
// it stayed committed — a ghost draft that burned a slug and got no audit
// row (issues/0134.md). The whole method now runs inside one transaction:
// each slug attempt is a pgx pseudo-nested transaction (SAVEPOINT / RELEASE
// SAVEPOINT / ROLLBACK TO SAVEPOINT under the hood), so a unique_violation on
// one candidate only unwinds that attempt rather than aborting the outer
// transaction outright — Postgres would otherwise reject every later
// statement, including the next slug attempt and the interests insert, once
// one statement in a transaction fails. If the interests insert then fails
// with ErrInterestNotFound, the outer transaction's deferred Rollback undoes
// the workshop insert along with it, so no row survives.
//
// # published_at's two transitions
//
// Update's SQL uses a CASE over the ROW'S OWN CURRENT status column
// (compared atomically inside the same UPDATE, not a Go-side value read
// earlier) to decide what published_at becomes:
//
//   - any status -> published: published_at = now(). Republishing after an
//     unpublish stamps a fresh time, which is correct — the workshop is
//     newly live again.
//   - published -> draft (unpublish): published_at = NULL. The workshop is
//     no longer live and never really "was" from the reader's perspective;
//     clearing it means a later query can distinguish "never published" from
//     "currently published" with one NULL check.
//   - published -> canceled: published_at is left UNCHANGED, deliberately.
//     Cancel is not unpublish — PRD's Notes (issues/0051.md) are explicit
//     that a canceled workshop stays visible with a clear notice, and
//     preserving the original publish timestamp keeps "this was announced on
//     X, and later canceled" honest instead of erasing when it went live.
//   - every other transition (draft -> canceled, canceled -> draft, no
//     change): published_at is left unchanged.
//
// # Deleting a workshop a campaign announces
//
// migrations/000020 attaches email_campaigns_workshop_id_fkey with no ON
// DELETE clause (#0050's Ruling 1: blocking is deliberate, not an
// oversight — a campaign in the public archive that says "this announced
// workshop X" must not silently lose that provenance to a stray DELETE).
// Delete maps SQLSTATE 23503 on that specific constraint to ErrHasCampaigns
// so internal/handlers/admin_workshops.go can answer 409, never a bare 500 —
// #0050's reviewer made this binding on this issue.
package workshops

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Workshop status constants, verbatim from migration 000020's
// workshops_status_check CHECK.
const (
	StatusDraft     = "draft"
	StatusPublished = "published"
	StatusCanceled  = "canceled"
)

var (
	// ErrNotFound is returned when no workshops row matches a lookup.
	ErrNotFound = errors.New("workshops: not found")

	// ErrTitleRequired is returned by Create when title is empty — checked
	// here (not just left to the NOT NULL column) because an empty title
	// would slugify to "", and the fallback "workshop" base would silently
	// paper over a client bug rather than reporting it.
	ErrTitleRequired = errors.New("workshops: title is required")

	// ErrUnknownStatus is returned by Update when the target status is not
	// one of StatusDraft/StatusPublished/StatusCanceled. Defence in depth
	// behind migration 000020's CHECK constraint, mirroring
	// mailing.ErrUnknownAudienceMode's role for campaigns.
	ErrUnknownStatus = errors.New("workshops: unknown status")

	// ErrInterestNotFound is returned when one or more interest ids in a
	// Create/Update request do not exist (workshop_interests.interest_id's
	// FK, mapped from a 23503 to a typed error — mirrors
	// mailing.ErrCampaignInterestNotFound).
	ErrInterestNotFound = errors.New("workshops: one or more interest ids do not exist")

	// ErrHasCampaigns is returned by Delete when one or more email_campaigns
	// rows reference this workshop via workshop_id
	// (email_campaigns_workshop_id_fkey, migration 000020). Deleting is
	// refused; canceling the workshop is the intended path for "this isn't
	// happening" while a campaign still announces it (#0050's Ruling 1).
	ErrHasCampaigns = errors.New("workshops: has campaigns referencing it; cancel instead of deleting")

	// errSlugAttemptsExhausted is wrapped into the error Create returns if
	// maxSlugAttempts consecutive candidates all collide — effectively
	// unreachable in practice (it would require ~1000 existing workshops
	// sharing one title), but a bounded loop must still terminate somehow
	// rather than spin forever.
	errSlugAttemptsExhausted = errors.New("workshops: could not generate a unique slug")
)

// maxSlugAttempts bounds Create's collision-suffix retry loop (see the
// package doc comment). 1000 is generous relative to any real title
// collision count while still being a cheap loop to exhaust if it's ever
// hit.
const maxSlugAttempts = 1000

// slugNonAlnum matches any run of characters that are not a lowercase ASCII
// letter or digit, for collapsing into a single hyphen.
var slugNonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

// slugify derives a URL slug from a workshop title: lowercase, then every
// run of non-alphanumeric characters becomes one hyphen, with leading and
// trailing hyphens trimmed. An input that slugifies to "" (e.g. all
// punctuation) falls back to "workshop" so Create's INSERT never attempts an
// empty slug — the CHECK-adjacent NOT NULL constraint would reject it
// anyway, but this keeps the failure informative rather than a raw
// constraint-violation error.
func slugify(title string) string {
	lower := strings.ToLower(title)
	hyphenated := slugNonAlnum.ReplaceAllString(lower, "-")
	trimmed := strings.Trim(hyphenated, "-")
	if trimmed == "" {
		return "workshop"
	}
	return trimmed
}

// Workshop is a single workshops row plus its workshop_interests targeting,
// loaded as a side query (never part of the base SELECT — see interestIDs),
// mirroring mailing.Campaign's InterestIDs convention.
type Workshop struct {
	ID              int64
	Slug            string
	Title           string
	Summary         *string
	BodyMD          *string
	StartsAt        *time.Time
	EndsAt          *time.Time
	LocationName    *string
	LocationAddress *string
	LocationNote    *string
	Capacity        *int
	SignupURL       *string
	CoverImage      *string
	Status          string
	PublishedAt     *time.Time
	InterestIDs     []int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// CreateInput is the payload for Create. Status is deliberately absent —
// every new workshop starts in 'draft' (the column default, migration
// 000020), matching CampaignStore.Create's "always creates in draft"
// convention; Update is the only path to published/canceled.
type CreateInput struct {
	Title           string
	Summary         *string
	BodyMD          *string
	StartsAt        *time.Time
	EndsAt          *time.Time
	LocationName    *string
	LocationAddress *string
	LocationNote    *string
	Capacity        *int
	SignupURL       *string
	CoverImage      *string
	InterestIDs     []int64
}

// UpdateInput is the payload for Update. Unlike a partial PATCH body, this
// carries every field's fully-resolved target value — the handler merges
// the caller's optional patch fields onto the current row before calling
// this method, mirroring mailing.CampaignUpdate and
// AdminInterestsHandler.Patch's shared merge-then-call-Update shape. Status
// IS settable here (unlike CampaignUpdate, which never changes status) —
// PRD §8 gives workshops no dedicated publish/unpublish/cancel routes, only
// PATCH, so this is the sole transition path; see the package doc comment
// for what each transition does to PublishedAt.
type UpdateInput struct {
	Title           string
	Summary         *string
	BodyMD          *string
	StartsAt        *time.Time
	EndsAt          *time.Time
	LocationName    *string
	LocationAddress *string
	LocationNote    *string
	Capacity        *int
	SignupURL       *string
	CoverImage      *string
	Status          string
	InterestIDs     []int64
}

// Store is the data-access layer over workshops and workshop_interests.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore constructs a Store over the shared connection pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

const workshopColumns = `id, slug, title, summary, body_md, starts_at, ends_at,
	location_name, location_address, location_note, capacity, signup_url,
	cover_image, status, published_at, created_at, updated_at`

func scanWorkshop(row pgx.Row) (Workshop, error) {
	var w Workshop
	if err := row.Scan(
		&w.ID, &w.Slug, &w.Title, &w.Summary, &w.BodyMD, &w.StartsAt, &w.EndsAt,
		&w.LocationName, &w.LocationAddress, &w.LocationNote, &w.Capacity, &w.SignupURL,
		&w.CoverImage, &w.Status, &w.PublishedAt, &w.CreatedAt, &w.UpdatedAt,
	); err != nil {
		return Workshop{}, err
	}
	return w, nil
}

// isUniqueViolation reports whether err is a Postgres unique_violation
// (SQLSTATE 23505) — a copy of the same check every other store package in
// this codebase (interests, mailing) keeps locally rather than sharing, per
// the existing convention.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// isForeignKeyViolation reports whether err is a Postgres foreign_key_violation
// (SQLSTATE 23503).
func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}

// pgErrorConstraint returns the failing constraint name from a pgconn.PgError,
// or "" if err is not one. Delete uses this to distinguish
// email_campaigns_workshop_id_fkey (ErrHasCampaigns) from any other
// foreign-key violation that might theoretically fire on this table in the
// future, rather than assuming every 23503 here means "has campaigns".
func pgErrorConstraint(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.ConstraintName
	}
	return ""
}

// dedupeInt64 returns ids with duplicates removed, preserving first
// occurrence order — a copy of mailing.dedupeInt64's shape (each store
// package keeps its own, per that file's own doc comment on the
// convention).
func dedupeInt64(ids []int64) []int64 {
	if len(ids) == 0 {
		return ids
	}
	seen := make(map[int64]bool, len(ids))
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// replaceInterestsTx replaces a workshop's full workshop_interests set with
// interestIDs (delete-then-insert, in the caller's transaction) — mirrors
// mailing.CampaignStore.replaceInterestsTx exactly, substituting
// workshop_id for campaign_id.
func (s *Store) replaceInterestsTx(ctx context.Context, tx pgx.Tx, workshopID int64, interestIDs []int64) error {
	if _, err := tx.Exec(ctx,
		`DELETE FROM workshop_interests WHERE workshop_id = $1`, workshopID,
	); err != nil {
		return fmt.Errorf("workshops: clearing interests for %d: %w", workshopID, err)
	}
	for _, interestID := range dedupeInt64(interestIDs) {
		if _, err := tx.Exec(ctx,
			`INSERT INTO workshop_interests (workshop_id, interest_id) VALUES ($1, $2)`,
			workshopID, interestID,
		); err != nil {
			if isForeignKeyViolation(err) {
				return ErrInterestNotFound
			}
			return fmt.Errorf("workshops: linking interest %d to workshop %d: %w", interestID, workshopID, err)
		}
	}
	return nil
}

// interestIDs returns the sorted interest ids attached to a workshop.
func (s *Store) interestIDs(ctx context.Context, workshopID int64) ([]int64, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT interest_id FROM workshop_interests WHERE workshop_id = $1 ORDER BY interest_id`, workshopID)
	if err != nil {
		return nil, fmt.Errorf("workshops: loading interests for workshop %d: %w", workshopID, err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("workshops: scanning workshop interest row: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("workshops: iterating workshop interest rows: %w", err)
	}
	return ids, nil
}

// interestIDsByWorkshop batch-loads interest ids for every workshop id in
// ids, for List/ListVisible (avoids N+1 queries) — mirrors
// mailing.CampaignStore.interestIDsByCampaign.
func (s *Store) interestIDsByWorkshop(ctx context.Context, ids []int64) (map[int64][]int64, error) {
	out := make(map[int64][]int64, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx,
		`SELECT workshop_id, interest_id FROM workshop_interests
		  WHERE workshop_id = ANY($1)
		  ORDER BY workshop_id, interest_id`, ids)
	if err != nil {
		return nil, fmt.Errorf("workshops: batch loading workshop interests: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var workshopID, interestID int64
		if err := rows.Scan(&workshopID, &interestID); err != nil {
			return nil, fmt.Errorf("workshops: scanning workshop interest row: %w", err)
		}
		out[workshopID] = append(out[workshopID], interestID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("workshops: iterating workshop interest rows: %w", err)
	}
	return out, nil
}

func attachInterests(ws []Workshop, interestMap map[int64][]int64) {
	for i := range ws {
		ws[i].InterestIDs = interestMap[ws[i].ID]
	}
}

func collectWorkshops(rows pgx.Rows) ([]Workshop, error) {
	var out []Workshop
	for rows.Next() {
		w, err := scanWorkshop(rows)
		if err != nil {
			return nil, fmt.Errorf("workshops: scanning row: %w", err)
		}
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("workshops: iterating rows: %w", err)
	}
	return out, nil
}

// List returns every workshop — any status — newest-created first, each
// carrying its InterestIDs. For the admin CRUD screen (#0052); no
// pagination, matching CampaignStore.List's reasoning (a workshop catalog is
// small).
func (s *Store) List(ctx context.Context) ([]Workshop, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+workshopColumns+` FROM workshops ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, fmt.Errorf("workshops: listing: %w", err)
	}
	defer rows.Close()
	ws, err := collectWorkshops(rows)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, len(ws))
	for i, w := range ws {
		ids[i] = w.ID
	}
	interestMap, err := s.interestIDsByWorkshop(ctx, ids)
	if err != nil {
		return nil, err
	}
	attachInterests(ws, interestMap)
	return ws, nil
}

// ListVisible returns every workshop the public may see — status
// 'published' OR 'canceled', never 'draft' — split into upcoming and past
// relative to now, for the public listing (GET /api/workshops; #0135).
//
// Named ListVisible, not ListPublished, since #0135: the index used to be
// published-only, which made the detail route (GetBySlug, #0051) and the
// index route disagree about a canceled workshop — GetBySlug served it,
// List silently dropped it. That produced exactly the outcome GetBySlug's
// own reasoning exists to prevent ("a 404 tells them nothing"): someone who
// bookmarked the index would see a canceled workshop vanish with no
// explanation, learning it was canceled only if they still held the direct
// link. #0135 widened the index to match the detail route, so both routes
// now apply the same visibility rule (published or canceled; never draft),
// and #0053's "canceled workshops shown with a clear canceled badge"
// criterion can actually be reached.
//
// A canceled workshop is placed in upcoming/past by the same starts_at
// comparison as a published one — a canceled workshop whose date hasn't
// passed yet is the case that matters (a reader checking on a session they
// planned to attend), and dropping it from upcoming the moment its date
// passes would just delay the same silent-vanish problem by one week. A
// canceled workshop safely in the past is harmless either way and reads as
// honest history there.
//
// A workshop with no starts_at set (TBD scheduling) is treated as upcoming
// — it has no past date to belong to, and hiding an announced-but-undated
// workshop entirely would be worse than surfacing it. upcoming is ordered
// soonest-first (NULLS LAST, so a TBD date sorts after every dated one);
// past is ordered most-recent-first.
//
// Uses idx_workshops_published (migration 000020's partial index, widened
// by #0135 to WHERE status <> 'draft' to keep covering this query) for both
// queries.
func (s *Store) ListVisible(ctx context.Context, now time.Time) (upcoming, past []Workshop, err error) {
	upcomingRows, err := s.pool.Query(ctx,
		`SELECT `+workshopColumns+` FROM workshops
		  WHERE status IN ($1, $2) AND (starts_at IS NULL OR starts_at >= $3)
		  ORDER BY starts_at ASC NULLS LAST, id ASC`,
		StatusPublished, StatusCanceled, now)
	if err != nil {
		return nil, nil, fmt.Errorf("workshops: listing upcoming visible: %w", err)
	}
	upcoming, err = collectWorkshops(upcomingRows)
	upcomingRows.Close()
	if err != nil {
		return nil, nil, err
	}

	pastRows, err := s.pool.Query(ctx,
		`SELECT `+workshopColumns+` FROM workshops
		  WHERE status IN ($1, $2) AND starts_at IS NOT NULL AND starts_at < $3
		  ORDER BY starts_at DESC, id DESC`,
		StatusPublished, StatusCanceled, now)
	if err != nil {
		return nil, nil, fmt.Errorf("workshops: listing past visible: %w", err)
	}
	past, err = collectWorkshops(pastRows)
	pastRows.Close()
	if err != nil {
		return nil, nil, err
	}

	all := make([]Workshop, 0, len(upcoming)+len(past))
	all = append(all, upcoming...)
	all = append(all, past...)
	ids := make([]int64, len(all))
	for i, w := range all {
		ids[i] = w.ID
	}
	interestMap, err := s.interestIDsByWorkshop(ctx, ids)
	if err != nil {
		return nil, nil, err
	}
	attachInterests(upcoming, interestMap)
	attachInterests(past, interestMap)
	return upcoming, past, nil
}

// GetByID loads a single workshop with its InterestIDs, any status. Used by
// the admin handler (#0052) and by Update/Delete's own pre-reads. Returns
// ErrNotFound when no row matches.
func (s *Store) GetByID(ctx context.Context, id int64) (Workshop, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+workshopColumns+` FROM workshops WHERE id = $1`, id)
	w, err := scanWorkshop(row)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Workshop{}, ErrNotFound
	case err != nil:
		return Workshop{}, fmt.Errorf("workshops: getting id %d: %w", id, err)
	}
	ids, err := s.interestIDs(ctx, id)
	if err != nil {
		return Workshop{}, err
	}
	w.InterestIDs = ids
	return w, nil
}

// GetBySlug loads a single workshop by slug, any status — the public detail
// handler (#0051's own GET /api/workshops/{slug}) decides what a non-admin
// caller is allowed to see based on the returned Status; this method itself
// applies no visibility filtering, matching interests.Store.GetBySlug's
// "resolve regardless of state" convention. Returns ErrNotFound when no row
// matches.
func (s *Store) GetBySlug(ctx context.Context, slug string) (Workshop, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+workshopColumns+` FROM workshops WHERE slug = $1`, slug)
	w, err := scanWorkshop(row)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Workshop{}, ErrNotFound
	case err != nil:
		return Workshop{}, fmt.Errorf("workshops: getting slug %q: %w", slug, err)
	}
	ids, err := s.interestIDs(ctx, w.ID)
	if err != nil {
		return Workshop{}, err
	}
	w.InterestIDs = ids
	return w, nil
}

// Create inserts a new workshop in status='draft' plus its workshop_interests
// rows, generating a unique slug from in.Title (see the package doc comment
// for the collision-suffix algorithm). Returns ErrTitleRequired if Title is
// empty. The whole thing — slug retries and the interest links — runs inside
// one transaction (#0134), so a bad interest id rolls back the workshop
// insert too, leaving no ghost row; see the package doc comment for how the
// slug-retry loop stays race-safe under that transaction.
func (s *Store) Create(ctx context.Context, in CreateInput) (Workshop, error) {
	title := strings.TrimSpace(in.Title)
	if title == "" {
		return Workshop{}, ErrTitleRequired
	}
	base := slugify(title)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Workshop{}, fmt.Errorf("workshops: beginning create tx for %q: %w", title, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var w Workshop
	var insertErr error
	for attempt := 0; attempt < maxSlugAttempts; attempt++ {
		candidate := base
		if attempt > 0 {
			candidate = fmt.Sprintf("%s-%d", base, attempt+1)
		}
		// Each attempt runs inside its own pseudo-nested transaction (pgx
		// implements Tx.Begin on a Tx as a SAVEPOINT/RELEASE
		// SAVEPOINT/ROLLBACK TO SAVEPOINT triple), so a unique_violation on
		// this candidate only unwinds this one attempt rather than aborting
		// the whole outer transaction — the failed INSERT would otherwise
		// leave the transaction in Postgres's "current transaction is
		// aborted" state and reject every later statement, including the
		// next attempt and the interests insert below. The attempt is still
		// a real INSERT fought out against the UNIQUE constraint, not a
		// pre-check-then-insert race, so two concurrent Creates with the
		// same title still can never both win the same slug.
		sp, err := tx.Begin(ctx)
		if err != nil {
			return Workshop{}, fmt.Errorf("workshops: starting slug-attempt savepoint for %q: %w", title, err)
		}
		row := sp.QueryRow(ctx,
			`INSERT INTO workshops (slug, title, summary, body_md, starts_at, ends_at,
				location_name, location_address, location_note, capacity, signup_url, cover_image)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
			 RETURNING `+workshopColumns,
			candidate, title, in.Summary, in.BodyMD, in.StartsAt, in.EndsAt,
			in.LocationName, in.LocationAddress, in.LocationNote, in.Capacity, in.SignupURL, in.CoverImage,
		)
		w, insertErr = scanWorkshop(row)
		if insertErr == nil {
			if err := sp.Commit(ctx); err != nil {
				return Workshop{}, fmt.Errorf("workshops: releasing slug-attempt savepoint for %q: %w", title, err)
			}
			break
		}
		if !isUniqueViolation(insertErr) {
			return Workshop{}, fmt.Errorf("workshops: creating %q: %w", title, insertErr)
		}
		// Collision on this candidate slug — roll back to the savepoint
		// (undoing just this failed attempt, not the outer transaction) and
		// try the next suffix.
		if err := sp.Rollback(ctx); err != nil {
			return Workshop{}, fmt.Errorf("workshops: rolling back slug-attempt savepoint for %q: %w", title, err)
		}
	}
	if insertErr != nil {
		return Workshop{}, fmt.Errorf("workshops: creating %q: %w", title, errSlugAttemptsExhausted)
	}

	interestIDs := dedupeInt64(in.InterestIDs)
	if err := s.replaceInterestsTx(ctx, tx, w.ID, interestIDs); err != nil {
		return Workshop{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Workshop{}, fmt.Errorf("workshops: committing create tx for %q: %w", title, err)
	}
	w.InterestIDs = interestIDs
	return w, nil
}

// Update replaces a workshop's content fields, status, and targeting in one
// atomic statement. The slug is immutable through this method (same
// reasoning as interests.Store.Update: a workshop's slug may already be
// linked from a shared social post or a campaign's archive entry — see this
// issue's Gotchas for why no rename path exists). See the package doc
// comment for exactly what each status transition does to PublishedAt; the
// CASE expression below reads the row's OWN status column, so the decision
// is made atomically against whatever the current value in the database is,
// not a value read earlier by the caller.
func (s *Store) Update(ctx context.Context, id int64, in UpdateInput) (Workshop, error) {
	title := strings.TrimSpace(in.Title)
	if title == "" {
		return Workshop{}, ErrTitleRequired
	}
	switch in.Status {
	case StatusDraft, StatusPublished, StatusCanceled:
	default:
		return Workshop{}, ErrUnknownStatus
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Workshop{}, fmt.Errorf("workshops: beginning update tx for %d: %w", id, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	row := tx.QueryRow(ctx,
		`UPDATE workshops
		    SET title = $2, summary = $3, body_md = $4, starts_at = $5, ends_at = $6,
		        location_name = $7, location_address = $8, location_note = $9,
		        capacity = $10, signup_url = $11, cover_image = $12,
		        status = $13,
		        published_at = CASE
		            WHEN $13 = 'published' AND status IS DISTINCT FROM 'published' THEN now()
		            WHEN status = 'published' AND $13 = 'draft' THEN NULL
		            ELSE published_at
		        END,
		        updated_at = now()
		  WHERE id = $1
		  RETURNING `+workshopColumns,
		id, title, in.Summary, in.BodyMD, in.StartsAt, in.EndsAt,
		in.LocationName, in.LocationAddress, in.LocationNote, in.Capacity, in.SignupURL, in.CoverImage,
		in.Status,
	)
	updated, err := scanWorkshop(row)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Workshop{}, ErrNotFound
	case err != nil:
		return Workshop{}, fmt.Errorf("workshops: updating id %d: %w", id, err)
	}

	interestIDs := dedupeInt64(in.InterestIDs)
	if err := s.replaceInterestsTx(ctx, tx, id, interestIDs); err != nil {
		return Workshop{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Workshop{}, fmt.Errorf("workshops: committing update tx for %d: %w", id, err)
	}
	updated.InterestIDs = interestIDs
	return updated, nil
}

// Delete permanently removes a workshop row. Refused with ErrHasCampaigns
// when any email_campaigns row still references it via workshop_id
// (email_campaigns_workshop_id_fkey, migration 000020, no ON DELETE clause —
// #0050's Ruling 1) — the caller (internal/handlers/admin_workshops.go) maps
// that to a 409, never a bare 500. workshop_interests rows cascade
// automatically (ON DELETE CASCADE) and need no separate cleanup here.
func (s *Store) Delete(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM workshops WHERE id = $1`, id)
	if err != nil {
		if isForeignKeyViolation(err) && pgErrorConstraint(err) == "email_campaigns_workshop_id_fkey" {
			return ErrHasCampaigns
		}
		return fmt.Errorf("workshops: deleting id %d: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
