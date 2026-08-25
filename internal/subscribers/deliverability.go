// deliverability.go backs #0124's GET /admin/deliverability (PRD §6.9): the
// admin-facing list of addresses with bounce activity, sorted by streak
// then recency. It reads subscribers directly — soft_bounce_streak,
// last_bounce_at, last_delivery_at all live on that row already (migration
// 000010) — plus a per-address EXISTS against suppressions so the screen
// can show "still suppressed?" without a second round trip per row.
//
// This is scoped to addresses that HAVE a subscribers row. An address that
// hard-bounced or was suppressed and then had its subscriber row erased
// (#0060) still has a suppressions row and an email_events history, but no
// longer has anything to join a streak against — it is visible on
// /admin/suppressions (#0100) instead, which is exactly the list that
// survives erasure by design. Splitting "the streak-bearing view" from "the
// suppression-list view" this way avoids duplicating #0100's screen for no
// new information: an erased address has no streak left to show.
package subscribers

import (
	"context"
	"fmt"
	"time"
)

// BounceActivityItem is one row of the deliverability list.
type BounceActivityItem struct {
	SubscriberID       int64
	Email              string
	Status             string
	SoftBounceStreak   int
	LastBounceAt       *time.Time
	LastDeliveryAt     *time.Time
	Suppressed         bool
	SuppressionReasons []string
}

// ListBounceActivity returns every non-synthetic subscriber with bounce
// activity — a nonzero streak, a hard-bounced status, or a recorded
// last_bounce_at (a streak that has since been reset to 0 by a Delivery
// event, but whose history is still worth an admin seeing) — sorted by
// streak descending, then most-recently-bounced first, matching this
// issue's acceptance criterion verbatim ("sorted by streak then recency").
func (s *Store) ListBounceActivity(ctx context.Context) ([]BounceActivityItem, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT sub.id, sub.email, sub.status, sub.soft_bounce_streak,
		        sub.last_bounce_at, sub.last_delivery_at,
		        EXISTS (SELECT 1 FROM suppressions supp WHERE supp.email = sub.email) AS suppressed,
		        COALESCE(
		          (SELECT array_agg(supp.reason ORDER BY supp.reason) FROM suppressions supp WHERE supp.email = sub.email),
		          '{}'
		        ) AS reasons
		   FROM subscribers sub
		  WHERE sub.synthetic = FALSE
		    AND (sub.soft_bounce_streak > 0 OR sub.status = $1 OR sub.last_bounce_at IS NOT NULL)
		  ORDER BY sub.soft_bounce_streak DESC, sub.last_bounce_at DESC NULLS LAST, sub.email ASC`,
		StatusBounced,
	)
	if err != nil {
		return nil, fmt.Errorf("subscribers: listing bounce activity: %w", err)
	}
	defer rows.Close()

	var out []BounceActivityItem
	for rows.Next() {
		var item BounceActivityItem
		if err := rows.Scan(
			&item.SubscriberID, &item.Email, &item.Status, &item.SoftBounceStreak,
			&item.LastBounceAt, &item.LastDeliveryAt, &item.Suppressed, &item.SuppressionReasons,
		); err != nil {
			return nil, fmt.Errorf("subscribers: scanning bounce activity row: %w", err)
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("subscribers: iterating bounce activity rows: %w", err)
	}
	return out, nil
}
