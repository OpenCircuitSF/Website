// Package crt is the data-access layer for the home hero's CRT screen
// session (#0270, #0274, #0393; PRD is silent on this — it predates a PRD
// section and is filed under Phase 9, "Reusable as a dependency"). Rows,
// not the hard-coded array web/src/lib/crtScreen.ts's CRT_SESSION used to
// be — the user wants to change the screen's copy from time to time without
// a code edit, a rebuild, and a deploy.
//
// Modelled deliberately on internal/interests (CLAUDE.md §1, #0393's
// Design §1): same slug/sort_order/active shape, the same
// lowercase-hyphenated slug format, no dedicated Reorder method — reordering
// is two Update calls swapping sort_order, exactly like the admin interests
// screen already does (web/src/lib/admin.ts's reorderSwap), not a fifth
// route.
//
// Unlike interests, a crt_commands row has no foreign-key referents (nothing
// else in the schema points at it), so Delete here is an unconditional hard
// delete — there is no ErrHasSubscribers-shaped refusal to make.
package crt

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

// ErrNotFound is returned when no crt_commands row matches a lookup.
var ErrNotFound = errors.New("crt: not found")

// ErrInvalidSlug is returned when a slug fails the lowercase-hyphenated
// format check before a query is even issued -- mirrors
// interests.ErrInvalidSlug.
var ErrInvalidSlug = errors.New("crt: slug must be lowercase and hyphenated")

// ErrDuplicateSlug is returned when a Create would collide with an existing
// slug.
var ErrDuplicateSlug = errors.New("crt: slug already exists")

// ErrInvalidSource is returned when source is not one of the closed
// vocabulary the crt_commands_source_check CHECK constraint also enforces
// (migrations/000028): static, workshops, list_stats, interests.
var ErrInvalidSource = errors.New("crt: source must be one of static, workshops, list_stats, interests")

// Source values. "Static" serves the stored Output verbatim; the other three
// each name a live builder in web/src/lib/crtScreen.ts
// (crtWorkshopLines/crtListLines/crtInterestLines) that replaces the stored
// lines at render time when its own fetch succeeds -- and falls back to the
// stored Output, unmodified, when it does not (#0274's rule: the screen must
// never degrade to a blank block or an error string).
const (
	SourceStatic    = "static"
	SourceWorkshops = "workshops"
	SourceListStats = "list_stats"
	SourceInterests = "interests"
)

// validSources is the same closed set the database CHECK constraint
// enforces (migrations/000028_create_crt_commands.up.sql), checked here too
// so a caller gets ErrInvalidSource instead of a raw constraint-violation
// error, mirroring ValidSlug/interests.ValidSlug's convention.
var validSources = map[string]bool{
	SourceStatic:    true,
	SourceWorkshops: true,
	SourceListStats: true,
	SourceInterests: true,
}

// ValidSource reports whether source is one of the closed vocabulary above.
func ValidSource(source string) bool {
	return validSources[source]
}

// slugPattern matches the same format enforced by the database CHECK
// constraint: lowercase alphanumerics separated by single hyphens, no
// leading, trailing, or doubled hyphens. Identical to interests' own
// slugPattern (internal/interests/store.go); duplicated rather than
// imported since the two packages otherwise share no dependency, and a
// third package existing solely to hold one regex is not worth the
// indirection.
var slugPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// ValidSlug reports whether slug is lowercase and hyphenated.
func ValidSlug(slug string) bool {
	return slugPattern.MatchString(slug)
}

// Command is a single row of the crt_commands table.
type Command struct {
	ID        int64
	Slug      string
	Command   string
	Output    string // newline-separated lines; see Lines()
	Source    string
	SortOrder int
	Active    bool
	CreatedAt time.Time
	UpdatedAt *time.Time
}

// Lines splits Output on "\n" and trims trailing empty lines -- interior
// blank lines (e.g. the seeded 'fortune' row's blank line between two
// sentences) are preserved, since they are part of the intended output, not
// an artifact of storage. This is the one place that splitting happens; both
// the admin view (which sends the raw Output string to the <textarea>) and
// the public view (which needs a []string) go through it or through Output
// directly as appropriate.
func (c Command) Lines() []string {
	lines := strings.Split(c.Output, "\n")
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// Store is the data-access layer over the crt_commands table.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore constructs a Store over the shared connection pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

const commandColumns = `id, slug, command, output, source, sort_order, active, created_at, updated_at`

func scanCommand(row pgx.Row) (Command, error) {
	var c Command
	if err := row.Scan(&c.ID, &c.Slug, &c.Command, &c.Output, &c.Source, &c.SortOrder, &c.Active, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return Command{}, err
	}
	return c, nil
}

// List returns every crt_commands row, active or not, ordered by sort_order
// then slug -- for the admin screen (#0393's Design §4).
func (s *Store) List(ctx context.Context) ([]Command, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+commandColumns+` FROM crt_commands
		 ORDER BY sort_order, slug`)
	if err != nil {
		return nil, fmt.Errorf("crt: listing all: %w", err)
	}
	defer rows.Close()
	return collectCommands(rows)
}

// ListActive returns every active crt_commands row ordered by sort_order
// then slug -- for GET /api/crt-session, the public read the home page's CRT
// screen consumes.
func (s *Store) ListActive(ctx context.Context) ([]Command, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+commandColumns+` FROM crt_commands
		 WHERE active = TRUE
		 ORDER BY sort_order, slug`)
	if err != nil {
		return nil, fmt.Errorf("crt: listing active: %w", err)
	}
	defer rows.Close()
	return collectCommands(rows)
}

func collectCommands(rows pgx.Rows) ([]Command, error) {
	var out []Command
	for rows.Next() {
		c, err := scanCommand(rows)
		if err != nil {
			return nil, fmt.Errorf("crt: scanning row: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("crt: iterating rows: %w", err)
	}
	return out, nil
}

// GetByID looks up a single crt_commands row by its primary key. Returns
// ErrNotFound when no row matches, regardless of the active flag -- the
// admin PATCH handler needs to load an inactive row to reactivate it.
func (s *Store) GetByID(ctx context.Context, id int64) (Command, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT `+commandColumns+` FROM crt_commands WHERE id = $1`, id)
	c, err := scanCommand(row)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Command{}, ErrNotFound
	case err != nil:
		return Command{}, fmt.Errorf("crt: getting id %d: %w", id, err)
	}
	return c, nil
}

// Create inserts a new crt_commands row. slug must already be lowercase and
// hyphenated (ValidSlug), and source must be one of the closed vocabulary
// (ValidSource) -- both checked here so the caller gets a typed error
// instead of a raw constraint-violation error, with the database CHECKs
// (migrations/000028) as the backstop for any path that bypasses this
// method.
func (s *Store) Create(ctx context.Context, slug, command, output, source string, sortOrder int) (Command, error) {
	if !ValidSlug(slug) {
		return Command{}, ErrInvalidSlug
	}
	if !ValidSource(source) {
		return Command{}, ErrInvalidSource
	}
	row := s.pool.QueryRow(ctx,
		`INSERT INTO crt_commands (slug, command, output, source, sort_order)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING `+commandColumns,
		slug, command, output, source, sortOrder,
	)
	c, err := scanCommand(row)
	if err != nil {
		if isUniqueViolation(err) {
			return Command{}, ErrDuplicateSlug
		}
		return Command{}, fmt.Errorf("crt: creating %q: %w", slug, err)
	}
	return c, nil
}

// Update changes an existing crt_commands row's command, output, source,
// sort_order, and active flag. The slug is immutable through this method --
// same reasoning as interests.Store.Update: a future consumer (#0246's /crt
// route) may come to depend on a stable slug, and there is no rename UI to
// need this for.
func (s *Store) Update(ctx context.Context, id int64, command, output, source string, sortOrder int, active bool) (Command, error) {
	if !ValidSource(source) {
		return Command{}, ErrInvalidSource
	}
	row := s.pool.QueryRow(ctx,
		`UPDATE crt_commands
		    SET command = $2, output = $3, source = $4, sort_order = $5, active = $6, updated_at = now()
		  WHERE id = $1
		 RETURNING `+commandColumns,
		id, command, output, source, sortOrder, active,
	)
	c, err := scanCommand(row)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Command{}, ErrNotFound
	case err != nil:
		return Command{}, fmt.Errorf("crt: updating id %d: %w", id, err)
	}
	return c, nil
}

// Delete permanently removes a crt_commands row. Unlike
// interests.Store.Delete, this is unconditional: nothing else in the schema
// references crt_commands, so there is no history to preserve and no
// ErrHasSubscribers-shaped refusal to make. Deactivating (Update with
// active=false) remains the way to retire a row without losing it, matching
// the admin screen's toggle.
func (s *Store) Delete(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM crt_commands WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("crt: deleting id %d: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// isUniqueViolation reports whether err is a Postgres unique_violation
// (SQLSTATE 23505), e.g. a duplicate slug. Identical to
// interests.isUniqueViolation.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
