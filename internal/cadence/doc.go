// Package cadence holds the event cadence rules (#0678,
// migrations/000055_create_event_cadence_rules) and the pure coverage
// calculation that answers "are we missing any events next month?".
//
// A rule says what a normal month looks like: "every Tuesday" (weekly on a
// weekday) or "2 a month" (N per month). It matches events by event type, by
// a case-insensitive title prefix, or by both, optionally narrowed to one
// venue. Everything is stated in America/Los_Angeles: month bounds, weekdays
// and event dates.
//
// rule.go is the rule and its validation, store.go the event_cadence_rules
// table, and coverage.go the calculation, which reads no database, clock or
// network.
package cadence
