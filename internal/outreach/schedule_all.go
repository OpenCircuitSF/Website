package outreach

import "time"

// ScheduleItem is one post PlanScheduleAll would schedule.
type ScheduleItem struct {
	PostID      int64
	Channel     string
	Wave        string
	ScheduledAt time.Time
}

// ScheduleSkip is one automatic slot PlanScheduleAll would not schedule, with
// the reason: not_drafted, not_connected, no_link, no_suggested_time,
// time_passed, too_far or invalid.
type ScheduleSkip struct {
	PostID      *int64 // nil for a slot with no post
	Channel     string
	Wave        string
	Reason      string
	SuggestedAt *time.Time
}

// PlanScheduleAll decides, for every automatic channel x wave, whether its
// post would be scheduled at the wave's suggested time. Manual channels are
// never listed. Posts whose status is not "draft" (scheduled, queued, posted,
// failed) are not listed either: they are not unscheduled, unposted drafts.
func PlanScheduleAll(posts []Post, startsAt *time.Time, now time.Time,
	minLead, maxLead time.Duration, postHour int, configured func(channel string) bool) ([]ScheduleItem, []ScheduleSkip) {
	bySlot := map[[2]string]Post{}
	for _, p := range posts {
		bySlot[[2]string{p.Channel, p.Wave}] = p
	}
	waveDrafted := map[string]bool{}
	for _, p := range posts {
		waveDrafted[p.Wave] = true
	}
	var items []ScheduleItem
	var skips []ScheduleSkip
	for _, ch := range Channels() {
		if !ch.Automatic {
			continue
		}
		for _, wv := range Waves() {
			p, ok := bySlot[[2]string{ch.Name, wv.Name}]
			if !ok {
				// Reminder and Last call are opt-in (#0574): a wave nobody
				// drafted is not a gap worth listing.
				if wv.DaysBefore > 0 && !waveDrafted[wv.Name] {
					continue
				}
				skips = append(skips, ScheduleSkip{Channel: ch.Name, Wave: wv.Name, Reason: "not_drafted"})
				continue
			}
			if p.Status != "draft" {
				continue
			}
			at := SuggestedAt(wv, startsAt, postHour)
			skip := func(reason string) {
				id := p.ID
				skips = append(skips, ScheduleSkip{PostID: &id, Channel: ch.Name, Wave: wv.Name, Reason: reason, SuggestedAt: at})
			}
			switch {
			case !configured(ch.Name):
				skip("not_connected")
			case !p.Prepared():
				skip("no_link")
			case at == nil:
				skip("no_suggested_time")
			case at.Before(now.Add(minLead)):
				skip("time_passed")
			case at.After(now.Add(maxLead)):
				skip("too_far")
			case ValidateForSend(ch, p.Body) != nil:
				skip("invalid")
			default:
				items = append(items, ScheduleItem{PostID: p.ID, Channel: ch.Name, Wave: wv.Name, ScheduledAt: at.UTC()})
			}
		}
	}
	return items, skips
}
