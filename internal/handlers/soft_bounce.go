// soft_bounce.go is #0039's shared threshold logic (corrected by #0124 to
// describe a consecutive STREAK, not a rolling window): the settings key,
// its PRD-specified default, and the code that resolves it, used by both
// ses_notifications.go (to decide whether a Transient or Undetermined
// bounce — #0109 widened the covered types — crosses the threshold) and
// admin_subscribers.go (to show the current threshold alongside the streak
// on the subscriber detail screen, #0039's admin-visibility criterion).
// Keeping it in one file means the two call sites can never silently
// disagree on what "the threshold" means.
//
// #0124 retired soft_bounce_threshold_window_days entirely: the repeated-
// soft-bounce rule is now subscribers.soft_bounce_streak, a consecutive
// count reset by a Delivery event, not a count re-queried from email_events
// within a rolling window. See issues/0124.md's Notes and PRD §6.9.
package handlers

import (
	"context"
	"log/slog"
	"strconv"
)

const (
	// settingSoftBounceThresholdCount is the settings key #0039's
	// acceptance criteria require ("threshold values live in settings, not
	// hardcoded"). Seeded by migrations/000015 and an ordinary PATCH-able
	// settings key — see settings.go's validSettingValue for the
	// positive-integer guard on PATCH.
	settingSoftBounceThresholdCount = "soft_bounce_threshold_count"

	// defaultSoftBounceThresholdCount mirrors PRD §6.9's streak default
	// (5). Used only as a fallback — when the settings row is missing or
	// holds a value that no longer parses as a positive integer — since
	// migrations/000015 seeds the row with this exact value and
	// validSettingValue refuses a PATCH that would make it invalid. A
	// config hiccup here must never break SES event ingestion or the admin
	// detail view, so this path degrades to the default rather than
	// erroring.
	defaultSoftBounceThresholdCount = 5
)

// softBounceSettingsReader is the narrow settings dependency #0039 needs.
// GetSetting(ctx, key) takes no querier parameter, so — unlike the
// events/subs/suppr stores ses_notifications.go holds concretely (see that
// file's package doc comment) — this CAN be a genuine narrow interface per
// CLAUDE.md §1. *auth.Store satisfies it via its existing GetSetting method;
// no change to internal/auth was needed. Settings reads are deliberately NOT
// transaction-scoped: unlike a subscriber's own streak column (which the
// transaction just incremented and must see fresh), the threshold
// configuration has no staleness relationship to the write this request is
// making.
type softBounceSettingsReader interface {
	GetSetting(ctx context.Context, key string) (string, error)
}

// softBounceThreshold resolves the configured streak threshold. log may be
// nil (a fallback is silently applied instead of logged) to match this
// package's existing nil-tolerance convention.
func softBounceThreshold(ctx context.Context, reader softBounceSettingsReader, log *slog.Logger) (count int) {
	return parsePositiveIntSetting(ctx, reader, settingSoftBounceThresholdCount, defaultSoftBounceThresholdCount, log)
}

// parsePositiveIntSetting reads key and parses it as a positive integer,
// returning fallback (and logging a warning, if log is non-nil) when the row
// is missing or its value isn't a positive integer.
func parsePositiveIntSetting(ctx context.Context, reader softBounceSettingsReader, key string, fallback int, log *slog.Logger) int {
	value, err := reader.GetSetting(ctx, key)
	if err != nil {
		if log != nil {
			log.Warn("handlers: soft-bounce setting missing, using default", "key", key, "default", fallback, "err", err)
		}
		return fallback
	}
	n, err := strconv.Atoi(value)
	if err != nil || n <= 0 {
		if log != nil {
			log.Warn("handlers: soft-bounce setting has an invalid value, using default", "key", key, "value", value, "default", fallback)
		}
		return fallback
	}
	return n
}
