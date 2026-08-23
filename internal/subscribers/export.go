// export.go adds the streaming CSV export query (#0059, PRD §8: GET
// /admin/subscribers/export). It is a separate query from List (store.go),
// not a reuse of it, for two reasons:
//
//  1. List buffers a full page into a []Subscriber slice, sized for a
//     paginated admin screen. An export can be the whole table, and the
//     acceptance criteria require it not be buffered in memory — so this
//     file streams rows to a caller-supplied callback as they arrive off the
//     wire instead of collecting them first.
//  2. The export needs each subscriber's interests as a single
//     semicolon-joined string, which List's row shape has no room for (its
//     Subscriber.Interests would require a second query per row — #0032's
//     admin detail view already does exactly that, deliberately, only for
//     the one-row case). Here it is computed once per subscriber with
//     string_agg over a LEFT JOIN, at the database, in the same query that
//     applies the caller's filter.
//
// synthetic = false is unconditional here for the identical reason it is in
// List/StatusCounts (see store.go's package doc comment): every admin who
// has ever run a campaign test send (#0046) has a
// campaign-test+admin-<id>@internal.opencircuitsf.test fixture row, and an
// operator-facing export must never include it.
package subscribers

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// ExportRow is one row of the streaming export. Deliberately narrower than
// Subscriber: no id, no confirm_token/manage_token (live bearer credentials
// — see internal/handlers/admin_subscribers.go's subscriberView doc comment
// on why those never leave the process as display/export data), no
// signup_ip/signup_user_agent/unsubscribed_at/unsubscribe_source (not asked
// for by #0059's acceptance criteria, and the subscribers admin *screen*
// doesn't expose signup_ip/signup_user_agent as export-shaped columns
// either — see the handler for the full column-set justification). Matches
// exactly the eight columns #0059 lists: email, status, interests,
// confirmed_at, created_at, utm_source, utm_medium, utm_campaign.
type ExportRow struct {
	Email       string
	Status      string
	Interests   string // semicolon-joined interest slugs, alphabetical; "" when none selected
	ConfirmedAt *time.Time
	CreatedAt   time.Time
	UTMSource   *string
	UTMMedium   *string
	UTMCampaign *string
}

// StreamExport runs filter (Status/InterestID/Query — the same fields List
// consults; Page/PerPage are ignored, an export is never paginated) against
// the subscribers table and invokes fn once per matching row, in the same
// newest-first order List uses, as rows arrive from the database — never
// collecting them into a slice first. If fn returns an error, iteration
// stops immediately and that error is returned (wrapped); the caller
// (internal/handlers/admin_subscribers.go's Export) uses this to abort a
// write that has failed partway through a chunked HTTP response.
//
// The interest filter is applied via an EXISTS subquery against
// subscriber_interests, deliberately independent of the LEFT JOIN used to
// compute the Interests column below: filtering the aggregation join itself
// to one interest_id would truncate a matching subscriber's OTHER selected
// interests out of the exported semicolon-joined list — EXISTS narrows which
// subscribers qualify without touching what gets aggregated for them.
func (s *Store) StreamExport(ctx context.Context, filter ListFilter, fn func(ExportRow) error) error {
	var (
		where = []string{`s.synthetic = false`}
		args  []any
	)
	if filter.InterestID != 0 {
		args = append(args, filter.InterestID)
		where = append(where, fmt.Sprintf(
			`EXISTS (SELECT 1 FROM subscriber_interests si WHERE si.subscriber_id = s.id AND si.interest_id = $%d)`,
			len(args)))
	}
	if filter.Status != "" {
		args = append(args, filter.Status)
		where = append(where, fmt.Sprintf(`s.status = $%d`, len(args)))
	}
	if q := strings.TrimSpace(filter.Query); q != "" {
		args = append(args, "%"+escapeLikeSpecials(q)+"%")
		where = append(where, fmt.Sprintf(`s.email ILIKE $%d ESCAPE '\'`, len(args)))
	}

	query := fmt.Sprintf(`
		SELECT s.email, s.status, s.confirmed_at, s.created_at,
		       s.utm_source, s.utm_medium, s.utm_campaign,
		       COALESCE(string_agg(i.slug, ';' ORDER BY i.slug), '') AS interests
		  FROM subscribers s
		  LEFT JOIN subscriber_interests si2 ON si2.subscriber_id = s.id
		  LEFT JOIN interests i ON i.id = si2.interest_id
		 WHERE %s
		 GROUP BY s.id
		 ORDER BY s.created_at DESC, s.id DESC`,
		strings.Join(where, " AND "))

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("subscribers: streaming export query: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var row ExportRow
		if err := rows.Scan(
			&row.Email, &row.Status, &row.ConfirmedAt, &row.CreatedAt,
			&row.UTMSource, &row.UTMMedium, &row.UTMCampaign, &row.Interests,
		); err != nil {
			return fmt.Errorf("subscribers: scanning export row: %w", err)
		}
		if err := fn(row); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("subscribers: iterating export rows: %w", err)
	}
	return nil
}
