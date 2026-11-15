package subscribers

import (
	"context"
	"fmt"
	"time"
)

// GuideSignups is one row of SignupsByGuide (#0662): signups started from a
// guide page's form, per guide and campaign post, in a window.
type GuideSignups struct {
	Guide       string // guide slug (internal/guides)
	UTMCampaign string // "" when the visit carried none
	UTMContent  string // "gp{id}" for a #0658 guide-post link; "" otherwise
	Requested   int    // distinct subscribers with a signup_requested event in [from, to)
	Confirmed   int    // of those, how many confirmed after that request and before their next one (#0664)
}

// SignupsByGuide counts signups attributed to a guide page (#0662) whose
// signup_requested event falls in [from, to), grouped by guide, utm_campaign
// and utm_content. The attribution lives in the event's detail
// (signupEventDetail), which is written only where a real signup starts, so
// bots, suppressed addresses and ordinary resends never count. An erased
// subscriber's events have a NULL subscriber_id and drop out of the counts by
// design. A request is credited only with a confirmation that precedes the
// subscriber's next signup_requested event (#0664), so a later restart from
// another form does not hand its confirmation back to the guide.
func (s *Store) SignupsByGuide(ctx context.Context, from, to time.Time) ([]GuideSignups, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT e.detail->>'guide',
		       coalesce(e.detail->>'utm_campaign', ''),
		       coalesce(e.detail->>'utm_content', ''),
		       count(DISTINCT e.subscriber_id),
		       count(DISTINCT e.subscriber_id) FILTER (WHERE EXISTS (
		           SELECT 1 FROM subscriber_events c
		            WHERE c.subscriber_id = e.subscriber_id
		              AND c.action = $4
		              AND c.created_at >= e.created_at
		              AND NOT EXISTS (
		                  SELECT 1 FROM subscriber_events n
		                   WHERE n.subscriber_id = e.subscriber_id
		                     AND n.action = $3
		                     AND n.created_at > e.created_at
		                     AND n.created_at <= c.created_at)))
		  FROM subscriber_events e
		 WHERE e.action = $3
		   AND e.detail->>'guide' IS NOT NULL
		   AND e.created_at >= $1 AND e.created_at < $2
		 GROUP BY 1, 2, 3
		 ORDER BY 1, 2, 3`, from, to, string(ActionSignupRequested), string(ActionConfirmed))
	if err != nil {
		return nil, fmt.Errorf("subscribers: signups by guide: %w", err)
	}
	defer rows.Close()
	var out []GuideSignups
	for rows.Next() {
		var g GuideSignups
		if err := rows.Scan(&g.Guide, &g.UTMCampaign, &g.UTMContent, &g.Requested, &g.Confirmed); err != nil {
			return nil, fmt.Errorf("subscribers: scanning signups by guide: %w", err)
		}
		out = append(out, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("subscribers: signups by guide: %w", err)
	}
	return out, nil
}
