package cadence

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrNotFound is returned when no event_cadence_rules row has the id.
	ErrNotFound = errors.New("cadence: rule not found")
	// ErrEventTypeNotFound is returned when a rule names an event type that
	// does not exist.
	ErrEventTypeNotFound = errors.New("cadence: event type not found")
	// ErrVenueNotFound is returned when a rule names a venue that does not
	// exist.
	ErrVenueNotFound = errors.New("cadence: venue not found")
)

// Foreign keys whose violation maps to a sentinel (PostgreSQL's default
// names for 000055's REFERENCES clauses).
const (
	fkEventType = "event_cadence_rules_event_type_id_fkey"
	fkVenue     = "event_cadence_rules_venue_id_fkey"
)

// Store is the data-access layer over event_cadence_rules.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore constructs a Store over the shared connection pool.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

const ruleColumns = `id, name, event_type_id, title_prefix, frequency, weekday, per_month, venue_id, active, created_at, updated_at`

func scanRule(row pgx.Row) (Rule, error) {
	var (
		r         Rule
		prefix    *string
		frequency string
		weekday   *int16
		perMonth  *int16
	)
	if err := row.Scan(&r.ID, &r.Name, &r.EventTypeID, &prefix, &frequency, &weekday, &perMonth, &r.VenueID, &r.Active, &r.CreatedAt, &r.UpdatedAt); err != nil {
		return Rule{}, err
	}
	r.Frequency = Frequency(frequency)
	if prefix != nil {
		r.TitlePrefix = *prefix
	}
	if weekday != nil {
		d := time.Weekday(*weekday)
		r.Weekday = &d
	}
	if perMonth != nil {
		r.PerMonth = int(*perMonth)
	}
	return r, nil
}

// args are r's column values for an insert or update, in
// name, event_type_id, title_prefix, frequency, weekday, per_month,
// venue_id, active order.
func args(r Rule) []any {
	var prefix *string
	if r.TitlePrefix != "" {
		p := r.TitlePrefix
		prefix = &p
	}
	var weekday, perMonth *int16
	if r.Weekday != nil {
		w := int16(*r.Weekday)
		weekday = &w
	}
	if r.PerMonth != 0 {
		n := int16(r.PerMonth)
		perMonth = &n
	}
	return []any{r.Name, r.EventTypeID, prefix, string(r.Frequency), weekday, perMonth, r.VenueID, r.Active}
}

func mapErr(err error, what string) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" {
		switch pgErr.ConstraintName {
		case fkEventType:
			return ErrEventTypeNotFound
		case fkVenue:
			return ErrVenueNotFound
		}
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return fmt.Errorf("cadence: %s: %w", what, err)
}

// List returns every rule, active or not, by id.
func (s *Store) List(ctx context.Context) ([]Rule, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+ruleColumns+` FROM event_cadence_rules ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("cadence: listing: %w", err)
	}
	defer rows.Close()
	var out []Rule
	for rows.Next() {
		r, err := scanRule(rows)
		if err != nil {
			return nil, fmt.Errorf("cadence: scanning row: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("cadence: iterating rows: %w", err)
	}
	return out, nil
}

// Get returns one rule.
func (s *Store) Get(ctx context.Context, id int64) (Rule, error) {
	r, err := scanRule(s.pool.QueryRow(ctx, `SELECT `+ruleColumns+` FROM event_cadence_rules WHERE id = $1`, id))
	if err != nil {
		return Rule{}, mapErr(err, fmt.Sprintf("getting id %d", id))
	}
	return r, nil
}

// Create inserts r (its ID and timestamps are ignored). The caller runs
// Validate first; a CHECK violation comes back wrapped.
func (s *Store) Create(ctx context.Context, r Rule) (Rule, error) {
	out, err := scanRule(s.pool.QueryRow(ctx,
		`INSERT INTO event_cadence_rules (name, event_type_id, title_prefix, frequency, weekday, per_month, venue_id, active)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING `+ruleColumns, args(r)...))
	if err != nil {
		return Rule{}, mapErr(err, "creating")
	}
	return out, nil
}

// Update replaces every field of rule id with r's.
func (s *Store) Update(ctx context.Context, id int64, r Rule) (Rule, error) {
	out, err := scanRule(s.pool.QueryRow(ctx,
		`UPDATE event_cadence_rules
		    SET name = $2, event_type_id = $3, title_prefix = $4, frequency = $5, weekday = $6,
		        per_month = $7, venue_id = $8, active = $9, updated_at = now()
		  WHERE id = $1 RETURNING `+ruleColumns, append([]any{id}, args(r)...)...))
	if err != nil {
		return Rule{}, mapErr(err, fmt.Sprintf("updating id %d", id))
	}
	return out, nil
}

// Delete removes rule id.
func (s *Store) Delete(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM event_cadence_rules WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("cadence: deleting id %d: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
